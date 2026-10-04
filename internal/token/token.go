package token

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"
)

// Store keeps only a SHA-256 digest of each random, 256-bit bearer secret.
type Store struct{ DB *sql.DB }

type Record struct {
	ID        int64   `json:"id"`
	Name      string  `json:"name"`
	CreatedAt string  `json:"created_at"`
	ExpiresAt string  `json:"expires_at"`
	FolderIDs []int64 `json:"folder_ids"`
}

func (s Store) Create(name string, expiry time.Time, folders []int64) (Record, string, error) {
	var record Record
	if strings.TrimSpace(name) == "" || len(name) > 200 || !expiry.After(time.Now()) || len(folders) == 0 {
		return record, "", errors.New("name, future expiry, and at least one folder are required")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return record, "", err
	}
	value := "mymail_" + base64.RawURLEncoding.EncodeToString(secret)
	digest := sha256.Sum256([]byte(value))
	tx, err := s.DB.Begin()
	if err != nil {
		return record, "", err
	}
	defer tx.Rollback()
	record = Record{Name: strings.TrimSpace(name), CreatedAt: time.Now().UTC().Format(time.RFC3339), ExpiresAt: expiry.UTC().Format(time.RFC3339), FolderIDs: folders}
	res, err := tx.Exec(`INSERT INTO api_tokens(name,token_hash,created_at,expires_at) VALUES(?,?,?,?)`, record.Name, digest[:], record.CreatedAt, record.ExpiresAt)
	if err != nil {
		return Record{}, "", err
	}
	record.ID, err = res.LastInsertId()
	if err != nil {
		return Record{}, "", err
	}
	seen := map[int64]bool{}
	for _, id := range folders {
		if id <= 0 || seen[id] {
			return Record{}, "", errors.New("folder IDs must be unique and positive")
		}
		seen[id] = true
		var exists int
		if err := tx.QueryRow(`SELECT 1 FROM folders WHERE id=?`, id).Scan(&exists); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Record{}, "", errors.New("folder not found")
			}
			return Record{}, "", err
		}
		if _, err := tx.Exec(`INSERT INTO api_token_folders(token_id,folder_id) VALUES(?,?)`, record.ID, id); err != nil {
			return Record{}, "", err
		}
	}
	if err := tx.Commit(); err != nil {
		return Record{}, "", err
	}
	return record, value, nil
}

func (s Store) List() ([]Record, error) {
	rows, err := s.DB.Query(`SELECT t.id,t.name,t.created_at,t.expires_at,f.folder_id FROM api_tokens t LEFT JOIN api_token_folders f ON f.token_id=t.id ORDER BY t.id DESC,f.folder_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		var r Record
		var folderID sql.NullInt64
		if err := rows.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.ExpiresAt, &folderID); err != nil {
			return nil, err
		}
		if len(items) == 0 || items[len(items)-1].ID != r.ID {
			r.FolderIDs = []int64{}
			items = append(items, r)
		}
		if folderID.Valid {
			items[len(items)-1].FolderIDs = append(items[len(items)-1].FolderIDs, folderID.Int64)
		}
	}
	return items, rows.Err()
}

func (s Store) Revoke(id int64) (bool, error) {
	res, err := s.DB.Exec(`DELETE FROM api_tokens WHERE id=?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s Store) Validate(value string) (map[int64]bool, error) {
	if !strings.HasPrefix(value, "mymail_") || len(value) != 50 {
		return nil, nil
	}
	digest := sha256.Sum256([]byte(value))
	var id int64
	var expiry string
	err := s.DB.QueryRow(`SELECT id,expires_at FROM api_tokens WHERE token_hash=?`, digest[:]).Scan(&id, &expiry)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	until, err := time.Parse(time.RFC3339, expiry)
	if err != nil {
		return nil, err
	}
	if !time.Now().Before(until) {
		return nil, nil
	}
	rows, err := s.DB.Query(`SELECT folder_id FROM api_token_folders WHERE token_id=?`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	allowed := map[int64]bool{}
	for rows.Next() {
		var f int64
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		allowed[f] = true
	}
	return allowed, rows.Err()
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

// Management permits only the full-access caller. The outer Basic middleware
// authenticates it when Basic auth is configured.
func (s Store) Management(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/api/v1/tokens" {
		switch r.Method {
		case http.MethodGet:
			items, err := s.List()
			if err != nil {
				writeError(w, 500, "database error")
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"total": len(items), "items": items})
		case http.MethodPost:
			var input struct {
				Name      string  `json:"name"`
				ExpiresAt string  `json:"expires_at"`
				FolderIDs []int64 `json:"folder_ids"`
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8192)
			dec := json.NewDecoder(r.Body)
			dec.DisallowUnknownFields()
			if err := dec.Decode(&input); err != nil {
				writeError(w, 400, "invalid token request")
				return
			}
			expiry, err := time.Parse(time.RFC3339, input.ExpiresAt)
			if err != nil {
				writeError(w, 400, "invalid expiry")
				return
			}
			record, value, err := s.Create(input.Name, expiry, input.FolderIDs)
			if err != nil {
				writeError(w, 400, err.Error())
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(struct {
				Record
				Token string `json:"token"`
			}{record, value})
		default:
			writeError(w, 405, "method not allowed")
		}
		return
	}
	if r.Method != http.MethodDelete {
		writeError(w, 405, "method not allowed")
		return
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/api/v1/tokens/"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 404, "token not found")
		return
	}
	ok, err := s.Revoke(id)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	if !ok {
		writeError(w, 404, "token not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Bearer bypasses Basic only for narrowly scoped read routes. Every other
// bearer request is denied before it can reach the full-access API or UI.
func (s Store) Bearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(strings.ToLower(header), "bearer ") {
			next.ServeHTTP(w, r)
			return
		}
		allowed, err := s.Validate(strings.TrimSpace(header[7:]))
		if err != nil {
			writeError(w, 500, "database error")
			return
		}
		if allowed == nil {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeError(w, 401, "unauthorized")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, 403, "token grants read access only")
			return
		}
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if len(parts) < 3 || parts[0] != "api" || parts[1] != "v1" {
			writeError(w, 403, "token cannot access this resource")
			return
		}
		if len(parts) == 3 && parts[2] == "folders" {
			rec := httptest.NewRecorder()
			next.ServeHTTP(rec, r)
			if rec.Code != http.StatusOK {
				for k, v := range rec.Header() {
					w.Header()[k] = v
				}
				w.WriteHeader(rec.Code)
				_, _ = w.Write(rec.Body.Bytes())
				return
			}
			for k, v := range rec.Header() {
				w.Header()[k] = v
			}
			var payload struct {
				Total int               `json:"total"`
				Items []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				writeError(w, 500, "invalid folder response")
				return
			}
			filtered := []json.RawMessage{}
			for _, item := range payload.Items {
				var folder struct {
					ID int64 `json:"id"`
				}
				if json.Unmarshal(item, &folder) == nil && allowed[folder.ID] {
					filtered = append(filtered, item)
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"total": len(filtered), "items": filtered})
			return
		}
		var folderID int64
		switch {
		case len(parts) == 4 && parts[2] == "messages" && parts[3] == "search":
			if values := r.URL.Query()["folder_id"]; len(values) == 1 {
				folderID, _ = strconv.ParseInt(values[0], 10, 64)
			}
		case len(parts) == 5 && parts[2] == "folders" && parts[4] == "messages":
			folderID, _ = strconv.ParseInt(parts[3], 10, 64)
		case (len(parts) == 4 || len(parts) == 5) && parts[2] == "messages" && parts[3] != "search":
			if len(parts) == 5 && parts[4] != "raw" && parts[4] != "headers" && parts[4] != "body" {
				break
			}
			id, e := strconv.ParseInt(parts[3], 10, 64)
			if e == nil {
				_ = s.DB.QueryRow(`SELECT folder_id FROM messages WHERE id=?`, id).Scan(&folderID)
			}
		case len(parts) == 4 && parts[2] == "attachments":
			id, e := strconv.ParseInt(parts[3], 10, 64)
			if e == nil {
				_ = s.DB.QueryRow(`SELECT m.folder_id FROM attachments a JOIN messages m ON m.id=a.message_id WHERE a.id=?`, id).Scan(&folderID)
			}
		}
		if folderID == 0 || !allowed[folderID] {
			writeError(w, 403, "token cannot access this resource")
			return
		}
		next.ServeHTTP(w, r)
	})
}
