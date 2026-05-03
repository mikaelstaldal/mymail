package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/model"
)

// openTestDB opens an in-memory SQLite database with the full schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "mymail-msg-test-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	db, err := OpenDB(path, 0)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	// Seed built-in folders.
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES
		(1,'Inbox','inbox',0),(2,'Sent','sent',1),(3,'Drafts','drafts',2),
		(4,'Trash','trash',3),(5,'Scheduled','scheduled',4),
		(6,'Snoozed','snoozed',5),(7,'Junk','junk',6)`)
	if err != nil {
		t.Fatalf("seed folders: %v", err)
	}
	return db
}

func makeMsg(folderID int, subject string) model.DBMessage {
	now := time.Now().UTC().Truncate(time.Second)
	return model.DBMessage{
		FolderID:  folderID,
		FromAddr:  "sender@example.com",
		ToAddr:    "to@example.com",
		Subject:   subject,
		Date:      now,
		BodyText:  "hello world",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestInsertAndGetMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "Test subject")
	msg.MessageID = sql.NullString{String: "abc@test", Valid: true}
	msg.References = sql.NullString{String: "ref1@x\nref2@x", Valid: true}

	id, err := r.InsertMessage(ctx, msg)
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := r.GetMessage(ctx, id)
	if err != nil {
		t.Fatalf("GetMessage: %v", err)
	}
	if got.Subject != "Test subject" {
		t.Errorf("subject = %q", got.Subject)
	}
	if !got.MessageID.Valid || got.MessageID.String != "abc@test" {
		t.Errorf("message_id = %v", got.MessageID)
	}
	if !got.References.Valid || got.References.String != "ref1@x\nref2@x" {
		t.Errorf("references = %v", got.References)
	}
}

func TestGetMessageNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.GetMessage(ctx, 9999)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestListMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	for i := range 3 {
		msg := makeMsg(1, "msg")
		msg.Date = time.Now().UTC().Add(time.Duration(i) * time.Minute)
		if _, err := r.InsertMessage(ctx, msg); err != nil {
			t.Fatal(err)
		}
	}

	items, total, err := r.ListMessages(ctx, 1, 10, 0)
	if err != nil {
		t.Fatalf("ListMessages: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(items) != 3 {
		t.Errorf("items len = %d, want 3", len(items))
	}
	// Should be ordered by date DESC — newest first.
	if !items[0].Date.After(items[1].Date) {
		t.Error("expected descending date order")
	}
}

func TestUpdateMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, err := r.InsertMessage(ctx, makeMsg(1, "upd"))
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.UpdateMessage(ctx, id, map[string]any{"read": true, "flagged": true})
	if err != nil {
		t.Fatalf("UpdateMessage: %v", err)
	}
	if !got.Read {
		t.Error("expected read=true")
	}
	if !got.Flagged {
		t.Error("expected flagged=true")
	}
}

func TestUpdateMessageNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.UpdateMessage(ctx, 9999, map[string]any{"read": true})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestDeleteMessageToTrash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "del"))
	if err := r.DeleteMessage(ctx, id); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}
	got, _ := r.GetMessage(ctx, id)
	if got.FolderID != 4 {
		t.Errorf("folder_id = %d, want 4 (Trash)", got.FolderID)
	}
}

func TestDeleteMessagePermanentFromTrash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(4, "del-perm"))
	if err := r.DeleteMessage(ctx, id); err != nil {
		t.Fatalf("DeleteMessage from Trash: %v", err)
	}
	_, err := r.GetMessage(ctx, id)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound after permanent delete, got %v", err)
	}
}

func TestBulkDeleteMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id1, _ := r.InsertMessage(ctx, makeMsg(1, "a"))
	id2, _ := r.InsertMessage(ctx, makeMsg(4, "b")) // already in Trash → permanent

	if err := r.BulkDeleteMessages(ctx, []int64{id1, id2}); err != nil {
		t.Fatalf("BulkDeleteMessages: %v", err)
	}
	// id1 should have moved to Trash.
	got1, _ := r.GetMessage(ctx, id1)
	if got1.FolderID != 4 {
		t.Errorf("id1 folder_id = %d, want 4", got1.FolderID)
	}
	// id2 should be permanently deleted.
	if _, err := r.GetMessage(ctx, id2); err != ErrNotFound {
		t.Errorf("expected id2 gone, got %v", err)
	}
}

func TestBulkDeleteMissingID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "x"))
	err := r.BulkDeleteMessages(ctx, []int64{id, 99999})
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestBulkUpdateMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id1, _ := r.InsertMessage(ctx, makeMsg(1, "u1"))
	id2, _ := r.InsertMessage(ctx, makeMsg(1, "u2"))

	readTrue := true
	n, err := r.BulkUpdateMessages(ctx, []int64{id1, id2}, &readTrue, nil)
	if err != nil {
		t.Fatalf("BulkUpdateMessages: %v", err)
	}
	if n != 2 {
		t.Errorf("changed = %d, want 2", n)
	}
	got, _ := r.GetMessage(ctx, id1)
	if !got.Read {
		t.Error("expected read=true after bulk update")
	}
}

func TestMoveMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "move"))
	n, err := r.MoveMessages(ctx, []int64{id}, 4)
	if err != nil {
		t.Fatalf("MoveMessages: %v", err)
	}
	if n != 1 {
		t.Errorf("moved = %d, want 1", n)
	}
	got, _ := r.GetMessage(ctx, id)
	if got.FolderID != 4 {
		t.Errorf("folder_id = %d, want 4", got.FolderID)
	}
}

func TestMoveMessagesMissingID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.MoveMessages(ctx, []int64{99999}, 4)
	if err != ErrNotFound {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestGetRawMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "raw")
	msg.Raw = []byte("raw bytes")
	id, _ := r.InsertMessage(ctx, msg)

	raw, err := r.GetRawMessage(ctx, id)
	if err != nil {
		t.Fatalf("GetRawMessage: %v", err)
	}
	if string(raw) != "raw bytes" {
		t.Errorf("raw = %q", raw)
	}
}

func TestGetRawMessageNilForDraft(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(3, "draft"))
	raw, err := r.GetRawMessage(ctx, id)
	if err != nil {
		t.Fatalf("GetRawMessage for draft: %v", err)
	}
	if raw != nil {
		t.Errorf("expected nil raw for draft, got %q", raw)
	}
}

func TestSnoozeAndCancelSnooze(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "snooze"))
	until := time.Now().UTC().Add(5 * time.Minute)

	got, err := r.SnoozeMessage(ctx, id, until)
	if err != nil {
		t.Fatalf("SnoozeMessage: %v", err)
	}
	if got.FolderID != 6 {
		t.Errorf("folder_id after snooze = %d, want 6", got.FolderID)
	}
	if !got.SnoozedUntil.Valid {
		t.Error("expected snoozed_until to be set")
	}
	if !got.SnoozeFolder.Valid || got.SnoozeFolder.Int64 != 1 {
		t.Errorf("snooze_folder = %v, want 1", got.SnoozeFolder)
	}

	// Cancel snooze.
	got2, err := r.CancelSnooze(ctx, id)
	if err != nil {
		t.Fatalf("CancelSnooze: %v", err)
	}
	if got2.FolderID != 1 {
		t.Errorf("folder_id after cancel = %d, want 1", got2.FolderID)
	}
	if got2.SnoozedUntil.Valid {
		t.Error("expected snoozed_until cleared")
	}
}

func TestSnoozeTooSoon(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "soon"))
	_, err := r.SnoozeMessage(ctx, id, time.Now().UTC().Add(30*time.Second))
	if err == nil {
		t.Error("expected error for snooze < 60s")
	}
}

func TestSnoozeForbiddenFolder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(3, "draft"))
	_, err := r.SnoozeMessage(ctx, id, time.Now().UTC().Add(5*time.Minute))
	if err == nil {
		t.Error("expected error for snooze of draft")
	}
}

func TestCancelSnoozeNotSnoozed(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "not-snoozed"))
	_, err := r.CancelSnooze(ctx, id)
	if err == nil {
		t.Error("expected error cancelling snooze on non-snoozed message")
	}
}

func TestMarkJunkAndNotJunk(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "junk test"))

	got, err := r.MarkJunk(ctx, id)
	if err != nil {
		t.Fatalf("MarkJunk: %v", err)
	}
	if got.FolderID != 7 || !got.Read {
		t.Errorf("after MarkJunk: folder=%d read=%v", got.FolderID, got.Read)
	}

	got2, err := r.MarkNotJunk(ctx, id)
	if err != nil {
		t.Fatalf("MarkNotJunk: %v", err)
	}
	if got2.FolderID != 1 || got2.Read {
		t.Errorf("after MarkNotJunk: folder=%d read=%v", got2.FolderID, got2.Read)
	}
}

func TestGetMessageThread_HeaderBased(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	// Insert parent with message_id "parent@x".
	parent := makeMsg(1, "thread subject")
	parent.MessageID = sql.NullString{String: "parent@x", Valid: true}
	pid, _ := r.InsertMessage(ctx, parent)

	// Insert child that references parent via in_reply_to.
	child := makeMsg(1, "Re: thread subject")
	child.MessageID = sql.NullString{String: "child@x", Valid: true}
	child.InReplyTo = sql.NullString{String: "parent@x", Valid: true}
	child.Date = parent.Date.Add(time.Minute)
	cid, _ := r.InsertMessage(ctx, child)

	summaries, truncated, err := r.GetMessageThread(ctx, pid)
	if err != nil {
		t.Fatalf("GetMessageThread: %v", err)
	}
	if truncated {
		t.Error("unexpected truncated=true")
	}
	ids := make(map[int64]bool)
	for _, s := range summaries {
		ids[int64(s.ID)] = true
	}
	if !ids[pid] || !ids[cid] {
		t.Errorf("thread missing expected IDs: got %v", ids)
	}
}

func TestGetMessageThread_SubjectFallback(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	// Two messages with no thread headers but same normalized subject.
	m1 := makeMsg(1, "Hello World")
	m1.Date = time.Now().UTC()
	id1, _ := r.InsertMessage(ctx, m1)

	m2 := makeMsg(1, "Re: Hello World")
	m2.Date = m1.Date.Add(time.Minute)
	id2, _ := r.InsertMessage(ctx, m2)

	summaries, _, err := r.GetMessageThread(ctx, id1)
	if err != nil {
		t.Fatalf("GetMessageThread (subject fallback): %v", err)
	}
	ids := make(map[int64]bool)
	for _, s := range summaries {
		ids[int64(s.ID)] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Errorf("subject fallback missed IDs: got %v", ids)
	}
}

func TestSearchMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "FTS test")
	msg.BodyText = "the quick brown fox"
	if _, err := r.InsertMessage(ctx, msg); err != nil {
		t.Fatal(err)
	}

	items, total, err := r.SearchMessages(ctx, "quick", nil, nil, nil, 10, 0)
	if err != nil {
		t.Fatalf("SearchMessages: %v", err)
	}
	if total == 0 || len(items) == 0 {
		t.Error("expected at least one search result")
	}
}

func TestSearchMessagesFTSSanitization(t *testing.T) {
	// Verify that ", non-ASCII, and FTS5 operators are treated as literals (no error).
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	cases := []string{
		`"quoted"`,
		`AND OR NOT NEAR`,
		`café résumé`,
	}
	for _, q := range cases {
		_, _, err := r.SearchMessages(ctx, q, nil, nil, nil, 10, 0)
		if err != nil {
			t.Errorf("SearchMessages(%q): unexpected error: %v", q, err)
		}
	}
}

func TestBulkLimitExceeded(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	ids := make([]int64, 1001)
	for i := range ids {
		ids[i] = int64(i + 1)
	}

	if err := r.BulkDeleteMessages(ctx, ids); err == nil {
		t.Error("expected error for BulkDeleteMessages with >1000 ids")
	}
	if _, err := r.BulkUpdateMessages(ctx, ids, nil, nil); err == nil {
		t.Error("expected error for BulkUpdateMessages with >1000 ids")
	}
	if _, err := r.MoveMessages(ctx, ids, 1); err == nil {
		t.Error("expected error for MoveMessages with >1000 ids")
	}
}
