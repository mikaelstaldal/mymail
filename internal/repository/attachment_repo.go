package repository

import (
	"context"
	"database/sql"
	"errors"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
)

// AttachmentRepository provides attachment CRUD operations backed by SQLite.
type AttachmentRepository struct {
	db *sql.DB
}

// NewAttachmentRepository creates an AttachmentRepository.
func NewAttachmentRepository(db *sql.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
}

// InsertAttachment inserts a new attachment row and returns its assigned ID.
func (r *AttachmentRepository) InsertAttachment(ctx context.Context, att model.DBAttachment) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO attachments (message_id, filename, content_type, size, data)
		 VALUES (?, ?, ?, ?, ?)`,
		att.MessageID, att.Filename, att.ContentType, att.Size, att.Data,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListAttachments returns metadata for all attachments belonging to messageID,
// ordered by id. The data BLOB is excluded.
func (r *AttachmentRepository) ListAttachments(ctx context.Context, messageID int64) ([]oas.AttachmentMeta, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, filename, content_type, size
		 FROM attachments WHERE message_id = ? ORDER BY id`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []oas.AttachmentMeta
	for rows.Next() {
		var a oas.AttachmentMeta
		if err := rows.Scan(&a.ID, &a.Filename, &a.ContentType, &a.Size); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if out == nil {
		out = []oas.AttachmentMeta{}
	}
	return out, nil
}

// GetAttachment returns the full attachment row including the data BLOB, or ErrNotFound.
func (r *AttachmentRepository) GetAttachment(ctx context.Context, id int64) (model.DBAttachment, error) {
	var a model.DBAttachment
	err := r.db.QueryRowContext(ctx,
		`SELECT id, message_id, filename, content_type, size, data
		 FROM attachments WHERE id = ?`,
		id,
	).Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.Data)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBAttachment{}, ErrNotFound
	}
	return a, err
}

// DeleteAttachment deletes the attachment only if it belongs to messageID.
// Returns ErrNotFound if the attachment does not exist or belongs to a different message.
func (r *AttachmentRepository) DeleteAttachment(ctx context.Context, id int64, messageID int64) error {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM attachments WHERE id = ? AND message_id = ?`,
		id, messageID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
