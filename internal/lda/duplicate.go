package lda

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"io"
	"net/mail"
	"net/textproto"
)

// deliveryFingerprint excludes transport headers which change on redelivery.
// Length-prefix fields so arbitrary header/body bytes cannot alias boundaries.
func deliveryFingerprint(raw []byte) [32]byte {
	h := sha256.New()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return sha256.Sum256(raw)
	}
	for _, name := range []string{"From", "To", "Cc", "Bcc", "Date", "Subject", "Reply-To", "Sender", "DKIM-Signature", "MIME-Version", "Content-Type", "Content-Transfer-Encoding", "In-Reply-To", "References", "List-ID", "List-Post", "List-Unsubscribe", "List-Unsubscribe-Post", "List-Help", "List-Subscribe", "List-Owner", "List-Archive"} {
		values := msg.Header[textproto.CanonicalMIMEHeaderKey(name)]
		_ = binary.Write(h, binary.BigEndian, uint64(len(values)))
		for _, value := range values {
			_ = binary.Write(h, binary.BigEndian, uint64(len(value)))
			_, _ = io.WriteString(h, value)
		}
	}
	_, _ = io.Copy(h, msg.Body)
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

type duplicateQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// New rows use indexed fingerprints. Only legacy rows (at most two per ID
// before v14) require reading raw content; no whole-store backfill is needed.
func isDuplicate(ctx context.Context, q duplicateQuerier, id *string, raw []byte, receivedOnly bool) (bool, error) {
	if id == nil {
		return false, nil
	}
	fingerprint := deliveryFingerprint(raw)
	// Separate indexed probes keep both receipt and database-wide import bounded
	// by matching fingerprints, even with many hostile collisions on one ID.
	suffix := ""
	if receivedOnly {
		suffix = " AND outgoing = 0"
	}
	query := `SELECT content_fingerprint, NULL FROM messages
      WHERE message_id = ? AND content_fingerprint = ?` + suffix + `
      UNION ALL SELECT NULL, raw FROM messages
      WHERE message_id = ? AND content_fingerprint IS NULL AND raw IS NOT NULL` + suffix
	rows, err := q.QueryContext(ctx, query, *id, fingerprint[:], *id)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var stored, digest []byte
		if err := rows.Scan(&digest, &stored); err != nil {
			return false, err
		}
		if bytes.Equal(digest, fingerprint[:]) || deliveryFingerprint(stored) == fingerprint {
			return true, nil
		}
	}
	return false, rows.Err()
}
