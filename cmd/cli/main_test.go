package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCommands(t *testing.T) {
	tests := []struct {
		args   []string
		method string
		uri    string
		body   string
	}{
		{[]string{"folders", "list"}, "GET", "/api/v1/folders", `{"items":[]}`},
		{[]string{"messages", "list", "7", "-limit", "2", "-offset", "3", "-unread", "-flagged"}, "GET", "/api/v1/folders/7/messages?flagged=true&limit=2&offset=3&unread=true", `{"items":[]}`},
		{[]string{"messages", "search", "-folder", "7", "-q", "hello world", "-sort", "date_desc"}, "GET", "/api/v1/messages/search?folder_id=7&limit=50&offset=0&q=hello+world&sort=date_desc", `{"items":[]}`},
		{[]string{"messages", "get", "9"}, "GET", "/api/v1/messages/9", `{"id":9}`},
		{[]string{"messages", "raw", "9"}, "GET", "/api/v1/messages/9/raw", "raw\x00bytes"},
		{[]string{"messages", "headers", "9"}, "GET", "/api/v1/messages/9/headers", "Subject: test\r\n"},
		{[]string{"messages", "body", "9", "-external"}, "GET", "/api/v1/messages/9/body?external=1", "<html></html>"},
		{[]string{"messages", "read", "9"}, "PUT", "/api/v1/messages/9/read", ""},
		{[]string{"attachments", "get", "11"}, "GET", "/api/v1/attachments/11", "file\x00bytes"},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, "_"), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, tt.method, r.Method)
				assert.Equal(t, tt.uri, r.URL.RequestURI())
				assert.Equal(t, "Bearer mymail_secret", r.Header.Get("Authorization"))
				if tt.method == "PUT" {
					assert.Equal(t, int64(0), r.ContentLength)
					w.WriteHeader(http.StatusNoContent)
					return
				}
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			args := append([]string{"-url", server.URL, "-token-stdin"}, tt.args...)
			var output bytes.Buffer
			err := run(args, strings.NewReader("mymail_secret\n"), &output)
			require.NoError(t, err)
			assert.Equal(t, tt.body, output.String())
		})
	}
}

func TestTokenFileAndHTTPError(t *testing.T) {
	file := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(file, []byte("mymail_secret\n"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer mymail_secret", r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"error":"out of scope"}`)
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run([]string{"-url", server.URL, "-token-file", file, "folders", "list"}, strings.NewReader(""), &output)
	require.ErrorContains(t, err, "HTTP 403")
	assert.ErrorContains(t, err, "out of scope")
	assert.Empty(t, output.String())
}

func TestRejectUnsafeOrInvalidRequests(t *testing.T) {
	tests := [][]string{
		{"-url", "http://example.com", "-token-stdin", "folders", "list"},
		{"-url", "https://user:pass@example.com", "-token-stdin", "folders", "list"},
		{"-token-stdin", "messages", "search", "-q", "test"},
		{"-token-stdin", "messages", "search", "-folder", "2", "-q", " "},
		{"-token-stdin", "messages", "list", "1", "-limit", "201"},
		{"-token-stdin", "messages", "get", "-1"},
		{"-token-stdin", "messages", "text"},
		{"-token-stdin", "messages", "text", "0"},
		{"-token-stdin", "messages", "text", "9", "extra"},
		{"-token-stdin", "messages", "text", "9", "-external"},
		{"-token-stdin", "messages", "get", "1", "extra"},
		{"-token-stdin", "messages", "list", "1", "extra"},
		{"-token-stdin", "messages", "search", "-folder", "1", "-q", "test", "extra"},
		{"-token-stdin", "messages", "delete", "1"},
		{"folders", "list"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var output bytes.Buffer
			err := run(args, strings.NewReader("mymail_secret\n"), &output)
			require.Error(t, err)
			assert.Empty(t, output.String())
		})
	}
}

func TestRedirectDoesNotReceiveToken(t *testing.T) {
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		followed = true
	}))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	var output bytes.Buffer
	err := run([]string{"-url", server.URL, "-token-stdin", "folders", "list"}, strings.NewReader("mymail_secret"), &output)
	require.ErrorContains(t, err, "HTTP 307")
	assert.False(t, followed)
}

func TestHelp(t *testing.T) {
	var output bytes.Buffer
	require.NoError(t, run([]string{"help"}, strings.NewReader(""), &output))
	assert.Contains(t, output.String(), "messages search")
}

func TestEnvironmentURLAndFlagOverride(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
	}))
	defer server.Close()
	t.Setenv("MYMAIL_URL", server.URL)
	var output bytes.Buffer
	require.NoError(t, run([]string{"-token-stdin", "folders", "list"}, strings.NewReader("mymail_secret"), &output))
	assert.Contains(t, output.String(), `"items":[]`)

	t.Setenv("MYMAIL_URL", "http://example.com")
	output.Reset()
	require.NoError(t, run([]string{"-url", server.URL, "-token-stdin", "folders", "list"}, strings.NewReader("mymail_secret"), &output))
}

func TestServerURLWithPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/suite/mymail/api/v1/messages/search", r.URL.Path)
		assert.Equal(t, "hello world", r.URL.Query().Get("q"))
		assert.Equal(t, "Bearer mymail_secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
	}))
	defer server.Close()
	for _, suffix := range []string{"", "/"} {
		for _, source := range []string{"flag", "environment"} {
			t.Run(source+suffix, func(t *testing.T) {
				args := []string{"-token-stdin"}
				baseURL := server.URL + "/suite/mymail" + suffix
				if source == "flag" {
					args = append(args, "-url", baseURL)
				} else {
					t.Setenv("MYMAIL_URL", baseURL)
				}
				args = append(args, "messages", "search", "-folder", "1", "-q", "hello world")
				var output bytes.Buffer
				require.NoError(t, run(args, strings.NewReader("mymail_secret"), &output))
			})
		}
	}
}

func TestUnicodeSearchLimits(t *testing.T) {
	queryText := strings.Repeat("å", 300)
	addressText := strings.Repeat("é", 150)
	_, _, query, err := parseCommand([]string{"messages", "search", "-folder", "1", "-q", queryText, "-from", addressText})
	require.NoError(t, err)
	assert.Equal(t, queryText, query.Get("q"))
	assert.Equal(t, addressText, query.Get("from_addr"))
}

func TestTokenInputLimit(t *testing.T) {
	var output bytes.Buffer
	largeToken := strings.Repeat("x", 4097)
	err := run([]string{"-token-stdin", "folders", "list"}, strings.NewReader(largeToken), &output)
	require.ErrorContains(t, err, "too large")

	file := t.TempDir() + "/token"
	require.NoError(t, os.WriteFile(file, []byte(largeToken), 0600))
	err = run([]string{"-token-file", file, "folders", "list"}, strings.NewReader(""), &output)
	require.ErrorContains(t, err, "too large")
}

func TestTokenFileAndStdinWithOrWithoutNewline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer mymail_secret", r.Header.Get("Authorization"))
		_, _ = w.Write([]byte(`{"total":0,"items":[]}`))
	}))
	defer server.Close()
	for _, source := range []string{"file", "stdin"} {
		for _, suffix := range []string{"", "\n"} {
			name := source + "_without_newline"
			if suffix != "" {
				name = source + "_with_newline"
			}
			t.Run(name, func(t *testing.T) {
				args := []string{"-url", server.URL}
				input := strings.NewReader("")
				if source == "file" {
					file := t.TempDir() + "/token"
					require.NoError(t, os.WriteFile(file, []byte("mymail_secret"+suffix), 0600))
					args = append(args, "-token-file", file)
				} else {
					args = append(args, "-token-stdin")
					input = strings.NewReader("mymail_secret" + suffix)
				}
				args = append(args, "folders", "list")
				var output bytes.Buffer
				require.NoError(t, run(args, input, &output))
				assert.Equal(t, `{"total":0,"items":[]}`, output.String())
			})
		}
	}
}

func TestMessageText(t *testing.T) {
	for _, text := range []string{"", "Hello", "  å\r\n\"quoted\"\t\n"} {
		t.Run(text, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/api/v1/messages/9", r.URL.RequestURI())
				assert.Equal(t, "Bearer mymail_secret", r.Header.Get("Authorization"))
				require.NoError(t, json.NewEncoder(w).Encode(map[string]any{"body_text": text, "subject": "excluded", "body_html": "<p>excluded</p>"}))
			}))
			defer server.Close()
			var output bytes.Buffer
			require.NoError(t, run([]string{"-url", server.URL, "-token-stdin", "messages", "text", "9"}, strings.NewReader("mymail_secret"), &output))
			assert.Equal(t, text, output.String())
		})
	}
}

func TestMessageTextErrors(t *testing.T) {
	for _, tt := range []struct {
		name      string
		status    int
		body      string
		errorText string
	}{
		{"malformed JSON", 200, "{", "decode message"},
		{"wrong body type", 200, `{"body_text":123}`, "decode message"},
		{"out of scope", 403, `{"error":"out of scope"}`, "HTTP 403"},
		{"missing message", 404, `{"error":"not found"}`, "HTTP 404"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			defer server.Close()
			var output bytes.Buffer
			err := run([]string{"-url", server.URL, "-token-stdin", "messages", "text", "9"}, strings.NewReader("mymail_secret"), &output)
			require.ErrorContains(t, err, tt.errorText)
			assert.Empty(t, output.String())
		})
	}
}
