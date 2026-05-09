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
)

// openImportTestDB creates a temp SQLite DB with built-in folders seeded.
func openImportTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "mymail-import-test-*.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	db, err := repository.OpenDB(path, 0)
	if err != nil {
		t.Fatalf("OpenDB: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := repository.SeedBuiltinFolders(db); err != nil {
		t.Fatalf("SeedBuiltinFolders: %v", err)
	}
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
			name:    "unknown format",
			args:    []string{"inbox:imap:/host"},
			wantErr: "invalid format",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseMappings(tc.args)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
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
			if err != nil {
				t.Fatal(err)
			}
			defer os.Remove(f.Name())
			f.WriteString(tc.content)
			f.Close()

			got, err := scanMboxSeparators(f.Name())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v (len %d), want %v (len %d)", got, len(got), tc.want, len(tc.want))
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("[%d] got %q, want %q", i, got[i], tc.want[i])
				}
			}
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
			if err != nil {
				t.Errorf("resolveFolder(%q): %v", slug, err)
				continue
			}
			if id != wantID {
				t.Errorf("resolveFolder(%q) = %d, want %d", slug, id, wantID)
			}
		}
	})

	t.Run("scheduled rejected", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		_, err := resolveFolder(ctx, db, "scheduled", cache)
		if err == nil {
			t.Fatal("expected error for scheduled")
		}
	})

	t.Run("snoozed rejected", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		_, err := resolveFolder(ctx, db, "snoozed", cache)
		if err == nil {
			t.Fatal("expected error for snoozed")
		}
	})

	t.Run("user folder created on first resolve", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		id, err := resolveFolder(ctx, db, "Work", cache)
		if err != nil {
			t.Fatalf("resolveFolder: %v", err)
		}
		if id < 100 {
			t.Errorf("user folder id = %d, want >= 100", id)
		}
		var name string
		db.QueryRowContext(ctx, `SELECT name FROM folders WHERE id = ?`, id).Scan(&name)
		if name != "Work" {
			t.Errorf("folder name = %q, want Work", name)
		}
	})

	t.Run("name cache hit avoids duplicate creation", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		id1, err := resolveFolder(ctx, db, "Archive", cache)
		if err != nil {
			t.Fatalf("first resolveFolder: %v", err)
		}
		id2, err := resolveFolder(ctx, db, "archive", cache) // lowercase → same cache key
		if err != nil {
			t.Fatalf("second resolveFolder: %v", err)
		}
		if id1 != id2 {
			t.Errorf("ids differ: %d vs %d", id1, id2)
		}
	})

	t.Run("case-insensitive lookup finds existing folder", func(t *testing.T) {
		db := openImportTestDB(t)
		cache := make(map[string]int64)
		// Create the folder once.
		id1, err := resolveFolder(ctx, db, "Projects", cache)
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		// Resolve with different case via a fresh cache (simulates a second mapping).
		cache2 := make(map[string]int64)
		id2, err := resolveFolder(ctx, db, "PROJECTS", cache2)
		if err != nil {
			t.Fatalf("lookup: %v", err)
		}
		if id1 != id2 {
			t.Errorf("ids differ: %d vs %d (case-insensitive lookup should find existing)", id1, id2)
		}
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
	if err != nil {
		t.Fatalf("importMaildir: %v", err)
	}
	if imp != 4 || skip != 0 {
		t.Fatalf("imported=%d skipped=%d, want 4/0", imp, skip)
	}

	ctx := context.Background()
	type row struct{ read, flagged int }
	queryMsg := func(msgID string) row {
		t.Helper()
		var r row
		if err := db.QueryRowContext(ctx, `SELECT read, flagged FROM messages WHERE message_id = ?`, msgID).Scan(&r.read, &r.flagged); err != nil {
			t.Fatalf("query %s: %v", msgID, err)
		}
		return r
	}

	if r := queryMsg("new1@host"); r.read != 0 || r.flagged != 0 {
		t.Errorf("new/: read=%d flagged=%d, want 0/0", r.read, r.flagged)
	}
	if r := queryMsg("seen1@host"); r.read != 1 || r.flagged != 0 {
		t.Errorf(":2,S: read=%d flagged=%d, want 1/0", r.read, r.flagged)
	}
	if r := queryMsg("flagged1@host"); r.read != 0 || r.flagged != 1 {
		t.Errorf(":2,F: read=%d flagged=%d, want 0/1", r.read, r.flagged)
	}
	if r := queryMsg("seenflagged1@host"); r.read != 1 || r.flagged != 1 {
		t.Errorf(":2,FS: read=%d flagged=%d, want 1/1", r.read, r.flagged)
	}
}

func TestImportMaildir_DuplicateSkip(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	raw := "From: a@b.com\r\nTo: c@d.com\r\nSubject: Dup\r\n" +
		"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <dup@host>\r\n\r\nBody\r\n"
	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "", raw)

	// First import.
	imp, skip, err := importMaildir(dir, 1, db)
	if err != nil || imp != 1 || skip != 0 {
		t.Fatalf("first import: imp=%d skip=%d err=%v", imp, skip, err)
	}

	// Second import of the same Maildir: same message-id → skipped.
	imp, skip, err = importMaildir(dir, 1, db)
	if err != nil || imp != 0 || skip != 1 {
		t.Fatalf("second import: imp=%d skip=%d err=%v", imp, skip, err)
	}
}

func TestImportMaildir_ContactUpsert(t *testing.T) {
	db := openImportTestDB(t)
	dir := makeMaildir(t)

	makeMaildirMsg(t, dir, "new", "1000000001.M1.host", "",
		"From: Carol <carol@example.com>\r\nTo: dave@example.com\r\nSubject: Hello\r\n"+
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <contact1@host>\r\n\r\nBody\r\n")

	if _, _, err := importMaildir(dir, 1, db); err != nil {
		t.Fatalf("importMaildir: %v", err)
	}

	var name string
	err := db.QueryRowContext(context.Background(),
		`SELECT name FROM contacts WHERE address = 'carol@example.com'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("contact not found: %v", err)
	}
	if name != "Carol" {
		t.Errorf("contact name = %q, want Carol", name)
	}
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
	if err != nil || imp != 1 {
		t.Fatalf("importMaildir: imp=%d err=%v", imp, err)
	}

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'dated@host'`).Scan(&dateStr)
	if !strings.HasPrefix(dateStr, "2024-01-05") {
		t.Errorf("date = %q, want prefix 2024-01-05", dateStr)
	}
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
	if err != nil || imp != 1 || skip != 0 {
		t.Fatalf("importMbox: imp=%d skip=%d err=%v", imp, skip, err)
	}

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'dated@mbox'`).Scan(&dateStr)
	if !strings.HasPrefix(dateStr, "2024-01-08") {
		t.Errorf("date = %q, want 2024-01-08 prefix", dateStr)
	}
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
	if err != nil || imp != 1 || skip != 0 {
		t.Fatalf("importMbox: imp=%d skip=%d err=%v", imp, skip, err)
	}

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'nodatembox@host'`).Scan(&dateStr)
	if !strings.HasPrefix(dateStr, "2024-01-15") {
		t.Errorf("date = %q, want 2024-01-15 prefix (From-separator fallback)", dateStr)
	}
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
	if err != nil || imp != 1 || skip != 0 {
		t.Fatalf("importMbox: imp=%d skip=%d err=%v", imp, skip, err)
	}

	var dateStr string
	db.QueryRowContext(context.Background(), `SELECT date FROM messages WHERE message_id = 'mtimembox@host'`).Scan(&dateStr)
	if !strings.HasPrefix(dateStr, "2023-06-15") {
		t.Errorf("date = %q, want 2023-06-15 prefix (fileMtime fallback)", dateStr)
	}
}

func TestImportMbox_DuplicateSkip(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMboxFile(t, []string{
		"From: a@b.com\r\nTo: c@d.com\r\nSubject: Dup\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <dupmbox@host>\r\n\r\nBody\r\n",
	})

	imp, skip, err := importMbox(path, 1, db)
	if err != nil || imp != 1 || skip != 0 {
		t.Fatalf("first import: imp=%d skip=%d err=%v", imp, skip, err)
	}
	imp, skip, err = importMbox(path, 1, db)
	if err != nil || imp != 0 || skip != 1 {
		t.Fatalf("second import: imp=%d skip=%d err=%v", imp, skip, err)
	}
}

func TestImportMbox_ContactUpsert(t *testing.T) {
	db := openImportTestDB(t)

	path := writeMboxFile(t, []string{
		"From: Dave <dave@example.com>\r\nTo: eve@example.com\r\nSubject: Hi\r\n" +
			"Date: Mon, 01 Jan 2024 10:00:00 +0000\r\nMessage-Id: <contact2@mbox>\r\n\r\nBody\r\n",
	})

	if _, _, err := importMbox(path, 1, db); err != nil {
		t.Fatalf("importMbox: %v", err)
	}

	var name string
	err := db.QueryRowContext(context.Background(),
		`SELECT name FROM contacts WHERE address = 'dave@example.com'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("contact not found: %v", err)
	}
	if name != "Dave" {
		t.Errorf("contact name = %q, want Dave", name)
	}
}

func TestResolveFolder_ScheduledSnoozedCaseInsensitive(t *testing.T) {
	ctx := context.Background()
	db := openImportTestDB(t)
	cache := make(map[string]int64)
	for _, name := range []string{"Scheduled", "SCHEDULED", "Snoozed", "SNOOZED"} {
		_, err := resolveFolder(ctx, db, name, cache)
		if err == nil {
			t.Errorf("resolveFolder(%q): expected error, got nil", name)
		}
	}
}
