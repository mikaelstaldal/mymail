package handler_test

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A remote sender pads an HTML body with 1000 cheap anchors. The deceptive
// link placed after them must be disabled with an explicit overflow warning.
// Without filler, the link must retain its deceptive warning and confirmation.
func TestLinksAfterCapAreDisabledWithOverflowWarning(t *testing.T) {
	deceptive := `<a href="https://evil.example/login">paypal.com</a>`
	for _, tc := range []struct {
		name   string
		filler int
	}{{"control_no_filler", 0}, {"after_1000_filler_anchors", 1000}, {"after_100000_filler_anchors", 100000}} {
		t.Run(tc.name, func(t *testing.T) {
			body := "<html><body>" + strings.Repeat(`<a href="https://a.example/">.</a>`, tc.filler) + deceptive + "</body></html>"
			raw := "From: Attacker <attacker@attacker.example>\r\n" +
				"To: me@example.com\r\n" +
				"Subject: Your account\r\n" +
				"Message-ID: <repro-" + tc.name + "@attacker.example>\r\n" +
				"Date: Mon, 19 Oct 2026 10:00:00 +0000\r\n" +
				"MIME-Version: 1.0\r\n" +
				"Content-Type: text/html; charset=utf-8\r\n\r\n" + body + "\r\n"
			t.Logf("raw message size: %d bytes", len(raw))

			// Real inbound pipeline: RFC 5322 parse plus the inbound sanitizer.
			pm, err := lda.ParseMessage([]byte(raw))
			require.NoError(t, err)
			require.Contains(t, pm.BodyHTML, `https://evil.example/login`, "sanitizer keeps the link")

			db := openTestDB(t)
			srv := newServer(t, db)
			_, err = db.Exec(`INSERT INTO messages(id,folder_id,from_addr,body_html,date,created_at,updated_at) VALUES(42,1,'attacker@attacker.example',?,'2026-10-19T10:00:00Z','2026-10-19T10:00:00Z','2026-10-19T10:00:00Z')`, pm.BodyHTML)
			require.NoError(t, err)

			res := do(srv, http.MethodGet, "/messages/42", "")
			require.Equal(t, http.StatusOK, res.StatusCode)
			var d api.MessageDetail
			decodeJSON(t, res, &d)
			require.LessOrEqual(t, len(d.Warnings), 2, "filler must not amplify warning output")
			deceptiveCount := 0
			overflowCount := 0
			for _, w := range d.Warnings {
				if w.Kind == api.MessageWarningKindTooManyLinks {
					overflowCount++
				}
				if w.Kind == api.MessageWarningKindDeceptiveLink {
					deceptiveCount++
				}
			}
			t.Logf("deceptive_link warnings: %d (all warnings: %d)", deceptiveCount, len(d.Warnings))

			res = do(srv, http.MethodGet, "/messages/42/body", "")
			require.Equal(t, http.StatusOK, res.StatusCode)
			served, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			text := string(served)
			t.Logf("served body still contains direct evil href: %v", strings.Contains(text, `href="https://evil.example/login"`))

			if tc.filler == 0 {
				assert.Equal(t, 1, deceptiveCount)
				assert.Zero(t, overflowCount)
				assert.Contains(t, text, "#/link/42/0")
			} else {
				assert.Zero(t, deceptiveCount)
				assert.Equal(t, 1, overflowCount)
				assert.NotContains(t, text, "#/link/42/1000")
			}
			assert.NotContains(t, text, `href="https://evil.example/login"`, "deceptive link must not open directly")
		})
	}
}
