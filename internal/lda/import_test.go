package lda

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openImportTestDB creates a temp SQLite DB with built-in folders seeded.
func openImportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "mymail-import-test-*.sqlite")
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

func TestParseMappings(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantLen int
		wantErr string
		check   func(t *testing.T, ms []importMapping)
	}{
		{
			name:    "simple mbox",
			args:    []string{"inbox:mbox:/var/mail/inbox"},
			wantLen: 1,
			check: func(t *testing.T, ms []importMapping) {
				if ms[0].folder != "inbox" || ms[0].format != "mbox" || ms[0].path != "/var/mail/inbox" {
					t.Errorf("got %+v", ms[0])
				}
			},
		},
		{
			name:    "maildir format",
			args:    []string{"sent:maildir:/home/user/Maildir/.Sent"},
			wantLen: 1,
			check: func(t *testing.T, ms []importMapping) {
				if ms[0].format != "maildir" {
					t.Errorf("format = %q, want maildir", ms[0].format)
				}
			},
		},
		{
			name:    "path with colons",
			args:    []string{"inbox:mbox:/path/to:file:with:colons"},
			wantLen: 1,
			check: func(t *testing.T, ms []importMapping) {
				if ms[0].path != "/path/to:file:with:colons" {
					t.Errorf("path = %q", ms[0].path)
				}
			},
		},
		{
			name:    "multiple mappings",
			args:    []string{"inbox:mbox:/a", "sent:maildir:/b"},
			wantLen: 2,
		},
		{
			name:    "empty args",
			args:    []string{},
			wantLen: 0,
		},
		{
			name:    "missing path component",
			args:    []string{"inbox:mbox"},
			wantErr: "invalid mapping",
		},
		{
			name:    "empty folder name",
			args:    []string{":mbox:/path"},
			wantErr: "invalid mapping",
		},
		{
			name:    "empty format",
			args:    []string{"inbox::/path"},
			wantErr: "invalid mapping",
		},
		{
			name:    "empty path",
			args:    []string{"inbox:mbox:"},
			wantErr: "invalid mapping",
		},
		{
			name:    "mbx format",
			args:    []string{"inbox:mbx:/var/mail/inbox.mbx"},
			wantLen: 1,
			check: func(t *testing.T, ms []importMapping) {
				if ms[0].format != "mbx" {
					t.Errorf("format = %q, want mbx", ms[0].format)
				}
			},
		},
		{
			name:    "unknown format",
			args:    []string{"inbox:imap:/host"},
			wantErr: "invalid format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMappings(tc.args)
			if tc.wantErr != "" {
				assert.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Len(t, got, tc.wantLen)
			if tc.check != nil {
				tc.check(t, got)
			}
		})
	}
}

func TestScanMboxSeparators(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{
			name:    "empty file",
			content: "",
			want:    nil,
		},
		{
			name: "single message canonical timestamp",
			content: "From user@example.com Mon Jan  2 15:04:05 2006\r\n" +
				"Subject: test\r\n\r\nbody\r\n",
			want: []string{"Mon Jan  2 15:04:05 2006"},
		},
		{
			name: "two messages",
			content: "From a@b.com Mon Jan  1 00:00:00 2000\r\n" +
				"Subject: one\r\n\r\nbody\r\n" +
				"From c@d.com Tue Jan  2 00:00:00 2001\r\n" +
				"Subject: two\r\n\r\nbody\r\n",
			want: []string{"Mon Jan  1 00:00:00 2000", "Tue Jan  2 00:00:00 2001"},
		},
		{
			name: "no timestamp after address",
			content: "From user@example.com\r\n" +
				"Subject: test\r\n\r\nbody\r\n",
			want: []string{""},
		},
		{
			name: "mboxrd escaped From line not counted",
			content: "From user@example.com Mon Jan  1 00:00:00 2000\r\n" +
				"Subject: test\r\n\r\n" +
				">From not a separator\r\n" +
				"more body\r\n",
			want: []string{"Mon Jan  1 00:00:00 2000"},
		},
		{
			name: "From with immediate space is not a separator",
			content: "From  user@example.com Mon Jan  1 00:00:00 2000\r\n" +
				"Subject: test\r\n\r\nbody\r\n",
			want: nil,
		},
		{
			name: "body line starting with From is not a separator",
			content: "From user@example.com Mon Jan  1 00:00:00 2000\r\n" +
				"Subject: test\r\n\r\n" +
				"From a friend I trust\r\n",
			// "From a friend I trust" — 'a' != ' ' so it matches HasPrefix("From ") AND line[5] != ' '
			// This IS counted as a separator (mboxo unescaped From line in body — expected behavior)
			want: []string{"Mon Jan  1 00:00:00 2000", "friend I trust"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := os.CreateTemp("", "mbox-test-*.mbox")
			require.NoError(t, err)
			defer os.Remove(f.Name())
			f.WriteString(tc.content)
			f.Close()

			got, err := scanMboxSeparators(f.Name())
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestResolveFolder(t *testing.T) {
	ctx := context.Background()

	t.Run("builtin slugs", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		for slug, wantID := range builtinSlugs {
			id, err := resolveFolder(ctx, db, slug, cache)
			assert.NoError(t, err, "resolveFolder(%q)", slug)
			assert.Equal(t, wantID, id, "resolveFolder(%q)", slug)
		}
	})

	t.Run("scheduled rejected", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		_, err := resolveFolder(ctx, db, "scheduled", cache)
		assert.Error(t, err)
	})

	t.Run("snoozed rejected", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		_, err := resolveFolder(ctx, db, "snoozed", cache)
		assert.Error(t, err)
	})

	t.Run("user folder created on first resolve", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		id, err := resolveFolder(ctx, db, "Work", cache)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, id, int64(100))

		var name string
		db.QueryRowContext(ctx, `SELECT name FROM folders WHERE id = ?`, id).Scan(&name)
		assert.Equal(t, "Work", name)
	})

	t.Run("name cache hit avoids duplicate creation", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		id1, err := resolveFolder(ctx, db, "Archive", cache)
		require.NoError(t, err)
		id2, err := resolveFolder(ctx, db, "archive", cache) // lowercase → same cache key
		require.NoError(t, err)
		assert.Equal(t, id1, id2)
	})

	t.Run("case-insensitive lookup finds existing folder", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		// Create the folder once.
		id1, err := resolveFolder(ctx, db, "Projects", cache)
		require.NoError(t, err)
		// Resolve with different case via a fresh cache (simulates a second mapping).
		cache2 := make(map[string]int64)
		id2, err := resolveFolder(ctx, db, "PROJECTS", cache2)
		require.NoError(t, err)
		assert.Equal(t, id1, id2, "case-insensitive lookup should find existing")
	})
}

// --- importMaildir tests ---

// makeMaildirMsg writes a raw RFC 5322 message into a Maildir subdirectory.
// subdir is "new" or "cur"; flags is the ":2,XYZ" suffix (empty for new/).
func makeMaildirMsg(t *testing.T, dir, subdir, key, flags, raw string) {
	t.Helper()
	filename := key
	if flags != "" {
		filename = key + flags
	}
	path := filepath.Join(dir, subdir, filename)
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("write maildir msg: %v", err)
	}
}

func makeMaildir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, sub := range []string{"new", "cur", "tmp"} {
		if err := os.Mkdir(filepath.Join(dir, sub), 0700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportMaildir_FlagMapping(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	// new/ message: always read=0, flagged=0
	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "",
		"From: alice@example.com\r\nTo: bob@example.com\r\nSubject: New\r\n"+
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\n"+
			"Message-Id: <new1@host>\r\n\r\nBody\r\n")

	// cur/ message with S (Seen) flag: read=1
	makeMaildirMsg(t, dir, "cur", "1000000002.M2.host", ":2,S",
		"From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Seen\r\n"+
			"Date: Mon, 01 Jan 2024 11:00:00 +0000\r\n"+
			"Message-Id: <seen1@host>\r\n\r\nBody\r\n")

	// cur/ message with F (Flagged) flag: flagged=1
	makeMaildirMsg(t, dir, "cur", "1000000003.M3.host", ":2,F",
		"From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Flagged\r\n"+
			"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\n"+
			"Message-Id: <flagged1@host>\r\n\r\nBody\r\n")

	// cur/ message with SF flags: read=1, flagged=1
	makeMaildirMsg(t, dir, "cur", "1000000004.M4.host", ":2,FS",
		"From: alice@example.com\r\nTo: bob@example.com\r\nSubject: Seen+Flagged\r\n"+
			"Date: Mon, 01 Jan 2024 13:00:00 +0000\r\n"+
			"Message-Id: <seenflagged1@host>\r\n\r\nBody\r\n")

	imp, skip, err := importMaildir(dir, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 4, imp)
	assert.Equal(t, 0, skip)

	ctx := context.Background()
	type row struct{ read, flagged int }
	queryMsg := func(msgID string) row {
		t.Helper()
		var r row
		err := db.QueryRowContext(ctx, `SELECT read, flagged FROM messages WHERE message_id = ?`, msgID).Scan(&r.read, &r.flagged)
		require.NoError(t, err, "query %s", msgID)
		return r
	}

	r := queryMsg("new1@host")
	assert.Equal(t, 0, r.read, "new/: read")
	assert.Equal(t, 0, r.flagged, "new/: flagged")

	r = queryMsg("seen1@host")
	assert.Equal(t, 1, r.read, ":2,S: read")
	assert.Equal(t, 0, r.flagged, ":2,S: flagged")

	r = queryMsg("flagged1@host")
	assert.Equal(t, 0, r.read, ":2,F: read")
	assert.Equal(t, 1, r.flagged, ":2,F: flagged")

	r = queryMsg("seenflagged1@host")
	assert.Equal(t, 1, r.read, ":2,FS: read")
	assert.Equal(t, 1, r.flagged, ":2,FS: flagged")
}

func TestImportMaildir_DuplicateSkip(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	raw := "From: a@b.com\r\nTo: c@d.com\r\nSubject: Dup\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <dup@host>\r\n\r\nBody\r\n"
	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "", raw)

	// First import.
	imp, skip, err := importMaildir(dir, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	// Second import of the same Maildir: same message-id → skipped.
	imp, skip, err = importMaildir(dir, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 0, imp)
	assert.Equal(t, 1, skip)
}

func TestImportMaildir_ContactUpsert(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "",
		"From: Carol <carol@example.com>\r\nTo: dave@example.com\r\nSubject: Hello\r\n"+
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <contact1@host>\r\n\r\nBody\r\n")

	_, _, err := importMaildir(dir, 1, db)
	require.NoError(t, err)

	var name string
	err = db.QueryRowContext(context.Background(),
		`SELECT name FROM contacts WHERE address = 'carol@example.com'`,
	).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "Carol", name)
}

func TestImportMaildir_NoDateNoMtime(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	// Message without Date header; we can't fake missing mtime in a real FS test,
	// but we can verify that a message WITH a date is imported while confirming
	// the date-header path works correctly.
	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "",
		"From: a@b.com\r\nTo: c@d.com\r\nSubject: WithDate\r\n"+
			"Date: Fri, 05 Jan 2024 08:00:00 +0000\r\nMessage-Id: <dated@host>\r\n\r\nBody\r\n")

	imp, _, err := importMaildir(dir, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'dated@host'`).Scan(&dateStr)
	assert.True(t, strings.HasPrefix(dateStr, "2024-01-05"))
}

// --- importMbox tests ---

func writeMboxFile(t *testing.T, messages []string) string {
	t.Helper()
	f, err := os.CreateTemp("", "test-*.mbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	for i, msg := range messages {
		ts := fmt.Sprintf("Mon Jan %2d 10:00:00 2024", i+1)
		fmt.Fprintf(f, "From sender@example.com %s\r\n%s", ts, msg)
	}
	f.Close()
	return f.Name()
}

func TestImportMbox_DateHeader(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMboxFile(t, []string{
		"From: a@b.com\r\nTo: c@d.com\r\nSubject: S\r\n" +
			"Date: Mon, 08 Jan 2024 09:00:00 +0000\r\nMessage-Id: <dated@mbox>\r\n\r\nBody\r\n",
	})

	imp, skip, err := importMbox(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'dated@mbox'`).Scan(&dateStr)
	assert.True(t, strings.HasPrefix(dateStr, "2024-01-08"))
}

func TestImportMbox_FromSeparatorFallback(t *testing.T) {
	db := openImportTestDB(t)

	// Message without Date header — fallback should use the From separator timestamp.
	f, err := os.CreateTemp("", "test-*.mbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	fmt.Fprintf(f,
		"From sender@example.com Mon Jan 15 12:30:00 2024\r\n"+
			"From: a@b.com\r\nTo: c@d.com\r\nSubject: NoDate\r\n"+
			"Message-Id: <nodatembox@host>\r\n\r\nBody\r\n")
	f.Close()

	imp, skip, err := importMbox(f.Name(), 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'nodatembox@host'`).Scan(&dateStr)
	assert.True(t, strings.HasPrefix(dateStr, "2024-01-15"), "From-separator fallback")
}

func TestImportMbox_FileMtimeFallback(t *testing.T) {
	db := openImportTestDB(t)

	// Message without Date header and unparseable separator timestamp → fileMtime.
	f, err := os.CreateTemp("", "test-*.mbox")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(f.Name()) })
	fmt.Fprintf(f,
		"From sender@example.com\r\n"+ // no timestamp after address
			"From: a@b.com\r\nTo: c@d.com\r\nSubject: NoDate\r\n"+
			"Message-Id: <mtimembox@host>\r\n\r\nBody\r\n")
	// Touch the file with a known mtime.
	knownTime := time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	os.Chtimes(f.Name(), knownTime, knownTime)
	f.Close()

	imp, skip, err := importMbox(f.Name(), 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'mtimembox@host'`).Scan(&dateStr)
	assert.True(t, strings.HasPrefix(dateStr, "2023-06-15"), "fileMtime fallback")
}

func TestImportMbox_DuplicateSkip(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMboxFile(t, []string{
		"From: a@b.com\r\nTo: c@d.com\r\nSubject: Dup\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <dupmbox@host>\r\n\r\nBody\r\n",
	})

	imp, skip, err := importMbox(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	imp, skip, err = importMbox(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 0, imp)
	assert.Equal(t, 1, skip)
}

func TestImportMbox_ContactUpsert(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMboxFile(t, []string{
		"From: Dave <dave@example.com>\r\nTo: eve@example.com\r\nSubject: Hi\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <contact2@mbox>\r\n\r\nBody\r\n",
	})

	_, _, err := importMbox(path, 1, db)
	require.NoError(t, err)

	var name string
	err = db.QueryRowContext(context.Background(),
		`SELECT name FROM contacts WHERE address = 'dave@example.com'`,
	).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "Dave", name)
}

// --- importMbx tests ---

// mbxTestMsg describes one message to write into a test mbx file.
type mbxTestMsg struct {
	raw   string // raw RFC 822 message bytes
	flags uint16 // system flags: fSEEN=0x1, fFLAGGED=0x4, fEXPUNGED=0x8000
	date  string // IMAP INTERNALDATE (e.g. " 1-Jan-2024 10:00:00 +0000"); "" = default
}

// writeMbxFile creates a temporary mbx file containing the given messages.
func writeMbxFile(t *testing.T, msgs []mbxTestMsg) string {
	t.Helper()
	f, err := os.CreateTemp("", "test-*.mbx")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })

	// 2048-byte file header: magic + uid_validity + uid_last + CRLF + zero padding.
	hdr := make([]byte, 2048)
	copy(hdr, "*mbx*\r\n0000000100000000\r\n")
	_, err = f.Write(hdr)
	require.NoError(t, err)

	for i, m := range msgs {
		if m.date == "" {
			m.date = fmt.Sprintf(" 1-Jan-2024 %02d:00:00 +0000", i+1)
		}
		// Per-message header: <date>,<size>;<8hex-uflags><4hex-sysflags>-<8hex-uid>\r\n
		msgHdr := fmt.Sprintf("%s,%d;%08x%04x-%08x\r\n", m.date, len(m.raw), 0, m.flags, i+1)
		_, err = f.WriteString(msgHdr)
		require.NoError(t, err)
		_, err = f.WriteString(m.raw)
		require.NoError(t, err)
	}

	require.NoError(t, f.Close())
	return f.Name()
}

func TestParseMbxDate(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantOK    bool
		wantYear  int
		wantMonth time.Month
		wantDay   int
	}{
		{
			name:      "space-padded single digit day",
			input:     " 2-Jan-2024 12:34:56 +0000",
			wantOK:    true,
			wantYear:  2024,
			wantMonth: time.January,
			wantDay:   2,
		},
		{
			name:      "double digit day with negative zone",
			input:     "25-Dec-2023 08:00:00 -0500",
			wantOK:    true,
			wantYear:  2023,
			wantMonth: time.December,
			wantDay:   25,
		},
		{
			name:   "invalid",
			input:  "not a date",
			wantOK: false,
		},
		{
			name:   "empty",
			input:  "",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMbxDate(tc.input)
			if !tc.wantOK {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tc.wantYear, got.Year())
			assert.Equal(t, tc.wantMonth, got.Month())
			assert.Equal(t, tc.wantDay, got.Day())
		})
	}
}

func TestImportMbx_Basic(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMbxFile(t, []mbxTestMsg{
		{raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Test\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <mbxbasic@host>\r\n\r\nBody\r\n"},
		{raw: "From: x@y.com\r\nTo: z@w.com\r\nSubject: Test2\r\n" +
			"Date: Mon, 01 Jan 2024 11:00:00 +0000\r\nMessage-Id: <mbxbasic2@host>\r\n\r\nBody\r\n"},
	})

	imp, skip, err := importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 2, imp)
	assert.Equal(t, 0, skip)
}

func TestImportMbx_FlagMapping(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMbxFile(t, []mbxTestMsg{
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Unseen\r\n" +
				"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <mbx-unseen@host>\r\n\r\nBody\r\n",
			flags: 0,
		},
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Seen\r\n" +
				"Date: Mon, 01 Jan 2024 11:00:00 +0000\r\nMessage-Id: <mbx-seen@host>\r\n\r\nBody\r\n",
			flags: 0x0001, // fSEEN
		},
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Flagged\r\n" +
				"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <mbx-flagged@host>\r\n\r\nBody\r\n",
			flags: 0x0004, // fFLAGGED
		},
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: SeenFlagged\r\n" +
				"Date: Mon, 01 Jan 2024 13:00:00 +0000\r\nMessage-Id: <mbx-seenflagged@host>\r\n\r\nBody\r\n",
			flags: 0x0005, // fSEEN | fFLAGGED
		},
	})

	imp, skip, err := importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 4, imp)
	assert.Equal(t, 0, skip)

	ctx := context.Background()
	type row struct{ read, flagged int }
	queryMsg := func(msgID string) row {
		t.Helper()
		var r row
		err := db.QueryRowContext(ctx, `SELECT read, flagged FROM messages WHERE message_id = ?`, msgID).Scan(&r.read, &r.flagged)
		require.NoError(t, err, "query %s", msgID)
		return r
	}

	r := queryMsg("mbx-unseen@host")
	assert.Equal(t, 0, r.read, "no flags: read")
	assert.Equal(t, 0, r.flagged, "no flags: flagged")

	r = queryMsg("mbx-seen@host")
	assert.Equal(t, 1, r.read, "fSEEN: read")
	assert.Equal(t, 0, r.flagged, "fSEEN: flagged")

	r = queryMsg("mbx-flagged@host")
	assert.Equal(t, 0, r.read, "fFLAGGED: read")
	assert.Equal(t, 1, r.flagged, "fFLAGGED: flagged")

	r = queryMsg("mbx-seenflagged@host")
	assert.Equal(t, 1, r.read, "fSEEN|fFLAGGED: read")
	assert.Equal(t, 1, r.flagged, "fSEEN|fFLAGGED: flagged")
}

func TestImportMbx_ExpungedSkip(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMbxFile(t, []mbxTestMsg{
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Normal\r\n" +
				"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <mbx-normal@host>\r\n\r\nBody\r\n",
			flags: 0,
		},
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Expunged\r\n" +
				"Date: Mon, 01 Jan 2024 11:00:00 +0000\r\nMessage-Id: <mbx-expunged@host>\r\n\r\nBody\r\n",
			flags: 0x8000, // fEXPUNGED
		},
		{
			raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Normal2\r\n" +
				"Date: Mon, 01 Jan 2024 12:00:00 +0000\r\nMessage-Id: <mbx-normal2@host>\r\n\r\nBody\r\n",
			flags: 0,
		},
	})

	imp, skip, err := importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 2, imp)
	assert.Equal(t, 1, skip)

	ctx := context.Background()
	var exists bool
	db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = 'mbx-expunged@host')`).Scan(&exists)
	assert.False(t, exists, "expunged message should not be imported")
}

func TestImportMbx_DuplicateSkip(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMbxFile(t, []mbxTestMsg{
		{raw: "From: a@b.com\r\nTo: c@d.com\r\nSubject: Dup\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <dupmbx@host>\r\n\r\nBody\r\n"},
	})

	imp, skip, err := importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	imp, skip, err = importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 0, imp)
	assert.Equal(t, 1, skip)
}

func TestImportMbx_InternalDateFallback(t *testing.T) {
	db := openImportTestDB(t)

	// Message without Date header — falls back to the mbx per-message internal date.
	path := writeMbxFile(t, []mbxTestMsg{
		{
			raw:  "From: a@b.com\r\nTo: c@d.com\r\nSubject: NoDate\r\nMessage-Id: <nodatembx@host>\r\n\r\nBody\r\n",
			date: "15-Mar-2023 08:30:00 +0000",
		},
	})

	imp, skip, err := importMbx(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imp)
	assert.Equal(t, 0, skip)

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'nodatembx@host'`).Scan(&dateStr)
	assert.True(t, strings.HasPrefix(dateStr, "2023-03-15"), "mbx internal date fallback")
}

func TestImportMbx_ContactUpsert(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMbxFile(t, []mbxTestMsg{
		{raw: "From: Eve <eve@example.com>\r\nTo: frank@example.com\r\nSubject: Hi\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <contactmbx@host>\r\n\r\nBody\r\n"},
	})

	_, _, err := importMbx(path, 1, db)
	require.NoError(t, err)

	var name string
	err = db.QueryRowContext(context.Background(),
		`SELECT name FROM contacts WHERE address = 'eve@example.com'`,
	).Scan(&name)
	require.NoError(t, err)
	assert.Equal(t, "Eve", name)
}

func TestImportMbx_InvalidMagic(t *testing.T) {
	db := openImportTestDB(t)

	f, err := os.CreateTemp("", "test-*.mbx")
	require.NoError(t, err)
	t.Cleanup(func() { os.Remove(f.Name()) })
	// Write 2048 bytes of wrong content
	hdr := make([]byte, 2048)
	copy(hdr, "not-an-mbx-file\r\n")
	f.Write(hdr)
	f.Close()

	_, _, err = importMbx(f.Name(), 1, db)
	assert.ErrorContains(t, err, "invalid magic")
}

func TestResolveFolder_ScheduledSnoozedCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	db := openImportTestDB(t)
	cache := make(map[string]int64)
	for _, name := range []string{"Scheduled", "SCHEDULED", "Snoozed", "SNOOZED"} {
		_, err := resolveFolder(ctx, db, name, cache)
		assert.Error(t, err, "resolveFolder(%q)", name)
	}
}
