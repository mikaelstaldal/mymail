package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	sqlite "modernc.org/sqlite"
	"golang.org/x/text/unicode/norm"
)

// FolderRepository provides folder CRUD operations backed by SQLite.
type FolderRepository struct {
	db *sql.DB
}

// NewFolderRepository creates a FolderRepository.
func NewFolderRepository(db *sql.DB) *FolderRepository {
	return &FolderRepository{db: db}
}

var nonAlphanumRun = regexp.MustCompile(`[^a-z0-9]+`)

// toSlug converts a display name to a URL-safe slug.
func toSlug(name string) string {
	s := norm.NFKD.String(name)
	s = strings.ToLower(s)
	s = nonAlphanumRun.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "folder"
	}
	return s
}

// uniqueSlug returns a slug derived from base that does not collide with any existing slug.
// It appends -2, -3, … until the slug is free. Must be called within a transaction.
func (r *FolderRepository) uniqueSlug(ctx context.Context, tx *sql.Tx, base string) (string, error) {
	slug := base
	for i := 2; ; i++ {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE slug = ?`, slug).Scan(&count); err != nil {
			return "", err
		}
		if count == 0 {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", base, i)
	}
}

// isConstraintErr reports whether err is a SQLite SQLITE_CONSTRAINT violation (code 19).
func isConstraintErr(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == 19
}

// scanFolder scans a folder row using the provided Scan func.
// Expected columns: id, name, slug, position, created_at (TEXT RFC3339), unread_count.
func scanFolder(scan func(dest ...any) error) (oas.Folder, error) {
	var f oas.Folder
	var createdAt string
	if err := scan(&f.ID, &f.Name, &f.Slug, &f.Position, &createdAt, &f.UnreadCount); err != nil {
		return oas.Folder{}, err
	}
	t, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return oas.Folder{}, fmt.Errorf("parse created_at %q: %w", createdAt, err)
	}
	f.CreatedAt = t
	return f, nil
}

const folderSelectSQL = `
SELECT f.id, f.name, f.slug, f.position, f.created_at,
       (SELECT COUNT(*) FROM messages WHERE folder_id = f.id AND read = 0)
FROM folders f`

// ListFolders returns all folders ordered by position ASC, id ASC, each with its unread count.
func (r *FolderRepository) ListFolders(ctx context.Context) ([]oas.Folder, error) {
	rows, err := r.db.QueryContext(ctx, folderSelectSQL+` ORDER BY f.position ASC, f.id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var folders []oas.Folder
	for rows.Next() {
		f, err := scanFolder(rows.Scan)
		if err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if folders == nil {
		folders = []oas.Folder{}
	}
	return folders, nil
}

// GetFolderByID returns a single folder by ID, or ErrNotFound.
func (r *FolderRepository) GetFolderByID(ctx context.Context, id int64) (oas.Folder, error) {
	row := r.db.QueryRowContext(ctx, folderSelectSQL+` WHERE f.id = ?`, id)
	f, err := scanFolder(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Folder{}, ErrNotFound
	}
	return f, err
}

// CreateFolder inserts a new user-defined folder.
// If position is nil, append semantics are used (COALESCE(MAX(position),-1)+1).
// Returns ErrConflict if the name already exists.
func (r *FolderRepository) CreateFolder(ctx context.Context, name string, position *int) (oas.Folder, error) {
	baseSlug := toSlug(name)

	for range 5 {
		tx, err := r.db.BeginTx(ctx, nil)
		if err != nil {
			return oas.Folder{}, err
		}

		// Check for name collision inside the transaction.
		var nameCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE name = ?`, name).Scan(&nameCount); err != nil {
			tx.Rollback()
			return oas.Folder{}, err
		}
		if nameCount > 0 {
			tx.Rollback()
			return oas.Folder{}, ErrConflict
		}

		slug, err := r.uniqueSlug(ctx, tx, baseSlug)
		if err != nil {
			tx.Rollback()
			return oas.Folder{}, err
		}

		var id int64
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(id), 99) + 1 FROM folders WHERE id >= 100`,
		).Scan(&id); err != nil {
			tx.Rollback()
			return oas.Folder{}, err
		}

		pos := 0
		if position != nil {
			pos = *position
		} else {
			if err := tx.QueryRowContext(ctx,
				`SELECT COALESCE(MAX(position), -1) + 1 FROM folders`,
			).Scan(&pos); err != nil {
				tx.Rollback()
				return oas.Folder{}, err
			}
		}

		_, err = tx.ExecContext(ctx,
			`INSERT INTO folders (id, name, slug, position) VALUES (?, ?, ?, ?)`,
			id, name, slug, pos,
		)
		if err != nil {
			tx.Rollback()
			if isConstraintErr(err) {
				continue // ID collision race — retry with a new MAX(id)
			}
			return oas.Folder{}, err
		}

		if err := tx.Commit(); err != nil {
			if isConstraintErr(err) {
				continue
			}
			return oas.Folder{}, err
		}

		return r.GetFolderByID(ctx, id)
	}
	return oas.Folder{}, fmt.Errorf("create folder: too many constraint retries")
}

// UpdateFolder applies PATCH-semantics updates (only non-nil fields are changed).
// Returns ErrNotFound if the folder does not exist, ErrConflict if the new name is taken.
// Slug is never updated.
func (r *FolderRepository) UpdateFolder(ctx context.Context, id int64, name *string, position *int) (oas.Folder, error) {
	if name == nil && position == nil {
		return r.GetFolderByID(ctx, id)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.Folder{}, err
	}
	defer tx.Rollback()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM folders WHERE id = ?`, id).Scan(&exists); err != nil {
		return oas.Folder{}, err
	}
	if exists == 0 {
		return oas.Folder{}, ErrNotFound
	}

	if name != nil {
		// Reject rename if another folder already has this name.
		var conflictID int64
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM folders WHERE name = ? AND id != ?`, *name, id,
		).Scan(&conflictID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return oas.Folder{}, err
		}
		if err == nil {
			return oas.Folder{}, ErrConflict
		}

		if _, err := tx.ExecContext(ctx, `UPDATE folders SET name = ? WHERE id = ?`, *name, id); err != nil {
			return oas.Folder{}, err
		}
	}

	if position != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET position = ? WHERE id = ?`, *position, id); err != nil {
			return oas.Folder{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return oas.Folder{}, err
	}

	return r.GetFolderByID(ctx, id)
}

// DeleteFolder deletes a user-created folder, moving its messages to Trash first.
// Returns ErrConflict for built-in folders (id < 100), ErrNotFound if not found.
func (r *FolderRepository) DeleteFolder(ctx context.Context, id int64) error {
	if id < 100 {
		return ErrConflict
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `UPDATE messages SET folder_id = 4 WHERE folder_id = ?`, id); err != nil {
		return err
	}

	res, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	return tx.Commit()
}

// ReorderFolders assigns positions 0,1,2,… to the supplied folder IDs in a single transaction.
// The ids slice must contain every existing folder ID exactly once.
// Returns the number of rows updated, or a validation error (ErrDuplicateID, ErrUnknownID,
// ErrIncompleteReorder).
func (r *FolderRepository) ReorderFolders(ctx context.Context, ids []int64) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM folders`)
	if err != nil {
		return 0, err
	}
	existing := make(map[int64]bool)
	for rows.Next() {
		var eid int64
		if err := rows.Scan(&eid); err != nil {
			rows.Close()
			return 0, err
		}
		existing[eid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	seen := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return 0, ErrDuplicateID
		}
		seen[id] = true
		if !existing[id] {
			return 0, ErrUnknownID
		}
	}
	if len(seen) != len(existing) {
		return 0, ErrIncompleteReorder
	}

	for pos, id := range ids {
		if _, err := tx.ExecContext(ctx, `UPDATE folders SET position = ? WHERE id = ?`, pos, id); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// DeleteAllMessagesInFolder permanently deletes messages if the folder is Trash (4) or Junk (7),
// otherwise moves them to Trash. Returns (movedToTrash, permanentlyDeleted, error).
func (r *FolderRepository) DeleteAllMessagesInFolder(ctx context.Context, folderID int64) (movedToTrash, permanentlyDeleted int, err error) {
	if folderID == 4 || folderID == 7 { // Trash, Junk — permanent delete
		res, err := r.db.ExecContext(ctx, `DELETE FROM messages WHERE folder_id = ?`, folderID)
		if err != nil {
			return 0, 0, err
		}
		n, _ := res.RowsAffected()
		return 0, int(n), nil
	}
	res, err := r.db.ExecContext(ctx, `UPDATE messages SET folder_id = 4 WHERE folder_id = ?`, folderID)
	if err != nil {
		return 0, 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), 0, nil
}

// MarkAllRead marks all unread messages in a folder as read and returns the number of rows changed.
func (r *FolderRepository) MarkAllRead(ctx context.Context, folderID int64) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE messages SET read = 1 WHERE folder_id = ? AND read = 0`, folderID,
	)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	return int(n), err
}
