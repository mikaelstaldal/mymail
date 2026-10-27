package repository

import (
	"context"
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

	// Schema version must match the latest migration.
	var v int
	db.QueryRow("PRAGMA user_version").Scan(&v)
	assert.Equal(t, 15, v, "user_version")

	// All tables must exist.
	tables := []string{
		"folders", "messages", "attachments", "identities",
		"blocked_senders", "contacts", "filters", "spam_filter_settings", "message_references", "api_tokens", "api_token_folders",
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
	assert.Equal(t, 15, v, "user_version after second run")

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

func TestAPITokenSlugSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mymail.sqlite")
	db, err := sqlite.Open(path, 0, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.MigrateStrict(context.Background(), db, migrations[:4]))
	require.NoError(t, InitSchema(db))
	insert := func(slug string, hash byte) error {
		_, err := db.Exec(`INSERT INTO api_tokens(name,slug,token_hash,created_at,expires_at) VALUES('Reader',?,?,'2024','2025')`, slug, []byte{hash})
		return err
	}
	require.NoError(t, insert("reader", 1))
	require.Error(t, insert("reader", 2), "slugs must be unique")
	require.Error(t, insert("", 3), "slugs must be nonempty")
	_, err = db.Exec(`INSERT INTO api_tokens(name,token_hash,created_at,expires_at) VALUES('bad',X'01','2024','2025')`)
	require.Error(t, err, "tokens require a slug")
	_, err = db.Exec(`DELETE FROM api_tokens WHERE slug='reader'`)
	require.NoError(t, err)
	require.NoError(t, insert("reader", 4), "revocation frees a slug")
}

func TestAPITokenMigrationPreservesGrantsAndDoesNotReuseIDs(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "mymail.sqlite"), 0, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.MigrateStrict(context.Background(), db, migrations[:5]))
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_tokens(id,name,slug,token_hash,created_at,expires_at)
		VALUES(42,'Reader','reader',X'01','2024','2025')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO api_token_folders(token_id,folder_id) VALUES(42,1)`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	var folderID int64
	require.NoError(t, db.QueryRow(`SELECT folder_id FROM api_token_folders WHERE token_id=42`).Scan(&folderID))
	assert.Equal(t, int64(1), folderID)
	_, err = db.Exec(`DELETE FROM api_tokens WHERE id=42`)
	require.NoError(t, err)
	var grants int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM api_token_folders WHERE token_id=42`).Scan(&grants))
	assert.Zero(t, grants, "revocation still cascades to grants")
	res, err := db.Exec(`INSERT INTO api_tokens(name,slug,token_hash,created_at,expires_at)
		VALUES('Replacement','replacement',X'02','2024','2025')`)
	require.NoError(t, err)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	assert.Greater(t, id, int64(42))
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

func TestAuthenticationMigrationPreservesOldMail(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "upgrade.sqlite"), 0, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.MigrateStrict(context.Background(), db, migrations[:7]))
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,date,raw,created_at,updated_at) VALUES(42,1,'2024-01-01T00:00:00Z',X'1234','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	var verdict, host string
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT raw,sender_authentication FROM messages WHERE id=42`).Scan(&raw, &verdict))
	assert.Equal(t, []byte{0x12, 0x34}, raw)
	assert.JSONEq(t, `{"dmarc":"unknown","dkim_aligned":false,"spf":"unknown"}`, verdict)
	require.NoError(t, db.QueryRow(`SELECT trusted_checker_host FROM spam_filter_settings`).Scan(&host))
	assert.Empty(t, host)
}

func TestAuthenticationGenerationMigrationPreservesEvidenceWithoutRewritingMail(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "generation-upgrade.sqlite"), 0, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, sqlite.MigrateStrict(context.Background(), db, migrations[:8]))
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host='checker';
 INSERT INTO messages(id,folder_id,date,raw,sender_authentication,created_at,updated_at)
 VALUES(42,1,'2024-01-01T00:00:00Z',X'1234','{"dmarc":"pass","dkim_aligned":true,"spf":"pass"}','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z');
 CREATE TRIGGER forbid_migration_mail_write BEFORE UPDATE ON messages BEGIN SELECT RAISE(ABORT,'migration must not rewrite mail'); END`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	msg, err := NewMessageRepository(db).GetMessageDetail(context.Background(), 42)
	require.NoError(t, err)
	assert.Equal(t, "pass", string(msg.ToOASMessage(nil).SenderAuthentication.Dmarc))
	var generation int64
	require.NoError(t, db.QueryRow(`SELECT auth_generation FROM messages WHERE id=42`).Scan(&generation))
	assert.Zero(t, generation)
	require.NoError(t, db.QueryRow(`SELECT auth_generation FROM spam_filter_settings WHERE id=1`).Scan(&generation))
	assert.Zero(t, generation)
}

func TestSelfSendMigrationPreservesMail(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "upgrade.sqlite"), 0, migrations[:11])
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,message_id,subject,date,created_at,updated_at,raw)
		VALUES(10,2,'self@example.com','migrationneedle','2026-01-01','2026-01-01','2026-01-01',x'0102');
		INSERT INTO attachments(id,message_id,filename,content_type,size,data) VALUES(12,10,'a','text/plain',2,x'0304');
		INSERT INTO message_references VALUES(10,'parent@example.com');
		UPDATE sqlite_sequence SET seq=100 WHERE name IN ('messages','attachments')`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	var outgoing, attachments, refs, hits, seq int
	require.NoError(t, db.QueryRow(`SELECT outgoing FROM messages WHERE id=10 AND raw=x'0102' AND has_attachments=1`).Scan(&outgoing))
	assert.Equal(t, 1, outgoing)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id=12 AND message_id=10 AND data=x'0304'`).Scan(&attachments))
	assert.Equal(t, 1, attachments)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM message_references WHERE message_id=10`).Scan(&refs))
	assert.Equal(t, 1, refs)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM messages_fts WHERE messages_fts MATCH 'migrationneedle'`).Scan(&hits))
	assert.Equal(t, 1, hits)
	for _, name := range []string{"messages", "attachments"} {
		require.NoError(t, db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name=?`, name).Scan(&seq))
		assert.Equal(t, 100, seq)
	}
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	assert.False(t, rows.Next())
	require.NoError(t, rows.Close())
	_, err = db.Exec(`INSERT INTO messages(folder_id,message_id,date,created_at,updated_at) VALUES(1,'self@example.com','2026-01-01','2026-01-01','2026-01-01')`)
	require.NoError(t, err, "receipt can coexist with migrated Sent copy")
	_, err = db.Exec(`DELETE FROM messages WHERE id=10`)
	require.NoError(t, err)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM attachments`).Scan(&attachments))
	assert.Zero(t, attachments, "foreign-key cascade is preserved")
}

func TestSelfSendMigrationClassifiesProvenance(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "old.sqlite"), 0, migrations[:11])
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO identities(id,name,address) VALUES(1,'Me','me@example.com');
		INSERT INTO folders(id,name,slug) VALUES(100,'Archive','archive')`)
	require.NoError(t, err)
	cases := []struct {
		name     string
		folder   int
		identity any
		raw      any
		want     int
	}{
		{"sent", 2, nil, []byte("sent"), 1},
		{"sent moved to trash", 4, 1, []byte("sent"), 1},
		{"sent moved to archive", 100, 1, []byte("sent"), 1},
		{"received", 1, nil, []byte("received"), 0},
		{"draft", 3, 1, nil, 0},
		{"scheduled", 5, 1, nil, 0},
		{"discarded draft", 4, 1, nil, 0},
	}
	for _, tc := range cases {
		_, err := db.Exec(`INSERT INTO messages(folder_id,identity_id,subject,raw,date,created_at,updated_at)
			VALUES(?,?,?,?, '2026-01-01','2026-01-01','2026-01-01')`, tc.folder, tc.identity, tc.name, tc.raw)
		require.NoError(t, err)
	}
	require.NoError(t, InitSchema(db))
	for _, tc := range cases {
		var outgoing int
		require.NoError(t, db.QueryRow(`SELECT outgoing FROM messages WHERE subject=?`, tc.name).Scan(&outgoing))
		assert.Equal(t, tc.want, outgoing, tc.name)
	}
}

func TestOutOfRangeDateMigrationRepairsListings(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "date-upgrade.sqlite"), 0, migrations[:12])
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,subject,date,created_at,updated_at) VALUES
		(1,1,'good','2024-01-01T10:00:00Z','2024-01-01T10:00:05Z','2024-01-01T10:00:05Z'),
		(2,1,'late','10000-01-01T23:58:00Z','2024-02-01T00:00:00Z','2024-02-01T00:00:00Z'),
		(3,1,'early','-0001-12-31T23:00:00Z','2024-03-01T00:00:00Z','2024-03-01T00:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))

	dates := map[int64]string{}
	rows, err := db.Query(`SELECT id, date FROM messages`)
	require.NoError(t, err)
	for rows.Next() {
		var id int64
		var date string
		require.NoError(t, rows.Scan(&id, &date))
		dates[id] = date
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	assert.Equal(t, map[int64]string{
		1: "2024-01-01T10:00:00Z",
		2: "2024-02-01T00:00:00Z",
		3: "2024-03-01T00:00:00Z",
	}, dates, "only unparseable dates fall back to delivery time")
}

func TestContentCollisionMigrationPreservesMovedProvenance(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "collision-upgrade.sqlite"), 0, migrations[:13])
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,message_id,subject,date,created_at,updated_at,raw,outgoing)
 VALUES(10,4,'collision@example.com','movedsent','2026-01-01','2026-01-01','2026-01-01',x'0102',1),
 (11,2,'received@example.com','movedreceived','2026-01-01','2026-01-01','2026-01-01',x'0304',0);
 INSERT INTO attachments(id,message_id,filename,content_type,size,data) VALUES(12,10,'a','text/plain',2,x'0304');
 INSERT INTO message_references VALUES(10,'parent@example.com');
 UPDATE sqlite_sequence SET seq=100 WHERE name IN ('messages','attachments')`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	var outgoing, count, sequence int
	require.NoError(t, db.QueryRow(`SELECT outgoing FROM messages WHERE id=10 AND raw=x'0102'`).Scan(&outgoing))
	assert.Equal(t, 1, outgoing)
	require.NoError(t, db.QueryRow(`SELECT outgoing FROM messages WHERE id=11`).Scan(&outgoing))
	assert.Zero(t, outgoing)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM attachments WHERE id=12 AND message_id=10`).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM message_references WHERE message_id=10`).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages_fts WHERE messages_fts MATCH 'movedsent'`).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, db.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name='messages'`).Scan(&sequence))
	assert.Equal(t, 100, sequence)
	_, err = db.Exec(`INSERT INTO messages(folder_id,message_id,date,created_at,updated_at,raw,outgoing)
 VALUES(1,'collision@example.com','2026-01-01','2026-01-01','2026-01-01',x'0506',1)`)
	require.NoError(t, err, "distinct raw content must survive colliding IDs")
	_, err = db.Exec(`INSERT INTO messages(folder_id,message_id,date,created_at,updated_at,raw,outgoing)
 VALUES(1,'received@example.com','2026-01-01','2026-01-01','2026-01-01',x'0708',0)`)
	require.NoError(t, err, "a received collision must coexist with a v13 received copy")
	rows, err := db.Query(`PRAGMA foreign_key_check`)
	require.NoError(t, err)
	defer rows.Close()
	assert.False(t, rows.Next())
	require.NoError(t, rows.Err())
}
