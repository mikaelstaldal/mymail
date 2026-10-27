package handler_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBlockedSendersAPI(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	res := do(srv, "POST", "/blocked-senders", `{"address":"ALICE@example.com"}`)
	require.Equal(t, 201, res.StatusCode)
	var first map[string]any
	decodeJSON(t, res, &first)
	assert.Equal(t, "alice@example.com", first["address"])
	assert.Nil(t, first["source_message_id"])
	res = do(srv, "POST", "/blocked-senders", `{"address":"alice@example.com"}`)
	require.Equal(t, 200, res.StatusCode)
	var again map[string]any
	decodeJSON(t, res, &again)
	assert.Equal(t, first, again)
	res = do(srv, "GET", "/blocked-senders?q=ALICE&limit=1", "")
	require.Equal(t, 200, res.StatusCode)
	var page struct {
		Total int
		Items []map[string]any
	}
	decodeJSON(t, res, &page)
	assert.Equal(t, 1, page.Total)
	assert.Len(t, page.Items, 1)
	for _, address := range []string{"bad", "example.com", "Name <a@example.com>", strings.Repeat("x", 255) + "@example.com"} {
		res = do(srv, "POST", "/blocked-senders", fmt.Sprintf(`{"address":%q}`, address))
		assert.Equal(t, 400, res.StatusCode)
		res.Body.Close()
	}
	_, err := db.Exec(`INSERT INTO identities(address,name,is_default,position,signature) VALUES ('me@example.com','Me',1,0,'')`)
	require.NoError(t, err)
	res = do(srv, "POST", "/blocked-senders", `{"address":"ME@example.com"}`)
	assert.Equal(t, 400, res.StatusCode)
	res.Body.Close()
	res = do(srv, "GET", "/blocked-senders?q="+strings.Repeat("x", 501), "")
	assert.Equal(t, 400, res.StatusCode)
	res.Body.Close()
	path := fmt.Sprintf("/blocked-senders/%.0f", first["id"])
	res = do(srv, "DELETE", path, "")
	assert.Equal(t, 204, res.StatusCode)
	res.Body.Close()
	res = do(srv, "DELETE", path, "")
	assert.Equal(t, 404, res.StatusCode)
	res.Body.Close()
}

func TestJunkBlockedSenderAPI(t *testing.T) {
	for _, tc := range []struct {
		name, from string
		folder     int
		block      bool
	}{
		{"normal", "Alice <ALICE@example.com>", 1, true},
		{"sent", "alice@example.com", 2, true},
		{"own sent", "me@example.com", 2, false},
		{"multi", "alice@example.com, other@example.com", 1, false},
		{"malformed", "not a mailbox", 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openTestDB(t)
			srv := newServer(t, db)
			_, err := db.Exec(`INSERT INTO identities(address,name,is_default,position,signature) VALUES ('me@example.com','Me',1,0,'')`)
			require.NoError(t, err)
			_, err = db.Exec(`INSERT INTO messages(id,folder_id,from_addr,date,created_at,updated_at) VALUES (1,?,?,'2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`, tc.folder, tc.from)
			require.NoError(t, err)
			res := do(srv, "POST", "/messages/1/mark-junk", "")
			require.Equal(t, 200, res.StatusCode)
			var body map[string]any
			decodeJSON(t, res, &body)
			assert.Equal(t, float64(7), body["folder_id"])
			if tc.block {
				assert.Equal(t, "alice@example.com", body["blocked_sender"])
			} else {
				assert.NotContains(t, body, "blocked_sender")
			}
			res = do(srv, "POST", "/messages/1/mark-not-junk", "")
			require.Equal(t, 200, res.StatusCode)
			decodeJSON(t, res, &body)
			if tc.block {
				assert.Equal(t, "alice@example.com", body["blocked_sender"])
			}
			var count int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM blocked_senders`).Scan(&count))
			assert.Zero(t, count)
		})
	}
}

func TestJunkBlockFailureRollsBackMove(t *testing.T) {
	db := openTestDB(t)
	srv := newServer(t, db)
	_, err := db.Exec(`INSERT INTO messages(id,folder_id,from_addr,date,created_at,updated_at) VALUES (1,1,'alice@example.com','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TRIGGER fail_block BEFORE INSERT ON blocked_senders BEGIN SELECT RAISE(ABORT,'test failure'); END`)
	require.NoError(t, err)
	res := do(srv, "POST", "/messages/1/mark-junk", "")
	assert.Equal(t, 500, res.StatusCode)
	res.Body.Close()
	var folder, read int
	require.NoError(t, db.QueryRow(`SELECT folder_id,read FROM messages WHERE id=1`).Scan(&folder, &read))
	assert.Equal(t, 1, folder)
	assert.Zero(t, read)
}
