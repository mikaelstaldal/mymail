package main

import (
	"fmt"
	"log"
	"net/mail"
	"os"
	"path/filepath"

	"golang.org/x/text/cases"

	"github.com/mikaelstaldal/mymail/internal/repository"
)

// runInit implements the -init operating mode: creates the data directory,
// initialises the SQLite database, seeds built-in folders and the initial
// identity, then exits 0. Exits 1 on any error.
func runInit(dataDir, identityAddress, identityName string) {
	if identityAddress == "" {
		fmt.Fprintln(os.Stderr, "error: -identity-address is required")
		os.Exit(1)
	}

	addr, err := mail.ParseAddress(identityAddress)
	if err != nil || addr.Name != "" {
		fmt.Fprintf(os.Stderr, "error: -identity-address %q must be a bare addr-spec (e.g. user@example.com)\n", identityAddress)
		os.Exit(1)
	}

	dbPath := filepath.Join(dataDir, "mymail.sqlite")

	if err := repository.CreateDataDir(dbPath); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	db, err := repository.OpenDB(dbPath, 0)
	if err != nil {
		log.Printf("error: open database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := os.Chmod(dbPath, 0600); err != nil {
		log.Printf("error: set database permissions: %v", err)
		os.Exit(1)
	}

	if err := repository.SeedBuiltinFolders(db); err != nil {
		log.Printf("error: seed built-in folders: %v", err)
		os.Exit(1)
	}

	// spam_filter_settings is seeded inside InitSchema (schemaV1); no extra step needed.

	normalizedAddress := cases.Fold().String(addr.Address)

	if err := repository.SeedIdentity(db, identityName, normalizedAddress); err != nil {
		log.Printf("error: seed identity: %v", err)
		os.Exit(1)
	}
}
