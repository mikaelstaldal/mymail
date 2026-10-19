package main

import (
	"fmt"
	"path/filepath"

	"github.com/mikaelstaldal/mymail/internal/repository"
)

// runMigrations opens an existing database, applies pending migrations through
// OpenDB, and closes it before returning. It does no application initialization.
func runMigrations(dataDir string) error {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		return err
	}
	db, err := repository.OpenDB(dbPath, 30000)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}
