package main

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
)

//go:embed openapi.yaml
var openAPIDocument []byte

const apiCatalog = `{"linkset":[{"anchor":"/api/v1/","service-desc":[{"href":"/openapi.yaml","type":"application/yaml"}]}]}`

// apiDiscovery serves the public generator input and catalog before authentication.
// The paths are claimed even in demo mode so they cannot fall through to the SPA.
func apiDiscovery(next http.Handler, demoMode bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openapi.yaml" && r.URL.Path != "/.well-known/api-catalog" {
			next.ServeHTTP(w, r)
			return
		}
		if demoMode {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		body := openAPIDocument
		w.Header().Set("Content-Type", "application/yaml")
		if r.URL.Path == "/.well-known/api-catalog" {
			w.Header().Set("Content-Type", `application/linkset+json; profile="https://www.rfc-editor.org/info/rfc9727"`)
			w.Header().Set("Link", `</.well-known/api-catalog>; rel="api-catalog"`)
			body = []byte(apiCatalog)
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		if r.Method == http.MethodGet {
			_, _ = w.Write(body)
		}
	})
}

// apiDescriptionLink wraps even middleware that can reject an API request.
func apiDescriptionLink(next http.Handler, demoMode bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !demoMode && strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.Header().Add("Link", `</openapi.yaml>; rel="service-desc"`)
		}
		next.ServeHTTP(w, r)
	})
}
