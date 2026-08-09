package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/imattau/frg/core/statemachine"
	bolt "go.etcd.io/bbolt"
)

func main() {
	dbPath := flag.String("db", "frg.db", "live FRG database path")
	backupDir := flag.String("backup-dir", "backups", "backup directory")
	retain := flag.Int("retain", 7, "number of backups to retain")
	restoreFrom := flag.String("restore-from", "", "backup file to restore instead of creating a backup")
	restoreTo := flag.String("restore-to", "", "new database path for the restored backup")
	flag.Parse()

	if *restoreFrom != "" {
		if *restoreTo == "" {
			log.Fatal("-restore-to is required with -restore-from")
		}
		if err := statemachine.RestoreDatabase(*restoreFrom, *restoreTo); err != nil {
			log.Fatalf("restore database: %v", err)
		}
		fmt.Printf("restored %s to %s\n", *restoreFrom, *restoreTo)
		return
	}

	db, err := bolt.Open(filepath.Clean(*dbPath), 0600, nil)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	path, err := statemachine.CreateBackup(db, filepath.Clean(*backupDir), *retain)
	if err != nil {
		log.Fatalf("create backup: %v", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		log.Fatalf("secure backup: %v", err)
	}
	fmt.Println(path)
}
