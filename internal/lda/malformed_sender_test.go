package lda

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
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

// A second From field, in obsolete `From :` syntax or not, may be the one a
// DMARC checker evaluated (SpamAssassin strips the whitespace and takes the
// first). Its pass must not exempt the other From as authenticated own mail.
func TestSecondFromFieldCannotBorrowDMARCPass(t *testing.T) {
	raw := func(from, id string) string {
		return from + "To: own@example.com\r\nSubject: note to self\r\nMessage-ID: <" + id + ">\r\n" +
			"X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\n" +
			"X-Spam-Status: No, tests=DMARC_PASS,DKIM_VALID_AU,SPF_PASS autolearn=ham\r\n" +
			"Content-Type: text/html; charset=utf-8\r\n\r\n" +
			"<p><a href=\"https://evil.example/login\">example.com</a></p>\r\n"
	}
	setup := func(t *testing.T) *sql.DB {
		db := openLDATestDB(t)
		_, err := db.Exec(`INSERT INTO identities(name,address,is_default,position) VALUES('Me','own@example.com',1,0);
			UPDATE spam_filter_settings SET trusted_checker_host='checker', enabled=0 WHERE id=1`)
		require.NoError(t, err)
		return db
	}
	t.Run("control: authenticated own-identity mail is exempt", func(t *testing.T) {
		db := setup(t)
		from, d := senderDeliverAndRead(t, db, raw("From: Me <own@example.com>\r\n", "ctl@x"), "ctl@x")
		assert.Equal(t, "Me <own@example.com>", from)
		assert.Empty(t, d.Warnings)
	})
	for _, tc := range []struct{ name, from, stored string }{
		{"obsolete-syntax From", "From : x@attacker.example\r\nFrom: Me <own@example.com>\r\n", "Me <own@example.com>, x@attacker.example"},
		{"two From fields", "From: x@attacker.example\r\nFrom: Me <own@example.com>\r\n", "x@attacker.example, Me <own@example.com>"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := setup(t)
			from, d := senderDeliverAndRead(t, db, raw(tc.from, "obs@x"), "obs@x")
			assert.Equal(t, tc.stored, from)
			assert.Equal(t, "unknown", string(d.SenderAuthentication.Dmarc))
			assert.Contains(t, senderKinds(d), "malformed_sender")
			assert.Contains(t, senderKinds(d), "deceptive_link")
			assert.Empty(t, senderContacts(t, db))
		})
	}
}

func TestDecodeFromFields(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		want   string
	}{
		{"none", nil, ""},
		{"one", []string{`"Doe, J" <j@example.com>`}, `"Doe, J" <j@example.com>`},
		{"two", []string{"a@example.com", "B <b@example.com>"}, "a@example.com, B <b@example.com>"},
		{"one malformed", []string{"a@example.com", "B <b@example..com>"}, "a@example.com, B <b@example..com>"},
		// Each field is malformed alone, but joined by a comma they would
		// read back as the single mailbox `"x, y" <ceo@bank.com>`.
		{"straddling a quote", []string{`"x`, `y" <ceo@bank.com>`}, "\"x\n" + `y" <ceo@bank.com>`},
		// An empty group leaves one mailbox from two fields.
		{"empty group", []string{"undisclosed:;", "ceo@bank.com"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeFromFields(tc.fields)
			assert.Equal(t, tc.want, got)
			if len(tc.fields) > 1 {
				_, err := mail.ParseAddress(got)
				assert.Error(t, err, "several From fields read back as one mailbox")
			}
		})
	}
}

// A bidi override in the addr-spec makes it display as another address, even
// inside the UI's isolating <bdi>.
func TestFormatCharacterInSenderAddress(t *testing.T) {
	for _, from := range []string{"PayPal <\u202emoc.lapyap@ecivres>", "Support <service@\u202emoc.lapyap>"} {
		t.Run(from, func(t *testing.T) {
			db := openLDATestDB(t)
			_, d := senderDeliverAndRead(t, db, "From: "+from+fmt.Sprintf(senderBEC, "bidi@x"), "bidi@x")
			assert.Contains(t, senderKinds(d), "malformed_sender")
			assert.Contains(t, senderKinds(d), "first_time_sender")
			assert.Empty(t, senderContacts(t, db), "contact collected for an address that displays as another")
		})
	}
}

// Display names with specials must be stored quoted, or the stored header no
// longer parses as what was sent and every sender check is skipped.
func TestStoredAddressHeadersKeepTheirMailboxes(t *testing.T) {
	db := openLDATestDB(t)
	_, err := db.Exec(`INSERT INTO messages(folder_id,from_addr,to_addr,date,created_at,updated_at)
		VALUES(2,'me@mydomain.com','x@paypal.com, y@bank.com','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	for i, tc := range []struct {
		name, headers, contact string
		want                   []string
	}{
		{"comma in name, lookalike domain", "From: \"Pay, Pal\" <attacker@paypa1.com>\r\n", "attacker@paypa1.com",
			[]string{"lookalike_domain", "first_time_sender"}},
		{"comma in names, reply-to", "From: \"Bank, Security Team\" <alerts@xn--bnk-sdd.com>\r\nReply-To: \"Bank, Billing\" <pay@evil.example>\r\n", "alerts@xn--bnk-sdd.com",
			[]string{"lookalike_domain", "reply_to_mismatch", "first_time_sender"}},
		{"address list in quoted name", "From: \"PayPal <service@paypal.com>, x\" <attacker@evil.example>\r\n", "attacker@evil.example",
			[]string{"display_name_address", "first_time_sender"}},
		{"address list in encoded name", "From: =?utf-8?q?PayPal_=3Cservice=40paypal=2Ecom=3E=2C_x?= <attacker@evil.example>\r\n", "attacker@evil.example",
			[]string{"display_name_address", "first_time_sender"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := fmt.Sprintf("q%d@x", i)
			raw := tc.headers + "To: me@mydomain.com\r\nSubject: s\r\nMessage-ID: <" + id + ">\r\n\r\nbody\r\n"
			from, d := senderDeliverAndRead(t, db, raw, id)
			a, err := mail.ParseAddress(from)
			require.NoError(t, err, from)
			assert.Equal(t, tc.contact, a.Address)
			assert.Equal(t, tc.want, senderKinds(d))
			assert.Contains(t, senderContacts(t, db), tc.contact)
		})
	}
}
