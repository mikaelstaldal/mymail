package handler_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Delivers raw RFC 5322 through the real LDA UNIX socket (the path mymail-lda
// uses), reads the message detail through the full handler -> repository
// stack, and returns the warning kinds the UI would show. Compatibility
// characters map to plain ASCII under IDNA and must not hide an IDN domain.
func TestCompatibilityCharacterSenderDomainIsFlagged(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	ctx := context.Background()

	// An install with a trusted SpamAssassin checker, so an exact spoof is caught.
	_, err := db.Exec(`UPDATE spam_filter_settings SET trusted_checker_host='checker' WHERE id=1`)
	require.NoError(t, err)

	// The owner has previously written to bob@bank.com and ceo@business.com.
	for _, to := range []string{"bob@bank.com", "ceo@business.com"} {
		_, err = repository.NewMessageRepository(db).InsertMessage(ctx, model.DBMessage{
			FolderID: 2, ToAddr: to, Date: time.Now(),
		})
		require.NoError(t, err)
	}

	sock := filepath.Join(t.TempDir(), "lda.sock")
	ln, err := lda.BindSocket(sock)
	require.NoError(t, err)
	sctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)
	go lda.ServeSocket(sctx, ln, db)

	n := 0
	deliver := func(from, dmarcRule string) (string, []api.MessageWarningKind) {
		n++
		mid := fmt.Sprintf("poc-%d@attacker.example", n)
		raw := "From: " + from + "\r\n" +
			"To: me@example.org\r\n" +
			"Subject: Updated payment details\r\n" +
			"Message-Id: <" + mid + ">\r\n" +
			"Date: Mon, 19 Oct 2026 10:00:00 +0000\r\n" +
			"X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-26) on checker\r\n" +
			"X-Spam-Status: No, score=0.1 required=5.0 tests=" + dmarcRule + ",SPF_NONE autolearn=no\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n\r\n" +
			"Please wire the invoice to our new account.\r\n"
		conn, err := net.Dial("unix", sock)
		require.NoError(t, err)
		_, err = conn.Write([]byte(raw))
		require.NoError(t, err)
		require.NoError(t, conn.(*net.UnixConn).CloseWrite())
		resp, err := io.ReadAll(conn)
		require.NoError(t, err)
		_ = conn.Close()
		require.Equal(t, "ok", string(resp))

		var id int64
		require.NoError(t, db.QueryRow(`SELECT id FROM messages WHERE message_id=?`, mid).Scan(&id))
		res := do(srv, http.MethodGet, fmt.Sprintf("/messages/%d", id), "")
		require.Equal(t, 200, res.StatusCode)
		var d api.MessageDetail
		decodeJSON(t, res, &d)
		kinds := []api.MessageWarningKind{}
		for _, w := range d.Warnings {
			kinds = append(kinds, w.Kind)
		}
		return d.FromAddr, kinds
	}

	// Control: an exact spoof of bank.com is caught by DMARC.
	shown, kinds := deliver("Bob <bob@bank.com>", "DMARC_REJECT")
	t.Logf("exact spoof   from=%q warnings=%v", shown, kinds)
	assert.Contains(t, kinds, api.MessageWarningKindAuthFail)

	// Control: a Cyrillic lookalike is flagged.
	shown, kinds = deliver("Bob <bob@bаnk.com>", "DMARC_MISSING")
	t.Logf("cyrillic a    from=%q warnings=%v", shown, kinds)
	assert.Contains(t, kinds, api.MessageWarningKindLookalikeDomain)

	// Kelvin sign U+212A, and long s U+017F: both fold onto the known address.
	for _, from := range []string{
		"Bob <bob@ban\u212A.com>",
		"CEO <ceo@bu\u017Fine\u017F\u017F.com>",
	} {
		shown, kinds = deliver(from, "DMARC_MISSING")
		t.Logf("folds         from=%q warnings=%v", shown, kinds)
		assert.Contains(t, kinds, api.MessageWarningKindLookalikeDomain,
			"a non-ASCII sender domain must produce lookalike_domain")
		assert.Contains(t, kinds, api.MessageWarningKindFirstTimeSender,
			"the owner has never written to this exact address")
	}

	// Fullwidth and mathematical bold "bank": not folded, but still mapped to ASCII.
	for _, from := range []string{
		"Bob <bob@ｂａｎｋ.com>",
		"Bob <bob@\U0001D41B\U0001D41A\U0001D427\U0001D424.com>",
	} {
		shown, kinds = deliver(from, "DMARC_MISSING")
		t.Logf("mapped only   from=%q warnings=%v", shown, kinds)
		assert.Contains(t, kinds, api.MessageWarningKindLookalikeDomain,
			"a non-ASCII sender domain must produce lookalike_domain")
	}
}
