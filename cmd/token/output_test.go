package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateOutputFile(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	for _, response := range []string{"success", "http-error", "invalid-token"} {
		t.Run(response, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				assert.Equal(t, http.MethodPost, r.Method)
				if response == "http-error" {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.WriteHeader(http.StatusCreated)
				if response == "success" {
					_, _ = fmt.Fprintf(w, `{"slug":"reader","token":%q}`, testSecret)
				} else {
					_, _ = fmt.Fprint(w, `{}`)
				}
			}))
			defer server.Close()
			path := filepath.Join(t.TempDir(), "token")
			args := []string{"-url", server.URL, "create", "-name", "reader", "-lifetime", "1m", "-folders", "1", "-out", path}
			var stdout, stderr bytes.Buffer
			err := run(args, strings.NewReader(""), &stdout, &stderr)
			assert.Empty(t, stdout.String())
			assert.Equal(t, 1, requests)
			if response != "success" {
				require.Error(t, err)
				_, statErr := os.Stat(path)
				assert.True(t, os.IsNotExist(statErr))
				return
			}
			require.NoError(t, err)
			contents, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, testSecret+"\n", string(contents))
			info, err := os.Stat(path)
			require.NoError(t, err)
			assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
			assert.Equal(t, "Token slug: reader\n", stderr.String())
			stdout.Reset()
			require.Error(t, run(args, strings.NewReader(""), &stdout, &stderr))
			assert.Equal(t, 1, requests, "existing output must prevent token issuance")
			contents, err = os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, testSecret+"\n", string(contents))
		})
	}
}

func TestCreateOutputCredentialFailure(t *testing.T) {
	t.Setenv("MYMAIL_USER", "")
	path := filepath.Join(t.TempDir(), "token")
	var stdout, stderr bytes.Buffer
	err := run([]string{"-credentials-stdin", "create", "-name", "reader", "-lifetime", "1m", "-folders", "1", "-out", path}, strings.NewReader("invalid credentials"), &stdout, &stderr)
	require.Error(t, err)
	_, statErr := os.Stat(path)
	assert.True(t, os.IsNotExist(statErr))
	assert.Empty(t, stdout.String())
}
