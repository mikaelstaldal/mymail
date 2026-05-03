package repository

import (
	"context"
	"database/sql"
	"errors"
	"net/mail"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"golang.org/x/text/cases"
)

// IdentityRepository provides identity CRUD operations backed by SQLite.
type IdentityRepository struct {
	db *sql.DB
}

// NewIdentityRepository creates an IdentityRepository.
func NewIdentityRepository(db *sql.DB) *IdentityRepository {
	return &IdentityRepository{db: db}
}

// parseAndFoldAddress validates that s is a bare RFC 5322 addr-spec (no display name),
// then returns the Unicode-simple-casefolded address.
func parseAndFoldAddress(s string) (string, error) {
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" {
		return "", ErrInvalidAddress
	}
	return cases.Fold().String(addr.Address), nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanIdentity(scan func(dest ...any) error) (oas.Identity, error) {
	var identity oas.Identity
	var isDefault int
	if err := scan(&identity.ID, &identity.Name, &identity.Address, &isDefault, &identity.Position, &identity.Signature); err != nil {
		return oas.Identity{}, err
	}
	identity.IsDefault = isDefault != 0
	return identity, nil
}

const identitySelectSQL = `SELECT id, name, address, is_default, position, signature FROM identities`

// ListIdentities returns all identities ordered by position ASC, id ASC.
func (r *IdentityRepository) ListIdentities(ctx context.Context) ([]oas.Identity, error) {
	rows, err := r.db.QueryContext(ctx, identitySelectSQL+` ORDER BY position ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	identities := []oas.Identity{}
	for rows.Next() {
		identity, err := scanIdentity(rows.Scan)
		if err != nil {
			return nil, err
		}
		identities = append(identities, identity)
	}
	return identities, rows.Err()
}

// GetIdentity returns a single identity by ID, or ErrNotFound.
func (r *IdentityRepository) GetIdentity(ctx context.Context, id int64) (oas.Identity, error) {
	row := r.db.QueryRowContext(ctx, identitySelectSQL+` WHERE id = ?`, id)
	identity, err := scanIdentity(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Identity{}, ErrNotFound
	}
	return identity, err
}

// GetDefaultIdentity returns the identity with is_default=1, or ErrNotFound if none.
func (r *IdentityRepository) GetDefaultIdentity(ctx context.Context) (oas.Identity, error) {
	row := r.db.QueryRowContext(ctx, identitySelectSQL+` WHERE is_default = 1`)
	identity, err := scanIdentity(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Identity{}, ErrNotFound
	}
	return identity, err
}

// GetAllIdentityAddresses returns all stored identity addresses (used for Reply-All exclusion).
func (r *IdentityRepository) GetAllIdentityAddresses(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT address FROM identities`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var addrs []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		addrs = append(addrs, a)
	}
	return addrs, rows.Err()
}

// CreateIdentity inserts a new identity.
// Address is validated as a bare addr-spec and casefolded before storage.
// If is_default=true, all other identities are cleared in the same transaction.
// If position==0 (unset), append semantics apply: COALESCE(MAX(position), -1) + 1.
// Returns ErrInvalidAddress for a bad address, ErrConflict on duplicate address.
func (r *IdentityRepository) CreateIdentity(ctx context.Context, identity oas.Identity) (oas.Identity, error) {
	addr, err := parseAndFoldAddress(identity.Address)
	if err != nil {
		return oas.Identity{}, err
	}
	identity.Address = addr

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.Identity{}, err
	}
	defer tx.Rollback()

	if identity.Position == 0 {
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(position), -1) + 1 FROM identities`,
		).Scan(&identity.Position); err != nil {
			return oas.Identity{}, err
		}
	}

	if !identity.IsDefault {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities`).Scan(&n); err != nil {
			return oas.Identity{}, err
		}
		if n == 0 {
			identity.IsDefault = true
		}
	}

	if identity.IsDefault {
		if _, err := tx.ExecContext(ctx, `UPDATE identities SET is_default = 0`); err != nil {
			return oas.Identity{}, err
		}
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO identities (name, address, is_default, position, signature) VALUES (?, ?, ?, ?, ?)`,
		identity.Name, identity.Address, boolToInt(identity.IsDefault), identity.Position, identity.Signature,
	)
	if err != nil {
		if isConstraintErr(err) {
			return oas.Identity{}, ErrConflict
		}
		return oas.Identity{}, err
	}

	newID, _ := res.LastInsertId()

	if err := tx.Commit(); err != nil {
		return oas.Identity{}, err
	}

	return r.GetIdentity(ctx, newID)
}

// UpdateIdentity fully replaces an identity (PUT semantics).
// Address is validated and casefolded. If is_default=true, all others are cleared.
// If is_default=false, the current default status is preserved unchanged.
// If the address changes, draft from_addr fields are updated in the same transaction.
// Returns ErrInvalidAddress, ErrNotFound, or ErrConflict on duplicate address.
func (r *IdentityRepository) UpdateIdentity(ctx context.Context, id int64, identity oas.Identity) (oas.Identity, error) {
	addr, err := parseAndFoldAddress(identity.Address)
	if err != nil {
		return oas.Identity{}, err
	}
	identity.Address = addr

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.Identity{}, err
	}
	defer tx.Rollback()

	var currentAddr string
	var currentIsDefault int
	err = tx.QueryRowContext(ctx,
		`SELECT address, is_default FROM identities WHERE id = ?`, id,
	).Scan(&currentAddr, &currentIsDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Identity{}, ErrNotFound
	}
	if err != nil {
		return oas.Identity{}, err
	}

	newIsDefault := currentIsDefault
	if identity.IsDefault {
		newIsDefault = 1
		if _, err := tx.ExecContext(ctx,
			`UPDATE identities SET is_default = 0 WHERE id != ?`, id,
		); err != nil {
			return oas.Identity{}, err
		}
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE identities SET name = ?, address = ?, is_default = ?, position = ?, signature = ? WHERE id = ?`,
		identity.Name, identity.Address, newIsDefault, identity.Position, identity.Signature, id,
	); err != nil {
		if isConstraintErr(err) {
			return oas.Identity{}, ErrConflict
		}
		return oas.Identity{}, err
	}

	if identity.Address != currentAddr {
		if _, err := tx.ExecContext(ctx,
			`UPDATE messages SET from_addr = ? WHERE identity_id = ? AND folder_id = 3`,
			identity.Address, id,
		); err != nil {
			return oas.Identity{}, err
		}
	}

	if err := tx.Commit(); err != nil {
		return oas.Identity{}, err
	}

	return r.GetIdentity(ctx, id)
}

// DeleteIdentity deletes an identity. Returns ErrLastIdentity if it is the only one,
// ErrNotFound if no such identity exists.
// If the deleted identity was the default, the one with the lowest position (then id) is promoted.
// After deletion, draft from_addr values matching the deleted address are cleared.
func (r *IdentityRepository) DeleteIdentity(ctx context.Context, id int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var addr string
	var isDefault int
	err = tx.QueryRowContext(ctx,
		`SELECT address, is_default FROM identities WHERE id = ?`, id,
	).Scan(&addr, &isDefault)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return ErrLastIdentity
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM identities WHERE id = ?`, id); err != nil {
		return err
	}

	// FK cascade has set identity_id = NULL on drafts referencing this identity;
	// clear from_addr on those drafts if it matches the deleted address.
	if _, err := tx.ExecContext(ctx,
		`UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3 AND from_addr = ?`,
		addr,
	); err != nil {
		return err
	}

	if isDefault != 0 {
		// Promote the remaining identity with the lowest position (then id).
		if _, err := tx.ExecContext(ctx, `
			UPDATE identities SET is_default = 1
			WHERE id = (SELECT id FROM identities ORDER BY position ASC, id ASC LIMIT 1)
		`); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// ReorderIdentities assigns positions 0,1,2,… to the supplied identity IDs in a single
// transaction. The ids slice must contain every existing identity ID exactly once.
// Returns the number of rows updated, or a validation error.
func (r *IdentityRepository) ReorderIdentities(ctx context.Context, ids []int64) (int, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `SELECT id FROM identities`)
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
		if _, err := tx.ExecContext(ctx, `UPDATE identities SET position = ? WHERE id = ?`, pos, id); err != nil {
			return 0, err
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(ids), nil
}
