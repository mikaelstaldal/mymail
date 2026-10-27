package lda

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/repository"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockedSenderDelivery(t *testing.T) {
	for _, tc := range []struct {
		name, from, action string
		want               int
	}{
		{"spam disabled", "Alice <ALICE@Example.com>", "", 7},
		{"other mailbox", "Other <other@example.com>", "", 1},
		{"display name", "\"alice@example.com\" <other@example.com>", "", 1},
		{"malformed", "not an address", "", 1},
		{"multi", "alice@example.com, other@example.com", "", 1},
		{"repeated", "alice@example.com\r\nFrom: other@example.com", "", 1},
		{"obsolete repeated", "alice@example.com\r\nFrom : other@example.com", "", 1},
		{"move overrides", "alice@example.com", "move", 1},
		{"drop wins", "alice@example.com", "drop", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openLDATestDB(t)
			_, _, err := repository.NewBlockedSenderRepository(db).Add(t.Context(), "alice@example.com", nil)
			require.NoError(t, err)
			if tc.action != "" {
				createFilter(t, db, oas.Filter{MatchFrom: "alice@example.com", Action: oas.FilterAction(tc.action), FolderID: oas.NewOptNilInt(1)})
			}
			require.Equal(t, 0, runCore(db, attackerMessage(tc.from, "blocked@test")))
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM messages`).Scan(&count))
			if tc.want == 0 {
				assert.Zero(t, count)
				return
			}
			folder, _ := queryMessage(t, db, "blocked@test")
			assert.Equal(t, tc.want, folder)
			if tc.want == 7 {
				require.NoError(t, db.QueryRow(`SELECT count(*) FROM contacts`).Scan(&count))
				assert.Zero(t, count)
			}
		})
	}
}

func TestBlockedSenderMarkUndoDelivery(t *testing.T) {
	db := openLDATestDB(t)
	ctx := t.Context()
	messages := repository.NewMessageRepository(db)
	require.Equal(t, 0, runCore(db, attackerMessage("alice@example.com", "first@test")))
	var id int64
	require.NoError(t, db.QueryRow(`SELECT id FROM messages`).Scan(&id))
	_, address, err := messages.MarkJunkWithSender(ctx, id, true)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", address)
	require.Equal(t, 0, runCore(db, attackerMessage("ALICE@example.com", "second@test")))
	folder, _ := queryMessage(t, db, "second@test")
	assert.Equal(t, 7, folder)
	_, address, err = messages.MarkJunkWithSender(ctx, id, false)
	require.NoError(t, err)
	assert.Equal(t, "alice@example.com", address)
	require.Equal(t, 0, runCore(db, attackerMessage("alice@example.com", "third@test")))
	folder, _ = queryMessage(t, db, "third@test")
	assert.Equal(t, 1, folder)
	folder, _ = queryMessage(t, db, "second@test")
	assert.Equal(t, 7, folder, "undo does not move existing mail")
}

func TestBlockedSenderMarkUsesOriginalFrom(t *testing.T) {
	for _, from := range []string{"alice@example.com, other@example.com", "alice@example.com\r\nFrom: other@example.com", "=?UTF-8?Q?alice=40example.com?=", "not a mailbox"} {
		t.Run(from, func(t *testing.T) {
			db := openLDATestDB(t)
			require.Equal(t, 0, runCore(db, attackerMessage(from, "original@test")))
			var id int64
			require.NoError(t, db.QueryRow(`SELECT id FROM messages`).Scan(&id))
			_, address, err := repository.NewMessageRepository(db).MarkJunkWithSender(t.Context(), id, true)
			require.NoError(t, err)
			assert.Empty(t, address)
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM blocked_senders`).Scan(&count))
			assert.Zero(t, count)
		})
	}
}

func TestImportPreservesExplicitTargetForBlockedSender(t *testing.T) {
	db := openLDATestDB(t)
	_, _, err := repository.NewBlockedSenderRepository(db).Add(t.Context(), "alice@example.com", nil)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "mail.mbox")
	raw := fmt.Sprintf("From alice@example.com Mon Jan  1 12:00:00 2024\n%s", attackerMessage("alice@example.com", "import-blocked@test"))
	require.NoError(t, os.WriteFile(path, []byte(raw), 0600))
	imported, _, err := importMbox(path, 1, db)
	require.NoError(t, err)
	assert.Equal(t, 1, imported)
	folder, _ := queryMessage(t, db, "import-blocked@test")
	assert.Equal(t, 1, folder)
}
