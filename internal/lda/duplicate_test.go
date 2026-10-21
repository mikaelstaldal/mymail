package lda

import (
	"bytes"
	"fmt"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestCollidingMessageIDsPreserveDistinctMail(t *testing.T) {
	db := openLDATestDB(t)
	raw := func(from, body, extra string) []byte {
		return []byte(fmt.Sprintf("From: %s\r\nTo: user@example.com\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-ID: <predictable@example.com>\r\nSubject: notification\r\n%s\r\n%s", from, extra, body))
	}
	require.Equal(t, 0, runCore(db, raw("evil@example.com", "plant", "X-Spam-Flag: YES\r\n")))
	_, err := db.Exec(`UPDATE messages SET folder_id = 4`)
	require.NoError(t, err)
	genuine := raw("real@example.com", "genuine", "")
	require.Equal(t, 0, runCore(db, genuine))
	require.Equal(t, 0, runCore(db, raw("real@example.com", "genuine", "Received: by another-mta\r\n")))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count))
	assert.Equal(t, 2, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages WHERE folder_id = 1 AND from_addr = 'real@example.com'`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestInvalidMessageIDsDoNotDeduplicate(t *testing.T) {
	for _, id := range []string{"<>", "no-domain", "<@example.com>", "<a@>", "<a@@b>", "<a@b", "a@b>", "<a@b> garbage"} {
		t.Run(id, func(t *testing.T) {
			db := openLDATestDB(t)
			raw := []byte("From: sender@example.com\r\nMessage-ID: " + id + "\r\n\r\nBody")
			pm, err := ParseMessage(raw)
			require.NoError(t, err)
			assert.Nil(t, pm.MessageID)
			require.Equal(t, 0, runCore(db, raw))
			require.Equal(t, 0, runCore(db, raw))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(DISTINCT message_id) FROM messages`).Scan(&count))
			assert.Equal(t, 2, count)
		})
	}
}

func TestImportBatchContentAwareDuplicates(t *testing.T) {
	db := openLDATestDB(t)
	var batch []batchEntry
	for _, body := range []string{"plant", "genuine", "genuine"} {
		raw := []byte("From: sender@example.com\r\nMessage-ID: <collision@example.com>\r\n\r\n" + body)
		pm, err := ParseMessage(raw)
		require.NoError(t, err)
		batch = append(batch, batchEntry{pm: pm, raw: raw, date: "2024-01-01T00:00:00Z"})
	}
	inserted, err := commitBatch(t.Context(), db, batch, 1, repository.NewContactRepository(db))
	require.NoError(t, err)
	assert.Equal(t, 2, inserted)
	inserted, err = commitBatch(t.Context(), db, batch, 1, repository.NewContactRepository(db))
	require.NoError(t, err)
	assert.Zero(t, inserted)
}

func TestConcurrentContentDuplicateDelivery(t *testing.T) {
	db := openLDATestDB(t)
	results := make(chan int, 2)
	for i := range 2 {
		go func() {
			raw := []byte(fmt.Sprintf("Received: by mta-%d\r\nFrom: sender@example.com\r\nMessage-ID: <concurrent@example.com>\r\n\r\nBody", i))
			results <- runCore(db, raw)
		}()
	}
	assert.Zero(t, <-results)
	assert.Zero(t, <-results)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count))
	assert.Equal(t, 1, count)
}

func TestDeliveryFingerprintContentAndTransport(t *testing.T) {
	original := "From: sender@example.com\r\nDate: Mon, 01 Jan 2024 12:00:00 +0000\r\nSubject: Notice\r\nContent-Type: text/plain\r\n\r\nBody"
	for _, tc := range []struct{ name, old, replacement string }{
		{"sender", "sender@example.com", "evil@example.com"},
		{"date", "12:00:00", "13:00:00"},
		{"subject", "Notice", "Another notice"},
		{"MIME type", "text/plain", "text/html"},
		{"body", "Body", "Different body"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.NotEqual(t, deliveryFingerprint([]byte(original)), deliveryFingerprint([]byte(strings.Replace(original, tc.old, tc.replacement, 1))))
		})
	}
	assert.Equal(t, deliveryFingerprint([]byte(original)), deliveryFingerprint([]byte("Received: by mta\r\nAuthentication-Results: checker; dmarc=pass\r\n"+original)))
}

func TestSafetyHeadersDistinguishOtherwiseIdenticalDeliveries(t *testing.T) {
	for _, header := range []string{"Reply-To", "Sender", "DKIM-Signature", "MIME-Version", "List-ID"} {
		t.Run(header, func(t *testing.T) {
			db := openLDATestDB(t)
			base := "From: notifications@example.com\r\nMessage-ID: <predictable@example.com>\r\nSubject: Notification\r\n"
			plant := []byte(base + header + ": attacker\r\n\r\nIdentical body")
			genuine := []byte(base + header + ": genuine\r\n\r\nIdentical body")
			require.Zero(t, runCore(db, plant))
			require.Zero(t, runCore(db, genuine))
			require.Zero(t, runCore(db, genuine))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count))
			assert.Equal(t, 2, count)
		})
	}
}

func TestLegacyContentDuplicatesAndIndexedCopies(t *testing.T) {
	db := openLDATestDB(t)
	raw := []byte("From: legacy@example.com\r\nMessage-ID: <legacy@example.com>\r\n\r\nOriginal")
	_, err := db.Exec(`INSERT INTO messages(folder_id,message_id,raw,date,created_at,updated_at)
 VALUES(1,'legacy@example.com',?,'2024-01-01','2024-01-01','2024-01-01')`, raw)
	require.NoError(t, err)
	require.Zero(t, runCore(db, raw), "legacy raw fallback deduplicates")
	different := bytes.Replace(raw, []byte("Original"), []byte("Different"), 1)
	require.Zero(t, runCore(db, different), "legacy ID cannot suppress distinct content")
	var total, indexed int
	require.NoError(t, db.QueryRow(`SELECT count(*), count(content_fingerprint) FROM messages`).Scan(&total, &indexed))
	assert.Equal(t, 2, total)
	assert.Equal(t, 1, indexed)
	// New copies match their indexed digest without reparsing stored raw. This
	// also exercises coexistence with an older NULL fingerprint for the same ID.
	_, err = db.Exec(`UPDATE messages SET raw=x'00' WHERE content_fingerprint IS NOT NULL`)
	require.NoError(t, err)
	require.Zero(t, runCore(db, different))
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages`).Scan(&total))
	assert.Equal(t, 2, total)
}

func TestUTF8MessageIDIsPreserved(t *testing.T) {
	pm, err := ParseMessage([]byte("From: sender@example.com\r\nMessage-ID: <通知@example.com>\r\n\r\nBody"))
	require.NoError(t, err)
	require.NotNil(t, pm.MessageID)
	assert.Equal(t, "通知@example.com", *pm.MessageID)
}
