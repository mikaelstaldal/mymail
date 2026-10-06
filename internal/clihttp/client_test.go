package clihttp

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestURLDefault(t *testing.T) {
	t.Setenv("MYMAIL_URL", "")
	assert.Equal(t, DefaultURL, URLDefault())
	t.Setenv("MYMAIL_URL", "https://mail.example.com")
	assert.Equal(t, "https://mail.example.com", URLDefault())
}

func TestParseOrigin(t *testing.T) {
	for _, value := range []string{
		"http://127.0.0.1:8080", "http://[::1]:8080", "https://mail.example.com",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseOrigin(value)
			require.NoError(t, err)
		})
	}
	for _, value := range []string{
		"http://example.com", "http://localhost:8080", "http://127.0.0.1.evil.example",
		"http://127.0.0.1@evil.example", "http://127.0.0.1:80@evil.example",
		"http://127.0.0.1:bad", "http://127.0.0.1?redirect=evil",
		"http://127.0.0.1?", "https://user:pass@example.com",
		"https://mail.example.com/path", "https://mail.example.com\\evil.example",
		"https://mail.example.com/ space", "http://127.0.0.1%2eevil.example",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseOrigin(value)
			require.Error(t, err)
		})
	}
}
