# mymail — Specification

A self-hosted personal (single-user) email client with a Go backend, SQLite storage, REST API, and embedded web UI.
Designed to run on a Linux server alongside a mail system such as Postfix.

---

## Overview

mymail stores, organizes, and presents email. It does **not** speak IMAP/POP3 or SMTP directly. Instead:

- **Incoming mail** is delivered by the local MTA (Postfix, etc.) via a local delivery agent (LDA) mode.
- **Outgoing mail** is handed off to the system `sendmail` binary.
- The application is a single self-contained binary with an embedded web UI.

---

## Project Structure

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
├── docs/                     # documentation
└── data/                     # Runtime data directory (default)
```

Follows this layered architecture:
`handler → service → repository → SQLite`

---

## Command-Line Interface

```
mymail [flags]
mymail -lda [flags]
mymail -import -data <dir> <mapping>...
```

### Server mode (default)

| Flag                | Default             | Description                                            |
|---------------------|---------------------|--------------------------------------------------------|
| `-port`             | `8080`              | HTTP listen port (1–65535)                             |
| `-addr`             | `127.0.0.1`         | Bind address                                           |
| `-data`             | `data/`             | Data directory (stores `mymail.sqlite`)                |
| `-basic-auth-file`  | ``                  | Path to htpasswd file; if set, enables HTTP Basic Auth |
| `-basic-auth-realm` | `mymail`            | Auth realm shown to clients                            |
| `-sendmail`         | `sendmail`          | Path to the sendmail binary (looked up via PATH if not absolute) |

> **Security note:** If `-basic-auth-file` is not set, all requests are accepted without authentication. This mode is only safe when `-addr` is bound to a loopback address (`127.0.0.1` or `::1`), which is the default. Binding to any public interface without authentication exposes all email data to the network.
>
> **TLS and reverse proxy note:** mymail does not terminate TLS itself. For any deployment that is not loopback-only, place mymail behind a TLS-terminating reverse proxy (nginx, Caddy, etc.). HTTP Basic Auth transmits credentials in cleartext; it must not be used over plain HTTP on a non-loopback interface. Rate limiting (for brute-force protection, send abuse, etc.) is also the responsibility of the reverse proxy layer, not mymail itself.

Identities are managed entirely through the REST API (Identities) and the web UI. There is no CLI flag for the initial identity; the first identity is created via the web UI on first use (the compose view prompts the user if no identities exist).

### Import mode (`-import`)

```
mymail -import -data <dir> <mapping>...
```

Each `<mapping>` argument is a colon-separated triplet `<folder>:<format>:<path>`:

| Part       | Values                                                      | Description                                                          |
|------------|-------------------------------------------------------------|----------------------------------------------------------------------|
| `<folder>` | `inbox`, `sent`, `drafts`, `trash`, or any user-folder name | Target folder in mymail. Created automatically if it does not exist. |
| `<format>` | `mbox`, `maildir`                                           | Source format (see Batch Import for details)                                   |
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
- **Concurrency:** Running `-import` concurrently with a running server against the same data directory is not supported. Stop the server before running import.

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

## Database Schema

File: `<data>/mymail.sqlite`

**File permissions:** The data directory and the database file must be readable only by the user running mymail. On first run, mymail creates the data directory with mode `0700` and the database file with mode `0600`. Operators should verify these permissions if the data directory is pre-existing.

**SQLite configuration:** The server opens the database with `PRAGMA journal_mode=WAL` (Write-Ahead Logging) so that readers and writers can proceed concurrently. The LDA opens the database with a 30-second busy timeout (`PRAGMA busy_timeout=30000`) to handle contention with the HTTP server. The HTTP server uses a 5-second busy timeout.

All timestamps are stored as UTC RFC 3339 strings.

### Schema migrations

The database schema is versioned using `PRAGMA user_version`. On every startup the server reads the current version and applies any missing migrations in order, then sets `user_version` to the new value. Each migration is a plain SQL string executed in a transaction; if it fails the server aborts with a fatal error.

```
user_version 0  →  fresh database: run all CREATE TABLE / CREATE INDEX / CREATE TRIGGER statements
user_version 1  →  (reserved for first future migration)
```

**Current schema version: 1** (initial schema applied; no further migrations yet).

The migration runner pseudocode:

```
v = PRAGMA user_version
if v < 1:
    -- create all tables, indexes, triggers, seed built-in folders
    PRAGMA user_version = 1
if v < 2:
    -- future migration example: ALTER TABLE messages ADD COLUMN foo TEXT
    PRAGMA user_version = 2
...
```

Each `if` block is checked independently (not `else if`), so a single startup can apply multiple sequential migrations. Because `PRAGMA user_version` is set inside the same transaction as the DDL statements, a crash mid-migration leaves the version unchanged and the migration will be retried on next startup.

### `folders`

```sql
CREATE TABLE IF NOT EXISTS folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,   -- display name, e.g. "Work"
    slug       TEXT    NOT NULL UNIQUE,   -- URL-safe key, e.g. "work"
    position   INTEGER NOT NULL DEFAULT 0  -- display order
);
```

**Built-in folders** (created on first run, protected from deletion):

| id | name      | slug      | position | Notes                                               |
|----|-----------|-----------|----------|-----------------------------------------------------|
| 1  | Inbox     | inbox     | 0        |                                                     |
| 2  | Sent      | sent      | 1        |                                                     |
| 3  | Drafts    | drafts    | 2        |                                                     |
| 4  | Trash     | trash     | 3        |                                                     |
| 5  | Scheduled | scheduled | 4        | Visible in sidebar; messages awaiting deferred send |
| 6  | Snoozed   | snoozed   | 5        | Visible in sidebar; messages awaiting snooze expiry |
| 7  | Junk      | junk      | 6        | Spam messages; visible in sidebar                   |

User-created folders have `id >= 100`.

### `messages`

```sql
CREATE TABLE IF NOT EXISTS messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    folder_id     INTEGER NOT NULL REFERENCES folders(id),
    message_id    TEXT UNIQUE,             -- RFC 5322 Message-ID header value (NULL rows are excluded from the UNIQUE constraint by SQLite semantics)
    in_reply_to   TEXT,                    -- In-Reply-To header value
    references    TEXT,                    -- References header value (space-separated); serialized as JSON array by the API
    from_addr     TEXT    NOT NULL,        -- From header (display name + address)
    to_addr       TEXT    NOT NULL,        -- To header (may contain multiple)
    cc_addr       TEXT    NOT NULL DEFAULT '',
    bcc_addr      TEXT    NOT NULL DEFAULT '',
    reply_to_addr TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    date          TEXT    NOT NULL,        -- RFC 3339 UTC timestamp (from Date header)
    body_text     TEXT    NOT NULL DEFAULT '', -- plain-text part (if absent, derived from body_html by stripping tags)
    body_html     TEXT    NOT NULL DEFAULT '', -- HTML part (sanitized on storage)
    raw           BLOB    NOT NULL,        -- original raw RFC 5322 message
    read          INTEGER NOT NULL DEFAULT 0, -- 0=unread, 1=read
    flagged       INTEGER NOT NULL DEFAULT 0, -- 0=normal, 1=starred/flagged
    has_attachments INTEGER NOT NULL DEFAULT 0, -- denormalized; 1 if any rows exist in attachments for this message
    send_at       TEXT,                    -- RFC 3339 UTC; non-NULL = deferred send, message sits in Scheduled folder
    snoozed_until TEXT,                    -- RFC 3339 UTC; non-NULL = snoozed, message sits in Snoozed folder
    snooze_folder INTEGER,                 -- folder_id to return to when snooze expires (usually Inbox=1)
    send_error    TEXT,                    -- last sendmail error for a scheduled message that failed to send
    send_failure_count INTEGER NOT NULL DEFAULT 0, -- consecutive send failures; message moved to Drafts after 3; also non-zero for messages in Drafts that were moved there after exhausting retries
    created_at    TEXT    NOT NULL,        -- RFC 3339 UTC, time of storage
    updated_at    TEXT    NOT NULL         -- RFC 3339 UTC, time of last modification (set equal to created_at on insert)
);

CREATE INDEX IF NOT EXISTS idx_messages_folder_id    ON messages(folder_id);
CREATE INDEX IF NOT EXISTS idx_messages_date         ON messages(date);
CREATE INDEX IF NOT EXISTS idx_messages_message_id   ON messages(message_id);
CREATE INDEX IF NOT EXISTS idx_messages_read         ON messages(read);
CREATE INDEX IF NOT EXISTS idx_messages_send_at      ON messages(send_at) WHERE send_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_snoozed_until ON messages(snoozed_until) WHERE snoozed_until IS NOT NULL;

-- Keep updated_at current on every write
CREATE TRIGGER IF NOT EXISTS messages_updated_at AFTER UPDATE ON messages BEGIN
    UPDATE messages SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = new.id;
END;

-- Maintain has_attachments denormalized flag
CREATE TRIGGER IF NOT EXISTS attachments_insert_flag AFTER INSERT ON attachments BEGIN
    UPDATE messages SET has_attachments = 1 WHERE id = new.message_id;
END;
CREATE TRIGGER IF NOT EXISTS attachments_delete_flag AFTER DELETE ON attachments BEGIN
    UPDATE messages SET has_attachments = (
        SELECT CASE WHEN EXISTS (SELECT 1 FROM attachments WHERE message_id = old.message_id) THEN 1 ELSE 0 END
    ) WHERE id = old.message_id;
END;
```

### `messages_fts` (FTS5)

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

-- Note: body_html is not indexed directly. When a message has no plain-text part, body_text is
-- populated by stripping HTML tags from body_html at storage time (see LDA section), so all
-- message content is searchable via body_text regardless of the original MIME structure.
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

-- Note: messages_updated_at fires BEFORE this trigger (it is defined earlier in the schema).
-- Both triggers use the old.* and new.* pseudo-rows which reflect pre- and post-update column
-- values regardless of trigger execution order.
CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF from_addr, to_addr, cc_addr, subject, body_text ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
    INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
END;
```

### `attachments`

Only MIME parts with `Content-Disposition: attachment` (or with no `Content-Disposition` and a non-displayable `Content-Type`) are stored here. Inline image parts referenced by `cid:` URLs in the HTML body are **not** stored as attachments; they are embedded as `data:` URIs directly into `body_html` at storage time (see Local Delivery Agent (LDA) and HTML Sanitization). This avoids storing the same bytes twice.

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

### `identities`

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

The first identity is created via the web UI on first use (the compose view prompts the user if no identities exist). There is no seeded default.

### `contacts`

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

### `filters`

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
    action        TEXT    NOT NULL,             -- "move", "trash", "mark_read", "drop"
    folder_id     INTEGER REFERENCES folders(id) ON DELETE SET NULL, -- required when action="move"; NULL after the target folder is deleted
    stop          INTEGER NOT NULL DEFAULT 1   -- 0=continue to next filter, 1=stop
);
```

**Note:** `match_to` performs a case-insensitive substring match against **both** the `To` and the `Cc` headers of the incoming message. A filter matches if the substring is found in either header.

**Non-empty criteria:** A match field (`match_from`, `match_to`, `match_subject`) is considered non-empty if it is not `NULL`, not `''`, and not a whitespace-only string (i.e. `TRIM(field) != ''`). Only non-empty fields are checked during filter evaluation. At least one of the three match fields must be non-empty when creating or updating a filter.

**`stop` type note:** `stop` is stored as `INTEGER` in the database (`1` = stop, `0` = continue) and exposed as `boolean` in the REST API.

**Actions:**
- `move` — deliver to `folder_id` instead of Inbox. If `folder_id` is NULL (because the target folder was deleted), the filter is skipped and a warning is logged; delivery continues to Inbox.
- `trash` — deliver directly to Trash
- `mark_read` — deliver to Inbox but mark as read
- `drop` — discard the message entirely; nothing is stored in the database

Multiple criteria within a filter are ANDed. Filters are evaluated in `position` order. When `stop=1` (default) the first matching filter wins and evaluation halts.

### `spam_filter_settings`

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

The `score_header` field specifies the header name to read the numeric score from. Header name comparison is case-insensitive, consistent with the MIME convention that header names are case-insensitive.

---

## REST API

**Base path:** `/api/v1`
**Full specification:** [`openapi.yaml`](openapi.yaml)

Use the OpenAPI specification as the source of truth and generate Go server stubs using [ogen](https://ogen.dev/).  

**Content type:** `application/json` for all request and response bodies, except attachment download endpoints.

**List endpoint response shapes:** Paginated list endpoints (those accepting `limit`/`offset` parameters) return a wrapper object `{"total": n, "<items>": [...]}` so clients can implement pagination. Non-paginated list endpoints (filters, identities) return a bare JSON array, since all items are always returned.

**Search snippet:** `GET /api/v1/messages/search` returns each result as `MessageSummary` extended with a `snippet` field — a short excerpt (up to 200 characters) of matching text from `body_text`, generated by the SQLite FTS5 `snippet()` function, with matched terms surrounded by `**` markers (e.g. `…the **keyword** in…`).

**Max request body:** 32 MiB (to accommodate raw message uploads; typical JSON operations limited to 1 MiB).

**RFC 5322 message parsing limit:** `POST /api/v1/messages/import` accepts a raw `message/rfc822` body. In addition to the global 32 MiB body cap, the RFC 5322 parser must enforce a **10 MiB limit on the total number of bytes consumed during parsing** (header fields + body parts) before returning an error. This prevents a crafted message with deeply nested MIME structure or extremely long header lines from causing memory exhaustion during parsing even if the raw bytes fit within the body cap.

**Bulk operation ID limits:** Bulk endpoints (`PATCH /api/v1/messages`, `DELETE /api/v1/messages`) accept at most 1000 message IDs per request. Requests exceeding this limit return 400.

**Error responses:** `{ "error": "human-readable message" }` with status `400`, `401`, `404`, `409`, or `500`.

**FTS search input:** The `q` parameter on `GET /api/v1/messages/search` is passed to SQLite FTS5 as a literal phrase match to prevent malformed FTS5 syntax from causing 500 errors. The exact transformation is:
1. Replace every `"` (U+0022) in the input with `""` (two double-quote characters).
2. Wrap the result in a single pair of outer double quotes.

Example: input `it's a "test"` becomes `"it's a ""test"""`. The escaping must be applied before any other transformation, must operate byte-by-byte (no locale-specific interpretation), and must cover the full Unicode range of the input string. A unit test must verify that inputs containing `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are all treated as literals.

### Endpoint summary

#### Folders
- `GET /api/v1/folders` — list all folders
- `POST /api/v1/folders` — create a user-defined folder
- `PATCH /api/v1/folders/{id}` — update folder name and/or position
- `DELETE /api/v1/folders/{id}` — delete a user-created folder (messages moved to Trash)
- `DELETE /api/v1/folders/{folder_id}/messages` — delete all messages in a folder

#### Messages
- `GET /api/v1/folders/{folder_id}/messages` — list messages in a folder
- `GET /api/v1/messages/search` — full-text search across all folders (supports optional `date_from`/`date_to` filters)
- `GET /api/v1/messages/{id}` — get full message details
- `GET /api/v1/messages/{id}/raw` — download original RFC 5322 message
- `GET /api/v1/messages/{id}/thread` — get all messages in the same thread
- `PATCH /api/v1/messages/{id}` — update message metadata (folder, read, flagged)
- `PATCH /api/v1/messages` — bulk update messages
- `DELETE /api/v1/messages/{id}` — delete message (to Trash, or permanently if already in Trash)
- `DELETE /api/v1/messages` — bulk delete messages
- `POST /api/v1/messages/send` — send or schedule a message
- `POST /api/v1/messages/send-with-attachments` — send/schedule with `multipart/form-data`
- `POST /api/v1/messages/import` — import a raw RFC 5322 message into a folder
- `POST /api/v1/messages/{id}/snooze` — snooze a message until a future time
- `DELETE /api/v1/messages/{id}/snooze` — cancel a snooze early
- `POST /api/v1/messages/{id}/mark-junk` — move to Junk and mark as read
- `POST /api/v1/messages/{id}/mark-not-junk` — move from Junk to Inbox

#### Attachments
- `GET /api/v1/attachments/{id}` — download attachment data

**Attachment download security:** The response always uses `Content-Type: application/octet-stream` regardless of the stored `content_type` value (which comes from the untrusted sender and may be attacker-controlled). The `Content-Disposition` header is constructed as follows:
- Strip all CR (`\r`), LF (`\n`), NUL (`\0`), and double-quote (`"`) characters from the filename to prevent response header injection and parameter delimiter breakout.
- If the sanitized filename contains only printable ASCII characters (U+0020–U+007E, excluding `"`, `\r`, `\n`, `\0`): emit `Content-Disposition: attachment; filename="<sanitized>"`.
- If the sanitized filename contains any non-ASCII characters (U+0080 and above): emit the RFC 8187 encoded form only — `Content-Disposition: attachment; filename*=UTF-8''<percent-encoded>` — where the percent-encoding follows RFC 3986 and encodes all octets outside the `attr-char` set defined in RFC 8187.

#### Scheduled messages
- `DELETE /api/v1/scheduled/{id}` — cancel a scheduled message (moves to Drafts)

#### Drafts
- `POST /api/v1/drafts` — save a new draft
- `POST /api/v1/drafts-with-attachments` — save a new draft with `multipart/form-data`
- `PUT /api/v1/drafts/{id}` — replace draft content
- `PUT /api/v1/drafts-with-attachments/{id}` — replace draft content with `multipart/form-data`
- `DELETE /api/v1/drafts/{id}` — permanently delete a draft
- `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}` — remove a single attachment from a draft

#### Filters
- `GET /api/v1/filters` — list all filters
- `POST /api/v1/filters` — create a filter
- `PUT /api/v1/filters/{id}` — replace a filter
- `DELETE /api/v1/filters/{id}` — delete a filter
- `POST /api/v1/filters/reorder` — reorder filters

#### Spam filter
- `GET /api/v1/spam-filter` — get spam filter settings
- `PUT /api/v1/spam-filter` — replace spam filter settings

#### Identities
- `GET /api/v1/identities` — list all identities
- `POST /api/v1/identities` — create an identity
- `PUT /api/v1/identities/{id}` — replace an identity
- `DELETE /api/v1/identities/{id}` — delete an identity
- `POST /api/v1/identities/reorder` — reorder identities

#### Contacts
- `GET /api/v1/contacts` — list contacts (supports autocomplete via `q` parameter)
- `POST /api/v1/contacts` — create a contact
- `PUT /api/v1/contacts/{id}` — replace a contact
- `DELETE /api/v1/contacts/{id}` — delete a contact

#### Health
- `GET /api/v1/health` — liveness check; returns 200 when the server is ready to serve requests

### Thread Algorithm

`GET /api/v1/messages/{id}/thread` determines membership using (in order):

1. **Header-based (primary):** Build a directed graph where message A links to message B if B's `Message-ID` appears in A's `In-Reply-To` or A's `References` header. Take the transitive closure to find all messages in the same connected component as the requested message.
2. **Subject-based fallback:** If header-based grouping yields only the single requested message (no links found), group by normalised subject: strip leading `Re:`, `Fwd:`, `Fw:` prefixes (any combination, case-insensitive) applied **repeatedly** until no recognized prefix remains (e.g. `Re: Fwd: Re: message` normalises to `message`), then compare the remainder case-insensitively.

Thread results include messages from all folders (Inbox, Sent, Trash, etc.) ordered by `date ASC`.

---

## Local Delivery Agent (LDA)

When invoked as `mymail -lda`, the program:

1. Opens the SQLite database at `<data>/mymail.sqlite` (creating it if necessary, running schema migrations).
2. Reads the raw message from **stdin** into memory.
3. Parses the RFC 5322 message:
   - Extracts headers: `Message-ID`, `From`, `To`, `Cc`, `Bcc`, `Reply-To`, `Subject`, `Date`, `In-Reply-To`, `References`, plus spam-related headers (`X-Spam-Flag`, `X-Spam-Status`, and the configured score header).
   - Decodes MIME structure:
     - Finds `text/plain` part → `body_text`
     - Finds `text/html` part → `body_html` (inline `cid:` images resolved and sanitized before storage — see below)
     - If no `text/plain` part is found but a `text/html` part is present, derive `body_text` by stripping HTML tags from the sanitized `body_html` (using the same bluemonday library in strip-all mode). This ensures all message content is full-text searchable regardless of MIME structure.
     - Collects inline image parts (MIME parts with a `Content-ID` header referenced by `cid:` in the HTML body) and embeds them into `body_html` as `data:` URIs (see HTML Sanitization). These parts are **not** stored in the `attachments` table.
     - Collects remaining `attachment` parts (Content-Disposition: attachment, or non-displayable parts without a Content-ID reference) → stored in `attachments` table
   - Falls back: if no `Date` header, use current time. If no `Message-ID`, generate one.
   - Handles encoded words (RFC 2047) in headers.
4. Duplicate detection: if a `Message-ID` is present, query `SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`. If the message already exists, log nothing and exit `0`. This early check avoids running spam detection and filters for messages that will not be stored. Messages without a `Message-ID` skip this check (they are always inserted).
5. Applies spam detection and user-defined filters (see Filter Application).
6. Inserts the message record and attachments in a single transaction using `INSERT OR IGNORE` on the `messages` table. The `UNIQUE` constraint on `message_id` acts as a race-safe guard: if two concurrent LDA processes attempt to deliver the same `Message-ID` simultaneously, only one will succeed; the other will find 0 rows inserted, log nothing, and exit `0`.
7. Upserts the sender into the `contacts` table: lower-case the `From` address; insert if not present, otherwise update `name` only if the existing `name` is empty. Only the `From` address is upserted for incoming mail — To and Cc recipients are not auto-added (to avoid polluting contacts with mailing list addresses).
8. Exits `0`.

### Filter Application

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

### LDA Error Handling

- Database locked (SQLite `SQLITE_BUSY`): retry up to 30 seconds with exponential backoff, then exit `75` (temporary failure — MTA will re-deliver).
- Parse failure: log to stderr, exit `1` (permanent failure — message bounces; prevents silent loss).
- All other errors: log to stderr, exit `75`.

---

## Background Scheduler

The server starts a single background goroutine on startup that processes deferred sends and snooze expiries. It wakes up every 60 seconds, queries the database for due items, and processes them.

### Deferred Send

```sql
SELECT id FROM messages
WHERE folder_id = 5          -- Scheduled folder
  AND send_at <= CURRENT_TIMESTAMP
ORDER BY send_at ASC;
```

For each result, in order:

1. Build the RFC 5322 message from the stored fields (same logic as immediate send in Outgoing Mail).
2. Pipe to `sendmail -t -oi` (maximum 30-second timeout; treat timeout as a sendmail failure).
3. **On success**: set `folder_id = 2` (Sent), `read = 1`, `send_at = NULL`, `send_error = NULL`, `send_failure_count = 0`. Resetting `send_failure_count` to 0 on success is what makes failures "consecutive" — only an unbroken run of failures without an intervening success counts toward the 3-failure limit that moves the message to Drafts.
4. **On failure** (non-zero sendmail exit): increment `send_failure_count`, set `send_error` to captured stderr (max 4 KB). Leave the message in the Scheduled folder. The scheduler will retry on the next tick. The UI shows a **yellow warning badge** on messages in the Scheduled folder when `send_failure_count >= 1`. After 3 consecutive failures (`send_failure_count >= 3`), the message is moved to Drafts (`folder_id = 3`, `read = 1`) and `send_at` is cleared, so it is no longer retried. The `send_error` text remains visible in the message detail so the user knows what happened. The UI shows a **red error badge** on messages in the Drafts folder when `send_failure_count >= 3` (indicating the message was moved from Scheduled after exhausting all retries).

### Snooze Expiry

```sql
SELECT id, snooze_folder FROM messages
WHERE folder_id = 6          -- Snoozed folder
  AND snoozed_until <= CURRENT_TIMESTAMP
ORDER BY snoozed_until ASC;
```

For each result:

1. Set `folder_id = COALESCE(snooze_folder, 1)` (falling back to Inbox if `snooze_folder` is NULL), `snoozed_until = NULL`, `snooze_folder = NULL`, `read = 0`. If `snooze_folder` pointed to a user folder that was deleted while the message was snoozed, the `COALESCE` fallback to Inbox (id=1) is intentional — the message reappears in Inbox rather than being lost or orphaned.
2. Mark as unread (`read = 0`) so the polling notification logic treats it as a new arrival.

Setting `read = 0` means the next poll of `GET /api/v1/folders` will see an increased `unread_count` for the target folder (typically Inbox), triggering the same browser notification as a freshly delivered message (see New Message Notifications).

### Scheduler Robustness

- The scheduler holds the SQLite write lock only for the duration of each individual UPDATE, not across the full tick. This keeps the database available to the HTTP server between updates.
- **Re-entrance guard:** The scheduler uses a mutex so that if a tick takes longer than 60 seconds (e.g. `sendmail` is slow), the next tick is skipped entirely rather than running concurrently. This prevents double-sends.
- **Conditional UPDATE:** Before sending, the scheduler UPDATE uses `WHERE send_at IS NOT NULL AND folder_id = 5` so that a message cancelled concurrently by the HTTP handler (which clears `send_at` and moves it to Drafts) is not sent.
- If the server is offline when a `send_at` or `snoozed_until` deadline passes, the scheduler processes the overdue items on the next startup/tick. Deferred sends may go out late; snoozed messages will reappear late. This is acceptable given the "simple, let the MTA handle reliability" design philosophy.
- The scheduler goroutine is stopped cleanly on server shutdown via a context cancellation.

---

## Batch Import

### Supported Formats

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

### Individual Message Format

Each message inside an mbox file or Maildir directory is an RFC 5322 Internet Message Format document.

Reference: [RFC 5322 — Internet Message Format](https://datatracker.ietf.org/doc/html/rfc5322)

Parsing uses the Go standard library's [`net/mail`](https://pkg.go.dev/net/mail) package for individual messages (header decoding, address parsing) combined with a third-party mbox reader for file-level splitting.

### Go Libraries

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

### Import Implementation Notes

- Open the database and run schema migrations before importing.
- Each source file/directory is imported using batched transactions: commit every 500 messages. This bounds the WAL file size and prevents the write lock from being held for the full duration of large imports. If a batch fails, only that batch is rolled back; previously committed batches are retained. A warning is printed for the failed batch.
- The full LDA parsing pipeline runs for each imported message: HTML sanitization, `cid:` inline image resolution, `body_text` derivation from `body_html` when no plain-text part exists, and attachment extraction. The only steps skipped are spam detection and user-defined filter application (messages go directly to the target folder specified in the mapping argument).
- For Maildir, map the `S` (Seen) flag from the message filename to `read=1`.
- The mbox `From ` separator line is **not** part of the RFC 5322 message and must be stripped before storing the `raw` BLOB.
- mbox files can be large (multi-GB). Use the streaming `NextMessage()` API; do not load the entire file into memory.
- Preserve the original `Date` header as the message's `date` field. If absent, fall back to the mtime of the Maildir file (for Maildir) or the timestamp on the `From ` separator line (for mbox).

### Pre-conversion with System Tools

Users with MBX files or other unsupported formats can pre-convert using standard Linux tools:

| Tool        | Package    | Purpose                                                      | Man page                                                                  |
|-------------|------------|--------------------------------------------------------------|---------------------------------------------------------------------------|
| `mb2md`     | `mb2md`    | Convert mbox files to Maildir                                | [Debian](https://manpages.debian.org/trixie/mb2md/mb2md.1.en.html)        |
| `formail`   | `procmail` | Split mbox into individual `.eml` files; reformat From-lines | [Debian](https://manpages.debian.org/unstable/procmail/formail.1.en.html) |
| `reformail` | `maildrop` | Split mbox, duplicate detection, header manipulation         | [Debian](https://manpages.debian.org/jessie/maildrop/reformail.1.en.html) |

---

## Outgoing Mail

The send flow in the service layer:

1. Construct a MIME message:
   - `Date`: current time (RFC 5322 format)
   - `Message-ID`: generate `<uuid@domain>` where `domain` is the domain part of the selected sender's `From` address (e.g. if the identity's address is `alice@example.com`, use `example.com`)
   - `MIME-Version: 1.0`
   - `Reply-To`: if `reply_to_addr` is non-empty, add a `Reply-To` header with that value; omit the header otherwise.
   - Body: `multipart/alternative` with `text/plain` and/or `text/html` parts.
   - If attachments present: wrap in `multipart/mixed`.
   - Encode non-ASCII headers as RFC 2047 encoded words.
   - Encode attachment data as base64.
   - **Header sanitization:** Before encoding, strip any CR (`\r`), LF (`\n`), and NUL (`\0`) characters from all user-supplied header values (subject, to_addr, cc_addr, bcc_addr, reply_to_addr). These control characters could otherwise inject spurious headers into the constructed MIME message.
2. Open a pipe to `sendmail -t -oi` (the `-t` flag reads recipients from headers; `-oi` prevents a lone `.` line from ending the message).
3. Write the raw message to the pipe.
4. Close the pipe and wait for the process to exit (maximum 30 seconds; treat timeout as an error).
5. On non-zero exit: capture stderr (max 4 KB) and return it as an error. No retries — the MTA owns queueing.
6. On success: upsert each recipient from To, Cc, and Bcc into the `contacts` table using the same rule as the LDA (lower-case address; insert if absent, update `name` only if stored `name` is empty).
7. Store the sent message in the Sent folder with the `Bcc` header intact in the raw BLOB. The `Bcc` header is intentionally preserved: it is hidden from recipients (the MTA strips it from the outgoing copies; `sendmail -t` does not re-send to addresses already delivered), but it remains visible to anyone with access to the sending account so the sender has a complete record of who received the message.

---

## HTML Sanitization

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
2. Enforce per-message limits before processing any `cid:` reference:
   - **Maximum inline images per message:** 64. If the message contains more than 64 `<img src="cid:...">` elements, all `cid:` `src` attributes are removed (the browser renders broken image placeholders) and no further resolution is attempted.
   - **Maximum total decoded byte size:** 10 MiB (10 485 760 bytes) across all resolved inline images in a single message. Inline images are resolved in document order; once the running total would exceed 10 MiB, that image and all subsequent `cid:` references have their `src` attribute removed.
3. For each `<img src="cid:<content-id>">` in the HTML body (subject to the limits above):
   - Look up `<content-id>` in the map (case-insensitive).
   - If found and the part's byte size is **≤ 1 MiB (1 048 576 bytes)**: replace the `src` value with `data:<content-type>;base64,<base64-encoded-bytes>`.
   - If found but larger than 1 MiB, or not found: remove the `src` attribute entirely (the browser renders a broken image placeholder).
4. After rewriting, run the HTML sanitiser as normal. The sanitiser then allows `data:image/…;base64,…` `src` values through.

This step runs at storage time (LDA and import), so `body_html` is stored with `data:` URIs already embedded. No MIME part data is stored separately for inline images.

---

## Security Headers

All HTTP responses include:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'
Strict-Transport-Security: max-age=31536000
```

The CSP blocks external image URLs (`https:` is intentionally absent from `img-src`). All inline images in email bodies are resolved to `data:` URIs at storage time (see `cid:` inline image resolution), so the `data:` source is sufficient. External images embedded in HTML email bodies (i.e. `<img src="https://...">` in the raw message) are blocked by default to prevent tracking pixels from revealing read receipts to arbitrary third-party servers.

A per-message opt-in for external images is provided via the `GET /api/v1/messages/{id}` response field `has_external_images` (boolean). When `true`, the UI may offer the user a "Load external images" action. If the user opts in, the frontend rerenders the message body in an isolated `<iframe>` with a relaxed CSP that permits `img-src https:` for that frame only; this must not affect the top-level document CSP.

The `Strict-Transport-Security` header instructs browsers to use HTTPS for all subsequent requests; it is harmless over HTTP (browsers ignore it) but enforced when deployed behind a TLS proxy.

---

## Authentication

- Optional HTTP Basic Auth over all endpoints (API + static UI).
- Passwords stored as bcrypt hashes in an htpasswd file, use Go library github.com/mikaelstaldal/go-server-common to read it.
- If `-basic-auth-file` is not set, all requests are accepted without authentication. **This mode must only be used when the server is bound to a loopback address.** See the security note in the CLI section.
- The LDA mode ignores authentication entirely (no HTTP involved).
- Creating the htpasswd file: `htpasswd -Bc htpasswd myuser`

### CSRF Protection

CSRF is a browser-specific threat: a malicious site exploiting the browser's automatic credential forwarding to make cross-origin requests on behalf of an authenticated user. Native clients (mobile apps, CLI tools) manage their own HTTP sessions and are not subject to this attack.

Note: HTTP Basic Auth has lower inherent CSRF risk than cookie-based auth because browsers do not automatically attach cached Basic Auth credentials to cross-origin form submissions or `fetch()` requests in the same way as cookies.

CSRF protection applies to all state-changing HTTP methods (POST, PUT, PATCH, DELETE) via Origin / Referer validation:

- The server rejects any state-changing request whose `Origin` header (or, if absent, the origin derived from the `Referer` header) does not match the server's own origin (`scheme://host:port`).
- `Origin: null` is explicitly rejected (browsers send this from sandboxed iframes, `file://` pages, and cross-origin redirects).
- Requests without either header are typical of native clients and bypass the check; GET requests are fully exempt (they must be side-effect-free).
- The LDA mode is fully exempt.

Use Go library github.com/mikaelstaldal/go-server-common for this.

---

## Web UI

### Technology Stack

- TypeScript with only `tsc` as build setp, ES6 modules, import maps.
- **Preact** + **JSX** for reactive components (vendored).
- Plain CSS for styling.
- All assets embedded in the binary via `//go:embed`.

### URL Routing

Hash-based routing (`/#/inbox`, `/#/message/123`). The server always serves the same `index.html` for all requests to non-API paths; the hash fragment is interpreted entirely by the client.

Route scheme:

| Hash pattern                    | View shown                                                          |
|---------------------------------|---------------------------------------------------------------------|
| `/#/` or `/#/inbox`             | Inbox folder view                                                   |
| `/#/folder/:slug`               | Named folder message list                                           |
| `/#/message/:id`                | Message detail for the given id                                     |
| `/#/compose`                    | New compose form (blank)                                            |
| `/#/compose?reply=:id`          | Compose pre-populated for reply to message `:id`                    |
| `/#/compose?replyall=:id`       | Compose pre-populated for reply-all to message `:id`                |
| `/#/compose?forward=:id`        | Compose pre-populated for forward of message `:id`                  |
| `/#/search?q=...`               | Search results                                                      |
| `/#/settings`                   | Settings page (defaults to first tab)                               |
| `/#/settings/:tab`              | Settings page at a specific tab                                     |

On first load the UI reads `localStorage` for the last selected folder and navigates there; if absent it navigates to `/#/inbox`.

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

1. **Folder view** — paginated message list for selected folder. Unread messages shown in bold. Click to open message detail. A **Mark all as read** button in the toolbar marks all messages in the folder as read: the UI fetches message IDs in batches of 200 using `GET /api/v1/folders/{id}/messages?limit=200&unread=true&offset=0` until all unread IDs are collected, then sends one or more `PATCH /api/v1/messages` requests each containing up to 200 IDs with `"read": true`. Loading indicator is shown while this is in progress.
2. **Message detail** — full headers, body, attachment download links. Reply/Reply All/Forward/Move/Delete/Snooze buttons. When the UI opens a message that is currently unread, it immediately sends `PATCH /api/v1/messages/{id}` with `"read": true` to mark it as read. The **Snooze** button is shown only when the message is in Inbox or a user folder (not in system folders such as Sent, Trash, Junk, Scheduled, or Snoozed). It opens a small picker with quick presets (later today, tomorrow morning, next week) and a custom date/time option; submits `POST /api/v1/messages/{id}/snooze`. When a message has both `body_html` and `body_text`, the body is shown according to the user's **preferred view** setting (see Client-Side Storage), defaulting to HTML. A **Plain text / HTML** toggle button switches the view for the current message and updates the stored preference. If only one body type is present it is shown directly with no toggle. HTML is rendered in a sandboxed iframe (see HTML Body Display); plain text is rendered as preformatted text.

   **Thread display:** When the message has a non-empty `references` list or `in_reply_to`, the UI calls `GET /api/v1/messages/{id}/thread`. If the result contains more than one message, a collapsed conversation strip is shown below the current message body. Each entry shows the sender, date, and subject snippet; clicking an entry expands it inline to show the full body (fetches `GET /api/v1/messages/{entryId}` on first expand). The current message is always shown expanded. Entries are ordered oldest-first. If the thread contains only the current message, no strip is shown.
3. **Compose / Reply / Reply All / Forward** — form with a **From** selector (dropdown of all identities, pre-selected to the default; or, when replying, to the identity whose address matches the original To/Cc), To, Cc, Bcc, Reply-To (optional, collapsed by default behind an "Add Reply-To" link), Subject, a rich-text body editor (see below), file upload for attachments. A **Send later** toggle reveals a date/time picker for `send_at`; when set, the Send button becomes "Schedule".

   **Rich-text editor:** The message body is edited using [Quill](https://quilljs.com/) (vendored, no CDN dependency). Quill operates in its default `delta` mode; on send/save the UI serialises the content as both HTML (via `quill.root.innerHTML`) and plain text (via `quill.getText()`). Both `body_html` and `body_text` are always sent; if the editor is empty, both fields are sent as empty strings. The Quill toolbar exposes: Bold, Italic, Underline, Ordered list, Bullet list, Link, and Clean (remove formatting). A **Send later** toggle reveals a date/time picker for `send_at`; when set, the Send button becomes "Schedule". Auto-save to Drafts on a 30-second timer: on the first save `POST /api/v1/drafts` (or `-with-attachments`) is called and the returned `id` is stored; subsequent saves call `PUT /api/v1/drafts/{id}` (or `-with-attachments`) to update the existing draft. **Navigate-away behavior:** when the user navigates away from a compose form that has unsaved content, an immediate draft save is triggered before navigation proceeds. **Auto-save and forward attachments:** `PUT /api/v1/drafts/{id}` (the JSON endpoint) does not modify attachment rows — attachments copied from the source message during the initial `POST` persist through all subsequent auto-saves. To remove individual attachments incrementally, the UI calls `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}` separately. Only `PUT /api/v1/drafts-with-attachments/{id}` replaces attachments wholesale. Drafts are always stored with `read = 1` so they never contribute to the unread badge on the Drafts folder. Scheduled messages auto-save to Drafts until explicitly scheduled. The To, Cc, and Bcc fields offer address autocomplete: as the user types, the UI queries `GET /api/v1/contacts?q=<input>` and shows a dropdown of matching name + address suggestions.

   **Send button behavior:** The Send button is disabled and shows a spinner while the send request is in-flight. Before submitting the send request, the UI performs an immediate draft save (equivalent to an auto-save tick), ensuring a draft copy exists in the Drafts folder prior to every send attempt regardless of whether the 30-second timer has previously fired. If `POST /api/v1/messages/send` returns an error (including HTTP 500 from a `sendmail` failure), the compose form remains open with all content intact and the error is displayed inline below the Send button (not as a toast). The pre-send draft save means content is always recoverable from Drafts even if the user closes the tab after a send failure.

   Pre-population rules per action:

   | Field           | Reply                                                                                   | Reply All                                                                         | Forward                                                                                                                                                 |
   |-----------------|-----------------------------------------------------------------------------------------|-----------------------------------------------------------------------------------|---------------------------------------------------------------------------------------------------------------------------------------------------------|
   | **From**        | Identity whose address appears in the original To or Cc; falls back to default identity | Same as Reply                                                                     | Default identity                                                                                                                                        |
   | **To**          | Original `Reply-To` address if present; otherwise original `From` address               | Original `Reply-To` address if present; otherwise original `From` address. If the resulting address matches the chosen From identity's own address, it is removed (prevents replying to yourself). | Empty                                                                                                                                                   |
   | **Cc**          | Empty                                                                                   | All addresses from original To + Cc, minus the chosen From identity's own address | Empty                                                                                                                                                   |
   | **Subject**     | `Re: <original subject>` (no double `Re:`)                                              | `Re: <original subject>` (no double `Re:`)                                        | `Fwd: <original subject>`                                                                                                                               |
   | **In-Reply-To** | Original `Message-ID`                                                                   | Original `Message-ID`                                                             | Empty                                                                                                                                                   |
   | **References**  | Original references + original `Message-ID`                                             | Original references + original `Message-ID`                                       | Empty                                                                                                                                                   |
   | **Attachments** | Empty                                                                                   | Empty                                                                             | Copies of all original attachments, populated server-side: the UI passes `source_message_id` in the `POST /api/v1/drafts` request body. The server atomically creates the draft and duplicates the attachment rows from the source message; the originals are not modified. |

   "No double `Re:`": if the original subject already starts with `Re:` (case-insensitive), it is used as-is.

   **Signature pre-population:** when the compose form opens, if the selected From identity has a non-empty `signature`, it is appended to the plain-text body preceded by `\n-- \n` (the standard signature delimiter). When the From identity is changed via the dropdown, the old identity's signature block (if present) is replaced with the new identity's signature. Reply and Reply-All prepend the quoted original message after the signature; Forward places the forwarded message after the signature.
4. **Message detail** (Scheduled folder) — shows the scheduled send time prominently. A **Cancel schedule** button calls `DELETE /api/v1/scheduled/{id}`, returning the message to Drafts for editing.
5. **Search** — global full-text search with results shown as a message list.
6. **Filter management** — CRUD UI for filters, with drag-to-reorder. The `match_to` field must be labelled **"To / Cc"** in the UI, because it matches against both the `To` and `Cc` headers.
7. **Folder management** — create/rename/delete/reorder user folders.
8. **Identity management** — CRUD UI for sender identities (name + address + signature + default flag), with drag-to-reorder. The default identity is marked visually; clicking a "Set default" button updates it. The signature field is a plain-text textarea; leave empty for no signature.
9. **Spam filter settings** — toggle to enable/disable the spam filter, numeric field for the score threshold, and text field for the score header name. Submits `PUT /api/v1/spam-filter`.
10. **Contact management** — paginated list of all contacts with name and address. Supports adding, editing, and deleting contacts. Queries `GET /api/v1/contacts`, `POST /api/v1/contacts`, `PUT /api/v1/contacts/{id}`, and `DELETE /api/v1/contacts/{id}`.
11. **Preferences** — UI panel (tab in Settings) for client-side display preferences stored in `localStorage`. Contains: dark mode toggle, message list density (Compact / Normal / Relaxed radio group), default body view (HTML / Plain text radio group; mirrors the per-message toggle), and browser notifications toggle (requests `Notification` API permission on enable; shows current permission state).

### Settings Navigation

A **gear icon** in the sidebar footer opens the Settings page at `/#/settings`. The page uses a tabbed layout with the following tabs (in order):

| Tab slug       | Content view                                      |
|----------------|---------------------------------------------------|
| `identities`   | Identity management (view 8)                      |
| `folders`      | Folder management (view 7)                        |
| `filters`      | Filter management (view 6)                        |
| `spam`         | Spam filter settings (view 9)                     |
| `contacts`     | Contact management (view 10)                      |
| `preferences`  | Preferences panel (view 11)                       |

Navigating to `/#/settings` defaults to the `identities` tab. Deep-linking to a specific tab (e.g. `/#/settings/spam`) opens that tab directly.

None of the settings views appear as top-level sidebar entries; they are all accessed exclusively through the Settings page.

### Date and Time Display

All timestamps are displayed in the **browser's local timezone** (no user-configurable override).

Display format is adaptive based on how old the message is relative to "now":

| Age                    | Format                       | Example            |
|------------------------|------------------------------|--------------------|
| < 1 hour               | Relative ("X minutes ago")   | "42 minutes ago"   |
| 1 hour – 23:59         | Time only (HH:MM, 24-hour)   | "14:32"            |
| Yesterday              | "Yesterday HH:MM"            | "Yesterday 09:15"  |
| 2–6 days ago           | Weekday + time               | "Mon 14:32"        |
| 7 days – same year     | Short date + time            | "Apr 3, 14:32"     |
| Previous years         | Short date with year         | "Apr 3, 2023"      |

In the message list (compact view), only the condensed part is shown (time only for today, date only for older). In the message detail header, the full `date` field is always shown in the "Apr 3, 14:32" or "Apr 3, 2023" form with the timezone abbreviation appended (e.g. "Apr 3, 14:32 CEST").

### Error Handling UX

- **Transient API errors** (non-network, non-form): shown as a toast/snackbar in the bottom-right corner. Auto-dismisses after 5 seconds. At most one toast is shown at a time; a new error replaces the previous one. The toast includes the HTTP status code and the `error` field from the response body.
- **Form validation errors** (400 responses from create/update forms): shown as an inline error message below the submit button, not as a toast. The `error` field from the response body is used verbatim.
- **Network failures** (fetch throws, or status 0): the UI retries once automatically after 2 seconds. If the retry also fails, a persistent toast is shown with a **Retry** button. The toast does not auto-dismiss until the user clicks Retry or dismisses it manually.
- **404 on message/folder navigation**: show an inline "Not found" message in the detail pane; do not navigate away from the current folder.
- **Auth failure (401)**: redirect to the login prompt (browser's built-in Basic Auth dialog, triggered by the `WWW-Authenticate` header from the server). No custom UI needed.

**Junk folder:** shown in the folder sidebar between Trash and user-created folders. The message detail view for messages in the Junk folder shows a **Not junk** button (calls `POST /api/v1/messages/{id}/mark-not-junk`) instead of the normal move controls. All other message views show a **Mark as junk** button (calls `POST /api/v1/messages/{id}/mark-junk`).

**Empty folder:** the Trash and Junk folder views show an **Empty** button in the toolbar. Clicking it prompts for confirmation, then calls `DELETE /api/v1/folders/{id}/messages`.

### HTML Body Display

Rendered in a sandboxed `<iframe srcdoc="...">` with `sandbox` and no additional tokens (maximum restriction: no scripts, no same-origin access, no forms, no popups). The sanitized HTML body is set as `srcdoc`. Links inside the email body must use `target="_blank"` and `rel="noopener noreferrer"` so they open in a new tab despite the sandbox; the sanitiser adds these attributes to all `<a href>` elements during sanitisation.

### New Message Notifications

The web UI polls `GET /api/v1/folders` every 30 seconds. When the `unread_count` for the Inbox folder increases compared to the previously known value, the UI:

1. Updates the unread badge on the Inbox entry in the folder sidebar.
2. Updates the `document.title` to include the unread count (e.g. `(3) mymail`).
3. If the user has granted the [Notifications API](https://developer.mozilla.org/en-US/docs/Web/API/Notifications_API) permission, fires a browser notification: title `New mail`, body `You have N unread messages in Inbox`.

Permission is requested only when the user explicitly enables browser notifications in the Preferences panel (the notifications toggle calls `Notification.requestPermission()` on click, which satisfies the browser's requirement for a user gesture). If permission is denied, steps 1 and 2 still apply; only the browser notification is skipped. The permission state is not re-requested after an explicit denial.

Polling is suspended while the browser tab is hidden (`document.visibilityState === 'hidden'`) and resumes immediately when the tab becomes visible again.

### Client-Side Storage

Using `localStorage`:
- Selected folder
- Compose draft state (as fallback) — stored as a JSON object containing all compose field values, the server-assigned draft `id` (if one exists), and a `savedAt` RFC 3339 UTC timestamp written each time the auto-save fires. **Draft recovery on page reload:** if a `savedAt` timestamp is present in `localStorage` and a server draft `id` is recorded, the UI fetches the server draft and compares its `updated_at` with `savedAt`; whichever is newer is loaded into the compose form silently (no prompt). If the timestamps are equal, the server draft is preferred (it is the persistent store). If the server returns `404` (e.g. the draft was sent or deleted in another tab), the `localStorage` state is used as the draft content and the stale `id` is cleared so the next auto-save creates a fresh server draft. If only one source exists it is used directly. If neither exists, the compose form opens blank.
- Dark mode toggle
- Message list density preference
- Notification permission state (cached to avoid repeated `Notification.permission` lookups)
- **Preferred body view** (`"html"` or `"text"`; default `"html"`): controls which body part is shown first in message detail when both are present. Updated whenever the user clicks the Plain text / HTML toggle.

---

## Go Dependencies

```
modernc.org/sqlite                         # Pure-Go SQLite (no CGO)
github.com/microcosm-cc/bluemonday         # HTML sanitization
github.com/mikaelstaldal/go-server-common  # htpasswd parsing
github.com/emersion/go-mbox                # mbox file reading (batch import)
github.com/emersion/go-maildir             # Maildir reading (batch import)
github.com/ogen-go/ogen                    # Generate server stubs from OpenAPI specification
github.com/go-faster/errors                # Needed by ogen
github.com/go-faster/jx                    # Needed by ogen
```

Parsing individual RFC 5322 messages: Go standard library (`net/mail`, `mime`, `mime/multipart`, `mime/quotedprintable`) — no third-party MIME library needed.

Building the binary: `go build -tags netgo` produces a single static binary with no CGO dependencies.

---

## `go.mod`

```
module github.com/mikaelstaldal/mymail

go 1.25.8

require (
    github.com/go-faster/errors v0.7.1
    github.com/go-faster/jx v1.2.0
    github.com/emersion/go-maildir v0.6.0
    github.com/emersion/go-mbox v1.0.4
    github.com/microcosm-cc/bluemonday v1.0.27
    github.com/mikaelstaldal/go-server-common v1.0.0
    github.com/ogen-go/ogen v1.20.2
    modernc.org/sqlite v1.48.1
)
```

---

## Key Design Decisions

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

### Send Failure Exposed as Boolean Only
`send_failure_count` is an internal counter used to enforce the 3-consecutive-failure limit. The API
exposes only the derived boolean `send_failed` (`true` when `send_failure_count > 0`). The UI
distinguishes the two meaningful states via `folder_id` context: `send_failed=true` in the Scheduled
folder means "retrying" (yellow badge); `send_failed=true` in the Drafts folder means "exhausted"
(red badge). Exposing the raw count would leak an implementation detail without adding UI value.

### Snooze Restores Unread State
When a snoozed message returns to Inbox, `read` is forcibly set to `0` regardless of whether it was read before snoozing. This is intentional: the user asked to be reminded, so the message should behave like a new arrival (badge, document title, browser notification). If the user had read it before snoozing and does not want the notification, they can simply dismiss it.

### Filter Evaluation in LDA
Filters are evaluated in the LDA process at delivery time, not asynchronously. This is correct because filters need to run before the message lands in the Inbox (otherwise notifications or badge counts would be wrong). The LDA holds a short write lock on the database for the duration of a single message insertion.

### No Retroactive Filter Application
Filters are applied only at LDA delivery time. Adding a new filter does not retroactively move existing messages that match the new criteria. This is a deliberate design decision consistent with the "simple client, let the MTA work" philosophy. Users who need to reorganise existing mail should do so manually using the move-to-folder UI.

### Thread View N+1 Fetch Pattern
`GET /api/v1/messages/{id}/thread` returns `MessageSummary` objects (no body). Expanding an entry in the thread view requires a separate `GET /api/v1/messages/{entryId}` fetch per entry. This is an accepted trade-off: threads are uncommon in personal email and are typically short, so the N+1 overhead is negligible in practice. A future enhancement could add a `?full=true` parameter to return full message bodies inline.

### Bulk Operation Atomicity
Bulk endpoints (`PATCH /api/v1/messages`, `DELETE /api/v1/messages`) return 404 if any supplied message ID does not exist. There is no partial-success response. In a single-user, single-active-session application stale IDs are rare, and an all-or-nothing contract is simpler to reason about. If the UI receives a 404 on a bulk operation it should refresh the folder view before retrying.

### Header-Based Spam Detection
Rather than implementing a built-in Bayesian classifier or content scorer, mymail reads spam verdicts from headers that the MTA pipeline has already set (SpamAssassin, Rspamd, etc.). This keeps mymail simple and lets operators choose and tune their preferred spam analysis tool independently. The standard `X-Spam-Flag` / `X-Spam-Status` / `X-Spam-Score` headers are the de-facto interoperability interface for this purpose. No new Go dependency is needed — header inspection uses the already-parsed `net/mail` header map.

Spam detection runs in Phase 1 (before user filters) so that user filters run with knowledge of the spam verdict and can override the Junk destination for known-good senders. This mirrors how most MUA spam integrations work.

---

## Production Deployment

mymail binds plain HTTP and delegates TLS termination, rate limiting, and access control to the operator's infrastructure. A separate `docs/deployment.md` document should be created covering:

- **TLS**: mymail must be placed behind a TLS-terminating reverse proxy (nginx, Caddy, etc.) before being exposed outside localhost. Using `-basic-auth-file` over plain HTTP leaks credentials; TLS is mandatory when Basic Auth is enabled.
- **Rate limiting**: The primary exposure from missing rate limiting is CPU exhaustion via repeated failed authentication attempts, not password guessing (bcrypt cost factor already limits guessing to ~10 attempts/second). The reverse proxy should apply a per-IP request rate limit (e.g. 20 req/s) to prevent this DoS vector.
- **Bind address**: When not behind a reverse proxy on the same host, use `-addr 127.0.0.1` to prevent accidental exposure on public interfaces.

---

## Out of scope

- **Multiple mailboxes**: currently one SQLite file = one mailbox. Multi-user support would require either per-user databases or a `user_id` column throughout.
- **PGP/S-MIME**: not in scope for v1.
- **Push notifications** (browser): not in scope for v1; polling the message list every 30 seconds is sufficient.
- **Offline support / PWA**: not in scope.
- **Large attachment threshold**: if attachments > N MB become a problem, move to disk storage. Threshold TBD.
- **Mobile / responsive layout**: the web UI targets desktop browsers only (fixed three-pane layout). Mobile support is out of scope for the initial version. A separate responsive web app or native mobile app may be developed in a future iteration.
- **Inline attachment preview**: only attachment download is provided. Inline preview of images, PDFs, and other file types is out of scope for v1.
- **Cross-folder Starred view**: the `flagged` flag can be filtered per folder but there is no virtual "Starred" folder that aggregates flagged messages across all folders. This is a potential future enhancement.

---

## Inspiration

Use the project *mycal* as inspiration for structure and patterns, see ../mycal
