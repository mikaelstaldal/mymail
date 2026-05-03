package repository

import (
	"database/sql"
	"fmt"
)

// SeedBuiltinFolders inserts the seven built-in folders (ids 1–7) in a single
// transaction. INSERT OR IGNORE makes the operation safe to re-run.
func SeedBuiltinFolders(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()

	folders := [7]struct {
		id       int
		name     string
		slug     string
		position int
	}{
		{1, "Inbox", "inbox", 0},
		{2, "Sent", "sent", 1},
		{3, "Drafts", "drafts", 2},
		{4, "Trash", "trash", 3},
		{5, "Scheduled", "scheduled", 4},
		{6, "Snoozed", "snoozed", 5},
		{7, "Junk", "junk", 6},
	}

	for _, f := range folders {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO folders (id, name, slug, position) VALUES (?, ?, ?, ?)`,
			f.id, f.name, f.slug, f.position,
		); err != nil {
			return fmt.Errorf("insert folder %q: %w", f.slug, err)
		}
	}

	return tx.Commit()
}

// SeedIdentity inserts the initial identity with is_default=1, position=0.
// INSERT OR IGNORE skips the insert when the address already exists.
func SeedIdentity(db *sql.DB, name, address string) error {
	_, err := db.Exec(
		`INSERT OR IGNORE INTO identities (name, address, is_default, position) VALUES (?, ?, 1, 0)`,
		name, address,
	)
	if err != nil {
		return fmt.Errorf("insert identity: %w", err)
	}
	return nil
}
