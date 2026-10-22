package repository

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mikaelstaldal/go-server-common/sqlite"
	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The contact filter is documented as case-insensitive, and the demo backend
// implements it with toLowerCase(). SQLite's built-in lower() folds ASCII only,
// which is why the query uses unicode_lower() — without it none of the
// lowercase probes below find anything.
func TestListContactsNonASCII(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewContactRepository(db)

	require.NoError(t, r.UpsertContact(ctx, "asa@example.com", "Åsa Öberg"))
	require.NoError(t, r.UpsertContact(ctx, "EMILE@example.com", "Émile Ünger"))

	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name string
		q    *string
		want int
	}{
		{"no filter", nil, 2},
		{"name as stored", strPtr("Åsa"), 1},
		{"name lowercased", strPtr("åsa"), 1},
		{"name uppercased", strPtr("ÅSA ÖBERG"), 1},
		{"name non-ASCII mid-word", strPtr("öberg"), 1},
		{"other name lowercased", strPtr("émile ünger"), 1},
		{"address folds too", strPtr("emile@example.com"), 1},
		{"no match", strPtr("örjan"), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := r.ListContacts(ctx, tc.q, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, total)
			assert.Len(t, items, tc.want)
		})
	}
}

// Two halves, and the first is the more important one: a contacts tie is
// **unreachable** through the repository, because all three write paths casefold
// the address before storing it. Asserted here so that a change to the folding
// is a failing test rather than a quiet widening of what the listing has to
// order.
//
// The second half then constructs the tie the API refuses to, by inserting
// straight into the table, and pins what the `id ASC` term decides. It is
// deliberately reaching past the repository — that is the only way to reach this
// state, and the reason the term is defence-in-depth rather than a bug fix.
// Like TestListMessagesPagingIsStableAcrossTies it passes with and without the
// clause (SQLite's sorter preserves input order; measured to 100k tied rows), so
// it pins the declared order, not a live defect.
func TestListContactsTieIsDecidedByID(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewContactRepository(db)

	require.NoError(t, r.UpsertContact(ctx, "Bob@example.com", "Bob"))
	require.NoError(t, r.UpsertContact(ctx, "bob@example.com", "Bob"))
	_, total, err := r.ListContacts(ctx, nil, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 1, total,
		"addresses are casefolded on write, so a case-differing pair collapses to one row")

	// Past the repository on purpose: nothing else can produce two rows that tie.
	for _, addr := range []string{"Tie@example.com", "tIe@example.com"} {
		_, err := db.ExecContext(ctx,
			`INSERT INTO contacts(address,name,created_at,updated_at) VALUES(?,'Zed',?,?)`,
			addr, "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
		require.NoError(t, err)
	}

	items, _, err := r.ListContacts(ctx, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	tied := items[1:] // "Zed" sorts after "Bob"
	assert.Less(t, tied[0].ID, tied[1].ID, "a tie must resolve to ascending id")

	// And paging across the tie returns each of them exactly once.
	first, _, err := r.ListContacts(ctx, nil, 1, 1)
	require.NoError(t, err)
	second, _, err := r.ListContacts(ctx, nil, 1, 2)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Len(t, second, 1)
	assert.NotEqual(t, first[0].ID, second[0].ID, "the same contact came back on both pages")
}

// The filter uses instr(), not LIKE, so % and _ are ordinary characters — the
// same rule as SearchMessages. Under LIKE every probe here but the last would
// have matched.
func TestListContactsWildcardsAreLiteral(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewContactRepository(db)

	require.NoError(t, r.UpsertContact(ctx, "alice@example.com", "Alice Andersson"))
	require.NoError(t, r.UpsertContact(ctx, "bob@example.com", "50% Off Deals"))

	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name string
		q    *string
		want int
	}{
		{"percent matches literally", strPtr("50%"), 1},
		{"percent is not a wildcard", strPtr("a%n"), 0},
		{"underscore is not a wildcard", strPtr("alic_"), 0},
		{"blank filter still matches everything", strPtr(""), 2},
		{"plain substring still works", strPtr("anders"), 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, total, err := r.ListContacts(ctx, tc.q, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, tc.want, total)
			assert.Len(t, items, tc.want)
		})
	}
}

func TestExplicitContactLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewContactRepository(db)
	require.NoError(t, r.UpsertContact(ctx, "ALICE@example.com", "Collected"))
	items, _, err := r.ListContacts(ctx, nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].Explicit)
	original := items[0]
	promoted, err := r.CreateContact(ctx, oas.Contact{Address: "ALICE@example.com"})
	require.NoError(t, err)
	assert.Equal(t, original.ID, promoted.ID)
	assert.Equal(t, "Collected", promoted.Name)
	assert.True(t, promoted.Explicit)
	_, err = r.CreateContact(ctx, oas.Contact{Address: original.Address})
	require.ErrorIs(t, err, ErrConflict)
	require.NoError(t, r.UpsertContact(ctx, original.Address, "Later receipt"))
	saved, err := r.GetContact(ctx, int64(original.ID))
	require.NoError(t, err)
	assert.True(t, saved.Explicit)
	require.NoError(t, r.DeleteContact(ctx, int64(original.ID)))
	require.NoError(t, r.UpsertContact(ctx, original.Address, ""))
	items, _, err = r.ListContacts(ctx, nil, 10, 0)
	require.NoError(t, err)
	assert.False(t, items[0].Explicit)
	edited, err := r.UpdateContact(ctx, int64(items[0].ID), items[0])
	require.NoError(t, err)
	assert.True(t, edited.Explicit, "no-op edit is explicit user action")
	require.NoError(t, r.UpsertContact(ctx, original.Address, "Filled name"))
	saved, err = r.GetContact(ctx, int64(edited.ID))
	require.NoError(t, err)
	assert.True(t, saved.Explicit)
	assert.Empty(t, saved.Name, "automatic collection must preserve an explicitly empty name")
	fresh, err := r.CreateContact(ctx, oas.Contact{Address: "new@example.com"})
	require.NoError(t, err)
	assert.True(t, fresh.Explicit)
	require.NoError(t, r.UpsertContact(ctx, fresh.Address, "Spoofed name"))
	saved, err = r.GetContact(ctx, int64(fresh.ID))
	require.NoError(t, err)
	assert.Empty(t, saved.Name, "new explicit contacts are protected too")
	assert.True(t, saved.Explicit)

	require.NoError(t, r.UpsertContact(ctx, "auto@example.com", ""))
	require.NoError(t, r.UpsertContact(ctx, "auto@example.com", "Collected name"))
	q := "Collected name"
	collected, _, err := r.ListContacts(ctx, &q, 10, 0)
	require.NoError(t, err)
	require.Len(t, collected, 1)
	assert.Equal(t, "Collected name", collected[0].Name)
	assert.False(t, collected[0].Explicit)
	require.NoError(t, r.UpsertContact(ctx, "promote@example.com", "Old"))
	named, err := r.CreateContact(ctx, oas.Contact{Address: "PROMOTE@example.com", Name: "New"})
	require.NoError(t, err)
	assert.Equal(t, "New", named.Name)
}

func TestContactMigrationDoesNotBackfillTrust(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "old.sqlite"), 0, migrations[:10])
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })
	_, err = db.Exec(`
 INSERT INTO contacts(address,name,created_at,updated_at) VALUES('old@example.com','Old','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z');
 PRAGMA user_version = 10`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	items, _, err := NewContactRepository(db).ListContacts(context.Background(), nil, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.False(t, items[0].Explicit)
}

// Remote senders must not crowd a saved exact address out of a limited search.
func TestContactAutocompleteRanking(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	r := NewContactRepository(db)
	_, err := r.CreateContact(ctx, oas.Contact{Address: "bob@corp.com", Name: "Z Bob"})
	require.NoError(t, err)
	for _, address := range []string{"bob@corp.com.evil.example", "other@evil.example"} {
		require.NoError(t, r.UpsertContact(ctx, address, "bob@corp.com"))
	}
	q := "BOB@CORP.COM"
	items, total, err := r.ListContacts(ctx, &q, 1, 0)
	require.NoError(t, err)
	assert.Equal(t, 3, total)
	require.Len(t, items, 1)
	assert.Equal(t, "bob@corp.com", items[0].Address)
	q = "bob"
	items, _, err = r.ListContacts(ctx, &q, 1, 0)
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.True(t, items[0].Explicit)
	_, err = db.Exec(`INSERT INTO sent_recipients(address) VALUES ('other@evil.example')`)
	require.NoError(t, err)
	items, _, err = r.ListContacts(ctx, &q, 10, 0)
	require.NoError(t, err)
	require.Len(t, items, 3)
	assert.Equal(t, "other@evil.example", items[1].Address)
}
