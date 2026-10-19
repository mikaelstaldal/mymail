package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/mikaelstaldal/go-server-common/sqlite"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// runMigrations applies pending migrations to an existing database, vacuums it
// only if the schema advanced, and closes it. It does no application initialization.
func runMigrations(dataDir string) (err error) {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		return err
	}
	// Open without migrating so we can detect whether any migrations were needed.
	// Keep compaction out of repository.OpenDB and normal server startup.
	db, err := sqlite.Open(dbPath, 30000, nil)
	if err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close database: %w", closeErr))
		}
	}()
	var before, after int
	if err := db.QueryRow("PRAGMA user_version").Scan(&before); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if err := repository.InitSchema(db); err != nil {
		return fmt.Errorf("migrate database: %w", err)
	}
	if err := db.QueryRow("PRAGMA user_version").Scan(&after); err != nil {
		return fmt.Errorf("read migrated schema version: %w", err)
	}
	if after > before {
		// InitSchema has committed all migrations. VACUUM cannot run within a
		// transaction and its failure cannot roll back the completed migrations.
		if _, err := db.Exec("VACUUM"); err != nil {
			return fmt.Errorf("migrations succeeded but vacuum failed: %w", err)
		}
	}
	return nil
}
