package token

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testStore(t *testing.T) Store {
	t.Helper()
	db, err := repository.OpenDB(t.TempDir()+"/tokens.sqlite", 0)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0),(2,'Sent','sent',1)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,date,created_at,updated_at) VALUES(10,1,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),(20,2,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	require.NoError(t, err)
	return Store{DB: db}
}

func TestTokenLifecycle(t *testing.T) {
	s := testStore(t)
	r, value, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	assert.NotEmpty(t, value)
	var stored []byte
	require.NoError(t, s.DB.QueryRow(`SELECT token_hash FROM api_tokens WHERE id=?`, r.ID).Scan(&stored))
	assert.NotContains(t, string(stored), value)
	allowed, err := s.Validate(value)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{1: true}, allowed)
	items, err := s.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, []int64{1}, items[0].FolderIDs)
	assert.Equal(t, "reader", r.Slug)
	ok, err := s.Revoke(r.Slug)
	require.NoError(t, err)
	assert.True(t, ok)
	allowed, err = s.Validate(value)
	require.NoError(t, err)
	assert.Nil(t, allowed)
}

func TestTokenSlugCollisionAndReuse(t *testing.T) {
	s := testStore(t)
	for _, tc := range []struct{ name, slug string }{
		{"Résumé", "re-sume"},
		{"Résumé", "re-sume-2"},
		{"Résumé-2", "re-sume-2-2"},
	} {
		r, _, err := s.Create(tc.name, time.Now().Add(time.Hour), []int64{1})
		require.NoError(t, err)
		assert.Equal(t, tc.slug, r.Slug)
	}
	ok, err := s.Revoke("re-sume")
	require.NoError(t, err)
	assert.True(t, ok)
	r, _, err := s.Create("Résumé", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	assert.Equal(t, "re-sume", r.Slug)
}

func TestTokenExpiryAndInvalidFolders(t *testing.T) {
	s := testStore(t)
	_, _, err := s.Create("reader", time.Now().Add(time.Hour), []int64{99})
	require.Error(t, err)
	items, err := s.List()
	require.NoError(t, err)
	assert.Empty(t, items)
	r, value, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	_, err = s.DB.Exec(`UPDATE api_tokens SET expires_at=? WHERE id=?`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), r.ID)
	require.NoError(t, err)
	allowed, err := s.Validate(value)
	require.NoError(t, err)
	assert.Nil(t, allowed)
}

func TestBearerScope(t *testing.T) {
	s := testStore(t)
	_, value, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	called := 0
	h := s.Bearer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.URL.Path == "/api/v1/folders" {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Security-Policy", "default-src 'none'")
			_, _ = w.Write([]byte(`{"total":2,"items":[{"id":1},{"id":2}]}`))
			return
		}
		w.WriteHeader(200)
	}))
	cases := []struct {
		method, path string
		want         int
	}{
		{"GET", "/api/v1/folders/1/messages", 200},
		{"GET", "/api/v1/folders/2/messages", 403},
		{"GET", "/api/v1/messages/10", 200},
		{"GET", "/api/v1/messages/20/body", 403},
		{"GET", "/api/v1/messages/search?folder_id=1", 200},
		{"GET", "/api/v1/messages/search", 403},
		{"GET", "/api/v1/messages/search?folder_id=1&folder_id=2", 403},
		{"GET", "/api/v1/messages/10/thread", 403},
		{"PUT", "/api/v1/messages/10/read", 204},
		{"PUT", "/api/v1/messages/20/read", 403},
		{"PUT", "/api/v1/messages/999/read", 403},
		{"PUT", "/api/v1/messages/10/snooze", 403},
		{"POST", "/api/v1/folders/1/messages", 403},
		{"GET", "/api/v1/tokens", 403},
		{"GET", "/", 403},
	}
	for _, tc := range cases {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			assert.Equal(t, tc.want, w.Code)
		})
	}
	req := httptest.NewRequest("GET", "/api/v1/folders", nil)
	req.Header.Set("Authorization", "Bearer "+value)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	var response struct {
		Total int `json:"total"`
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	assert.Equal(t, "default-src 'none'", w.Header().Get("Content-Security-Policy"))
	assert.Equal(t, 1, response.Total)
	require.Len(t, response.Items, 1)
	assert.Equal(t, int64(1), response.Items[0].ID)
	assert.Equal(t, 4, called)
	var read, otherRead int
	require.NoError(t, s.DB.QueryRow(`SELECT read FROM messages WHERE id=10`).Scan(&read))
	require.NoError(t, s.DB.QueryRow(`SELECT read FROM messages WHERE id=20`).Scan(&otherRead))
	assert.Equal(t, 1, read)
	assert.Equal(t, 0, otherRead)
	req = httptest.NewRequest("GET", "/api/v1/messages/10", nil)
	req.Header.Set("Authorization", "Bearer invalid")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, 401, w.Code)
	assert.Equal(t, "Bearer", w.Header().Get("WWW-Authenticate"))
}

func TestManagement(t *testing.T) {
	s := testStore(t)
	body := `{"name":"reader","expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `","folder_ids":[1]}`
	w := httptest.NewRecorder()
	s.Management(w, httptest.NewRequest("POST", "/api/v1/tokens", strings.NewReader(body)))
	assert.Equal(t, 201, w.Code)
	var created struct {
		Slug  string `json:"slug"`
		Token string `json:"token"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &created))
	assert.NotEmpty(t, created.Token)
	assert.Equal(t, "reader", created.Slug)
	assert.NotContains(t, w.Body.String(), `"id"`)
	w = httptest.NewRecorder()
	s.Management(w, httptest.NewRequest("GET", "/api/v1/tokens", nil))
	assert.Equal(t, 200, w.Code)
	assert.Contains(t, w.Body.String(), `"slug":"reader"`)
	assert.NotContains(t, w.Body.String(), `"id"`)
	w = httptest.NewRecorder()
	s.Management(w, httptest.NewRequest("DELETE", "/api/v1/tokens/1", nil))
	assert.Equal(t, 404, w.Code)
	w = httptest.NewRecorder()
	s.Management(w, httptest.NewRequest("DELETE", "/api/v1/tokens/"+created.Slug, nil))
	assert.Equal(t, 204, w.Code)
}
