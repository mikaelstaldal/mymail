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
