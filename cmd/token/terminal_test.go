package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUntrustedErrorDiagnostics(t *testing.T) {
	const payload = "\x1b]52;c;evil\a\rFAKE\u009b\u202e\xff"
	for _, command := range []string{"create", "revoke", "authorization", "poll"} {
		t.Run(command, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if command == "poll" && r.URL.Path == "/api/v1/oauth/device_authorization" {
					_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device", "user_code": "ABCD-EFGH", "verification_uri": "https://example.org", "expires_in": 10, "interval": 1})
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				if command == "authorization" || command == "poll" {
					_ = json.NewEncoder(w).Encode(map[string]string{"error": payload})
				} else {
					_, _ = w.Write([]byte(payload))
				}
			}))
			defer server.Close()
			args := []string{"-url", server.URL}
			switch command {
			case "create":
				args = append(args, "create", "-name", "test", "-lifetime", "1d", "-folders", "1")
			case "revoke":
				args = append(args, "revoke", "test")
			default:
				args = append(args, "login", "-name", "test")
			}
			var stdout, stderr bytes.Buffer
			err := run(args, nil, &stdout, &stderr)
			require.Error(t, err)
			require.NoError(t, writeTokenError(&stderr, err))
			assert.Empty(t, stdout.String())
			for _, unsafe := range []string{"\x1b", "\a", "\r", "\u009b", "\u202e", "\xff"} {
				assert.NotContains(t, stderr.String(), unsafe)
			}
			assert.Contains(t, stderr.String(), "\\u001b]52;c;evil\\u0007\\u000dFAKE\\u009b\\u202e")
			if command == "create" || command == "revoke" {
				assert.Contains(t, stderr.String(), "\\xff")
			}
		})
	}
}
