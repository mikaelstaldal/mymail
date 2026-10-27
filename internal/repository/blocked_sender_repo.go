package repository

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"mime"
	"net/mail"
	"strings"
	"time"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/phishing"
	"github.com/mikaelstaldal/mymail/internal/senderauth"
	"golang.org/x/net/html/charset"
)

// ASCIILower preserves non-ASCII lookalikes, matching sender filter comparisons.
func ASCIILower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}

// SenderAddress accepts exactly one mailbox, as match_from does. Repeated From
// fields are stored as multiple mailboxes or an intentionally unparseable string.
func SenderAddress(from string) string {
	addresses, err := mail.ParseAddressList(from)
	if err != nil || len(addresses) != 1 {
		return ""
	}
	return ASCIILower(addresses[0].Address)
}

type BlockedSenderRepository struct{ db *sql.DB }

func NewBlockedSenderRepository(db *sql.DB) *BlockedSenderRepository {
	return &BlockedSenderRepository{db: db}
}

const blockedSelect = `SELECT id, address, created_at, source_message_id FROM blocked_senders`

func scanBlocked(scan func(...any) error) (oas.BlockedSender, error) {
	var b oas.BlockedSender
	var created string
	var source sql.NullInt64
	if err := scan(&b.ID, &b.Address, &created, &source); err != nil {
		return b, err
	}
	var err error
	b.CreatedAt, err = time.Parse(time.RFC3339, created)
	if source.Valid {
		b.SourceMessageID.SetTo(int(source.Int64))
	} else {
		b.SourceMessageID.SetToNull()
	}
	return b, err
}

func (r *BlockedSenderRepository) List(ctx context.Context, q *string, limit, offset int) ([]oas.BlockedSender, int, error) {
	needle := ""
	if q != nil {
		needle = ASCIILower(*q)
	}
	const where = ` WHERE instr(address, ?) > 0`
	var total int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM blocked_senders`+where, needle).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := r.db.QueryContext(ctx, blockedSelect+where+` ORDER BY address LIMIT ? OFFSET ?`, needle, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	items := []oas.BlockedSender{}
	for rows.Next() {
		b, err := scanBlocked(rows.Scan)
		if err != nil {
			return nil, 0, err
		}
		items = append(items, b)
	}
	return items, total, rows.Err()
}

// addBlocked runs under a writer transaction, preserving the original source on duplicates.
func addBlocked(ctx context.Context, tx *sql.Tx, address string, source *int64) (oas.BlockedSender, bool, error) {
	res, err := tx.ExecContext(ctx, `INSERT INTO blocked_senders(address, created_at, source_message_id)
 VALUES (?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), ?) ON CONFLICT(address) DO NOTHING`, address, source)
	if err != nil {
		return oas.BlockedSender{}, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return oas.BlockedSender{}, false, err
	}
	b, err := scanBlocked(tx.QueryRowContext(ctx, blockedSelect+` WHERE address = ?`, address).Scan)
	return b, n > 0, err
}

func ownAddress(ctx context.Context, tx *sql.Tx, address string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT address FROM identities`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var own string
		if err := rows.Scan(&own); err != nil {
			return false, err
		}
		if phishing.FoldAddress(own) == phishing.FoldAddress(address) {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Add validates a manually entered address and refuses the user's identities.
// The boolean distinguishes creation (201) from an existing entry (200).
func (r *BlockedSenderRepository) Add(ctx context.Context, address string, source *int64) (oas.BlockedSender, bool, error) {
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed.Name != "" || len([]rune(address)) > 254 {
		return oas.BlockedSender{}, false, ErrInvalidAddress
	}
	address = ASCIILower(parsed.Address)
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return oas.BlockedSender{}, false, err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `UPDATE blocked_senders SET id = id WHERE 0`); err != nil {
		return oas.BlockedSender{}, false, err
	}
	own, err := ownAddress(ctx, tx, address)
	if err != nil {
		return oas.BlockedSender{}, false, err
	}
	if own {
		return oas.BlockedSender{}, false, ErrInvalidAddress
	}
	b, created, err := addBlocked(ctx, tx, address, source)
	if err != nil {
		return b, false, err
	}
	return b, created, tx.Commit()
}

func (r *BlockedSenderRepository) Delete(ctx context.Context, id int64) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM blocked_senders WHERE id = ?`, id)
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
func (r *BlockedSenderRepository) DeleteByAddress(ctx context.Context, address string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM blocked_senders WHERE address = ?`, ASCIILower(address))
	return err
}
func (r *BlockedSenderRepository) IsBlocked(ctx context.Context, address string) (bool, error) {
	var found bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM blocked_senders WHERE address = ?)`, ASCIILower(address)).Scan(&found)
	return found, err
}

// markJunk changes the message and its sender's blocked status atomically.
func (r *MessageRepository) markJunk(ctx context.Context, id int64, junk bool) (string, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET id = id WHERE 0`); err != nil {
		return "", err
	}
	var folder int
	var from string
	var raw []byte
	err = tx.QueryRowContext(ctx, `SELECT folder_id, from_addr, raw FROM messages WHERE id = ?`, id).Scan(&folder, &from, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if junk && (folder == 3 || folder == 5 || folder == 6 || folder == 7) || !junk && folder != 7 {
		return "", ErrForbiddenFolder
	}
	target, read := 1, 0
	if junk {
		target, read = 7, 1
	}
	if _, err := tx.ExecContext(ctx, `UPDATE messages SET folder_id = ?, read = ? WHERE id = ?`, target, read, id); err != nil {
		return "", err
	}
	address := SenderAddress(from)
	if len(raw) > 0 {
		address = ""
		if message, err := mail.ReadMessage(bytes.NewReader(raw)); err == nil {
			decoder := &mime.WordDecoder{CharsetReader: func(label string, input io.Reader) (io.Reader, error) { return charset.NewReaderLabel(label, input) }}
			address = ASCIILower(senderauth.SingleFrom(message.Header, decoder))
		}
	}
	changed := false
	if address != "" {
		if junk {
			own, err := ownAddress(ctx, tx, address)
			if err != nil {
				return "", err
			}
			if !own {
				_, changed, err = addBlocked(ctx, tx, address, &id)
				if err != nil {
					return "", err
				}
			}
		} else {
			res, err := tx.ExecContext(ctx, `DELETE FROM blocked_senders WHERE address = ?`, address)
			if err != nil {
				return "", err
			}
			n, err := res.RowsAffected()
			if err != nil {
				return "", err
			}
			changed = n > 0
		}
	}
	if !changed {
		address = ""
	}
	return address, tx.Commit()
}

func (r *MessageRepository) MarkJunkWithSender(ctx context.Context, id int64, junk bool) (int, string, error) {
	address, err := r.markJunk(ctx, id, junk)
	folder := 1
	if junk {
		folder = 7
	}
	return folder, address, err
}

// BlockedSenders shares the message repository's database.
func (r *MessageRepository) BlockedSenders() *BlockedSenderRepository {
	return NewBlockedSenderRepository(r.db)
}

// removeIdentityBlocks preserves the own-identity exclusion when an address is
// added or changed later. Identity equivalence uses the identity storage fold;
// the list itself still uses exact ASCII-folded sender comparisons.
func removeIdentityBlocks(ctx context.Context, tx *sql.Tx, address string) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, address FROM blocked_senders`)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var blocked string
		if err := rows.Scan(&id, &blocked); err != nil {
			rows.Close()
			return err
		}
		if phishing.FoldAddress(blocked) == address {
			ids = append(ids, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := tx.ExecContext(ctx, `DELETE FROM blocked_senders WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}
