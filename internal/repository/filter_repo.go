package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	oas "github.com/mikaelstaldal/mymail/internal/api"
)

// FilterRepository provides filter CRUD operations backed by SQLite.
type FilterRepository struct {
	db *sql.DB
}

// NewFilterRepository creates a FilterRepository.
func NewFilterRepository(db *sql.DB) *FilterRepository {
	return &FilterRepository{db: db}
}

func scanFilter(scan func(dest ...any) error) (oas.Filter, error) {
	var f oas.Filter
	var action string
	var folderID sql.NullInt64
	var stop int
	if err := scan(&f.ID, &f.Position, &f.Name, &f.MatchFrom, &f.MatchTo, &f.MatchSubject, &action, &folderID, &stop); err != nil {
		return oas.Filter{}, err
	}
	f.Action = oas.FilterAction(action)
	if folderID.Valid {
		f.FolderID = oas.NewOptNilInt(int(folderID.Int64))
	} else {
		f.FolderID.SetToNull()
	}
	f.Stop = stop != 0
	return f, nil
}

const filterSelectSQL = `SELECT id, position, name, match_from, match_to, match_subject, action, folder_id, stop FROM filters`

// ListFilters returns all filters ordered by position ASC, id ASC.
func (r *FilterRepository) ListFilters(ctx context.Context) ([]oas.Filter, error) {
	rows, err := r.db.QueryContext(ctx, filterSelectSQL+` ORDER BY position ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	filters := []oas.Filter{}
	for rows.Next() {
		f, err := scanFilter(rows.Scan)
		if err != nil {
			return nil, err
		}
		filters = append(filters, f)
	}
	return filters, rows.Err()
}

// GetFilter returns a single filter by ID, or ErrNotFound.
func (r *FilterRepository) GetFilter(ctx context.Context, id int64) (oas.Filter, error) {
	row := r.db.QueryRowContext(ctx, filterSelectSQL+` WHERE id = ?`, id)
	f, err := scanFilter(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Filter{}, ErrNotFound
	}
	return f, err
}

func validateFilter(f oas.Filter) error {
	if strings.TrimSpace(f.MatchFrom) == "" && strings.TrimSpace(f.MatchTo) == "" && strings.TrimSpace(f.MatchSubject) == "" {
		return ErrInvalidFilter
	}
	switch f.Action {
	case oas.FilterActionMove, oas.FilterActionTrash, oas.FilterActionMarkRead, oas.FilterActionDrop:
	default:
		return ErrInvalidAction
	}
	if f.Action == oas.FilterActionMove {
		fid, ok := f.FolderID.Get()
		if !ok || (fid != 1 && fid != 4 && fid != 7 && fid < 100) {
			return ErrInvalidFolderTarget
		}
	}
	return nil
}

// folderIDArg returns the SQL value for the folder_id column.
func folderIDArg(folderID oas.OptNilInt) any {
	if fid, ok := folderID.Get(); ok {
		return fid
	}
	return nil
}

// CreateFilter inserts a new filter.
// At least one of match_from/match_to/match_subject must be non-empty after trimming.
// If action is "move", folder_id must be 1, 4, 7, or >= 100.
// pos controls the position: if not set, append semantics apply (COALESCE(MAX(position), -1) + 1).
func (r *FilterRepository) CreateFilter(ctx context.Context, f oas.Filter, pos oas.OptInt) (oas.Filter, error) {
	if err := validateFilter(f); err != nil {
		return oas.Filter{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.Filter{}, err
	}
	defer tx.Rollback()

	if p, ok := pos.Get(); ok {
		f.Position = p
	} else {
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), -1) + 1 FROM filters`,
		).Scan(&f.Position); err != nil {
			return oas.Filter{}, err
		}
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO filters (position, name, match_from, match_to, match_subject, action, folder_id, stop)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Position, f.Name, f.MatchFrom, f.MatchTo, f.MatchSubject,
		string(f.Action), folderIDArg(f.FolderID), boolToInt(f.Stop),
	)
	if err != nil {
		return oas.Filter{}, err
	}

	newID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		return oas.Filter{}, err
	}

	return r.GetFilter(ctx, newID)
}

// UpdateFilter fully replaces a filter (PUT semantics).
// Applies the same validation as CreateFilter.
// Returns ErrNotFound if no filter with the given id exists.
func (r *FilterRepository) UpdateFilter(ctx context.Context, id int64, f oas.Filter) (oas.Filter, error) {
	if err := validateFilter(f); err != nil {
		return oas.Filter{}, err
	}

	res, err := r.db.ExecContext(ctx,
		`UPDATE filters SET position = ?, name = ?, match_from = ?, match_to = ?, match_subject = ?,
		 action = ?, folder_id = ?, stop = ? WHERE id = ?`,
		f.Position, f.Name, f.MatchFrom, f.MatchTo, f.MatchSubject,
		string(f.Action), folderIDArg(f.FolderID), boolToInt(f.Stop), id,
	)
	if err != nil {
		return oas.Filter{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return oas.Filter{}, ErrNotFound
	}

	return r.GetFilter(ctx, id)
}

// DeleteFilter removes a filter by ID. Returns ErrNotFound if no such filter exists.
func (r *FilterRepository) DeleteFilter(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM filters WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderFilters assigns positions 0,1,2,… to the supplied filter IDs.
// The ids slice must contain every existing filter ID exactly once.
// Returns the number of rows updated, or a validation error.
func (r *FilterRepository) ReorderFilters(ctx context.Context, ids []int64) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM filters`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	existing := make(map[int64]bool)
	for rows.Next() {
		var eid int64
		if err := rows.Scan(&eid); err != nil {
			return 0, err
		}
		existing[eid] = true
	}
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
		if _, err := tx.ExecContext(ctx, `UPDATE filters SET position = ? WHERE id = ?`, pos, id); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}
