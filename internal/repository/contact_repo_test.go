package repository

import (
	"context"
	"testing"

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
