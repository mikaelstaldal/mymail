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
	m.subject, m.date, m.read, m.flagged, m.has_attachments, m.send_failure_count, m.created_at`

// scanMessageSummary scans a row with summaryColumns into oas.MessageSummary.
func scanMessageSummary(scan func(...any) error) (oas.MessageSummary, error) {
	var (
		s            oas.MessageSummary
		messageID    sql.NullString
		dateStr      string
		readInt      int
		flaggedInt   int
		hasAttInt    int
		sendFailCnt  int
		createdAtStr string
	)
	if err := scan(&s.ID, &s.FolderID, &messageID, &s.FromAddr, &s.ToAddr,
		&s.Subject, &dateStr, &readInt, &flaggedInt, &hasAttInt, &sendFailCnt, &createdAtStr); err != nil {
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
		m              model.DBMessage
		sendAtStr      sql.NullString
		snoozedUntilStr sql.NullString
		dateStr        string
		createdAtStr   string
		updatedAtStr   string
		readInt        int
		flaggedInt     int
		hasAttInt      int
		hasExtImgInt   int
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

	if sendAtStr.Valid && sendAtStr.String != "" {
		st, err := time.Parse(time.RFC3339, sendAtStr.String)
		if err != nil {
			return model.DBMessage{}, fmt.Errorf("parse send_at %q: %w", sendAtStr.String, err)
		}
		m.SendAt = sql.NullTime{Time: st, Valid: true}
	}

	if snoozedUntilStr.Valid && snoozedUntilStr.String != "" {
		su, err := time.Parse(time.RFC3339, snoozedUntilStr.String)
		if err != nil {
			return model.DBMessage{}, fmt.Errorf("parse snoozed_until %q: %w", snoozedUntilStr.String, err)
		}
		m.SnoozedUntil = sql.NullTime{Time: su, Valid: true}
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

// ListMessages returns paginated messages in a folder ordered by date DESC, plus total count.
func (r *MessageRepository) ListMessages(ctx context.Context, folderID int64, limit, offset int) ([]oas.MessageSummary, int, error) {
	var total int
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages m WHERE m.folder_id = ?`, folderID,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := r.db.QueryContext(ctx,
		`SELECT `+summaryColumns+` FROM messages m WHERE m.folder_id = ? ORDER BY m.date DESC LIMIT ? OFFSET ?`,
		folderID, limit, offset,
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

	return newID, tx.Commit()
}

// UpdateMessage applies a PATCH update on the provided fields and returns the updated row.
// Allowed keys: "folder_id" (int64), "read" (bool), "flagged" (bool).
func (r *MessageRepository) UpdateMessage(ctx context.Context, id int64, fields map[string]any) (model.DBMessage, error) {
	if len(fields) == 0 {
		return r.GetMessage(ctx, id)
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
		return r.GetMessage(ctx, id)
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
	return r.GetMessage(ctx, id)
}

// BulkUpdateMessages sets read and/or flagged on a set of messages. Returns count changed.
func (r *MessageRepository) BulkUpdateMessages(ctx context.Context, ids []int64, read *bool, flagged *bool) (int, error) {
	if len(ids) > 1000 {
		return 0, ErrTooManyIDs
	}
	if len(ids) == 0 || (read == nil && flagged == nil) {
		return 0, nil
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
// in which case it is permanently deleted.
func (r *MessageRepository) DeleteMessage(ctx context.Context, id int64) error {
	var folderID int64
	err := r.db.QueryRowContext(ctx, `SELECT folder_id FROM messages WHERE id = ?`, id).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}

	if folderID == 4 || folderID == 7 {
		_, err = r.db.ExecContext(ctx, `DELETE FROM messages WHERE id = ?`, id)
	} else {
		_, err = r.db.ExecContext(ctx, `UPDATE messages SET folder_id = 4 WHERE id = ?`, id)
	}
	return err
}

// BulkDeleteMessages applies delete logic for a batch of messages (all-or-nothing).
// Returns ErrNotFound if any ID is missing; ErrTooManyIDs if len > 1000.
func (r *MessageRepository) BulkDeleteMessages(ctx context.Context, ids []int64) error {
	if len(ids) > 1000 {
		return ErrTooManyIDs
	}
	if len(ids) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Verify all IDs exist.
	rows, err := tx.QueryContext(ctx,
		`SELECT id FROM messages WHERE id IN (`+placeholders(len(ids))+`)`,
		int64Args(ids)...,
	)
	if err != nil {
		return err
	}
	found := make(map[int64]bool, len(ids))
	for rows.Next() {
		var rid int64
		if err := rows.Scan(&rid); err != nil {
			rows.Close()
			return err
		}
		found[rid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if !found[id] {
			return ErrNotFound
		}
	}

	// Permanently delete those in Trash (4) or Junk (7).
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM messages WHERE id IN (`+placeholders(len(ids))+`) AND folder_id IN (4, 7)`,
		int64Args(ids)...,
	); err != nil {
		return err
	}

	// Move the rest to Trash.
	if _, err := tx.ExecContext(ctx,
		`UPDATE messages SET folder_id = 4 WHERE id IN (`+placeholders(len(ids))+`) AND folder_id NOT IN (4, 7)`,
		int64Args(ids)...,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// MoveMessages moves all listed messages to the target folder.
// Returns ErrNotFound if any ID is missing; ErrTooManyIDs if len > 1000.
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
		`SELECT id FROM messages WHERE id IN (`+placeholders(len(ids))+`)`,
		int64Args(ids)...,
	)
	if err != nil {
		return 0, err
	}
	found := make(map[int64]bool, len(ids))
	for rows.Next() {
		var rid int64
		if err := rows.Scan(&rid); err != nil {
			rows.Close()
			return 0, err
		}
		found[rid] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if !found[id] {
			return 0, ErrNotFound
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE messages SET folder_id = ? WHERE id IN (`+placeholders(len(ids))+`)`,
		append([]any{folderID}, int64Args(ids)...)...,
	)
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
}

// scanThreadSeedRow scans id, message_id, in_reply_to, "references".
func scanThreadSeedRow(scan func(...any) error) (threadSeedRow, error) {
	var r threadSeedRow
	return r, scan(&r.id, &r.messageID, &r.inReplyTo, &r.refs)
}

var subjectPrefixRe = regexp.MustCompile(`(?i)^[ \t]*(re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]+`)

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
	// Fetch seed row including subject for the subject-based fallback.
	var seed threadSeedRow
	err := r.db.QueryRowContext(ctx,
		`SELECT id, message_id, in_reply_to, "references", subject FROM messages WHERE id = ?`, id,
	).Scan(&seed.id, &seed.messageID, &seed.inReplyTo, &seed.refs, &seed.subject)
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
	if len(foundIDs) == 1 {
		normSubject := normalizeSubject(seed.subject)

		if normSubject != "" {
			rows, err := r.db.QueryContext(ctx,
				`SELECT id, message_id, in_reply_to, "references", subject FROM messages WHERE id != ? ORDER BY date ASC LIMIT 999`,
				id,
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

	for _, mid := range msgIDs {
		sb.WriteString(` OR (char(10) || COALESCE("references", '') || char(10)) LIKE ('%' || char(10) || ? || char(10) || '%')`)
		args = append(args, mid)
	}
	sb.WriteString(`)`)

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

	return r.GetMessage(ctx, id)
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

	return r.GetMessage(ctx, id)
}

// MarkJunk moves the message to Junk (folder_id=7) and marks it as read.
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) MarkJunk(ctx context.Context, id int64) (model.DBMessage, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE messages SET folder_id = 7, read = 1 WHERE id = ?`, id,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrNotFound
	}
	return r.GetMessage(ctx, id)
}

// MarkNotJunk moves the message to Inbox (folder_id=1) and marks it unread.
// Returns ErrNotFound if the message does not exist.
func (r *MessageRepository) MarkNotJunk(ctx context.Context, id int64) (model.DBMessage, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE messages SET folder_id = 1, read = 0 WHERE id = ?`, id,
	)
	if err != nil {
		return model.DBMessage{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return model.DBMessage{}, ErrNotFound
	}
	return r.GetMessage(ctx, id)
}

// sanitizeFTSQuery escapes double quotes and wraps the input in outer quotes
// to produce a literal phrase match for SQLite FTS5.
func sanitizeFTSQuery(q string) string {
	escaped := strings.ReplaceAll(q, `"`, `""`)
	return `"` + escaped + `"`
}

// SearchMessages performs FTS5 phrase-match search with optional folder and date filters.
func (r *MessageRepository) SearchMessages(
	ctx context.Context,
	q string,
	folderID *int64,
	dateFrom, dateTo *time.Time,
	limit, offset int,
) ([]oas.MessagesSearchGetOKItemsItem, int, error) {
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

	whereClause := strings.Join(conditions, " AND ")

	// Count query.
	var total int
	countSQL := `SELECT COUNT(*) FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id WHERE ` + whereClause
	if err := r.db.QueryRowContext(ctx, countSQL, filterArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}

	// Main query with snippet.
	mainSQL := `SELECT m.id, m.folder_id, m.message_id, m.from_addr, m.to_addr,
		m.subject, m.date, m.read, m.flagged, m.has_attachments, m.send_failure_count, m.created_at,
		snippet(messages_fts, 4, '**', '**', '…', 15)
	FROM messages_fts JOIN messages m ON messages_fts.rowid = m.id
	WHERE ` + whereClause + ` ORDER BY rank LIMIT ? OFFSET ?`

	mainArgs := append(filterArgs, limit, offset)
	rows, err := r.db.QueryContext(ctx, mainSQL, mainArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var items []oas.MessagesSearchGetOKItemsItem
	for rows.Next() {
		var (
			item         oas.MessagesSearchGetOKItemsItem
			messageID    sql.NullString
			dateStr      string
			readInt      int
			flaggedInt   int
			hasAttInt    int
			sendFailCnt  int
			createdAtStr string
			snippet      string
		)
		if err := rows.Scan(
			&item.ID, &item.FolderID, &messageID, &item.FromAddr, &item.ToAddr,
			&item.Subject, &dateStr, &readInt, &flaggedInt, &hasAttInt, &sendFailCnt, &createdAtStr,
			&snippet,
		); err != nil {
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
		item.Snippet = html.EscapeString(snippet)
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
