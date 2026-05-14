package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/mikaelstaldal/mymail/internal/lda"
	"github.com/mikaelstaldal/mymail/internal/repository"
)

// runDemo populates the database with mock messages for demonstration and
// manual testing. The database must already be initialised with -init.
// Each invocation adds a fresh set of messages (unique message IDs) so the
// command can be run multiple times to grow the dataset.
func runDemo(dataDir string) {
	dbPath := filepath.Join(dataDir, "mymail.sqlite")
	if err := repository.CheckDBExists(dbPath); err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	lockFile, err := lda.AcquireImportLock(dataDir)
	if err != nil {
		log.Printf("error: %v", err)
		os.Exit(1)
	}

	db, err := repository.OpenDB(dbPath, 5000)
	if err != nil {
		log.Printf("error: open database: %v", err)
		lockFile.Close()
		os.Exit(1)
	}

	code := insertDemoData(db)
	db.Close()
	lockFile.Close()
	os.Exit(code)
}

type demoMsg struct {
	folderID   int64
	msgID      string
	inReplyTo  string // message-id without angle brackets; empty = none
	references string // newline-separated message-ids without angle brackets; empty = none
	from       string
	to         string
	cc         string
	subject    string
	date       time.Time
	bodyText   string
	bodyHTML   string
	read       bool
	flagged    bool
	isDraft    bool // raw IS NULL when true
}

func insertDemoData(db *sql.DB) int {
	ctx := context.Background()
	now := time.Now().UTC()
	nowStr := now.Format(time.RFC3339)

	// Unique 8-char suffix so message_ids never collide across runs.
	runID := uuid.New().String()[:8]
	mid := func(label string) string {
		return "demo-" + label + "-" + runID + "@demo.example"
	}

	// ago returns a time approximately `days` days before now, with up to
	// `jitterH` hours of additional random variation.
	ago := func(days int, jitterH int) time.Time {
		base := now.Add(-time.Duration(days) * 24 * time.Hour)
		jitter := time.Duration(rand.IntN(jitterH*60+1)) * time.Minute
		return base.Add(-jitter).Truncate(time.Second)
	}

	// Seed a demo identity only when none exists yet.
	var identCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM identities`).Scan(&identCount); err != nil {
		log.Printf("demo: check identities: %v", err)
		return 1
	}
	if identCount == 0 {
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO identities (name, address, is_default, position) VALUES (?, ?, 1, 0)`,
			"Demo User", "demo@example.com",
		); err != nil {
			log.Printf("demo: insert identity: %v", err)
			return 1
		}
	}

	// Pre-compute threaded message IDs so references can point to each other.
	budgetInbox1 := mid("budget-inbox-1")
	budgetSent1 := mid("budget-sent-1")
	budgetInbox2 := mid("budget-inbox-2")
	quoteInbox := mid("quote-inbox")

	// Random values for fields that vary across runs.
	issueNum := 30 + rand.IntN(40) // newsletter issue 30–69
	monthStr := now.Format("January 2006")

	// threadDate1 is anchored 4–7 days ago; subsequent replies come after it.
	threadBase := ago(4+rand.IntN(4), 6)
	threadReply1 := threadBase.Add(time.Duration(2+rand.IntN(12)) * time.Hour)
	threadReply2 := threadReply1.Add(time.Duration(1+rand.IntN(8)) * time.Hour)

	msgs := []demoMsg{
		// ── Thread: Q2 Budget Review ──────────────────────────────────────────
		{
			folderID: 1,
			msgID:    budgetInbox1,
			from:     "Alice Smith <alice@example.com>",
			to:       "Demo User <demo@example.com>",
			subject:  "Q2 Budget Review",
			date:     threadBase,
			bodyText: "Hi,\n\nI've finished the Q2 budget analysis. Can we schedule a call to go over the numbers?\n\nThe highlights:\n- Marketing spend is 12% over target\n- Engineering came in under budget by $18k\n- Overall we're roughly on track\n\nLet me know when you're free.\n\nAlice",
			read:     false,
		},
		{
			folderID:   2,
			msgID:      budgetSent1,
			inReplyTo:  budgetInbox1,
			references: budgetInbox1,
			from:       "Demo User <demo@example.com>",
			to:         "Alice Smith <alice@example.com>",
			subject:    "Re: Q2 Budget Review",
			date:       threadReply1,
			bodyText:   "Hi Alice,\n\nThanks for pulling this together. How about Thursday at 2 PM?\n\nBest,\nDemo User",
			read:       true,
		},
		{
			folderID:   1,
			msgID:      budgetInbox2,
			inReplyTo:  budgetSent1,
			references: budgetInbox1 + "\n" + budgetSent1,
			from:       "Alice Smith <alice@example.com>",
			to:         "Demo User <demo@example.com>",
			subject:    "Re: Q2 Budget Review",
			date:       threadReply2,
			bodyText:   "Thursday at 2 PM works perfectly. I'll send a calendar invite.\n\nAlice",
			read:       false,
			flagged:    true,
		},
		// ── Inbox: message with attachment ────────────────────────────────────
		{
			folderID: 1,
			msgID:    quoteInbox,
			from:     "Bob Johnson <bob@supplier.com>",
			to:       "Demo User <demo@example.com>",
			subject:  fmt.Sprintf("Service quote — %s", monthStr),
			date:     ago(1+rand.IntN(3), 8),
			bodyText: "Hello,\n\nPlease find our updated service quote for " + monthStr + " attached.\n\nDon't hesitate to reach out with any questions.\n\nRegards,\nBob Johnson\nSupplier Co.",
			read:     false,
		},
		// ── Inbox: HTML newsletter ────────────────────────────────────────────
		{
			folderID: 1,
			msgID:    mid("newsletter"),
			from:     "Tech Digest <news@techdigest.example>",
			to:       "demo@example.com",
			subject:  fmt.Sprintf("Tech Digest Weekly — Issue %d", issueNum),
			date:     ago(6+rand.IntN(4), 4),
			bodyText: fmt.Sprintf("Tech Digest Weekly — Issue %d\n\nThis week:\n• AI model releases from three major labs\n• New database engine benchmarks\n• Open-source highlights\n\nTo unsubscribe reply with 'unsubscribe'.", issueNum),
			bodyHTML: fmt.Sprintf("<html><body style=\"font-family:sans-serif;max-width:600px\">\n<h2 style=\"color:#2563eb\">Tech Digest Weekly</h2>\n<p style=\"color:#6b7280\">Issue %d — %s</p>\n<h3>This week</h3>\n<ul>\n<li>AI model releases from three major labs</li>\n<li>New database engine benchmarks</li>\n<li>Open-source highlights</li>\n</ul>\n<p style=\"font-size:0.8em;color:#9ca3af\">To unsubscribe, reply with 'unsubscribe'.</p>\n</body></html>", issueNum, now.Format("2 January 2006")),
			read:     true,
		},
		// ── Sent: standalone ─────────────────────────────────────────────────
		{
			folderID: 2,
			msgID:    mid("sent-intro"),
			from:     "Demo User <demo@example.com>",
			to:       "Charlie Davis <charlie@partner.example>",
			subject:  "Introduction",
			date:     ago(8+rand.IntN(5), 6),
			bodyText: "Hi Charlie,\n\nI'm the new point of contact on this project. Looking forward to collaborating.\n\nBest,\nDemo User",
			read:     true,
		},
		// ── Draft ─────────────────────────────────────────────────────────────
		{
			folderID: 3,
			msgID:    mid("draft-agenda"),
			from:     "Demo User <demo@example.com>",
			to:       "Alice Smith <alice@example.com>",
			subject:  "Meeting agenda",
			date:     now.Add(-time.Duration(rand.IntN(120)) * time.Minute).Truncate(time.Second),
			bodyText: "Draft agenda for Thursday:\n\n1. Q2 budget overview\n2. Marketing overspend\n3. Engineering savings\n4. Q3 planning\n\nTODO: add action items",
			isDraft:  true,
		},
		// ── Trash ─────────────────────────────────────────────────────────────
		{
			folderID: 4,
			msgID:    mid("trash-promo"),
			from:     "Deals <promo@deals.example>",
			to:       "demo@example.com",
			subject:  "50% off this weekend only!",
			date:     ago(10+rand.IntN(10), 8),
			bodyText: "HUGE SAVINGS — this weekend only!\n\nUse code WEEKEND50 at checkout.\n\nShop now at deals.example",
			read:     true,
		},
		// ── Junk ──────────────────────────────────────────────────────────────
		{
			folderID: 7,
			msgID:    mid("junk-phish"),
			from:     "Security Alert <no-reply@verify-account.example>",
			to:       "demo@example.com",
			subject:  "Verify your account immediately!",
			date:     ago(0, 12),
			bodyText: "Your account has been flagged for suspicious activity.\n\nClick the link to verify: http://verify-account.example/verify?token=abc123\n\nIgnore this message and your account will be suspended within 24 hours.",
			read:     false,
		},
	}

	// Pick 2 random extra inbox messages from the pool (without replacement).
	extras := demoExtraInboxMessages(mid, ago)
	picks := rand.Perm(len(extras))[:2]
	for _, i := range picks {
		msgs = append(msgs, extras[i])
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		log.Printf("demo: begin transaction: %v", err)
		return 1
	}
	defer tx.Rollback() //nolint:errcheck

	insertedIDs := make(map[string]int64)

	for _, m := range msgs {
		var inReplyToVal, refsVal, rawVal any
		if m.inReplyTo != "" {
			inReplyToVal = m.inReplyTo
		}
		if m.references != "" {
			refsVal = m.references
		}
		if !m.isDraft {
			rawVal = demoRaw(m.from, m.to, m.subject, m.date, m.msgID, m.bodyText, m.inReplyTo, m.references)
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
			demoBoolInt(m.read), demoBoolInt(m.flagged),
			nowStr, nowStr,
		)
		if err != nil {
			log.Printf("demo: insert %q: %v", m.subject, err)
			return 1
		}
		if n, _ := res.RowsAffected(); n > 0 {
			if id, err2 := res.LastInsertId(); err2 == nil {
				insertedIDs[m.msgID] = id
			}
		}
	}

	// Attachment for the "Service quote" message (trigger sets has_attachments=1).
	if rowID := insertedIDs[quoteInbox]; rowID != 0 {
		data := []byte("Supplier Co. — Service Quote\n============================\n\nService A:  $500 / month\nService B:  $300 / month\nSupport:    $200 / month\n\nTotal:    $1,000 / month\n\nValid until: " + now.AddDate(0, 1, 0).Format("2006-01-02") + "\nContact: bob@supplier.com\n")
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO attachments (message_id, filename, content_type, size, data) VALUES (?, ?, ?, ?, ?)`,
			rowID, "quote-"+now.Format("2006-01")+".txt", "text/plain", len(data), data,
		); err != nil {
			log.Printf("demo: insert attachment: %v", err)
			return 1
		}
	}

	if err := tx.Commit(); err != nil {
		log.Printf("demo: commit: %v", err)
		return 1
	}

	contactRepo := repository.NewContactRepository(db)
	demoContacts := []struct{ addr, name string }{
		{"alice@example.com", "Alice Smith"},
		{"bob@supplier.com", "Bob Johnson"},
		{"carol@partner.example", "Carol Brown"},
		{"charlie@partner.example", "Charlie Davis"},
		{"dave@company.example", "Dave Wilson"},
		{"eve@startup.example", "Eve Martinez"},
		{"frank@consulting.example", "Frank Lee"},
		{"news@techdigest.example", "Tech Digest"},
	}
	for _, c := range demoContacts {
		if err := contactRepo.UpsertContact(ctx, c.addr, c.name); err != nil {
			log.Printf("demo: upsert contact %s: %v", c.addr, err)
		}
	}

	fmt.Println("Demo data loaded successfully.")
	return 0
}

// demoExtraInboxMessages returns a pool of optional inbox messages to pick from.
// mid and ago are the same helpers used in insertDemoData.
func demoExtraInboxMessages(mid func(string) string, ago func(int, int) time.Time) []demoMsg {
	return []demoMsg{
		{
			folderID: 1,
			msgID:    mid("welcome"),
			from:     "Carol Brown <carol@partner.example>",
			to:       "Demo User <demo@example.com>",
			subject:  "Welcome aboard!",
			date:     ago(12+rand.IntN(6), 6),
			bodyText: "Hi,\n\nGreat to have you on the team! Feel free to reach out if you have any questions as you get started.\n\nLooking forward to working with you.\n\nCarol",
			read:     false,
		},
		{
			folderID: 1,
			msgID:    mid("meeting-request"),
			from:     "Dave Wilson <dave@company.example>",
			to:       "Demo User <demo@example.com>",
			subject:  "Meeting request: project sync",
			date:     ago(2+rand.IntN(4), 8),
			bodyText: "Hi,\n\nWould you be available for a 30-minute sync this week to discuss the project status?\n\nI'm flexible — any slot Tuesday or Wednesday works for me.\n\nThanks,\nDave",
			read:     false,
		},
		{
			folderID: 1,
			msgID:    mid("invoice"),
			from:     "Frank Lee <frank@consulting.example>",
			to:       "Demo User <demo@example.com>",
			subject:  fmt.Sprintf("Invoice #%d — consulting services", 1000+rand.IntN(9000)),
			date:     ago(3+rand.IntN(5), 6),
			bodyText: "Hi,\n\nPlease find attached invoice for consulting services rendered in the past month.\n\nPayment is due within 30 days.\n\nBest regards,\nFrank Lee\nFrank Lee Consulting",
			read:     true,
		},
		{
			folderID: 1,
			msgID:    mid("status-update"),
			from:     "Eve Martinez <eve@startup.example>",
			to:       "Demo User <demo@example.com>",
			cc:       "Alice Smith <alice@example.com>",
			subject:  "Project status update — week " + fmt.Sprintf("%d", 1+rand.IntN(52)),
			date:     ago(1+rand.IntN(6), 8),
			bodyText: "Hi team,\n\nQuick status update for this week:\n\n✓ Feature A shipped to staging\n✓ Bug fixes from last sprint merged\n⟳ Feature B still in review\n✗ Documentation update postponed to next sprint\n\nOverall on track for the release date.\n\nEve",
			read:     false,
		},
	}
}

// demoRaw builds a minimal RFC 5322 byte slice for a demo message.
// inReplyTo and references are stored without angle brackets in the DB;
// this function adds them back for the raw headers.
func demoRaw(from, to, subject string, date time.Time, msgID, body, inReplyTo, references string) []byte {
	var sb strings.Builder
	sb.WriteString("Message-ID: <" + msgID + ">\r\n")
	sb.WriteString("Date: " + date.Format("Mon, 02 Jan 2006 15:04:05 +0000") + "\r\n")
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + to + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	if inReplyTo != "" {
		sb.WriteString("In-Reply-To: <" + inReplyTo + ">\r\n")
	}
	if references != "" {
		parts := strings.Split(references, "\n")
		var refs []string
		for _, p := range parts {
			if p != "" {
				refs = append(refs, "<"+p+">")
			}
		}
		if len(refs) > 0 {
			sb.WriteString("References: " + strings.Join(refs, " ") + "\r\n")
		}
	}
	sb.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}

func demoBoolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
