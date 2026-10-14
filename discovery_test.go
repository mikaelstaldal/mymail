package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-faster/yaml"
	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIDiscovery(t *testing.T) {
	disk, err := os.ReadFile("openapi.yaml")
	require.NoError(t, err)
	var document struct {
		Paths   map[string]any `yaml:"paths"`
		OpenAPI string         `yaml:"openapi"`
		Servers []struct {
			URL string `yaml:"url"`
		} `yaml:"servers"`
	}
	require.NoError(t, yaml.Unmarshal(disk, &document))
	assert.Equal(t, "3.0.3", document.OpenAPI)
	require.NotEmpty(t, document.Servers)
	assert.Equal(t, "/api/v1", document.Servers[0].URL)
	base, err := url.Parse("https://example.test/openapi.yaml")
	require.NoError(t, err)
	server, err := url.Parse(document.Servers[0].URL)
	require.NoError(t, err)
	require.False(t, server.IsAbs())
	require.Contains(t, document.Paths, "/folders")
	operation := base.ResolveReference(server).JoinPath("folders")
	db, err := repository.OpenDB(filepath.Join(t.TempDir(), "mymail.sqlite"), 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	apiHandler, err := api.NewServer(handler.New(repository.NewFolderRepository(db), nil, nil, nil, nil, nil, nil, nil, ""), api.WithPathPrefix("/api/v1"))
	require.NoError(t, err)
	mux := http.NewServeMux()
	mux.Handle("/api/v1/", apiHandler)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, operation.String(), nil))
	assert.Equal(t, http.StatusOK, response.Code)

	basic, err := loadAuthMiddleware(writeHtpasswd(t, "alice:"+aliceHash+"\n"), "mymail")
	require.NoError(t, err)
	for _, demoMode := range []bool{false, true} {
		for _, store := range []*token.Store{nil, {}} {
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
			h := apiDescriptionLink(apiDiscovery(selectAuthentication(next, basic, store, func(h http.Handler) http.Handler { return h }), demoMode), demoMode)
			for _, path := range []string{"/openapi.yaml", "/.well-known/api-catalog"} {
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					for _, auth := range []string{"", "Bearer invalid"} {
						r := httptest.NewRequest(method, path, nil)
						r.Header.Set("Authorization", auth)
						w := httptest.NewRecorder()
						h.ServeHTTP(w, r)
						if demoMode {
							assert.Equal(t, http.StatusNotFound, w.Code)
							continue
						}
						assert.Equal(t, http.StatusOK, w.Code)
						if path == "/openapi.yaml" {
							assert.Equal(t, "application/yaml", w.Header().Get("Content-Type"))
							if method == http.MethodGet {
								assert.Equal(t, disk, w.Body.Bytes())
							}
						} else {
							assert.Equal(t, `application/linkset+json; profile="https://www.rfc-editor.org/info/rfc9727"`, w.Header().Get("Content-Type"))
							assert.Equal(t, `</.well-known/api-catalog>; rel="api-catalog"`, w.Header().Get("Link"))
							if method == http.MethodGet {
								assert.JSONEq(t, `{"linkset":[{"anchor":"/api/v1/","service-desc":[{"href":"/openapi.yaml","type":"application/yaml"}]}]}`, w.Body.String())
							}
						}
						if method == http.MethodHead {
							assert.Empty(t, w.Body.String())
						}
					}
				}
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
			assert.Equal(t, http.StatusUnauthorized, w.Code)
			if demoMode {
				assert.Empty(t, w.Header().Get("Link"))
			} else {
				assert.Equal(t, `</openapi.yaml>; rel="service-desc"`, w.Header().Get("Link"))
			}
		}
	}
}

func TestAPIDescriptionLinkStatuses(t *testing.T) {
	for _, status := range []int{200, 400, 403, 404, 500, 503} {
		h := apiDescriptionLink(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }), false)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/folders", nil))
		assert.Equal(t, status, w.Code)
		assert.Equal(t, `</openapi.yaml>; rel="service-desc"`, w.Header().Get("Link"))
	}
}

func TestDemoAPIDiscoveryLinkAbsent(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/", demoAPIUnavailable)
	h := apiDescriptionLink(apiDiscovery(mux, true), true)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/folders", nil))
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
	assert.Empty(t, w.Header().Get("Link"))
}
