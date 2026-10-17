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
