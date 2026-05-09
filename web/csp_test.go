package web_test

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
	"testing/fstest"

	"github.com/mikaelstaldal/mymail/web"
)

const fixtureImportMapContent = "\n{\"imports\":{}}\n"

const fixtureHTML = `<!DOCTYPE html>
<html><head>
<script type="importmap">` + fixtureImportMapContent + `</script>
</head><body></body></html>`

func TestImportMapCSPHash_HappyPath(t *testing.T) {
	fsys := fstest.MapFS{
		"static/index.html": {Data: []byte(fixtureHTML)},
	}

	got, err := web.ImportMapCSPHash(fsys)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sum := sha256.Sum256([]byte(fixtureImportMapContent))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if got != want {
		t.Errorf("hash mismatch\n got:  %s\n want: %s", got, want)
	}
}

func TestImportMapCSPHash_MissingOpenTag(t *testing.T) {
	fsys := fstest.MapFS{
		"static/index.html": {Data: []byte(`<html><body>no importmap here</body></html>`)},
	}

	_, err := web.ImportMapCSPHash(fsys)
	if err == nil {
		t.Fatal("expected error for missing importmap tag, got nil")
	}
}

func TestImportMapCSPHash_MissingCloseTag(t *testing.T) {
	fsys := fstest.MapFS{
		"static/index.html": {Data: []byte(`<html><head><script type="importmap">{"imports":{}}</html>`)},
	}

	_, err := web.ImportMapCSPHash(fsys)
	if err == nil {
		t.Fatal("expected error for missing closing script tag, got nil")
	}
}

func TestImportMapCSPHash_MissingFile(t *testing.T) {
	_, err := web.ImportMapCSPHash(fstest.MapFS{})
	if err == nil {
		t.Fatal("expected error for missing index.html, got nil")
	}
}
