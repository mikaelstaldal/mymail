package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHostGuard(t *testing.T) {
	for _, tt := range []struct {
		name, addr, publicURL, host, method string
		want                                int
	}{
		{"local IPv4 read", "127.0.0.1", "", "127.0.0.1:8080", http.MethodGet, 200},
		{"local name read", "127.0.0.1", "", "localhost:8080", http.MethodGet, 200},
		{"local IPv6 read", "::1", "", "[::1]:8080", http.MethodGet, 200},
		{"other loopback address", "127.0.0.2", "", "127.0.0.2:8080", http.MethodGet, 200},
		{"wildcard local access", "0.0.0.0", "https://mail.example", "localhost:8080", http.MethodGet, 200},
		{"uppercase host", "127.0.0.1", "", "LOCALHOST:8080", http.MethodGet, 200},
		{"rebound read", "127.0.0.1", "", "attacker.example:8080", http.MethodGet, 421},
		{"rebound write", "127.0.0.1", "", "attacker.example:8080", http.MethodPost, 421},
		{"wrong port", "127.0.0.1", "", "127.0.0.1:8081", http.MethodGet, 421},
		{"missing local port", "127.0.0.1", "", "localhost", http.MethodGet, 421},
		{"userinfo", "127.0.0.1", "", "attacker.example@localhost:8080", http.MethodGet, 421},
		{"empty host", "127.0.0.1", "", "", http.MethodGet, 421},
		{"comma list", "127.0.0.1", "", "localhost:8080,attacker.example", http.MethodGet, 421},
		{"public HTTPS", "127.0.0.1", "https://mail.example", "mail.example", http.MethodGet, 200},
		{"public HTTPS explicit default port", "127.0.0.1", "https://mail.example", "mail.example:443", http.MethodGet, 200},
		{"public wrong port", "127.0.0.1", "https://mail.example", "mail.example:8080", http.MethodGet, 421},
		{"forwarded host cannot override foreign host", "127.0.0.1", "https://mail.example", "attacker.example:8080", http.MethodGet, 421},
		{"public IPv6", "127.0.0.1", "https://[2001:db8::1]", "[2001:db8:0::1]", http.MethodGet, 200},
		{"public custom port", "0.0.0.0", "https://mail.example:8443", "mail.example:8443", http.MethodGet, 200},
		{"wildcard bind is not an authority", "0.0.0.0", "https://mail.example", "0.0.0.0:8080", http.MethodGet, 421},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			h, err := hostGuard(tt.addr, 8080, tt.publicURL, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			}))
			require.NoError(t, err)
			req := httptest.NewRequest(tt.method, "/api/v1/messages/1", nil)
			req.Host = tt.host
			req.Header.Set("Origin", "http://attacker.example:8080")
			req.Header.Set("X-Forwarded-Host", "mail.example")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			assert.Equal(t, tt.want, rec.Code)
			assert.Equal(t, tt.want == 200, called)
			if tt.want == 421 {
				assert.JSONEq(t, `{"error":"invalid host"}`, rec.Body.String())
			}
		})
	}
}

func TestHostGuardRejectsUnspecifiedDeploymentWithoutPublicURL(t *testing.T) {
	for _, addr := range []string{"", "0.0.0.0", "::"} {
		_, err := hostGuard(addr, 8080, "", http.NotFoundHandler())
		require.ErrorContains(t, err, "-public-url")
	}
}

func TestHostGuardRejectsInvalidConfiguration(t *testing.T) {
	_, err := hostGuard("127.0.0.1", 0, "", http.NotFoundHandler())
	require.ErrorContains(t, err, "invalid HTTP port")
	_, err = hostGuard("127.0.0.1", 8080, "https://mail.example:bad", http.NotFoundHandler())
	require.ErrorContains(t, err, "invalid public URL")
}

func TestLocalCSRFOrigins(t *testing.T) {
	assert.Contains(t, localCSRFOrigins("127.0.0.1", 8080), "http://localhost:8080")
	assert.Contains(t, localCSRFOrigins("::1", 8080), "http://[::1]:8080")
	assert.Contains(t, localCSRFOrigins("127.0.0.2", 8080), "http://127.0.0.2:8080")
	assert.Contains(t, localCSRFOrigins("0.0.0.0", 8080), "http://localhost:8080")
	assert.Equal(t, "https://mail.example", browserOrigin("https://MAIL.EXAMPLE:443"))
}
