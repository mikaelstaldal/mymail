package repository

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mikaelstaldal/go-server-common/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSentRecipientsDurable(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewMessageRepository(db)
	// Receipt creates a contact, but never correspondence.
	require.NoError(t, NewContactRepository(db).UpsertContact(ctx, "a@example.org", "Alice"))
	c, err := r.WarningContext(ctx, "a@example.org")
	require.NoError(t, err)
	assert.False(t, c.SentTo)
	m := makeMsg(2, "sent")
	m.ToAddr = `"A, Alice" <A@EXAMPLE.ORG>`
	m.CcAddr = "B@EXAMPLE.ORG"
	m.BccAddr = "Straße@example.org"
	id, err := r.InsertMessage(ctx, m)
	require.NoError(t, err)
	for _, a := range []string{"a@example.org", "b@example.org", "STRASSE@example.org"} {
		c, err = r.WarningContext(ctx, a)
		require.NoError(t, err)
		assert.True(t, c.SentTo, a)
	}
	_, err = db.Exec(`DELETE FROM messages WHERE id = ?`, id)
	require.NoError(t, err)
	c, err = r.WarningContext(ctx, "a@example.org")
	require.NoError(t, err)
	assert.True(t, c.SentTo, "deleting Sent mail must not erase history")
	// Folder moves do not send mail.
	m = makeMsg(1, "received")
	m.ToAddr = "not-sent@example.org"
	id, err = r.InsertMessage(ctx, m)
	require.NoError(t, err)
	_, err = r.UpdateMessage(ctx, id, map[string]any{"folder_id": int64(2)})
	require.NoError(t, err)
	c, err = r.WarningContext(ctx, "not-sent@example.org")
	require.NoError(t, err)
	assert.False(t, c.SentTo)
	// All scheduled success paths update 5 → 2, including send-now after claim.
	m = makeMsg(5, "scheduled")
	m.ToAddr = "scheduled@example.org"
	id, err = r.InsertMessage(ctx, m)
	require.NoError(t, err)
	c, err = r.WarningContext(ctx, m.ToAddr)
	require.NoError(t, err)
	assert.False(t, c.SentTo)
	require.NoError(t, NewDraftRepository(db).MarkSent(ctx, id, "scheduled-id"))
	c, err = r.WarningContext(ctx, m.ToAddr)
	require.NoError(t, err)
	assert.True(t, c.SentTo)
	// Recording runs inside the sending transaction, so rollback removes it too.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`INSERT INTO messages(folder_id,to_addr,date,created_at,updated_at) VALUES(2,'rollback@example.org','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, tx.Rollback())
	c, err = r.WarningContext(ctx, "rollback@example.org")
	require.NoError(t, err)
	assert.False(t, c.SentTo)
}

func TestSentRecipientsMigrationBackfill(t *testing.T) {
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "old.sqlite"), 0, nil)
	require.NoError(t, err)
	defer db.Close()
	require.NoError(t, sqlite.MigrateStrict(context.Background(), db, migrations[:9]))
	require.NoError(t, SeedBuiltinFolders(db))
	_, err = db.Exec(`INSERT INTO messages(folder_id,to_addr,cc_addr,bcc_addr,date,created_at,updated_at) VALUES
 (2,'"Last, First" <A@example.org>','b@example.org','C@example.org','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z'),
 (1,'received@example.org','','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	require.NoError(t, InitSchema(db))
	require.NoError(t, InitSchema(db))
	for _, a := range []string{"a@example.org", "b@example.org", "c@example.org"} {
		c, err := NewMessageRepository(db).WarningContext(context.Background(), a)
		require.NoError(t, err)
		assert.True(t, c.SentTo)
	}
	c, err := NewMessageRepository(db).WarningContext(context.Background(), "received@example.org")
	require.NoError(t, err)
	assert.False(t, c.SentTo)
}

func TestWarningDomainCandidates(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	r := NewMessageRepository(db)
	m := makeMsg(2, "sent")
	m.ToAddr = "a@example.com,b@mail.paypal.com,c@irrelevant.org"
	_, err := r.InsertMessage(ctx, m)
	require.NoError(t, err)
	c, err := r.WarningContext(ctx, "user@exarnple.com", "pаypal.com")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"example.com", "paypal.com"}, c.KnownDomains)
	assert.False(t, c.SentTo)
}

func TestDetailWarningSnapshot(t *testing.T) {
	db := openTestDB(t)
	r := NewMessageRepository(db)
	ctx := context.Background()
	snapshot, release, err := r.DetailReadSnapshot(ctx)
	require.NoError(t, err)
	defer release()
	before, err := r.WarningContext(snapshot, "a@example.org")
	require.NoError(t, err)
	assert.False(t, before.SentTo)
	m := makeMsg(2, "sent concurrently")
	m.ToAddr = "a@example.org"
	_, err = r.InsertMessage(ctx, m)
	require.NoError(t, err)
	same, err := r.WarningContext(snapshot, "a@example.org")
	require.NoError(t, err)
	assert.False(t, same.SentTo, "metadata assembled in one detail read uses one snapshot")
	current, err := r.WarningContext(ctx, "a@example.org")
	require.NoError(t, err)
	assert.True(t, current.SentTo)
	release()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	borrowed, releaseBorrowed, err := r.DetailReadSnapshot(WithReadTransaction(ctx, tx))
	require.NoError(t, err)
	releaseBorrowed()
	var one int
	require.NoError(t, queryer(borrowed, db).QueryRowContext(ctx, "SELECT 1").Scan(&one))
	assert.Equal(t, 1, one, "bearer-owned transaction remains open")
}
