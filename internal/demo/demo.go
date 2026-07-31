// Package demo seeds a MyMail database with a curated set of sample messages,
// contacts, and an identity, and exports that same content for the browser demo.
//
// There is exactly one definition of the demo dataset (content.go). The `-demo`
// flag writes it into SQLite; `-demo-server` and `-demo-bundle` hand it to the
// in-browser backend as demo-data.json (bundle.go). Neither re-states the
// content, so the two demos can never drift apart.
package demo

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/mikaelstaldal/mymail/internal/repository"
)

// Run populates db with the demo dataset. The database must already carry the
// schema and the built-in folders.
//
// Each invocation adds a fresh set of messages (unique Message-IDs) so it can
// be run repeatedly to grow the dataset; the identity and the contacts are
// seeded only when they are not there already.
func Run(ctx context.Context, db *sql.DB, out io.Writer) error {
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Unique 8-char suffix so message_ids never collide across runs.
	runID := uuid.New().String()[:8]
	c := buildContent(now, runID)

	// Seed a demo identity only when none exists yet.
	var identCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities`).Scan(&identCount); err != nil {
		return fmt.Errorf("check identities: %w", err)
	}
	if identCount == 0 {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO identities (name, address, is_default, position) VALUES (?, ?, 1, 0)`,
			"Demo User", "demo@example.com",
		); err != nil {
			return fmt.Errorf("insert identity: %w", err)
		}
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	insertedIDs := make(map[string]int64)

	for _, m := range c.msgs {
		var inReplyToVal, refsVal, rawVal any
		if m.inReplyTo != "" {
			inReplyToVal = m.inReplyTo
		}
		if m.references != "" {
			refsVal = m.references
		}
		if !m.isDraft {
			rawVal = rawMessage(m)
		}

		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO messages (
				folder_id, message_id, in_reply_to, "references",
				from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
				date, body_text, body_html, raw, read, flagged,
				has_attachments, has_external_images,
				send_failure_count, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?)`,
			m.folderID, m.msgID, inReplyToVal, refsVal,
			m.from, m.to, m.cc, "", "", m.subject,
			m.date.Format(time.RFC3339), m.bodyText, m.bodyHTML, rawVal,
			boolInt(m.read), boolInt(m.flagged),
			nowStr, nowStr,
		)
		if err != nil {
			return fmt.Errorf("insert %q: %w", m.subject, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if id, err := res.LastInsertId(); err == nil {
				insertedIDs[m.msgID] = id
			}
		}
	}

	// Attachments (the insert trigger sets has_attachments=1 on the message).
	for _, a := range c.attachments {
		rowID := insertedIDs[a.msgID]
		if rowID == 0 {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO attachments (message_id, filename, content_type, size, data) VALUES (?, ?, ?, ?, ?)`,
			rowID, a.filename, a.contentType, len(a.data), a.data,
		); err != nil {
			return fmt.Errorf("insert attachment: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	contactRepo := repository.NewContactRepository(db)
	for _, ct := range c.contacts {
		if err := contactRepo.UpsertContact(ctx, ct.address, ct.name); err != nil {
			return fmt.Errorf("upsert contact %s: %w", ct.address, err)
		}
	}

	_, _ = fmt.Fprintln(out, "Demo data loaded successfully.")
	return nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
