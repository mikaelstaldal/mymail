package lda

import (
	"context"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An explicit (user-created) contact saved without a name must not get its
// display name from an unauthenticated, DMARC-failing received message.
func TestSpoofedFromDoesNotNameExplicitContact(t *testing.T) {
	db := openLDATestDB(t)
	ctx := context.Background()

	// Trust the SpamAssassin host so the sender-authentication verdict is
	// computed and stored; spam scoring itself stays off so the message is
	// delivered to Inbox.
	spamRepo := repository.NewSpamFilterRepository(db)
	settings, err := spamRepo.GetSpamFilterSettings(ctx)
	require.NoError(t, err)
	settings.Enabled = false
	settings.TrustedCheckerHost = oas.NewOptString("checker")
	_, err = spamRepo.UpdateSpamFilterSettings(ctx, settings)
	require.NoError(t, err)

	// The owner saves carol@corp.com as a contact, without a name.
	contacts := repository.NewContactRepository(db)
	created, err := contacts.CreateContact(ctx, oas.Contact{Address: "carol@corp.com"})
	require.NoError(t, err)
	require.True(t, created.Explicit)
	require.Empty(t, created.Name)

	// A remote sender forges From with Carol's address and another person's name.
	raw := []byte("From: \"Alice Smith\" <carol@corp.com>\r\n" +
		"To: victim@example.org\r\n" +
		"Subject: hi\r\n" +
		"Message-ID: <spoof@attacker.example>\r\n" +
		"X-Spam-Checker-Version: SpamAssassin 4.0.1 (2024-03-25) on checker\r\n" +
		"X-Spam-Status: No, tests=DMARC_REJECT,SPF_FAIL autolearn=ham\r\n" +
		"\r\nbody\r\n")
	require.Equal(t, 0, runCore(db, raw))

	folder, _ := queryMessage(t, db, "spoof@attacker.example")
	var verdict string
	require.NoError(t, db.QueryRow(
		`SELECT sender_authentication FROM messages WHERE message_id = 'spoof@attacker.example'`).Scan(&verdict))
	assert.Equal(t, int64(1), int64(folder))
	assert.Contains(t, verdict, `"dmarc":"fail"`)

	got, err := contacts.GetContact(ctx, int64(created.ID))
	require.NoError(t, err)
	assert.Equal(t, created.Address, got.Address)
	assert.True(t, got.Explicit)

	// The query the compose autocomplete sends.
	q := "alice"
	items, _, err := contacts.ListContacts(ctx, &q, 10, 0)
	require.NoError(t, err)
	for _, c := range items {
		t.Logf("autocomplete %q -> %q <%s> explicit=%v", q, c.Name, c.Address, c.Explicit)
	}

	assert.Empty(t, got.Name, "spoofed From display name was written onto an explicit contact")
	assert.Empty(t, items, "autocomplete for 'alice' must not offer carol@corp.com")
}
