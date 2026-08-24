package repository

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
)

// openTestDB opens an in-memory SQLite database with the full schema applied.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	f, err := os.CreateTemp("", "mymail-msg-test-*.sqlite")
	require.NoError(t, err)
	f.Close()
	path := f.Name()
	t.Cleanup(func() { os.Remove(path) })

	db, err := OpenDB(path, 0)
	require.NoError(t, err, "OpenDB")
	t.Cleanup(func() { db.Close() })

	// Seed built-in folders.
	_, err = db.Exec(`INSERT INTO folders(id,name,slug,position) VALUES
		(1,'Inbox','inbox',0),(2,'Sent','sent',1),(3,'Drafts','drafts',2),
		(4,'Trash','trash',3),(5,'Scheduled','scheduled',4),
		(6,'Snoozed','snoozed',5),(7,'Junk','junk',6)`)
	require.NoError(t, err, "seed folders")
	return db
}

func makeMsg(folderID int, subject string) model.DBMessage {
	now := time.Now().UTC().Truncate(time.Second)
	return model.DBMessage{
		FolderID:  folderID,
		FromAddr:  "sender@example.com",
		ToAddr:    "to@example.com",
		Subject:   subject,
		Date:      now,
		BodyText:  "hello world",
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func TestInsertAndGetMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "Test subject")
	msg.MessageID = sql.NullString{String: "abc@test", Valid: true}
	msg.References = sql.NullString{String: "ref1@x\nref2@x", Valid: true}

	id, err := r.InsertMessage(ctx, msg)
	require.NoError(t, err)
	assert.NotZero(t, id)

	got, err := r.GetMessage(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "Test subject", got.Subject)
	assert.True(t, got.MessageID.Valid)
	assert.Equal(t, "abc@test", got.MessageID.String)
	assert.True(t, got.References.Valid)
	assert.Equal(t, "ref1@x\nref2@x", got.References.String)
}

func TestGetMessageNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.GetMessage(ctx, 9999)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestListMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	for i := range 3 {
		msg := makeMsg(1, "msg")
		msg.Date = time.Now().UTC().Add(time.Duration(i) * time.Minute)
		_, err := r.InsertMessage(ctx, msg)
		require.NoError(t, err)
	}

	items, total, err := r.ListMessages(ctx, 1, 10, 0, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	assert.Len(t, items, 3)
	// Should be ordered by date DESC — newest first.
	assert.True(t, items[0].Date.After(items[1].Date), "expected descending date order")
}

// Paging a folder whose messages all share one date: every message must appear
// on exactly one page, in the declared ascending-id order.
//
// **This test passes with and without the `, m.id ASC` it exists to defend, and
// that is worth knowing before trusting it.** It was written to demonstrate the
// bug and does not: removing the clause and re-running leaves it green, for the
// unfiltered, unread-filtered and flagged-filtered listings alike. The reason is
// idx_messages_folder_date — (folder_id, date DESC) — which every reachable plan
// for this query uses, and an index scan already returns ascending rowid within
// equal keys. So SQLite's incidental behaviour today matches the clause exactly.
//
// It is kept because what it pins is still real: the *declared* order, so that a
// later edit to that ORDER BY (dropping it as redundant, or mirroring it to
// m.id DESC to match the date direction) is a failing test rather than a silent
// change. What it cannot do is prove the clause is load-bearing — nothing here
// can, because on this schema and this SQLite it is not, yet. Do not cite it as
// evidence that the ordering is enforced.
//
// Same-second dates are not contrived: an import assigns them freely, and a Date
// header has only second resolution to begin with.
func TestListMessagesPagingIsStableAcrossTies(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	const n = 40
	const page = 10
	same := time.Date(2026, 5, 4, 3, 2, 1, 0, time.UTC)
	for range n {
		msg := makeMsg(1, "tied")
		msg.Date = same
		_, err := r.InsertMessage(ctx, msg)
		require.NoError(t, err)
	}

	var paged []int
	for off := 0; off < n; off += page {
		items, total, err := r.ListMessages(ctx, 1, page, off, nil, nil)
		require.NoError(t, err)
		require.Equal(t, n, total)
		require.Len(t, items, page, "page at offset %d", off)
		for _, it := range items {
			paged = append(paged, it.ID)
		}
	}

	seen := make(map[int]bool, n)
	for _, id := range paged {
		assert.False(t, seen[id], "id %d returned on more than one page", id)
		seen[id] = true
	}
	assert.Len(t, seen, n, "paging did not cover every message exactly once")

	// And the order is the declared one: ascending id within the tie.
	assert.IsIncreasing(t, paged, "tied rows must come back in ascending id order")
}

// The Scheduled and Snoozed listings show their own time in a column, which is
// only possible because the summary carries send_at and snoozed_until — the
// listing never fetches a message detail.
func TestListMessagesCarriesScheduleTimes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	sendAt := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	scheduled := makeMsg(5, "scheduled")
	scheduled.SendAt = sql.NullTime{Time: sendAt, Valid: true}
	_, err := r.InsertMessage(ctx, scheduled)
	require.NoError(t, err)

	items, _, err := r.ListMessages(ctx, 5, 10, 0, nil, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].SendAt.Null)
	assert.True(t, sendAt.Equal(items[0].SendAt.Value), "got %v", items[0].SendAt.Value)
	assert.True(t, items[0].SnoozedUntil.Null)

	until := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Second)
	inboxID, err := r.InsertMessage(ctx, makeMsg(1, "to-snooze"))
	require.NoError(t, err)
	_, err = r.SnoozeMessage(ctx, inboxID, until)
	require.NoError(t, err)

	items, _, err = r.ListMessages(ctx, 6, 10, 0, nil, nil)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].SnoozedUntil.Null)
	assert.True(t, until.Equal(items[0].SnoozedUntil.Value), "got %v", items[0].SnoozedUntil.Value)
	assert.True(t, items[0].SendAt.Null)

	// An ordinary message has neither, and null is what the column renders as
	// empty rather than as a zero date.
	_, err = r.InsertMessage(ctx, makeMsg(1, "plain"))
	require.NoError(t, err)
	items, _, err = r.ListMessages(ctx, 1, 10, 0, nil, nil)
	require.NoError(t, err)
	require.NotEmpty(t, items)
	assert.True(t, items[0].SendAt.Null)
	assert.True(t, items[0].SnoozedUntil.Null)
}

func TestUpdateMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, err := r.InsertMessage(ctx, makeMsg(1, "upd"))
	require.NoError(t, err)

	got, err := r.UpdateMessage(ctx, id, map[string]any{"read": true, "flagged": true})
	require.NoError(t, err)
	assert.True(t, got.Read)
	assert.True(t, got.Flagged)
}

func TestUpdateMessageNotFound(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.UpdateMessage(ctx, 9999, map[string]any{"read": true})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDeleteMessageToTrash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "del"))
	err := r.DeleteMessage(ctx, id)
	require.NoError(t, err)

	got, err := r.GetMessage(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 4, got.FolderID, "folder_id should be 4 (Trash)")
}

func TestDeleteMessagePermanentFromTrash(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(4, "del-perm"))
	err := r.DeleteMessage(ctx, id)
	require.NoError(t, err)

	_, err = r.GetMessage(ctx, id)
	assert.ErrorIs(t, err, ErrNotFound, "expected ErrNotFound after permanent delete")
}

func TestBulkDeleteMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id1, _ := r.InsertMessage(ctx, makeMsg(1, "a"))
	id2, _ := r.InsertMessage(ctx, makeMsg(4, "b")) // already in Trash → permanent

	_, _, err := r.BulkDeleteMessages(ctx, []int64{id1, id2})
	require.NoError(t, err)

	// id1 should have moved to Trash.
	got1, err := r.GetMessage(ctx, id1)
	require.NoError(t, err)
	assert.Equal(t, 4, got1.FolderID)

	// id2 should be permanently deleted.
	_, err = r.GetMessage(ctx, id2)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestBulkDeleteMissingID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "x"))
	_, _, err := r.BulkDeleteMessages(ctx, []int64{id, 99999})
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestBulkUpdateMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id1, _ := r.InsertMessage(ctx, makeMsg(1, "u1"))
	id2, _ := r.InsertMessage(ctx, makeMsg(1, "u2"))

	readTrue := true
	n, err := r.BulkUpdateMessages(ctx, []int64{id1, id2}, &readTrue, nil)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	got, err := r.GetMessage(ctx, id1)
	require.NoError(t, err)
	assert.True(t, got.Read)
}

func TestMoveMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "move"))
	n, err := r.MoveMessages(ctx, []int64{id}, 4)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	got, err := r.GetMessage(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 4, got.FolderID)
}

func TestMoveMessagesMissingID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	_, err := r.MoveMessages(ctx, []int64{99999}, 4)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestGetRawMessage(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "raw")
	msg.Raw = []byte("raw bytes")
	id, _ := r.InsertMessage(ctx, msg)

	raw, err := r.GetRawMessage(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, "raw bytes", string(raw))
}

func TestGetRawMessageNilForDraft(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(3, "draft"))
	raw, err := r.GetRawMessage(ctx, id)
	require.NoError(t, err)
	assert.Nil(t, raw)
}

func TestSnoozeAndCancelSnooze(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "snooze"))
	until := time.Now().UTC().Add(5 * time.Minute)

	got, err := r.SnoozeMessage(ctx, id, until)
	require.NoError(t, err)
	assert.Equal(t, 6, got.FolderID)
	assert.True(t, got.SnoozedUntil.Valid)
	assert.True(t, got.SnoozeFolder.Valid)
	assert.Equal(t, int64(1), got.SnoozeFolder.Int64)

	// Cancel snooze.
	got2, err := r.CancelSnooze(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, got2.FolderID)
	assert.False(t, got2.SnoozedUntil.Valid)
}

func TestSnoozeTooSoon(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "soon"))
	_, err := r.SnoozeMessage(ctx, id, time.Now().UTC().Add(30*time.Second))
	assert.Error(t, err)
}

func TestSnoozeForbiddenFolder(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(3, "draft"))
	_, err := r.SnoozeMessage(ctx, id, time.Now().UTC().Add(5*time.Minute))
	assert.Error(t, err)
}

func TestCancelSnoozeNotSnoozed(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "not-snoozed"))
	_, err := r.CancelSnooze(ctx, id)
	assert.Error(t, err)
}

func TestMarkJunkAndNotJunk(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	id, _ := r.InsertMessage(ctx, makeMsg(1, "junk test"))

	got, err := r.MarkJunk(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 7, got.FolderID)
	assert.True(t, got.Read)

	got2, err := r.MarkNotJunk(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, 1, got2.FolderID)
	assert.False(t, got2.Read)
}

func TestGetMessageThread_HeaderBased(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	// Insert parent with message_id "parent@x".
	parent := makeMsg(1, "thread subject")
	parent.MessageID = sql.NullString{String: "parent@x", Valid: true}
	pid, _ := r.InsertMessage(ctx, parent)

	// Insert child that references parent via in_reply_to.
	child := makeMsg(1, "Re: thread subject")
	child.MessageID = sql.NullString{String: "child@x", Valid: true}
	child.InReplyTo = sql.NullString{String: "parent@x", Valid: true}
	child.Date = parent.Date.Add(time.Minute)
	cid, _ := r.InsertMessage(ctx, child)

	summaries, truncated, err := r.GetMessageThread(ctx, pid)
	require.NoError(t, err)
	assert.False(t, truncated)

	ids := make(map[int64]bool)
	for _, s := range summaries {
		ids[int64(s.ID)] = true
	}
	assert.True(t, ids[pid])
	assert.True(t, ids[cid])
}

func TestGetMessageThread_SubjectFallback(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	// Two messages with no thread headers but same normalized subject.
	m1 := makeMsg(1, "Hello World")
	m1.Date = time.Now().UTC()
	id1, _ := r.InsertMessage(ctx, m1)

	m2 := makeMsg(1, "Re: Hello World")
	m2.Date = m1.Date.Add(time.Minute)
	id2, _ := r.InsertMessage(ctx, m2)

	summaries, _, err := r.GetMessageThread(ctx, id1)
	require.NoError(t, err)

	ids := make(map[int64]bool)
	for _, s := range summaries {
		ids[int64(s.ID)] = true
	}
	assert.True(t, ids[id1])
	assert.True(t, ids[id2])
}

func TestSearchMessages(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "FTS test")
	msg.BodyText = "the quick brown fox"
	_, err := r.InsertMessage(ctx, msg)
	require.NoError(t, err)

	items, total, err := r.SearchMessages(ctx, "quick", nil, nil, nil, nil, nil, SortRelevance, 10, 0)
	require.NoError(t, err)
	assert.NotEmpty(t, items)
	assert.NotZero(t, total)
	// The matched term is highlighted with ** markers in the snippet.
	assert.Contains(t, items[0].Snippet, "**quick**")
}

// The date orderings, and the id tiebreaker that makes paging them stable. The
// bodies are deliberately unequal in how well they match so that relevance would
// order them differently from either date sort — otherwise a test could pass
// with the sort ignored entirely.
func TestSearchMessagesDateSort(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	base := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	oldest := makeMsg(1, "needle in the subject too")
	oldest.Date = base
	oldestID, err := r.InsertMessage(ctx, oldest)
	require.NoError(t, err)

	// Same date as the message below, so only the id tiebreaker separates them —
	// ascending in both directions, so this one leads the pair either way.
	tiedEarlier := makeMsg(1, "middle")
	tiedEarlier.Date = base.Add(time.Hour)
	tiedEarlier.BodyText = "needle"
	tiedEarlierID, err := r.InsertMessage(ctx, tiedEarlier)
	require.NoError(t, err)

	tiedLater := makeMsg(1, "middle")
	tiedLater.Date = base.Add(time.Hour)
	tiedLater.BodyText = "needle"
	tiedLaterID, err := r.InsertMessage(ctx, tiedLater)
	require.NoError(t, err)

	newest := makeMsg(1, "newest")
	newest.Date = base.Add(2 * time.Hour)
	newest.BodyText = "needle"
	newestID, err := r.InsertMessage(ctx, newest)
	require.NoError(t, err)

	ids := func(items []oas.MessagesSearchGetOKItemsItem) []int64 {
		out := make([]int64, len(items))
		for i, it := range items {
			out[i] = int64(it.ID)
		}
		return out
	}

	t.Run("date_desc", func(t *testing.T) {
		items, total, err := r.SearchMessages(ctx, "needle", nil, nil, nil, nil, nil, SortDateDesc, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		assert.Equal(t, []int64{newestID, tiedEarlierID, tiedLaterID, oldestID}, ids(items))
	})

	t.Run("date_asc", func(t *testing.T) {
		items, total, err := r.SearchMessages(ctx, "needle", nil, nil, nil, nil, nil, SortDateAsc, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 4, total)
		assert.Equal(t, []int64{oldestID, tiedEarlierID, tiedLaterID, newestID}, ids(items))
	})

	// Paging a date sort across a tie must neither repeat nor skip a message.
	t.Run("paging across a tie is stable", func(t *testing.T) {
		var paged []int64
		for off := 0; off < 4; off += 2 {
			items, _, err := r.SearchMessages(ctx, "needle", nil, nil, nil, nil, nil, SortDateDesc, 2, off)
			require.NoError(t, err)
			paged = append(paged, ids(items)...)
		}
		assert.Equal(t, []int64{newestID, tiedEarlierID, tiedLaterID, oldestID}, paged)
	})

	// The sort is applied to the filtered set, not instead of the filters.
	t.Run("combines with a refinement", func(t *testing.T) {
		to := base.Add(90 * time.Minute)
		items, total, err := r.SearchMessages(ctx, "needle", nil, nil, &to, nil, nil, SortDateAsc, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 3, total)
		assert.Equal(t, []int64{oldestID, tiedEarlierID, tiedLaterID}, ids(items))
	})
}

func TestSearchMessagesAddressFilters(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	alice := makeMsg(1, "Address filter A")
	alice.FromAddr = `"Alice Andersson" <alice@example.com>`
	alice.ToAddr = "me@example.com"
	alice.BodyText = "shared needle text"
	_, err := r.InsertMessage(ctx, alice)
	require.NoError(t, err)

	bob := makeMsg(1, "Address filter B")
	bob.FromAddr = "bob@other.example"
	bob.ToAddr = "team@example.com"
	bob.CcAddr = "me@example.com"
	bob.BodyText = "shared needle text"
	_, err = r.InsertMessage(ctx, bob)
	require.NoError(t, err)

	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name      string
		fromAddr  *string
		toAddr    *string
		wantTotal int
		wantFrom  string
	}{
		{"no filter", nil, nil, 2, ""},
		{"from substring", strPtr("alice@"), nil, 1, `"Alice Andersson" <alice@example.com>`},
		{"from case-insensitive", strPtr("ALICE@EXAMPLE.COM"), nil, 1, `"Alice Andersson" <alice@example.com>`},
		{"from matches display name", strPtr("andersson"), nil, 1, `"Alice Andersson" <alice@example.com>`},
		{"from no match", strPtr("carol@"), nil, 0, ""},
		{"to matches To header", nil, strPtr("team@example.com"), 1, "bob@other.example"},
		{"to matches Cc header", nil, strPtr("me@example.com"), 2, ""},
		{"from and to are ANDed", strPtr("bob@"), strPtr("me@example.com"), 1, "bob@other.example"},
		{"from and to disagree", strPtr("alice@"), strPtr("team@"), 0, ""},
		{"LIKE wildcards are literal", nil, strPtr("t%m@"), 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := r.SearchMessages(ctx, "needle", nil, nil, nil, tc.fromAddr, tc.toAddr, SortRelevance, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.wantTotal, total)
			assert.Len(t, items, tc.wantTotal)
			if tc.wantFrom != "" {
				assert.Equal(t, tc.wantFrom, items[0].FromAddr)
			}
		})
	}
}

// The address filters claim to be case-insensitive, and the filter engine
// (internal/lda) and the demo backend both fold with a Unicode-aware ToLower.
// SQLite's built-in lower() does not, which is why the query uses
// unicode_lower() — without it every lowercase probe below finds nothing.
func TestSearchMessagesAddressFiltersNonASCII(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "Non-ASCII address filter")
	msg.FromAddr = `"Åsa Öberg" <asa@example.com>`
	msg.ToAddr = `"Émile Ünger" <emile@example.com>`
	msg.BodyText = "shared needle text"
	_, err := r.InsertMessage(ctx, msg)
	require.NoError(t, err)

	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name     string
		fromAddr *string
		toAddr   *string
	}{
		{"from as stored", strPtr("Åsa"), nil},
		{"from lowercased", strPtr("åsa"), nil},
		{"from uppercased", strPtr("ÅSA ÖBERG"), nil},
		{"from non-ASCII mid-word", strPtr("öberg"), nil},
		{"to lowercased", nil, strPtr("émile ünger")},
		{"to uppercased", nil, strPtr("ÉMILE")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, total, err := r.SearchMessages(ctx, "needle", nil, nil, nil, tc.fromAddr, tc.toAddr, SortRelevance, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, total)
		})
	}
}

func TestSearchMessagesSnippetLargeBody(t *testing.T) {
	// Regression: snippet generation must not re-tokenize the full body. A very
	// large body that previously made FTS5 snippet() pathologically slow should
	// now return quickly with a bounded, highlighted excerpt.
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	msg := makeMsg(1, "big")
	msg.BodyText = strings.Repeat("filler word here and there ", 200000) + " needle tail"
	_, err := r.InsertMessage(ctx, msg)
	require.NoError(t, err)

	start := time.Now()
	items, total, err := r.SearchMessages(ctx, "filler", nil, nil, nil, nil, nil, SortRelevance, 10, 0)
	require.NoError(t, err)
	require.Equal(t, 1, total)
	assert.Less(t, time.Since(start), 2*time.Second, "search over a large body must stay fast")

	snip := items[0].Snippet
	assert.Contains(t, snip, "**filler**")
	assert.Less(t, len(snip), 1024, "snippet must be a bounded excerpt, not the whole body")
}

// TestSearchMessagesSnippetLargeBody covers one enormous body; this covers the
// other axis, many matching rows, which is the one the date sorts make
// expensive. They cannot use idx_messages_date — the FTS5 MATCH drives the
// query — so SQLite materialises and sorts the whole match set before
// LIMIT/OFFSET (EXPLAIN QUERY PLAN: USE TEMP B-TREE FOR ORDER BY), where rank
// streams. The assertion is on the ordering being right across a paged result
// at this size, plus a deliberately loose time bound: a tight one would be a
// flaky test on shared CI, and the real guard is searchTimeout in production.
func TestSearchMessagesDateSortLargeMatchSet(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	const n = 2000
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := range n {
		msg := makeMsg(1, "bulk")
		msg.BodyText = "every one of these contains the needle"
		// Shuffled relative to id so neither insertion order nor rank can pass
		// for date order: id i carries the date of a scattered minute.
		msg.Date = base.Add(time.Duration((i*7919)%n) * time.Minute)
		_, err := r.InsertMessage(ctx, msg)
		require.NoError(t, err)
	}

	start := time.Now()
	first, total, err := r.SearchMessages(ctx, "needle", nil, nil, nil, nil, nil, SortDateDesc, 50, 0)
	require.NoError(t, err)
	require.Equal(t, n, total)
	require.Len(t, first, 50)
	assert.Less(t, time.Since(start), 5*time.Second, "a date sort over a large match set must still answer")

	// Descending within the page...
	for i := 1; i < len(first); i++ {
		assert.False(t, first[i].Date.After(first[i-1].Date), "page not descending at %d", i)
	}
	// ...and across the boundary into the next one, which is where an
	// unstable sort would repeat or skip a row.
	second, _, err := r.SearchMessages(ctx, "needle", nil, nil, nil, nil, nil, SortDateDesc, 50, 50)
	require.NoError(t, err)
	require.Len(t, second, 50)
	assert.False(t, second[0].Date.After(first[len(first)-1].Date), "boundary not descending")

	seen := make(map[int]bool, 100)
	for _, it := range append(append([]oas.MessagesSearchGetOKItemsItem{}, first...), second...) {
		assert.False(t, seen[it.ID], "id %d appeared on both pages", it.ID)
		seen[it.ID] = true
	}
}

func TestBuildSnippet(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		query string
		want  string
	}{
		{"match middle", "alpha beta gamma delta epsilon", "gamma", "alpha beta **gamma** delta epsilon"},
		{"case insensitive", "Hello World", "world", "Hello **World**"},
		{"no match returns prefix", "one two three", "zzz", "one two three"},
		{"empty body", "", "x", ""},
		{"multi-term phrase", "send the meeting report now", "meeting report", "send the **meeting** **report** now"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, buildSnippet(tc.body, tc.query))
		})
	}

	// A match far into a long body still produces a bounded, centered excerpt.
	long := strings.Repeat("pad ", 100) + "needle " + strings.Repeat("pad ", 100)
	got := buildSnippet(long, "needle")
	assert.Contains(t, got, "**needle**")
	assert.True(t, strings.HasPrefix(got, "…") && strings.HasSuffix(got, "…"), "expected ellipses on both sides, got %q", got)
}

func TestSanitizeFTSQuery(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", ``, `""`},
		{"quoted", `"quoted"`, `"""quoted"""`},
		{"operators", `AND OR NOT NEAR`, `"AND OR NOT NEAR"`},
		{"non-ascii", `café résumé`, `"café résumé"`},
		{"mixed", `it's a "test"`, `"it's a ""test"""`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeFTSQuery(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestSearchMessagesFTSSanitization(t *testing.T) {
	// Verify that ", non-ASCII, and FTS5 operators are treated as literals (no error).
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	cases := []string{
		`"quoted"`,
		`AND OR NOT NEAR`,
		`café résumé`,
	}
	for _, q := range cases {
		_, _, err := r.SearchMessages(ctx, q, nil, nil, nil, nil, nil, SortRelevance, 10, 0)
		assert.NoError(t, err, "SearchMessages(%q)", q)
	}
}

func TestBulkLimitExceeded(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewMessageRepository(db)

	ids := make([]int64, 1001)
	for i := range ids {
		ids[i] = int64(i + 1)
	}

	_, _, err := r.BulkDeleteMessages(ctx, ids)
	assert.Error(t, err, "expected error for BulkDeleteMessages with >1000 ids")

	_, err = r.BulkUpdateMessages(ctx, ids, nil, nil)
	assert.Error(t, err, "expected error for BulkUpdateMessages with >1000 ids")

	_, err = r.MoveMessages(ctx, ids, 1)
	assert.Error(t, err, "expected error for MoveMessages with >1000 ids")
}
