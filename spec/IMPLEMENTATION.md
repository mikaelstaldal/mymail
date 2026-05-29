# mymail — Implementation Plan

This document covers the technical implementation details for mymail. For what the system does, see REQUIREMENTS.md. For the REST API contract, see openapi.yaml. For architecture and key design decisions, see ARCHITECTURE.md.


## Go Dependencies

```
modernc.org/sqlite                         # Pure-Go SQLite (no CGO)
github.com/microcosm-cc/bluemonday         # HTML sanitization
github.com/jaytaylor/html2text             # HTML → plain text derivation when message has no text/plain part
github.com/mikaelstaldal/go-server-common  # htpasswd parsing, CSRF protection
github.com/emersion/go-mbox                # mbox file reading (batch import)
github.com/emersion/go-maildir             # Maildir reading (batch import)
github.com/ogen-go/ogen                    # Generate server stubs from OpenAPI specification
github.com/go-faster/errors                # Needed by ogen
github.com/go-faster/jx                    # Needed by ogen
golang.org/x/net/html/charset              # Charset decoding (ISO-8859-x, Windows-125x, GB2312, etc.)
golang.org/x/text/cases                    # Unicode simple casefolding (contacts, identities)
```

Parsing individual RFC 5322 messages: Go standard library (`net/mail`, `mime`, `mime/multipart`, `mime/quotedprintable`) — no third-party MIME library needed.

Building: `go build -tags netgo` produces a single static binary with no CGO dependencies.

### `go.mod`

```
module github.com/mikaelstaldal/mymail

go 1.26

require (
    github.com/go-faster/errors v0.7.1
    github.com/go-faster/jx v1.2.0
    github.com/emersion/go-maildir v0.6.0
    github.com/emersion/go-mbox v1.0.4
    github.com/jaytaylor/html2text v0.0.0-20230321000545-74c2419ad056
    github.com/microcosm-cc/bluemonday v1.0.27
    github.com/mikaelstaldal/go-server-common v1.0.0
    github.com/ogen-go/ogen v1.20.2
    golang.org/x/net v0.43.0
    golang.org/x/text v0.30.0
    modernc.org/sqlite v1.48.1
)
```


## REST API Implementation

The full REST API contract is in `openapi.yaml`. Use ogen to generate Go server stubs from it.

**Route priority:** ogen's generated router (backed by chi) gives static path segments priority over path parameters. `PATCH /folders/reorder` matches as a static route before `PATCH /folders/{id}` would match with `{id}="reorder"`, and identically for `/filters/reorder` and `/identities/reorder`. No additional router configuration is required — chi's default behaviour is correct for this API.

**Base path:** `/api/v1`

**Content type:** `application/json` for all request/response bodies except attachment downloads.

**Error responses:** `{ "error": "human-readable message" }` with status `400`, `401`, `404`, `409`, or `500`.

**Max request body:** 32 MiB. This limit applies to the entire encoded request body, including all parts of a `multipart/form-data` request combined.

**Entity counts:** The number of user-defined folders, filters, identities, and contacts is unbounded at the API level. No 400 is returned for exceeding any count; growth is bounded only by SQLite file size and available disk space.

**Bulk operation ID limits:** Bulk endpoints accept at most 1000 message IDs per request; exceeding this returns 400.

**Input length limits:**

| Field | Context | Limit |
|-------|---------|-------|
| `match_from`, `match_to`, `match_subject` | Filter criteria | 1000 characters |
| `score_header` | Spam filter settings | 200 characters |
| Contact `name`, identity `name`, folder `name`, filter `name` | General | 200 characters |
| Contact `address` | POST /contacts, PUT /contacts/{id} | 254 characters (RFC 5321 maximum) |
| `to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr` | SendRequest, DraftRequest | 8192 characters each |
| `subject` | SendRequest, DraftRequest | 998 characters (RFC 5322 per-line limit) |
| Identity `signature` | Stored and transmitted | 50 KiB |
| Search `q` parameter | Full-text search | 500 characters |
| Contact autocomplete `q` parameter | Substring filter | 500 characters |

### Email Address Validation

`net/mail.ParseAddress()` is used to validate addr-spec fields wherever the spec requires "a valid RFC 5322 addr-spec":

- **Identity `address` and contact `address`** (create/update): call `net/mail.ParseAddress(input)`. Accept only when the returned `Address.Name` is empty — a bare addr-spec like `user@example.com` is valid; a display-name form like `"John Doe <john@example.com>"` is rejected with 400.
- **`to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr`** in `SendRequest` and `DraftRequest`: these fields carry comma-separated address lists as they appear in email headers. When non-empty, parse with `net/mail.ParseAddressList(input)` and verify that every parsed address has a non-empty `.Address` field. An unparseable list returns 400. Empty strings are always accepted (constraints on which fields must be non-empty are validated separately).

**Whitespace trimming:** Leading and trailing Unicode whitespace (using `strings.TrimSpace`) is trimmed from folder names, filter names, contact names, identity names, and the search `q` parameter before validation and storage.

**Position default (append semantics):** When `position` is omitted from `POST /folders`, `POST /filters`, or `POST /identities`, the server sets `position = COALESCE(MAX(position), -1) + 1` within the relevant table, placing the new entity at the end of the ordered list. This query must be executed inside the same transaction as the INSERT.

**Reorder endpoint semantics:** `PATCH /folders/reorder`, `PATCH /filters/reorder`, and `PATCH /identities/reorder` all share the same validation rules:

- The submitted `ids` array must contain **every** existing entity of that type exactly once. Partial reorders are not supported (the endpoint rewrites all positions in a single transaction).
- Duplicate IDs in the array → `400 {"error": "duplicate id"}`.
- IDs that do not refer to an existing entity → `400 {"error": "unknown id"}`.
- Missing IDs (any existing entity not present in the array) → `400 {"error": "incomplete reorder; all ids must be supplied"}`.
- An empty `ids` array is rejected with the same "incomplete reorder" 400 (unless no entities exist at all, in which case the call is a no-op returning `updated: 0`).
- On success the array index becomes the new `position` value (0, 1, 2, …) for each id, applied in a single SQLite transaction so the new ordering is atomic.

**List endpoints:** All list endpoints return `{"total": n, "items": [...]}`, whether paginated or not. The `total` field is the total number of matching records (before pagination). Non-paginated endpoints (folders, filters, identities) return all records in `items` and set `total` to the same count. The `updated` count in bulk PATCH responses equals the number of rows actually modified by the SQL UPDATE (SQLite's `changes()` function); no-op updates where the new value equals the existing value are not counted.

**Search snippet:** Search results include a `snippet` field generated with `snippet(messages_fts, 4, '**', '**', '…', 15)`, where `4` is the zero-based index of the `body_text` column and `15` is the token context window. The snippet is HTML-escaped before being returned in the API response to prevent XSS. The `snippet` field appears only in search results, not in `GET /messages/{id}` responses. **Snippet limitation:** `snippet()` is sourced solely from the `body_text` column. If the search term matches only in `from_addr`, `to_addr`, `cc_addr`, or `subject` (columns 0–3), the snippet may be empty or an irrelevant excerpt from `body_text`. This is a known SQLite FTS5 limitation; the UI displays an empty snippet in that case.

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
- `GET /api/v1/messages/{id}/body` — get sanitized HTML body as a standalone HTML document with CSP header
- `GET /api/v1/messages/{id}/thread` — get all messages in the same thread
- `PATCH /api/v1/messages/{id}` — update message metadata (folder, read, flagged)
- `PATCH /api/v1/messages` — bulk update read/flagged state for multiple messages
- `DELETE /api/v1/messages/{id}` — delete message (to Trash, or permanently if already in Trash)
- `DELETE /api/v1/messages` — bulk delete messages
- `POST /api/v1/messages/move` — bulk move messages to a folder
- `POST /api/v1/messages/send` — send or schedule a message
- `POST /api/v1/messages/send-with-attachments` — send/schedule with `multipart/form-data`
- `POST /api/v1/messages/{id}/snooze` — snooze a message until a future time
- `DELETE /api/v1/messages/{id}/snooze` — cancel a snooze early
- `POST /api/v1/messages/{id}/mark-junk` — move to Junk and mark as read
- `POST /api/v1/messages/{id}/mark-not-junk` — move from Junk to Inbox

#### Attachments
- `GET /api/v1/attachments/{id}` — download attachment data

#### Scheduled Messages
- `PATCH /api/v1/scheduled/{id}` — reschedule a scheduled message (update `send_at`; must be > 60 seconds in the future; 404 if not in Scheduled folder)
- `POST /api/v1/scheduled/{id}/send` — send a scheduled message immediately; atomically claims it then calls sendmail; on success moves to Sent; on sendmail failure moves to Drafts; 404 if not in Scheduled folder
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

1. **Header-based (primary):** Build the connected component of the message graph using an iterative Go query loop (see below). Message A has an edge to message B when B's `Message-ID` appears in A's `In-Reply-To` or A's `References` field.
2. **Subject-based fallback:** If header-based grouping yields only the single requested message, group by normalized subject and compare case-insensitively. Normalisation strips leading reply/forward prefixes using the regex defined in REQUIREMENTS.md → Compose → Subject prefix stripping (`^[ \t]*(?i:re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]*`), applied repeatedly to the start of the subject until no further match is found, then trims surrounding whitespace. The fallback is restricted to messages in the **same folder** as the seed message (matching `folder_id`) and uses an FTS5 query on `messages_fts` for the subject lookup instead of a full table scan.

Thread results include messages from all folders, ordered by `date ASC`. **Cap:** Thread results are limited to 1000 messages. If the transitive closure (or subject-based fallback) yields more than 1000 messages, only the 1000 with the earliest `date` are returned and the response includes `truncated: true`. The UI shows a "thread too long" indicator when `truncated` is true. **`truncated` flag semantics:** set `truncated: true` whenever the iterative loop terminated because `len(foundIDs) == 1000` (i.e. the cap was reached), regardless of whether the full transitive closure is known to exceed 1000. Once the loop exits early, it is impossible to determine the true thread size, so reaching the cap is sufficient evidence to set the flag.

Messages received via the LDA that lacked a `Message-ID` header are assigned a generated ID at storage time (see LDA Implementation → Message-ID Generation). Messages imported in batch mode may have a NULL `message_id` — import does not generate IDs for messages without a `Message-ID` header. Since external mailers may not reference a generated ID in their `In-Reply-To`/`References` headers, and imported messages may have a NULL `message_id` entirely, subject-based threading serves as the natural fallback for such messages.

**Iterative Go query loop for transitive closure:**

A recursive SQL CTE cannot efficiently match against the newline-separated `references` column for multiple message IDs simultaneously. Instead, compute the transitive closure in Go:

1. Fetch the seed row: `id`, `message_id`, `in_reply_to`, `"references"`.
2. Initialize `foundIDs = {seed.id}` (row IDs), `knownMsgIDs = {seed.message_id}` (message_id strings; exclude NULL).
3. Split `seed."references"` on `\n` to get `referencedMsgIDs`.
4. Repeat until no new rows are added or `len(foundIDs) >= 1000`:

   **Forward query** — messages that link to any ID in `knownMsgIDs`:
   ```sql
   SELECT id, message_id, in_reply_to, "references"
   FROM messages
   WHERE id NOT IN (/* foundIDs */)
     AND (
       in_reply_to IN (/* knownMsgIDs */)
       OR id IN (
         SELECT message_id FROM message_references
         WHERE ref_msg_id IN (/* knownMsgIDs */)
       )
     )
   ```
   The `message_references` join table is populated at insert/update time (one row per `\n`-separated entry in the `references` column) and indexed on `ref_msg_id`, giving O(log n) forward lookups instead of the previous LIKE full-table-scan approach.

   **Backward query** — messages whose `message_id` is referenced by any row in `foundIDs`:
   collect all unique `in_reply_to` values and all individual `\n`-split entries from `"references"` for every row in `foundIDs`, then:
   ```sql
   SELECT id, message_id, in_reply_to, "references"
   FROM messages
   WHERE message_id IN (/* referencedMsgIDs */)
     AND id NOT IN (/* foundIDs */)
   ```

   For each newly returned row, add its `id` to `foundIDs`, its `message_id` (if non-null) to `knownMsgIDs`, and its split `"references"` entries to `referencedMsgIDs`.

5. After reaching a fixed point (or the 1000-row cap), fetch the full `MessageSummary` rows for all IDs in `foundIDs`, ordered by `date ASC`.

### FTS Search Input Sanitization

The `q` parameter on `GET /api/v1/messages/search` is passed to SQLite FTS5 as a literal phrase match. Transform:
1. Replace every `"` (U+0022) in the input with `""` (two double-quote characters).
2. Wrap the result in a single pair of outer double quotes.

Example: `it's a "test"` → `"it's a ""test"""`. Apply byte-by-byte (no locale-specific interpretation). A unit test must verify that inputs containing `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are treated as literals.

**FTS5 tokenizer:** FTS5 uses the built-in `unicode61` tokenizer (the default), which performs Unicode-aware case folding. All FTS searches are effectively case-insensitive.

**Search SQL pattern:**

The `total` field in the response reflects total matches before pagination. A separate count query must be issued alongside the main query:

```sql
SELECT COUNT(*) FROM messages_fts
JOIN messages m ON messages_fts.rowid = m.id
WHERE messages_fts MATCH ?
  AND m.folder_id NOT IN (3, 5, 7)  -- (same folder/date conditions as the main query)
  -- AND m.folder_id = ?
  -- AND m.date >= ?
  -- AND m.date < ?
```

```sql
SELECT m.*, snippet(messages_fts, 4, '**', '**', '…', 15) AS snippet
FROM messages_fts
JOIN messages m ON messages_fts.rowid = m.id
WHERE messages_fts MATCH ?
  AND m.folder_id NOT IN (3, 5, 7)  -- (when no folder_id parameter is supplied: exclude Drafts, Scheduled, Junk)
  -- AND m.folder_id = ?            (when folder_id parameter is supplied; replaces the NOT IN clause)
  -- AND m.date >= ?                (when date_from parameter is supplied)
  -- AND m.date < ?                 (when date_to parameter is supplied)
ORDER BY rank
LIMIT ? OFFSET ?;
```

Date filtering uses lexicographic string comparison on the stored `date` column (UTC RFC 3339 strings). This is correct because all dates are normalised to UTC before storage, so lexicographic order matches chronological order. The `date_from` bound is inclusive (`>=`) and `date_to` is exclusive (`<`). Omitted date parameters are simply excluded from the WHERE clause — no coalescing to a sentinel value. The `ORDER BY rank` sorts by SQLite FTS5 BM25 score; lower (more negative) values are more relevant, so this produces highest-relevance first. No custom column weighting is applied.


## Database

**File:** `<data>/mymail.sqlite`

**File permissions:** Create data directory with mode `0700` and database file with mode `0600` in init mode.

**SQLite configuration:**
- Init: sets `PRAGMA journal_mode=WAL` before initializing schema.
- Server: 5-second busy timeout; additionally sets `mmap_size=134217728` (128 MiB mmap), `synchronous=NORMAL`. These pragmas are baked into the DSN so every connection in the pool inherits them. After opening, runs `PRAGMA optimize` once to update query-planner statistics. Connection pool is sized to `GOMAXPROCS` open/idle connections (allowing concurrent reads under WAL).
- LDA: 30-second busy timeout (`PRAGMA busy_timeout=30000`).
- Import: 5-second busy timeout (`PRAGMA busy_timeout=5000`).

All timestamps stored as UTC RFC 3339 strings. All incoming `date-time` fields (`send_at` in `SendRequest`/`DraftRequest`, and `until` in the snooze request) are normalized to UTC before storage and before any threshold comparison (e.g. `> now + 60 seconds`). A value with a non-UTC offset such as `2025-06-01T15:00:00+02:00` is converted to `2025-06-01T13:00:00Z` before being stored or compared.

**Database existence check:** The server, LDA, and import modes check that the database file exists at startup and exit immediately with a fatal error if it does not. The database must be created by `mymail -init` before running any other mode.

### Schema Migrations

Versioned using `PRAGMA user_version`. On every startup the server reads the current version and applies any missing migrations in order, each in a transaction. `PRAGMA user_version` is set inside the same transaction as the DDL, so a crash mid-migration leaves the version unchanged and is retried on next startup. **Note:** SQLite's handling of `PRAGMA user_version` inside a transaction may vary by version; treat the atomicity guarantee as best-effort. In particular, `CREATE VIRTUAL TABLE` for FTS5 may auto-commit on some SQLite versions. Every `CREATE TABLE`, `CREATE INDEX`, `CREATE TRIGGER`, and `CREATE VIRTUAL TABLE` statement therefore uses `IF NOT EXISTS` so the v0→v1 migration is safe to re-run after a partial-commit interruption.

Each `if v < N` block is checked independently (not `else if`), so a single startup can apply multiple sequential migrations.

### SQL Identifier Quoting

`messages.references` collides with the SQL reserved word `REFERENCES`. SQLite (and modernc.org/sqlite) accept the bare identifier in most contexts because the parser is lenient, but every read or write of the column must quote it as `"references"` (or `[references]`) to remain portable and to avoid surprising behaviour from future parser tightening. Code review and any future migration touching this column must enforce the quoting convention.

### Single-Writer Lock (`-import` vs server)

`-import` and the server cannot safely share a data directory because import bypasses the LDA serialization model and may hold long write transactions. Enforcement: on startup the server creates a shared advisory lock file `<data>/mymail.lock` (containing the server PID) and acquires an exclusive `flock(2)` on it. `-import` acquires the same lock at startup; if it is already held, the import exits with status 1 and a message naming the holding PID. The server releases the lock on shutdown; `flock` releases automatically on process exit so a crashed server does not leave the lock orphaned. The LDA mode does not take this lock — concurrent LDA + server is supported via SQLite WAL and the LDA's busy timeout, as documented in REQUIREMENTS.md.

```
user_version 0  →  uninitialized: apply all CREATE TABLE / CREATE INDEX / CREATE TRIGGER statements, then set user_version to 1
user_version 1  →  initial schema in place
user_version 2  →  adds composite indexes idx_messages_folder_date and idx_messages_folder_read
user_version 3  →  adds message_references join table and idx_msgref_ref for indexed thread forward lookups
user_version 4  →  adds idx_messages_in_reply_to for O(log n) thread forward query instead of full table scan
```

**Current schema version: 4** (v4 adds index on in_reply_to to eliminate full table scans during message threading).

### `folders`

```sql
CREATE TABLE IF NOT EXISTS folders (
    id         INTEGER PRIMARY KEY,
    name       TEXT    NOT NULL UNIQUE,
    slug       TEXT    NOT NULL UNIQUE,
    position   INTEGER NOT NULL DEFAULT 0,
    created_at TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
);
```

Built-in folders seeded by `mymail -init` (id=1..7); user-created folders have `id >= 100`.

If `-identity-address` is supplied to `mymail -init`, an initial identity row is inserted into the `identities` table with `name` from `-identity-name` (empty string if omitted), `address` normalised via Unicode simple casefolding, `is_default = 1`, and `position = 0`. The address must be a valid RFC 5322 addr-spec; init exits with `1` otherwise.

**User folder ID generation:** When creating a user folder, the service layer generates the ID explicitly rather than relying on `AUTOINCREMENT`. Query `SELECT COALESCE(MAX(id), 99) + 1 FROM folders WHERE id >= 100` to get the next candidate, then INSERT with that ID. Retry on `SQLITE_CONSTRAINT` (duplicate key) by re-querying. This guarantees IDs ≥ 100 without relying on SQLite's sequence table.

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
    identity_id   INTEGER REFERENCES identities(id) ON DELETE SET NULL,
    message_id    TEXT UNIQUE,
    in_reply_to   TEXT,
    references    TEXT,                    -- newline-separated (\n); serialized as JSON array by the API
    from_addr     TEXT    NOT NULL DEFAULT '',
    to_addr       TEXT    NOT NULL DEFAULT '',
    cc_addr       TEXT    NOT NULL DEFAULT '',
    bcc_addr      TEXT    NOT NULL DEFAULT '',
    reply_to_addr TEXT    NOT NULL DEFAULT '',
    subject       TEXT    NOT NULL DEFAULT '',
    date          TEXT    NOT NULL,        -- RFC 3339 UTC
    body_text     TEXT    NOT NULL DEFAULT '',
    body_html     TEXT    NOT NULL DEFAULT '',
    raw           BLOB,                        -- NULL for drafts (no raw RFC 5322 bytes until sent)
    read          INTEGER NOT NULL DEFAULT 0,
    flagged       INTEGER NOT NULL DEFAULT 0,
    has_attachments INTEGER NOT NULL DEFAULT 0,
    has_external_images INTEGER NOT NULL DEFAULT 0,
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
CREATE INDEX IF NOT EXISTS idx_messages_folder_date  ON messages(folder_id, date DESC);
CREATE INDEX IF NOT EXISTS idx_messages_folder_read  ON messages(folder_id, read);
CREATE INDEX IF NOT EXISTS idx_messages_in_reply_to  ON messages(in_reply_to) WHERE in_reply_to IS NOT NULL;

CREATE TRIGGER IF NOT EXISTS messages_updated_at AFTER UPDATE ON messages WHEN new.updated_at = old.updated_at BEGIN
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

The API exposes `send_failure_count > 0` as the boolean `send_failed`; the raw count is not exposed. `send_failure_count` and `send_error` are intentionally not cleared when a message is moved to Trash; the UI must suppress the `send_failed` badge when the message is in Trash (`folder_id = 4`).

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

Content table: tokens stored in FTS index, row content in `messages`. `body_html` is not indexed directly; `body_text` is derived from sanitized HTML when no plain-text part is present, so all content is searchable. Content removed by the sanitizer (e.g. `<script>` text) is intentionally not indexed. `bcc_addr` and `reply_to_addr` are intentionally excluded from the index: BCC data is private recipient metadata (including it would expose BCC relationships via search), and `reply_to_addr` is rarely a meaningful search target.

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

### `message_references`

Join table that denormalises the newline-separated `messages.references` column into one row per reference. Populated at insert/update time by `insertMessageRefs`; also backfilled from existing data as part of the v3 migration. The `idx_msgref_ref` index enables O(log n) forward thread lookups without scanning the `messages` table.

```sql
CREATE TABLE IF NOT EXISTS message_references (
    message_id INTEGER NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    ref_msg_id TEXT    NOT NULL,
    UNIQUE (message_id, ref_msg_id)
);

CREATE INDEX IF NOT EXISTS idx_msgref_ref ON message_references(ref_msg_id);
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

**Identity deletion cleanup:** When `DELETE /identities/{id}` is processed, after the identity row is deleted (which sets `messages.identity_id = NULL` via the `ON DELETE SET NULL` foreign key), the handler must also issue:
```sql
UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3 AND from_addr = ?
```
binding `?` to the deleted identity's `address`. This clears `from_addr` only on draft rows whose `from_addr` matches the deleted address, regardless of how `identity_id` was stored (the FK cascade has already set `identity_id = NULL` for all drafts that referenced this identity). Drafts with a different `from_addr` are left unchanged.

**`PUT /identities/{id}` default handling:** If `is_default: true` is supplied, that identity becomes the default and all others are updated to `is_default = 0` in the same transaction. If `is_default` is absent or `false`, the identity's current default status is preserved unchanged — the field only acts when explicitly `true`. This rule ensures the "exactly one default" invariant cannot be violated by a PUT request.

**Identity address-change cleanup:** When `PUT /identities/{id}` is processed and the identity's `address` changes, the handler must also update `from_addr` on drafts that reference this identity:
```sql
UPDATE messages SET from_addr = ? WHERE identity_id = ? AND folder_id = 3
```
binding the new address and the identity id. This keeps draft `from_addr` values consistent until the next draft auto-save re-resolves the identity. If the address is unchanged, this UPDATE is a no-op and may be skipped.

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

**Contact list ordering SQL:**
```sql
ORDER BY CASE WHEN name = '' THEN 1 ELSE 0 END, LOWER(name), LOWER(address)
```
This places empty-name contacts after named contacts, with named contacts sorted case-insensitively by name, then case-insensitively by address for ties.

**Contact autocomplete SQL** (when the `q` query parameter is supplied):
```sql
SELECT * FROM contacts
WHERE LOWER(name) LIKE '%' || LOWER(?) || '%'
   OR LOWER(address) LIKE '%' || LOWER(?) || '%'
ORDER BY CASE WHEN name = '' THEN 1 ELSE 0 END, LOWER(name), LOWER(address)
LIMIT ? OFFSET ?
```
The `q` value is bound twice — once for the name match, once for the address match. When `q` is absent, the WHERE clause is omitted. The total count for pagination uses the same filter:
```sql
SELECT COUNT(*) FROM contacts
WHERE LOWER(name) LIKE '%' || LOWER(?) || '%'
   OR LOWER(address) LIKE '%' || LOWER(?) || '%'
```

Upsert must use a single atomic statement:
```sql
INSERT INTO contacts (address, name, created_at, updated_at)
VALUES (?, ?, strftime('%Y-%m-%dT%H:%M:%SZ','now'), strftime('%Y-%m-%dT%H:%M:%SZ','now'))
ON CONFLICT(address) DO UPDATE SET
    name = excluded.name,
    updated_at = excluded.updated_at
WHERE contacts.name = ''
```

`PUT /contacts/{id}` has no trigger to auto-set `updated_at`, so the UPDATE statement must include `updated_at = strftime('%Y-%m-%dT%H:%M:%SZ', 'now')` explicitly.

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

Filters are listed in `position ASC, id ASC` order (consistent with the identity sort order).

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

**Charset decoding:** Each text body part is decoded to UTF-8 before storage. Use `golang.org/x/net/html/charset` (`charset.NewReader`) to wrap the per-part body reader: it inspects the `Content-Type` charset parameter and, when known, returns a UTF-8 reader; when the declared charset is unrecognised the reader passes bytes through, which combined with `utf8.ToValidUTF8(b, "�")` (or the equivalent rune-by-rune copy with `utf8.RuneError` replacement) yields the U+FFFD-replacement behaviour required by REQUIREMENTS. Add `golang.org/x/net` to `go.mod` for this; no other transitive C dependencies are pulled in.

**Plain-text derivation from HTML:** when the message has an HTML part but no `text/plain` part, run the **sanitized** `body_html` through `html2text.FromString(html, html2text.Options{PrettyTables: false, OmitLinks: false})`. The library is invoked after sanitization (not on the raw HTML) so any markup the sanitizer stripped is also absent from `body_text` — keeping the FTS index aligned with what the user actually sees. Treat the html2text version as part of the schema: bumping it requires a backfill pass that re-derives `body_text` for every affected row and rebuilds the FTS5 entries.

**`has_external_images` computation:** After sanitization, compute and persist the `has_external_images` flag (see `has_external_images` Computation below) before inserting the message row. This step is part of both the LDA pipeline and the batch import pipeline.

### Duplicate Detection

```sql
SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)
```

Run before spam detection and filter evaluation. Use `INSERT OR IGNORE` on the `messages` table as a race-safe guard for concurrent LDA processes. Matching is a case-sensitive byte comparison (`=` operator). Messages without a `Message-ID` header have `NULL` stored; since `NULL = NULL` is false in SQL, messages without a Message-ID are never considered duplicates of each other and are always stored.

**Race case exit code:** After the final `INSERT OR IGNORE`, check `changes()` (or the Go driver's `RowsAffected`). If it returns 0, a concurrent LDA process inserted the same message between the initial `SELECT EXISTS` check and the INSERT. This is not an error — the message is already in the database. The LDA must exit 0 in this case (same as a detected duplicate at the `SELECT EXISTS` step).

### Message-ID Generation in LDA Mode

When an incoming message lacks a `Message-ID` header, generate one as `<uuid@domain>` where `domain` is extracted from the first address in the `To` header. If the `To` header is absent or unparseable, fall back to `localhost`.

### Database Insertion

Insert message record and attachments in a single transaction.

### Contacts Upsert

Only the `From` address is upserted for incoming mail (To/Cc not auto-added). Lower-case the address before storage using Unicode simple casefolding (`golang.org/x/text/cases` with `language.Und` and `cases.Fold()`). The same normalization applies to outgoing contacts (To/Cc/Bcc addresses upserted on send). For outgoing contacts, extract the display name from the address string (e.g. `"John Doe <john@example.com>"` → name `John Doe`) and apply the same rule as incoming: only update the stored name when it is currently empty.

### Error Handling

**Definition of parse failure:** any error returned by `net/mail.ReadMessage()` (missing or malformed headers) is a hard parse failure. Missing optional fields (e.g. absent `Date`, absent `Message-ID`, empty `Subject`) are treated as warnings and handled gracefully, not as failures.

- `SQLITE_BUSY`: retry with exponential backoff for up to 30 seconds, then exit `75`.
- Parse failure: log to stderr, exit `1`.
- All other errors: log to stderr, exit `75`.


## Thin LDA Client (`cmd/lda`)

Built as a separate binary (`go build -tags netgo ./cmd/lda/`) with no SQLite, ogen, or HTTP server dependencies. Binary size ≈3 MB; peak RSS ≈3 MB regardless of message size (the raw bytes are held in memory only for the duration of the socket write).

### Socket Protocol

The UNIX socket uses a single-message half-duplex protocol over a `SOCK_STREAM` connection:

1. **Client → Server:** raw RFC 5322 message bytes (the full stdin content).
2. **Client signals end:** half-close with `CloseWrite()` (sends TCP FIN on the write side).
3. **Server reads** until EOF on the connection (the half-close signals end of input).
4. **Server processes** the message through the full LDA pipeline (`runCore`).
5. **Server → Client:** one of three ASCII strings (no newline): `ok`, `parse_error`, or `transient_error`.
6. **Server closes** the connection; client reads the response then exits.

Response → exit code mapping in the thin client:

| Response          | Exit code | Meaning                         |
|-------------------|-----------|---------------------------------|
| `ok`              | 0         | Delivered (or duplicate/drop)   |
| `parse_error`     | 1         | Permanent failure — will bounce |
| `transient_error` | 75        | Temporary failure — MTA retries |
| connection error  | 75        | Socket unreachable — MTA retries |
| unexpected        | 75        | Safe fallback                   |

The thin client uses a 30-second dial timeout (`net.DialTimeout`); if the server is overloaded and not accepting connections within that window the client exits 75.

### Server Socket Binding

`lda.BindSocket(path)` removes any stale socket file (from a crashed previous run) then calls `net.Listen("unix", path)`. `lda.ServeSocket(ctx, ln, db)` runs `Accept` in a loop; each connection is handled in its own goroutine. When `ctx` is cancelled (server shutdown) the listener is closed and the socket file is removed.

The socket is created with the default umask-derived permissions. The operator is responsible for ensuring the socket directory and/or file permissions allow the MTA delivery user to connect (e.g. shared group membership or a world-accessible containing directory).


## Background Scheduler Implementation

Single background goroutine started on server startup, stopped cleanly via context cancellation on shutdown.

**Re-entrance guard:** Uses a mutex so overlapping ticks are skipped rather than run concurrently.

**Deferred send query:**
```sql
SELECT id FROM messages
WHERE folder_id = 5
  AND send_at <= ?
ORDER BY send_at ASC;
```
Bind an explicit Go UTC timestamp (`time.Now().UTC().Format(time.RFC3339)`) rather than `CURRENT_TIMESTAMP` to ensure consistent comparison against the RFC 3339 UTC strings stored in the column.

**Conditional UPDATE before send** (prevents race with HTTP cancel handler):
```sql
UPDATE messages SET ... WHERE id = ? AND send_at IS NOT NULL AND folder_id = 5
```

The `DELETE /api/v1/scheduled/{id}` handler must clear `send_at`, reset `send_failure_count` to 0, clear `send_error` (set to NULL), and set `folder_id = 3` in a **single** UPDATE statement to avoid a race window and to ensure the message does not carry a failure badge in the Drafts list after cancellation.

**Failure handling:** The scheduler distinguishes two cases based on `send_failure_count`:

- **1st and 2nd failures** (`send_failure_count < 2`): increment the failure count and record the error; leave the message in the Scheduled folder so it will be retried.
  ```sql
  UPDATE messages SET send_failure_count = send_failure_count + 1, send_error = ?
  WHERE id = ? AND folder_id = 5 AND send_failure_count < 2
  ```
- **3rd failure** (`send_failure_count >= 2`): increment the count, record the error, move to Drafts, and clear `send_at` — all in a single UPDATE. Clearing `send_at` preserves the invariant that `send_at` is non-null only for messages in the Scheduled folder.
  ```sql
  UPDATE messages SET folder_id = 3, send_at = NULL, send_failure_count = send_failure_count + 1, send_error = ?
  WHERE id = ? AND folder_id = 5 AND send_failure_count >= 2
  ```

**Snooze creation handler (`POST /messages/{id}/snooze`):** Validate that `until >= now + 60 seconds` (i.e. at least 1 minute ahead, inclusive); return 400 otherwise. Two cases must then be handled separately to preserve the "original return folder" invariant:

- **First snooze** (message is not currently in Snoozed, i.e. `folder_id ≠ 6`): move the message to Snoozed, set `snoozed_until`, and record the current `folder_id` as `snooze_folder`.
  ```sql
  UPDATE messages
  SET snooze_folder = folder_id,
      folder_id = 6,
      snoozed_until = ?
  WHERE id = ? AND folder_id != 6
  ```
- **Re-snooze** (message is already in Snoozed, i.e. `folder_id = 6`): update `snoozed_until` only — do **not** change `snooze_folder`, so the original return folder is preserved across reschedules.
  ```sql
  UPDATE messages
  SET snoozed_until = ?
  WHERE id = ? AND folder_id = 6
  ```

Determine which case applies by inspecting the current `folder_id` before issuing the UPDATE (or check `changes()` after the conditional UPDATE). Return 400 if the message's current `folder_id` is one of the forbidden folders (Drafts=3, Sent=2, Trash=4, Junk=7, Scheduled=5).

**Snooze expiry query:**
```sql
SELECT id, snooze_folder FROM messages
WHERE folder_id = 6
  AND snoozed_until <= ?
ORDER BY snoozed_until ASC;
```
Bind an explicit Go UTC timestamp (same pattern as the deferred send query).

For each expired snooze, the scheduler moves the message to `COALESCE(snooze_folder, 1)` (falling back to Inbox if the return folder was deleted or was never set), clears `snoozed_until` and `snooze_folder`, and marks the message as unread — all in a single UPDATE:
```sql
UPDATE messages
SET folder_id = COALESCE(snooze_folder, 1),
    snoozed_until = NULL,
    snooze_folder = NULL,
    read = 0
WHERE id = ? AND folder_id = 6
```

**Cancel-snooze handler (`DELETE /messages/{id}/snooze`):** Must return the message to `COALESCE(snooze_folder, 1)` (same fallback as the scheduler), clear both `snoozed_until` and `snooze_folder`, and mark the message unread — all in a single UPDATE. Marking unread matches natural snooze expiry so the message reappears as new in the destination folder regardless of how the snooze ended. The `folder_id` returned in the response must reflect the actual folder the message was moved to (the result of the COALESCE), not always a hard-coded value:
```sql
UPDATE messages
SET folder_id = COALESCE(snooze_folder, 1),
    snoozed_until = NULL,
    snooze_folder = NULL,
    read = 0
WHERE id = ? AND folder_id = 6
```
Return 400 if the message is not currently in the Snoozed folder (i.e. the UPDATE affects 0 rows after confirming the message exists).

The scheduler holds the SQLite write lock only for the duration of each individual UPDATE, not across the full tick.

**Known limitation — double-send on restart:** If the server restarts while a `sendmail` process is running, the new scheduler retries and may send twice. The conditional UPDATE prevents duplicate database records but not duplicate email delivery. Deduplication of duplicate deliveries is outside mymail's scope; operators may configure MTA-level deduplication if needed.


## Batch Import Implementation

### Libraries

**mbox:** `github.com/emersion/go-mbox` (primary, v1, MIT). API: `mbox.NewReader(r io.Reader)` → `*Reader`; call `NextMessage() (io.Reader, error)` in a loop. Handles mboxo and mboxrd. Add `github.com/tvanriper/mbox` only if SVR4 mboxcl support is needed.

**Maildir:** `github.com/emersion/go-maildir` (v0.6.0, MIT). API: `maildir.Dir(path)` → iterate with `Keys()`, open each with `Message(key)`.

### Implementation Notes

- Open database and run migrations before importing.
- **Folder resolution:** For each `<folder>` value in the mapping arguments, resolve the target folder as follows: if the value matches a built-in slug (`inbox`, `sent`, `drafts`, `trash`, `junk`) case-sensitively, use that built-in folder. **Warning:** built-in slug matching is case-sensitive; `Inbox` (capital I) does not match the built-in inbox and would instead create a new user folder named "Inbox". If the value is `scheduled` or `snoozed`, exit with code 1 and an error message (these folders have semantic fields that import cannot populate). Otherwise, search user-created folders by name using a case-insensitive match (`LOWER(name) = LOWER(?)`). If no match is found, create a new user-created folder with that value as its name (applying the standard slug-generation algorithm). If the same `<folder>` value appears in multiple mapping triplets, all triplets share the single resolved/created folder — no attempt is made to create the folder twice.
- Use batched transactions: commit every 500 messages to bound WAL file size. If a batch fails, only that batch is rolled back; previously committed batches are retained. There is no way to identify which individual messages were committed after a partial failure — re-run the full import after fixing the source data (duplicate detection will skip already-imported messages).
- Run the full LDA parsing pipeline (HTML sanitization, `cid:` resolution, `body_text` derivation, `has_external_images` computation, attachment extraction) for each message. Skip only spam detection and filter application.
- Upsert the `From` address of each successfully imported message into the contacts table using the same upsert logic as the LDA (update name only when the stored name is empty).
- For Maildir, map the `S` (Seen) flag to `read=1` and the `F` (Flagged) flag to `flagged=1`.
- **Maildir message ordering:** sort the keys returned by `Keys()` lexicographically (ascending) before processing. Standard Maildir filenames begin with a Unix timestamp, so lexicographic order approximates delivery order. This defines "source order (oldest first)" for Maildir imports.
- For mbox: call `os.Stat(path)` **before** opening the mbox reader to capture the file's mtime; this value is used as the timestamp fallback when a `From ` separator timestamp cannot be parsed, and must be available throughout the per-message loop. The `go-mbox` `NextMessage()` call reads and discards the `From ` separator line internally before returning the message reader, so the timestamp must be captured before `NextMessage()` is called. Use a **two-pass approach**: in the first pass, open the file with `bufio.Scanner` and collect the timestamp suffix of every line matching `^From \S` in order; in the second pass, seek back to the beginning and use `mbox.NewReader()` / `NextMessage()` for message parsing. The nth timestamp collected in the first pass corresponds to the nth message yielded by `NextMessage()`. This works correctly for mboxrd files (where embedded `From ` lines are escaped as `>From ` and therefore not counted). For mboxo files, embedded unescaped `From ` lines are rare in practice; if the number of timestamps collected in the first pass does not match the number of messages yielded by the second pass (in either direction), fall back to the file mtime for all messages in the file. Strip the `From ` line before storing the `raw` BLOB. Use streaming `NextMessage()` API — do not load the entire file into memory.
- mbox `From ` timestamp parsing: after splitting off the address prefix on the first ASCII space, try the following Go layouts in order:
  1. `"Mon Jan _2 15:04:05 2006"` — canonical format (no timezone)
  2. `"Mon Jan _2 15:04:05 MST 2006"` — three-letter timezone abbreviation (e.g. `CST`)
  3. `"Mon Jan _2 15:04:05 -0700 2006"` — numeric UTC offset (e.g. `+0200`)
  If all three layouts fail, fall back to the file's mtime captured by the `os.Stat` call above; if the file mtime cannot be obtained either, log a warning and skip the message rather than substituting the current time.
- `date` field: use the original `Date` header. Fallback: mtime of the Maildir file (Maildir) or `From ` separator timestamp (mbox). If the fallback is also unavailable, log a warning and skip the message. Never use current time as fallback during import.


## Multipart Form-Data Attachment Extraction

For `POST /messages/send-with-attachments`, `POST /drafts-with-attachments`, and `PUT /drafts-with-attachments/{id}`, extract the following from each file part of the multipart request:

- **Filename:** read from the `Content-Disposition: form-data; filename="..."` parameter. For RFC 5987–encoded filenames (`filename*=UTF-8''...`), decode before use. If the filename parameter is absent, use `untitled` as the default.
- **Content-Type:** read from the part's `Content-Type` header. If the header is absent, use `application/octet-stream` as the default.

## Outgoing Mail Implementation

### Sendmail Path Resolution

At server startup, resolve the `-sendmail` binary path:

```go
path, err := exec.LookPath(cfg.Sendmail)
if err != nil {
    log.Fatalf("sendmail binary %q not found or not executable: %v", cfg.Sendmail, err)
}
cfg.Sendmail = path // store the resolved absolute path
```

`exec.LookPath` performs PATH search when the value is not absolute and verifies the file is executable. The server exits with a fatal error and a non-zero exit code if the lookup fails; it does not start serving HTTP. The resolved absolute path is used for all subsequent `exec.Command` calls.

### Message Construction

1. Set `Date` to current time at send (RFC 5322 format).
2. Generate `Message-ID` as `<uuid@domain>` where `domain` is extracted from the sender address.
3. Strip CR (`\r`), LF (`\n`), and NUL (`\0`) from all user-supplied header values before encoding (`to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr`, `subject`, `in_reply_to`, every element of `references`, and the identity display name).
4. Encode non-ASCII header values as RFC 2047 encoded words.
5. Encode attachment data as base64.
6. Body structure:
   - If neither `body_text` nor `body_html` is provided (both empty): single empty `text/plain` part.
   - If only one body part is provided: single `text/plain` or `text/html` part directly.
   - If both text and HTML are provided: `multipart/alternative` with both parts.
   - Wrap in `multipart/mixed` if attachments are present.
7. Add `Reply-To` header only if `reply_to_addr` is non-empty.
8. Add `In-Reply-To` header only if `in_reply_to` is non-empty. Each value is wrapped in angle brackets at emission time (the storage format strips them — see "Threading-header storage format" below).
9. Add `References` header only if `references` is a non-empty list. Wrap each value in angle brackets and join the resulting tokens with single spaces. Both `In-Reply-To` and `References` are required for replies sent through mymail to thread on the recipient side.

### Threading-header storage format

`messages.message_id`, `messages.in_reply_to`, and the entries of `messages.references` are all stored **without** angle brackets (the brackets are syntactic delimiters from RFC 5322, not part of the identifier value). For client-supplied values in `SendRequest` and `DraftRequest`, the server strips surrounding angle brackets from `in_reply_to` before storage (consistent with the stripping applied to `references` elements). On read:

- `MessageDetail.message_id` is serialized without brackets.
- `MessageDetail.in_reply_to` is serialized without brackets.
- `MessageDetail.references` re-adds angle brackets to each element on serialization (matching the openapi description "as it appears in the header, including angle brackets").
- The header-based thread query joins on the bracket-stripped form on both sides, so threading works irrespective of bracket policy in the stored raw bytes.

`messages.in_reply_to` is a single TEXT column. RFC 5322 syntactically permits multiple Message-IDs in `In-Reply-To`; if the parsed header contains more than one, only the **first** ID is stored. The discarded IDs remain visible in `references` (most mail clients populate both headers consistently).

`messages.references` is stored as the parsed Message-IDs joined by `\n`. **Maximum stored length: 16 KiB.** When the joined value exceeds 16 KiB, the **oldest** IDs are dropped from the front and the most recent IDs are retained until the value fits. The first/last semantics (oldest-first ordering of `References`) is preserved among the retained IDs. This same truncation policy applies to client-supplied `references` arrays in `POST /messages/send`, `POST /messages/send-with-attachments`, and `PUT /drafts/{id}`: if the joined value would exceed 16 KiB, the oldest IDs are silently dropped before storage (consistent with the inbound truncation behaviour).

### Sendmail Integration

- Pipe to `sendmail -t -oi`.
- Maximum 30-second timeout; treat timeout as a failure.
- Capture stderr on non-zero exit or timeout: retain the **last** 4 KB (oldest output is discarded when stderr exceeds 4 KB).
- **HTTP status mapping:** any non-zero exit code or timeout → `500 Internal Server Error`. The sendmail stderr output is included in the `{"error": "..."}` response body.

### Outgoing HTML Sanitization

Sanitize `body_html` using the standard email HTML sanitization policy before constructing the MIME message and before storing the message in the Sent folder. This applies to all outgoing HTML content regardless of source (composed, quoted, or forwarded). Client-side sanitization by the rich-text editor is defence in depth only; server-side sanitization is authoritative.

**`has_external_images` for outgoing messages:** After sanitizing `body_html`, compute the `has_external_images` flag using the same algorithm as the LDA and import pipelines (see `has_external_images` Computation below) before storing the message in the Sent folder. The same computation applies when storing a message in the Scheduled folder (deferred send) — the flag is computed at schedule-creation time so message list and detail responses are accurate before the message is actually sent.

### Bcc Handling

The `Bcc` header is preserved in the raw BLOB stored in Sent. Relies on the MTA (`sendmail`) stripping `Bcc` from outgoing copies before delivery.


## HTML Sanitization Implementation

Use `github.com/microcosm-cc/bluemonday` with a custom email-appropriate policy.

**Per-element attribute configuration:** bluemonday's `AllowAttrs(...).OnElements(...)` form is used to scope attributes to the elements where they are valid. The policy mirrors the per-element matrix in REQUIREMENTS.md → HTML Sanitization (e.g. `colspan`/`rowspan` only on `td`/`th`, `align` only on table-related elements plus headings, paragraphs, and `div`). A unit test verifies that disallowed combinations (e.g. `<p colspan="2">`) are stripped.

**CSS property matching:** bluemonday's CSS handling is regex-based. After parsing the `style` attribute into declarations, each declaration's property name is checked for **exact** match against the allowlist (so `background` does not match `background-color`). The forbidden value patterns below are checked against the raw declaration value before the property allowlist. The residual risk is that a sufficiently obscure CSS comment or encoding trick could slip past the regex; the consequence is at most an unexpected style being applied inside the sandboxed iframe (no script execution, no network access). Mitigations beyond regex matching are not added because no maintained Go CSS parser library matches the dependency profile (CGO-free, single binary). This decision is documented here so it is auditable.

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


## Raw Message Download (`GET /messages/{id}/raw`)

The response sets `Content-Type: message/rfc822` and `Content-Disposition: attachment; filename=...` where the filename is built as `<id>.eml` (numeric, always ASCII-safe; no header injection vector). The numeric id avoids leaking subject content into URL bars and download history while still being unique. RFC 8187 percent-encoding therefore never has to be applied to this endpoint.

When the `raw` column is NULL (the message is a draft), the endpoint returns `200 OK` with `Content-Type: application/json` and body `{}` (empty JSON object) rather than a `message/rfc822` response.

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

### Signature Plain-Text → HTML Conversion

When inserting a signature into Quill's HTML content model (on compose open or identity change), convert the plain-text signature as follows:
1. Normalize line endings: replace all `\r\n` sequences with `\n`, then replace any remaining bare `\r` with `\n`.
2. Detect the standard email signature delimiter: a line whose exact content is `-- ` (two hyphens, one space). Replace that entire line (including its trailing newline) with `<hr>`.
3. In all other lines, escape `&` → `&amp;`, `<` → `&lt;`, `>` → `&gt;`.
4. Replace each remaining `\n` with `<br>`.
Line-ending normalization runs first so that delimiter detection reliably matches `-- ` regardless of how the signature was stored.

### Draft Auto-Save Logic

- Auto-save fires every 30 seconds.
- **Forward exception:** `POST /api/v1/drafts` is called immediately at form-open time for Forward (because `source_message_id` for attachment copying is only valid on the initial POST). For Reply, Reply-All, and new compose, the first POST is deferred until the first 30-second tick.
- **Navigate-away before first tick:** for Reply, Reply-All, and new compose, if the user navigates away before the first 30-second tick fires (i.e. no draft ID exists yet), perform a `POST /api/v1/drafts` immediately — the same request that the first tick would have issued. If this POST fails, show a brief warning but do not block navigation (consistent with the documented failure behaviour for the navigate-away save on subsequent ticks).
- `PUT /api/v1/drafts/{id}` (JSON endpoint) does not modify attachment rows.
- `PUT /api/v1/drafts-with-attachments/{id}` replaces attachments wholesale. If no file parts are present, all existing attachments for the draft are deleted — an empty file-parts list is a valid way to clear all attachments.
- To remove individual attachments, call `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}`.
- On every PUT, the server resolves `identity_id` to its `address` and updates both the `identity_id` column and `from_addr` in the `messages` row. If `identity_id` is absent in the PUT body, `identity_id` is set to NULL and `from_addr` is set to the default identity's address. This keeps the Drafts message list accurate when the user changes the From identity. On POST (new draft), `identity_id` is stored directly from the request body (or NULL if absent), and `from_addr` is set to the specified identity's address, or to the default identity's address when `identity_id` is absent — mirroring the PUT rule. A 500 is returned when no default identity exists, which would indicate a database integrity violation. If `identity_id` is supplied but does not match any existing identity row, the server returns 400 with `{"error": "identity not found"}`; this applies to both `POST /drafts` and `PUT /drafts/{id}`.
- **No-identities-at-all case (first-run state):** when no identity rows exist in the database and `identity_id` is absent from the request body, `identity_id` is stored as NULL and `from_addr` is stored as empty string; 201 (or 200 for PUT) is returned without error. This allows drafts to be saved before any identity has been created.

### Draft Folder Check (`PUT`, `DELETE`, `POST .../send` on `/api/v1/drafts/{id}`)

All three handlers (`PUT /api/v1/drafts/{id}`, `DELETE /api/v1/drafts/{id}`, and `POST /api/v1/drafts/{id}/send`) must verify that the referenced message exists **and** has `folder_id = 3` (Drafts). If the message does not exist, return 404. If the message exists but `folder_id ≠ 3`, return 404 (indistinguishable from not found, to avoid exposing non-draft message metadata via the drafts API surface).

### Send Draft Logic (`POST /api/v1/drafts/{id}/send`)

1. Read the draft from the DB. Return 404 if not found or if `folder_id != 3` (not in Drafts).
2. Validate: at least one of `to_addr`, `cc_addr`, `bcc_addr` must be non-empty. Return 400 otherwise.
3. Resolve the identity: use `identity_id` from the draft if set, otherwise the default identity.
4. Look up all attachment rows for the draft.
5. Determine mode from `send_at` using a single threshold (the same rule applies to `POST /messages/send`, `POST /messages/send-with-attachments`, and `POST /drafts/{id}/send`):
   - **Scheduled** when `send_at` is non-null AND `send_at > now + 60 seconds`. Insert the message and attachments into the Scheduled folder (do not call sendmail), then delete the draft. Return 202.
   - **Immediate** in every other case (`send_at` is null, in the past, equal to now, or within the next 60 seconds). Run the standard outgoing mail pipeline (sanitize body_html, construct MIME message with all attachments, pipe to sendmail). On sendmail failure: return 500 with the error; draft is preserved unchanged. On success: insert the sent message and its attachments into the Sent folder, then delete the draft row (attachments cascade-delete).
6. Upsert recipients (To/Cc/Bcc) into the contacts table on immediate send only. For scheduled sends (`send_at > now + 60s`), skip the upsert here; the scheduler performs it when it actually calls `sendmail`. This rule applies identically to `POST /messages/send`, `POST /messages/send-with-attachments`, and `POST /drafts/{id}/send`.

### Direct Send Logic (`POST /api/v1/messages/send` and `POST /api/v1/messages/send-with-attachments`)

These endpoints follow the same pipeline as Send Draft Logic, applied directly to the request body:

1. Validate: at least one of `to_addr`, `cc_addr`, `bcc_addr` must be non-empty. Return 400 otherwise.
2. Resolve the identity: if `identity_id` is supplied in the request body, look it up; if no identity with that ID exists, return `400 {"error": "identity not found"}`. If `identity_id` is absent, use the default identity. If no identities exist at all, return `400 {"error": "no identity configured; create one in Settings → Identities first"}`.
3. Determine mode from `send_at` and execute the same Scheduled vs. Immediate logic described in Send Draft Logic.
4. Upsert recipients on immediate send only (same rule as Send Draft Logic).

### Mark All As Read

Call `POST /api/v1/folders/{id}/mark-all-read`.

### HTML Body Display

Render in a sandboxed `<iframe src="/api/v1/messages/{id}/body">` (no additional sandbox tokens — maximum restriction). The `GET /messages/{id}/body` endpoint returns a standalone HTML document (`<!DOCTYPE html>…`) with its own `Content-Security-Policy` response header:

- **Default (no external images):** `default-src 'none'; img-src data:; style-src 'unsafe-inline'; frame-ancestors 'self'`
- **External images (`?external=1`):** `default-src 'none'; img-src https: data:; style-src 'unsafe-inline'; frame-ancestors 'self'`

Because the CSP is delivered as a response header (not a `<meta>` tag), it is enforced by the browser regardless of sandbox mode. The `X-Frame-Options: SAMEORIGIN` header is also set. Per-message opt-in for external images: reload the iframe with `?external=1` appended to the URL. The iframe URL is swapped (not srcdoc rewritten) each time the user toggles "Load external images".

### `has_external_images` Computation

Computed at storage time and persisted as a column on `messages` (`has_external_images INTEGER NOT NULL DEFAULT 0`) so that the message list and detail responses do not have to re-scan the HTML on every request:

1. After sanitization, scan the resulting `body_html` with a simple HTML token walker (`golang.org/x/net/html`).
2. Set the flag to `1` if any `<img>` element has a `src` attribute beginning with `http://` or `https://` (case-insensitive). `data:` URIs do not count.
3. The flag is recomputed only when `body_html` is (re)written; it is not maintained otherwise.

Add the column to the v1 schema (initial schema, not a migration) and to the trigger surface that mirrors `messages` into `messages_fts` if needed (it is not indexed in FTS, so no trigger change is required).

### Notifications Polling

Poll `GET /api/v1/folders` every 30 seconds. Suspend while `document.visibilityState === 'hidden'`; resume immediately when the tab becomes visible.
