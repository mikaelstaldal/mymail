# mymail — Implementation Plan

This document covers the technical implementation details for mymail. For what the system does, see REQUIREMENTS.md. For the REST API contract, see openapi.yaml. For architecture and key design decisions, see ARCHITECTURE.md.


## Go Dependencies

```
modernc.org/sqlite                         # Pure-Go SQLite (no CGO)
github.com/microcosm-cc/bluemonday         # HTML sanitization
github.com/mikaelstaldal/go-server-common  # htpasswd parsing, CSRF protection
github.com/emersion/go-mbox                # mbox file reading (batch import)
github.com/emersion/go-maildir             # Maildir reading (batch import)
github.com/ogen-go/ogen                    # Generate server stubs from OpenAPI specification
github.com/go-faster/errors                # Needed by ogen
github.com/go-faster/jx                    # Needed by ogen
```

Parsing individual RFC 5322 messages: Go standard library (`net/mail`, `mime`, `mime/multipart`, `mime/quotedprintable`) — no third-party MIME library needed.

Building: `go build -tags netgo` produces a single static binary with no CGO dependencies.

### `go.mod`

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


## REST API Implementation

The full REST API contract is in `openapi.yaml`. Use ogen to generate Go server stubs from it.

**Base path:** `/api/v1`

**Content type:** `application/json` for all request/response bodies except attachment downloads.

**Error responses:** `{ "error": "human-readable message" }` with status `400`, `401`, `404`, `409`, or `500`.

**Max request body:** 32 MiB.

**Entity counts:** The number of user-defined folders, filters, identities, and contacts is unbounded at the API level. No 400 is returned for exceeding any count; growth is bounded only by SQLite file size and available disk space.

**Bulk operation ID limits:** Bulk endpoints accept at most 1000 message IDs per request; exceeding this returns 400.

**Input length limits:**

| Field | Context | Limit |
|-------|---------|-------|
| `match_from`, `match_to`, `match_subject` | Filter criteria | 1000 characters |
| `score_header` | Spam filter settings | 200 characters |
| Contact `name`, identity `name`, folder `name` | General | 200 characters |
| `to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr` | SendRequest, DraftRequest | 8192 characters each |
| Identity `signature` | Stored and transmitted | 50 KiB |
| Search `q` parameter | Full-text search | 500 characters |
| Contact autocomplete `q` parameter | Substring filter | 500 characters |

**Whitespace trimming:** Leading and trailing whitespace is trimmed from folder names, filter names, contact names, and identity names before validation and storage.

**List endpoints:** All list endpoints return `{"total": n, "items": [...]}`, whether paginated or not. The `total` field is the total number of matching records (before pagination). Non-paginated endpoints (folders, filters, identities) return all records in `items` and set `total` to the same count. The `updated` count in bulk PATCH responses equals the number of rows actually modified by the SQL UPDATE (SQLite's `changes()` function); no-op updates where the new value equals the existing value are not counted.

**Search snippet:** Search results include a `snippet` field generated with `snippet(messages_fts, 4, '**', '**', '…', 15)`, where `4` is the zero-based index of the `body_text` column and `15` is the token context window. The snippet is HTML-escaped before being returned in the API response to prevent XSS. The `snippet` field appears only in search results, not in `GET /messages/{id}` responses.

**Contact autocomplete:** When `GET /api/v1/contacts` is used for address-field autocomplete dropdowns, the Web UI sends `limit=10`. If `total > 10`, the dropdown shows a "type more to narrow" hint rather than paginating.

### Endpoint Summary

#### Folders
- `GET /api/v1/folders` — list all folders (includes unread count per folder)
- `POST /api/v1/folders` — create a user-defined folder
- `PATCH /api/v1/folders/{id}` — update folder name and/or position
- `DELETE /api/v1/folders/{id}` — delete a user-created folder (messages moved to Trash)
- `PATCH /api/v1/folders/reorder` — reorder folders
- `DELETE /api/v1/folders/{folder_id}/messages` — delete all messages in a folder
- `POST /api/v1/folders/{folder_id}/mark-all-read` — mark all messages in a folder as read (atomic)

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

#### Scheduled Messages
- `DELETE /api/v1/scheduled/{id}` — cancel a scheduled message (moves to Drafts)

#### Drafts
- `POST /api/v1/drafts` — save a new draft
- `POST /api/v1/drafts-with-attachments` — save a new draft with `multipart/form-data`
- `PUT /api/v1/drafts/{id}` — replace draft content (does not modify attachments)
- `PUT /api/v1/drafts-with-attachments/{id}` — replace draft content and attachments
- `DELETE /api/v1/drafts/{id}` — permanently delete a draft
- `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}` — remove a single attachment from a draft
- `POST /api/v1/drafts/{id}/send` — send or schedule the draft (reads draft fields and attachments from DB; deletes draft on success)

#### Filters
- `GET /api/v1/filters` — list all filters
- `POST /api/v1/filters` — create a filter
- `PUT /api/v1/filters/{id}` — replace a filter
- `DELETE /api/v1/filters/{id}` — delete a filter
- `PATCH /api/v1/filters/reorder` — reorder filters

#### Spam Filter
- `GET /api/v1/spam-filter` — get spam filter settings
- `PUT /api/v1/spam-filter` — replace spam filter settings

#### Identities
- `GET /api/v1/identities` — list all identities
- `POST /api/v1/identities` — create an identity
- `PUT /api/v1/identities/{id}` — replace an identity
- `DELETE /api/v1/identities/{id}` — delete an identity
- `PATCH /api/v1/identities/reorder` — reorder identities

#### Contacts
- `GET /api/v1/contacts` — list contacts (supports autocomplete via `q` parameter)
- `POST /api/v1/contacts` — create a contact
- `PUT /api/v1/contacts/{id}` — replace a contact
- `DELETE /api/v1/contacts/{id}` — delete a contact

#### Health
- `GET /api/v1/health` — liveness check; returns 200 when the server is ready

### Thread Algorithm

`GET /api/v1/messages/{id}/thread` determines membership using (in order):

1. **Header-based (primary):** Build a directed graph where message A links to message B if B's `Message-ID` appears in A's `In-Reply-To` or `References` header. Take the transitive closure to find all messages in the same connected component.
2. **Subject-based fallback:** If header-based grouping yields only the single requested message, group by normalized subject: strip leading reply/forward prefixes (any combination, case-insensitive, repeatedly) and compare case-insensitively. Strip: `Re:`, `Fwd:`, `Fw:`, `AW:`, `WG:`, `RES:`, `ENC:`, `VS:`, `SV:`.

Thread results include messages from all folders, ordered by `date ASC`.

Messages that initially lacked a `Message-ID` header are assigned a generated ID at storage time. Since external mailers may not reference this generated ID in their `In-Reply-To`/`References` headers, subject-based threading serves as the natural fallback for such messages.

### FTS Search Input Sanitization

The `q` parameter on `GET /api/v1/messages/search` is passed to SQLite FTS5 as a literal phrase match. Transform:
1. Replace every `"` (U+0022) in the input with `""` (two double-quote characters).
2. Wrap the result in a single pair of outer double quotes.

Example: `it's a "test"` → `"it's a ""test"""`. Apply byte-by-byte (no locale-specific interpretation). A unit test must verify that inputs containing `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are treated as literals.

**RFC 5322 message parsing limit:** `POST /api/v1/messages/import` enforces a 10 MiB limit on the total bytes consumed during parsing (in addition to the global 32 MiB body cap) to prevent memory exhaustion from deeply nested MIME structures.

**FTS5 tokenizer:** FTS5 uses the built-in `unicode61` tokenizer (the default), which performs Unicode-aware case folding. All FTS searches are effectively case-insensitive.


## Database

**File:** `<data>/mymail.sqlite`

**File permissions:** Create data directory with mode `0700` and database file with mode `0600` on first run.

**SQLite configuration:**
- Server: `PRAGMA journal_mode=WAL`, 5-second busy timeout.
- LDA: 30-second busy timeout (`PRAGMA busy_timeout=30000`).

All timestamps stored as UTC RFC 3339 strings.

### Schema Migrations

Versioned using `PRAGMA user_version`. On every startup the server reads the current version and applies any missing migrations in order, each in a transaction. `PRAGMA user_version` is set inside the same transaction as the DDL, so a crash mid-migration leaves the version unchanged and is retried on next startup. **Note:** SQLite's handling of `PRAGMA user_version` inside a transaction may vary by version; treat the atomicity guarantee as best-effort.

Each `if v < N` block is checked independently (not `else if`), so a single startup can apply multiple sequential migrations.

```
user_version 0  →  fresh database: run all CREATE TABLE / CREATE INDEX / CREATE TRIGGER statements
user_version 1  →  (reserved for first future migration)
```

**Current schema version: 1** (initial schema applied; no further migrations yet).

### `folders`

```sql
CREATE TABLE IF NOT EXISTS folders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT    NOT NULL UNIQUE,
    slug       TEXT    NOT NULL UNIQUE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
```

Built-in folders seeded on first run (id=1..7); user-created folders have `id >= 100`.

The `unread_count` field in the `Folder` API response is computed on-the-fly:
```sql
SELECT COUNT(*) FROM messages WHERE folder_id = ? AND read = 0
```
No denormalized counter is maintained.

| id | name      | slug      | position |
|----|-----------|-----------|----------|
| 1  | Inbox     | inbox     | 0        |
| 2  | Sent      | sent      | 1        |
| 3  | Drafts    | drafts    | 2        |
| 4  | Trash     | trash     | 3        |
| 5  | Scheduled | scheduled | 4        |
| 6  | Snoozed   | snoozed   | 5        |
| 7  | Junk      | junk      | 6        |

### `messages`

```sql
CREATE TABLE IF NOT EXISTS messages (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    folder_id     INTEGER NOT NULL REFERENCES folders(id),
    message_id    TEXT UNIQUE,
    in_reply_to   TEXT,
    references    TEXT,                    -- newline-separated (\n); serialized as JSON array by the API
    from_addr     TEXT    NOT NULL,
    to_addr       TEXT    NOT NULL,
    cc_addr       TEXT    NOT NULL DEFAULT '',
    bcc_addr      TEXT    NOT NULL DEFAULT '',
    reply_to_addr TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    date          TEXT    NOT NULL,        -- RFC 3339 UTC
    body_text     TEXT    NOT NULL DEFAULT '',
    body_html     TEXT    NOT NULL DEFAULT '',
    raw           BLOB    NOT NULL,
    read          INTEGER NOT NULL DEFAULT 0,
    flagged       INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    send_at       TEXT,                    -- RFC 3339 UTC; non-NULL = deferred send
    snoozed_until TEXT,                    -- RFC 3339 UTC; non-NULL = snoozed
    snooze_folder INTEGER REFERENCES folders(id) ON DELETE SET NULL, -- folder_id to return to on snooze expiry; exposed as snooze_folder_id in API
    send_error    TEXT,                    -- last sendmail error (max 4 KB stored)
    send_failure_count INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT    NOT NULL,
    updated_at    TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_messages_folder_id    ON messages(folder_id);
CREATE INDEX IF NOT EXISTS idx_messages_date         ON messages(date);
CREATE INDEX IF NOT EXISTS idx_messages_message_id   ON messages(message_id);
CREATE INDEX IF NOT EXISTS idx_messages_read         ON messages(read);
CREATE INDEX IF NOT EXISTS idx_messages_send_at      ON messages(send_at) WHERE send_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_messages_snoozed_until ON messages(snoozed_until) WHERE snoozed_until IS NOT NULL;

CREATE TRIGGER IF NOT EXISTS messages_updated_at AFTER UPDATE ON messages BEGIN
    UPDATE messages SET updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now') WHERE id = new.id;
END;

CREATE TRIGGER IF NOT EXISTS attachments_insert_flag AFTER INSERT ON attachments BEGIN
    UPDATE messages SET has_attachments = 1 WHERE id = new.message_id;
END;
CREATE TRIGGER IF NOT EXISTS attachments_delete_flag AFTER DELETE ON attachments BEGIN
    UPDATE messages SET has_attachments = (
        SELECT CASE WHEN EXISTS (SELECT 1 FROM attachments WHERE message_id = old.message_id) THEN 1 ELSE 0 END
    ) WHERE id = old.message_id;
END;
```

The API exposes `send_failure_count > 0` as the boolean `send_failed`; the raw count is not exposed.

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
```

Content table: tokens stored in FTS index, row content in `messages`. `body_html` is not indexed directly; `body_text` is derived from sanitized HTML when no plain-text part is present, so all content is searchable. Content removed by the sanitizer (e.g. `<script>` text) is intentionally not indexed.

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

CREATE TRIGGER IF NOT EXISTS messages_fts_update AFTER UPDATE OF from_addr, to_addr, cc_addr, subject, body_text ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES ('delete', old.id, old.from_addr, old.to_addr, old.cc_addr, old.subject, old.body_text);
    INSERT INTO messages_fts(rowid, from_addr, to_addr, cc_addr, subject, body_text)
    VALUES (new.id, new.from_addr, new.to_addr, new.cc_addr, new.subject, new.body_text);
END;
```

### `attachments`

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
    name         TEXT    NOT NULL,
    address      TEXT    NOT NULL UNIQUE,
    is_default   INTEGER NOT NULL DEFAULT 0,
    position     INTEGER NOT NULL DEFAULT 0,
    signature    TEXT    NOT NULL DEFAULT ''
);
```

The "exactly one default" invariant and address validation are enforced in the service layer (not as SQL constraints) for human-readable error messages. Because SQLite serializes all write operations, concurrent identity deletions are naturally serialized: if two DELETE requests arrive concurrently, only one finds the count > 1; the other returns 400. Position values need not be contiguous; gaps are allowed. The server sorts by `position ASC, id ASC`. The reorder endpoint assigns contiguous 0-based positions for convenience, but direct position assignment via POST/PUT may use arbitrary non-negative integers.

### `contacts`

```sql
CREATE TABLE IF NOT EXISTS contacts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    address    TEXT    NOT NULL UNIQUE,
    name       TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_contacts_address ON contacts(address);
```

Upsert must use a single atomic statement:
```sql
INSERT INTO contacts (...) ON CONFLICT(address) DO UPDATE SET name = excluded.name WHERE contacts.name = ''
```

### `filters`

```sql
CREATE TABLE IF NOT EXISTS filters (
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
```

`stop` stored as INTEGER (`1`/`0`), exposed as boolean in the API.

A match field is "non-empty" if `TRIM(field) != ''`.

### `spam_filter_settings`

```sql
CREATE TABLE IF NOT EXISTS spam_filter_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    enabled       INTEGER NOT NULL DEFAULT 1,
    score_header  TEXT    NOT NULL DEFAULT 'X-Spam-Score',
    score_threshold REAL  NOT NULL DEFAULT 5.0
);
```

Single-row table. Initialize with:
```sql
INSERT OR IGNORE INTO spam_filter_settings (id, enabled, score_header, score_threshold) VALUES (1, 1, 'X-Spam-Score', 5.0)
```
The `OR IGNORE` makes concurrent first-run inserts race-safe.


## LDA Implementation

### Message Parsing

Use Go standard library (`net/mail`, `mime`, `mime/multipart`, `mime/quotedprintable`):
- Decode RFC 2047 encoded words in headers.
- Decode RFC 2231 encoded parameters (e.g. `filename*=UTF-8''...`) in `Content-Disposition` and `Content-Type` parameter values.
- RFC 2047 must not be applied inside MIME parameter values.

**MIME body part search strategy:** Use depth-first search of the primary body tree to locate `text/plain` and `text/html` parts. Skip `message/rfc822` sub-parts entirely (treat them as opaque attachments, not as a source of body text). Within a `multipart/alternative`, prefer `text/html` over `text/plain` when both are present (as they are siblings at the same level, not nested).

### Duplicate Detection

```sql
SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)
```

Run before spam detection and filter evaluation. Use `INSERT OR IGNORE` on the `messages` table as a race-safe guard for concurrent LDA processes. Matching is a case-sensitive byte comparison (`=` operator). Messages without a `Message-ID` header have `NULL` stored; since `NULL = NULL` is false in SQL, messages without a Message-ID are never considered duplicates of each other and are always stored.

### Message-ID Generation in LDA Mode

When an incoming message lacks a `Message-ID` header, generate one as `<uuid@domain>` where `domain` is extracted from the first address in the `To` header. If the `To` header is absent or unparseable, fall back to `localhost`.

### Database Insertion

Insert message record and attachments in a single transaction.

### Contacts Upsert

Only the `From` address is upserted for incoming mail (To/Cc not auto-added). Lower-case the address before storage using Unicode simple casefolding (`golang.org/x/text/cases` with `language.Und` and `cases.Fold()`). The same normalization applies to outgoing contacts (To/Cc/Bcc addresses upserted on send).

### Error Handling

**Definition of parse failure:** any error returned by `net/mail.ReadMessage()` (missing or malformed headers) is a hard parse failure. Missing optional fields (e.g. absent `Date`, absent `Message-ID`, empty `Subject`) are treated as warnings and handled gracefully, not as failures.

- `SQLITE_BUSY`: retry with exponential backoff for up to 30 seconds, then exit `75`.
- Parse failure: log to stderr, exit `1`.
- All other errors: log to stderr, exit `75`.


## Background Scheduler Implementation

Single background goroutine started on server startup, stopped cleanly via context cancellation on shutdown.

**Re-entrance guard:** Uses a mutex so overlapping ticks are skipped rather than run concurrently.

**Deferred send query:**
```sql
SELECT id FROM messages
WHERE folder_id = 5
  AND send_at <= CURRENT_TIMESTAMP
ORDER BY send_at ASC;
```

**Conditional UPDATE before send** (prevents race with HTTP cancel handler):
```sql
UPDATE messages SET ... WHERE id = ? AND send_at IS NOT NULL AND folder_id = 5
```

The `DELETE /api/v1/scheduled/{id}` handler must clear `send_at` and set `folder_id = 3` in a **single** UPDATE statement to avoid a race window.

**Snooze expiry query:**
```sql
SELECT id, snooze_folder FROM messages
WHERE folder_id = 6
  AND snoozed_until <= CURRENT_TIMESTAMP
ORDER BY snoozed_until ASC;
```

The scheduler holds the SQLite write lock only for the duration of each individual UPDATE, not across the full tick.

**Known limitation — double-send on restart:** If the server restarts while a `sendmail` process is running, the new scheduler retries and may send twice. The conditional UPDATE prevents duplicate database records but not duplicate email delivery. Deduplication of duplicate deliveries is outside mymail's scope; operators may configure MTA-level deduplication if needed.


## Batch Import Implementation

### Libraries

**mbox:** `github.com/emersion/go-mbox` (primary, v1, MIT). API: `mbox.NewReader(r io.Reader)` → `*Reader`; call `NextMessage() (io.Reader, error)` in a loop. Handles mboxo and mboxrd. Add `github.com/tvanriper/mbox` only if SVR4 mboxcl support is needed.

**Maildir:** `github.com/emersion/go-maildir` (v0.6.0, MIT). API: `maildir.Dir(path)` → iterate with `Keys()`, open each with `Message(key)`.

### Implementation Notes

- Open database and run migrations before importing.
- Use batched transactions: commit every 500 messages to bound WAL file size. If a batch fails, only that batch is rolled back; previously committed batches are retained. There is no way to identify which individual messages were committed after a partial failure — re-run the full import after fixing the source data (duplicate detection will skip already-imported messages).
- Run the full LDA parsing pipeline (HTML sanitization, `cid:` resolution, `body_text` derivation, attachment extraction) for each message. Skip only spam detection and filter application.
- For Maildir, map the `S` (Seen) flag to `read=1` and the `F` (Flagged) flag to `flagged=1`.
- For mbox: parse and save the `From ` separator line timestamp **before** stripping it (used as `date` fallback). Strip the `From ` line before storing the `raw` BLOB. Use streaming `NextMessage()` API — do not load the entire file into memory.
- `date` field: use the original `Date` header. Fallback: mtime of the Maildir file (Maildir) or `From ` separator timestamp (mbox). Never use current time as fallback during import.


## Outgoing Mail Implementation

### Message Construction

1. Set `Date` to current time at send (RFC 5322 format).
2. Generate `Message-ID` as `<uuid@domain>` where `domain` is extracted from the sender address.
3. Strip CR (`\r`), LF (`\n`), and NUL (`\0`) from all user-supplied header values before encoding.
4. Encode non-ASCII header values as RFC 2047 encoded words.
5. Encode attachment data as base64.
6. Body structure:
   - If only one body part is provided: single `text/plain` or `text/html` part directly.
   - If both text and HTML are provided: `multipart/alternative` with both parts.
   - Wrap in `multipart/mixed` if attachments are present.
7. Add `Reply-To` header only if `reply_to_addr` is non-empty.

### Sendmail Integration

- Pipe to `sendmail -t -oi`.
- Maximum 30-second timeout; treat timeout as a failure.
- Capture stderr on non-zero exit or timeout: retain the **last** 4 KB (oldest output is discarded when stderr exceeds 4 KB).
- **HTTP status mapping:** any non-zero exit code or timeout → `500 Internal Server Error`. The sendmail stderr output is included in the `{"error": "..."}` response body.

### Outgoing HTML Sanitization

Sanitize `body_html` using the standard email HTML sanitization policy before constructing the MIME message and before storing the message in the Sent folder. This applies to all outgoing HTML content regardless of source (composed, quoted, or forwarded). Client-side sanitization by the rich-text editor is defence in depth only; server-side sanitization is authoritative.

### Bcc Handling

The `Bcc` header is preserved in the raw BLOB stored in Sent. Relies on the MTA (`sendmail`) stripping `Bcc` from outgoing copies before delivery.


## HTML Sanitization Implementation

Use `github.com/microcosm-cc/bluemonday` with a custom email-appropriate policy.

**CSS property matching:** Use a proper CSS parser (not string splitting or prefix matching) to distinguish properties with unexpected whitespace, comments, or encoding. For example, `background` (forbidden shorthand) and `background-color` (allowed) must be distinguished by exact name after parsing.

**Forbidden value patterns** (checked before the property allowlist):
- `url(`
- `expression(`
- `-moz-binding`
- `/*` (CSS comment)

**Links:** Add `target="_blank"` and `rel="noopener noreferrer"` to all `<a href>` elements during sanitization.

### `cid:` Resolution Algorithm

1. Build a map: Content-ID value → MIME part bytes (strip angle brackets from Content-ID header value).
2. Count `<img src="cid:...">` elements in the HTML. If > 64, remove all `cid:` `src` attributes and return.
3. Process in document order, tracking running total of decoded bytes:
   - Look up the content-id (case-insensitive).
   - If found and ≤ 1 MiB and adding it would not exceed the 10 MiB total: replace `src` with `data:<content-type>;base64,<base64-encoded-bytes>`.
   - Otherwise: remove `src` attribute.
4. Run bluemonday sanitizer on the rewritten HTML.


## Authentication and CSRF

Use `github.com/mikaelstaldal/go-server-common` for:
- htpasswd file reading (bcrypt verification)
- CSRF Origin/Referer validation middleware

When a request lacks valid credentials, respond with `401 Unauthorized` and `WWW-Authenticate: Basic realm="<realm>"` (where `<realm>` is the `-basic-auth-realm` flag value). The `GET /api/v1/health` endpoint is exempt from authentication.

CSRF middleware logic:
- Extract the origin from the `Origin` header; if absent, derive from the `Referer` header.
- Reject `Origin: null`.
- Allow requests with neither header (native clients).
- Compare against the server's own `scheme://host:port`.
- Exempt GET requests and the LDA (no HTTP).

Create htpasswd files with: `htpasswd -Bc htpasswd myuser`


## Attachment Download Response

Always respond with `Content-Type: application/octet-stream`. This sanitization applies to the single-attachment download endpoint (`GET /attachments/{id}`) only; there are no multipart attachment download endpoints.

`Content-Disposition` construction:
1. Strip CR (`\r`), LF (`\n`), NUL (`\0`), and `"` from the filename. If the filename is empty after stripping, use `attachment` as the filename.
2. After stripping, if the filename contains only printable ASCII (U+0020–U+007E, excluding stripped chars): emit `Content-Disposition: attachment; filename="<sanitized>"`.
3. If it contains any non-ASCII characters (U+0080+): emit `Content-Disposition: attachment; filename*=UTF-8''<RFC-8187-percent-encoded>` (encode all octets outside the `attr-char` set from RFC 8187).

## Web UI

Use openapi-typescript to generate the client for REST API from `openapi.yaml`. 

### Rich-Text Editor

Use [Quill](https://quilljs.com/) (vendored). On send/save, serialize as both HTML (`quill.root.innerHTML`) and plain text (`quill.getText()`). Toolbar: Bold, Italic, Underline, Ordered list, Bullet list, Link, Clean.

### Draft Auto-Save Logic

- Auto-save fires every 30 seconds.
- **Forward exception:** `POST /api/v1/drafts` is called immediately at form-open time for Forward (because `source_message_id` for attachment copying is only valid on the initial POST). For Reply, Reply-All, and new compose, the first POST is deferred until the first 30-second tick.
- `PUT /api/v1/drafts/{id}` (JSON endpoint) does not modify attachment rows.
- `PUT /api/v1/drafts-with-attachments/{id}` replaces attachments wholesale.
- To remove individual attachments, call `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}`.

### Send Draft Logic (`POST /api/v1/drafts/{id}/send`)

1. Read the draft from the DB. Return 404 if not found or if `folder_id != 3` (not in Drafts).
2. Validate: at least one of `to_addr`, `cc_addr`, `bcc_addr` must be non-empty. Return 400 otherwise.
3. Resolve the identity: use `identity_id` from the draft if set, otherwise the default identity.
4. Look up all attachment rows for the draft.
5. Determine mode from `send_at`:
   - **Immediate send** (`send_at` is null or ≤ now): run the standard outgoing mail pipeline (sanitize body_html, construct MIME message with all attachments, pipe to sendmail). On sendmail failure: return 500 with the error; draft is preserved unchanged. On success: insert the sent message and its attachments into the Sent folder, then delete the draft row (attachments cascade-delete).
   - **Scheduled** (`send_at` > now + 1 min): insert the message and attachments into the Scheduled folder (do not call sendmail), then delete the draft. Return 202.
6. Upsert recipients (To/Cc/Bcc) into the contacts table on immediate send only (same as the regular send flow). For scheduled sends, upsert happens when the scheduler actually sends the message.

### Mark All As Read

Call `POST /api/v1/folders/{id}/mark-all-read`. This single atomic request replaces the previous client-side batching approach.

### HTML Body Display

Render in `<iframe srcdoc="...">` with `sandbox` attribute and no additional tokens (maximum restriction). Per-message opt-in for external images: rerender in an isolated iframe with a relaxed CSP (`img-src https:`) for that frame only.

### Notifications Polling

Poll `GET /api/v1/folders` every 30 seconds. Suspend while `document.visibilityState === 'hidden'`; resume immediately when the tab becomes visible.
