package repository

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// OpenDB opens the SQLite database at path, enables foreign keys, sets the
// busy_timeout pragma (0 = skip), and runs any pending schema migrations.
func OpenDB(path string, busyTimeout int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Single connection ensures pragmas set below remain in effect for all queries.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	if busyTimeout > 0 {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA busy_timeout = %d", busyTimeout)); err != nil {
			db.Close()
			return nil, fmt.Errorf("set busy_timeout: %w", err)
		}
	}

	if err := InitSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

// InitSchema applies pending schema migrations using PRAGMA user_version.
// Each if-block is independent so multiple migrations can apply in one startup.
func InitSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	if version < 1 {
		// WAL mode must be set before schema initialization and outside a transaction.
		if _, err := db.Exec("PRAGMA journal_mode = WAL"); err != nil {
			return fmt.Errorf("set WAL mode: %w", err)
		}

		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin migration to v1: %w", err)
		}
		defer tx.Rollback()

		for _, stmt := range schemaV1 {
			if _, err := tx.Exec(stmt); err != nil {
				preview := stmt
				if len(preview) > 60 {
					preview = preview[:60]
				}
				return fmt.Errorf("schema v1 %q: %w", preview, err)
			}
		}

		// Best-effort atomicity: PRAGMA user_version inside a transaction is
		// committed together with the DDL on most SQLite versions.
		if _, err := tx.Exec("PRAGMA user_version = 1"); err != nil {
			return fmt.Errorf("set user_version = 1: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration to v1: %w", err)
		}
	}

	return nil
}

// CreateDataDir creates the data directory with mode 0700 and sets the
// database file permissions to 0600 after creation (init mode only).
func CreateDataDir(dbPath string) error {
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// Chmod is a no-op if the file doesn't exist yet; caller must re-apply after
	// sql.Open creates the file.
	return nil
}

// CheckDBExists returns an error if the database file at path does not exist.
// Server, LDA, and import modes call this before opening the database.
func CheckDBExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("database %q does not exist; run 'mymail -init' first", path)
	} else if err != nil {
		return fmt.Errorf("stat database: %w", err)
	}
	return nil
}

// schemaV1 contains every DDL statement for the initial schema (version 0 → 1).
// All statements use IF NOT EXISTS so the migration is safe to re-run after a
// partial-commit interruption (e.g. if CREATE VIRTUAL TABLE auto-commits).
// Tables are ordered by dependency: folders and identities before messages,
// messages before attachments, attachments before its triggers.
var schemaV1 = []string{
	`CREATE TABLE IF NOT EXISTS folders (
		id         INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL UNIQUE,
		slug       TEXT    NOT NULL UNIQUE,
		position   INTEGER NOT NULL DEFAULT 0,
		created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	)`,

	`CREATE TABLE IF NOT EXISTS identities (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		address    TEXT    NOT NULL UNIQUE,
		is_default INTEGER NOT NULL DEFAULT 0,
		position   INTEGER NOT NULL DEFAULT 0,
		signature  TEXT    NOT NULL DEFAULT ''
	)`,

	`CREATE TABLE IF NOT EXISTS messages (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		folder_id           INTEGER NOT NULL REFERENCES folders(id),
		identity_id         INTEGER REFERENCES identities(id) ON DELETE SET NULL,
		message_id          TEXT    UNIQUE,
		in_reply_to         TEXT,
		"references"        TEXT,
		from_addr           TEXT    NOT NULL DEFAULT '',
		to_addr             TEXT    NOT NULL DEFAULT '',
		cc_addr             TEXT    NOT NULL DEFAULT '',
		bcc_addr            TEXT    NOT NULL DEFAULT '',
		reply_to_addr       TEXT    NOT NULL DEFAULT '',
		subject             TEXT    NOT NULL DEFAULT '',
		date                TEXT    NOT NULL,
		body_text           TEXT    NOT NULL DEFAULT '',
		body_html           TEXT    NOT NULL DEFAULT '',
		raw                 BLOB,
		read                INTEGER NOT NULL DEFAULT 0,
		flagged             INTEGER NOT NULL DEFAULT 0,
		has_attachments     INTEGER NOT NULL DEFAULT 0,
		has_external_images INTEGER NOT NULL DEFAULT 0,
		send_at             TEXT,
		snoozed_until       TEXT,
		snooze_folder       INTEGER REFERENCES folders(id) ON DELETE SET NULL,
		send_error          TEXT,
		send_failure_count  INTEGER NOT NULL DEFAULT 0,
		created_at          TEXT    NOT NULL,
		updated_at          TEXT    NOT NULL
	)`,

	`CREATE INDEX IF NOT EXISTS idx_messages_folder_id    ON messages(folder_id)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_date         ON messages(date)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_message_id   ON messages(message_id)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_read         ON messages(read)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_send_at      ON messages(send_at) WHERE send_at IS NOT NULL`,
	`CREATE INDEX IF NOT EXISTS idx_messages_snoozed_until ON messages(snoozed_until) WHERE snoozed_until IS NOT NULL`,

	`CREATE TRIGGER IF NOT EXISTS messages_updated_at
		AFTER UPDATE ON messages
		WHEN new.updated_at = old.updated_at
	BEGIN
		UPDATE messages SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = new.id;
	END`,

	`CREATE TABLE IF NOT EXISTS attachments (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		filename     TEXT    NOT NULL,
		content_type TEXT    NOT NULL,
		size         INTEGER NOT NULL,
		data         BLOB    NOT NULL
	)`,

	`CREATE INDEX IF NOT EXISTS idx_attachments_message_id ON attachments(message_id)`,

	`CREATE TRIGGER IF NOT EXISTS attachments_insert_flag
		AFTER INSERT ON attachments
	BEGIN
		UPDATE messages SET has_attachments = 1 WHERE id = new.message_id;
	END`,

	`CREATE TRIGGER IF NOT EXISTS attachments_delete_flag
		AFTER DELETE ON attachments
	BEGIN
		UPDATE messages SET has_attachments = (
			SELECT CASE WHEN EXISTS (SELECT 1 FROM attachments WHERE message_id = old.message_id) THEN 1 ELSE 0 END
		) WHERE id = old.message_id;
	END`,

	`CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
		from_addr,
		to_addr,
		cc_addr,
		subject,
		body_text,
		content='messages',
		content_rowid='id'
	)`,

	`CREATE TRIGGER IF NOT EXISTS messages_fts_insert
		AFTER INSERT ON messages
	BEGIN
		INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
	END`,

	`CREATE TRIGGER IF NOT EXISTS messages_fts_delete
		AFTER DELETE ON messages
	BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
	END`,

	`CREATE TRIGGER IF NOT EXISTS messages_fts_update
		AFTER UPDATE OF from_addr, to_addr, cc_addr, subject, body_text ON messages
	BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
		INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
	END`,

	`CREATE TABLE IF NOT EXISTS contacts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		address    TEXT    NOT NULL UNIQUE,
		name       TEXT    NOT NULL DEFAULT '',
		created_at TEXT    NOT NULL,
		updated_at TEXT    NOT NULL
	)`,

	`CREATE INDEX IF NOT EXISTS idx_contacts_address ON contacts(address)`,

	`CREATE TABLE IF NOT EXISTS filters (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		position      INTEGER NOT NULL DEFAULT 0,
		name          TEXT    NOT NULL DEFAULT '',
		match_from    TEXT    NOT NULL DEFAULT '',
		match_to      TEXT    NOT NULL DEFAULT '',
		match_subject TEXT    NOT NULL DEFAULT '',
		action        TEXT    NOT NULL,
		folder_id     INTEGER REFERENCES folders(id) ON DELETE SET NULL,
		stop          INTEGER NOT NULL DEFAULT 1
	)`,

	`CREATE TABLE IF NOT EXISTS spam_filter_settings (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		enabled         INTEGER NOT NULL DEFAULT 1,
		score_header    TEXT    NOT NULL DEFAULT 'X-Spam-Score',
		score_threshold REAL    NOT NULL DEFAULT 5.0
	)`,

	// OR IGNORE makes concurrent first-run inserts race-safe.
	`INSERT OR IGNORE INTO spam_filter_settings (id, enabled, score_header, score_threshold)
		VALUES (1, 1, 'X-Spam-Score', 5.0)`,
}
