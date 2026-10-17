package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMessageWarningsRecomputed(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	_, err := db.Exec(`INSERT INTO messages(id,folder_id,from_addr,date,created_at,updated_at) VALUES(42,1,'person@example.org','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	read := func() api.MessageDetail {
		res := do(srv, http.MethodGet, "/messages/42", "")
		require.Equal(t, 200, res.StatusCode)
		var d api.MessageDetail
		decodeJSON(t, res, &d)
		return d
	}
	detail := read()
	require.Len(t, detail.Warnings, 1)
	assert.Equal(t, api.MessageWarningKindFirstTimeSender, detail.Warnings[0].Kind)
	_, err = repository.NewMessageRepository(db).InsertMessage(context.Background(), model.DBMessage{FolderID: 2, ToAddr: "person@example.org", Date: detail.Date})
	require.NoError(t, err)
	assert.Empty(t, read().Warnings, "same message changes when correspondence changes")
	_, err = db.Exec(`DELETE FROM messages WHERE folder_id = 2`)
	require.NoError(t, err)
	assert.Empty(t, read().Warnings)
	res := do(srv, http.MethodGet, fmt.Sprintf("/messages/%d", 9999), "")
	assert.Equal(t, 404, res.StatusCode)
}

func TestMessageWarningExemptionsThroughAPI(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	_, err := db.Exec(`INSERT INTO identities(name,address,is_default,position) VALUES('Own','own@example.com',1,0);
 UPDATE spam_filter_settings SET trusted_checker_host='checker' WHERE id=1`)
	require.NoError(t, err)
	for i, folder := range []int{2, 3, 5, 1} {
		_, err = db.Exec(`INSERT INTO messages(id,folder_id,from_addr,body_html,sender_authentication,date,created_at,updated_at)
  VALUES(?,?,'own@example.com','<a href="https://evil.org">example.com</a>','{"dmarc":"pass","dkim_aligned":true,"spf":"pass"}','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, 50+i, folder)
		require.NoError(t, err)
		res := do(srv, http.MethodGet, fmt.Sprintf("/messages/%d", 50+i), "")
		require.Equal(t, 200, res.StatusCode)
		var detail api.MessageDetail
		decodeJSON(t, res, &detail)
		assert.Empty(t, detail.Warnings)
	}
}

// Serving a body must never persist its navigation wrappers, including when
// replying/forwarding via the original detail endpoint.
func TestDeceptiveBodyRewrite(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	original := `<a>no href</a><a href="https://example.com" target="_blank" rel="noopener noreferrer">example.com</a><a href="https://evil.org/a?x=1&amp;y=2" target="_blank" rel="noopener noreferrer">example.com</a>`
	_, err := db.Exec(`INSERT INTO messages(id,folder_id,from_addr,body_html,raw,date,created_at,updated_at) VALUES(42,1,'person@example.org',?,'original raw','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, original)
	require.NoError(t, err)
	res := do(srv, http.MethodGet, "/messages/42/body", "")
	require.Equal(t, 200, res.StatusCode)
	served, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	text := string(served)
	assert.Contains(t, text, `href="https://example.com"`)
	assert.Contains(t, text, `href="../../../../#/link/42/2" rel="noopener noreferrer" target="_blank"`)
	assert.NotContains(t, text, `href="https://evil.org`)
	var stored, raw string
	require.NoError(t, db.QueryRow(`SELECT body_html, raw FROM messages WHERE id=42`).Scan(&stored, &raw))
	assert.Equal(t, original, stored)
	assert.Equal(t, "original raw", raw)
	_, err = db.Exec(`UPDATE messages SET folder_id=2 WHERE id=42`)
	require.NoError(t, err)
	res = do(srv, http.MethodGet, "/messages/42/body", "")
	served, err = io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Contains(t, string(served), `href="https://evil.org/a?x=1&amp;y=2"`)
	assert.NotContains(t, string(served), "#/link/")
}
