package repository

import (
	"fmt"
	"os"
	"testing"
)

func TestOpenDBAndInitSchema(t *testing.T) {
	f, err := os.CreateTemp("", "mymail-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	db, err := OpenDB(path, 0)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	defer db.Close()

	// Schema version must be 1.
	var v int
	db.QueryRow("PRAGMA user_version").Scan(&v)
	if v != 1 {
		t.Errorf("user_version = %d, want 1", v)
	}

	// All tables must exist.
	tables := []string{
		"folders", "messages", "attachments", "identities",
		"contacts", "filters", "spam_filter_settings",
	}
	for _, tbl := range tables {
		var name string
		if err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl,
		).Scan(&name); err != nil {
			t.Errorf("missing table: %s", tbl)
		}
	}

	// FTS virtual table.
	var name string
	if err := db.QueryRow(
		"SELECT name FROM sqlite_master WHERE type='table' AND name='messages_fts'",
	).Scan(&name); err != nil {
		t.Error("missing virtual table: messages_fts")
	}

	// Triggers.
	triggers := []string{
		"messages_updated_at",
		"attachments_insert_flag", "attachments_delete_flag",
		"messages_fts_insert", "messages_fts_delete", "messages_fts_update",
	}
	for _, tr := range triggers {
		var n string
		if err := db.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='trigger' AND name=?", tr,
		).Scan(&n); err != nil {
			t.Errorf("missing trigger: %s", tr)
		}
	}

	// spam_filter_settings seed row.
	var id int
	if err := db.QueryRow("SELECT id FROM spam_filter_settings WHERE id=1").Scan(&id); err != nil {
		t.Error("missing spam_filter_settings seed row")
	}

	// WAL mode.
	var mode string
	db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	// Idempotency: second InitSchema must not fail.
	if err := InitSchema(db); err != nil {
		t.Errorf("second InitSchema: %v", err)
	}
	db.QueryRow("PRAGMA user_version").Scan(&v)
	if v != 1 {
		t.Errorf("user_version after second run = %d, want 1", v)
	}

	// Basic FK cascade: insert a message row then delete it; attachment should cascade.
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0)`)
	if err != nil {
		t.Fatalf("insert folder: %v", err)
	}
	res, err := db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at)
		VALUES(1,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	if err != nil {
		t.Fatalf("insert message: %v", err)
	}
	msgID, _ := res.LastInsertId()

	_, err = db.Exec(`INSERT INTO attachments(message_id,filename,content_type,size,data)
		VALUES(?,'test.txt','text/plain',4,'data')`, msgID)
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	// Delete message → attachment should cascade.
	if _, err := db.Exec("DELETE FROM messages WHERE id=?", msgID); err != nil {
		t.Fatalf("delete message: %v", err)
	}
	var count int
	db.QueryRow("SELECT COUNT(*) FROM attachments WHERE message_id=?", msgID).Scan(&count)
	if count != 0 {
		t.Errorf("cascade delete failed: %d attachments remain", count)
	}

	fmt.Println("all schema checks passed")
}
