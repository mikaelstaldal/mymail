package web

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io/fs"
	"strings"
)

// ImportMapCSPHash reads static/index.html from staticFS, extracts the text
// content of the <script type="importmap"> element, and returns the
// base64-encoded SHA-256 hash in the form required by CSP script-src, e.g.
// "'sha256-abc123=='".  Computing at startup from the embedded bytes keeps the
// hash in sync automatically as index.html evolves.
func ImportMapCSPHash(staticFS fs.FS) (string, error) {
	data, err := fs.ReadFile(staticFS, "static/index.html")
	if err != nil {
		return "", fmt.Errorf("read index.html: %w", err)
	}

	const open = `<script type="importmap">`
	const close = `</script>`

	_, after, found := strings.Cut(string(data), open)
	if !found {
		return "", fmt.Errorf("importmap script tag not found in index.html")
	}

	content, _, found := strings.Cut(after, close)
	if !found {
		return "", fmt.Errorf("importmap closing tag not found in index.html")
	}

	sum := sha256.Sum256([]byte(content))
	return "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'", nil
}
