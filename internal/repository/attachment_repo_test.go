package repository

import (
	"context"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/model"
)

// insertTestMessage inserts a minimal message row and returns its ID.
func insertTestMessage(t *testing.T, r *MessageRepository, folderID int) int64 {
	t.Helper()
	ctx := context.Background()
	id, err := r.InsertMessage(ctx, makeMsg(folderID, "subject"))
	require.NoError(t, err, "InsertMessage")
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
	require.NoError(t, err, "InsertAttachment")
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

		err := ar.DeleteAttachmentsByMessageID(ctx, msgID)
		assert.NoError(t, err)

		atts, err := ar.ListAttachments(ctx, msgID)
		assert.NoError(t, err)
		assert.Empty(t, atts)
	})

	t.Run("no-op when message has no attachments", func(t *testing.T) {
		msgID := insertTestMessage(t, mr, 1)

		err := ar.DeleteAttachmentsByMessageID(ctx, msgID)
		assert.NoError(t, err)
	})

	t.Run("does not affect attachments for other messages", func(t *testing.T) {
		msgA := insertTestMessage(t, mr, 1)
		msgB := insertTestMessage(t, mr, 1)
		insertTestAttachment(t, ar, msgA, "a.txt", []byte("a"))
		insertTestAttachment(t, ar, msgB, "b.txt", []byte("b"))

		err := ar.DeleteAttachmentsByMessageID(ctx, msgA)
		assert.NoError(t, err)

		atts, err := ar.ListAttachments(ctx, msgB)
		assert.NoError(t, err)
		assert.Len(t, atts, 1)
	})
}

func TestListAttachmentsWithData(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	ar := NewAttachmentRepository(db)
	mr := NewMessageRepository(db)

	t.Run("returns empty slice for unknown message", func(t *testing.T) {
		atts, err := ar.ListAttachmentsWithData(ctx, 99999)
		assert.NoError(t, err)
		assert.Empty(t, atts)
	})

	t.Run("returns BLOB data and correct ordering by id", func(t *testing.T) {
		msgID := insertTestMessage(t, mr, 1)
		data1 := []byte("hello attachment one")
		data2 := []byte("hello attachment two")
		insertTestAttachment(t, ar, msgID, "first.txt", data1)
		insertTestAttachment(t, ar, msgID, "second.txt", data2)

		atts, err := ar.ListAttachmentsWithData(ctx, msgID)
		assert.NoError(t, err)
		require.Len(t, atts, 2)

		assert.Equal(t, "first.txt", atts[0].Filename)
		assert.Equal(t, "hello attachment one", string(atts[0].Data))
		assert.Equal(t, "second.txt", atts[1].Filename)
		assert.Equal(t, "hello attachment two", string(atts[1].Data))
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

		err := ar.CopyAttachments(ctx, src, dst)
		assert.NoError(t, err)

		atts, err := ar.ListAttachmentsWithData(ctx, dst)
		assert.NoError(t, err)
		require.Len(t, atts, 2)
		assert.Equal(t, "one.txt", atts[0].Filename)
		assert.Equal(t, "content one", string(atts[0].Data))
		assert.Equal(t, "two.txt", atts[1].Filename)
		assert.Equal(t, "content two", string(atts[1].Data))
	})

	t.Run("source attachments are unaffected", func(t *testing.T) {
		src := insertTestMessage(t, mr, 1)
		dst := insertTestMessage(t, mr, 3)
		insertTestAttachment(t, ar, src, "orig.txt", []byte("original"))

		err := ar.CopyAttachments(ctx, src, dst)
		assert.NoError(t, err)

		srcAtts, err := ar.ListAttachmentsWithData(ctx, src)
		assert.NoError(t, err)
		assert.Len(t, srcAtts, 1)
		assert.Equal(t, "orig.txt", srcAtts[0].Filename)
	})

	t.Run("no-op when source has no attachments", func(t *testing.T) {
		src := insertTestMessage(t, mr, 1)
		dst := insertTestMessage(t, mr, 3)

		err := ar.CopyAttachments(ctx, src, dst)
		assert.NoError(t, err)

		atts, err := ar.ListAttachments(ctx, dst)
		assert.NoError(t, err)
		assert.Empty(t, atts)
	})
}

func TestMessageExists(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	mr := NewMessageRepository(db)

	t.Run("returns false for unknown id", func(t *testing.T) {
		ok, err := mr.MessageExists(ctx, 99999)
		assert.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("returns true for existing id", func(t *testing.T) {
		id := insertTestMessage(t, mr, 1)

		ok, err := mr.MessageExists(ctx, id)
		assert.NoError(t, err)
		assert.True(t, ok)
	})
}
