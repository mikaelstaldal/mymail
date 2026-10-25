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
	"golang.org/x/net/html"
)

// renderedAnchors parses the document exactly as the message-body iframe does.
// The served document starts with <!DOCTYPE html>, so the parser runs in
// no-quirks (standards) mode, as the browser does. It returns (text, href) pairs.
func renderedAnchors(t *testing.T, doc string) [][2]string {
	t.Helper()
	root, err := html.Parse(strings.NewReader(doc))
	require.NoError(t, err)
	var text func(*html.Node) string
	text = func(n *html.Node) string {
		if n.Type == html.TextNode {
			return n.Data
		}
		var b strings.Builder
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			b.WriteString(text(c))
		}
		return b.String()
	}
	var out [][2]string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			out = append(out, [2]string{text(n), href})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// A link whose rendered text is "paypal.com" but whose href is evil.example must
// raise a deceptive_link warning and be routed through the #/link confirmation
// page. It fails while Links/RewriteLinks parse the stored fragment in quirks
// mode, where <table> does not close the open <p>/<a> and the analysed anchor
// text becomes "paypal.com more".
func TestDeceptiveLinkQuirksModeMismatch(t *testing.T) {
	raw := "From: Support <support@evil.example>\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Verify your account\r\n" +
		"Date: Mon, 19 Oct 2026 10:00:00 +0000\r\n" +
		"Message-ID: <quirks-repro@evil.example>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		`<p><a href="https://evil.example/">paypal.com<table><tr><td> more</td></tr></table></a></p>` + "\r\n"

	// The real LDA parse + sanitize pipeline, as for any delivered message.
	pm, err := lda.ParseMessage([]byte(raw))
	require.NoError(t, err)
	require.NotEmpty(t, pm.BodyHTML)
	t.Logf("stored body_html: %s", pm.BodyHTML)

	db := openTestDB(t)
	srv := newServer(t, db)
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,from_addr,body_html,raw,date,created_at,updated_at)
		VALUES(42,1,'Support <support@evil.example>',?,?,'2026-10-19T10:00:00Z','2026-10-19T10:00:00Z','2026-10-19T10:00:00Z')`,
		pm.BodyHTML, raw)
	require.NoError(t, err)

	// Warnings as the UI receives them.
	res := do(srv, http.MethodGet, "/messages/42", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var detail api.MessageDetail
	decodeJSON(t, res, &detail)
	kinds := []api.MessageWarningKind{}
	for _, w := range detail.Warnings {
		kinds = append(kinds, w.Kind)
	}
	t.Logf("warnings: %v", kinds)

	// Body as the sandboxed iframe receives it.
	res = do(srv, http.MethodGet, "/messages/42/body", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	served, err := io.ReadAll(res.Body)
	require.NoError(t, err)

	anchors := renderedAnchors(t, string(served))
	t.Logf("anchors in the rendered (standards-mode) DOM: %q", anchors)

	require.Equal(t, [][2]string{{"paypal.com", "../../../../#/link/42/0"}}, anchors)

	assert.Contains(t, kinds, api.MessageWarningKindDeceptiveLink)
}
