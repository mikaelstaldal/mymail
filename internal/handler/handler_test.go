package handler_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := repository.OpenDB("file::memory:?cache=shared&mode=memory", 0)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES
		(1,'Inbox','inbox',0),(2,'Sent','sent',1),(3,'Drafts','drafts',2),
		(4,'Trash','trash',3),(5,'Scheduled','scheduled',4),
		(6,'Snoozed','snoozed',5),(7,'Junk','junk',6)`)
	if err != nil {
		t.Fatalf("seed folders: %v", err)
	}
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
	srv, err := api.NewServer(h)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
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
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func TestFoldersGet(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodGet, "/folders", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body struct {
		Total int          `json:"total"`
		Items []api.Folder `json:"items"`
	}
	decodeJSON(t, resp, &body)
	if body.Total != 7 {
		t.Errorf("total = %d, want 7 (built-in folders)", body.Total)
	}
	if len(body.Items) != 7 {
		t.Errorf("len(items) = %d, want 7", len(body.Items))
	}

	// After creating a user folder the count grows.
	do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)

	resp = do(srv, http.MethodGet, "/folders", "")
	decodeJSON(t, resp, &body)
	if body.Total != 8 {
		t.Errorf("total = %d, want 8 after create", body.Total)
	}
	found := false
	for _, f := range body.Items {
		if f.Name == "Work" {
			found = true
			break
		}
	}
	if !found {
		t.Error("created folder 'Work' not found in items")
	}
}

func TestFolderCreate(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("want 201, got %d", resp.StatusCode)
	}
	var folder api.Folder
	decodeJSON(t, resp, &folder)
	if folder.Name != "Work" {
		t.Errorf("name = %q, want Work", folder.Name)
	}
	if folder.ID < 100 {
		t.Errorf("id = %d, want >= 100", folder.ID)
	}

	resp = do(srv, http.MethodPost, "/folders", `{"name":"Work"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
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
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestFolderCreate_NameTooLong(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	longName := strings.Repeat("a", 201)
	resp := do(srv, http.MethodPost, "/folders", fmt.Sprintf(`{"name":%q}`, longName))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestFolderUpdate_BuiltinRename(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	resp := do(srv, http.MethodPatch, "/folders/1", `{"name":"Renamed"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}

	resp = do(srv, http.MethodPatch, "/folders/1", `{"position":99}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestFolderUpdate_NotFound(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	resp := do(srv, http.MethodPatch, "/folders/999", `{"position":1}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestFolderUpdate_Conflict(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	do(srv, http.MethodPost, "/folders", `{"name":"Alpha"}`)
	do(srv, http.MethodPost, "/folders", `{"name":"Beta"}`)

	var betaID int
	db.QueryRow(`SELECT id FROM folders WHERE name='Beta'`).Scan(&betaID)

	resp := do(srv, http.MethodPatch, fmt.Sprintf("/folders/%d", betaID), `{"name":"Alpha"}`)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
}

func TestFolderDelete(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	resp := do(srv, http.MethodDelete, "/folders/1", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("built-in delete: want 400, got %d", resp.StatusCode)
	}

	resp = do(srv, http.MethodDelete, "/folders/999", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown delete: want 404, got %d", resp.StatusCode)
	}
}

func TestFolderDelete_MovesToTrash(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	resp := do(srv, http.MethodPost, "/folders", `{"name":"ToDelete"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create: want 201, got %d", resp.StatusCode)
	}
	var folder api.Folder
	decodeJSON(t, resp, &folder)

	db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(?,?,?,?)`,
		folder.ID, "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

	resp = do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d", folder.ID), "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete: want 204, got %d", resp.StatusCode)
	}

	var folderID int
	db.QueryRow(`SELECT folder_id FROM messages WHERE folder_id = 4 LIMIT 1`).Scan(&folderID)
	if folderID != 4 {
		t.Errorf("message folder_id = %d, want 4 (Trash)", folderID)
	}
}

func TestFolderReorder(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	do(srv, http.MethodPost, "/folders", `{"name":"MyFolder"}`)
	var userID int
	db.QueryRow(`SELECT id FROM folders WHERE name='MyFolder'`).Scan(&userID)

	ids := fmt.Sprintf(`{"ids":[%d,1,2,3,4,5,6,7]}`, userID)
	resp := do(srv, http.MethodPatch, "/folders/reorder", ids)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder: want 200, got %d", resp.StatusCode)
	}

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[1,1,2,3,4,5,6,7]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate: want 400, got %d", resp.StatusCode)
	}

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[999,1,2,3,4,5,6,7]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown: want 400, got %d", resp.StatusCode)
	}

	resp = do(srv, http.MethodPatch, "/folders/reorder", `{"ids":[1,2,3,4,5,6,7]}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing: want 400, got %d", resp.StatusCode)
	}
}

func TestDeleteFolderMessages_ForbiddenFolders(t *testing.T) {
	srv := newServer(t, openTestDB(t))

	for _, id := range []int{3, 5, 6} {
		t.Run(fmt.Sprintf("folder%d", id), func(t *testing.T) {
			resp := do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d/messages", id), "")
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("want 400, got %d", resp.StatusCode)
			}
		})
	}
}

func TestDeleteFolderMessages_NotFound(t *testing.T) {
	srv := newServer(t, openTestDB(t))
	resp := do(srv, http.MethodDelete, "/folders/9999/messages", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestDeleteFolderMessages_PermanentDelete(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	for _, folderID := range []int{4, 7} {
		db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(?,?,?,?)`,
			folderID, "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

		resp := do(srv, http.MethodDelete, fmt.Sprintf("/folders/%d/messages", folderID), "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("folder %d: want 200, got %d", folderID, resp.StatusCode)
		}
		var body struct {
			MovedToTrash       int `json:"moved_to_trash"`
			PermanentlyDeleted int `json:"permanently_deleted"`
		}
		decodeJSON(t, resp, &body)
		if body.PermanentlyDeleted < 1 {
			t.Errorf("folder %d: permanently_deleted = %d, want >= 1", folderID, body.PermanentlyDeleted)
		}
		if body.MovedToTrash != 0 {
			t.Errorf("folder %d: moved_to_trash = %d, want 0", folderID, body.MovedToTrash)
		}
	}
}

func TestDeleteFolderMessages_MoveToTrash(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	db.Exec(`INSERT INTO messages(folder_id,date,created_at,updated_at) VALUES(1,?,?,?)`,
		"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")

	resp := do(srv, http.MethodDelete, "/folders/1/messages", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body struct {
		MovedToTrash       int `json:"moved_to_trash"`
		PermanentlyDeleted int `json:"permanently_deleted"`
	}
	decodeJSON(t, resp, &body)
	if body.MovedToTrash < 1 {
		t.Errorf("moved_to_trash = %d, want >= 1", body.MovedToTrash)
	}
	if body.PermanentlyDeleted != 0 {
		t.Errorf("permanently_deleted = %d, want 0", body.PermanentlyDeleted)
	}
}

func TestMarkAllRead(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)

	for range 2 {
		db.Exec(`INSERT INTO messages(folder_id,read,date,created_at,updated_at) VALUES(1,0,?,?,?)`,
			"2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z", "2024-01-01T00:00:00Z")
	}

	resp := do(srv, http.MethodPost, "/folders/1/mark-all-read", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var body struct {
		Updated int `json:"updated"`
	}
	decodeJSON(t, resp, &body)
	if body.Updated != 2 {
		t.Errorf("updated = %d, want 2", body.Updated)
	}

	resp = do(srv, http.MethodPost, "/folders/9999/mark-all-read", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}
