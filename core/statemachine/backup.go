package statemachine

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Backup writes a consistent Bolt snapshot to path and atomically replaces any
// existing file at that path. The destination must be on the same filesystem
// as its parent directory for the final rename to be atomic.
func (sm *StateMachine) Backup(path string) (err error) {
	return BackupDatabase(sm.db, path)
}

// BackupDatabase writes a consistent Bolt snapshot to path and atomically
// replaces any existing file at that path.
func BackupDatabase(db *bolt.DB, path string) (err error) {
	if db == nil {
		return fmt.Errorf("database is nil")
	}
	if path == "" {
		return fmt.Errorf("backup path is empty")
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".frg-backup-*")
	if err != nil {
		return fmt.Errorf("create backup temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close backup temporary file: %w", err)
	}
	if err := db.View(func(btx *bolt.Tx) error {
		return btx.CopyFile(tmpPath, 0600)
	}); err != nil {
		return fmt.Errorf("copy database: %w", err)
	}
	file, err := os.OpenFile(tmpPath, os.O_RDWR, 0600)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync backup: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("install backup: %w", err)
	}
	return nil
}

// CreateBackup writes a timestamped snapshot and retains only the newest
// retain files matching the FRG backup naming convention.
func CreateBackup(db *bolt.DB, dir string, retain int) (string, error) {
	if retain < 1 {
		return "", fmt.Errorf("backup retention must be positive")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	name := "frg-" + time.Now().UTC().Format("20060102T150405.000000000Z") + ".db"
	path := filepath.Join(dir, name)
	if err := BackupDatabase(db, path); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("list backups: %w", err)
	}
	type backupEntry struct {
		name string
		when time.Time
	}
	backups := make([]backupEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "frg-") || !strings.HasSuffix(entry.Name(), ".db") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		backups = append(backups, backupEntry{name: entry.Name(), when: info.ModTime()})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].when.Equal(backups[j].when) {
			return backups[i].name > backups[j].name
		}
		return backups[i].when.After(backups[j].when)
	})
	for _, old := range backups[retain:] {
		if err := os.Remove(filepath.Join(dir, old.name)); err != nil {
			return "", fmt.Errorf("remove old backup %s: %w", old.name, err)
		}
	}
	return path, nil
}

// RestoreDatabase validates a backup and atomically installs it at destination.
// Destination must not already exist and must not be the live database path.
func RestoreDatabase(source, destination string) error {
	if source == "" || destination == "" {
		return fmt.Errorf("restore source and destination are required")
	}
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("restore destination already exists")
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check restore destination: %w", err)
	}
	backup, err := bolt.Open(source, 0600, &bolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	if err := backup.View(func(tx *bolt.Tx) error {
		if tx.Bucket(metaBucket) == nil || tx.Bucket(blocksBucket) == nil {
			return fmt.Errorf("backup is missing required buckets")
		}
		return nil
	}); err != nil {
		_ = backup.Close()
		return fmt.Errorf("validate backup: %w", err)
	}
	if err := backup.Close(); err != nil {
		return fmt.Errorf("close backup: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return fmt.Errorf("create restore directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".frg-restore-*")
	if err != nil {
		return fmt.Errorf("create restore temporary file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	input, err := os.Open(source)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("open restore source: %w", err)
	}
	if _, err := io.Copy(tmp, input); err != nil {
		_ = input.Close()
		_ = tmp.Close()
		return fmt.Errorf("copy restore source: %w", err)
	}
	if err := input.Close(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("close restore source: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync restored database: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close restored database: %w", err)
	}
	if err := os.Rename(tmpPath, destination); err != nil {
		return fmt.Errorf("install restored database: %w", err)
	}
	return nil
}
