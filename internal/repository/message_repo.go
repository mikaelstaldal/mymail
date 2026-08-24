package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
)

// MessageRepository provides message CRUD operations backed by SQLite.
type MessageRepository struct {
	db *sql.DB
}

// NewMessageRepository creates a MessageRepository.
func NewMessageRepository(db *sql.DB) *MessageRepository {
	return &MessageRepository{db: db}
}

// placeholders returns a comma-separated list of n '?' placeholders, e.g. "?,?,?".
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 2*n-1)
	for i := range n {
		b[i*2] = '?'
		if i+1 < n {
			b[i*2+1] = ','
		}
	}
	return string(b)
}

// int64Args converts a slice of int64 to []any for use as SQL args.
func int64Args(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

// stringArgs converts a slice of strings to []any for use as SQL args.
func stringArgs(ss []string) []any {
	args := make([]any, len(ss))
	for i, s := range ss {
		args[i] = s
	}
	return args
}

// setToInt64Slice converts a map[int64]bool set to a slice.
func setToInt64Slice(m map[int64]bool) []int64 {
	out := make([]int64, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// setToStringSlice converts a map[string]bool set to a slice.
func setToStringSlice(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// splitNL splits a newline-separated string, dropping empty parts.
func splitNL(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// const columns selected for MessageSummary scans.
const summaryColumns = `m.id, m.folder_id, m.message_id, m.from_addr, m.to_addr,
	m.subject, m.date, m.read, m.flagged, m.has_attachments, m.send_failure_count,
	m.send_at, m.snoozed_until, m.created_at`

// parseNullTime converts a nullable RFC 3339 column into sql.NullTime. An empty
// string counts as NULL: send_at and snoozed_until are cleared by writing one
// in some paths and a real NULL in others, and both mean "not set".
func parseNullTime(ns sql.NullString, field string) (sql.NullTime, error) {
	if !ns.Valid || ns.String == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse(time.RFC3339, ns.String)
	if err != nil {
		return sql.NullTime{}, fmt.Errorf("parse %s %q: %w", field, ns.String, err)
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// parseNilDateTime is the same column read straight into the API's nullable
// type, for the scans that build an oas value rather than a model.DBMessage.
func parseNilDateTime(ns sql.NullString, field string) (oas.NilDateTime, error) {
	var n oas.NilDateTime
	nt, err := parseNullTime(ns, field)
	if err != nil {
		return n, err
	}
	if nt.Valid {
		n.SetTo(nt.Time)
	} else {
		n.SetToNull()
	}
	return n, nil
}

// scanMessageSummary scans a row with summaryColumns into oas.MessageSummary.
func scanMessageSummary(scan func(...any) error) (oas.MessageSummary, error) {
	var (
		s               oas.MessageSummary
		messageID       sql.NullString
		dateStr         string
		readInt         int
		flaggedInt      int
		hasAttInt       int
		sendFailCnt     int
		sendAtStr       sql.NullString
		snoozedUntilStr sql.NullString
		createdAtStr    string
	)
	if err := scan(&s.ID, &s.FolderID, &messageID, &s.FromAddr, &s.ToAddr,
		&s.Subject, &dateStr, &readInt, &flaggedInt, &hasAttInt, &sendFailCnt,
		&sendAtStr, &snoozedUntilStr, &createdAtStr); err != nil {
		return oas.MessageSummary{}, err
	}
	var err error
	if s.SendAt, err = parseNilDateTime(sendAtStr, "send_at"); err != nil {
		return oas.MessageSummary{}, err
	}
	if s.SnoozedUntil, err = parseNilDateTime(snoozedUntilStr, "snoozed_until"); err != nil {
		return oas.MessageSummary{}, err
	}
	if messageID.Valid {
		s.MessageID = oas.NewNilString(messageID.String)
	} else {
		s.MessageID.SetToNull()
	}
	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return oas.MessageSummary{}, fmt.Errorf("parse date %q: %w", dateStr, err)
	}
	s.Date = t
	s.Read = readInt != 0
	s.Flagged = flaggedInt != 0
	s.HasAttachments = hasAttInt != 0
	s.SendFailed = sendFailCnt > 0 && s.FolderID != 4
	cat, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return oas.MessageSummary{}, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
	}
	s.CreatedAt = cat
	return s, nil
}

// scanDBMessage scans a full messages row into model.DBMessage.
// Expected column order: id, folder_id, identity_id, message_id, in_reply_to, "references",
// from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject, date, body_text, body_html,
// raw, read, flagged, has_attachments, has_external_images, send_at, snoozed_until,
// snooze_folder, send_error, send_failure_count, created_at, updated_at.
func scanDBMessage(scan func(...any) error) (model.DBMessage, error) {
	var (
		m               model.DBMessage
		sendAtStr       sql.NullString
		snoozedUntilStr sql.NullString
		dateStr         string
		createdAtStr    string
		updatedAtStr    string
		readInt         int
		flaggedInt      int
		hasAttInt       int
		hasExtImgInt    int
	)
	if err := scan(
		&m.ID, &m.FolderID, &m.IdentityID, &m.MessageID, &m.InReplyTo, &m.References,
		&m.FromAddr, &m.ToAddr, &m.CcAddr, &m.BccAddr, &m.ReplyToAddr, &m.Subject,
		&dateStr, &m.BodyText, &m.BodyHTML, &m.Raw,
		&readInt, &flaggedInt, &hasAttInt, &hasExtImgInt,
		&sendAtStr, &snoozedUntilStr, &m.SnoozeFolder, &m.SendError,
		&m.SendFailureCount, &createdAtStr, &updatedAtStr,
	); err != nil {
		return model.DBMessage{}, err
	}

	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse date %q: %w", dateStr, err)
	}
	m.Date = t

	if m.SendAt, err = parseNullTime(sendAtStr, "send_at"); err != nil {
		return model.DBMessage{}, err
	}
	if m.SnoozedUntil, err = parseNullTime(snoozedUntilStr, "snoozed_until"); err != nil {
		return model.DBMessage{}, err
	}

	cat, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
	}
	m.CreatedAt = cat

	uat, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse updated_at %q: %w", updatedAtStr, err)
	}
	m.UpdatedAt = uat

	m.Read = readInt != 0
	m.Flagged = flaggedInt != 0
	m.HasAttachments = hasAttInt != 0
	m.HasExternalImages = hasExtImgInt != 0

	return m, nil
}

const dbMessageColumns = `id, folder_id, identity_id, message_id, in_reply_to, "references",
	from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
	date, body_text, body_html, raw, read, flagged, has_attachments, has_external_images,
	send_at, snoozed_until, snooze_folder, send_error, send_failure_count, created_at, updated_at`

// dbMessageColumnsNoRaw selects all message columns except the raw BLOB.
// Used for API detail responses where raw is never needed.
const dbMessageColumnsNoRaw = `id, folder_id, identity_id, message_id, in_reply_to, "references",
	from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
	date, body_text, body_html, read, flagged, has_attachments, has_external_images,
	send_at, snoozed_until, snooze_folder, send_error, send_failure_count, created_at, updated_at`

// scanDBMessageNoRaw scans a row selected with dbMessageColumnsNoRaw into model.DBMessage.
// m.Raw is left nil; callers must not access it.
func scanDBMessageNoRaw(scan func(...any) error) (model.DBMessage, error) {
	var (
		m               model.DBMessage
		sendAtStr       sql.NullString
		snoozedUntilStr sql.NullString
		dateStr         string
		createdAtStr    string
		updatedAtStr    string
		readInt         int
		flaggedInt      int
		hasAttInt       int
		hasExtImgInt    int
	)
	if err := scan(
		&m.ID, &m.FolderID, &m.IdentityID, &m.MessageID, &m.InReplyTo, &m.References,
		&m.FromAddr, &m.ToAddr, &m.CcAddr, &m.BccAddr, &m.ReplyToAddr, &m.Subject,
		&dateStr, &m.BodyText, &m.BodyHTML,
		&readInt, &flaggedInt, &hasAttInt, &hasExtImgInt,
		&sendAtStr, &snoozedUntilStr, &m.SnoozeFolder, &m.SendError,
		&m.SendFailureCount, &createdAtStr, &updatedAtStr,
	); err != nil {
		return model.DBMessage{}, err
	}

	t, err := time.Parse(time.RFC3339, dateStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse date %q: %w", dateStr, err)
	}
	m.Date = t

	if m.SendAt, err = parseNullTime(sendAtStr, "send_at"); err != nil {
		return model.DBMessage{}, err
	}
	if m.SnoozedUntil, err = parseNullTime(snoozedUntilStr, "snoozed_until"); err != nil {
		return model.DBMessage{}, err
	}

	cat, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
	}
	m.CreatedAt = cat

	uat, err := time.Parse(time.RFC3339, updatedAtStr)
	if err != nil {
		return model.DBMessage{}, fmt.Errorf("parse updated_at %q: %w", updatedAtStr, err)
	}
	m.UpdatedAt = uat

	m.Read = readInt != 0
	m.Flagged = flaggedInt != 0
	m.HasAttachments = hasAttInt != 0
	m.HasExternalImages = hasExtImgInt != 0

	return m, nil
}

// ListMessages returns paginated messages in a folder ordered by date DESC, plus total count.
// unread and flagged are optional filters; nil means no filter for that field.
func (r *MessageRepository) ListMessages(ctx context.Context, folderID int64, limit, offset int, unread, flagged *bool) ([]oas.MessageSummary, int, error) {
	conditions := []string{"m.folder_id = ?"}
	filterArgs := []any{folderID}

	if unread != nil {
		if *unread {
			conditions = append(conditions, "m.read = 0")
		} else {
			conditions = append(conditions, "m.read = 1")
		}
	}
	if flagged != nil {
		if *flagged {
			conditions = append(conditions, "m.flagged = 1")
		} else {
			conditions = append(conditions, "m.flagged = 0")
		}
	}

	where := strings.Join(conditions, " AND ")

	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages m WHERE `+where, filterArgs...,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	listArgs := append(filterArgs, limit, offset)
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+summaryColumns+` FROM messages m WHERE `+where+` ORDER BY m.date DESC LIMIT ? OFFSET ?`,
		listArgs...,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []oas.MessageSummary
	for rows.Next() {
		s, err := scanMessageSummary(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, s)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []oas.MessageSummary{}
	}
	return items, total, nil
}

// MessageExists returns true if a message with the given ID exists (in any folder).
func (r *MessageRepository) MessageExists(ctx context.Context, id int64) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT 1 FROM messages WHERE id = ?`, id).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// GetMessage returns the full DBMessage row, or ErrNotFound.
func (r *MessageRepository) GetMessage(ctx context.Context, id int64) (model.DBMessage, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+dbMessageColumns+` FROM messages WHERE id = ?`, id,
	)
	m, err := scanDBMessage(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	return m, err
}

// GetMessageDetail returns the full DBMessage row without the raw BLOB, or ErrNotFound.
// Use this for API responses; use GetMessage only when raw is genuinely needed.
func (r *MessageRepository) GetMessageDetail(ctx context.Context, id int64) (model.DBMessage, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+dbMessageColumnsNoRaw+` FROM messages WHERE id = ?`, id,
	)
	m, err := scanDBMessageNoRaw(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	return m, err
}

// GetMessageSummary returns a summary row, or ErrNotFound.
func (r *MessageRepository) GetMessageSummary(ctx context.Context, id int64) (oas.MessageSummary, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT `+summaryColumns+` FROM messages m WHERE m.id = ?`, id,
	)
	s, err := scanMessageSummary(row.Scan)
	if errors.Is(err, sql.ErrNoRows) {
		return oas.MessageSummary{}, ErrNotFound
	}
	return s, err
}

// InsertMessage inserts a full messages row in a transaction and returns the new ID.
// msg.References is stored as a newline-joined string.
func (r *MessageRepository) InsertMessage(ctx context.Context, msg model.DBMessage) (int64, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	now := time.Now().UTC()
	createdAt := now.Format(time.RFC3339)
	updatedAt := createdAt
	dateStr := msg.Date.UTC().Format(time.RFC3339)

	var sendAtVal any
	if msg.SendAt.Valid {
		sendAtVal = msg.SendAt.Time.UTC().Format(time.RFC3339)
	}
	var snoozedUntilVal any
	if msg.SnoozedUntil.Valid {
		snoozedUntilVal = msg.SnoozedUntil.Time.UTC().Format(time.RFC3339)
	}
	var snoozeFolderVal any
	if msg.SnoozeFolder.Valid {
		snoozeFolderVal = msg.SnoozeFolder.Int64
	}
	var sendErrorVal any
	if msg.SendError.Valid {
		sendErrorVal = msg.SendError.String
	}
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
	readInt := 0
	if msg.Read {
		readInt = 1
	}
	flaggedInt := 0
	if msg.Flagged {
		flaggedInt = 1
	}
	hasAttInt := 0
	if msg.HasAttachments {
		hasAttInt = 1
	}
	hasExtImgInt := 0
	if msg.HasExternalImages {
		hasExtImgInt = 1
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO messages (
			folder_id, identity_id, message_id, in_reply_to, "references",
			from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
			date, body_text, body_html, raw, read, flagged,
			has_attachments, has_external_images,
			send_at, snoozed_until, snooze_folder, send_error, send_failure_count,
			created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?, ?, ?, ?, ?,
			?, ?,
			?, ?, ?, ?, ?,
			?, ?
		)`,
		msg.FolderID, identityIDVal, messageIDVal, inReplyToVal, refsVal,
		msg.FromAddr, msg.ToAddr, msg.CcAddr, msg.BccAddr, msg.ReplyToAddr, msg.Subject,
		dateStr, msg.BodyText, msg.BodyHTML, msg.Raw, readInt, flaggedInt,
		hasAttInt, hasExtImgInt,
		sendAtVal, snoozedUntilVal, snoozeFolderVal, sendErrorVal, msg.SendFailureCount,
		createdAt, updatedAt,
	)
	if err != nil {
		return 0, err
	}

	newID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	if msg.References.Valid && msg.References.String != "" {
		if err := insertMessageRefs(ctx, tx, newID, msg.References.String); err != nil {
			return 0, err
		}
	}

	return newID, tx.Commit()
}

// insertMessageRefs inserts one row per newline-separated reference into message_references.
func insertMessageRefs(ctx context.Context, tx *sql.Tx, messageID int64, refsStr string) error {
	for ref := range strings.SplitSeq(refsStr, "\n") {
		if ref == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO message_references (message_id, ref_msg_id) VALUES (?, ?)`,
			messageID, ref,
		); err != nil {
			return err
		}
	}
	return nil
}

// UpdateMessage applies a PATCH update on the provided fields and returns the updated row.
// Allowed keys: "folder_id" (int64), "read" (bool), "flagged" (bool).
// When folder_id is set to 4 (Trash) or 7 (Junk), scheduling fields are cleared atomically.
func (r *MessageRepository) UpdateMessage(ctx context.Context, id int64, fields map[string]any) (model.DBMessage, error) {
	if len(fields) == 0 {
		return r.GetMessageDetail(ctx, id)
	}

	var setClauses []string
	var args []any
	for _, key := range []string{"folder_id", "read", "flagged"} {
		v, ok := fields[key]
		if !ok {
			continue
		}
		setClauses = append(setClauses, key+" = ?")
		switch key {
		case "read", "flagged":
			if b, ok := v.(bool); ok && b {
				args = append(args, 1)
			} else {
				args = append(args, 0)
			}
		default:
			args = append(args, v)
		}
	}
	if len(setClauses) == 0 {
		return r.GetMessageDetail(ctx, id)
	}

	if newFolderID, ok := fields["folder_id"].(int64); ok && (newFolderID == 4 || newFolderID == 7) {
		setClauses = append(setClauses, "snoozed_until = NULL", "snooze_folder = NULL", "send_at = NULL")
	}

	args = append(args, id)

	res, err := r.db.ExecContext(ctx,
		`UPDATE messages SET `+strings.Join(setClauses, ", ")+` WHERE id = ?`,
		args...,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrNotFound
	}
	return r.GetMessageDetail(ctx, id)
}

// BulkUpdateMessages sets read and/or flagged on a set of messages. Returns count changed.
// Returns ErrNotFound if any ID is missing; ErrTooManyIDs if len > 1000.
func (r *MessageRepository) BulkUpdateMessages(ctx context.Context, ids []int64, read *bool, flagged *bool) (int, error) {
	if len(ids) > 1000 {
		return 0, ErrTooManyIDs
	}
	if len(ids) == 0 || (read == nil && flagged == nil) {
		return 0, nil
	}

	var count int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE id IN (`+placeholders(len(ids))+`)`,
		int64Args(ids)...,
	).Scan(&count); err != nil {
		return 0, err
	}
	if count != len(ids) {
		return 0, ErrNotFound
	}

	var setClauses []string
	var args []any
	if read != nil {
		setClauses = append(setClauses, "read = ?")
		if *read {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if flagged != nil {
		setClauses = append(setClauses, "flagged = ?")
		if *flagged {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}

	args = append(args, int64Args(ids)...)
	query := `UPDATE messages SET ` + strings.Join(setClauses, ", ") +
		` WHERE id IN (` + placeholders(len(ids)) + `)`

	res, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteMessage moves message to Trash unless it is already in Trash (4) or Junk (7),
// in which case it is permanently deleted. Returns ErrForbiddenFolder for Drafts (3),
// Scheduled (5), or Snoozed (6). When moving to Trash, scheduling fields are cleared.
func (r *MessageRepository) DeleteMessage(ctx context.Context, id int64) error {
	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	switch folderID {
	case 3, 5, 6:
		return ErrForbiddenFolder
	case 4, 7:
		_, err = r.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id)
	default:
		_, err = r.db.ExecContext(ctx,
			`UPDATE messages SET folder_id = 4, snoozed_until = NULL, snooze_folder = NULL, send_at = NULL WHERE id = ?`, id)
	}
	return err
}

// BulkDeleteMessages applies delete logic for a batch of messages (all-or-nothing).
// Returns ErrNotFound if any ID is missing; ErrTooManyIDs if len > 1000.
// Returns ErrForbiddenFolder if any message is in Drafts (3), Scheduled (5), or Snoozed (6).
// When moving to Trash, scheduling fields are cleared. Returns (movedToTrash, permanentlyDeleted, error).
func (r *MessageRepository) BulkDeleteMessages(ctx context.Context, ids []int64) (int, int, error) {
	if len(ids) > 1000 {
		return 0, 0, ErrTooManyIDs
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	// Verify all IDs exist and check folder restrictions.
	rows, err := tx.QueryContext(ctx,
		`SELECT id, folder_id FROM messages WHERE id IN (`+placeholders(len(ids))+`)`,
		int64Args(ids)...,
	)
	if err != nil {
		return 0, 0, err
	}
	found := make(map[int64]int64, len(ids))
	for rows.Next() {
		var rid, rfid int64
		if err := rows.Scan(&rid, &rfid); err != nil {
			rows.Close()
			return 0, 0, err
		}
		found[rid] = rfid
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		fid, ok := found[id]
		if !ok {
			return 0, 0, ErrNotFound
		}
		if fid == 3 || fid == 5 || fid == 6 {
			return 0, 0, ErrForbiddenFolder
		}
	}

	// Permanently delete those in Trash (4) or Junk (7).
	res, err := tx.ExecContext(ctx,
		`DELETE FROM messages WHERE id IN (`+placeholders(len(ids))+`) AND folder_id IN (4, 7)`,
		int64Args(ids)...,
	)
	if err != nil {
		return 0, 0, err
	}
	perm, _ := res.RowsAffected()

	// Move the rest to Trash, clearing scheduling fields.
	res, err = tx.ExecContext(ctx,
		`UPDATE messages SET folder_id = 4, snoozed_until = NULL, snooze_folder = NULL, send_at = NULL
		 WHERE id IN (`+placeholders(len(ids))+`) AND folder_id NOT IN (4, 7)`,
		int64Args(ids)...,
	)
	if err != nil {
		return 0, 0, err
	}
	moved, _ := res.RowsAffected()

	return int(moved), int(perm), tx.Commit()
}

// MoveMessages moves all listed messages to the target folder.
// Returns ErrNotFound if any ID is missing; ErrTooManyIDs if len > 1000.
// Returns ErrForbiddenFolder if any source message is in Drafts (3), Scheduled (5), or Snoozed (6).
// When moving to Trash (4), scheduling fields are cleared atomically.
func (r *MessageRepository) MoveMessages(ctx context.Context, ids []int64, folderID int64) (int, error) {
	if len(ids) > 1000 {
		return 0, ErrTooManyIDs
	}
	if len(ids) == 0 {
		return 0, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx,
		`SELECT id, folder_id FROM messages WHERE id IN (`+placeholders(len(ids))+`)`,
		int64Args(ids)...,
	)
	if err != nil {
		return 0, err
	}
	found := make(map[int64]int64, len(ids))
	for rows.Next() {
		var rid, rfid int64
		if err := rows.Scan(&rid, &rfid); err != nil {
			rows.Close()
			return 0, err
		}
		found[rid] = rfid
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		srcFID, ok := found[id]
		if !ok {
			return 0, ErrNotFound
		}
		if srcFID == 3 || srcFID == 5 || srcFID == 6 {
			return 0, ErrForbiddenFolder
		}
	}

	var res sql.Result
	if folderID == 4 {
		res, err = tx.ExecContext(ctx,
			`UPDATE messages SET folder_id = ?, snoozed_until = NULL, snooze_folder = NULL, send_at = NULL
			 WHERE id IN (`+placeholders(len(ids))+`)`,
			append([]any{folderID}, int64Args(ids)...)...,
		)
	} else {
		res, err = tx.ExecContext(ctx,
			`UPDATE messages SET folder_id = ? WHERE id IN (`+placeholders(len(ids))+`)`,
			append([]any{folderID}, int64Args(ids)...)...,
		)
	}
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int(n), nil
}

// GetRawMessage returns the raw BLOB for a message, or nil for drafts.
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) GetRawMessage(ctx context.Context, id int64) ([]byte, error) {
	var raw []byte
	err := r.db.QueryRowContext(ctx, `SELECT raw FROM messages WHERE id = ?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return raw, err
}

// threadSeedRow holds the minimal columns needed for the thread algorithm.
type threadSeedRow struct {
	id        int64
	messageID sql.NullString
	inReplyTo sql.NullString
	refs      sql.NullString
	subject   string // only populated for the seed row and subject-fallback candidates
	folderID  int64  // only populated for the seed row
}

// scanThreadSeedRow scans id, message_id, in_reply_to, "references".
func scanThreadSeedRow(scan func(...any) error) (threadSeedRow, error) {
	var r threadSeedRow
	return r, scan(&r.id, &r.messageID, &r.inReplyTo, &r.refs)
}

var subjectPrefixRe = regexp.MustCompile(`(?i)^[ \t]*(re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]*`)

func normalizeSubject(s string) string {
	for {
		ns := subjectPrefixRe.ReplaceAllString(s, "")
		if ns == s {
			break
		}
		s = ns
	}
	return strings.TrimSpace(s)
}

// addThreadRow adds a newly found thread row to the tracking sets and returns true if it was new.
func addThreadRow(
	row threadSeedRow,
	foundIDs map[int64]bool,
	knownMsgIDs map[string]bool,
	referencedMsgIDs map[string]bool,
) {
	foundIDs[row.id] = true
	if row.messageID.Valid && row.messageID.String != "" {
		knownMsgIDs[row.messageID.String] = true
	}
	if row.inReplyTo.Valid && row.inReplyTo.String != "" {
		referencedMsgIDs[row.inReplyTo.String] = true
	}
	for _, ref := range splitNL(row.refs.String) {
		referencedMsgIDs[ref] = true
	}
}

// fetchSummariesByIDs fetches MessageSummary rows for the given IDs ordered by date ASC.
func (r *MessageRepository) fetchSummariesByIDs(ctx context.Context, ids []int64) ([]oas.MessageSummary, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+summaryColumns+` FROM messages m WHERE m.id IN (`+placeholders(len(ids))+`) ORDER BY m.date ASC`,
		int64Args(ids)...,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []oas.MessageSummary
	for rows.Next() {
		s, err := scanMessageSummary(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// GetMessageThread returns all messages in the thread, using the iterative transitive-closure
// algorithm with a 1000-message cap and subject-based fallback.
func (r *MessageRepository) GetMessageThread(ctx context.Context, id int64) ([]oas.MessageSummary, bool, error) {
	// Fetch seed row including subject and folder_id for the subject-based fallback.
	var seed threadSeedRow
	err := r.db.QueryRowContext(ctx,
		`SELECT id, message_id, in_reply_to, "references", subject, folder_id FROM messages WHERE id = ?`, id,
	).Scan(&seed.id, &seed.messageID, &seed.inReplyTo, &seed.refs, &seed.subject, &seed.folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}

	foundIDs := make(map[int64]bool)
	knownMsgIDs := make(map[string]bool)
	referencedMsgIDs := make(map[string]bool)

	addThreadRow(seed, foundIDs, knownMsgIDs, referencedMsgIDs)

	truncated := false

	for len(foundIDs) < 1000 {
		prevCount := len(foundIDs)

		// Forward query: find messages that reference any ID in knownMsgIDs.
		if len(knownMsgIDs) > 0 {
			newRows, err := r.threadForwardQuery(ctx, knownMsgIDs, foundIDs)
			if err != nil {
				return nil, false, err
			}
			for _, row := range newRows {
				if len(foundIDs) >= 1000 {
					truncated = true
					break
				}
				addThreadRow(row, foundIDs, knownMsgIDs, referencedMsgIDs)
			}
			if truncated {
				break
			}
		}

		// Backward query: find messages whose message_id is referenced by found rows.
		if len(referencedMsgIDs) > 0 {
			newRows, err := r.threadBackwardQuery(ctx, referencedMsgIDs, foundIDs)
			if err != nil {
				return nil, false, err
			}
			for _, row := range newRows {
				if len(foundIDs) >= 1000 {
					truncated = true
					break
				}
				addThreadRow(row, foundIDs, knownMsgIDs, referencedMsgIDs)
			}
			if truncated {
				break
			}
		}

		if len(foundIDs) == prevCount {
			break // fixed point
		}
	}

	if len(foundIDs) >= 1000 {
		truncated = true
	}

	// Subject-based fallback when only the seed was found.
	// Uses FTS5 on the subject column for indexed lookup instead of a full table scan.
	if len(foundIDs) == 1 {
		normSubject := normalizeSubject(seed.subject)

		if normSubject != "" {
			ftsTerm := `subject : ` + sanitizeFTSQuery(normSubject)
			rows, err := r.db.QueryContext(ctx,
				`SELECT m.id, m.message_id, m.in_reply_to, m."references", m.subject
				 FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id
				 WHERE messages_fts MATCH ? AND m.id != ? AND m.folder_id = ?
				 LIMIT 999`,
				ftsTerm, id, seed.folderID,
			)
			if err != nil {
				return nil, false, err
			}
			defer rows.Close()
			for rows.Next() {
				var row threadSeedRow
				if err := rows.Scan(&row.id, &row.messageID, &row.inReplyTo, &row.refs, &row.subject); err != nil {
					return nil, false, err
				}
				if strings.EqualFold(normalizeSubject(row.subject), normSubject) {
					if len(foundIDs) >= 1000 {
						truncated = true
						break
					}
					foundIDs[row.id] = true
				}
			}
			if err := rows.Err(); err != nil {
				return nil, false, err
			}
		}
	}

	summaries, err := r.fetchSummariesByIDs(ctx, setToInt64Slice(foundIDs))
	if err != nil {
		return nil, false, err
	}
	if summaries == nil {
		summaries = []oas.MessageSummary{}
	}
	return summaries, truncated, nil
}

// threadForwardQuery returns rows that link to any of the given knownMsgIDs
// and are not already in foundIDs.
func (r *MessageRepository) threadForwardQuery(
	ctx context.Context,
	knownMsgIDs map[string]bool,
	foundIDs map[int64]bool,
) ([]threadSeedRow, error) {
	msgIDs := setToStringSlice(knownMsgIDs)
	excluded := setToInt64Slice(foundIDs)

	// Build: in_reply_to IN (?,…) OR LIKE clause per msgID.
	var sb strings.Builder
	var args []any

	sb.WriteString(`SELECT id, message_id, in_reply_to, "references" FROM messages WHERE `)

	if len(excluded) > 0 {
		sb.WriteString(`id NOT IN (` + placeholders(len(excluded)) + `) AND `)
		args = append(args, int64Args(excluded)...)
	}

	sb.WriteString(`(in_reply_to IN (` + placeholders(len(msgIDs)) + `)`)
	args = append(args, stringArgs(msgIDs)...)

	sb.WriteString(` OR id IN (SELECT message_id FROM message_references WHERE ref_msg_id IN (` + placeholders(len(msgIDs)) + `)))`)
	args = append(args, stringArgs(msgIDs)...)

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreadRows(rows)
}

// threadBackwardQuery returns rows whose message_id appears in referencedMsgIDs
// and are not already in foundIDs.
func (r *MessageRepository) threadBackwardQuery(
	ctx context.Context,
	referencedMsgIDs map[string]bool,
	foundIDs map[int64]bool,
) ([]threadSeedRow, error) {
	refIDs := setToStringSlice(referencedMsgIDs)
	excluded := setToInt64Slice(foundIDs)

	var sb strings.Builder
	var args []any

	sb.WriteString(`SELECT id, message_id, in_reply_to, "references" FROM messages WHERE message_id IN (` + placeholders(len(refIDs)) + `)`)
	args = append(args, stringArgs(refIDs)...)

	if len(excluded) > 0 {
		sb.WriteString(` AND id NOT IN (` + placeholders(len(excluded)) + `)`)
		args = append(args, int64Args(excluded)...)
	}

	rows, err := r.db.QueryContext(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanThreadRows(rows)
}

func scanThreadRows(rows *sql.Rows) ([]threadSeedRow, error) {
	var out []threadSeedRow
	for rows.Next() {
		r, err := scanThreadSeedRow(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SnoozeMessage snoozes a message until the given time.
// Returns ErrSnoozeTimeTooSoon if until < now+60s, ErrForbiddenFolder if the message is in
// Sent/Drafts/Trash/Scheduled/Junk, ErrNotFound if the message does not exist.
func (r *MessageRepository) SnoozeMessage(ctx context.Context, id int64, until time.Time) (model.DBMessage, error) {
	until = until.UTC()
	if until.Before(time.Now().UTC().Add(60 * time.Second)) {
		return model.DBMessage{}, ErrSnoozeTimeTooSoon
	}

	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	if err != nil {
		return model.DBMessage{}, err
	}

	// Forbidden folders: Drafts=3, Sent=2, Trash=4, Junk=7, Scheduled=5.
	switch folderID {
	case 2, 3, 4, 5, 7:
		return model.DBMessage{}, ErrForbiddenFolder
	}

	untilStr := until.Format(time.RFC3339)

	if folderID != 6 {
		// First snooze.
		_, err = r.db.ExecContext(ctx, `
			UPDATE messages
			SET snooze_folder = folder_id, folder_id = 6, snoozed_until = ?
			WHERE id = ? AND folder_id != 6`,
			untilStr, id,
		)
	} else {
		// Re-snooze: preserve snooze_folder.
		_, err = r.db.ExecContext(ctx, `
			UPDATE messages SET snoozed_until = ? WHERE id = ? AND folder_id = 6`,
			untilStr, id,
		)
	}
	if err != nil {
		return model.DBMessage{}, err
	}

	return r.GetMessageDetail(ctx, id)
}

// CancelSnooze cancels an active snooze, returning the message to its original folder.
// Returns ErrForbiddenFolder if the message is not in the Snoozed folder.
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) CancelSnooze(ctx context.Context, id int64) (model.DBMessage, error) {
	// Check message exists.
	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	if err != nil {
		return model.DBMessage{}, err
	}

	res, err := r.db.ExecContext(ctx, `
		UPDATE messages
		SET folder_id = COALESCE(snooze_folder, 1),
		    snoozed_until = NULL,
		    snooze_folder = NULL,
		    read = 0
		WHERE id = ? AND folder_id = 6`,
		id,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrForbiddenFolder
	}

	return r.GetMessageDetail(ctx, id)
}

// MarkJunk moves the message to Junk (folder_id=7) and marks it as read.
// Returns ErrForbiddenFolder if the message is in Junk (7), Drafts (3), Scheduled (5), or Snoozed (6).
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) MarkJunk(ctx context.Context, id int64) (model.DBMessage, error) {
	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	if err != nil {
		return model.DBMessage{}, err
	}
	switch folderID {
	case 3, 5, 6, 7:
		return model.DBMessage{}, ErrForbiddenFolder
	}

	_, err = r.db.ExecContext(ctx, `UPDATE messages SET folder_id = 7, read = 1 WHERE id = ?`, id)
	if err != nil {
		return model.DBMessage{}, err
	}
	return r.GetMessageDetail(ctx, id)
}

// MarkNotJunk moves the message from Junk to Inbox and marks it unread.
// Returns ErrForbiddenFolder if the message is not in Junk (7).
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) MarkNotJunk(ctx context.Context, id int64) (model.DBMessage, error) {
	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return model.DBMessage{}, ErrNotFound
	}
	if err != nil {
		return model.DBMessage{}, err
	}
	if folderID != 7 {
		return model.DBMessage{}, ErrForbiddenFolder
	}

	_, err = r.db.ExecContext(ctx, `UPDATE messages SET folder_id = 1, read = 0 WHERE id = ?`, id)
	if err != nil {
		return model.DBMessage{}, err
	}
	return r.GetMessageDetail(ctx, id)
}

// searchTimeout bounds a single search query. Must stay below the HTTP
// server's WriteTimeout (see main.go) so the query is cancelled and a clean
// error returned before the connection write deadline trips.
const searchTimeout = 15 * time.Second

// snippetSourceLimit bounds how many leading characters of body_text are read
// per result row when building the highlighted excerpt. The FTS5 snippet()
// function re-reads and re-tokenizes the *entire* body of every returned row
// (the table uses content='messages', so the text is not stored in the index);
// for large messages that is pathologically slow. Building the excerpt in Go
// from a bounded prefix instead keeps the cost constant per row.
const snippetSourceLimit = 64 * 1024

// snippetContextTokens is the approximate number of whitespace/word tokens
// shown in a snippet, matching the old FTS5 snippet() token budget.
const snippetContextTokens = 15

// sanitizeFTSQuery escapes double quotes and wraps the input in outer quotes
// to produce a literal phrase match for SQLite FTS5.
func sanitizeFTSQuery(q string) string {
	escaped := strings.ReplaceAll(q, `"`, `""`)
	return `"` + escaped + `"`
}

// token is a word in a body of text together with its byte offsets.
type token struct {
	start, end int
	lower      string
}

// tokenizeText splits text into alphanumeric word tokens, approximating the
// FTS5 unicode61 tokenizer (split on non-alphanumeric, fold to lower case).
func tokenizeText(text string) []token {
	var tokens []token
	start := -1
	for i, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start < 0 {
				start = i
			}
			continue
		}
		if start >= 0 {
			tokens = append(tokens, token{start, i, strings.ToLower(text[start:i])})
			start = -1
		}
	}
	if start >= 0 {
		tokens = append(tokens, token{start, len(text), strings.ToLower(text[start:])})
	}
	return tokens
}

// buildSnippet produces a short highlighted excerpt of body around the first
// occurrence of a query term, wrapping matched terms in ** markers and adding
// … at truncated boundaries — mirroring the output of the FTS5 snippet()
// function it replaces, but computed in Go from a bounded prefix so the cost is
// independent of total message size.
func buildSnippet(body, query string) string {
	bodyTokens := tokenizeText(body)
	if len(bodyTokens) == 0 {
		return strings.TrimSpace(body)
	}

	queryTokens := tokenizeText(query)
	querySet := make(map[string]struct{}, len(queryTokens))
	for _, t := range queryTokens {
		querySet[t.lower] = struct{}{}
	}

	// Find the first body token that matches a query term.
	match := -1
	if len(querySet) > 0 {
		for i, t := range bodyTokens {
			if _, ok := querySet[t.lower]; ok {
				match = i
				break
			}
		}
	}

	// Center the window on the match (or start at the beginning if none found).
	lo, hi := 0, snippetContextTokens
	if match >= 0 {
		lo = max(match-snippetContextTokens/2, 0)
		hi = lo + snippetContextTokens
	}
	if hi > len(bodyTokens) {
		hi = len(bodyTokens)
	}

	var sb strings.Builder
	if lo > 0 {
		sb.WriteString("…")
	}
	for i := lo; i < hi; i++ {
		t := bodyTokens[i]
		if i > lo {
			sb.WriteString(body[bodyTokens[i-1].end:t.start])
		}
		if _, ok := querySet[t.lower]; ok {
			sb.WriteString("**")
			sb.WriteString(body[t.start:t.end])
			sb.WriteString("**")
		} else {
			sb.WriteString(body[t.start:t.end])
		}
	}
	if hi < len(bodyTokens) {
		sb.WriteString("…")
	}
	return sb.String()
}

// SearchSort selects the ordering of a search result page. It is an enum rather
// than a string so no caller can hand SearchMessages an ORDER BY fragment: the
// clause is chosen by the switch in orderBy below and never interpolated from
// input.
type SearchSort int

const (
	// SortRelevance is FTS5 ORDER BY rank, the default.
	SortRelevance SearchSort = iota
	// SortDateAsc orders by message date, oldest first.
	SortDateAsc
	// SortDateDesc orders by message date, newest first.
	SortDateDesc
)

// orderBy returns the ORDER BY clause for this sort.
//
// Both date orderings break ties on id so that paging is stable: rows with an
// identical date would otherwise come back in an unspecified order, which can
// differ between the LIMIT/OFFSET queries of two adjacent pages and so repeat or
// skip a message. Any total order does that job; ascending in both directions is
// chosen because it is the simplest thing to state and to mirror, and the demo
// backend mirrors it exactly.
//
// Note this makes the two date sorts stricter than the folder listing above,
// which is a bare ORDER BY m.date DESC and so has the tie problem this clause
// exists to avoid. That is a pre-existing gap, not a rule being followed here.
//
// Sorting m.date as text is chronological because every date is stored as UTC
// RFC 3339 — a fixed-width YYYY-MM-DDTHH:MM:SSZ — by every write path
// (InsertMessage and the three in draft_repo.go).
//
// The date orderings are not free, and idx_messages_date does not make them so:
// the FTS5 MATCH drives the query, so that index cannot be used to order it.
// EXPLAIN QUERY PLAN shows the date clauses adding USE TEMP B-TREE FOR ORDER BY
// where rank has none — every matching row is materialised and sorted before
// LIMIT/OFFSET, rather than streamed. On a large mailbox and a common term this
// is the ordering that reaches searchTimeout first, and the user sees a query
// that works under Relevance time out under Newest first.
func (s SearchSort) orderBy() string {
	switch s {
	case SortRelevance:
		return "rank"
	case SortDateAsc:
		return "m.date ASC, m.id ASC"
	case SortDateDesc:
		return "m.date DESC, m.id ASC"
	}
	// Unreachable: SearchSort is closed, and handler.searchSorts is what maps
	// the wire enum onto it — a value with no entry there is refused before it
	// reaches this method, so this branch cannot silently serve relevance for a
	// sort someone forgot to wire up. Relevance is nonetheless the least
	// surprising answer, being the endpoint's own default.
	return "rank"
}

// SearchMessages performs FTS5 phrase-match search with optional folder, date
// and address filters, ordered by sort.
//
// fromAddr and toAddr refine the result set by sender/recipient using the same
// rule as a filter's match_from/match_to (case-insensitive substring, toAddr
// matching either the To or the Cc header). instr() is used rather than LIKE so
// that % and _ stay literals, and unicode_lower() rather than SQLite's built-in
// lower() so that the folding covers non-ASCII too — see sqlfunc.go.
func (r *MessageRepository) SearchMessages(
	ctx context.Context,
	q string,
	folderID *int64,
	dateFrom, dateTo *time.Time,
	fromAddr, toAddr *string,
	sort SearchSort,
	limit, offset int,
) ([]oas.MessagesSearchGetOKItemsItem, int, error) {
	// Bound the query so a pathologically slow search fails fast with a clean
	// error instead of running past the HTTP server's WriteTimeout (which would
	// trip the connection write deadline mid-response and log a confusing
	// "superfluous response.WriteHeader call"). Kept comfortably below that
	// timeout to leave headroom for encoding and writing the response.
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()

	ftsQuery := sanitizeFTSQuery(q)

	// Build WHERE conditions (beyond the MATCH clause).
	var conditions []string
	var filterArgs []any
	conditions = append(conditions, "messages_fts MATCH ?")
	filterArgs = append(filterArgs, ftsQuery)

	if folderID != nil {
		conditions = append(conditions, "m.folder_id = ?")
		filterArgs = append(filterArgs, *folderID)
	} else {
		conditions = append(conditions, "m.folder_id NOT IN (3, 5, 7)")
	}
	if dateFrom != nil {
		conditions = append(conditions, "m.date >= ?")
		filterArgs = append(filterArgs, dateFrom.UTC().Format(time.RFC3339))
	}
	if dateTo != nil {
		conditions = append(conditions, "m.date < ?")
		filterArgs = append(filterArgs, dateTo.UTC().Format(time.RFC3339))
	}
	// The needle is folded here rather than in SQL: one strings.ToLower instead
	// of a per-row call, and it makes the equivalence with the Go filter rule
	// (strings.Contains of one ToLower in another) plain.
	if fromAddr != nil {
		conditions = append(conditions, "instr(unicode_lower(m.from_addr), ?) > 0")
		filterArgs = append(filterArgs, strings.ToLower(*fromAddr))
	}
	if toAddr != nil {
		conditions = append(conditions,
			"(instr(unicode_lower(m.to_addr), ?) > 0 OR instr(unicode_lower(m.cc_addr), ?) > 0)")
		lo := strings.ToLower(*toAddr)
		filterArgs = append(filterArgs, lo, lo)
	}

	whereClause := strings.Join(conditions, " AND ")

	// Count query.
	var total int
	countSQL := `SELECT COUNT(*) FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id WHERE ` + whereClause
	if err := r.db.QueryRowContext(ctx, countSQL, filterArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Main query with snippet.
	// Read a bounded prefix of body_text and build the highlighted excerpt in
	// Go (see buildSnippet). Calling FTS5 snippet() here would re-tokenize the
	// full body of every returned row, which is catastrophically slow for large
	// messages and can exceed the request timeout.
	mainSQL := `SELECT m.id, m.folder_id, m.message_id, m.from_addr, m.to_addr,
		m.subject, m.date, m.read, m.flagged, m.has_attachments, m.send_failure_count,
		m.send_at, m.snoozed_until, m.created_at,
		substr(m.body_text, 1, ` + fmt.Sprint(snippetSourceLimit) + `)
	FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id
	WHERE ` + whereClause + ` ORDER BY ` + sort.orderBy() + ` LIMIT ? OFFSET ?`

	mainArgs := append(filterArgs, limit, offset)
	rows, err := r.db.QueryContext(ctx, mainSQL, mainArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []oas.MessagesSearchGetOKItemsItem
	for rows.Next() {
		var (
			item            oas.MessagesSearchGetOKItemsItem
			messageID       sql.NullString
			dateStr         string
			readInt         int
			flaggedInt      int
			hasAttInt       int
			sendFailCnt     int
			sendAtStr       sql.NullString
			snoozedUntilStr sql.NullString
			createdAtStr    string
			bodyPrefix      sql.NullString
		)
		if err := rows.Scan(
			&item.ID, &item.FolderID, &messageID, &item.FromAddr, &item.ToAddr,
			&item.Subject, &dateStr, &readInt, &flaggedInt, &hasAttInt, &sendFailCnt,
			&sendAtStr, &snoozedUntilStr, &createdAtStr,
			&bodyPrefix,
		); err != nil {
			return nil, 0, err
		}
		var err error
		if item.SendAt, err = parseNilDateTime(sendAtStr, "send_at"); err != nil {
			return nil, 0, err
		}
		if item.SnoozedUntil, err = parseNilDateTime(snoozedUntilStr, "snoozed_until"); err != nil {
			return nil, 0, err
		}
		if messageID.Valid {
			item.MessageID = oas.NewNilString(messageID.String)
		} else {
			item.MessageID.SetToNull()
		}
		t, err := time.Parse(time.RFC3339, dateStr)
		if err != nil {
			return nil, 0, fmt.Errorf("parse date %q: %w", dateStr, err)
		}
		item.Date = t
		item.Read = readInt != 0
		item.Flagged = flaggedInt != 0
		item.HasAttachments = hasAttInt != 0
		item.SendFailed = sendFailCnt > 0
		cat, err := time.Parse(time.RFC3339, createdAtStr)
		if err != nil {
			return nil, 0, fmt.Errorf("parse created_at %q: %w", createdAtStr, err)
		}
		item.CreatedAt = cat
		item.Snippet = html.EscapeString(buildSnippet(bodyPrefix.String, q))
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if items == nil {
		items = []oas.MessagesSearchGetOKItemsItem{}
	}
	return items, total, nil
}
