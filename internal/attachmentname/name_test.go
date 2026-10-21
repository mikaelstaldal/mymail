package attachmentname

import (
	"encoding/base64"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestNormalize(t *testing.T) {
	for _, name := range []string{"invoice.e\nxe", "invoice.exe\x00", "invoice.exe\"", "invoice.e\"xe", "invoice.e\\xe", "=?utf-8?b?aW52b2ljZS5leGU=?=", "invoice.e\u202exe", "invoice.e/xe", "invoice.=?utf-8?q?ex?= =?utf-8?q?e?="} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "invoice.exe", Normalize(name))
			assert.Equal(t, Normalize(name), Normalize(Normalize(name)))
		})
	}
	assert.Empty(t, Normalize("\x00\u202e\\/\""))
	assert.Equal(t, "résumé.pdf", Normalize("résumé.pdf"))
}

func TestMalformedWordsAndNestedNames(t *testing.T) {
	for _, name := range []string{"=?utf-8?q?bad=xx?=", "=?utf-8?b?Z Xhl?=", "=?utf-8?b?ZXhlLg?="} {
		assert.Equal(t, name, Normalize(name))
	}
	assert.Equal(t, "\ufffd", Normalize("=?us-ascii?q?=FF?="))
	name := "invoice.exe"
	for range 10 {
		name = "=?utf-8?b?" + base64.StdEncoding.EncodeToString([]byte(name)) + "?="
	}
	assert.Equal(t, "invoice.exe", Normalize(name))
}
