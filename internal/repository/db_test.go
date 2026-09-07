package repository

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikaelstaldal/go-server-common/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSchemaSnapshot verifies that spec/schema.sql describes exactly the
// application-owned objects produced by migrating a new database. SQLite's
// internal tables and the shadow tables behind the FTS5 virtual table are
// implementation details and are deliberately omitted from the snapshot.
func TestSchemaSnapshot(t *testing.T) {
	db, err := OpenDB(filepath.Join(t.TempDir(), "mymail.sqlite"), 0)
	require.NoError(t, err, "OpenDB")
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	var version int
	require.NoError(t, db.QueryRow("PRAGMA user_version").Scan(&version))

	rows, err := db.Query(`
		SELECT s.sql
		FROM sqlite_schema AS s
		WHERE s.sql IS NOT NULL
		  AND s.name NOT LIKE 'sqlite_%'
		  AND NOT (s.type = 'table' AND EXISTS (
			SELECT 1 FROM sqlite_schema AS virtual
			WHERE virtual.sql LIKE 'CREATE VIRTUAL TABLE%'
			  AND s.name LIKE virtual.name || '\_%' ESCAPE '\'
		  ))
		ORDER BY s.type, s.name`)
	require.NoError(t, err)
	defer rows.Close()

	var ddl []string
	for rows.Next() {
		var statement string
		require.NoError(t, rows.Scan(&statement))
		ddl = append(ddl, statement+";")
	}
	require.NoError(t, rows.Err())

	actual := fmt.Sprintf("-- Application-owned DDL from a freshly migrated database; see AGENTS.md.\n-- This omits seed rows and is not a replacement for mymail -init.\nPRAGMA user_version = %d;\n\n%s\n", version, strings.Join(ddl, "\n\n"))
	snapshotPath := filepath.Join("..", "..", "spec", "schema.sql")
	if os.Getenv("MYMAIL_UPDATE_SCHEMA_SNAPSHOT") == "1" {
		require.NoError(t, os.WriteFile(snapshotPath, []byte(actual), 0o644))
	}
	want, err := os.ReadFile(snapshotPath)
	require.NoError(t, err)
	assert.Equal(t, string(want), actual, "schema changed; update spec/schema.sql in the same commit as the migration")
}

func TestOpenDBAndInitSchema(t *testing.T) {
	f, err := os.CreateTemp("", "mymail-*.sqlite")
	require.NoError(t, err)
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	db, err := OpenDB(path, 0)
	require.NoError(t, err, "OpenDB")
	defer db.Close()

	// Schema version must be 4.
	var v int
	db.QueryRow("PRAGMA user_version").Scan(&v)
	assert.Equal(t, 4, v, "user_version")

	// All tables must exist.
	tables := []string{
		"folders", "messages", "attachments", "identities",
		"contacts", "filters", "spam_filter_settings", "message_references",
	}
	for _, tbl := range tables {
		var name string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name)
		assert.NoError(t, err, "missing table: %s", tbl)
	}

	// FTS virtual table.
	var name string
	err = db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'",
	).Scan(&name)
	assert.NoError(t, err, "missing virtual table: messages_fts")

	// Triggers.
	triggers := []string{
		"messages_updated_at",
		"attachments_insert_flag", "attachments_delete_flag",
		"messages_fts_insert", "messages_fts_delete", "messages_fts_update",
	}
	for _, tr := range triggers {
		var n string
		err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", tr,
		).Scan(&n)
		assert.NoError(t, err, "missing trigger: %s", tr)
	}

	// spam_filter_settings seed row.
	var id int
	err = db.QueryRow("SELECT id FROM spam_filter_settings WHERE id=1").Scan(&id)
	assert.NoError(t, err, "missing spam_filter_settings seed row")

	// WAL mode.
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	assert.Equal(t, "wal", mode, "journal_mode")

	// Idempotency: second InitSchema must not fail.
	err = InitSchema(db)
	assert.NoError(t, err, "second InitSchema")
	db.QueryRow("PRAGMA user_version").Scan(&v)
	assert.Equal(t, 4, v, "user_version after second run")

	// Basic FK cascade: insert a message row then delete it; attachment should cascade.
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0)`)
	require.NoError(t, err, "insert folder")

	res, err := db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at)
		VALUES(1,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	require.NoError(t, err, "insert message")
	msgID, _ := res.LastInsertId()

	_, err = db.Exec(`INSERT INTO attachments(message_id,filename,content_type,size,data)
		VALUES(?,'test.txt','text/plain',4,'data')`, msgID)
	require.NoError(t, err, "insert attachment")

	// Delete message → attachment should cascade.
	_, err = db.Exec("DELETE FROM messages WHERE id=?", msgID)
	require.NoError(t, err, "delete message")

	var count int
	db.QueryRow("SELECT COUNT(*) FROM attachments WHERE message_id=?", msgID).Scan(&count)
	assert.Zero(t, count, "cascade delete failed: attachments remain")
}

// TestOpenDBRefusesNewerSchema pins the MigrateStrict guarantee: a database
// written by a build that knows more migrations than this one is refused, so an
// older binary never writes to a schema it does not understand.
func TestOpenDBRefusesNewerSchema(t *testing.T) {
	f, err := os.CreateTemp("", "mymail-*.sqlite")
	require.NoError(t, err)
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	db, err := OpenDB(path, 0)
	require.NoError(t, err, "OpenDB")
	_, err = db.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations)+1))
	require.NoError(t, err, "bump user_version past the last migration")
	require.NoError(t, db.Close())

	_, err = OpenDB(path, 0)
	require.Error(t, err, "OpenDB on a newer schema")
	assert.ErrorIs(t, err, sqlite.ErrSchemaTooNew)
	assert.Contains(t, err.Error(), "upgrade the binary")
}
