package repository

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"

	"github.com/mikaelstaldal/mymail/internal/phishing"
	"modernc.org/sqlite"
)

// Keep this independent of contacts: receipt, contact edits and deletions cannot
// manufacture or erase evidence that this instance previously sent to someone.
// Inserts into Sent are successful immediate sends (or imported Sent history).
// Only Scheduled → Sent updates represent sending; a user moving received mail
// into Sent must not establish correspondence.
var schemaV10 = []string{
	`CREATE TABLE sent_recipients (address TEXT PRIMARY KEY,
 domain TEXT GENERATED ALWAYS AS (mail_domain(address)) STORED,
 skeleton TEXT GENERATED ALWAYS AS (domain_skeleton(mail_domain(address))) STORED) WITHOUT ROWID`,
	`CREATE INDEX idx_sent_recipients_skeleton ON sent_recipients(skeleton)`,
	`INSERT OR IGNORE INTO sent_recipients(address) SELECT j.value FROM messages, json_each(mail_recipients(to_addr, cc_addr, bcc_addr)) AS j WHERE folder_id = 2`,
	`CREATE TRIGGER messages_sent_insert AFTER INSERT ON messages WHEN NEW.folder_id = 2 BEGIN
 INSERT OR IGNORE INTO sent_recipients(address) SELECT value FROM json_each(mail_recipients(NEW.to_addr, NEW.cc_addr, NEW.bcc_addr)); END`,
	`CREATE TRIGGER messages_sent_update AFTER UPDATE OF folder_id ON messages WHEN NEW.folder_id = 2 AND OLD.folder_id = 5 BEGIN
 INSERT OR IGNORE INTO sent_recipients(address) SELECT value FROM json_each(mail_recipients(NEW.to_addr, NEW.cc_addr, NEW.bcc_addr)); END`,
}

func init() {
	sqlite.MustRegisterDeterministicScalarFunction("mail_recipients", 3, mailRecipients)
	sqlite.MustRegisterDeterministicScalarFunction("mail_domain", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		_, domain, _ := strings.Cut(s, "@")
		return phishing.Registrable(domain), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("domain_skeleton", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		s, _ := args[0].(string)
		return phishing.Skeleton(s), nil
	})
}
func mailRecipients(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	recipients := []string{}
	for _, arg := range args {
		if arg == nil {
			continue
		}
		s, ok := arg.(string)
		if !ok {
			return nil, fmt.Errorf("mail_recipients: expected string")
		}
		addrs, err := mail.ParseAddressList(s)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			recipients = append(recipients, phishing.FoldAddress(a.Address))
		}
	}
	b, err := json.Marshal(recipients)
	return string(b), err
}

// WarningContext reads current identity and sent history, not receipt-created
// contacts. Build the skeleton index once per detail read in phishing.Compute.
func (r *MessageRepository) WarningContext(ctx context.Context, from string, linkHosts ...string) (phishing.Context, error) {
	c := phishing.Context{}
	if a, err := mail.ParseAddress(from); err == nil {
		if err := queryer(ctx, r.db).QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sent_recipients WHERE address = ?)`, phishing.FoldAddress(a.Address)).Scan(&c.SentTo); err != nil {
			return c, err
		}
	}
	// Match indexed skeletons rather than scanning the entire correspondence
	// history for every message. The HTML analyser supplies at most MaxLinks hosts.
	if a, err := mail.ParseAddress(from); err == nil {
		_, d, _ := strings.Cut(a.Address, "@")
		linkHosts = append(linkHosts, d)
	}
	keys := []string{}
	seenKeys := map[string]bool{}
	for _, host := range linkHosts {
		if host != "" {
			for _, candidate := range []string{host, phishing.Registrable(host)} {
				key := phishing.Skeleton(candidate)
				if !seenKeys[key] {
					seenKeys[key] = true
					keys = append(keys, key)
				}
			}
		}
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		return c, err
	}
	rows, err := queryer(ctx, r.db).QueryContext(ctx, `SELECT address, 1 FROM identities UNION ALL
 SELECT DISTINCT domain, 0 FROM sent_recipients WHERE skeleton IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return c, err
	}
	defer rows.Close()
	domains := map[string]bool{}
	for rows.Next() {
		var a string
		var own bool
		if err := rows.Scan(&a, &own); err != nil {
			return c, err
		}
		if own {
			c.OwnAddresses = append(c.OwnAddresses, a)
		}
		d := a
		if own {
			_, d, _ = strings.Cut(a, "@")
		}
		if d != "" && !domains[d] {
			domains[d] = true
			c.KnownDomains = append(c.KnownDomains, d)
		}
	}
	return c, rows.Err()
}
