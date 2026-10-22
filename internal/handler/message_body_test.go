package handler_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageBodyBrowserIsolation(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	_, err := db.Exec(`INSERT INTO messages(id,folder_id,from_addr,body_html,raw,date,created_at,updated_at) VALUES(42,1,'sender@example.org','<img src="https://tracker.example/pixel">','raw','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, site, dest string
		blocked          bool
	}{
		{"app iframe", "same-origin", "iframe", false},
		{"app fetch", "same-origin", "empty", false},
		{"legacy client", "", "", false},
		{"cross-site popup", "cross-site", "document", true},
		{"cross-site iframe", "cross-site", "iframe", true},
		{"cross-site fetch", "cross-site", "empty", true},
		{"same-origin link", "same-origin", "document", true},
		{"same-site navigation", "same-site", "document", true},
		{"address bar", "none", "document", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, query := range []string{"", "?external=1"} {
				req := httptest.NewRequest(http.MethodGet, "/messages/42/body"+query, nil)
				req.Header.Set("Sec-Fetch-Site", tc.site)
				req.Header.Set("Sec-Fetch-Dest", tc.dest)
				w := httptest.NewRecorder()
				srv.ServeHTTP(w, req)
				if tc.blocked {
					require.Equal(t, http.StatusForbidden, w.Code)
					assert.NotContains(t, w.Body.String(), "tracker.example")
					continue
				}
				require.Equal(t, http.StatusOK, w.Code)
				csp := w.Header().Get("Content-Security-Policy")
				assert.Contains(t, csp, "; sandbox allow-popups allow-popups-to-escape-sandbox allow-downloads")
				assert.NotContains(t, csp, "allow-same-origin")
				assert.NotContains(t, csp, "allow-scripts")
				if query == "" {
					assert.Contains(t, csp, "img-src data:;")
				} else {
					assert.Contains(t, csp, "img-src https: data:;")
				}
				body, err := io.ReadAll(w.Result().Body)
				require.NoError(t, err)
				assert.Contains(t, string(body), "tracker.example")
			}
		})
	}
}
