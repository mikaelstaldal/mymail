package auth

import (
	"encoding/json"
	"fmt"
	"net/http"

	serverauth "github.com/mikaelstaldal/go-server-common/auth"
)

// NewBasicAuth returns a middleware that enforces HTTP Basic Auth using the given
// htpasswd file. If htpasswdPath is empty, all requests pass through.
// GET /api/v1/health is always exempt.
func NewBasicAuth(htpasswdPath, realm string) func(http.Handler) http.Handler {
	if htpasswdPath == "" {
		return func(next http.Handler) http.Handler { return next }
	}

	htpasswd, err := serverauth.LoadHtpasswd(htpasswdPath)
	if err != nil {
		panic(fmt.Sprintf("auth: %v", err))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && r.URL.Path == "/api/v1/health" {
				next.ServeHTTP(w, r)
				return
			}
			username, password, ok := r.BasicAuth()
			if !ok || !htpasswd.Check(username, password) {
				w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm=%q`, realm))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "Unauthorized"})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// NewSecurityHeaders returns a middleware that adds standard security headers.
// importMapHash is the precomputed 'sha256-…' CSP token for the inline
// importmap in index.html (see web.ImportMapCSPHash); it is added to
// script-src so the importmap is allowed without 'unsafe-inline'.
func NewSecurityHeaders(importMapHash string) func(http.Handler) http.Handler {
	csp := "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' " + importMapHash
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", csp)
			h.Set("Strict-Transport-Security", "max-age=31536000")
			next.ServeHTTP(w, r)
		})
	}
}
