package repository

import (
	"context"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/senderauth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackfillSenderAuthentication(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)
	msg := makeMsg(1, "old message")
	msg.Raw = []byte("From: a@example.org\r\nX-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_PASS,DKIM_VALID_AU,SPF_PASS\r\n\r\nbody")
	id, err := r.InsertMessage(ctx, msg)
	require.NoError(t, err)
	draft := makeMsg(3, "draft")
	draftID, err := r.InsertMessage(ctx, draft)
	require.NoError(t, err)
	require.NoError(t, BackfillSenderAuthentication(ctx, db))
	got, err := r.GetMessageDetail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
	_, err = db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host = 'checker'`)
	require.NoError(t, err)
	for range 2 {
		require.NoError(t, BackfillSenderAuthentication(ctx, db))
		got, err = r.GetMessageDetail(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, "pass", string(got.ToOASMessage(nil).SenderAuthentication.Dmarc))
		assert.True(t, got.ToOASMessage(nil).SenderAuthentication.DkimAligned)
	}
	got, err = r.GetMessageDetail(ctx, draftID)
	require.NoError(t, err)
	assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
	_, err = db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host = ''`)
	require.NoError(t, err)
	require.NoError(t, BackfillSenderAuthentication(ctx, db))
	got, err = r.GetMessageDetail(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
}

func TestBackfillSelectedFolders(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	_, err := db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host = 'checker';
 INSERT INTO folders(id,name,slug,position) VALUES(100,'Old archive','old-archive',100)`)
	require.NoError(t, err)
	r := NewMessageRepository(db)
	ids := map[int]int64{}
	for _, folderID := range []int{1, 2, 100} {
		msg := makeMsg(folderID, "message")
		msg.Raw = []byte("From: a@example.org\r\nX-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_PASS\r\n\r\nbody")
		ids[folderID], err = r.InsertMessage(ctx, msg)
		require.NoError(t, err)
	}
	// Any accidental write to the excluded archive must fail, even one that
	// happens to leave its verdict unchanged.
	_, err = db.Exec(`CREATE TRIGGER protect_archive BEFORE UPDATE ON messages WHEN OLD.folder_id = 100 BEGIN SELECT RAISE(ABORT, 'archive must remain untouched'); END`)
	require.NoError(t, err)
	for _, selection := range [][]string{{"inbox", "missing"}, {""}} {
		require.Error(t, BackfillSenderAuthentication(ctx, db, selection...))
		got, err := r.GetMessageDetail(ctx, ids[1])
		require.NoError(t, err)
		assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
	}
	// A failure in a later selected folder must roll back earlier updates.
	require.ErrorContains(t, BackfillSenderAuthentication(ctx, db, "inbox", "old-archive"), "archive must remain untouched")
	got, err := r.GetMessageDetail(ctx, ids[1])
	require.NoError(t, err)
	assert.Equal(t, senderauth.Unknown(), got.ToOASMessage(nil).SenderAuthentication)
	// Slug, exact display name, and aliases selecting the same folder all work.
	require.NoError(t, BackfillSenderAuthentication(ctx, db, "inbox", "Sent", "Inbox"))
	for _, folderID := range []int{1, 2, 100} {
		got, err := r.GetMessageDetail(ctx, ids[folderID])
		require.NoError(t, err)
		want := "pass"
		if folderID == 100 {
			want = "unknown"
		}
		assert.Equal(t, want, string(got.ToOASMessage(nil).SenderAuthentication.Dmarc))
	}
	// A folder name colliding with a different folder's slug must not silently
	// select both, or unexpectedly expand a maintenance operation's scope.
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(101,'inbox','other',101)`)
	require.NoError(t, err)
	require.ErrorContains(t, BackfillSenderAuthentication(ctx, db, "inbox"), "ambiguous")
	require.ErrorContains(t, BackfillSenderAuthentication(ctx, db, "INBOX"), "does not exist")
}

func TestBackfillDisambiguatesSwappedNamesAndSlugs(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	_, err := db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host = 'checker';
 INSERT INTO folders(id,name,slug,position) VALUES(100,'x','y',100),(101,'y','x',101)`)
	require.NoError(t, err)
	r := NewMessageRepository(db)
	ids := []int64{}
	for _, folder := range []int{100, 101} {
		msg := makeMsg(folder, "old mail")
		msg.Raw = []byte("From: a@example.org\r\nX-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_PASS\r\n\r\nbody")
		id, err := r.InsertMessage(ctx, msg)
		require.NoError(t, err)
		ids = append(ids, id)
	}
	for _, selector := range []string{"x", "y", "name:", "slug:"} {
		require.Error(t, BackfillSenderAuthentication(ctx, db, selector))
	}
	require.NoError(t, BackfillSenderAuthentication(ctx, db, "name:x"))
	first, err := r.GetMessageDetail(ctx, ids[0])
	require.NoError(t, err)
	second, err := r.GetMessageDetail(ctx, ids[1])
	require.NoError(t, err)
	assert.Equal(t, "pass", string(first.ToOASMessage(nil).SenderAuthentication.Dmarc))
	assert.Equal(t, senderauth.Unknown(), second.ToOASMessage(nil).SenderAuthentication)
	require.NoError(t, BackfillSenderAuthentication(ctx, db, "slug:x"))
	second, err = r.GetMessageDetail(ctx, ids[1])
	require.NoError(t, err)
	assert.Equal(t, "pass", string(second.ToOASMessage(nil).SenderAuthentication.Dmarc))
}
