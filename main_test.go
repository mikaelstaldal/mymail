package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// aliceHash is bcrypt (cost 5) of "s3cret", as `htpasswd -B` writes it.
const aliceHash = "$2y$05$YjuAjU9iJvJ8KhdNm5UFSurCx2d/Y4BqmPeg/3t1ybsqlysbbwtQ6"

// apr1Hash is a password `htpasswd` wrote without -B. It is the case that
// separates the strict loader from the forgiving one: LoadHtpasswd skips a
// non-bcrypt line and reports no error, leaving a login the operator believes
// in but that cannot be used.
const apr1Hash = "$apr1$9UluwDXh$yEfYy6J3T9gi5Lp2M2pcF0"

func writeHtpasswd(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "htpasswd")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// TestLoadAuthMiddlewareRefusesUnusableFile pins that the server reads its
// htpasswd file strictly, so an operator learns at startup that a login does
// not exist rather than at the first attempt to use it.
func TestLoadAuthMiddlewareRefusesUnusableFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wants   string
	}{
		{"line that is not a pair", "alice:" + aliceHash + "\ngarbage\n", "line 2"},
		{"hash that is not bcrypt", "bob:" + apr1Hash + "\n", "bcrypt"},
		{"duplicate username", "alice:" + aliceHash + "\nalice:" + aliceHash + "\n", "duplicate"},
		{"no entries", "\n\n", "no entries"},
		{"missing file", "", "open htpasswd file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent")
			if tt.name != "missing file" {
				path = writeHtpasswd(t, tt.content)
			}
			middleware, err := loadAuthMiddleware(path, "mymail")
			require.Error(t, err)
			assert.Nil(t, middleware)
			assert.Contains(t, err.Error(), tt.wants)
		})
	}
}

// TestLoadAuthMiddlewareChallenge pins the 401 an unauthenticated request gets:
// the JSON error body openapi.yaml promises, alongside the Basic challenge the
// browser needs to prompt.
func TestLoadAuthMiddlewareChallenge(t *testing.T) {
	path := writeHtpasswd(t, "alice:"+aliceHash+"\n")
	middleware, err := loadAuthMiddleware(path, "mymail")
	require.NoError(t, err)

	handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name     string
		user     string
		password string
		wantCode int
	}{
		{"no credentials", "", "", http.StatusUnauthorized},
		{"wrong password", "alice", "wrong", http.StatusUnauthorized},
		{"unknown user", "mallory", "s3cret", http.StatusUnauthorized},
		{"correct credentials", "alice", "s3cret", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/folders", nil)
			if tt.user != "" {
				req.SetBasicAuth(tt.user, tt.password)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.Equal(t, tt.wantCode, rec.Code)
			if tt.wantCode != http.StatusUnauthorized {
				return
			}
			assert.Equal(t, `Basic realm="mymail"`, rec.Header().Get("WWW-Authenticate"))
			assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			assert.JSONEq(t, `{"error":"unauthorized"}`, strings.TrimSpace(rec.Body.String()))
		})
	}
}
