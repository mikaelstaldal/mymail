package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/senderauth"
)

// BackfillSenderAuthentication recomputes all stored verdicts using the current
// checker host. Run explicitly after securing delivery and changing that host.
// Optional folders are exact names or slugs; omitted means all folders. Prefix
// with name: or slug: to disambiguate. Unknown
// or ambiguous selectors fail before any message is updated. Unselected raw
// blobs are never loaded.
// One raw message is held in memory at a time; the update is atomic.
func BackfillSenderAuthentication(ctx context.Context, db *sql.DB, folders ...string) error {
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
	// Resolve the complete selection before processing any message. A name can
	// collide with another folder's slug, so reject that ambiguity explicitly.
	folderIDs := []int64{}
	seen := map[int64]bool{}
	for _, folder := range folders {
		query := `SELECT COUNT(*), MIN(id) FROM folders WHERE name = ? OR slug = ?`
		value := folder
		args := []any{value, value}
		if name, ok := strings.CutPrefix(folder, "name:"); ok {
			value = name
			query = `SELECT COUNT(*), MIN(id) FROM folders WHERE name = ?`
			args = []any{value}
		} else if slug, ok := strings.CutPrefix(folder, "slug:"); ok {
			value = slug
			query = `SELECT COUNT(*), MIN(id) FROM folders WHERE slug = ?`
			args = []any{value}
		}
		if value == "" {
			return fmt.Errorf("backfill folder must not be empty")
		}
		var count int
		var id sql.NullInt64
		if err := conn.QueryRowContext(ctx, query, args...).Scan(&count, &id); err != nil {
			return err
		}
		if count == 0 {
			return fmt.Errorf("backfill folder %q does not exist", folder)
		}
		if count != 1 {
			return fmt.Errorf("backfill folder %q is ambiguous between a name and a slug; use a name: or slug: prefix", folder)
		}
		if !seen[id.Int64] {
			folderIDs = append(folderIDs, id.Int64)
			seen[id.Int64] = true
		}
	}
	if len(folders) == 0 {
		folderIDs = append(folderIDs, 0)
	} // all-folder pass
	for _, folderID := range folderIDs {
		var last int64
		for {
			var id int64
			var raw []byte
			query := `SELECT id, raw FROM messages WHERE id > ? ORDER BY id LIMIT 1`
			args := []any{last}
			if len(folders) > 0 {
				// The folder index avoids walking a large excluded archive for every row.
				query = `SELECT id, raw FROM messages WHERE folder_id = ? AND id > ? ORDER BY id LIMIT 1`
				args = []any{folderID, last}
			}
			err := conn.QueryRowContext(ctx, query, args...).Scan(&id, &raw)
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
	}

	_, err = conn.ExecContext(ctx, "COMMIT")
	return err
}
