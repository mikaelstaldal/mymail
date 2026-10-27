package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelstaldal/go-server-common/csrf"
	"github.com/mikaelstaldal/go-server-common/devicegrant"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/mikaelstaldal/mymail/internal/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeviceLoginFullStack(t *testing.T) {
	db, err := repository.OpenDB(filepath.Join(t.TempDir(), "app.sqlite"), 5000)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0),(2,'Sent','sent',1)`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,date,created_at,updated_at) VALUES(10,1,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z'),(20,2,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`)
	require.NoError(t, err)
	store := &token.Store{DB: db}
	now := time.Now().UTC().Truncate(time.Second)
	grant := store.DeviceGrant(token.DeviceVerificationURI("http://localhost:8080", "127.0.0.1", 8080))
	grant.Now = func() time.Time { return now }
	mux := http.NewServeMux()
	token.MountDeviceGrant(mux, grant)
	mux.HandleFunc("GET /api/v1/folders", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"id": 1, "name": "Allowed"}, {"id": 2, "name": "Other"}}, "total": 2})
	})
	mux.HandleFunc("GET /api/v1/messages/{id}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	path := filepath.Join(t.TempDir(), "htpasswd")
	require.NoError(t, os.WriteFile(path, []byte("alice:"+aliceHash+"\n"), 0600))
	basic, err := loadAuthMiddleware(path, "mymail")
	require.NoError(t, err)
	h := selectAuthentication(mux, basic, store, csrf.Middleware("http://localhost:8080"))
	send := func(method, path, body, content, credentials, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", content)
		if credentials == "basic" {
			r.SetBasicAuth("alice", "s3cret")
		} else if credentials != "" {
			r.Header.Set("Authorization", "Bearer "+credentials)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	start := func() devicegrant.Authorization {
		w := send("POST", "/api/v1/oauth/device_authorization", url.Values{"client_id": {"mymail-token"}, "name": {"Reader"}, "scope": {"folder:1"}}.Encode(), "application/x-www-form-urlencoded", "", "")
		require.Equal(t, 200, w.Code, w.Body.String())
		var a devicegrant.Authorization
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &a))
		return a
	}
	poll := func(a devicegrant.Authorization) *httptest.ResponseRecorder {
		return send("POST", "/api/v1/oauth/token", url.Values{"client_id": {"mymail-token"}, "grant_type": {devicegrant.GrantType}, "device_code": {a.DeviceCode}}.Encode(), "application/x-www-form-urlencoded", "", "")
	}
	assert.Equal(t, 401, send("POST", "/api/v1/oauth%2Ftoken", "grant_type=x", "application/x-www-form-urlencoded", "", "").Code)
	a := start()
	approval := "/api/v1/oauth/device/" + a.UserCode
	assert.Equal(t, 401, send("GET", approval, "", "", "", "").Code)
	assert.Equal(t, 401, send("POST", approval, `{"approve":false}`, "application/json", "", "").Code)
	w := send("GET", approval, "", "", "basic", "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "Reader")
	assert.Contains(t, poll(a).Body.String(), "authorization_pending")
	assert.Contains(t, poll(a).Body.String(), "slow_down")
	assert.Equal(t, 403, send("POST", approval, `{"approve":false}`, "application/json", "basic", "http://evil.test").Code)
	w = send("POST", approval, `{"approve":true,"scope":["folder:1"],"lifetime":"30d"}`, "application/json", "basic", "http://localhost:8080")
	require.Equal(t, 200, w.Code, w.Body.String())
	now = now.Add(10 * time.Second)
	w = poll(a)
	require.Equal(t, 200, w.Code, w.Body.String())
	var issued devicegrant.Token
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &issued))
	assert.NotEmpty(t, issued.Slug)
	assert.Contains(t, poll(a).Body.String(), "invalid_grant")
	allowed, err := store.Validate(issued.AccessToken)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{1: true}, allowed)
	w = send("GET", "/api/v1/folders", "", "", issued.AccessToken, "")
	require.Equal(t, 200, w.Code, w.Body.String())
	assert.NotContains(t, w.Body.String(), `"name":"Other"`)
	assert.Equal(t, 403, send("GET", "/api/v1/messages/20", "", "", issued.AccessToken, "").Code)
	for _, route := range []struct{ method, path string }{{"POST", "/api/v1/oauth/device_authorization"}, {"POST", "/api/v1/oauth/token"}, {"GET", approval}, {"POST", approval}} {
		assert.Equal(t, 403, send(route.method, route.path, `{"approve":false}`, "application/json", issued.AccessToken, "").Code)
	}
	denied := start()
	assert.Equal(t, 200, send("POST", "/api/v1/oauth/device/"+denied.UserCode, `{"approve":false}`, "application/json", "basic", "").Code)
	assert.Contains(t, poll(denied).Body.String(), "access_denied")
	expired := start()
	now = now.Add(600 * time.Second)
	assert.Contains(t, poll(expired).Body.String(), "expired_token")
	assert.Equal(t, 404, send("GET", "/api/v1/oauth/device/"+expired.UserCode, "", "", "basic", "").Code)
	removed := start()
	require.Equal(t, 200, send("POST", "/api/v1/oauth/device/"+removed.UserCode, `{"approve":true,"scope":["folder:1"],"lifetime":"30d"}`, "application/json", "basic", "").Code)
	_, err = db.Exec(`DELETE FROM messages WHERE folder_id=1; DELETE FROM folders WHERE id=1`)
	require.NoError(t, err)
	w = poll(removed)
	assert.Equal(t, 400, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), "invalid_grant")

}

func TestDeviceMigrationPreservesExistingTokens(t *testing.T) {
	path := filepath.Join(t.TempDir(), "upgrade.sqlite")
	db, err := repository.OpenDB(path, 5000)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Inbox','inbox',0)`)
	require.NoError(t, err)
	store := token.Store{DB: db}
	original, secret, err := store.Create("Existing token", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	_, err = db.Exec(`DROP TABLE blocked_senders; DROP INDEX idx_contacts_explicit; ALTER TABLE contacts DROP COLUMN explicit; DROP TRIGGER messages_sent_insert; DROP TRIGGER messages_sent_update; DROP TABLE sent_recipients; ALTER TABLE spam_filter_settings DROP COLUMN auth_generation; ALTER TABLE messages DROP COLUMN auth_generation; ALTER TABLE spam_filter_settings DROP COLUMN trusted_checker_host; ALTER TABLE messages DROP COLUMN sender_authentication; DROP TRIGGER device_authorizations_resource_deleted; DROP TABLE device_authorizations; PRAGMA user_version=6`)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	db, err = repository.OpenDB(path, 5000)
	require.NoError(t, err)
	defer db.Close()
	store = token.Store{DB: db}
	granted, err := store.Validate(secret)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{1: true}, granted)
	items, err := store.List()
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, original.Slug, items[0].Slug)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM device_authorizations`).Scan(&count))
	assert.Zero(t, count)
}
