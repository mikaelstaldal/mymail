package service

import (
	"context"
	"database/sql"
	"log"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/mikaelstaldal/mymail/internal/model"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// Scheduler runs periodic background tasks: deferred sends and snooze expiry.
type Scheduler struct {
	mu           sync.Mutex
	db           *sql.DB
	sendmailPath string
	contactRepo  *repository.ContactRepository
	draftRepo    *repository.DraftRepository
	messageRepo  *repository.MessageRepository
	identityRepo *repository.IdentityRepository
}

// NewScheduler creates a Scheduler backed by db.
func NewScheduler(db *sql.DB, sendmailPath string, contactRepo *repository.ContactRepository) *Scheduler {
	return &Scheduler{
		db:           db,
		sendmailPath: sendmailPath,
		contactRepo:  contactRepo,
		draftRepo:    repository.NewDraftRepository(db),
		messageRepo:  repository.NewMessageRepository(db),
		identityRepo: repository.NewIdentityRepository(db),
	}
}

// Start launches the background scheduler goroutine. Stops when ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !s.mu.TryLock() {
					continue
				}
				s.tick(ctx)
				s.mu.Unlock()
			}
		}
	}()
}

func (s *Scheduler) tick(ctx context.Context) {
	s.processDeferredSends(ctx)
	s.processSnoozeExpiry(ctx)
}

func (s *Scheduler) processDeferredSends(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM messages WHERE folder_id = 5 AND send_at <= CURRENT_TIMESTAMP ORDER BY send_at ASC`,
	)
	if err != nil {
		log.Printf("scheduler: query scheduled: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			log.Printf("scheduler: scan scheduled id: %v", err)
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("scheduler: scheduled rows: %v", err)
		return
	}

	for _, id := range ids {
		s.sendScheduledMessage(ctx, id)
	}
}

func (s *Scheduler) sendScheduledMessage(ctx context.Context, id int64) {
	// Atomically claim: clear send_at while the message is still in Scheduled (folder_id=5).
	// Returns false if HTTP cancel already ran, so we skip.
	claimed, err := s.draftRepo.ConditionalUpdateScheduled(ctx, id, map[string]any{
		"send_at": nil,
	})
	if err != nil {
		log.Printf("scheduler: claim message %d: %v", id, err)
		return
	}
	if !claimed {
		return
	}

	msg, err := s.messageRepo.GetMessage(ctx, id)
	if err != nil {
		log.Printf("scheduler: load message %d: %v", id, err)
		return
	}

	var fromName, fromAddr string
	if msg.IdentityID.Valid {
		identity, err := s.identityRepo.GetIdentity(ctx, msg.IdentityID.Int64)
		if err == nil {
			fromName = identity.Name
			fromAddr = identity.Address
		} else {
			log.Printf("scheduler: get identity for message %d: %v", id, err)
			fromAddr = msg.FromAddr
		}
	} else {
		fromAddr = msg.FromAddr
	}

	attachments, err := s.loadAttachmentsWithData(ctx, int64(msg.ID))
	if err != nil {
		log.Printf("scheduler: load attachments for message %d: %v", id, err)
		return
	}

	var refs []string
	if msg.References.Valid && msg.References.String != "" {
		for r := range strings.SplitSeq(msg.References.String, "\n") {
			if r != "" {
				refs = append(refs, r)
			}
		}
	}

	inReplyTo := ""
	if msg.InReplyTo.Valid {
		inReplyTo = msg.InReplyTo.String
	}

	fields := SendFields{
		FromName:    fromName,
		FromAddr:    fromAddr,
		ToAddr:      msg.ToAddr,
		CcAddr:      msg.CcAddr,
		BccAddr:     msg.BccAddr,
		ReplyToAddr: msg.ReplyToAddr,
		Subject:     msg.Subject,
		BodyText:    msg.BodyText,
		BodyHTML:    msg.BodyHTML,
		InReplyTo:   inReplyTo,
		References:  refs,
	}

	raw, _, msgIDValue, buildErr := BuildMIMEMessage(fields, attachments)
	if buildErr != nil {
		s.recordSendFailure(ctx, id, msg.SendFailureCount, "build: "+buildErr.Error())
		return
	}

	stderr, sendErr := SendMail(s.sendmailPath, raw)
	if sendErr != nil {
		errMsg := stderr
		if errMsg == "" {
			errMsg = sendErr.Error()
		}
		s.recordSendFailure(ctx, id, msg.SendFailureCount, errMsg)
		return
	}

	if _, err := s.db.ExecContext(ctx,
		`UPDATE messages SET folder_id = 2, send_at = NULL, read = 1, message_id = ? WHERE id = ? AND folder_id = 5`,
		msgIDValue, id,
	); err != nil {
		log.Printf("scheduler: move message %d to sent: %v", id, err)
	}

	s.upsertRecipients(ctx, msg.ToAddr)
	s.upsertRecipients(ctx, msg.CcAddr)
	s.upsertRecipients(ctx, msg.BccAddr)
}

func (s *Scheduler) recordSendFailure(ctx context.Context, id int64, currentCount int, errMsg string) {
	if currentCount < 2 {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE messages SET send_failure_count = send_failure_count + 1, send_error = ? WHERE id = ? AND folder_id = 5 AND send_failure_count < 2`,
			errMsg, id,
		); err != nil {
			log.Printf("scheduler: record failure for message %d: %v", id, err)
		}
	} else {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE messages SET folder_id = 3, send_at = NULL, send_failure_count = send_failure_count + 1, send_error = ? WHERE id = ? AND folder_id = 5 AND send_failure_count >= 2`,
			errMsg, id,
		); err != nil {
			log.Printf("scheduler: move message %d to drafts after failures: %v", id, err)
		}
	}
}

func (s *Scheduler) loadAttachmentsWithData(ctx context.Context, messageID int64) ([]model.DBAttachment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, message_id, filename, content_type, size, data FROM attachments WHERE message_id = ? ORDER BY id`,
		messageID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var atts []model.DBAttachment
	for rows.Next() {
		var a model.DBAttachment
		if err := rows.Scan(&a.ID, &a.MessageID, &a.Filename, &a.ContentType, &a.Size, &a.Data); err != nil {
			return nil, err
		}
		atts = append(atts, a)
	}
	return atts, rows.Err()
}

func (s *Scheduler) upsertRecipients(ctx context.Context, addrList string) {
	if addrList == "" {
		return
	}
	addrs, err := mail.ParseAddressList(addrList)
	if err != nil {
		return
	}
	for _, addr := range addrs {
		if err := s.contactRepo.UpsertContact(ctx, addr.Address, addr.Name); err != nil {
			log.Printf("scheduler: upsert contact %s: %v", addr.Address, err)
		}
	}
}

func (s *Scheduler) processSnoozeExpiry(ctx context.Context) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, snooze_folder FROM messages WHERE folder_id = 6 AND snoozed_until <= CURRENT_TIMESTAMP ORDER BY snoozed_until ASC`,
	)
	if err != nil {
		log.Printf("scheduler: query snoozed: %v", err)
		return
	}
	type snoozedRow struct {
		id          int64
		snoozeFolder sql.NullInt64
	}
	var msgs []snoozedRow
	for rows.Next() {
		var r snoozedRow
		if err := rows.Scan(&r.id, &r.snoozeFolder); err != nil {
			rows.Close()
			log.Printf("scheduler: scan snoozed: %v", err)
			return
		}
		msgs = append(msgs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		log.Printf("scheduler: snoozed rows: %v", err)
		return
	}

	for _, m := range msgs {
		if _, err := s.db.ExecContext(ctx,
			`UPDATE messages SET folder_id = COALESCE(snooze_folder, 1), snoozed_until = NULL, snooze_folder = NULL, read = 0 WHERE id = ? AND folder_id = 6`,
			m.id,
		); err != nil {
			log.Printf("scheduler: expire snooze for message %d: %v", m.id, err)
		}
	}
}
