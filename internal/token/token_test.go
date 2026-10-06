package token

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
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

func TestTokenCreationCleansExpiredTokens(t *testing.T) {
	s := testStore(t)
	expired, expiredSecret, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1, 2})
	require.NoError(t, err)
	valid, validSecret, err := s.Create("active", time.Now().Add(time.Hour), []int64{2})
	require.NoError(t, err)
	otherExpired, _, err := s.Create("other", time.Now().Add(time.Hour), []int64{2})
	require.NoError(t, err)
	_, err = s.DB.Exec(`UPDATE api_tokens SET expires_at=? WHERE id IN (?,?)`, time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), expired.ID, otherExpired.ID)
	require.NoError(t, err)

	// A failed creation must roll back both the insert and cleanup.
	_, _, err = s.Create("reader", time.Now().Add(time.Hour), []int64{99})
	require.Error(t, err)
	var count int
	require.NoError(t, s.DB.QueryRow(`SELECT COUNT(*) FROM api_tokens WHERE id=?`, expired.ID).Scan(&count))
	assert.Equal(t, 1, count)
	require.NoError(t, s.DB.QueryRow(`SELECT COUNT(*) FROM api_token_folders WHERE token_id=?`, expired.ID).Scan(&count))
	assert.Equal(t, 2, count)

	replacement, _, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	assert.Equal(t, expired.Slug, replacement.Slug)
	assert.Greater(t, replacement.ID, valid.ID)
	items, err := s.List()
	require.NoError(t, err)
	require.Len(t, items, 2)
	assert.Equal(t, replacement.ID, items[0].ID)
	assert.Equal(t, valid.ID, items[1].ID)
	require.NoError(t, s.DB.QueryRow(`SELECT COUNT(*) FROM api_token_folders WHERE token_id=?`, expired.ID).Scan(&count))
	assert.Zero(t, count)
	require.NoError(t, s.DB.QueryRow(`SELECT COUNT(*) FROM api_token_folders WHERE token_id=?`, otherExpired.ID).Scan(&count))
	assert.Zero(t, count)
	allowed, err := s.Validate(validSecret)
	require.NoError(t, err)
	assert.Equal(t, map[int64]bool{2: true}, allowed)
	allowed, err = s.Validate(expiredSecret)
	require.NoError(t, err)
	assert.Nil(t, allowed)
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

func TestRevokedTokenCannotUseReplacementGrant(t *testing.T) {
	s := testStore(t)
	old, secret, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	oldHash := sha256.Sum256([]byte(secret))
	ok, err := s.Revoke(old.Slug)
	require.NoError(t, err)
	require.True(t, ok)
	// Explicitly reuse the ID to exercise the write guard independently of
	// AUTOINCREMENT, as a future import or schema change could supply an ID.
	newHash := sha256.Sum256([]byte("replacement"))
	_, err = s.DB.Exec(`INSERT INTO api_tokens(id,name,slug,token_hash,created_at,expires_at)
		VALUES(?,?,?,?,?,?)`, old.ID, "replacement", "replacement", newHash[:],
		time.Now().UTC().Format(time.RFC3339), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	require.NoError(t, err)
	_, err = s.DB.Exec(`INSERT INTO api_token_folders(token_id,folder_id) VALUES(?,2)`, old.ID)
	require.NoError(t, err)
	allowed, err := s.Validate(secret)
	require.NoError(t, err)
	assert.Nil(t, allowed)
	found, err := repository.NewMessageRepository(s.DB).MarkRead(context.Background(), 20, oldHash[:])
	require.NoError(t, err)
	assert.False(t, found)
	var read int
	require.NoError(t, s.DB.QueryRow(`SELECT read FROM messages WHERE id=20`).Scan(&read))
	assert.Zero(t, read)
	found, err = repository.NewMessageRepository(s.DB).MarkRead(context.Background(), 20, newHash[:])
	require.NoError(t, err)
	assert.True(t, found)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/messages/20/read", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	w := httptest.NewRecorder()
	s.Bearer(http.NotFoundHandler()).ServeHTTP(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
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

func TestBearerReadUsesAuthorizationSnapshot(t *testing.T) {
	for _, tc := range []struct {
		name, path, change string
	}{
		{"message move", "/api/v1/messages/10", `UPDATE messages SET folder_id=2,subject='later' WHERE id=10`},
		{"token revocation", "/api/v1/messages/10", ""},
		{"token rotation", "/api/v1/messages/10", ""},
		{"attachment move", "/api/v1/attachments/1", ""},
		{"raw", "/api/v1/messages/10/raw", `UPDATE messages SET folder_id=2,raw='later' WHERE id=10`},
		{"headers", "/api/v1/messages/10/headers", `UPDATE messages SET folder_id=2,raw='later' WHERE id=10`},
		{"body", "/api/v1/messages/10/body", `UPDATE messages SET folder_id=2,body_html='<p>later</p>' WHERE id=10`},
		{"folder list", "/api/v1/folders", `UPDATE folders SET name='Later' WHERE id=1`},
		{"folder recreation", "/api/v1/folders", ""},
		{"folder messages", "/api/v1/folders/1/messages", `UPDATE messages SET folder_id=2,subject='later' WHERE id=10`},
		{"search", "/api/v1/messages/search?q=before&folder_id=1", `UPDATE messages SET folder_id=2,subject='later',body_text='later' WHERE id=10`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := testStore(t)
			var mode string
			require.NoError(t, s.DB.QueryRow(`PRAGMA journal_mode=WAL`).Scan(&mode))
			require.Equal(t, "wal", mode)
			_, err := s.DB.Exec(`UPDATE messages SET subject='before',body_text='before',body_html='<p>before</p>',raw=? WHERE id=10`, []byte("Subject: before\r\n\r\nbefore"))
			require.NoError(t, err)
			_, err = s.DB.Exec(`INSERT INTO attachments(id,message_id,filename,content_type,size,data) VALUES(1,10,'before.txt','text/plain',6,'before')`)
			require.NoError(t, err)
			original, secret, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
			require.NoError(t, err)
			h := handler.New(
				repository.NewFolderRepository(s.DB), repository.NewMessageRepository(s.DB),
				repository.NewAttachmentRepository(s.DB), repository.NewDraftRepository(s.DB),
				repository.NewContactRepository(s.DB), repository.NewIdentityRepository(s.DB),
				repository.NewFilterRepository(s.DB), repository.NewSpamFilterRepository(s.DB), "",
			)
			server, err := api.NewServer(h, api.WithErrorHandler(handler.WriteError))
			require.NoError(t, err)
			// The writer commits after middleware authorization but before the
			// generated handler asks its repositories for response data.
			next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch tc.name {
				case "folder recreation":
					_, err := s.DB.Exec(`UPDATE messages SET folder_id=2 WHERE folder_id=1`)
					require.NoError(t, err)
					_, err = s.DB.Exec(`DELETE FROM folders WHERE id=1`)
					require.NoError(t, err)
					_, err = s.DB.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(1,'Replacement','replacement',0)`)
					require.NoError(t, err)
				case "token revocation", "token rotation":
					_, err := s.DB.Exec(`DELETE FROM api_tokens`)
					require.NoError(t, err)
					if tc.name == "token rotation" {
						digest := sha256.Sum256([]byte("replacement"))
						_, err = s.DB.Exec(`INSERT INTO api_tokens(id,name,slug,token_hash,created_at,expires_at)
							VALUES(?,?,?,?,?,?)`, original.ID, "replacement", "replacement", digest[:],
							time.Now().UTC().Format(time.RFC3339), time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
						require.NoError(t, err)
						_, err = s.DB.Exec(`INSERT INTO api_token_folders(token_id,folder_id) VALUES(?,2)`, original.ID)
						require.NoError(t, err)
					}
					_, err = s.DB.Exec(`UPDATE messages SET subject='later' WHERE id=10`)
					require.NoError(t, err)
				case "attachment move":
					_, err := s.DB.Exec(`UPDATE messages SET folder_id=2 WHERE id=10`)
					require.NoError(t, err)
					_, err = s.DB.Exec(`UPDATE attachments SET filename='later.txt',data='later' WHERE id=1`)
					require.NoError(t, err)
				case "message move":
					_, err := s.DB.Exec(tc.change)
					require.NoError(t, err)
					_, err = s.DB.Exec(`UPDATE attachments SET filename='later.txt' WHERE id=1`)
					require.NoError(t, err)
				default:
					_, err := s.DB.Exec(tc.change)
					require.NoError(t, err)
				}
				http.StripPrefix("/api/v1", server).ServeHTTP(w, r)
			})
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+secret)
			w := httptest.NewRecorder()
			s.Bearer(next).ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			switch tc.name {
			case "attachment move":
				assert.Equal(t, "before", w.Body.String())
				assert.Equal(t, `attachment; filename="before.txt"`, w.Header().Get("Content-Disposition"))
			case "raw", "headers", "body":
				assert.Contains(t, w.Body.String(), "before")
				assert.NotContains(t, w.Body.String(), "later")
			case "folder list", "folder recreation":
				assert.Contains(t, w.Body.String(), `"name":"Inbox"`)
				assert.NotContains(t, w.Body.String(), `"name":"Later"`)
				assert.NotContains(t, w.Body.String(), `"name":"Replacement"`)
			default:
				assert.Contains(t, w.Body.String(), `"folder_id":1`)
				assert.Contains(t, w.Body.String(), `"subject":"before"`)
				if tc.name == "message move" {
					assert.Contains(t, w.Body.String(), `"filename":"before.txt"`)
					assert.NotContains(t, w.Body.String(), `"filename":"later.txt"`)
				}
			}
			if tc.name == "token revocation" || tc.name == "token rotation" {
				allowed, err := s.Validate(secret)
				require.NoError(t, err)
				assert.Nil(t, allowed)
			}
		})
	}
}

type writeHookResponse struct {
	http.ResponseWriter
	onHeader func()
	onWrite  func([]byte)
}

func (w writeHookResponse) WriteHeader(status int) {
	w.onHeader()
	w.ResponseWriter.WriteHeader(status)
}

func (w writeHookResponse) Write(p []byte) (int, error) {
	w.onWrite(p)
	return w.ResponseWriter.Write(p)
}

func TestBearerReleasesReadTransactionBeforeSendingResponse(t *testing.T) {
	for _, explicitHeader := range []bool{false, true} {
		name := "implicit header"
		if explicitHeader {
			name = "explicit header"
		}
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			s.DB.SetMaxOpenConns(1)
			_, secret, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
			require.NoError(t, err)
			payload := bytes.Repeat([]byte("x"), 2<<20)
			next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if explicitHeader {
					w.WriteHeader(http.StatusOK)
				}
				_, err := w.Write(payload)
				require.NoError(t, err)
			})
			req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/10/raw", nil)
			req.Header.Set("Authorization", "Bearer "+secret)
			rec := httptest.NewRecorder()
			checkConnection := func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				var n int
				err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
				require.NoError(t, err, "response write must not retain the sole database connection")
			}
			checkedHeader, checkedWrite := false, false
			w := writeHookResponse{ResponseWriter: rec, onHeader: func() {
				checkedHeader = true
				checkConnection()
			}, onWrite: func(p []byte) {
				checkedWrite = true
				checkConnection()
				assert.True(t, &payload[0] == &p[0], "the writer wrapper must pass this slice through")
			}}
			s.Bearer(next).ServeHTTP(w, req)
			assert.Equal(t, explicitHeader, checkedHeader)
			assert.True(t, checkedWrite)
			assert.Equal(t, payload, rec.Body.Bytes())
		})
	}
}

func TestBearerGeneratedRawReleasesBeforeWriting(t *testing.T) {
	s := testStore(t)
	s.DB.SetMaxOpenConns(1)
	payload := bytes.Repeat([]byte("raw"), 1<<19)
	_, err := s.DB.Exec(`UPDATE messages SET raw=? WHERE id=10`, payload)
	require.NoError(t, err)
	_, secret, err := s.Create("reader", time.Now().Add(time.Hour), []int64{1})
	require.NoError(t, err)
	h := handler.New(
		repository.NewFolderRepository(s.DB), repository.NewMessageRepository(s.DB),
		repository.NewAttachmentRepository(s.DB), repository.NewDraftRepository(s.DB),
		repository.NewContactRepository(s.DB), repository.NewIdentityRepository(s.DB),
		repository.NewFilterRepository(s.DB), repository.NewSpamFilterRepository(s.DB), "",
	)
	server, err := api.NewServer(h, api.WithErrorHandler(handler.WriteError))
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/10/raw", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	rec := httptest.NewRecorder()
	checkConnection := func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		var n int
		err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&n)
		require.NoError(t, err, "the generated response must release the sole connection before writing")
	}
	checkedHeader, checkedWrite := false, false
	w := writeHookResponse{ResponseWriter: rec, onHeader: func() {
		checkedHeader = true
		checkConnection()
	}, onWrite: func(_ []byte) {
		checkedWrite = true
		checkConnection()
	}}
	s.Bearer(http.StripPrefix("/api/v1", server)).ServeHTTP(w, req)
	assert.True(t, checkedHeader)
	assert.True(t, checkedWrite)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, payload, rec.Body.Bytes())
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
