package statemachine

import (
	"fmt"
	"os"
	"path/filepath"

	bolt "go.etcd.io/bbolt"
)

// Backup writes a consistent Bolt snapshot to path and atomically replaces any
// existing file at that path. The destination must be on the same filesystem
// as its parent directory for the final rename to be atomic.
func (sm *StateMachine) Backup(path string) (err error) {
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
	if err := sm.db.View(func(btx *bolt.Tx) error {
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
