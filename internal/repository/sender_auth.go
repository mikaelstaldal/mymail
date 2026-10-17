package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mikaelstaldal/mymail/internal/senderauth"
)

// BackfillSenderAuthentication recomputes all stored verdicts using the current
// checker host. Run explicitly after securing delivery and changing that host.
// One raw message is held in memory at a time; the update is atomic.
func BackfillSenderAuthentication(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck
	// Acquire the write lock before reading, avoiding a deferred read-to-write
	// upgrade that could bypass busy_timeout when another writer is active.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), "ROLLBACK") //nolint:errcheck
	var host string
	if err := conn.QueryRowContext(ctx, `SELECT trusted_checker_host FROM spam_filter_settings WHERE id = 1`).Scan(&host); err != nil {
		return err
	}
	var last int64
	for {
		var id int64
		var raw []byte
		err := conn.QueryRowContext(ctx, `SELECT id, raw FROM messages WHERE id > ? ORDER BY id LIMIT 1`, last).Scan(&id, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		if _, err := conn.ExecContext(ctx, `UPDATE messages SET sender_authentication = ? WHERE id = ?`, senderauth.Encode(senderauth.Parse(raw, host)), id); err != nil {
			return err
		}
		last = id
	}
	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
