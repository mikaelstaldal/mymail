package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mikaelstaldal/mymail/internal/model"
)

// DraftRepository provides draft and scheduled-message operations backed by SQLite.
type DraftRepository struct {
	db *sql.DB
}

// NewDraftRepository creates a DraftRepository.
func NewDraftRepository(db *sql.DB) *DraftRepository {
	return &DraftRepository{db: db}
}

// resolveFromAddr derives from_addr from identityID.
// If identityID is set, looks it up and returns ErrUnknownIdentity if not found.
// If identityID is nil, uses the default identity address, or "" if no identities exist.
func (r *DraftRepository) resolveFromAddr(ctx context.Context, identityID sql.NullInt64) (string, error) {
	if identityID.Valid {
		var addr string
		err := r.db.QueryRowContext(ctx, `SELECT address FROM identities WHERE id = ?`, identityID.Int64).Scan(&addr)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUnknownIdentity
		}
		if err != nil {
			return "", err
		}
		return addr, nil
	}
	var addr string
	err := r.db.QueryRowContext(ctx, `SELECT address FROM identities WHERE is_default = 1`).Scan(&addr)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return addr, nil
}

// GetDraft returns the draft with the given ID.
// Returns ErrNotFound if the message does not exist or is not in Drafts (folder_id=3).
func (r *DraftRepository) GetDraft(ctx context.Context, id int64) (model.DBMessage, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+dbMessageColumns+` FROM messages WHERE id = ? AND folder_id = 3`, id,
	)
	m, err := scanDBMessage(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	return m, err
}

// CreateDraft inserts a new draft (folder_id=3, raw=NULL) and returns the new ID.
// from_addr is resolved from msg.IdentityID: if set and not found → ErrUnknownIdentity;
// if nil, uses the default identity address or "" if no identities exist.
func (r *DraftRepository) CreateDraft(ctx context.Context, msg model.DBMessage) (int64, error) {
	fromAddr, err := r.resolveFromAddr(ctx, msg.IdentityID)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC().Format(time.RFC3339)
	dateStr := msg.Date.UTC().Format(time.RFC3339)

	var identityIDVal any
	if msg.IdentityID.Valid {
		identityIDVal = msg.IdentityID.Int64
	}
	var messageIDVal any
	if msg.MessageID.Valid {
		messageIDVal = msg.MessageID.String
	}
	var inReplyToVal any
	if msg.InReplyTo.Valid {
		inReplyToVal = msg.InReplyTo.String
	}
	var refsVal any
	if msg.References.Valid && msg.References.String != "" {
		refsVal = msg.References.String
	}
	var sendAtVal any
	if msg.SendAt.Valid {
		sendAtVal = msg.SendAt.Time.UTC().Format(time.RFC3339)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			folder_id, identity_id, message_id, in_reply_to, "references",
			from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
			date, body_text, body_html, raw,
			read, flagged, has_attachments, has_external_images,
			send_at, send_failure_count,
			created_at, updated_at
		) VALUES (
			3, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, NULL,
			0, 0, 0, 0,
			?, 0,
			?, ?
		)`,
		identityIDVal, messageIDVal, inReplyToVal, refsVal,
		fromAddr, msg.ToAddr, msg.CcAddr, msg.BccAddr, msg.ReplyToAddr, msg.Subject,
		dateStr, msg.BodyText, msg.BodyHTML,
		sendAtVal,
		now, now,
	)
	if err != nil {
		return 0, err
	}

	draftID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if msg.References.Valid && msg.References.String != "" {
		if err := insertMessageRefs(ctx, tx, draftID, msg.References.String); err != nil {
			return 0, err
		}
	}

	return draftID, tx.Commit()
}

// CreateDraftCopying creates a draft and atomically copies all attachments from
// sourceMessageID (when non-nil) in a single transaction.
// Returns ErrSourceNotFound (→ 400) if sourceMessageID does not exist.
// Identity resolution follows the same rules as CreateDraft.
func (r *DraftRepository) CreateDraftCopying(ctx context.Context, msg model.DBMessage, sourceMessageID *int64) (int64, error) {
	// Resolve from_addr before the transaction (read-only, safe outside tx).
	fromAddr, err := r.resolveFromAddr(ctx, msg.IdentityID)
	if err != nil {
		return 0, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	dateStr := msg.Date.UTC().Format(time.RFC3339)

	var identityIDVal any
	if msg.IdentityID.Valid {
		identityIDVal = msg.IdentityID.Int64
	}
	var messageIDVal any
	if msg.MessageID.Valid {
		messageIDVal = msg.MessageID.String
	}
	var inReplyToVal any
	if msg.InReplyTo.Valid {
		inReplyToVal = msg.InReplyTo.String
	}
	var refsVal any
	if msg.References.Valid && msg.References.String != "" {
		refsVal = msg.References.String
	}
	var sendAtVal any
	if msg.SendAt.Valid {
		sendAtVal = msg.SendAt.Time.UTC().Format(time.RFC3339)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			folder_id, identity_id, message_id, in_reply_to, "references",
			from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
			date, body_text, body_html, raw,
			read, flagged, has_attachments, has_external_images,
			send_at, send_failure_count,
			created_at, updated_at
		) VALUES (
			3, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, NULL,
			0, 0, 0, 0,
			?, 0,
			?, ?
		)`,
		identityIDVal, messageIDVal, inReplyToVal, refsVal,
		fromAddr, msg.ToAddr, msg.CcAddr, msg.BccAddr, msg.ReplyToAddr, msg.Subject,
		dateStr, msg.BodyText, msg.BodyHTML,
		sendAtVal,
		now, now,
	)
	if err != nil {
		return 0, err
	}

	draftID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if msg.References.Valid && msg.References.String != "" {
		if err := insertMessageRefs(ctx, tx, draftID, msg.References.String); err != nil {
			return 0, err
		}
	}

	if sourceMessageID != nil {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ?`, *sourceMessageID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return 0, ErrSourceNotFound
		}
		if err != nil {
			return 0, err
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO attachments (message_id, filename, content_type, size, data)
			 SELECT ?, filename, content_type, size, data FROM attachments WHERE message_id = ?`,
			draftID, *sourceMessageID,
		); err != nil {
			return 0, err
		}
	}

	return draftID, tx.Commit()
}

// UpdateDraft replaces all draft fields on message id.
// Returns ErrNotFound if the message does not exist or is not in Drafts (folder_id=3).
// Identity resolution follows the same rules as CreateDraft.
func (r *DraftRepository) UpdateDraft(ctx context.Context, id int64, msg model.DBMessage) error {
	fromAddr, err := r.resolveFromAddr(ctx, msg.IdentityID)
	if err != nil {
		return err
	}

	dateStr := msg.Date.UTC().Format(time.RFC3339)

	var identityIDVal any
	if msg.IdentityID.Valid {
		identityIDVal = msg.IdentityID.Int64
	}
	var messageIDVal any
	if msg.MessageID.Valid {
		messageIDVal = msg.MessageID.String
	}
	var inReplyToVal any
	if msg.InReplyTo.Valid {
		inReplyToVal = msg.InReplyTo.String
	}
	var refsVal any
	if msg.References.Valid && msg.References.String != "" {
		refsVal = msg.References.String
	}
	var sendAtVal any
	if msg.SendAt.Valid {
		sendAtVal = msg.SendAt.Time.UTC().Format(time.RFC3339)
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
		UPDATE messages SET
			identity_id = ?, message_id = ?, in_reply_to = ?, "references" = ?,
			from_addr = ?, to_addr = ?, cc_addr = ?, bcc_addr = ?, reply_to_addr = ?,
			subject = ?, date = ?, body_text = ?, body_html = ?,
			send_at = ?
		WHERE id = ? AND folder_id = 3`,
		identityIDVal, messageIDVal, inReplyToVal, refsVal,
		fromAddr, msg.ToAddr, msg.CcAddr, msg.BccAddr, msg.ReplyToAddr,
		msg.Subject, dateStr, msg.BodyText, msg.BodyHTML,
		sendAtVal,
		id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM message_references WHERE message_id = ?`, id); err != nil {
		return err
	}
	refsStr := ""
	if msg.References.Valid {
		refsStr = msg.References.String
	}
	if err := insertMessageRefs(ctx, tx, id, refsStr); err != nil {
		return err
	}

	return tx.Commit()
}

// DeleteDraft permanently deletes a draft (folder_id=3).
// Returns ErrNotFound if the message does not exist or is not in Drafts.
// Attachments are cascade-deleted by the foreign key constraint.
func (r *DraftRepository) DeleteDraft(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ? AND folder_id = 3`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetScheduledMessages returns all scheduled messages (folder_id=5) whose send_at
// is at or before now, ordered by send_at ASC.
func (r *DraftRepository) GetScheduledMessages(ctx context.Context) ([]model.DBMessage, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+dbMessageColumns+` FROM messages WHERE folder_id = 5 AND send_at <= ? ORDER BY send_at ASC`,
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []model.DBMessage
	for rows.Next() {
		m, err := scanDBMessage(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ConditionalUpdateScheduled applies updates to a scheduled message only while it
// remains in folder_id=5 with a non-NULL send_at. Returns true if one row was affected.
// Allowed keys: "folder_id" (int64), "send_at" (time.Time or nil), "send_failure_count" (int), "send_error" (string or nil).
func (r *DraftRepository) ConditionalUpdateScheduled(ctx context.Context, id int64, updates map[string]any) (bool, error) {
	if len(updates) == 0 {
		return false, nil
	}

	var setClauses []string
	var args []any

	for _, key := range []string{"folder_id", "send_at", "send_failure_count", "send_error"} {
		v, ok := updates[key]
		if !ok {
			continue
		}
		setClauses = append(setClauses, key+" = ?")
		switch key {
		case "send_at":
			if v == nil {
				args = append(args, nil)
			} else if t, ok := v.(time.Time); ok {
				args = append(args, t.UTC().Format(time.RFC3339))
			} else {
				args = append(args, v)
			}
		case "send_error":
			args = append(args, v) // nil stored as NULL, string stored as-is
		default:
			args = append(args, v)
		}
	}

	if len(setClauses) == 0 {
		return false, nil
	}

	args = append(args, id)
	query := `UPDATE messages SET ` + strings.Join(setClauses, ", ") +
		` WHERE id = ? AND send_at IS NOT NULL AND folder_id = 5`

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// RescheduleMessage updates the send_at of a message that is currently in the Scheduled
// folder (folder_id=5). Returns ErrNotFound if the message does not exist or is not in Scheduled.
func (r *DraftRepository) RescheduleMessage(ctx context.Context, id int64, sendAt time.Time) (model.DBMessage, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE messages SET send_at = ? WHERE id = ? AND folder_id = 5`,
		sendAt.UTC().Format(time.RFC3339), id,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrNotFound
	}
	row := r.db.QueryRowContext(ctx, `SELECT `+dbMessageColumns+` FROM messages WHERE id = ?`, id)
	m, err := scanDBMessage(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	return m, err
}

// MarkSent moves a claimed scheduled message to Sent (folder_id=2) and sets its Message-ID.
// Call this only after ConditionalUpdateScheduled has cleared send_at (claim step).
func (r *DraftRepository) MarkSent(ctx context.Context, id int64, msgID string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages SET folder_id = 2, read = 1, message_id = ? WHERE id = ?`,
		msgID, id,
	)
	return err
}

// MoveToDrafts unconditionally moves a message to Drafts (folder_id=3).
// Call this to recover a claimed scheduled message when the send attempt fails.
func (r *DraftRepository) MoveToDrafts(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE messages SET folder_id = 3, send_failure_count = 0, send_error = NULL WHERE id = ?`,
		id,
	)
	return err
}

// CancelScheduled moves a scheduled message back to Drafts (folder_id=3),
// clearing send_at, send_failure_count, and send_error in a single UPDATE.
// Returns ErrNotFound if the message does not exist or is not in Scheduled (folder_id=5).
func (r *DraftRepository) CancelScheduled(ctx context.Context, id int64) (model.DBMessage, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE messages
		SET send_at = NULL, send_failure_count = 0, send_error = NULL, folder_id = 3
		WHERE id = ? AND folder_id = 5`,
		id,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrNotFound
	}
	return r.GetDraft(ctx, id)
}
