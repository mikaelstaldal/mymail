package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"golang.org/x/text/cases"
)

// ContactRepository provides contact CRUD operations backed by SQLite.
type ContactRepository struct {
	db *sql.DB
}

// NewContactRepository creates a ContactRepository.
func NewContactRepository(db *sql.DB) *ContactRepository {
	return &ContactRepository{db: db}
}

func scanContact(scan func(dest ...any) error) (oas.Contact, error) {
	var (
		c            oas.Contact
		createdAtStr string
		updatedAtStr string
	)
	if err := scan(&c.ID, &c.Address, &c.Name, &createdAtStr, &updatedAtStr); err != nil {
		return oas.Contact{}, err
	}
	cat, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return oas.Contact{}, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
	}
	c.CreatedAt = cat
	uat, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return oas.Contact{}, fmt.Errorf("parse updated_at %q: %w", updatedAtStr, err)
	}
	c.UpdatedAt = uat
	return c, nil
}

const contactSelectSQL = `SELECT id, address, name, created_at, updated_at FROM contacts`
const contactOrderSQL = ` ORDER BY CASE WHEN name = '' THEN 1 ELSE 0 END, LOWER(name), LOWER(address)`

// ListContacts returns contacts with an optional substring filter on name+address, plus the total count.
func (r *ContactRepository) ListContacts(ctx context.Context, q *string, limit, offset int) ([]oas.Contact, int, error) {
	var (
		where string
		args  []any
	)
	if q != nil {
		where = ` WHERE LOWER(name) LIKE '%' || LOWER(?) || '%' OR LOWER(address) LIKE '%' || LOWER(?) || '%'`
		args = []any{*q, *q}
	}

	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM contacts`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}

	queryArgs := append(args, limit, offset)
	rows, err := r.db.QueryContext(ctx,
		contactSelectSQL+where+contactOrderSQL+` LIMIT ? OFFSET ?`,
		queryArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	contacts := []oas.Contact{}
	for rows.Next() {
		c, err := scanContact(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		contacts = append(contacts, c)
	}
	return contacts, total, rows.Err()
}

// GetContact returns a single contact by ID, or ErrNotFound.
func (r *ContactRepository) GetContact(ctx context.Context, id int64) (oas.Contact, error) {
	row := r.db.QueryRowContext(ctx, contactSelectSQL+` WHERE id = ?`, id)
	c, err := scanContact(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.Contact{}, ErrNotFound
	}
	return c, err
}

// CreateContact inserts a new contact.
// Address is validated as a bare addr-spec and casefolded before storage.
// Returns ErrInvalidAddress for a bad address, ErrConflict on duplicate address.
func (r *ContactRepository) CreateContact(ctx context.Context, contact oas.Contact) (oas.Contact, error) {
	addr, err := parseAndFoldAddress(contact.Address)
	if err != nil {
		return oas.Contact{}, err
	}
	contact.Address = addr

	res, err := r.db.ExecContext(ctx,
		`INSERT INTO contacts (address, name, created_at, updated_at)
		 VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))`,
		contact.Address, contact.Name,
	)
	if err != nil {
		if isConstraintErr(err) {
			return oas.Contact{}, ErrConflict
		}
		return oas.Contact{}, err
	}

	newID, _ := res.LastInsertId()
	return r.GetContact(ctx, newID)
}

// UpdateContact fully replaces a contact (PUT semantics).
// Address is validated and casefolded. updated_at is set explicitly in the UPDATE.
// Returns ErrInvalidAddress, ErrNotFound, or ErrConflict on duplicate address.
func (r *ContactRepository) UpdateContact(ctx context.Context, id int64, contact oas.Contact) (oas.Contact, error) {
	addr, err := parseAndFoldAddress(contact.Address)
	if err != nil {
		return oas.Contact{}, err
	}
	contact.Address = addr

	res, err := r.db.ExecContext(ctx,
		`UPDATE contacts SET address = ?, name = ?, updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE id = ?`,
		contact.Address, contact.Name, id,
	)
	if err != nil {
		if isConstraintErr(err) {
			return oas.Contact{}, ErrConflict
		}
		return oas.Contact{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return oas.Contact{}, ErrNotFound
	}

	return r.GetContact(ctx, id)
}

// DeleteContact removes a contact by ID. Returns ErrNotFound if no such contact exists.
func (r *ContactRepository) DeleteContact(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM contacts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpsertContact atomically inserts a contact or, if the address already exists with an empty name,
// updates the name. The address is Unicode-simple-casefolded before storage.
func (r *ContactRepository) UpsertContact(ctx context.Context, address, name string) error {
	address = cases.Fold().String(address)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO contacts (address, name, created_at, updated_at)
		VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))
		ON CONFLICT(address) DO UPDATE SET
			name       = excluded.name,
			updated_at = strftime('%Y-%m-%dT%H:%M:%SZ','now')
		WHERE contacts.name = ''`,
		address, name,
	)
	return err
}
