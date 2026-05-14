package lda

import (
	"database/sql"
	"errors"
	"net/mail"
	"os"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openLDATestDB creates a temp-file SQLite DB with the full schema and built-in folders.
func openLDATestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "mymail-lda-test-*.sqlite")
	require.NoError(t, err)
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	db, err := repository.OpenDB(path, 0)
	require.NoError(t, err, "OpenDB")
	t.Cleanup(func() { db.Close() })

	err = repository.SeedBuiltinFolders(db)
	require.NoError(t, err, "SeedBuiltinFolders")
	return db
}

// createFilter inserts a filter into the DB for pipeline tests.
func createFilter(t *testing.T, db *sql.DB, f oas.Filter) {
	t.Helper()
	repo := repository.NewFilterRepository(db)
	_, err := repo.CreateFilter(t.Context(), f, oas.OptInt{})
	require.NoError(t, err, "CreateFilter")
}

// queryMessage fetches folder_id and read flag for the given message_id.
func queryMessage(t *testing.T, db *sql.DB, messageID string) (folderID int, read bool) {
	t.Helper()
	var readInt int
	err := db.QueryRow(
		`SELECT folder_id, read FROM messages WHERE message_id = ?`, messageID,
	).Scan(&folderID, &readInt)
	require.NoError(t, err, "queryMessage %q", messageID)
	return folderID, readInt != 0
}

const simpleMsg = "From: Alice <alice@example.com>\r\n" +
	"To: bob@example.com\r\n" +
	"Subject: Hello\r\n" +
	"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
	"Message-Id: <hello001@example.com>\r\n" +
	"\r\n" +
	"Hello, Bob!\r\n"

func TestRunCore_SuccessfulDelivery(t *testing.T) {
	db := openLDATestDB(t)

	code := runCore(db, []byte(simpleMsg))
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "hello001@example.com")
	assert.Equal(t, 1, folderID, "folder_id should be Inbox")

	// Contact should be upserted.
	var addr string
	err := db.QueryRow(`SELECT address FROM contacts WHERE address = 'alice@example.com'`).Scan(&addr)
	assert.NoError(t, err, "sender not in contacts")
}

func TestRunCore_RawAndAttachments(t *testing.T) {
	raw := []byte("From: sender@example.com\r\n" +
		"To: recip@example.com\r\n" +
		"Subject: With Attachment\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <att001@example.com>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/mixed; boundary=\"B\"\r\n" +
		"\r\n" +
		"--B\r\n" +
		"Content-Type: text/plain\r\n" +
		"\r\n" +
		"Body text\r\n" +
		"--B\r\n" +
		"Content-Type: application/octet-stream\r\n" +
		"Content-Disposition: attachment; filename=\"file.bin\"\r\n" +
		"\r\n" +
		"BINARYDATA\r\n" +
		"--B--\r\n")

	db := openLDATestDB(t)
	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	var msgID int
	err := db.QueryRow(`SELECT id FROM messages WHERE message_id = 'att001@example.com'`).Scan(&msgID)
	assert.NoError(t, err, "message not found")

	var attCount int
	db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE message_id = ?`, msgID).Scan(&attCount)
	assert.Equal(t, 1, attCount)
}

func TestRunCore_DuplicateSkip(t *testing.T) {
	db := openLDATestDB(t)

	code := runCore(db, []byte(simpleMsg))
	assert.Equal(t, 0, code, "first delivery")

	code = runCore(db, []byte(simpleMsg))
	assert.Equal(t, 0, code, "duplicate delivery")

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = 'hello001@example.com'`).Scan(&count)
	assert.Equal(t, 1, count, "duplicate must not be stored")
}

func TestRunCore_ParseFailure(t *testing.T) {
	db := openLDATestDB(t)
	code := runCore(db, []byte("not a valid email\x00\x00"))
	assert.Equal(t, 1, code, "parse failure")
}

func TestRunCore_SpamXSpamFlag(t *testing.T) {
	raw := []byte("From: spammer@evil.com\r\n" +
		"To: victim@example.com\r\n" +
		"Subject: Buy now\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <spam001@evil.com>\r\n" +
		"X-Spam-Flag: YES\r\n" +
		"\r\n" +
		"Click here!\r\n")

	db := openLDATestDB(t)
	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "spam001@evil.com")
	assert.Equal(t, 7, folderID, "folder_id should be Junk")
}

func TestRunCore_SpamXSpamStatus(t *testing.T) {
	raw := []byte("From: spammer@evil.com\r\n" +
		"To: victim@example.com\r\n" +
		"Subject: Spam\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <spam002@evil.com>\r\n" +
		"X-Spam-Status: Yes, score=8.5\r\n" +
		"\r\n" +
		"Spam body\r\n")

	db := openLDATestDB(t)
	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "spam002@evil.com")
	assert.Equal(t, 7, folderID, "folder_id should be Junk")
}

func TestRunCore_SpamScoreHeader(t *testing.T) {
	raw := []byte("From: spammer@evil.com\r\n" +
		"To: victim@example.com\r\n" +
		"Subject: High score\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <spam003@evil.com>\r\n" +
		"X-Spam-Score: 7.2\r\n" +
		"\r\n" +
		"Body\r\n")

	db := openLDATestDB(t)
	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "spam003@evil.com")
	assert.Equal(t, 7, folderID, "folder_id should be Junk")
}

func TestRunCore_SpamDisabled(t *testing.T) {
	raw := []byte("From: spammer@evil.com\r\n" +
		"To: victim@example.com\r\n" +
		"Subject: Spam but filter off\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <spam004@evil.com>\r\n" +
		"X-Spam-Flag: YES\r\n" +
		"\r\n" +
		"Body\r\n")

	db := openLDATestDB(t)
	// Disable spam filter.
	_, err := db.Exec(`UPDATE spam_filter_settings SET enabled = 0 WHERE id = 1`)
	require.NoError(t, err, "disable spam filter")

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "spam004@evil.com")
	assert.Equal(t, 1, folderID, "folder_id should be Inbox")
}

func TestRunCore_FilterMove(t *testing.T) {
	raw := []byte("From: newsletter@acme.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Weekly digest\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <nl001@acme.com>\r\n" +
		"\r\n" +
		"Newsletter body\r\n")

	db := openLDATestDB(t)
	// Create a user folder (id >= 100) to move into.
	_, err := db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(100,'News','news',10)`)
	require.NoError(t, err, "create folder")

	createFilter(t, db, oas.Filter{
		MatchFrom: "newsletter@acme.com",
		Action:    oas.FilterActionMove,
		FolderID:  oas.NewOptNilInt(100),
		Stop:      true,
	})

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "nl001@acme.com")
	assert.Equal(t, 100, folderID, "folder_id should be News")
}

func TestRunCore_FilterTrash(t *testing.T) {
	raw := []byte("From: junk@example.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Trash this\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <trash001@example.com>\r\n" +
		"\r\n" +
		"Body\r\n")

	db := openLDATestDB(t)
	createFilter(t, db, oas.Filter{
		MatchFrom: "junk@example.com",
		Action:    oas.FilterActionTrash,
		Stop:      true,
	})

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "trash001@example.com")
	assert.Equal(t, 4, folderID, "folder_id should be Trash")
}

func TestRunCore_FilterMarkRead(t *testing.T) {
	raw := []byte("From: notify@service.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Notification\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <notif001@service.com>\r\n" +
		"\r\n" +
		"You have a notification\r\n")

	db := openLDATestDB(t)
	createFilter(t, db, oas.Filter{
		MatchFrom: "notify@service.com",
		Action:    oas.FilterActionMarkRead,
		Stop:      true,
	})

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, read := queryMessage(t, db, "notif001@service.com")
	assert.Equal(t, 1, folderID, "folder_id should be Inbox")
	assert.True(t, read, "read should be true")
}

func TestRunCore_FilterDrop(t *testing.T) {
	raw := []byte("From: drop@example.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Drop me\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <drop001@example.com>\r\n" +
		"\r\n" +
		"Dropped\r\n")

	db := openLDATestDB(t)
	createFilter(t, db, oas.Filter{
		MatchFrom: "drop@example.com",
		Action:    oas.FilterActionDrop,
		Stop:      true,
	})

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = 'drop001@example.com'`).Scan(&count)
	assert.Zero(t, count, "dropped message must not be stored in DB")
}

func TestRunCore_FilterStop(t *testing.T) {
	raw := []byte("From: news@example.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Stop after first match\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <stop001@example.com>\r\n" +
		"\r\n" +
		"Body\r\n")

	db := openLDATestDB(t)
	_, err := db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(101,'News','news101',10)`)
	require.NoError(t, err, "create folder")

	// First filter: move to News, stop=true.
	createFilter(t, db, oas.Filter{
		MatchFrom: "news@example.com",
		Action:    oas.FilterActionMove,
		FolderID:  oas.NewOptNilInt(101),
		Stop:      true,
	})
	// Second filter: trash — must NOT apply because first filter stopped evaluation.
	createFilter(t, db, oas.Filter{
		MatchFrom: "news@example.com",
		Action:    oas.FilterActionTrash,
		Stop:      false,
	})

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "stop001@example.com")
	assert.Equal(t, 101, folderID, "folder_id should be 101 (News): second filter should be stopped")
}

func TestRunCore_FilterMoveToDeletedFolder(t *testing.T) {
	raw := []byte("From: sender@example.com\r\n" +
		"To: me@example.com\r\n" +
		"Subject: Move to deleted folder\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <delfolder001@example.com>\r\n" +
		"\r\n" +
		"Body\r\n")

	db := openLDATestDB(t)
	_, err := db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES(100,'Temp','temp',10)`)
	require.NoError(t, err, "create folder")

	createFilter(t, db, oas.Filter{
		MatchFrom: "sender@example.com",
		Action:    oas.FilterActionMove,
		FolderID:  oas.NewOptNilInt(100),
		Stop:      true,
	})
	// Delete the folder; ON DELETE SET NULL cascades filter.folder_id to NULL.
	_, err = db.Exec(`DELETE FROM folders WHERE id = 100`)
	require.NoError(t, err, "delete folder")

	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	folderID, _ := queryMessage(t, db, "delfolder001@example.com")
	assert.Equal(t, 1, folderID, "folder_id should be 1 (Inbox): move to deleted folder should fall back to spam-determined folder")
}

func TestRunCore_ConcurrentDuplicateGuard(t *testing.T) {
	// Exercises the code path where INSERT OR IGNORE returns RowsAffected=0
	// because another process stored the same message_id between the SELECT EXISTS
	// check and the INSERT. We simulate this by pre-inserting the row directly,
	// then calling runCore — SELECT EXISTS catches it before INSERT fires, but the
	// observable outcome (exit 0, one row) is identical to the INSERT OR IGNORE n=0 path.
	db := openLDATestDB(t)
	now := "2024-01-01T00:00:00Z"
	_, err := db.Exec(
		`INSERT INTO messages(folder_id, message_id, date, created_at, updated_at) VALUES(1,?,?,?,?)`,
		"race001@example.com", now, now, now,
	)
	require.NoError(t, err, "pre-insert")

	raw := []byte("From: a@example.com\r\nTo: b@example.com\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"Message-Id: <race001@example.com>\r\n\r\nBody\r\n")

	code := runCore(db, raw)
	assert.Equal(t, 0, code, "concurrent duplicate must not fail with 75")

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM messages WHERE message_id = 'race001@example.com'`).Scan(&count)
	assert.Equal(t, 1, count)
}

func TestRunCore_NoMessageID_Generated(t *testing.T) {
	raw := []byte("From: sender@example.com\r\n" +
		"To: recip@domain.org\r\n" +
		"Subject: No ID\r\n" +
		"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n" +
		"\r\n" +
		"Body without Message-Id header\r\n")

	db := openLDATestDB(t)
	code := runCore(db, raw)
	assert.Equal(t, 0, code)

	var msgID string
	err := db.QueryRow(`SELECT message_id FROM messages WHERE from_addr = 'sender@example.com'`).Scan(&msgID)
	assert.NoError(t, err, "message not found")
	// Generated ID must be stored without angle brackets, consistent with how
	// ParseMessage strips them from real Message-Id headers via stripAngles.
	// Storing with brackets would break threading: a reply's In-Reply-To header
	// is also stripped to "uuid@domain", which would not match "<uuid@domain>".
	assert.NotContains(t, msgID, "<")
	assert.NotContains(t, msgID, ">")
	assert.Contains(t, msgID, "@domain.org")
}

// --- detectSpam unit tests ---

func TestDetectSpam(t *testing.T) {
	settings := oas.SpamFilterSettings{
		Enabled:        true,
		ScoreHeader:    "X-Spam-Score",
		ScoreThreshold: 5.0,
	}

	tests := []struct {
		name    string
		headers map[string][]string
		want    bool
	}{
		{
			name:    "X-Spam-Flag YES",
			headers: map[string][]string{"X-Spam-Flag": {"YES"}},
			want:    true,
		},
		{
			name:    "X-Spam-Flag yes lowercase",
			headers: map[string][]string{"X-Spam-Flag": {"yes"}},
			want:    true,
		},
		{
			name:    "X-Spam-Flag NO",
			headers: map[string][]string{"X-Spam-Flag": {"NO"}},
			want:    false,
		},
		{
			name:    "X-Spam-Status Yes exact no trailing char",
			headers: map[string][]string{"X-Spam-Status": {"Yes"}},
			want:    true,
		},
		{
			name:    "X-Spam-Status Yes comma",
			headers: map[string][]string{"X-Spam-Status": {"Yes, score=8.5 required=5.0"}},
			want:    true,
		},
		{
			name:    "X-Spam-Status Yes space",
			headers: map[string][]string{"X-Spam-Status": {"Yes "}},
			want:    true,
		},
		{
			name:    "X-Spam-Status Yesterday must not match",
			headers: map[string][]string{"X-Spam-Status": {"Yesterday was a good day"}},
			want:    false,
		},
		{
			name:    "X-Spam-Status No",
			headers: map[string][]string{"X-Spam-Status": {"No, score=1.0"}},
			want:    false,
		},
		{
			name:    "score at threshold",
			headers: map[string][]string{"X-Spam-Score": {"5.0"}},
			want:    true,
		},
		{
			name:    "score above threshold",
			headers: map[string][]string{"X-Spam-Score": {"9.9"}},
			want:    true,
		},
		{
			name:    "score below threshold",
			headers: map[string][]string{"X-Spam-Score": {"4.9"}},
			want:    false,
		},
		{
			name:    "score not parseable",
			headers: map[string][]string{"X-Spam-Score": {"high"}},
			want:    false,
		},
		{
			name:    "no spam headers",
			headers: map[string][]string{},
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectSpam(mail.Header(tc.headers), settings)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- filterMatches unit tests ---

func TestFilterMatches(t *testing.T) {
	pm := &model.ParsedMessage{
		FromAddr: "Alice <alice@example.com>",
		ToAddr:   "bob@example.com",
		CcAddr:   "charlie@example.com",
		Subject:  "Hello World",
	}

	tests := []struct {
		name   string
		filter oas.Filter
		want   bool
	}{
		{
			name:   "match_from hit",
			filter: oas.Filter{MatchFrom: "alice@example.com", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "match_from miss",
			filter: oas.Filter{MatchFrom: "other@example.com", Action: oas.FilterActionTrash},
			want:   false,
		},
		{
			name:   "match_from case-insensitive",
			filter: oas.Filter{MatchFrom: "ALICE@EXAMPLE.COM", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "match_to hits ToAddr",
			filter: oas.Filter{MatchTo: "bob@example.com", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "match_to hits CcAddr",
			filter: oas.Filter{MatchTo: "charlie@example.com", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "match_to miss",
			filter: oas.Filter{MatchTo: "nobody@example.com", Action: oas.FilterActionTrash},
			want:   false,
		},
		{
			name:   "match_subject hit",
			filter: oas.Filter{MatchSubject: "hello", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "match_subject miss",
			filter: oas.Filter{MatchSubject: "goodbye", Action: oas.FilterActionTrash},
			want:   false,
		},
		{
			name:   "AND: both criteria match",
			filter: oas.Filter{MatchFrom: "alice", MatchSubject: "world", Action: oas.FilterActionTrash},
			want:   true,
		},
		{
			name:   "AND: one criterion fails",
			filter: oas.Filter{MatchFrom: "alice", MatchSubject: "goodbye", Action: oas.FilterActionTrash},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterMatches(pm, tc.filter)
			assert.Equal(t, tc.want, got)
		})
	}
}

// --- withRetry unit tests ---

func TestWithRetry_SuccessImmediate(t *testing.T) {
	calls := 0
	err := withRetry(func() error {
		calls++
		return nil
	})
	assert.NoError(t, err)
	assert.Equal(t, 1, calls)
}

func TestWithRetry_NonBusyErrorNoRetry(t *testing.T) {
	sentinel := errors.New("some db error")
	calls := 0
	err := withRetry(func() error {
		calls++
		return sentinel
	})
	assert.ErrorIs(t, err, sentinel)
	assert.Equal(t, 1, calls, "non-busy errors must not be retried")
}
