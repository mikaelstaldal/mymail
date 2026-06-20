package repository

import (
	"database/sql"
	"fmt"
	"os"

	"github.com/mikaelstaldal/go-server-common/sqlite"
)

// migrations lists the schema versions in order: migrations[0] takes the
// database from user_version 0 to 1, migrations[1] from 1 to 2, and so on.
// Append a new schemaVN slice here to add a migration.
var migrations = [][]string{
	schemaV1,
	schemaV2,
	schemaV3,
	schemaV4,
}

// OpenDB opens the SQLite database at path, enables foreign keys, sets the
// busy_timeout pragma (0 = skip), applies any extraPragmas, and runs pending
// schema migrations. Pragmas are baked into the DSN so every connection in
// the pool inherits them automatically.
func OpenDB(path string, busyTimeout int, extraPragmas ...string) (*sql.DB, error) {
	return sqlite.Open(path, busyTimeout, migrations, extraPragmas...)
}

// InitSchema applies pending schema migrations using PRAGMA user_version.
func InitSchema(db *sql.DB) error {
	return sqlite.Migrate(db, migrations)
}

// CreateDataDir creates the parent directory of dbPath with mode 0700.
func CreateDataDir(dbPath string) error {
	return sqlite.CreateDataDir(dbPath)
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
	`CREATE INDEX IF NOT EXISTS idx_messages_folder_date ON messages(folder_id, date DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_folder_read ON messages(folder_id, read)`,

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

// schemaV2 adds composite indexes that improve list-messages and list-folders query performance.
// Applied as a migration on existing v1 databases; also present in schemaV1 for fresh installs.
var schemaV2 = []string{
	`CREATE INDEX IF NOT EXISTS idx_messages_folder_date ON messages(folder_id, date DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_messages_folder_read ON messages(folder_id, read)`,
}

// schemaV3 adds the message_references join table for indexed thread forward lookups,
// replacing the full-table-scan LIKE '%…%' pattern on the references column.
var schemaV3 = []string{
	`CREATE TABLE IF NOT EXISTS message_references (
		message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		ref_msg_id TEXT    NOT NULL,
		UNIQUE (message_id, ref_msg_id)
	)`,
	`CREATE INDEX IF NOT EXISTS idx_msgref_ref ON message_references(ref_msg_id)`,
	// Backfill from existing data. Recursive CTE splits the newline-separated
	// references column one token at a time, avoiding JSON encoding issues.
	`WITH RECURSIVE split(message_id, ref, rest) AS (
		SELECT id,
		       CASE WHEN instr("references", char(10)) > 0
		            THEN substr("references", 1, instr("references", char(10)) - 1)
		            ELSE "references" END,
		       CASE WHEN instr("references", char(10)) > 0
		            THEN substr("references", instr("references", char(10)) + 1)
		            ELSE '' END
		FROM messages
		WHERE "references" IS NOT NULL AND "references" != ''
		UNION ALL
		SELECT message_id,
		       CASE WHEN instr(rest, char(10)) > 0
		            THEN substr(rest, 1, instr(rest, char(10)) - 1)
		            ELSE rest END,
		       CASE WHEN instr(rest, char(10)) > 0
		            THEN substr(rest, instr(rest, char(10)) + 1)
		            ELSE '' END
		FROM split WHERE rest != ''
	)
	INSERT OR IGNORE INTO message_references (message_id, ref_msg_id)
	SELECT message_id, ref FROM split WHERE ref != ''`,
}

// schemaV4 adds an index on in_reply_to to speed up the thread forward query,
// which previously caused a full table scan on every threading iteration.
var schemaV4 = []string{
	`CREATE INDEX IF NOT EXISTS idx_messages_in_reply_to ON messages(in_reply_to) WHERE in_reply_to IS NOT NULL`,
}
