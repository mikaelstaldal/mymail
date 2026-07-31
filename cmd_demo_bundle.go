package main

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	commonweb "github.com/mikaelstaldal/go-server-common/web"

	"github.com/mikaelstaldal/mymail/internal/demo"
	"github.com/mikaelstaldal/mymail/web"
)

// writeDemoBundle assembles a self-contained static demo site in outDir and
// returns. The result is plain files: any web server that serves a directory
// can host it, and a service worker (web/static/demo-sw.js) stands in for the
// REST API, keeping every message, contact, and attachment in browser-local
// storage.
//
// outDir must not already exist, or must be an empty directory — the bundle is
// never merged into a populated directory, so a stale file from an earlier
// version cannot linger and no pre-existing content is overwritten.
//
// The bundle needs no -public-url: every URL the app builds is relative and it
// routes on the fragment, so the same files work at the origin root and under
// any path.
func writeDemoBundle(ctx context.Context, outDir string) error {
	if err := requireEmptyDir(outDir); err != nil {
		return err
	}

	configScript := serverConfig{Demo: true}.script()
	indexHTML, err := buildIndexHTML(web.Static, configScript)
	if err != nil {
		return fmt.Errorf("build index.html: %w", err)
	}
	importMapHash, err := commonweb.ImportMapCSPHash(web.Static)
	if err != nil {
		return fmt.Errorf("compute importmap CSP hash: %w", err)
	}
	// A static bundle has no server to set response headers, so the policy the
	// server would send travels in the page instead. frame-ancestors is omitted
	// because browsers ignore it in a meta element; a host that cares should
	// send it as a header.
	csp := "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' " +
		importMapHash + " " + inlineScriptCSPHash(configScript)
	indexHTML = injectMetaCSP(indexHTML, csp)

	seed, err := demo.BuildSeedJSON(ctx)
	if err != nil {
		return fmt.Errorf("build demo seed: %w", err)
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	files := 0
	err = fs.WalkDir(web.Static, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(path, "static"), "/")
		dest := filepath.Join(outDir, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		data, err := fs.ReadFile(web.Static, path)
		if err != nil {
			return err
		}
		if rel == "index.html" {
			data = indexHTML
		}
		files++
		return os.WriteFile(dest, data, 0o644)
	})
	if err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outDir, demo.SeedFileName), seed, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", demo.SeedFileName, err)
	}

	fmt.Printf("Wrote a static MyMail demo (%d files) to %s\n", files+1, outDir)
	fmt.Printf("Serve that directory with any web server; it needs no backend.\n")
	fmt.Printf("A service worker is required, so serve it over HTTPS or from localhost.\n")
	return nil
}

// requireEmptyDir accepts a path that does not exist or is an empty directory,
// and rejects anything else.
func requireEmptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check %s: %w", dir, err)
	}
	if len(entries) > 0 {
		return fmt.Errorf("%s is not empty; the demo bundle is only written to a new or empty directory", dir)
	}
	return nil
}

// charsetMeta is the encoding declaration in web/static/index.html, used as the
// anchor for injectMetaCSP.
const charsetMeta = `<meta charset="UTF-8">`

// injectMetaCSP inserts a Content-Security-Policy <meta> immediately after the
// charset declaration. Both positions matter: the charset has to stay first so
// encoding detection still sees it, and the policy has to come before the
// import map, which it allows by hash — a meta policy governs only what the
// parser reaches after it.
func injectMetaCSP(html []byte, csp string) []byte {
	s := string(html)
	if !strings.Contains(s, charsetMeta) {
		// index.html changed shape; a bundle whose policy silently covers
		// nothing would be worse than one with no policy at all.
		panic("web/static/index.html no longer contains " + charsetMeta + "; update injectMetaCSP")
	}
	meta := "\n    <meta http-equiv=\"Content-Security-Policy\" content=\"" + csp + "\">"
	return []byte(strings.Replace(s, charsetMeta, charsetMeta+meta, 1))
}

// demoAPIUnavailable answers any REST API request that reaches the server in
// demo mode, which only happens when the browser's service worker is not
// installed or not in control.
func demoAPIUnavailable(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(`{"error":"demo mode: the in-browser backend is not running; reload the page"}`))
}

// seedHandler serves the demo seed document. It is regenerated on every server
// start and is not content-addressed, so it is revalidated rather than cached.
func seedHandler(seed []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(seed)
	}
}
