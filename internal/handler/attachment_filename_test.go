package handler

import (
	"mime"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/phishing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Check legacy stored names too: a migration must not be needed for protection.
func TestAttachmentRiskMatchesDownload(t *testing.T) {
	for _, name := range []string{"invoice.exe", "invoice.e\nxe", "evil.exe\x00", "invoice.exe\"", "invoice.e\"xe", "evil.e\\xe", "=?utf-8?b?aW52b2ljZS5leGU=?=", "invoice.e\u202exe"} {
		t.Run(name, func(t *testing.T) {
			header := contentDispositionFilename(name)
			_, params, err := mime.ParseMediaType(header)
			require.NoError(t, err)
			assert.NotContains(t, header, "\n")
			assert.Equal(t, "executable_extension", phishing.AttachmentRisk(name, "application/octet-stream"))
			assert.Equal(t, phishing.AttachmentRisk(name, "application/octet-stream"), phishing.AttachmentRisk(params["filename"], "application/octet-stream"))
		})
	}
}
