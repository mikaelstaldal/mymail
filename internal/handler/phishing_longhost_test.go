package handler_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/phishing"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A remote sender mails one HTML message with phishing.MaxLinks anchors, each
// pointing at a distinct ~10 KB hostname. Opening that message (the detail
// JSON and the iframe body) must stay cheap. Regression for repeated Unicode
// script analysis of every byte of these invalid DNS hosts.
func TestLongLinkHostsBoundViewWork(t *testing.T) {
	const links, hostLen = phishing.MaxLinks, 10_000

	var html strings.Builder
	html.WriteString("<html><body>")
	for i := 0; i < links; i++ {
		prefix := fmt.Sprintf("h%d", i)
		host := prefix + strings.Repeat(string(rune('a'+i%26)), hostLen-len(prefix)) + ".com"
		fmt.Fprintf(&html, `<a href="https://%s/">click</a> `, host)
	}
	html.WriteString("</body></html>")
	enc := base64.StdEncoding.EncodeToString([]byte(html.String()))
	var b64 strings.Builder
	for len(enc) > 76 {
		b64.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b64.WriteString(enc + "\r\n")

	raw := "From: Attacker <attacker@attacker.example>\r\n" +
		"To: owner@example.com\r\n" +
		"Subject: hello\r\n" +
		"Message-ID: <dos-1@attacker.example>\r\n" +
		"Date: Mon, 01 Jan 2026 00:00:00 +0000\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" + b64.String()
	t.Logf("raw message size: %.1f MB", float64(len(raw))/1e6)
	require.Less(t, len(raw), lda.MaxMessageBytes)

	// The real LDA parse + sanitize pipeline keeps every anchor and its long host.
	pm, err := lda.ParseMessage([]byte(raw))
	require.NoError(t, err)
	require.Equal(t, links, strings.Count(pm.BodyHTML, "<a "), "all anchors survive sanitization")
	require.Contains(t, pm.BodyHTML, "h999"+strings.Repeat("l", 100), "long host survives sanitization")

	db := openTestDB(t)
	srv := newServer(t, db)
	id, err := repository.NewMessageRepository(db).InsertMessage(context.Background(), model.DBMessage{
		FolderID: 1, FromAddr: pm.FromAddr, ToAddr: pm.ToAddr, Subject: pm.Subject,
		Date: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), BodyHTML: pm.BodyHTML, Raw: []byte(raw),
	})
	require.NoError(t, err)

	for _, path := range []string{fmt.Sprintf("/messages/%d", id), fmt.Sprintf("/messages/%d/body", id)} {
		for view := 1; view <= 2; view++ {
			start := time.Now()
			res := do(srv, http.MethodGet, path, "")
			_, err := io.Copy(io.Discard, res.Body)
			require.NoError(t, err)
			require.NoError(t, res.Body.Close())
			elapsed := time.Since(start)
			require.Equal(t, http.StatusOK, res.StatusCode)
			t.Logf("GET %s (view %d): %v", path, view, elapsed)
			assert.Less(t, elapsed, 2*time.Second, "viewing one attacker-sent message must not burn seconds of CPU on every open")
		}
	}
}
