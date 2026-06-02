package lda

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	sqlite "modernc.org/sqlite"

	oas "github.com/mikaelstaldal/mymail/internal/api"
	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// sqliteBusy is the primary SQLITE_BUSY error code (low byte).
// Extended variants (SQLITE_BUSY_RECOVERY, SQLITE_BUSY_SNAPSHOT, …) share this low byte.
const sqliteBusy = 5

// MaxMessageBytes caps the size of a single inbound RFC 5322 message accepted by
// the LDA (stdin or socket). The whole raw message is held in memory and stored
// in the messages.raw BLOB, so an unbounded read would let a hostile or
// misconfigured MTA exhaust memory / bloat the database. Messages exceeding this
// limit are rejected as a permanent parse failure (no point retrying).
const MaxMessageBytes = 64 << 20 // 64 MiB

var errBusyTimeout = errors.New("SQLITE_BUSY timed out after 30s")

// Run terminates the process via os.Exit: 0 success/duplicate/drop, 1 parse failure, 75 transient error.
func Run(db *sql.DB, rawBytes []byte) {
	os.Exit(runCore(db, rawBytes))
}

func runCore(db *sql.DB, rawBytes []byte) int {
	ctx := context.Background()

	pm, err := ParseMessage(rawBytes)
	if err != nil {
		log.Printf("lda: parse failure: %v", err)
		return 1
	}

	if pm.MessageID != nil {
		var exists bool
		if err := withRetry(func() error {
			return db.QueryRowContext(ctx,
				`SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`,
				*pm.MessageID,
			).Scan(&exists)
		}); err != nil {
			log.Printf("lda: duplicate check: %v", err)
			return 75
		}
		if exists {
			log.Printf("lda: skip duplicate message_id=%s", *pm.MessageID)
			return 0
		}
	}

	if pm.MessageID == nil {
		domain := "localhost"
		if pm.ToAddr != "" {
			if addrs, err := mail.ParseAddressList(pm.ToAddr); err == nil && len(addrs) > 0 {
				if at := strings.LastIndex(addrs[0].Address, "@"); at >= 0 {
					domain = addrs[0].Address[at+1:]
				}
			}
		}
		generated := fmt.Sprintf("%s@%s", uuid.New().String(), domain)
		pm.MessageID = &generated
	}

	spamRepo := repository.NewSpamFilterRepository(db)
	var spamSettings oas.SpamFilterSettings
	if err := withRetry(func() error {
		var e error
		spamSettings, e = spamRepo.GetSpamFilterSettings(ctx)
		return e
	}); err != nil {
		log.Printf("lda: load spam settings: %v", err)
		return 75
	}

	folderID := 1
	if spamSettings.Enabled && detectSpam(mail.Header(pm.Headers), spamSettings) {
		folderID = 7
	}

	filterRepo := repository.NewFilterRepository(db)
	var filters []oas.Filter
	if err := withRetry(func() error {
		var e error
		filters, e = filterRepo.ListFilters(ctx)
		return e
	}); err != nil {
		log.Printf("lda: load filters: %v", err)
		return 75
	}

	markRead := false
	for _, f := range filters {
		if !filterMatches(pm, f) {
			continue
		}
		switch f.Action {
		case oas.FilterActionDrop:
			log.Printf("lda: dropped from=%s message_id=%s", pm.FromAddr, *pm.MessageID)
			return 0
		case oas.FilterActionMove:
			if fid, ok := f.FolderID.Get(); ok {
				var folderExists bool
				if err := withRetry(func() error {
					return db.QueryRowContext(ctx,
						`SELECT EXISTS(SELECT 1 FROM folders WHERE id = ?)`, fid,
					).Scan(&folderExists)
				}); err != nil {
					log.Printf("lda: check folder: %v", err)
					return 75
				}
				if folderExists {
					folderID = fid
				}
			}
		case oas.FilterActionTrash:
			folderID = 4
		case oas.FilterActionMarkRead:
			markRead = true
		}
		if f.Stop {
			break
		}
	}

	date := time.Now().UTC()
	if pm.Date != nil {
		date = pm.Date.UTC()
	}

	var newID int64
	if err := withRetry(func() error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback() //nolint:errcheck

		now := time.Now().UTC().Format(time.RFC3339)
		dateStr := date.Format(time.RFC3339)

		var inReplyToVal any
		if pm.InReplyTo != nil {
			inReplyToVal = *pm.InReplyTo
		}
		var refsVal any
		if len(pm.References) > 0 {
			refsVal = strings.Join(pm.References, "\n")
		}

		res, err := tx.ExecContext(ctx, `
			INSERT OR IGNORE INTO messages (
				folder_id, message_id, in_reply_to, "references",
				from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject,
				date, body_text, body_html, raw, read, flagged,
				has_attachments, has_external_images,
				send_failure_count, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?, ?, 0, ?, ?)`,
			folderID, *pm.MessageID, inReplyToVal, refsVal,
			pm.FromAddr, pm.ToAddr, pm.CcAddr, pm.BccAddr, pm.ReplyToAddr, pm.Subject,
			dateStr, pm.BodyText, pm.BodyHTML, rawBytes, boolInt(markRead),
			boolInt(len(pm.Attachments) > 0), boolInt(pm.HasExternalImages),
			now, now,
		)
		if err != nil {
			return err
		}

		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			newID = 0
			return nil
		}

		msgID, err := res.LastInsertId()
		if err != nil {
			return err
		}

		for _, ref := range pm.References {
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO message_references (message_id, ref_msg_id) VALUES (?, ?)`,
				msgID, ref,
			); err != nil {
				return err
			}
		}

		for _, att := range pm.Attachments {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO attachments (message_id, filename, content_type, size, data)
				 VALUES (?, ?, ?, ?, ?)`,
				msgID, att.Filename, att.ContentType, att.Size, att.Data,
			); err != nil {
				return err
			}
		}

		if err := tx.Commit(); err != nil {
			return err
		}
		newID = msgID
		return nil
	}); err != nil {
		log.Printf("lda: store message: %v", err)
		return 75
	}

	if newID == 0 {
		return 0
	}

	if pm.FromAddr != "" {
		addr, name := parseFromAddr(pm.FromAddr)
		if addr != "" {
			contactRepo := repository.NewContactRepository(db)
			if err := withRetry(func() error {
				return contactRepo.UpsertContact(ctx, addr, name)
			}); err != nil {
				log.Printf("lda: upsert contact: %v", err)
				return 75
			}
		}
	}

	return 0
}

func detectSpam(headers mail.Header, settings oas.SpamFilterSettings) bool {
	if flag := strings.TrimSpace(headers.Get("X-Spam-Flag")); strings.EqualFold(flag, "YES") {
		return true
	}

	if status := strings.TrimLeftFunc(headers.Get("X-Spam-Status"), isASCIISpace); len(status) >= 3 {
		if strings.EqualFold(status[:3], "Yes") && (len(status) == 3 || !isASCIILetter(status[3])) {
			return true
		}
	}

	if hdr := strings.TrimSpace(headers.Get(settings.ScoreHeader)); hdr != "" {
		if score, err := strconv.ParseFloat(hdr, 64); err == nil && score >= settings.ScoreThreshold {
			return true
		}
	}

	return false
}

// match_to checks both ToAddr and CcAddr; all non-empty criteria are ANDed.
func filterMatches(pm *model.ParsedMessage, f oas.Filter) bool {
	if f.MatchFrom != "" {
		if !strings.Contains(strings.ToLower(pm.FromAddr), strings.ToLower(f.MatchFrom)) {
			return false
		}
	}
	if f.MatchTo != "" {
		lo := strings.ToLower(f.MatchTo)
		if !strings.Contains(strings.ToLower(pm.ToAddr), lo) &&
			!strings.Contains(strings.ToLower(pm.CcAddr), lo) {
			return false
		}
	}
	if f.MatchSubject != "" {
		if !strings.Contains(strings.ToLower(pm.Subject), strings.ToLower(f.MatchSubject)) {
			return false
		}
	}
	return true
}

func parseFromAddr(fromAddr string) (address, name string) {
	addrs, err := mail.ParseAddressList(fromAddr)
	if err != nil || len(addrs) == 0 {
		return "", ""
	}
	return addrs[0].Address, addrs[0].Name
}

func withRetry(fn func() error) error {
	const maxWait = 30 * time.Second
	delay := 50 * time.Millisecond
	deadline := time.Now().Add(maxWait)

	for {
		err := fn()
		if err == nil || !isBusyErr(err) {
			return err
		}
		if !time.Now().Before(deadline) {
			return errBusyTimeout
		}
		time.Sleep(delay)
		delay *= 2
		if delay > 5*time.Second {
			delay = 5 * time.Second
		}
	}
}

func isBusyErr(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code()&0xFF == sqliteBusy
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isASCIISpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\r' || r == '\n'
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}
