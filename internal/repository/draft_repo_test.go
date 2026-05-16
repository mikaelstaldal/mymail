package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func insertScheduledMsg(t *testing.T, db *sql.DB, sendAt time.Time) int64 {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO messages(folder_id,from_addr,to_addr,subject,date,body_text,send_at,created_at,updated_at)
		VALUES(5,'sender@example.com','to@example.com','scheduled','2024-01-01T00:00:00Z','body',?,
		       '2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`,
		sendAt.UTC().Format(time.RFC3339),
	)
	require.NoError(t, err)
	id, _ := res.LastInsertId()
	return id
}

func TestRescheduleMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewDraftRepository(db)

	original := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	id := insertScheduledMsg(t, db, original)

	newTime := time.Now().UTC().Add(5 * time.Hour).Truncate(time.Second)
	got, err := r.RescheduleMessage(ctx, id, newTime)
	require.NoError(t, err)
	assert.Equal(t, int(id), int(got.ID))
	assert.Equal(t, 5, got.FolderID)
	require.True(t, got.SendAt.Valid)
	assert.Equal(t, newTime, got.SendAt.Time.UTC())
}

func TestRescheduleMessageNotScheduled(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewDraftRepository(db)

	// Insert a message in Inbox (folder_id=1), not Scheduled.
	res, err := db.Exec(`
		INSERT INTO messages(folder_id,from_addr,to_addr,subject,date,created_at,updated_at)
		VALUES(1,'a@b.com','c@d.com','hi','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	require.NoError(t, err)
	idRaw, _ := res.LastInsertId()
	id := idRaw

	_, err = r.RescheduleMessage(ctx, id, time.Now().UTC().Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestRescheduleMessageNonExistent(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewDraftRepository(db)

	_, err := r.RescheduleMessage(ctx, 9999, time.Now().UTC().Add(2*time.Hour))
	assert.ErrorIs(t, err, ErrNotFound)
}
