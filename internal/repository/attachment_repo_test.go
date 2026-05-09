package repository

import (
	"context"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/model"
)

// insertTestMessage inserts a minimal message row and returns its ID.
func insertTestMessage(t *testing.T, r *MessageRepository, folderID int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := r.InsertMessage(ctx, makeMsg(folderID, "subject"))
	if err != nil {
		t.Fatalf("InsertMessage: %v", err)
	}
	return id
}

// insertTestAttachment inserts a single attachment for msgID and returns its ID.
func insertTestAttachment(t *testing.T, r *AttachmentRepository, msgID int64, filename string, data []byte) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := r.InsertAttachment(ctx, model.DBAttachment{
		MessageID:   int(msgID),
		Filename:    filename,
		ContentType: "text/plain",
		Size:        len(data),
		Data:        data,
	})
	if err != nil {
		t.Fatalf("InsertAttachment: %v", err)
	}
	return id
}

func TestDeleteAttachmentsByMessageID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	ar := NewAttachmentRepository(db)
	mr := NewMessageRepository(db)

	t.Run("deletes all attachments for that message", func(t *testing.T) {
		msgID := insertTestMessage(t, mr, 1)
		insertTestAttachment(t, ar, msgID, "a.txt", []byte("aaa"))
		insertTestAttachment(t, ar, msgID, "b.txt", []byte("bbb"))

		if err := ar.DeleteAttachmentsByMessageID(ctx, msgID); err != nil {
			t.Fatalf("DeleteAttachmentsByMessageID: %v", err)
		}

		atts, err := ar.ListAttachments(ctx, msgID)
		if err != nil {
			t.Fatalf("ListAttachments: %v", err)
		}
		if len(atts) != 0 {
			t.Errorf("want 0 attachments after delete, got %d", len(atts))
		}
	})

	t.Run("no-op when message has no attachments", func(t *testing.T) {
		msgID := insertTestMessage(t, mr, 1)

		if err := ar.DeleteAttachmentsByMessageID(ctx, msgID); err != nil {
			t.Fatalf("DeleteAttachmentsByMessageID on empty message: %v", err)
		}
	})

	t.Run("does not affect attachments for other messages", func(t *testing.T) {
		msgA := insertTestMessage(t, mr, 1)
		msgB := insertTestMessage(t, mr, 1)
		insertTestAttachment(t, ar, msgA, "a.txt", []byte("a"))
		insertTestAttachment(t, ar, msgB, "b.txt", []byte("b"))

		if err := ar.DeleteAttachmentsByMessageID(ctx, msgA); err != nil {
			t.Fatalf("DeleteAttachmentsByMessageID: %v", err)
		}

		atts, err := ar.ListAttachments(ctx, msgB)
		if err != nil {
			t.Fatalf("ListAttachments msgB: %v", err)
		}
		if len(atts) != 1 {
			t.Errorf("want 1 attachment on msgB, got %d", len(atts))
		}
	})
}

func TestListAttachmentsWithData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	ar := NewAttachmentRepository(db)
	mr := NewMessageRepository(db)

	t.Run("returns empty slice for unknown message", func(t *testing.T) {
		atts, err := ar.ListAttachmentsWithData(ctx, 99999)
		if err != nil {
			t.Fatalf("ListAttachmentsWithData: %v", err)
		}
		if len(atts) != 0 {
			t.Errorf("want 0, got %d", len(atts))
		}
	})

	t.Run("returns BLOB data and correct ordering by id", func(t *testing.T) {
		msgID := insertTestMessage(t, mr, 1)
		data1 := []byte("hello attachment one")
		data2 := []byte("hello attachment two")
		insertTestAttachment(t, ar, msgID, "first.txt", data1)
		insertTestAttachment(t, ar, msgID, "second.txt", data2)

		atts, err := ar.ListAttachmentsWithData(ctx, msgID)
		if err != nil {
			t.Fatalf("ListAttachmentsWithData: %v", err)
		}
		if len(atts) != 2 {
			t.Fatalf("want 2 attachments, got %d", len(atts))
		}

		if atts[0].Filename != "first.txt" {
			t.Errorf("wrong order: first filename = %q", atts[0].Filename)
		}
		if string(atts[0].Data) != "hello attachment one" {
			t.Errorf("wrong data[0]: %q", atts[0].Data)
		}
		if atts[1].Filename != "second.txt" {
			t.Errorf("wrong order: second filename = %q", atts[1].Filename)
		}
		if string(atts[1].Data) != "hello attachment two" {
			t.Errorf("wrong data[1]: %q", atts[1].Data)
		}
	})
}

func TestCopyAttachments(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	ar := NewAttachmentRepository(db)
	mr := NewMessageRepository(db)

	t.Run("copies all attachments to destination", func(t *testing.T) {
		src := insertTestMessage(t, mr, 1)
		dst := insertTestMessage(t, mr, 3)

		data1 := []byte("content one")
		data2 := []byte("content two")
		insertTestAttachment(t, ar, src, "one.txt", data1)
		insertTestAttachment(t, ar, src, "two.txt", data2)

		if err := ar.CopyAttachments(ctx, src, dst); err != nil {
			t.Fatalf("CopyAttachments: %v", err)
		}

		atts, err := ar.ListAttachmentsWithData(ctx, dst)
		if err != nil {
			t.Fatalf("ListAttachmentsWithData dst: %v", err)
		}
		if len(atts) != 2 {
			t.Fatalf("want 2 attachments on dst, got %d", len(atts))
		}
		if atts[0].Filename != "one.txt" || string(atts[0].Data) != "content one" {
			t.Errorf("unexpected att[0]: %+v", atts[0])
		}
		if atts[1].Filename != "two.txt" || string(atts[1].Data) != "content two" {
			t.Errorf("unexpected att[1]: %+v", atts[1])
		}
	})

	t.Run("source attachments are unaffected", func(t *testing.T) {
		src := insertTestMessage(t, mr, 1)
		dst := insertTestMessage(t, mr, 3)
		insertTestAttachment(t, ar, src, "orig.txt", []byte("original"))

		if err := ar.CopyAttachments(ctx, src, dst); err != nil {
			t.Fatalf("CopyAttachments: %v", err)
		}

		srcAtts, err := ar.ListAttachmentsWithData(ctx, src)
		if err != nil {
			t.Fatalf("ListAttachmentsWithData src: %v", err)
		}
		if len(srcAtts) != 1 {
			t.Errorf("source: want 1 attachment, got %d", len(srcAtts))
		}
		if srcAtts[0].Filename != "orig.txt" {
			t.Errorf("source filename changed: %q", srcAtts[0].Filename)
		}
	})

	t.Run("no-op when source has no attachments", func(t *testing.T) {
		src := insertTestMessage(t, mr, 1)
		dst := insertTestMessage(t, mr, 3)

		if err := ar.CopyAttachments(ctx, src, dst); err != nil {
			t.Fatalf("CopyAttachments on empty source: %v", err)
		}

		atts, err := ar.ListAttachments(ctx, dst)
		if err != nil {
			t.Fatalf("ListAttachments dst: %v", err)
		}
		if len(atts) != 0 {
			t.Errorf("want 0 attachments on dst, got %d", len(atts))
		}
	})
}

func TestMessageExists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	mr := NewMessageRepository(db)

	t.Run("returns false for unknown id", func(t *testing.T) {
		ok, err := mr.MessageExists(ctx, 99999)
		if err != nil {
			t.Fatalf("MessageExists: %v", err)
		}
		if ok {
			t.Error("want false, got true")
		}
	})

	t.Run("returns true for existing id", func(t *testing.T) {
		id := insertTestMessage(t, mr, 1)

		ok, err := mr.MessageExists(ctx, id)
		if err != nil {
			t.Fatalf("MessageExists: %v", err)
		}
		if !ok {
			t.Error("want true, got false")
		}
	})
}
