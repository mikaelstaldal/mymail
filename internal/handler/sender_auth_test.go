package handler_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSenderAuthenticationAPI(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	res := do(srv, http.MethodPut, "/spam-filter", `{"enabled":true,"score_header":"X-Spam-Score","score_threshold":5,"trusted_checker_host":"checker"}`)
	require.Equal(t, http.StatusOK, res.StatusCode)
	var settings api.SpamFilterSettings
	decodeJSON(t, res, &settings)
	assert.Equal(t, "checker", settings.TrustedCheckerHost.Value)
	assert.Equal(t, http.StatusBadRequest, do(srv, http.MethodPut, "/spam-filter", `{"enabled":true,"score_header":"X-Spam-Score","score_threshold":5,"trusted_checker_host":"invalid host"}`).StatusCode)
	raw := []byte("From: a@example.org\r\nX-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\nX-Spam-Status: No, tests=DMARC_NONE,SPF_FAIL\r\n\r\nbody")
	// Backfill is exercised through the complete HTTP detail projection.
	_, err := db.Exec(`INSERT INTO messages(id,folder_id,date,raw,created_at,updated_at) VALUES(42,1,'2024-01-01T00:00:00Z',?,'2024-01-01T00:00:00Z','2024-01-01T00:00:00Z')`, raw)
	require.NoError(t, err)
	require.NoError(t, repository.BackfillSenderAuthentication(context.Background(), db))
	res = do(srv, http.MethodGet, "/messages/42", "")
	require.Equal(t, http.StatusOK, res.StatusCode)
	var detail api.MessageDetail
	decodeJSON(t, res, &detail)
	assert.Equal(t, api.SenderAuthenticationDmarcFail, detail.SenderAuthentication.Dmarc)
	assert.Equal(t, api.SenderAuthenticationSpfFail, detail.SenderAuthentication.Spf)
}
