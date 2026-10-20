package lda

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/handler"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// senderDeliverAndRead delivers raw RFC 5322 through the real LDA pipeline
// (runCore), then reads the message back through the full handler stack
// (GET /messages/{id}), as the web UI does.
func senderDeliverAndRead(t *testing.T, db *sql.DB, raw, msgID string) (string, oas.MessageDetail) {
	t.Helper()
	require.Equal(t, 0, runCore(db, []byte(raw)))
	var id int64
	var from string
	require.NoError(t, db.QueryRow(`SELECT id, from_addr FROM messages WHERE message_id = ?`, msgID).Scan(&id, &from))
	h := handler.New(
		repository.NewFolderRepository(db), repository.NewMessageRepository(db),
		repository.NewAttachmentRepository(db), repository.NewDraftRepository(db),
		repository.NewContactRepository(db), repository.NewIdentityRepository(db),
		repository.NewFilterRepository(db), repository.NewSpamFilterRepository(db), "",
	)
	srv, err := oas.NewServer(h, oas.WithErrorHandler(handler.WriteError))
	require.NoError(t, err)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/messages/%d", id), nil))
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var d oas.MessageDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &d))
	return from, d
}

func senderKinds(d oas.MessageDetail) []string {
	out := []string{}
	for _, w := range d.Warnings {
		out = append(out, string(w.Kind))
	}
	return out
}

func senderContacts(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`SELECT address FROM contacts`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		require.NoError(t, rows.Scan(&a))
		out = append(out, a)
	}
	return out
}

// The user has written to ceo@bank.com before, so genuine mail from that
// address legitimately shows no first_time_sender warning.
func senderSeedSentToCEO(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO messages(folder_id,from_addr,to_addr,date,created_at,updated_at)
		VALUES(2,'me@mydomain.com','ceo@bank.com','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
}

const senderBEC = "\r\nTo: me@mydomain.com\r\nSubject: wire\r\nMessage-ID: <%s>\r\n\r\nPlease wire $50k today.\r\n"

// A From whose addr-spec does not parse must not be stored as its display
// name: every consumer re-parses from_addr, so a name such as "ceo@bank.com"
// would read back as a well-formed, possibly known, sender — while sender
// authentication describes the attacker's real domain.
func TestMalformedFromMustNotStoreDisplayNameAsSender(t *testing.T) {
	// Control: a well-formed attacker address with an innocuous name is
	// flagged first_time_sender, and the stored address is the attacker's.
	t.Run("control: well-formed attacker address", func(t *testing.T) {
		db := openLDATestDB(t)
		senderSeedSentToCEO(t, db)
		from, d := senderDeliverAndRead(t, db, `From: "Bank CEO" <xy@attacker.example>`+fmt.Sprintf(senderBEC, "ctl@x"), "ctl@x")
		assert.Equal(t, `Bank CEO <xy@attacker.example>`, from)
		assert.Contains(t, senderKinds(d), "first_time_sender")
	})

	for _, tc := range []struct{ name, from string }{
		{"consecutive dots in local part", `"ceo@bank.com" <x..y@attacker.example>`},
		{"space in domain", `"ceo@bank.com" <evil@evil com>`},
		{"encoded-word name, space in local part", `=?utf-8?q?ceo=40bank.com?= <a b@evil.com>`},
		{"trailing-dot domain", `"ceo@bank.com" <evil@attacker.example.>`},
	} {
		t.Run("known correspondent: "+tc.name, func(t *testing.T) {
			db := openLDATestDB(t)
			senderSeedSentToCEO(t, db)
			from, d := senderDeliverAndRead(t, db, "From: "+tc.from+fmt.Sprintf(senderBEC, "bec@x"), "bec@x")
			assert.NotEqual(t, "ceo@bank.com", from, "attacker's display name stored as the sender address")
			assert.Contains(t, senderKinds(d), "malformed_sender")
		})
	}

	t.Run("no addr-spec at all", func(t *testing.T) {
		db := openLDATestDB(t)
		from, d := senderDeliverAndRead(t, db, `From: "Bank Security" <a@b..c>`+fmt.Sprintf(senderBEC, "nos@x"), "nos@x")
		assert.NotEqual(t, "Bank Security", from)
		assert.Contains(t, senderKinds(d), "malformed_sender")
	})

	t.Run("several mailboxes in From", func(t *testing.T) {
		db := openLDATestDB(t)
		from, d := senderDeliverAndRead(t, db, `From: x@evil.com, "Bank" <ceo@bank.com>`+fmt.Sprintf(senderBEC, "multi@x"), "multi@x")
		assert.Equal(t, `x@evil.com, Bank <ceo@bank.com>`, from)
		assert.Contains(t, senderKinds(d), "malformed_sender")
		assert.Empty(t, senderContacts(t, db), "contact collected from one of several mailboxes")
	})

	// Own-identity spoof: the attacker DKIM-signs its own domain, the trusted
	// checker reports DMARC_PASS (for evil.example), and the display name is
	// the user's own address. The HTML body carries a deceptive link.
	ownRaw := func(from, id string) string {
		return "From: " + from + "\r\nTo: me@mydomain.com\r\nSubject: note to self\r\nMessage-ID: <" + id + ">\r\n" +
			"X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\n" +
			"X-Spam-Status: No, tests=DMARC_PASS,\r\n\tDKIM_VALID_AU,SPF_PASS autolearn=ham\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n\r\n" +
			"<p><a href=\"https://evil.example/login\">mydomain.com</a></p>\r\n"
	}
	ownSetup := func(t *testing.T) *sql.DB {
		db := openLDATestDB(t)
		_, err := db.Exec(`INSERT INTO identities(name,address,is_default,position) VALUES('Me','me@mydomain.com',1,0);
			UPDATE spam_filter_settings SET trusted_checker_host='checker', enabled=0 WHERE id=1`)
		require.NoError(t, err)
		return db
	}
	t.Run("control: DMARC-pass mail from a well-formed attacker address", func(t *testing.T) {
		db := ownSetup(t)
		from, d := senderDeliverAndRead(t, db, ownRaw(`"Me" <ab@evil.example>`, "ownctl@x"), "ownctl@x")
		assert.Equal(t, `Me <ab@evil.example>`, from)
		assert.Contains(t, senderKinds(d), "deceptive_link")
	})
	t.Run("own identity spoof: contact upsert and DMARC-pass exemption", func(t *testing.T) {
		db := ownSetup(t)
		from, d := senderDeliverAndRead(t, db, ownRaw(`"me@mydomain.com" <a..b@evil.example>`, "own@x"), "own@x")
		contacts := senderContacts(t, db)
		assert.NotEqual(t, "me@mydomain.com", from, "attacker's display name stored as the user's own address")
		assert.NotContains(t, contacts, "me@mydomain.com", "contact auto-collected for the substituted address")
		assert.Contains(t, senderKinds(d), "deceptive_link", "own-identity exemption suppressed every warning")
		assert.Contains(t, senderKinds(d), "malformed_sender")
	})

	// A well-formed spoof must keep its display name quoted when stored, or
	// the stored string no longer parses and every sender check is skipped.
	t.Run("quoted display name containing an address", func(t *testing.T) {
		db := openLDATestDB(t)
		from, d := senderDeliverAndRead(t, db, `From: "boss@company.org" <evil@example.com>`+fmt.Sprintf(senderBEC, "boss@x"), "boss@x")
		assert.Equal(t, `"boss@company.org" <evil@example.com>`, from)
		assert.Equal(t, []string{"display_name_address", "first_time_sender"}, senderKinds(d))
	})
}
