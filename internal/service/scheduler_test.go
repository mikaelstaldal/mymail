package service

import (
	"bufio"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fake sendmail runs this helper in a separate process. Store the received
// copy before sendmail returns, reproducing a fast local self-delivery.
func TestSchedulerSelfSendHelper(t *testing.T) {
	path := os.Getenv("MYMAIL_SELF_SEND_TEST_DB")
	if path == "" {
		return
	}
	msg, err := mail.ReadMessage(bufio.NewReader(os.Stdin))
	require.NoError(t, err)
	db, err := repository.OpenDB(path, 5000)
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`INSERT INTO messages(folder_id,message_id,date,created_at,updated_at)
		VALUES(1,?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, strings.Trim(msg.Header.Get("Message-ID"), "<>"))
	require.NoError(t, err)
}

func TestSchedulerSelfDeliveryBeforeSendmailReturns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mail.sqlite")
	db, err := repository.OpenDB(path, 5000)
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	require.NoError(t, repository.SeedBuiltinFolders(db))
	result, err := db.Exec(`INSERT INTO messages(folder_id,from_addr,to_addr,date,send_at,created_at,updated_at)
		VALUES(5,'me@example.com','me@example.com','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	id, err := result.LastInsertId()
	require.NoError(t, err)
	exe, err := os.Executable()
	require.NoError(t, err)
	t.Setenv("MYMAIL_SELF_SEND_TEST_EXECUTABLE", exe)
	t.Setenv("MYMAIL_SELF_SEND_TEST_DB", path)
	sendmail := filepath.Join(t.TempDir(), "sendmail")
	require.NoError(t, os.WriteFile(sendmail, []byte("#!/bin/sh\nexec \"$MYMAIL_SELF_SEND_TEST_EXECUTABLE\" -test.run='^TestSchedulerSelfSendHelper$'\n"), 0o700))
	scheduler := NewScheduler(db, sendmail, repository.NewContactRepository(db))
	scheduler.processDeferredSends(t.Context())
	var folder, outgoing, count int
	var messageID string
	require.NoError(t, db.QueryRow(`SELECT folder_id,outgoing,message_id FROM messages WHERE id=?`, id).Scan(&folder, &outgoing, &messageID))
	assert.Equal(t, 2, folder)
	assert.Equal(t, 1, outgoing)
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id=?`, messageID).Scan(&count))
	assert.Equal(t, 2, count)
}
