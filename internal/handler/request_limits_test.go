package handler_test

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteRequestLimits(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	for _, path := range []string{"/drafts", "/drafts/123", "/messages/send"} {
		for _, field := range []string{"body_text", "body_html", "in_reply_to", "references"} {
			t.Run(path+"/"+field, func(t *testing.T) {
				body := map[string]any{"to_addr": "a@example.com"}
				switch field {
				case "body_text", "body_html":
					body[field] = strings.Repeat("a", 8388609)
				case "in_reply_to":
					body[field] = strings.Repeat("a", 999)
				case "references":
					body[field] = make([]string, 1001)
				}
				data, err := json.Marshal(body)
				require.NoError(t, err)
				method := http.MethodPost
				if path == "/drafts/123" {
					method = http.MethodPut
				}
				res := do(srv, method, path, string(data))
				defer res.Body.Close()
				assert.Equal(t, http.StatusBadRequest, res.StatusCode)
			})
		}
	}
	for _, path := range []string{"/identities", "/identities/123"} {
		for _, field := range []string{"name", "address"} {
			t.Run(path+"/"+field, func(t *testing.T) {
				body := map[string]string{"name": "Test", "address": "test@example.com"}
				limit := 200
				if field == "address" {
					limit = 254
				}
				body[field] = strings.Repeat("a", limit+1)
				data, err := json.Marshal(body)
				require.NoError(t, err)
				method := http.MethodPost
				if path != "/identities" {
					method = http.MethodPut
				}
				res := do(srv, method, path, string(data))
				defer res.Body.Close()
				assert.Equal(t, http.StatusBadRequest, res.StatusCode)
			})
		}
	}
	for _, path := range []string{"/folders/reorder", "/filters/reorder", "/identities/reorder"} {
		data, err := json.Marshal(map[string]any{"ids": make([]int, 1001)})
		require.NoError(t, err)
		res := do(srv, http.MethodPatch, path, string(data))
		res.Body.Close()
		assert.Equal(t, http.StatusBadRequest, res.StatusCode)
	}
	res := do(srv, http.MethodPost, "/drafts", `{"references":["`+strings.Repeat("a", 999)+`"]}`)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestMultipartAttachmentCountLimit(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	for _, path := range []string{"/messages/send-with-attachments", "/drafts-with-attachments", "/drafts-with-attachments/123"} {
		t.Run(path, func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			require.NoError(t, writer.WriteField("message", `{"to_addr":"test@example.com"}`))
			for range 101 {
				part, err := writer.CreateFormFile("attachments", "test.txt")
				require.NoError(t, err)
				_, err = part.Write([]byte("test"))
				require.NoError(t, err)
			}
			require.NoError(t, writer.Close())
			method := http.MethodPost
			if path == "/drafts-with-attachments/123" {
				method = http.MethodPut
			}
			req := httptest.NewRequest(method, path, &body)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			recorder := httptest.NewRecorder()
			srv.ServeHTTP(recorder, req)
			assert.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
