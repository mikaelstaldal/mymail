package handler_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := repository.OpenDB("file::memory:?cache=shared&mode=memory", 0)
	require.NoError(t, err, "OpenDB")
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES
		(1,'Inbox','inbox',0),(2,'Sent','sent',1),(3,'Drafts','drafts',2),
		(4,'Trash','trash',3),(5,'Scheduled','scheduled',4),
		(6,'Snoozed','snoozed',5),(7,'Junk','junk',6)`)
	require.NoError(t, err, "seed folders")
	return db
}

func newServer(t *testing.T, db *sql.DB) http.Handler {
	t.Helper()
	h := handler.New(
		repository.NewFolderRepository(db),
		repository.NewMessageRepository(db),
		repository.NewAttachmentRepository(db),
		repository.NewDraftRepository(db),
		repository.NewContactRepository(db),
		repository.NewIdentityRepository(db),
		repository.NewFilterRepository(db),
		repository.NewSpamFilterRepository(db),
		"",
	)
	srv, err := api.NewServer(h, api.WithErrorHandler(handler.WriteError))
	require.NoError(t, err, "NewServer")
	return srv
}

func do(srv http.Handler, method, path, body string) *http.Response {
	bodyReader := strings.NewReader(body)
	req := httptest.NewRequest(method, path, bodyReader)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	return w.Result()
}

func decodeJSON(t *testing.T, r *http.Response, v any) {
	t.Helper()
	err := json.NewDecoder(r.Body).Decode(v)
	require.NoError(t, err, "decode response")
}

func TestFoldersGet(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodGet, "/folders", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Total int          `json:"total"`
		Items []api.Folder `json:"items"`
	}
	decodeJSON(t, resp, &body)
	assert.Equal(t, 7, body.Total, "total built-in folders")
	assert.Len(t, body.Items, 7)

	// After creating a user folder the count grows.
	do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)

	resp = do(srv, http.MethodGet, "/folders", "")
	decodeJSON(t, resp, &body)
	assert.Equal(t, 8, body.Total, "total after create")

	found := false
	for _, f := range body.Items {
		if f.Name == "Work" {
			found = true
			break
		}
	}
	assert.True(t, found, "created folder 'Work' not found in items")
}

func TestFolderCreate(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var folder api.Folder
	decodeJSON(t, resp, &folder)
	assert.Equal(t, "Work", folder.Name)
	assert.GreaterOrEqual(t, folder.ID, 100)

	resp = do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestFolderCreate_EmptyName(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	cases := []struct{ name, body string }{
		{"empty", `{"name":""}`},
		{"whitespace only", `{"name":"   "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(srv, http.MethodPost, "/folders", tc.body)
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestFolderCreate_NameTooLong(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	longName := strings.Repeat("a", 201)
	resp := do(srv, http.MethodPost, "/folders", fmt.Sprintf(`{"name":%q}`, longName))
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestFolderUpdate_BuiltinRename(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	resp := do(srv, http.MethodPatch, "/folders/1", `{"name":"Renamed"}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = do(srv, http.MethodPatch, "/folders/1", `{"position":99}`)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestFolderUpdate_NotFound(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	resp := do(srv, http.MethodPatch, "/folders/999", `{"position":1}`)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestFolderUpdate_Conflict(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	do(srv, http.MethodPost, "/folders", `{"name":"Alpha"}`)
	do(srv, http.MethodPost, "/folders", `{"name":"Beta"}`)

	var betaID int
	db.QueryRow(`SELECT id FROM folders WHERE name='Beta'`).Scan(&betaID)

	resp := do(srv, http.MethodPatch, fmt.Sprintf("/folders/%d", betaID), `{"name":"Alpha"}`)
	assert.Equal(t, http.StatusConflict, resp.StatusCode)
}

func TestFolderDelete(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	resp := do(srv, http.MethodDelete, "/folders/1", "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = do(srv, http.MethodDelete, "/folders/999", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestFolderDelete_MovesToTrash(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodPost, "/folders", `{"name":"ToDelete"}`)
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	var folder api.Folder
	decodeJSON(t, resp, &folder)

	db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(?,?,?,?)`,
		folder.ID, "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

	resp = do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d", folder.ID), "")
	assert.Equal(t, http.StatusNoContent, resp.StatusCode)

	var folderID int
	err := db.QueryRow(`SELECT folder_id FROM messages WHERE folder_id = 4 LIMIT 1`).Scan(&folderID)
	assert.NoError(t, err)
	assert.Equal(t, 4, folderID, "message folder_id should be 4 (Trash)")
}

func TestFolderReorder(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	do(srv, http.MethodPost, "/folders", `{"name":"MyFolder"}`)
	var userID int
	db.QueryRow(`SELECT id FROM folders WHERE name='MyFolder'`).Scan(&userID)

	ids := fmt.Sprintf(`{"ids":[%d,1,2,3,4,5,6,7]}`, userID)
	resp := do(srv, http.MethodPatch, "/folders/reorder", ids)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[1,1,2,3,4,5,6,7]}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[999,1,2,3,4,5,6,7]}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[1,2,3,4,5,6,7]}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestDeleteFolderMessages_ForbiddenFolders(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	for _, id := range []int{3, 5, 6} {
		t.Run(fmt.Sprintf("folder%d", id), func(t *testing.T) {
			resp := do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d/messages", id), "")
			assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
		})
	}
}

func TestDeleteFolderMessages_NotFound(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	resp := do(srv, http.MethodDelete, "/folders/9999/messages", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestDeleteFolderMessages_PermanentDelete(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	for _, folderID := range []int{4, 7} {
		db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(?,?,?,?)`,
			folderID, "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

		resp := do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d/messages", folderID), "")
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var body struct {
			MovedToTrash       int `json:"moved_to_trash"`
			PermanentlyDeleted int `json:"permanently_deleted"`
		}
		decodeJSON(t, resp, &body)
		assert.GreaterOrEqual(t, body.PermanentlyDeleted, 1)
		assert.Zero(t, body.MovedToTrash)
	}
}

func TestDeleteFolderMessages_MoveToTrash(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(1,?,?,?)`,
		"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

	resp := do(srv, http.MethodDelete, "/folders/1/messages", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		MovedToTrash       int `json:"moved_to_trash"`
		PermanentlyDeleted int `json:"permanently_deleted"`
	}
	decodeJSON(t, resp, &body)
	assert.GreaterOrEqual(t, body.MovedToTrash, 1)
	assert.Zero(t, body.PermanentlyDeleted)
}

func TestMarkAllRead(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	for range 2 {
		db.Exec(`INSERT INTO messages(folder_id,read,date,created_at,updated_at) VALUES(1,0,?,?,?)`,
			"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")
	}

	resp := do(srv, http.MethodPost, "/folders/1/mark-all-read", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Updated int `json:"updated"`
	}
	decodeJSON(t, resp, &body)
	assert.Equal(t, 2, body.Updated)

	resp = do(srv, http.MethodPost, "/folders/9999/mark-all-read", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// The handler's map from the wire enum to the repository's must be total. ogen
// regenerates AllValues() from openapi.yaml, so adding a sort there and
// forgetting the handler is caught here — as the "unsupported sort" the handler
// answers with for a value it cannot map — instead of silently serving
// relevance. Driven through HTTP rather than reading the map directly, which
// this external test package cannot see.
func TestSearchSortsCoverTheEnum(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	values := (api.MessagesSearchGetSort("")).AllValues()
	require.NotEmpty(t, values, "the generated enum is empty; regenerate internal/api")
	for _, v := range values {
		t.Run(string(v), func(t *testing.T) {
			resp := do(srv, http.MethodGet, "/messages/search?q=needle&sort="+string(v), "")
			assert.Equal(t, http.StatusOK, resp.StatusCode)
			var body struct {
				Error string `json:"error"`
			}
			decodeJSON(t, resp, &body)
			assert.Empty(t, body.Error)
		})
	}
}

// The sort parameter, end to end: which ordering each value asks for, that an
// absent one still means relevance, and that an unknown one is rejected by the
// schema's enum rather than silently falling back to a default.
func TestSearchSort(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	insert := func(subject, date string) {
		t.Helper()
		_, err := db.Exec(
			`INSERT INTO messages(folder_id,from_addr,to_addr,subject,body_text,date,created_at,updated_at)
			 VALUES(1,'sender@example.com','me@example.com',?,'the needle is here',?,?,?)`,
			subject, date, date, date)
		require.NoError(t, err)
	}
	insert("oldest", "2024-01-01T00:00:00Z")
	insert("middle", "2024-02-01T00:00:00Z")
	insert("newest", "2024-03-01T00:00:00Z")

	type result struct {
		Total int `json:"total"`
		Items []struct {
			Subject string `json:"subject"`
		} `json:"items"`
	}
	subjects := func(query string) []string {
		t.Helper()
		resp := do(srv, http.MethodGet, "/messages/search"+query, "")
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var body result
		decodeJSON(t, resp, &body)
		assert.Equal(t, 3, body.Total)
		out := make([]string, len(body.Items))
		for i, it := range body.Items {
			out[i] = it.Subject
		}
		return out
	}

	assert.Equal(t, []string{"newest", "middle", "oldest"}, subjects("?q=needle&sort=date_desc"))
	assert.Equal(t, []string{"oldest", "middle", "newest"}, subjects("?q=needle&sort=date_asc"))
	// Every row here matches the query identically, so relevance leaves them in
	// whatever order rank produces; all this asserts is that the default and an
	// explicit "relevance" are the same request.
	assert.Equal(t, subjects("?q=needle"), subjects("?q=needle&sort=relevance"))

	// The sort applies to the page, so paging a date sort walks the whole result.
	// `subjects` asserts total == 3 throughout, which still holds under a
	// LIMIT/OFFSET: total comes from the separate count query.
	assert.Equal(t, []string{"middle"}, subjects("?q=needle&sort=date_desc&limit=1&offset=1"))

	// Unlike from_addr/to_addr — where the handler reads a blank value as "no
	// filter" — sort is validated by ogen against the schema's enum before the
	// handler runs, so an empty value is rejected rather than defaulted. The
	// demo backend's sortParam mirrors that.
	for _, bad := range []string{"subject", ""} {
		resp := do(srv, http.MethodGet, "/messages/search?q=needle&sort="+bad, "")
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode, "sort=%q", bad)
		var errBody struct {
			Error        string `json:"error"`
			ErrorMessage string `json:"error_message"`
		}
		decodeJSON(t, resp, &errBody)
		assert.Empty(t, errBody.ErrorMessage, "the undocumented field must not appear")
		assert.Contains(t, errBody.Error, "sort")
	}
}

func TestSearchAddressRefinement(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	insert := func(from, to, cc string) {
		t.Helper()
		_, err := db.Exec(
			`INSERT INTO messages(folder_id,from_addr,to_addr,cc_addr,subject,body_text,date,created_at,updated_at)
			 VALUES(1,?,?,?,'Refine me','the needle is here',?,?,?)`,
			from, to, cc,
			"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")
		require.NoError(t, err)
	}
	insert(`"Alice Andersson" <alice@example.com>`, "me@example.com", "")
	insert("bob@other.example", "team@example.com", "me@example.com")

	type result struct {
		Total int `json:"total"`
		Items []struct {
			FromAddr string `json:"from_addr"`
		} `json:"items"`
	}

	cases := []struct {
		name      string
		query     string
		wantTotal int
	}{
		{"unfiltered", "?q=needle", 2},
		{"from address", "?q=needle&from_addr=alice%40example.com", 1},
		{"from address is case-insensitive", "?q=needle&from_addr=ALICE", 1},
		{"to address matches Cc", "?q=needle&to_addr=me%40example.com", 2},
		{"to address matches To only", "?q=needle&to_addr=team%40", 1},
		{"from and to are ANDed", "?q=needle&from_addr=bob&to_addr=me%40example.com", 1},
		{"blank from_addr is no filter", "?q=needle&from_addr=", 2},
		{"whitespace-only to_addr is no filter", "?q=needle&to_addr=%20%20", 2},
		{"no match", "?q=needle&from_addr=carol", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(srv, http.MethodGet, "/messages/search"+tc.query, "")
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var body result
			decodeJSON(t, resp, &body)
			assert.Equal(t, tc.wantTotal, body.Total)
			assert.Len(t, body.Items, tc.wantTotal)
		})
	}

	// Over the declared maxLength ogen rejects the request while decoding the
	// parameters, before it reaches the handler or the DB. That path still has
	// to answer in the documented {"error": …} shape and name the parameter —
	// ogen's own error handler would write {"error_message": …}, which the web
	// UI does not read, leaving the user with a bare "Bad Request".
	resp := do(srv, http.MethodGet, "/messages/search?q=needle&from_addr="+strings.Repeat("a", 201), "")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	var errBody struct {
		Error        string `json:"error"`
		ErrorMessage string `json:"error_message"`
	}
	decodeJSON(t, resp, &errBody)
	assert.Empty(t, errBody.ErrorMessage, "the undocumented field must not appear")
	assert.Contains(t, errBody.Error, "from_addr")
	assert.Contains(t, errBody.Error, "200")
}
