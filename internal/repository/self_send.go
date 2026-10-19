package repository

import "strings"

// Rebuild messages and its dependent tables to replace the inline UNIQUE
// constraint. Preserve row IDs, child data, FTS contents and sequence high-water
// marks. Provenance survives folder moves so a sent copy never suppresses receipt.
func schemaV12() []string {
	var statements []string
	for _, table := range []string{"messages", "attachments", "message_references"} {
		var definition string
		for _, ddl := range append(append([]string{}, schemaV1...), schemaV3...) {
			if strings.HasPrefix(ddl, "CREATE TABLE IF NOT EXISTS "+table+" (") {
				definition = ddl
				break
			}
		}
		definition = strings.Replace(definition, "IF NOT EXISTS "+table, table+"_new", 1)
		definition = strings.ReplaceAll(definition, "REFERENCES messages(id)", "REFERENCES messages_new(id)")
		if table == "messages" {
			definition = strings.Replace(definition, "message_id          TEXT    UNIQUE", "message_id          TEXT", 1)
			definition = strings.TrimSuffix(definition, "\n\t)") + `,
		sender_authentication TEXT NOT NULL DEFAULT '{"dmarc":"unknown","dkim_aligned":false,"spf":"unknown"}',
		auth_generation INTEGER NOT NULL DEFAULT 0,
		outgoing INTEGER NOT NULL DEFAULT 0 CHECK (outgoing IN (0, 1)),
		UNIQUE (message_id, outgoing)
	)`
		}
		statements = append(statements, definition)
		if table == "messages" {
			statements = append(statements, `INSERT INTO messages_new SELECT id, folder_id, identity_id, message_id, in_reply_to, "references",
 from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject, date, body_text, body_html, raw, read, flagged,
 has_attachments, has_external_images, send_at, snoozed_until, snooze_folder, send_error, send_failure_count,
 created_at, updated_at, sender_authentication, auth_generation, CASE WHEN folder_id = 2 OR
				(identity_id IS NOT NULL AND raw IS NOT NULL AND folder_id NOT IN (3, 5)) THEN 1 ELSE 0 END FROM messages`)
		} else {
			statements = append(statements, "INSERT INTO "+table+"_new SELECT * FROM "+table)
		}
		if table != "message_references" {
			statements = append(statements, "UPDATE sqlite_sequence SET seq = MAX(seq, "+
				"COALESCE((SELECT seq FROM sqlite_sequence WHERE name = '"+table+"'), 0)) WHERE name = '"+table+"_new'")
		}
	}
	// Keep the external-content FTS index: its row IDs do not change.
	for _, name := range []string{"messages_fts_insert", "messages_fts_delete", "messages_fts_update"} {
		statements = append(statements, "DROP TRIGGER "+name)
	}
	statements = append(statements,
		"DROP TABLE attachments", "DROP TABLE message_references", "DROP TABLE messages",
		"ALTER TABLE messages_new RENAME TO messages",
		"ALTER TABLE attachments_new RENAME TO attachments",
		"ALTER TABLE message_references_new RENAME TO message_references")
	for _, batch := range [][]string{schemaV1, schemaV3, schemaV4, schemaV10} {
		for _, ddl := range batch {
			if strings.HasPrefix(ddl, "CREATE TRIGGER") ||
				(strings.HasPrefix(ddl, "CREATE INDEX") &&
					(strings.Contains(ddl, " ON messages(") || strings.Contains(ddl, " ON attachments(") ||
						strings.Contains(ddl, " ON message_references("))) {
				statements = append(statements, ddl)
			}
		}
	}
	return statements
}
