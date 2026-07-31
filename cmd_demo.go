package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mikaelstaldal/mymail/internal/demo"
	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// runDemo populates the database with mock messages for demonstration and
// manual testing. The database must already be initialised with -init.
// Each invocation adds a fresh set of messages (unique message IDs) so the
// command can be run multiple times to grow the dataset.
//
// The dataset itself lives in internal/demo, which also exports it as
// demo-data.json for the backend-less browser demo (-demo-server /
// -demo-bundle) — one definition, two demos.
func runDemo(dataDir string) {
	if err := seedDemoData(dataDir); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}
}

// seedDemoData is runDemo's body, split out so the lock file and the database
// are closed on every path. os.Exit does not run deferred functions, so the
// exit has to happen in the caller for these defers to mean anything.
func seedDemoData(dataDir string) error {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		return err
	}

	lockFile, err := lda.AcquireImportLock(dataDir)
	if err != nil {
		return err
	}
	defer lockFile.Close() //nolint:errcheck

	db, err := repository.OpenDB(dbPath, 5000)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close() //nolint:errcheck

	return demo.Run(context.Background(), db, os.Stdout)
}
