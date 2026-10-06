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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testSecret = "mymail_abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQ"

func TestCreateWithStdinCredentialsAndEnvironmentURL(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPost, r.Method)
		assert.Equal(t, "/api/v1/tokens", r.URL.Path)
		username, password, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "agent", username)
		assert.Equal(t, "a:secret", password)
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		var request struct {
			Name      string  `json:"name"`
			ExpiresAt string  `json:"expires_at"`
			FolderIDs []int64 `json:"folder_ids"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		assert.Equal(t, "Archive reader", request.Name)
		assert.Equal(t, []int64{1, 100}, request.FolderIDs)
		expiry, err := time.Parse(time.RFC3339, request.ExpiresAt)
		require.NoError(t, err)
		assert.WithinDuration(t, now.Add(7*24*time.Hour), expiry, 2*time.Second)
		w.WriteHeader(http.StatusCreated)
		_, _ = fmt.Fprintf(w, `{"slug":"archive-reader","token":"%s"}`, testSecret)
	}))
	defer server.Close()
	t.Setenv("MYMAIL_URL", server.URL)
	var stdout, stderr bytes.Buffer
	err := run([]string{"-credentials-stdin", "create", "-name", "Archive reader", "-lifetime", "7d", "-folders", "1,100"}, strings.NewReader("agent:a:secret\n"), &stdout, &stderr)
	require.NoError(t, err)
	assert.Equal(t, testSecret+"\n", stdout.String())
	assert.Equal(t, "Token slug: archive-reader\n", stderr.String())
}

func TestRevokeWithCredentialsFile(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	file := t.TempDir() + "/credentials"
	require.NoError(t, os.WriteFile(file, []byte("agent:password\n"), 0600))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/api/v1/tokens/archive-reader", r.URL.Path)
		username, password, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "agent", username)
		assert.Equal(t, "password", password)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run([]string{"-url", server.URL, "-credentials-file", file, "revoke", "archive-reader"}, strings.NewReader(""), &stdout, &stderr)
	require.NoError(t, err)
	assert.Empty(t, stdout.String())
	assert.Equal(t, "Revoked token archive-reader\n", stderr.String())
}

func TestInvalidInput(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	tests := [][]string{
		{"-credentials-stdin", "create", "-name", "bad", "-lifetime", "0d", "-folders", "1"},
		{"-credentials-stdin", "create", "-name", "bad", "-lifetime", "11y", "-folders", "1"},
		{"-credentials-stdin", "create", "-name", "bad", "-lifetime", "3651d", "-folders", "1"},
		{"-credentials-stdin", "create", "-name", "bad", "-lifetime", "1d", "-folders", "1,1"},
		{"-credentials-stdin", "create", "-name", "bad", "-lifetime", "1d", "-folders", "01"},
		{"-credentials-stdin", "revoke", "../secret"},
		{"-user", "agent", "-credentials-stdin", "revoke", "valid-slug"},
	}
	for _, args := range tests {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			err := run(args, strings.NewReader("agent:password\n"), &stdout, &stderr)
			require.Error(t, err)
			assert.Empty(t, stdout.String())
		})
	}
	for _, credentials := range []string{"", "agent", ":password", "agent:", "agent:password\nextra", strings.Repeat("x", 4097)} {
		_, _, err := readCredentials(strings.NewReader(credentials), io.Discard)
		require.Error(t, err)
	}
}

func TestResponseErrorAndRedirect(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	var followed bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	err := run([]string{"-url", server.URL, "-credentials-stdin", "revoke", "token"}, strings.NewReader("agent:password"), &stdout, &stderr)
	require.ErrorContains(t, err, "HTTP 307")
	assert.False(t, followed)
	assert.Empty(t, stdout.String())
}

func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	require.NoError(t, run([]string{"help"}, strings.NewReader(""), &stdout, &stderr))
	assert.Contains(t, stdout.String(), "-credentials-stdin")
	stdout.Reset()
	require.NoError(t, run([]string{"create", "-h"}, strings.NewReader(""), &stdout, &stderr))
	assert.Contains(t, stdout.String(), "create -name")
}

func TestNoAuthenticationAndUserPassword(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/tokens/anonymous" {
			assert.Empty(t, r.Header.Get("Authorization"))
		} else {
			user, password, ok := r.BasicAuth()
			assert.True(t, ok)
			assert.Equal(t, "agent", user)
			assert.Equal(t, "password", password)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	t.Setenv("MYMAIL_USER", "")
	var stdout, stderr bytes.Buffer
	require.NoError(t, run([]string{"-url", server.URL, "revoke", "anonymous"}, strings.NewReader(""), &stdout, &stderr))

	t.Setenv("MYMAIL_USER", "agent")
	stdout.Reset()
	stderr.Reset()
	require.NoError(t, run([]string{"-url", server.URL, "revoke", "named-user"}, strings.NewReader("password\n"), &stdout, &stderr))
}

func TestCredentialsFileAndStdinWithOrWithoutNewline(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		assert.True(t, ok)
		assert.Equal(t, "agent", user)
		assert.Equal(t, "password", password)
		w.WriteHeader(http.StatusNoContent)
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
					file := t.TempDir() + "/credentials"
					require.NoError(t, os.WriteFile(file, []byte("agent:password"+suffix), 0600))
					args = append(args, "-credentials-file", file)
				} else {
					args = append(args, "-credentials-stdin")
					input = strings.NewReader("agent:password" + suffix)
				}
				args = append(args, "revoke", "sample")
				var stdout, stderr bytes.Buffer
				require.NoError(t, run(args, input, &stdout, &stderr))
				assert.Empty(t, stdout.String())
			})
		}
	}
}
