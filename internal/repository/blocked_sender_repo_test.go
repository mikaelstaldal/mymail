package repository

import (
	oas "github.com/mikaelstaldal/mymail/internal/api"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockedSenders(t *testing.T) {
	db := openTestDB(t)
	r := NewBlockedSenderRepository(db)
	ctx := t.Context()
	source := int64(123)
	first, created, err := r.Add(ctx, "Alice@Example.COM", &source)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, "alice@example.com", first.Address)
	again, created, err := r.Add(ctx, "ALICE@example.com", nil)
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first, again)
	blocked, err := r.IsBlocked(ctx, "ALICE@EXAMPLE.COM")
	require.NoError(t, err)
	assert.True(t, blocked)
	blocked, err = r.IsBlocked(ctx, "other@example.com")
	require.NoError(t, err)
	assert.False(t, blocked)
	for _, address := range []string{"z@example.com", "a_b@example.com", "percent%here@example.com", "a@K.example"} {
		_, _, err := r.Add(ctx, address, nil)
		require.NoError(t, err)
	}
	for _, q := range []string{"_", "%", "ALICE"} {
		items, total, err := r.List(ctx, &q, 50, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		assert.Len(t, items, 1)
	}
	items, total, err := r.List(ctx, nil, 2, 1)
	require.NoError(t, err)
	assert.Equal(t, 5, total)
	assert.Len(t, items, 2)
	blocked, err = r.IsBlocked(ctx, "a@k.example")
	require.NoError(t, err)
	assert.False(t, blocked, "non-ASCII lookalikes stay distinct")
	require.NoError(t, r.Delete(ctx, int64(first.ID)))
	assert.ErrorIs(t, r.Delete(ctx, int64(first.ID)), ErrNotFound)
	require.NoError(t, r.DeleteByAddress(ctx, "Z@EXAMPLE.COM"))
	_, _, err = r.Add(ctx, "Name <bad@example.com>", nil)
	assert.ErrorIs(t, err, ErrInvalidAddress)
	_, err = db.Exec(`INSERT INTO identities(address,name,is_default,position,signature) VALUES ('me@example.com','Me',1,0,'')`)
	require.NoError(t, err)
	_, _, err = r.Add(ctx, "ME@example.com", nil)
	assert.ErrorIs(t, err, ErrInvalidAddress)
}

func TestOwnIdentityBlockExclusion(t *testing.T) {
	db := openTestDB(t)
	ctx := t.Context()
	blocked := NewBlockedSenderRepository(db)
	identities := NewIdentityRepository(db)
	identity, err := identities.CreateIdentity(ctx, oas.Identity{Address: "me@exämple.se"})
	require.NoError(t, err)
	_, _, err = blocked.Add(ctx, "ME@EXÄMPLE.SE", nil)
	assert.ErrorIs(t, err, ErrInvalidAddress)
	_, err = db.Exec(`INSERT INTO messages(id,folder_id,from_addr,date,created_at,updated_at) VALUES (99,2,'Me <ME@EXÄMPLE.SE>','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, address, err := NewMessageRepository(db).MarkJunkWithSender(ctx, 99, true)
	require.NoError(t, err)
	assert.Empty(t, address)
	_, _, err = blocked.Add(ctx, "SECOND@EXÄMPLE.SE", nil)
	require.NoError(t, err)
	second, err := identities.CreateIdentity(ctx, oas.Identity{Address: "second@exämple.se"})
	require.NoError(t, err)
	found, err := blocked.IsBlocked(ctx, "SECOND@EXÄMPLE.SE")
	require.NoError(t, err)
	assert.False(t, found)
	_, _, err = blocked.Add(ctx, "THIRD@EXÄMPLE.SE", nil)
	require.NoError(t, err)
	second.Address = "third@exämple.se"
	_, err = identities.UpdateIdentity(ctx, int64(second.ID), second)
	require.NoError(t, err)
	found, err = blocked.IsBlocked(ctx, "THIRD@EXÄMPLE.SE")
	require.NoError(t, err)
	assert.False(t, found)
	assert.NotZero(t, identity.ID)
}
