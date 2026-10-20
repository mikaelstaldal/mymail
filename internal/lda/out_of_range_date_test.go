package lda

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func outOfRangeDateServer(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	h := handler.New(
		repository.NewFolderRepository(db),
		repository.NewMessageRepository(db),
		repository.NewAttachmentRepository(db),
		repository.NewDraftRepository(db),
		repository.NewContactRepository(db),
		repository.NewIdentityRepository(db),
		repository.NewFilterRepository(db),
		repository.NewSpamFilterRepository(db),
		"",
	)
	srv, err := api.NewServer(h, api.WithErrorHandler(handler.WriteError))
	require.NoError(t, err)
	return srv
}

func outOfRangeDateGet(srv http.Handler, path string) (int, string) {
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	b, _ := io.ReadAll(w.Result().Body)
	if len(b) > 160 {
		b = b[:160]
	}
	return w.Code, string(b)
}

// TestOutOfRangeDateHeaderMustNotBreakReads delivers an attacker-written
// message through the real LDA pipeline (runCore) and then reads through the
// full handler stack. net/mail.ParseDate accepts both edge offsets, whose UTC
// conversion is year 10000 or -1; stored as is, one such email made every
// listing, search and thread containing it return 500.
func TestOutOfRangeDateHeaderMustNotBreakReads(t *testing.T) {
	for _, dateHdr := range []string{
		"Fri, 31 Dec 9999 23:59:00 -2359", // UTC year 10000
		"Sat, 01 Jan 0000 00:00:00 +0100", // UTC year -0001
	} {
		t.Run(dateHdr, func(t *testing.T) {
			db := openLDATestDB(t)
			srv := outOfRangeDateServer(t, db)

			good := "From: alice@example.com\r\nTo: me@example.com\r\nSubject: hello there\r\n" +
				"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-ID: <good001@example.com>\r\n\r\nhello world\r\n"
			require.Equal(t, 0, runCore(db, []byte(good)))

			var goodID int64
			require.NoError(t, db.QueryRow(`SELECT id FROM messages WHERE message_id='good001@example.com'`).Scan(&goodID))

			// Baseline: everything works before the attack.
			for _, p := range []string{"/folders/1/messages", "/messages/search?q=hello", fmt.Sprintf("/messages/%d/thread", goodID)} {
				code, body := outOfRangeDateGet(srv, p)
				require.Equal(t, http.StatusOK, code, "baseline GET %s: %s", p, body)
			}

			evil := "From: mallory@evil.example\r\nTo: me@example.com\r\nSubject: Re: hello there\r\n" +
				"Date: " + dateHdr + "\r\nMessage-ID: <evil001@evil.example>\r\n" +
				"In-Reply-To: <good001@example.com>\r\nReferences: <good001@example.com>\r\n\r\n" +
				"hello the and a to of in is you that it\r\n"
			require.Equal(t, 0, runCore(db, []byte(evil)), "LDA accepts the message")

			var evilID int64
			var storedDate string
			require.NoError(t, db.QueryRow(`SELECT id, date FROM messages WHERE message_id='evil001@evil.example'`).Scan(&evilID, &storedDate))
			stored, err := time.Parse(time.RFC3339, storedDate)
			require.NoError(t, err, "stored messages.date %q must parse", storedDate)
			assert.WithinDuration(t, time.Now(), stored, time.Minute, "falls back to delivery time")

			for _, p := range []string{
				"/folders/1/messages",
				"/messages/search?q=hello",
				fmt.Sprintf("/messages/%d/thread", goodID),
				fmt.Sprintf("/messages/%d", evilID),
				fmt.Sprintf("/messages/%d/body", evilID),
			} {
				code, body := outOfRangeDateGet(srv, p)
				t.Logf("GET %s -> %d %s", p, code, body)
				assert.Equal(t, http.StatusOK, code, "GET %s must not fail because of one attacker email", p)
			}

			// Filing the message to Junk does not repair the thread or the Junk listing.
			_, err = db.Exec(`UPDATE messages SET folder_id = 7 WHERE id = ?`, evilID)
			require.NoError(t, err)
			for _, p := range []string{"/folders/1/messages", fmt.Sprintf("/messages/%d/thread", goodID), "/folders/7/messages"} {
				code, body := outOfRangeDateGet(srv, p)
				t.Logf("after move to Junk: GET %s -> %d %s", p, code, body)
				assert.Equal(t, http.StatusOK, code, "after move to Junk: GET %s", p)
			}
		})
	}
}

func TestParseMessageDiscardsOutOfRangeDate(t *testing.T) {
	for hdr, want := range map[string]string{
		"Fri, 31 Dec 9999 23:59:00 -2359": "",
		"Sat, 01 Jan 0000 00:00:00 +0100": "",
		"Sat, 01 Jan 0000 00:00:00 -0100": "",
		"Fri, 31 Dec 9999 23:59:00 +2359": "9999-12-31T00:00:00Z",
		"Mon, 01 Jan 2024 10:00:00 +0200": "2024-01-01T08:00:00Z",
	} {
		pm, err := ParseMessage([]byte("From: a@example.com\r\nDate: " + hdr + "\r\n\r\nbody\r\n"))
		require.NoError(t, err)
		if want == "" {
			assert.Nil(t, pm.Date, hdr)
		} else if assert.NotNil(t, pm.Date, hdr) {
			assert.Equal(t, want, pm.Date.Format(time.RFC3339), hdr)
		}
	}
}

func TestImportFallbackDatesDiscardOutOfRange(t *testing.T) {
	assert.Nil(t, parseMbxDate("31-Dec-9999 23:59:00 -2359"))
	assert.Nil(t, parseMbxDate(" 1-Jan-0000 00:00:00 +0100"))
	assert.NotNil(t, parseMbxDate(" 1-Jan-2024 00:00:00 +0100"))
	assert.Nil(t, parseFromTimestamp("Fri Dec 31 23:59:00 -2359 9999"))
	assert.NotNil(t, parseFromTimestamp("Mon Jan  1 10:00:00 2024"))
}

func TestImportMboxOutOfRangeDateUsesFallback(t *testing.T) {
	db := openImportTestDB(t)
	path := filepath.Join(t.TempDir(), "in.mbox")
	require.NoError(t, os.WriteFile(path, []byte(
		"From sender@example.com Mon Jan 15 12:30:00 2024\r\n"+
			"From: a@b.com\r\nDate: Fri, 31 Dec 9999 23:59:00 -2359\r\nMessage-Id: <fromline@host>\r\n\r\nBody\r\n"+
			"From sender@example.com Fri Dec 31 23:59:00 -2359 9999\r\n"+
			"From: a@b.com\r\nDate: Sat, 01 Jan 0000 00:00:00 +0100\r\nMessage-Id: <mtime@host>\r\n\r\nBody\r\n"), 0o600))
	mtime := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	require.NoError(t, os.Chtimes(path, mtime, mtime))

	imp, skip, err := importMbox(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 2, imp)
	assert.Equal(t, 0, skip)

	for id, want := range map[string]string{
		"fromline@host": "2024-01-15T12:30:00Z",
		"mtime@host":    "2023-06-15T00:00:00Z",
	} {
		var date string
		require.NoError(t, db.QueryRow(`SELECT date FROM messages WHERE message_id = ?`, id).Scan(&date))
		assert.Equal(t, want, date, id)
	}
}
