-- Application-owned DDL from a freshly migrated database; see AGENTS.md.
-- This omits seed rows and is not a replacement for mymail -init.
PRAGMA user_version = 4;

CREATE INDEX idx_attachments_message_id ON attachments(message_id);

CREATE INDEX idx_contacts_address ON contacts(address);

CREATE INDEX idx_messages_date         ON messages(date);

CREATE INDEX idx_messages_folder_date ON messages(folder_id, date DESC);

CREATE INDEX idx_messages_folder_id    ON messages(folder_id);

CREATE INDEX idx_messages_folder_read ON messages(folder_id, read);

CREATE INDEX idx_messages_in_reply_to ON messages(in_reply_to) WHERE in_reply_to IS NOT NULL;

CREATE INDEX idx_messages_message_id   ON messages(message_id);

CREATE INDEX idx_messages_read         ON messages(read);

CREATE INDEX idx_messages_send_at      ON messages(send_at) WHERE send_at IS NOT NULL;

CREATE INDEX idx_messages_snoozed_until ON messages(snoozed_until) WHERE snoozed_until IS NOT NULL;

CREATE INDEX idx_msgref_ref ON message_references(ref_msg_id);

CREATE TABLE attachments (
		id           INTEGER PRIMARY KEY AUTOINCREMENT,
		message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		filename     TEXT    NOT NULL,
		content_type TEXT    NOT NULL,
		size         INTEGER NOT NULL,
		data         BLOB    NOT NULL
	);

CREATE TABLE contacts (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		address    TEXT    NOT NULL UNIQUE,
		name       TEXT    NOT NULL DEFAULT '',
		created_at TEXT    NOT NULL,
		updated_at TEXT    NOT NULL
	);

CREATE TABLE filters (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		position      INTEGER NOT NULL DEFAULT 0,
		name          TEXT    NOT NULL DEFAULT '',
		match_from    TEXT    NOT NULL DEFAULT '',
		match_to      TEXT    NOT NULL DEFAULT '',
		match_subject TEXT    NOT NULL DEFAULT '',
		action        TEXT    NOT NULL,
		folder_id     INTEGER REFERENCES folders(id) ON DELETE SET NULL,
		stop          INTEGER NOT NULL DEFAULT 1
	);

CREATE TABLE folders (
		id         INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL UNIQUE,
		slug       TEXT    NOT NULL UNIQUE,
		position   INTEGER NOT NULL DEFAULT 0,
		created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
	);

CREATE TABLE identities (
		id         INTEGER PRIMARY KEY AUTOINCREMENT,
		name       TEXT    NOT NULL,
		address    TEXT    NOT NULL UNIQUE,
		is_default INTEGER NOT NULL DEFAULT 0,
		position   INTEGER NOT NULL DEFAULT 0,
		signature  TEXT    NOT NULL DEFAULT ''
	);

CREATE TABLE message_references (
		message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
		ref_msg_id TEXT    NOT NULL,
		UNIQUE (message_id, ref_msg_id)
	);

CREATE TABLE messages (
		id                  INTEGER PRIMARY KEY AUTOINCREMENT,
		folder_id           INTEGER NOT NULL REFERENCES folders(id),
		identity_id         INTEGER REFERENCES identities(id) ON DELETE SET NULL,
		message_id          TEXT    UNIQUE,
		in_reply_to         TEXT,
		"references"        TEXT,
		from_addr           TEXT    NOT NULL DEFAULT '',
		to_addr             TEXT    NOT NULL DEFAULT '',
		cc_addr             TEXT    NOT NULL DEFAULT '',
		bcc_addr            TEXT    NOT NULL DEFAULT '',
		reply_to_addr       TEXT    NOT NULL DEFAULT '',
		subject             TEXT    NOT NULL DEFAULT '',
		date                TEXT    NOT NULL,
		body_text           TEXT    NOT NULL DEFAULT '',
		body_html           TEXT    NOT NULL DEFAULT '',
		raw                 BLOB,
		read                INTEGER NOT NULL DEFAULT 0,
		flagged             INTEGER NOT NULL DEFAULT 0,
		has_attachments     INTEGER NOT NULL DEFAULT 0,
		has_external_images INTEGER NOT NULL DEFAULT 0,
		send_at             TEXT,
		snoozed_until       TEXT,
		snooze_folder       INTEGER REFERENCES folders(id) ON DELETE SET NULL,
		send_error          TEXT,
		send_failure_count  INTEGER NOT NULL DEFAULT 0,
		created_at          TEXT    NOT NULL,
		updated_at          TEXT    NOT NULL
	);

CREATE VIRTUAL TABLE messages_fts USING fts5(
		from_addr,
		to_addr,
		cc_addr,
		subject,
		body_text,
		content='messages',
		content_rowid='id'
	);

CREATE TABLE spam_filter_settings (
		id              INTEGER PRIMARY KEY CHECK (id = 1),
		enabled         INTEGER NOT NULL DEFAULT 1,
		score_header    TEXT    NOT NULL DEFAULT 'X-Spam-Score',
		score_threshold REAL    NOT NULL DEFAULT 5.0
	);

CREATE TRIGGER attachments_delete_flag
		AFTER DELETE ON attachments
	BEGIN
		UPDATE messages SET has_attachments = (
			SELECT CASE WHEN EXISTS (SELECT 1 FROM attachments WHERE message_id = old.message_id) THEN 1 ELSE 0 END
		) WHERE id = old.message_id;
	END;

CREATE TRIGGER attachments_insert_flag
		AFTER INSERT ON attachments
	BEGIN
		UPDATE messages SET has_attachments = 1 WHERE id = new.message_id;
	END;

CREATE TRIGGER messages_fts_delete
		AFTER DELETE ON messages
	BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
	END;

CREATE TRIGGER messages_fts_insert
		AFTER INSERT ON messages
	BEGIN
		INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
	END;

CREATE TRIGGER messages_fts_update
		AFTER UPDATE OF from_addr, to_addr, cc_addr, subject, body_text ON messages
	BEGIN
		INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
		INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
		VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
	END;

CREATE TRIGGER messages_updated_at
		AFTER UPDATE ON messages
		WHEN new.updated_at = old.updated_at
	BEGIN
		UPDATE messages SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = new.id;
	END;
