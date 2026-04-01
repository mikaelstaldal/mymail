# mymail — Specification

A self-hosted personal (single-user) email client with a Go backend, SQLite storage, REST API, and embedded web UI.
Designed to run on a Linux server alongside a mail system such as Postfix.

---

## 1. Overview

mymail stores, organizes, and presents email. It does **not** speak IMAP/POP3 or SMTP directly. Instead:

- **Incoming mail** is delivered by the local MTA (Postfix, etc.) via a local delivery agent (LDA) mode.
- **Outgoing mail** is handed off to the system `sendmail` binary.
- The application is a single self-contained binary with an embedded web UI.

---

## 2. Project Structure

```
mymail/
├── main.go                   # Entry point, CLI flags, routing, startup
├── go.mod / go.sum
├── internal/
│   ├── auth/                 # HTTP Basic Auth middleware (htpasswd)
│   ├── handler/              # HTTP handlers (REST API)
│   ├── lda/                  # Local delivery agent (parse & store incoming mail)
│   ├── model/                # Data types (Message, Folder, Filter, etc.)
│   ├── repository/           # SQLite data access layer
│   ├── sanitize/             # HTML sanitization for message bodies
│   └── service/              # Business logic
├── web/
│   ├── embed.go              # //go:embed directive
│   └── static/               # Frontend assets (HTML, JS, CSS)
├── docs/                     # API documentation
└── data/                     # Runtime data directory (default)
```

Follows the same layered architecture as mycal:
`handler → service → repository → SQLite`

---

## 3. Command-Line Interface

```
mymail [flags]
mymail -lda [flags]
```

### Server mode (default)

| Flag                | Default             | Description                                            |
|---------------------|---------------------|--------------------------------------------------------|
| `-port`             | `8080`              | HTTP listen port (1–65535)                             |
| `-addr`             | `` (all interfaces) | Bind address                                           |
| `-data`             | `data/`             | Data directory (stores `mymail.sqlite`)                |
| `-basic-auth-file`  | ``                  | Path to htpasswd file; if set, enables HTTP Basic Auth |
| `-basic-auth-realm` | `mymail`            | Auth realm shown to clients                            |

Identities are managed entirely through the REST API (§5.9) and the web UI. There is no CLI flag for the initial identity; the first identity is created via the web UI on first use (the compose view prompts the user if no identities exist).

### Import mode (`-import`)

```
mymail -import -data <dir> <mapping>...
```

Each `<mapping>` argument is a colon-separated triplet `<folder>:<format>:<path>`:

| Part       | Values                                                      | Description                                                          |
|------------|-------------------------------------------------------------|----------------------------------------------------------------------|
| `<folder>` | `inbox`, `sent`, `drafts`, `trash`, or any user-folder name | Target folder in mymail. Created automatically if it does not exist. |
| `<format>` | `mbox`, `maildir`                                           | Source format (see §7 for details)                                   |
| `<path>`   | file or directory path                                      | Source mbox file or Maildir root directory                           |

Example — import from a Thunderbird profile:

```bash
mymail -import -data /var/lib/mymail \
  inbox:mbox:/home/user/.thunderbird/abc123/Mail/Local\ Folders/Inbox \
  sent:mbox:/home/user/.thunderbird/abc123/Mail/Local\ Folders/Sent \
  drafts:mbox:/home/user/.thunderbird/abc123/Mail/Local\ Folders/Drafts \
  work:maildir:/home/user/Maildir/.Work
```

Behaviour:
- Messages are imported in source order (oldest first within each file/directory).
- Duplicate detection: if a message with the same `Message-ID` already exists anywhere in the database, it is skipped (not re-imported). Messages without a `Message-ID` are always imported.
- Filters are **not** applied during import — messages go directly to the specified target folder.
- A running count is printed to stdout as each folder completes: `inbox: 1042 imported, 3 skipped`.
- On completion, a summary line is printed: `Total: 2381 imported, 17 skipped`.
- Exit code `0` on success, `1` on any error (details logged to stderr). A single unparseable message logs a warning and continues; it does not abort the import.

### LDA mode (`-lda`)

When invoked with `-lda`, the program reads a single RFC 5322 message from **stdin**, stores it in the database, applies filters, and exits. 
All other server flags are irrelevant in this mode; only `-data` is used.

This allows Postfix configuration like:

```
# /etc/postfix/main.cf
mailbox_command = /usr/local/bin/mymail -lda -data /var/lib/mymail
```

Exit codes follow standard LDA conventions:
- `0` — success
- `1` — permanent failure (message will bounce)
- `75` — temporary failure (MTA will retry; used e.g. if database is locked)

---

## 4. Database Schema

File: `<data>/mymail.sqlite`

All timestamps are stored as UTC RFC 3339 strings.

### Schema migrations

The database schema is versioned using `PRAGMA user_version`. On every startup the server reads the current version and applies any missing migrations in order, then sets `user_version` to the new value. Each migration is a plain SQL string executed in a transaction; if it fails the server aborts with a fatal error.

```
user_version 0  →  fresh database: run all CREATE TABLE / CREATE INDEX / CREATE TRIGGER statements
user_version 1  →  (reserved for first future migration)
```

**Current schema version: 0** (initial schema; no migrations beyond table creation yet).

The migration runner pseudocode:

```
v = PRAGMA user_version
if v == 0:
    -- create all tables, indexes, triggers, seed built-in folders
    PRAGMA user_version = 0   -- already at 0; explicit for clarity
if v < 1:
    -- future migration example: ALTER TABLE messages ADD COLUMN foo TEXT
    PRAGMA user_version = 1
...
```

Because `PRAGMA user_version` is set inside the same transaction as the DDL statements, a crash mid-migration leaves the version unchanged and the migration will be retried on next startup.

### 4.1 `folders`

```sql
CREATE TABLE IF NOT EXISTS folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,   -- display name, e.g. "Work"
    slug       TEXT    NOT NULL UNIQUE,   -- URL-safe key, e.g. "work"
    position   INTEGER NOT NULL DEFAULT 0, -- display order
    hidden     INTEGER NOT NULL DEFAULT 0  -- 1 = hidden from normal folder listing
);
```

**Built-in folders** (created on first run, protected from deletion):

| id | name      | slug      | hidden | Notes                                                                  |
|----|-----------|-----------|--------|------------------------------------------------------------------------|
| 1  | Inbox     | inbox     | 0      |                                                                        |
| 2  | Sent      | sent      | 0      |                                                                        |
| 3  | Drafts    | drafts    | 0      |                                                                        |
| 4  | Trash     | trash     | 0      |                                                                        |
| 5  | Scheduled | scheduled | 0      | Visible in sidebar; messages awaiting deferred send                    |
| 6  | Snoozed   | snoozed   | 0      | Visible in sidebar; messages awaiting snooze expiry                    |
| 7  | Junk      | junk      | 0      | Spam messages; visible in sidebar                                      |

"Hidden" folders (`hidden=1`) are not returned by `GET /api/v1/folders` in the normal listing and cannot be targeted by user-defined filters or manual `PATCH` moves. They are managed exclusively by the scheduler.

User-created folders have `id >= 100`.

### 4.2 `messages`

```sql
CREATE TABLE IF NOT EXISTS messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    folder_id     INTEGER NOT NULL REFERENCES folders(id),
    message_id    TEXT,                    -- RFC 5322 Message-ID header value
    in_reply_to   TEXT,                    -- In-Reply-To header value
    references    TEXT,                    -- References header value (space-separated); serialized as JSON array by the API
    from_addr     TEXT    NOT NULL,        -- From header (display name + address)
    to_addr       TEXT    NOT NULL,        -- To header (may contain multiple)
    cc_addr       TEXT    NOT NULL DEFAULT '',
    bcc_addr      TEXT    NOT NULL DEFAULT '',
    reply_to_addr TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    date          TEXT    NOT NULL,        -- RFC 3339 UTC timestamp (from Date header)
    body_text     TEXT    NOT NULL DEFAULT '', -- plain-text part
    body_html     TEXT    NOT NULL DEFAULT '', -- HTML part (sanitized on storage)
    raw           BLOB    NOT NULL,        -- original raw RFC 5322 message
    read          INTEGER NOT NULL DEFAULT 0, -- 0=unread, 1=read
    flagged       INTEGER NOT NULL DEFAULT 0, -- 0=normal, 1=starred/flagged
    send_at       TEXT,                    -- RFC 3339 UTC; non-NULL = deferred send, message sits in Scheduled folder
    snoozed_until TEXT,                    -- RFC 3339 UTC; non-NULL = snoozed, message sits in Snoozed folder
    snooze_folder INTEGER,                 -- folder_id to return to when snooze expires (usually Inbox=1)
    send_error    TEXT,                    -- last sendmail error for a scheduled message that failed to send
    send_failure_count INTEGER NOT NULL DEFAULT 0, -- consecutive send failures; message moved to Drafts after 3
    created_at    TEXT    NOT NULL,        -- RFC 3339 UTC, time of storage
    updated_at    TEXT    NOT NULL         -- RFC 3339 UTC, time of last modification (set equal to created_at on insert; updated on every PATCH)
);

CREATE INDEX IF NOT EXISTS idx_messages_folder_id    ON messages(folder_id);
CREATE INDEX IF NOT EXISTS idx_messages_date         ON messages(date);
CREATE INDEX IF NOT EXISTS idx_messages_message_id   ON messages(message_id);
CREATE INDEX IF NOT EXISTS idx_messages_read         ON messages(read);
CREATE INDEX IF NOT EXISTS idx_messages_send_at      ON messages(send_at) WHERE send_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_snoozed_until ON messages(snoozed_until) WHERE snoozed_until IS NOT NULL;
```

### 4.3 `messages_fts` (FTS5)

```sql
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
    from_addr,
    to_addr,
    cc_addr,
    subject,
    body_text,
    content='messages',
    content_rowid='id'
);
```

Maintained by triggers:

```sql
CREATE TRIGGER IF NOT EXISTS messages_fts_insert AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_delete AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
END;

CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
    INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
END;
```

### 4.4 `attachments`

Only MIME parts with `Content-Disposition: attachment` (or with no `Content-Disposition` and a non-displayable `Content-Type`) are stored here. Inline image parts referenced by `cid:` URLs in the HTML body are **not** stored as attachments; they are embedded as `data:` URIs directly into `body_html` at storage time (see §6 and §10). This avoids storing the same bytes twice.

```sql
CREATE TABLE IF NOT EXISTS attachments (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id   INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    filename     TEXT    NOT NULL,
    content_type TEXT    NOT NULL,
    size         INTEGER NOT NULL,
    data         BLOB    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_attachments_message_id ON attachments(message_id);
```

### 4.5 `identities`

```sql
CREATE TABLE IF NOT EXISTS identities (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT    NOT NULL,          -- display name, e.g. "Alice Doe"
    address      TEXT    NOT NULL UNIQUE,   -- email address, e.g. "alice@example.com"
    is_default   INTEGER NOT NULL DEFAULT 0, -- exactly one row should have 1
    position     INTEGER NOT NULL DEFAULT 0, -- display order in the From selector
    signature    TEXT    NOT NULL DEFAULT '' -- plain-text signature; empty = no signature
);
```

**Constraints enforced in the service layer** (not as SQL constraints, to give cleaner error messages):
- At least one identity must exist at all times.
- Exactly one identity has `is_default=1`. When a new identity is created with `is_default=true`, all other rows are set to `is_default=0` in the same transaction. When the default identity is deleted, the identity with the lowest `position` (then lowest `id`) becomes the new default.
- `address` must be a syntactically valid email address (RFC 5322 `addr-spec`).

The first identity is created interactively or via CLI (see §3). There is no seeded default.

### 4.6 `contacts`

```sql
CREATE TABLE IF NOT EXISTS contacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    address    TEXT    NOT NULL UNIQUE,   -- email address (lower-cased for deduplication)
    name       TEXT    NOT NULL DEFAULT '', -- display name; may be empty
    created_at TEXT    NOT NULL,          -- RFC 3339 UTC
    updated_at TEXT    NOT NULL           -- RFC 3339 UTC
);

CREATE INDEX IF NOT EXISTS idx_contacts_address ON contacts(address);
```

Contacts are upserted automatically on message receipt (From address) and on send (To, Cc, Bcc addresses). On auto-upsert, `address` is inserted if not present; if a row already exists, `name` is updated only when the stored `name` is empty (so a manually set name is never overwritten automatically). `address` is lower-cased before storage to ensure case-insensitive deduplication.

### 4.7 `filters`

```sql
CREATE TABLE IF NOT EXISTS filters (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    position      INTEGER NOT NULL DEFAULT 0,  -- evaluation order (ascending)
    name          TEXT    NOT NULL DEFAULT '',  -- human-readable label
    -- match criteria (all non-empty fields are ANDed together)
    match_from    TEXT    NOT NULL DEFAULT '',  -- substring match on From
    match_to      TEXT    NOT NULL DEFAULT '',  -- substring match on To or Cc
    match_subject TEXT    NOT NULL DEFAULT '',  -- substring match on Subject
    -- action
    action        TEXT    NOT NULL,             -- "move", "trash", "mark_read"
    folder_id     INTEGER REFERENCES folders(id), -- required when action="move"
    stop          INTEGER NOT NULL DEFAULT 1   -- 0=continue to next filter, 1=stop
);
```

**Actions:**
- `move` — deliver to `folder_id` instead of Inbox
- `trash` — deliver directly to Trash
- `mark_read` — deliver to Inbox but mark as read
- `drop` — discard the message entirely; nothing is stored in the database

Multiple criteria within a filter are ANDed. Filters are evaluated in `position` order. When `stop=1` (default) the first matching filter wins and evaluation halts.

### 4.7 `spam_filter_settings`

```sql
CREATE TABLE IF NOT EXISTS spam_filter_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1), -- single-row table
    enabled       INTEGER NOT NULL DEFAULT 1,         -- 0=disabled, 1=enabled
    score_header  TEXT    NOT NULL DEFAULT 'X-Spam-Score', -- header to read score from
    score_threshold REAL  NOT NULL DEFAULT 5.0        -- route to Junk if score >= threshold
);
```

A single row (enforced by `CHECK (id = 1)`). Created with defaults on first run. The `score_header` and `score_threshold` fields support deployments where the MTA uses a non-standard score header name or a different numeric scale.

Spam detection also recognises the `X-Spam-Flag` header (value `YES`, case-insensitive) and the `X-Spam-Status` header (value starting with `Yes`, case-insensitive) regardless of the score threshold. Either a flag match or a score-threshold breach independently triggers spam routing.

---

## 5. REST API

**Base path:** `/api/v1`

**Content type:** `application/json` for all request and response bodies, except attachment download endpoints.

**Max request body:** 32 MB (to accommodate raw message uploads; typical JSON operations limited to 1 MB).

**Error responses:**

```json
{ "error": "human-readable message" }
```

HTTP status codes:
- `400` — validation error
- `401` — authentication required
- `404` — not found
- `409` — conflict (e.g. duplicate folder name)
- `500` — internal error

### 5.1 Folders

#### `GET /api/v1/folders`

Returns all folders ordered by `position`.

Response `200`:
```json
[
  {
    "id": 1,
    "name": "Inbox",
    "slug": "inbox",
    "position": 0,
    "unread_count": 3
  }
]
```

#### `POST /api/v1/folders`

Create a user-defined folder.

Request:
```json
{
  "name": "Work",
  "position": 10
}
```

The `slug` is derived from `name` (lowercase, spaces → hyphens, non-alphanumeric stripped). Returns `409` if name or slug already exists.

Response `201`: folder object.

#### `PATCH /api/v1/folders/{id}`

Update folder name and/or position. Built-in folders (id 1–4) may have their `position` updated but not their `name`.

Request (all fields optional):
```json
{
  "name": "Important Work",
  "position": 5
}
```

Response `200`: updated folder object.

#### `DELETE /api/v1/folders/{id}`

Delete a user-created folder. Messages in this folder are moved to Trash first. Returns `400` for built-in folders.

Response `204`.

---

### 5.2 Messages

#### `GET /api/v1/folders/{folder_id}/messages`

List messages in a folder. Results are ordered by `date` descending.

Query parameters:

| Parameter | Type | Description                                  |
|-----------|------|----------------------------------------------|
| `limit`   | int  | Max messages to return (default 50, max 200) |
| `offset`  | int  | Pagination offset (default 0)                |
| `unread`  | bool | If `true`, return only unread messages       |
| `flagged` | bool | If `true`, return only flagged messages      |

Response `200`:
```json
{
  "total": 142,
  "messages": [
    {
      "id": 17,
      "folder_id": 1,
      "message_id": "<abc@example.com>",
      "from_addr": "Alice <alice@example.com>",
      "to_addr": "Bob <bob@example.com>",
      "subject": "Hello",
      "date": "2026-03-29T10:00:00Z",
      "read": false,
      "flagged": false,
      "has_attachments": true,
      "created_at": "2026-03-29T10:01:05Z"
    }
  ]
}
```

Note: the list endpoint omits `body_text`, `body_html`, `raw`, `cc_addr`, `bcc_addr`, and `attachments` for efficiency.

#### `GET /api/v1/messages/search`

Full-text search across all folders.

Query parameters:

| Parameter   | Type   | Description                     |
|-------------|--------|---------------------------------|
| `q`         | string | FTS5 query string (required)    |
| `folder_id` | int    | Restrict to a folder (optional) |
| `limit`     | int    | Default 50, max 200             |
| `offset`    | int    | Pagination offset               |

The query is executed as `SELECT ... FROM messages_fts WHERE messages_fts MATCH ?` using FTS5 syntax. Results include a `snippet` field (FTS5 `snippet()` function output) and are ordered by rank (relevance).

Response `200`:
```json
{
  "total": 5,
  "messages": [
    {
      "id": 17,
      "folder_id": 1,
      "from_addr": "...",
      "subject": "...",
      "date": "...",
      "read": false,
      "flagged": false,
      "snippet": "...highlighted excerpt..."
    }
  ]
}
```

#### `GET /api/v1/messages/{id}`

Get full message details including body and attachments list.

Response `200`:
```json
{
  "id": 17,
  "folder_id": 1,
  "message_id": "<abc@example.com>",
  "in_reply_to": "<prev@example.com>",
  "references": ["<older@example.com>", "<prev@example.com>"],
  "from_addr": "Alice <alice@example.com>",
  "to_addr": "Bob <bob@example.com>",
  "cc_addr": "",
  "reply_to_addr": "",
  "subject": "Hello",
  "date": "2026-03-29T10:00:00Z",
  "body_text": "Hi Bob,\n...",
  "body_html": "<p>Hi Bob,</p>...",
  "read": true,
  "flagged": false,
  "created_at": "2026-03-29T10:01:05Z",
  "attachments": [
    {
      "id": 3,
      "filename": "report.pdf",
      "content_type": "application/pdf",
      "size": 204800
    }
  ]
}
```

Fetching a message automatically marks it as read (sets `read=1`). The HTML body is sanitized (see §7).

#### `GET /api/v1/messages/{id}/raw`

Download the original raw RFC 5322 message.

Response `200` with `Content-Type: message/rfc822` and `Content-Disposition: attachment; filename="message.eml"`.

#### `GET /api/v1/attachments/{id}`

Download attachment data.

Response `200` with appropriate `Content-Type` and `Content-Disposition: attachment; filename="<filename>"`.

The filename used in `Content-Disposition` is sanitised from the stored attachment name before being placed in the header:
1. Replace each space character with `_`.
2. Remove all characters that are not ASCII alphanumeric (`A–Z`, `a–z`, `0–9`), `-`, `_`, or `.`.
3. If the result is empty, use the fallback `attachment`.

Example: `"My Report (draft).pdf"` → `"My_Report_draft.pdf"`.

#### `PATCH /api/v1/messages/{id}`

Update message metadata. Supports partial updates (only supplied fields are changed).

Request (all fields optional):
```json
{
  "folder_id": 3,
  "read": true,
  "flagged": false
}
```

Response `200`: updated message summary (same shape as list item).

#### `PATCH /api/v1/messages`

Bulk update. Apply the same patch to multiple messages.

Request:
```json
{
  "ids": [17, 18, 19],
  "folder_id": 4,
  "read": true
}
```

Response `200`:
```json
{ "updated": 3 }
```

#### `DELETE /api/v1/messages/{id}`

If the message is not in Trash, move it to Trash. If it is already in Trash, permanently delete it.

Response `204`.

#### `DELETE /api/v1/messages`

Bulk delete. Same two-step semantics as single delete.

Request:
```json
{ "ids": [17, 18] }
```

Response `200`:
```json
{ "deleted": 2 }
```

#### `DELETE /api/v1/folders/{id}/messages`

Delete all messages in a folder. Applies the same two-step semantics as single delete: messages not already in Trash are moved to Trash; messages already in Trash are permanently deleted. This means "Empty Trash" (`DELETE /api/v1/folders/4/messages`) permanently deletes everything in Trash, while "Empty Junk" (`DELETE /api/v1/folders/7/messages`) moves all Junk messages to Trash.

Returns `400` for hidden folders (Scheduled, Snoozed).

Response `200`:
```json
{ "deleted": 42 }
```

#### `POST /api/v1/messages/send`

Compose and send a new message, or schedule it for future delivery.

Request:
```json
{
  "identity_id": 2,
  "to_addr": "Alice <alice@example.com>",
  "cc_addr": "",
  "bcc_addr": "",
  "reply_to_addr": "",
  "subject": "Re: Hello",
  "body_text": "Hi Alice,\n...",
  "body_html": "<p>Hi Alice,</p>...",
  "in_reply_to": "<abc@example.com>",
  "references": ["<older@example.com>", "<abc@example.com>"],
  "send_at": "2026-04-01T09:00:00Z"
}
```

- `identity_id` is optional; if absent, the default identity is used. Returns `400` if the supplied ID does not exist.
- The `From` header is constructed as `"Name" <address>` from the chosen identity.
- At least one of `body_text` or `body_html` must be non-empty.
- Attachments in the send flow are handled via a separate endpoint (see §5.3).
- **If `send_at` is absent or null**: send immediately. The handler builds and pipes the message to `sendmail -t -oi`. If `sendmail` exits non-zero, return `500` with captured stderr. The stored copy goes to Sent with `read=true`.
- **If `send_at` is a future RFC 3339 timestamp**: do not send now. Store the message in the Scheduled folder with `send_at` set. The background scheduler will send it at the specified time (see §7). Returns `202 Accepted`. `send_at` must be at least 1 minute in the future; returns `400` otherwise.

Response `201` (sent immediately):
```json
{ "id": 23 }
```

Response `202` (scheduled):
```json
{ "id": 24, "send_at": "2026-04-01T09:00:00Z" }
```

#### `POST /api/v1/messages/send-with-attachments`

Same as `/messages/send` but uses `multipart/form-data`. The JSON fields are submitted as a `message` part (content-type `application/json`); each attachment is a separate file part.

Response `201`/`202`: same as `/messages/send`.

#### `DELETE /api/v1/scheduled/{id}`

Cancel a scheduled (not-yet-sent) message. Moves it to Drafts and clears `send_at`, so the user can edit and reschedule or discard it.

Returns `400` if the message has already been sent (i.e. is no longer in the Scheduled folder).

Response `200`:
```json
{ "id": 24, "folder_id": 3 }
```

#### `POST /api/v1/messages/import`

Store a raw RFC 5322 message (submitted as request body with `Content-Type: message/rfc822`) directly into a folder. Intended for testing and manual import.

Query parameter: `folder_id` (default: Inbox).

Filters are **not** applied (this is a direct import).

Response `201`:
```json
{ "id": 25 }
```

---

### 5.3 Draft Management

Drafts are regular messages in the Drafts folder (`folder_id=3`). The compose UI saves drafts via `PATCH /api/v1/messages/{id}` (updating the stored draft) or creates new ones via a simplified endpoint:

#### `POST /api/v1/drafts`

Save a new draft. Same request body as `/messages/send` but nothing is sent.

Response `201`:
```json
{ "id": 27, "updated_at": "2026-03-30T12:00:00Z" }
```

#### `POST /api/v1/drafts-with-attachments`

Same as `POST /api/v1/drafts` but uses `multipart/form-data`. The JSON fields are submitted as a `message` part (content-type `application/json`); each attachment is a separate file part. Use this endpoint when the compose UI auto-saves a draft that has one or more files attached.

Response `201`:
```json
{ "id": 28, "updated_at": "2026-03-30T12:00:00Z" }
```

#### `DELETE /api/v1/drafts/{id}`

Permanently delete a draft (no Trash step).

Response `204`.

---

### 5.4 Snooze

Snoozing a message temporarily hides it from the Inbox and returns it at a specified future time, triggering the same new-message notification as a freshly delivered message.

#### `POST /api/v1/messages/{id}/snooze`

Snooze a message. The message must currently be in Inbox (or a user folder — snoozing is not valid for Sent, Drafts, Trash, Scheduled, or Snoozed). Returns `400` otherwise.

Request:
```json
{ "until": "2026-04-02T08:00:00Z" }
```

- `until` must be a future RFC 3339 timestamp, at least 1 minute ahead. Returns `400` otherwise.
- The message's current `folder_id` is saved to `snooze_folder` (so it returns to the right place).
- `folder_id` is changed to Snoozed (id=6) and `snoozed_until` is set.
- `read` is unchanged.

Response `200`:
```json
{
  "id": 17,
  "folder_id": 6,
  "snoozed_until": "2026-04-02T08:00:00Z"
}
```

#### `DELETE /api/v1/messages/{id}/snooze`

Cancel a snooze early, returning the message to its original folder immediately. `snoozed_until` and `snooze_folder` are cleared.

Returns `400` if the message is not currently snoozed.

Response `200`:
```json
{ "id": 17, "folder_id": 1 }
```

---

### 5.5 Filters

#### `GET /api/v1/filters`

Returns all filters ordered by `position`.

Response `200`:
```json
[
  {
    "id": 1,
    "position": 0,
    "name": "GitHub notifications",
    "match_from": "notifications@github.com",
    "match_to": "",
    "match_subject": "",
    "action": "move",
    "folder_id": 5,
    "stop": true
  }
]
```

#### `POST /api/v1/filters`

Create a filter. At least one of `match_from`, `match_to`, `match_subject` must be non-empty.

Request:
```json
{
  "position": 0,
  "name": "GitHub notifications",
  "match_from": "notifications@github.com",
  "action": "move",
  "folder_id": 5,
  "stop": true
}
```

Response `201`: filter object.

#### `PUT /api/v1/filters/{id}`

Replace a filter entirely.

Request: same shape as POST.

Response `200`: updated filter object.

#### `DELETE /api/v1/filters/{id}`

Response `204`.

#### `POST /api/v1/filters/reorder`

Update `position` values for multiple filters at once.

Request:
```json
{ "ids": [3, 1, 2] }
```

The supplied order becomes the new `position` sequence (0, 1, 2, …).

Response `200`:
```json
{ "updated": 3 }
```

---

### 5.6 Spam Filter

#### `GET /api/v1/spam-filter`

Returns the current spam filter settings.

Response `200`:
```json
{
  "enabled": true,
  "score_header": "X-Spam-Score",
  "score_threshold": 5.0
}
```

#### `PUT /api/v1/spam-filter`

Replace spam filter settings entirely.

Request:
```json
{
  "enabled": true,
  "score_header": "X-Spam-Score",
  "score_threshold": 5.0
}
```

- `score_threshold` must be a positive number. Returns `400` otherwise.
- `score_header` must be a non-empty string. Returns `400` otherwise.

Response `200`: updated settings object.

#### `POST /api/v1/messages/{id}/mark-junk`

Manually mark a message as junk. Moves it to the Junk folder (id=7). Marks it as read.

Returns `400` if the message is already in the Junk folder.

Response `200`:
```json
{ "id": 17, "folder_id": 7 }
```

#### `POST /api/v1/messages/{id}/mark-not-junk`

Mark a message as not junk. Moves it from the Junk folder back to Inbox (id=1). Marks it as unread.

Returns `400` if the message is not currently in the Junk folder.

Response `200`:
```json
{ "id": 17, "folder_id": 1 }
```

---

### 5.8 Thread View

A thread is a group of messages linked by `In-Reply-To` / `References` headers. The API does not store threads explicitly; they are computed on demand.

#### `GET /api/v1/messages/{id}/thread`

Returns all messages in the same thread as `{id}`, ordered by `date` ascending. Each entry has the same shape as items in the folder message list (i.e. summary only — no `body_text`, `body_html`, `raw`, or `attachments`). The UI fetches the full message via `GET /api/v1/messages/{id}` when the user selects a message in the thread.

Response `200`:
```json
{
  "messages": [
    {
      "id": 12,
      "folder_id": 1,
      "message_id": "<prev@example.com>",
      "from_addr": "Alice <alice@example.com>",
      "to_addr": "Bob <bob@example.com>",
      "subject": "Re: Hello",
      "date": "2026-03-28T09:00:00Z",
      "read": true,
      "flagged": false,
      "has_attachments": false,
      "created_at": "2026-03-28T09:01:02Z"
    },
    {
      "id": 17,
      "folder_id": 1,
      "message_id": "<abc@example.com>",
      "from_addr": "Bob <bob@example.com>",
      "to_addr": "Alice <alice@example.com>",
      "subject": "Re: Hello",
      "date": "2026-03-29T10:00:00Z",
      "read": true,
      "flagged": false,
      "has_attachments": true,
      "created_at": "2026-03-29T10:01:05Z"
    }
  ]
}
```

Thread reconstruction algorithm:
1. Starting from the message's `message_id`, collect all `message_id` values in its `references` chain.
2. Query for any message whose `message_id`, `in_reply_to`, or `references` overlaps with the collected set.
3. Union and sort by `date`.

---

### 5.9 Identities

#### `GET /api/v1/identities`

Returns all identities ordered by `position`, then `id`.

Response `200`:
```json
[
  {
    "id": 1,
    "name": "Alice Doe",
    "address": "alice@example.com",
    "is_default": true,
    "position": 0,
    "signature": "Alice Doe\nalice@example.com"
  },
  {
    "id": 2,
    "name": "Alice Doe (work)",
    "address": "alice@corp.example.com",
    "is_default": false,
    "position": 1,
    "signature": ""
  }
]
```

#### `POST /api/v1/identities`

Create an identity.

Request:
```json
{
  "name": "Alice Doe (work)",
  "address": "alice@corp.example.com",
  "is_default": false,
  "position": 1,
  "signature": "Alice Doe\nalice@corp.example.com"
}
```

- `name` must be non-empty.
- `address` must be a valid RFC 5322 `addr-spec`. Returns `409` if the address already exists.
- If `is_default` is `true`, all other identities are set to `is_default=false` in the same transaction.
- `signature` is optional; defaults to empty string if absent.

Response `201`: identity object.

#### `PUT /api/v1/identities/{id}`

Replace an identity entirely. Same validation rules as POST.

If `is_default` is set to `true`, clears `is_default` on all other identities. If `is_default` is set to `false` and this is the only identity, returns `400`.

Response `200`: updated identity object.

#### `DELETE /api/v1/identities/{id}`

Delete an identity. Returns `400` if it is the only identity.

If the deleted identity was the default, the identity with the lowest `position` (then lowest `id`) among remaining identities is promoted to default.

Response `204`.

#### `POST /api/v1/identities/reorder`

Update `position` values for multiple identities at once.

Request:
```json
{ "ids": [2, 1, 3] }
```

The supplied order becomes the new `position` sequence (0, 1, 2, …).

Response `200`:
```json
{ "updated": 3 }
```

---

### 5.10 Contacts

#### `GET /api/v1/contacts`

List all contacts ordered by `name` ascending (empty names sorted last), then `address` ascending.

Query parameters:

| Parameter | Type   | Description                                                        |
|-----------|--------|--------------------------------------------------------------------|
| `q`       | string | Filter to contacts whose `address` or `name` contains the value (case-insensitive substring). Used for autocomplete. |
| `limit`   | int    | Max contacts to return (default 50, max 200)                       |
| `offset`  | int    | Pagination offset (default 0)                                      |

Response `200`:
```json
{
  "total": 2,
  "contacts": [
    { "id": 1, "address": "alice@example.com", "name": "Alice Doe" },
    { "id": 2, "address": "bob@example.com",   "name": "" }
  ]
}
```

#### `POST /api/v1/contacts`

Create a contact.

Request:
```json
{ "address": "carol@example.com", "name": "Carol" }
```

- `address` must be a valid RFC 5322 `addr-spec`. Returns `409` if the address already exists.
- `name` is optional; defaults to empty string.

Response `201`: contact object.

#### `PUT /api/v1/contacts/{id}`

Replace a contact entirely.

Request:
```json
{ "address": "carol@example.com", "name": "Carol Smith" }
```

Same validation as POST. Returns `409` if the new address conflicts with a different existing contact.

Response `200`: updated contact object.

#### `DELETE /api/v1/contacts/{id}`

Delete a contact.

Response `204`.

---

## 6. Local Delivery Agent (LDA)

When invoked as `mymail -lda`, the program:

1. Opens the SQLite database at `<data>/mymail.sqlite` (creating it if necessary, running schema migrations).
2. Reads the raw message from **stdin** into memory.
3. Parses the RFC 5322 message:
   - Extracts headers: `Message-ID`, `From`, `To`, `Cc`, `Bcc`, `Reply-To`, `Subject`, `Date`, `In-Reply-To`, `References`, plus spam-related headers (`X-Spam-Flag`, `X-Spam-Status`, and the configured score header).
   - Decodes MIME structure:
     - Finds `text/plain` part → `body_text`
     - Finds `text/html` part → `body_html` (inline `cid:` images resolved and sanitized before storage — see below)
     - Collects inline image parts (MIME parts with a `Content-ID` header referenced by `cid:` in the HTML body) and embeds them into `body_html` as `data:` URIs (see §10). These parts are **not** stored in the `attachments` table.
     - Collects remaining `attachment` parts (Content-Disposition: attachment, or non-displayable parts without a Content-ID reference) → stored in `attachments` table
   - Falls back: if no `Date` header, use current time. If no `Message-ID`, generate one.
   - Handles encoded words (RFC 2047) in headers.
4. Duplicate detection: if a `Message-ID` is present and a message with the same `Message-ID` already exists anywhere in the database, exit `0` silently. This prevents double-storage when the MTA retries delivery after a transient failure that was actually recovered.
5. Applies spam detection and user-defined filters (see §6.1).
6. Inserts the message record and attachments in a single transaction.
7. Upserts the sender into the `contacts` table: lower-case the `From` address; insert if not present, otherwise update `name` only if the existing `name` is empty.
8. Exits `0`.

### 6.1 Filter Application

Delivery proceeds in two phases:

**Phase 1 — Spam detection** (if the spam filter is enabled):

Inspect the parsed message headers to determine `is_spam`. Any of the following independently triggers `is_spam=true`:

- `X-Spam-Flag` header equals `YES` (case-insensitive).
- `X-Spam-Status` header value starts with `Yes` (case-insensitive).
- The configured `score_header` (default `X-Spam-Score`) is present and its numeric value is ≥ `score_threshold` (default 5.0).

Set the initial delivery folder: `folder_id = 7` (Junk) if `is_spam`, else `folder_id = 1` (Inbox).

If the spam filter is disabled, set `folder_id = 1` (Inbox) unconditionally.

**Phase 2 — User-defined filters:**

Filters are loaded from the database ordered by `position ASC`. For each filter:

1. Check each non-empty match field as a **case-insensitive substring** search against the corresponding parsed header.
2. All non-empty criteria must match (AND logic).
3. On match:
   - `move` → set `folder_id` to the filter's `folder_id`
   - `trash` → set `folder_id` to Trash (id=4)
   - `mark_read` → set `read=1` (does not change `folder_id`)
   - `drop` → exit immediately without inserting anything; return exit code `0` to the MTA (the message is silently discarded, not bounced)
4. If `stop=1`, halt filter evaluation. (`drop` always implies stop — continuing after a drop is meaningless.)

The final `folder_id` after both phases determines where the message is stored. This means user-defined filters can rescue a spam-tagged message from Junk (e.g. a filter matching a known-good sender with `action=move` targeting Inbox) or can send a non-spam message directly to Junk (`action=move, folder_id=7`).

`mark_read` does not alter the folder chosen by the spam filter; it only sets the read flag.

### 6.2 LDA Error Handling

- Database locked (SQLite `SQLITE_BUSY`): retry up to 30 seconds with exponential backoff, then exit `75` (temporary failure — MTA will re-deliver).
- Parse failure: log to stderr, exit `1` (permanent failure — message bounces; prevents silent loss).
- All other errors: log to stderr, exit `75`.

---

## 7. Background Scheduler

The server starts a single background goroutine on startup that processes deferred sends and snooze expiries. It wakes up every 60 seconds, queries the database for due items, and processes them.

### 7.1 Deferred Send

```sql
SELECT id FROM messages
WHERE folder_id = 5          -- Scheduled folder
  AND send_at <= CURRENT_TIMESTAMP
ORDER BY send_at ASC;
```

For each result, in order:

1. Build the RFC 5322 message from the stored fields (same logic as immediate send in §9).
2. Pipe to `sendmail -t -oi`.
3. **On success**: set `folder_id = 2` (Sent), `read = 1`, `send_at = NULL`, `send_error = NULL`, `send_failure_count = 0`.
4. **On failure** (non-zero sendmail exit): increment `send_failure_count`, set `send_error` to captured stderr (max 4 KB). Leave the message in the Scheduled folder. The scheduler will retry on the next tick. After 3 consecutive failures (`send_failure_count >= 3`), the message is moved to Drafts and `send_at` is cleared, so it is no longer retried. The `send_error` text remains visible in the message detail so the user knows what happened.

### 7.2 Snooze Expiry

```sql
SELECT id, snooze_folder FROM messages
WHERE folder_id = 6          -- Snoozed folder
  AND snoozed_until <= CURRENT_TIMESTAMP
ORDER BY snoozed_until ASC;
```

For each result:

1. Set `folder_id = snooze_folder`, `snoozed_until = NULL`, `snooze_folder = NULL`, `read = 0`.
2. Mark as unread (`read = 0`) so the polling notification logic treats it as a new arrival.

Setting `read = 0` means the next poll of `GET /api/v1/folders` will see an increased `unread_count` for the target folder (typically Inbox), triggering the same browser notification as a freshly delivered message (see §12).

### 7.3 Scheduler Robustness

- The scheduler holds the SQLite write lock only for the duration of each individual UPDATE, not across the full tick. This keeps the database available to the HTTP server between updates.
- If the server is offline when a `send_at` or `snoozed_until` deadline passes, the scheduler processes the overdue items on the next startup/tick. Deferred sends may go out late; snoozed messages will reappear late. This is acceptable given the "simple, let the MTA handle reliability" design philosophy.
- The scheduler goroutine is stopped cleanly on server shutdown via a context cancellation.

---

## 8. Batch Import

### 8.1 Supported Formats

#### mbox

A single file containing multiple messages concatenated. Each message begins with a `From ` separator line (note the trailing space) and extends to the next such line.

Four incompatible variants exist, differing in how `From ` occurrences within message bodies are escaped and how message boundaries are located:

| Variant     | Body escaping                                                                           | Boundary detection      | Used by                    |
|-------------|-----------------------------------------------------------------------------------------|-------------------------|----------------------------|
| **mboxo**   | Prepends `>` to bare `From ` lines (not reversible)                                     | `From ` scan            | Original Unix mail, Eudora |
| **mboxrd**  | Prepends `>` to any line starting with one or more `>` followed by `From ` (reversible) | `From ` scan            | qmail, Thunderbird export  |
| **mboxcl**  | Same as mboxrd                                                                          | `Content-Length` header | SVR4 Unix tools            |
| **mboxcl2** | No escaping (not needed)                                                                | `Content-Length` header | SVR4 variant               |

References:
- [RFC 4155 — The application/mbox Media Type](https://datatracker.ietf.org/doc/html/rfc4155)
- [Mbox format variants — Wikipedia](https://en.wikipedia.org/wiki/Mbox)
- [mbox man page — qmail.org](http://qmail.org/man/man5/mbox.html)

#### Maildir

Each message is stored as a separate file. A Maildir root contains three subdirectories: `new/` (unread, not yet moved by MUA), `cur/` (read or moved), `tmp/` (delivery scratch space, ignored on import). Maildir++ extends this with subdirectories named `.FolderName/` for subfolders.

References:
- [Maildir specification — cr.yp.to](https://cr.yp.to/proto/maildir.html)
- [Maildir — Wikipedia](https://en.wikipedia.org/wiki/Maildir)

#### MBX (not supported)

The UW-IMAP / Pine / Alpine native format. It uses a 2 KB binary file header, per-message metadata, and CRLF line endings. 
There is no public formal specification, no maintained Go library, and it is only used by UW-derived mail clients. It is **out of scope** for this importer; users with MBX files should first convert them with `mb2md` or a similar tool.

Reference: [MBX format description — faisal.com](https://www.faisal.com/docs/mbx.html)

### 8.2 Individual Message Format

Each message inside an mbox file or Maildir directory is an RFC 5322 Internet Message Format document.

Reference: [RFC 5322 — Internet Message Format](https://datatracker.ietf.org/doc/html/rfc5322)

Parsing uses the Go standard library's [`net/mail`](https://pkg.go.dev/net/mail) package for individual messages (header decoding, address parsing) combined with a third-party mbox reader for file-level splitting.

### 8.3 Go Libraries

#### mbox reading — `github.com/emersion/go-mbox`

- Package: [pkg.go.dev/github.com/emersion/go-mbox](https://pkg.go.dev/github.com/emersion/go-mbox)
- License: MIT · v1.0.4 (June 2025)
- Handles: mboxo and mboxrd (streaming `From ` scan). Sufficient for files produced by Thunderbird, Evolution, mutt, and Google Takeout.
- API: `mbox.NewReader(r io.Reader)` → `*Reader`; call `NextMessage() (io.Reader, error)` in a loop.

If auto-detection of all four variants is needed (e.g. files from SVR4-derived clients using `Content-Length`), use:

- Package: [pkg.go.dev/github.com/tvanriper/mbox](https://pkg.go.dev/github.com/tvanriper/mbox)
- License: MIT · v0.1.6 (June 2025, pre-v1)
- Handles: mboxo, mboxrd, mboxcl, mboxcl2 with `DetectType(io.ReadSeeker)` auto-detection.

**Decision**: use `emersion/go-mbox` as the primary dependency (stable v1, MIT, widely used). Note the variant limitation in user-facing documentation; add `tvanriper/mbox` only if SVR4 mboxcl support is later needed.

#### Maildir reading — `github.com/emersion/go-maildir`

- Package: [pkg.go.dev/github.com/emersion/go-maildir](https://pkg.go.dev/github.com/emersion/go-maildir)
- License: MIT · v0.6.0 (August 2024)
- API: `maildir.Dir(path)` → iterate with `Keys()`, open each with `Message(key) (io.Reader, error)`. Handles `new/` and `cur/` subdirectories. Flag parsing (Seen, Replied, Flagged, etc.) available via `Flags(key)`.
- Maildir++ subdirectories: each subfolder is a separate `maildir.Dir`; the caller is responsible for enumerating them by listing directories prefixed with `.`.

### 8.4 Import Implementation Notes

- Open the database and run schema migrations before importing.
- Wrap each source file/directory import in a single SQLite transaction for atomicity (if it fails mid-way, nothing from that source is partially committed).
- For Maildir, map the `S` (Seen) flag from the message filename to `read=1`.
- The mbox `From ` separator line is **not** part of the RFC 5322 message and must be stripped before storing the `raw` BLOB.
- mbox files can be large (multi-GB). Use the streaming `NextMessage()` API; do not load the entire file into memory.
- Preserve the original `Date` header as the message's `date` field. If absent, fall back to the mtime of the Maildir file (for Maildir) or the timestamp on the `From ` separator line (for mbox).

### 8.5 Pre-conversion with System Tools

Users with MBX files or other unsupported formats can pre-convert using standard Linux tools:

| Tool        | Package    | Purpose                                                      | Man page                                                                  |
|-------------|------------|--------------------------------------------------------------|---------------------------------------------------------------------------|
| `mb2md`     | `mb2md`    | Convert mbox files to Maildir                                | [Debian](https://manpages.debian.org/trixie/mb2md/mb2md.1.en.html)        |
| `formail`   | `procmail` | Split mbox into individual `.eml` files; reformat From-lines | [Debian](https://manpages.debian.org/unstable/procmail/formail.1.en.html) |
| `reformail` | `maildrop` | Split mbox, duplicate detection, header manipulation         | [Debian](https://manpages.debian.org/jessie/maildrop/reformail.1.en.html) |

---

## 9. Outgoing Mail

The send flow in the service layer:

1. Construct a MIME message:
   - `Date`: current time (RFC 5322 format)
   - `Message-ID`: generate `<uuid@domain>` where `domain` is the domain part of the selected sender's `From` address (e.g. if the identity's address is `alice@example.com`, use `example.com`)
   - `MIME-Version: 1.0`
   - Body: `multipart/alternative` with `text/plain` and/or `text/html` parts.
   - If attachments present: wrap in `multipart/mixed`.
   - Encode non-ASCII headers as RFC 2047 encoded words.
   - Encode attachment data as base64.
2. Open a pipe to `sendmail -t -oi` (the `-t` flag reads recipients from headers; `-oi` prevents a lone `.` line from ending the message).
3. Write the raw message to the pipe.
4. Close the pipe and wait for the process to exit.
5. On non-zero exit: capture stderr (max 4 KB) and return it as an error. No retries — the MTA owns queueing.
6. On success: upsert each recipient from To, Cc, and Bcc into the `contacts` table using the same rule as the LDA (lower-case address; insert if absent, update `name` only if stored `name` is empty).
7. Store the sent message in the Sent folder with the `Bcc` header intact in the raw BLOB. The `Bcc` header is intentionally preserved: it is hidden from recipients (the MTA strips it from the outgoing copies; `sendmail -t` does not re-send to addresses already delivered), but it remains visible to anyone with access to the sending account so the sender has a complete record of who received the message.

---

## 10. HTML Sanitization

Incoming HTML bodies and the HTML part of outgoing messages are sanitized using a library equivalent to [microcosm-cc/bluemonday](https://github.com/microcosm-cc/bluemonday) with a strict email-appropriate policy:

**Allowed elements:** `a`, `b`, `blockquote`, `br`, `code`, `del`, `em`, `h1`–`h6`, `hr`, `i`, `img`, `li`, `ol`, `p`, `pre`, `s`, `strong`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`

**Allowed attributes:**
- `href` on `a` (must be `http://`, `https://`, or `mailto:`)
- `src` on `img` (must be `http://`, `https://`, or `data:image/…;base64,…`; `cid:` references are resolved to `data:` URIs before sanitisation as described below)
- `alt` on `img`
- Standard formatting attributes: `align`, `colspan`, `rowspan`, `style` (restricted to the safe property list below)

**Stripped always:** `script`, `style` (standalone), `iframe`, `object`, `embed`, `form`, `input`

**Allowed CSS properties** (enforced on the `style` attribute; all others are stripped):

| Property           | Notes                        |
|--------------------|------------------------------|
| `color`            |                              |
| `background-color` |                              |
| `font-family`      |                              |
| `font-size`        |                              |
| `font-style`       |                              |
| `font-variant`     |                              |
| `font-weight`      |                              |
| `letter-spacing`   |                              |
| `line-height`      |                              |
| `text-align`       |                              |
| `text-decoration`  |                              |
| `text-indent`      |                              |
| `vertical-align`   |                              |
| `white-space`      |                              |
| `word-spacing`     |                              |
| `border`           | Shorthand                    |
| `border-color`     |                              |
| `border-style`     |                              |
| `border-width`     |                              |
| `border-collapse`  |                              |
| `border-spacing`   |                              |
| `padding`          | Shorthand and longhand sides |
| `margin`           | Shorthand and longhand sides |
| `width`            |                              |
| `max-width`        |                              |
| `height`           |                              |

**Explicitly forbidden regardless of property name:** any value containing `url(`, `expression(`, `-moz-binding`, or a CSS comment (`/*`). These are stripped at the value level before the property allowlist is checked. This prevents URL-based tracking and legacy IE CSS expression attacks.

**Not allowed:** `background` (shorthand, could include `background-image`), `position`, `display`, `overflow`, `content`, `z-index`, `opacity`, and all vendor-prefixed properties (`-webkit-*`, `-moz-*`, etc.).

### `cid:` inline image resolution

Before the HTML sanitiser runs, all `<img src="cid:...">` references in the HTML body are resolved to `data:` URIs using the MIME parts collected from the same message. The algorithm:

1. Build a map of `Content-ID` value → MIME part bytes for all inline image parts in the message (strip angle brackets from the Content-ID header value before keying, e.g. `<logo@example.com>` → `logo@example.com`).
2. For each `<img src="cid:<content-id>">` in the HTML body:
   - Look up `<content-id>` in the map (case-insensitive).
   - If found and the part's byte size is **≤ 1 MiB (1 048 576 bytes)**: replace the `src` value with `data:<content-type>;base64,<base64-encoded-bytes>`.
   - If found but larger than 1 MiB, or not found: remove the `src` attribute entirely (the browser renders a broken image placeholder).
3. After rewriting, run the HTML sanitiser as normal. The sanitiser then allows `data:image/…;base64,…` `src` values through.

This step runs at storage time (LDA and import), so `body_html` is stored with `data:` URIs already embedded. No MIME part data is stored separately for inline images.

---

## 11. Security Headers

All HTTP responses include:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; img-src 'self' https:; style-src 'self' 'unsafe-inline'
```

The CSP allows HTTPS images (for email HTML bodies rendered in the UI) but restricts scripts to `'self'`.

---

## 12. Authentication

Identical to mycal. See mycal's `internal/auth/` for the htpasswd implementation.

- Optional HTTP Basic Auth over all endpoints (API + static UI).
- Passwords stored as bcrypt hashes in an htpasswd file.
- If `-basic-auth-file` is not set, all requests are accepted without authentication.
- The LDA mode ignores authentication entirely (no HTTP involved).
- Creating the htpasswd file: `htpasswd -Bc htpasswd myuser`

### 12.1 CSRF Protection

All state-changing HTTP methods (POST, PUT, PATCH, DELETE) are protected by two layers:

**Layer 1 — Origin / Referer validation:**
The server rejects any state-changing request whose `Origin` header (or, if absent, the origin derived from the `Referer` header) does not match the server's own origin (`scheme://host:port`). Requests with neither header are also rejected. GET requests are exempt (they must be side-effect-free).

**Layer 2 — CSRF token:**
- On startup the server generates a cryptographically random 32-byte token (hex-encoded, 64 characters). It is held in memory and regenerated on each restart.
- The token is embedded in the main HTML page as `<meta name="csrf-token" content="...">` so that the JavaScript UI can read it.
- Every state-changing API request from the UI must include the header `X-CSRF-Token: <token>`.
- The server validates this header on every state-changing request. A missing or incorrect token returns `403 Forbidden`.
- The LDA mode and GET requests are fully exempt.

The token endpoint itself:

#### `GET /api/v1/csrf-token`

Returns the current CSRF token. Used by the UI on startup (as a fallback if the meta tag is unavailable). Subject to the same Basic Auth rules as all other endpoints.

Response `200`:
```json
{ "token": "a3f8...e91c" }
```

---

## 13. Web UI

### Technology Stack

Same approach as mycal:
- **No build step.** ES6 modules, import maps.
- **Preact** + **HTM** for reactive components (vendored).
- Plain CSS for styling.
- All assets embedded in the binary via `//go:embed`.

### Layout

```
+-------------------+-----------------------------------+
|  Folder list      |  Message list (subject, from,    |
|  (sidebar)        |  date, snippet)                  |
|                   +-----------------------------------+
|  - Inbox (3)      |  Message detail / compose pane   |
|  - Sent           |  (full headers, body, attachments)|
|  - Drafts         |                                  |
|  - Scheduled      |                                  |
|  - Snoozed        |                                  |
|  - Trash          |                                  |
|  - Junk           |                                  |
|  - [user folders] |                                  |
+-------------------+-----------------------------------+
|  [Compose]  [Search bar]                             |
+------------------------------------------------------|
```

The Scheduled folder is shown in the sidebar so the user can review and cancel pending sends. The Snoozed folder is also shown in the sidebar so the user can browse and cancel snoozed messages; individual messages in the Snoozed folder show a **Cancel snooze** button (calls `DELETE /api/v1/messages/{id}/snooze`).

### Views

1. **Folder view** — paginated message list for selected folder. Unread messages shown in bold. Click to open message detail. A **Mark all as read** button in the toolbar sends `PATCH /api/v1/messages` with all message IDs in the folder and `"read": true`.
2. **Message detail** — full headers, body, attachment download links. Shows thread if `references` chain exists. Reply/Reply All/Forward/Move/Delete/Snooze buttons. The **Snooze** button opens a small picker with quick presets (later today, tomorrow morning, next week) and a custom date/time option; submits `POST /api/v1/messages/{id}/snooze`. When a message has both `body_html` and `body_text`, the body is shown according to the user's **preferred view** setting (see Client-Side Storage), defaulting to HTML. A **Plain text / HTML** toggle button switches the view for the current message and updates the stored preference. If only one body type is present it is shown directly with no toggle. HTML is rendered in a sandboxed iframe (see §13 HTML Body Display); plain text is rendered as preformatted text.
3. **Compose / Reply / Reply All / Forward** — form with a **From** selector (dropdown of all identities, pre-selected to the default; or, when replying, to the identity whose address matches the original To/Cc), To, Cc, Bcc, Subject, plain-text body, optional HTML body toggle. File upload for attachments. A **Send later** toggle reveals a date/time picker for `send_at`; when set, the Send button becomes "Schedule". Auto-save to Drafts on a 30-second timer (scheduled messages auto-save to Drafts until explicitly scheduled). The To, Cc, and Bcc fields offer address autocomplete: as the user types, the UI queries `GET /api/v1/contacts?q=<input>` and shows a dropdown of matching name + address suggestions.

   Pre-population rules per action:

   | Field           | Reply                                                                                   | Reply All                                                                         | Forward                                                                                                                                                 |
   |-----------------|-----------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
   | **From**        | Identity whose address appears in the original To or Cc; falls back to default identity | Same as Reply                                                                     | Default identity                                                                                                                                        |
   | **To**          | Original `From` address                                                                 | Original `From` address                                                           | Empty                                                                                                                                                   |
   | **Cc**          | Empty                                                                                   | All addresses from original To + Cc, minus the chosen From identity's own address | Empty                                                                                                                                                   |
   | **Subject**     | `Re: <original subject>` (no double `Re:`)                                              | `Re: <original subject>` (no double `Re:`)                                        | `Fwd: <original subject>`                                                                                                                               |
   | **In-Reply-To** | Original `Message-ID`                                                                   | Original `Message-ID`                                                             | Empty                                                                                                                                                   |
   | **References**  | Original references + original `Message-ID`                                             | Original references + original `Message-ID`                                       | Empty                                                                                                                                                   |
   | **Attachments** | Empty                                                                                   | Empty                                                                             | Copies of all original attachments pre-populated as attachment rows (new rows referencing copied `attachments` records; the originals are not modified) |

   "No double `Re:`": if the original subject already starts with `Re:` (case-insensitive), it is used as-is.

   **Signature pre-population:** when the compose form opens, if the selected From identity has a non-empty `signature`, it is appended to the plain-text body preceded by `\n-- \n` (the standard signature delimiter). When the From identity is changed via the dropdown, the old identity's signature block (if present) is replaced with the new identity's signature. Reply and Reply-All prepend the quoted original message after the signature; Forward places the forwarded message after the signature.
4. **Message detail** (Scheduled folder) — shows the scheduled send time prominently. A **Cancel schedule** button calls `DELETE /api/v1/scheduled/{id}`, returning the message to Drafts for editing.
5. **Search** — global full-text search with results shown as a message list.
6. **Filter management** — CRUD UI for filters, with drag-to-reorder. The `match_to` field must be labelled **"To / Cc"** in the UI, because it matches against both the `To` and `Cc` headers.
7. **Folder management** — create/rename/delete/reorder user folders.
8. **Identity management** — CRUD UI for sender identities (name + address + signature + default flag), with drag-to-reorder. The default identity is marked visually; clicking a "Set default" button updates it. The signature field is a plain-text textarea; leave empty for no signature.
9. **Spam filter settings** — toggle to enable/disable the spam filter, numeric field for the score threshold, and text field for the score header name. Submits `PUT /api/v1/spam-filter`.
10. **Contact management** — paginated list of all contacts with name and address. Supports adding, editing, and deleting contacts. Queries `GET /api/v1/contacts`, `POST /api/v1/contacts`, `PUT /api/v1/contacts/{id}`, and `DELETE /api/v1/contacts/{id}`.

**Junk folder:** shown in the folder sidebar between Trash and user-created folders. The message detail view for messages in the Junk folder shows a **Not junk** button (calls `POST /api/v1/messages/{id}/mark-not-junk`) instead of the normal move controls. All other message views show a **Mark as junk** button (calls `POST /api/v1/messages/{id}/mark-junk`).

**Empty folder:** the Trash and Junk folder views show an **Empty** button in the toolbar. Clicking it prompts for confirmation, then calls `DELETE /api/v1/folders/{id}/messages`.

### HTML Body Display

Rendered in a sandboxed `<iframe srcdoc="...">` with `sandbox` and no additional tokens (maximum restriction: no scripts, no same-origin access, no forms, no popups). The sanitized HTML body is set as `srcdoc`. Links inside the email body must use `target="_blank"` and `rel="noopener noreferrer"` so they open in a new tab despite the sandbox; the sanitiser adds these attributes to all `<a href>` elements during sanitisation.

### New Message Notifications

The web UI polls `GET /api/v1/folders` every 30 seconds. When the `unread_count` for the Inbox folder increases compared to the previously known value, the UI:

1. Updates the unread badge on the Inbox entry in the folder sidebar.
2. Updates the `document.title` to include the unread count (e.g. `(3) mymail`).
3. If the user has granted the [Notifications API](https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API) permission, fires a browser notification: title `New mail`, body `You have N unread messages in Inbox`.

Permission is requested the first time the user opens the UI (a prompt is shown explaining why). If permission is denied, steps 1 and 2 still apply; only the browser notification is skipped. The permission state is not re-requested after an explicit denial.

Polling is suspended while the browser tab is hidden (`document.visibilityState === 'hidden'`) and resumes immediately when the tab becomes visible again.

### Client-Side Storage

Using `localStorage`:
- Selected folder
- Compose draft state (as fallback) — stored as a JSON object containing all compose field values, the server-assigned draft `id` (if one exists), and a `savedAt` RFC 3339 UTC timestamp written each time the auto-save fires. **Draft recovery on page reload:** if a `savedAt` timestamp is present in `localStorage` and a server draft `id` is recorded, the UI fetches the server draft and compares its `updated_at` with `savedAt`; whichever is newer is loaded into the compose form silently (no prompt). If only one source exists it is used directly. If neither exists, the compose form opens blank.
- Dark mode toggle
- Message list density preference
- Notification permission state (cached to avoid repeated `Notification.permission` lookups)
- **Preferred body view** (`"html"` or `"text"`; default `"html"`): controls which body part is shown first in message detail when both are present. Updated whenever the user clicks the Plain text / HTML toggle.

---

## 14. Go Dependencies

```
modernc.org/sqlite                  # Pure-Go SQLite (no CGO)
github.com/microcosm-cc/bluemonday  # HTML sanitization
golang.org/x/crypto                 # bcrypt for htpasswd
github.com/emersion/go-mbox         # mbox file reading (batch import)
github.com/emersion/go-maildir      # Maildir reading (batch import)
```

Parsing individual RFC 5322 messages: Go standard library (`net/mail`, `mime`, `mime/multipart`, `mime/quotedprintable`) — no third-party MIME library needed.

Building the binary: `go build -tags netgo` produces a single static binary with no CGO dependencies.

---

## 15. `go.mod`

```go
module github.com/mikaelstaldal/mymail

go 1.24

require (
    github.com/emersion/go-maildir v0.6.0
    github.com/emersion/go-mbox v1.0.4
    github.com/microcosm-cc/bluemonday v1.0.27
    golang.org/x/crypto v0.x.x
    modernc.org/sqlite v1.x.x
)
```

---

## 16. Key Design Decisions

### No IMAP/POP3
The application relies entirely on the host MTA for mail retrieval. This keeps the codebase simple and lets Postfix handle TLS, authentication, queuing, and delivery retries.

### Raw Message Storage
Every incoming and outgoing message is stored as a raw RFC 5322 BLOB. This makes it lossless and allows the original message to be downloaded at any time, regardless of parse errors or future schema changes.

### FTS5 Content Table
Using `content='messages'` makes the FTS index a "content table" — rows are stored in `messages`, and the FTS index stores only the search tokens. The trigger-based maintenance keeps them in sync. This avoids duplicating large body text in the FTS table.

### Attachment Storage in SQLite
Attachments are stored in SQLite BLOBs. For typical personal email workloads this is acceptable. A future optimization could store large attachments on disk and keep only a path reference in SQLite.

### Sendmail for Outgoing Mail
Using `/usr/sbin/sendmail` (or whatever `sendmail` resolves to in `PATH`) means outgoing mail benefits from the full MTA pipeline: queueing, TLS, DKIM signing, etc.

### Sender Identities
Identities are stored in the database and managed through the same API/UI as everything else — no config file needed. Storing them in SQLite (rather than a flat file) keeps all configuration in one place and makes the REST API the single source of truth. The "exactly one default" invariant is maintained in the service layer rather than as a SQL constraint so that the error message is human-readable.

### No Send Queue in mymail
If `sendmail` returns an error (e.g. MTA down), mymail returns an HTTP 500 to the client. The message is **not** stored in a retry queue. The user is expected to retry. This matches the "simple client, let the MTA work" philosophy.

### Scheduling via Polling, Not a Timer Queue
The scheduler uses a 60-second polling loop rather than a priority queue or `time.AfterFunc`. This is simpler, requires no persistent timer state across restarts, and is accurate enough for email scheduling where minute-level precision is sufficient. The partial-index on `send_at` and `snoozed_until` keeps the polling queries fast even with a large messages table.

### Snooze Restores Unread State
When a snoozed message returns to Inbox, `read` is forcibly set to `0` regardless of whether it was read before snoozing. This is intentional: the user asked to be reminded, so the message should behave like a new arrival (badge, document title, browser notification). If the user had read it before snoozing and does not want the notification, they can simply dismiss it.

### Filter Evaluation in LDA
Filters are evaluated in the LDA process at delivery time, not asynchronously. This is correct because filters need to run before the message lands in the Inbox (otherwise notifications or badge counts would be wrong). The LDA holds a short write lock on the database for the duration of a single message insertion.

### Header-Based Spam Detection
Rather than implementing a built-in Bayesian classifier or content scorer, mymail reads spam verdicts from headers that the MTA pipeline has already set (SpamAssassin, Rspamd, etc.). This keeps mymail simple and lets operators choose and tune their preferred spam analysis tool independently. The standard `X-Spam-Flag` / `X-Spam-Status` / `X-Spam-Score` headers are the de-facto interoperability interface for this purpose. No new Go dependency is needed — header inspection uses the already-parsed `net/mail` header map.

Spam detection runs in Phase 1 (before user filters) so that user filters run with knowledge of the spam verdict and can override the Junk destination for known-good senders. This mirrors how most MUA spam integrations work.

---

## 17. Production Deployment

mymail binds plain HTTP and delegates TLS termination, rate limiting, and access control to the operator's infrastructure. A separate `docs/deployment.md` document should be created covering:

- **TLS**: mymail must be placed behind a TLS-terminating reverse proxy (nginx, Caddy, etc.) before being exposed outside localhost. Using `-basic-auth-file` over plain HTTP leaks credentials; TLS is mandatory when Basic Auth is enabled.
- **Rate limiting**: The primary exposure from missing rate limiting is CPU exhaustion via repeated failed authentication attempts, not password guessing (bcrypt cost factor already limits guessing to ~10 attempts/second). The reverse proxy should apply a per-IP request rate limit (e.g. 20 req/s) to prevent this DoS vector.
- **Bind address**: When not behind a reverse proxy on the same host, use `-addr 127.0.0.1` to prevent accidental exposure on public interfaces.

---

## 18. Out of scope

- **Multiple mailboxes**: currently one SQLite file = one mailbox. Multi-user support would require either per-user databases or a `user_id` column throughout.
- **PGP/S-MIME**: not in scope for v1.
- **Push notifications** (browser): not in scope for v1; polling the message list every 30 seconds is sufficient.
- **Offline support / PWA**: not in scope.
- **Large attachment threshold**: if attachments > N MB become a problem, move to disk storage. Threshold TBD.
