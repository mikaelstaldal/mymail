package demo

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// The curated demo dataset: a handful of messages that between them exercise
// the features the UI has to show — a three-message thread across Inbox and
// Sent, an attachment, an HTML body, a draft, and something in each of Trash
// and Junk — plus the contacts a user would have accumulated by sending to
// them.
//
// Content lives here rather than next to the insert loop in demo.go so that
// adding a message is a data edit, and so that the browser demo's seed
// (bundle.go) and the SQLite seed are provably the same content.

// demoMsg is one seed message, in the shape the messages table wants.
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

// demoAttachment is a file attached to one of the seed messages.
type demoAttachment struct {
	msgID       string // the demoMsg.msgID it belongs to
	filename    string
	contentType string
	data        []byte
}

type demoContact struct {
	address string
	name    string
}

// content is one generated demo dataset. Message IDs are unique per call so a
// second `-demo` run against the same database adds messages rather than
// colliding with the first.
type content struct {
	msgs        []demoMsg
	attachments []demoAttachment
	contacts    []demoContact
}

// buildContent generates the demo dataset relative to now. runID is an
// 8-character token mixed into every Message-ID.
//
// Dates, a newsletter issue number and an invoice number are randomised so that
// two runs do not look identical; nothing about the shape of the data depends
// on those values.
func buildContent(now time.Time, runID string) content {
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
	extras := extraInboxMessages(mid, ago)
	picks := rand.Perm(len(extras))[:2]
	for _, i := range picks {
		msgs = append(msgs, extras[i])
	}

	quoteData := []byte("Supplier Co. — Service Quote\n============================\n\nService A:  $500 / month\nService B:  $300 / month\nSupport:    $200 / month\n\nTotal:    $1,000 / month\n\nValid until: " + now.AddDate(0, 1, 0).Format("2006-01-02") + "\nContact: bob@supplier.com\n")

	return content{
		msgs: msgs,
		attachments: []demoAttachment{{
			msgID:       quoteInbox,
			filename:    "quote-" + now.Format("2006-01") + ".txt",
			contentType: "text/plain",
			data:        quoteData,
		}},
		contacts: []demoContact{
			{"alice@example.com", "Alice Smith"},
			{"bob@supplier.com", "Bob Johnson"},
			{"carol@partner.example", "Carol Brown"},
			{"charlie@partner.example", "Charlie Davis"},
			{"dave@company.example", "Dave Wilson"},
			{"eve@startup.example", "Eve Martinez"},
			{"frank@consulting.example", "Frank Lee"},
			{"news@techdigest.example", "Tech Digest"},
		},
	}
}

// extraInboxMessages returns a pool of optional inbox messages to pick from.
// mid and ago are the same helpers used in buildContent.
func extraInboxMessages(mid func(string) string, ago func(int, int) time.Time) []demoMsg {
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

// rawMessage builds a minimal RFC 5322 byte slice for a demo message.
// inReplyTo and references are stored without angle brackets in the DB;
// this function adds them back for the raw headers.
func rawMessage(m demoMsg) []byte {
	var sb strings.Builder
	sb.WriteString("Message-ID: <" + m.msgID + ">\r\n")
	sb.WriteString("Date: " + m.date.Format("Mon, 02 Jan 2006 15:04:05 +0000") + "\r\n")
	sb.WriteString("From: " + m.from + "\r\n")
	sb.WriteString("To: " + m.to + "\r\n")
	if m.cc != "" {
		sb.WriteString("Cc: " + m.cc + "\r\n")
	}
	sb.WriteString("Subject: " + m.subject + "\r\n")
	if m.inReplyTo != "" {
		sb.WriteString("In-Reply-To: <" + m.inReplyTo + ">\r\n")
	}
	if m.references != "" {
		var refs []string
		for p := range strings.SplitSeq(m.references, "\n") {
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
	sb.WriteString(m.bodyText)
	return []byte(sb.String())
}
