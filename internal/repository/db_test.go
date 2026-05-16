package repository

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
