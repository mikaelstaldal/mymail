package lda

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// autocompleteSuggestions issues the request ComposeForm.tsx:605 makes while
// the user types a recipient: GET /contacts?q=<typed>&limit=10.
func autocompleteSuggestions(t *testing.T, srv http.Handler, typed string) []oas.Contact {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/contacts?limit=10&q="+url.QueryEscape(typed), nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var out struct {
		Items []oas.Contact `json:"items"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&out))
	return out.Items
}

func attackerMessage(from, msgID string) []byte {
	return []byte("From: " + from + "\r\nTo: me@example.com\r\nSubject: hi\r\nMessage-ID: <" + msgID +
		">\r\nDate: Mon, 19 Oct 2026 10:00:00 +0000\r\n\r\nhello\r\n")
}

// Received sender names and addresses must not outrank or crowd out an exact saved address.
func TestReceivedMailCannotOutrankExactContactInAutocomplete(t *testing.T) {
	db := openLDATestDB(t)
	h := handler.New(repository.NewFolderRepository(db), repository.NewMessageRepository(db),
		repository.NewAttachmentRepository(db), repository.NewDraftRepository(db),
		repository.NewContactRepository(db), repository.NewIdentityRepository(db),
		repository.NewFilterRepository(db), repository.NewSpamFilterRepository(db), "")
	srv, err := oas.NewServer(h, oas.WithErrorHandler(handler.WriteError))
	require.NoError(t, err)

	// The owner has saved the genuine contact explicitly.
	req := httptest.NewRequest(http.MethodPost, "/contacts",
		strings.NewReader(`{"address":"bob@corp.com","name":"Bob Smith"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	// One attacker message, delivered to the Inbox through the real LDA pipeline.
	require.Equal(t, 0, runCore(db, attackerMessage(`"Bob J." <bob@corp.com.evil.example>`, "evil-1@evil.example")))

	items := autocompleteSuggestions(t, srv, "bob@corp.com")
	for i, c := range items {
		t.Logf("suggestion[%d] = %q <%s> explicit=%v", i, c.Name, c.Address, c.Explicit)
	}
	require.NotEmpty(t, items)
	assert.Equal(t, "bob@corp.com", items[0].Address,
		"the first result for an exact address must be that address")

	// Ten more attacker messages from distinct addresses containing the query.
	for i := 0; i < 10; i++ {
		require.Equal(t, 0, runCore(db, attackerMessage(
			fmt.Sprintf(`"A%02d" <bob@corp.com.x%d.example>`, i, i), fmt.Sprintf("evil-%d-b@evil.example", i))))
	}
	items = autocompleteSuggestions(t, srv, "bob@corp.com")
	found := false
	for i, c := range items {
		t.Logf("after flood suggestion[%d] = %q <%s> explicit=%v", i, c.Name, c.Address, c.Explicit)
		if c.Address == "bob@corp.com" {
			found = true
		}
	}
	assert.True(t, found, "the explicitly saved, exactly matching contact must stay in the autocomplete list")
}
