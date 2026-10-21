package lda

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRiskyAttachmentWarningMatchesServedName delivers one attacker email per
// crafted Content-Disposition through the real LDA pipeline (runCore) into
// SQLite, then fetches the message detail and the attachment download through
// the real ogen server and handler stack. Secure behaviour: if the name the
// browser saves has a risky extension, the message must carry a
// risky_attachment warning, because that warning is what makes the web UI
// label the attachment and ask for confirmation before downloading it.
func TestRiskyAttachmentWarningMatchesServedName(t *testing.T) {
	cases := []struct{ name, disp string }{
		{"control plain exe", `attachment; filename="invoice.exe"`},
		{"rfc2231 LF inside ext", `attachment; filename*=utf-8''invoice.e%0Axe`},
		{"rfc2231 trailing NUL", `attachment; filename*=us-ascii''evil.exe%00`},
		{"quoted-pair trailing quote", `attachment; filename="invoice.exe\""`},
		{"quoted-pair quote inside ext", `attachment; filename="invoice.e\"xe"`},
		{"backslash left unescaped", `attachment; filename="evil.e\xe"`},
		{"rfc2047 encoded word", `attachment; filename="=?utf-8?b?aW52b2ljZS5leGU=?="`},
	}
	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := openLDATestDB(t)
			msgID := fmt.Sprintf("repro%d@attacker.example", i)
			raw := "From: Billing <billing@attacker.example>\r\n" +
				"To: victim@example.com\r\n" +
				"Subject: Your invoice\r\n" +
				"Date: Mon, 19 Oct 2026 10:00:00 +0000\r\n" +
				"Message-Id: <" + msgID + ">\r\n" +
				"MIME-Version: 1.0\r\n" +
				"Content-Type: multipart/mixed; boundary=BND\r\n" +
				"\r\n" +
				"--BND\r\n" +
				"Content-Type: text/plain\r\n\r\nSee attached.\r\n" +
				"--BND\r\n" +
				"Content-Type: application/octet-stream\r\n" +
				"Content-Disposition: " + c.disp + "\r\n" +
				"Content-Transfer-Encoding: base64\r\n\r\n" +
				"TVqQAAMAAAAEAAAA//8AALgAAAAAAAAAQAAAAAAAAAA=\r\n" +
				"--BND--\r\n"
			require.Equal(t, 0, runCore(db, []byte(raw)))

			var id int64
			require.NoError(t, db.QueryRow(`SELECT id FROM messages WHERE message_id=?`, msgID).Scan(&id))

			h := handler.New(
				repository.NewFolderRepository(db), repository.NewMessageRepository(db),
				repository.NewAttachmentRepository(db), repository.NewDraftRepository(db),
				repository.NewContactRepository(db), repository.NewIdentityRepository(db),
				repository.NewFilterRepository(db), repository.NewSpamFilterRepository(db), "")
			srv, err := oas.NewServer(h, oas.WithErrorHandler(handler.WriteError))
			require.NoError(t, err)

			w := httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/messages/%d", id), nil))
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var detail oas.MessageDetail
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
			require.Len(t, detail.Attachments, 1)
			att := detail.Attachments[0]

			risky := false
			for _, wn := range detail.Warnings {
				if wn.Kind == oas.MessageWarningKindRiskyAttachment {
					risky = true
				}
			}

			w = httptest.NewRecorder()
			srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/attachments/%d", att.ID), nil))
			require.Equal(t, http.StatusOK, w.Code)
			cd := w.Header().Get("Content-Disposition")
			_, params, err := mime.ParseMediaType(cd)
			require.NoError(t, err)
			saved := params["filename"]
			assert.Contains(t, []string{"invoice.exe", "evil.exe"}, att.Filename)
			assert.Equal(t, att.Filename, saved)
			assert.True(t, risky)

			t.Logf("stored=%q risky_attachment=%v served=%q browser-saves=%q", att.Filename, risky, cd, saved)
			if strings.HasSuffix(strings.ToLower(saved), ".exe") {
				assert.True(t, risky, "browser saves %q (an .exe) but the message has no risky_attachment warning, so the UI renders a plain download link with no confirmation", saved)
			}
		})
	}
}
