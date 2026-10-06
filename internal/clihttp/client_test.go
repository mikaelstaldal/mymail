package clihttp

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPIURLPreservesDeploymentPath(t *testing.T) {
	for _, prefix := range []string{"/mymail", "/mymail/", "/suite/mail", "/mail%2Fprivate"} {
		t.Run(prefix, func(t *testing.T) {
			base, err := ParseBaseURL("https://mail.example.com" + prefix)
			require.NoError(t, err)
			expectedPrefix := prefix
			if prefix == "/mymail/" {
				expectedPrefix = "/mymail"
			}
			assert.Equal(t, "https://mail.example.com"+expectedPrefix+"/api/v1/messages/search?q=hello+world", APIURL(base, "/messages/search", url.Values{"q": {"hello world"}}))
		})
	}
}

func TestURLDefault(t *testing.T) {
	t.Setenv("MYMAIL_URL", "")
	assert.Equal(t, DefaultURL, URLDefault())
	t.Setenv("MYMAIL_URL", "https://mail.example.com")
	assert.Equal(t, "https://mail.example.com", URLDefault())
}

func TestParseBaseURL(t *testing.T) {
	for _, value := range []string{
		"http://127.0.0.1:8080", "http://[::1]:8080", "https://mail.example.com",
		"https://mail.example.com/mymail", "https://mail.example.com/mymail/",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseBaseURL(value)
			require.NoError(t, err)
		})
	}
	for _, value := range []string{
		"http://example.com", "http://localhost:8080", "http://127.0.0.1.evil.example",
		"http://127.0.0.1@evil.example", "http://127.0.0.1:80@evil.example",
		"http://127.0.0.1:bad", "http://127.0.0.1?redirect=evil",
		"http://127.0.0.1?", "https://user:pass@example.com",
		"https://mail.example.com\\evil.example",
		"https://mail.example.com/ space", "http://127.0.0.1%2eevil.example",
	} {
		t.Run(value, func(t *testing.T) {
			_, err := ParseBaseURL(value)
			require.Error(t, err)
		})
	}
}
