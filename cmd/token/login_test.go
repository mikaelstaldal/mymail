package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type unreadableInput struct{}

func (unreadableInput) Read([]byte) (int, error) { panic("login read credentials") }

func TestLoginCommand(t *testing.T) {
	t.Setenv("MYMAIL_USER", "must-not-prompt")
	secret := "mymail_" + strings.Repeat("a", 43)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		require.NoError(t, r.ParseForm())
		assert.Empty(t, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/prefix/api/v1/oauth/device_authorization":
			assert.Equal(t, "mymail-token", r.PostForm.Get("client_id"))
			assert.Equal(t, "folder:1", r.PostForm.Get("scope"))
			assert.Equal(t, "30d", r.PostForm.Get("lifetime"))
			_ = json.NewEncoder(w).Encode(map[string]any{"device_code": "device", "user_code": "BCDF-GHJK", "verification_uri": "https://example.org/approve", "expires_in": 10, "interval": 1})
		case "/prefix/api/v1/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": secret, "token_type": "Bearer", "expires_in": 100, "slug": "reader"})
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	var stdout, stderr bytes.Buffer
	file := filepath.Join(t.TempDir(), "token")
	args := []string{"-url", server.URL + "/prefix", "login", "-name", "Reader", "-folders", "1", "-out", file}
	require.NoError(t, run(args, unreadableInput{}, &stdout, &stderr))
	assert.Empty(t, stdout.String())
	assert.Contains(t, stderr.String(), "User code: BCDF-GHJK")
	assert.Contains(t, stderr.String(), "Token slug: reader")
	content, err := os.ReadFile(file)
	require.NoError(t, err)
	assert.Equal(t, secret+"\n", string(content))
	info, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
	assert.Equal(t, 2, requests)
}
func TestLoginRejectsCredentials(t *testing.T) {
	for _, args := range [][]string{{"-user", "alice", "login", "-name", "Reader"}, {"-credentials-stdin", "login", "-name", "Reader"}, {"-credentials-file", "not-a-file", "login", "-name", "Reader"}} {
		err := run(args, unreadableInput{}, new(bytes.Buffer), new(bytes.Buffer))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not accept credentials")
	}
}

func TestLoginOutputFailureBeforeAuthorization(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"access_denied"}`))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(path, []byte("keep"), 0600))
	args := []string{"-url", server.URL, "login", "-name", "Reader", "-out", path}
	err := run(args, unreadableInput{}, new(bytes.Buffer), new(bytes.Buffer))
	require.Error(t, err)
	assert.Zero(t, requests)
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "keep", string(content))
	require.NoError(t, os.Remove(path))
	err = run(args, unreadableInput{}, new(bytes.Buffer), new(bytes.Buffer))
	require.Error(t, err)
	assert.Equal(t, 1, requests)
	_, err = os.Stat(path)
	assert.True(t, os.IsNotExist(err))
}

func TestLoginRejectsInvalidNamesBeforeAuthorization(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests++; w.WriteHeader(500) }))
	defer server.Close()
	for _, name := range []string{"Bad\u202ename", string([]byte{0xff})} {
		err := run([]string{"-url", server.URL, "login", "-name", name}, unreadableInput{}, new(bytes.Buffer), new(bytes.Buffer))
		require.Error(t, err)
	}
	assert.Zero(t, requests)
}
