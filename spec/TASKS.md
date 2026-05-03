# mymail — Implementation Tasks

Each task below is self-contained and can be handed to a coding agent. Tasks are ordered so later tasks only depend on earlier ones, but tasks within a group may be executed in parallel. All tasks implement what is described in `spec/IMPLEMENTATION.md`, `spec/REQUIREMENTS.md`, and `spec/ARCHITECTURE.md`.


## T01: Go Module Dependencies & Project Scaffold

Add all required third-party dependencies to `go.mod` and create the directory scaffold for the layered architecture.

**Dependencies to add (`go get`):**
```
modernc.org/sqlite v1.48.1
github.com/microcosm-cc/bluemonday v1.0.27
github.com/jaytaylor/html2text v0.0.0-20230321000545-74c2419ad056
github.com/mikaelstaldal/go-server-common v1.0.0
github.com/emersion/go-mbox v1.0.4
github.com/emersion/go-maildir v0.6.0
github.com/ogen-go/ogen v1.20.2
github.com/go-faster/errors v0.7.1
github.com/go-faster/jx v1.2.0
golang.org/x/net v0.43.0
golang.org/x/text v0.30.0
```

**Directories to create** (with placeholder `.gitkeep` or minimal Go files so `go build` works):
```
internal/auth/
internal/handler/
internal/lda/
internal/model/
internal/repository/
internal/sanitize/
internal/service/
```

Do **not** implement any logic in this task — just get `go mod tidy` to succeed with all dependencies present and create the directory structure with empty package declarations.


## T02: REST API Code Generation Setup

Set up `ogen` code generation from `openapi.yaml` as the first substantive task, so all subsequent tasks can refer to the generated types.

1. Add a `//go:generate go run github.com/ogen-go/ogen/cmd/ogen --target internal/api --clean openapi.yaml` directive in a file at `internal/generate.go`.
2. Run `go generate ./internal/` to produce the generated stubs under `internal//`.
3. Create `internal/handler/handler.go` defining a `Handler` struct and implementing every method of the generated `Handler` interface. Stub all methods with `return nil, oas.ErrNotImplemented` (or the appropriate ogen not-implemented response) — subsequent tasks (T21–T24) will replace these stubs with real logic.
4. Confirm `go build ./...` succeeds with the generated code in place. Do **not** wire the router into `main.go` yet — that happens in T25.

**Route priority note:** ogen (backed by chi) gives static path segments priority over parameters, so `PATCH /folders/reorder` takes priority over `PATCH /folders/{id}`. No extra router config needed.


## T03: Data Model Types (`internal/model`)

Define Go data types for the database layer in `internal/model/`. Because T02 has already generated API-facing types under `internal/api/`, this task reuses those generated types wherever the field set and Go types are compatible, and only defines new types for representations that differ at the DB layer.

**Strategy:**
- Inspect the structs generated in `internal/api/` for each entity (Folder, Message, MessageSummary, Attachment, Identity, Contact, Filter, SpamFilterSettings, etc.).
- Where a generated struct maps cleanly to what the repository needs to scan from SQLite (field names align, Go types are compatible — e.g., `string`, `bool`, `int64`, `time.Time`), import and use the generated type directly in the repository and service layers. Do not redefine it in `internal/model/`.
- Where a generated struct differs from the DB representation (e.g., uses ogen's `opt.String`/`oas.OptNilString` for nullable fields that SQLite returns as `sql.NullString`, or omits DB-only columns), define a dedicated internal struct in `internal/model/` and write a conversion function to/from the ogen type.

**Types that must be defined in `internal/model/` regardless** (no ogen equivalent):

- **`DBMessage`** — full database row for the `messages` table, including columns not in any API response: `raw []byte` (BLOB, NULL for drafts), `send_failure_count int`, `snooze_folder *int64`. Use `sql.NullString` / `sql.NullInt64` / `sql.NullTime` for nullable columns. Provide `ToOASMessage() *oas.Message` and `ToOASMessageSummary() *oas.MessageSummary` conversion methods.
- **`DBAttachment`** — full row including `data []byte`; the ogen-generated attachment type likely omits the blob. Provide `ToOASAttachmentMeta() *oas.AttachmentMeta` conversion.
- **`ParsedMessage`** — used internally by the LDA and import pipeline, never serialised to JSON. Fields: `FromAddr`, `ToAddr`, `CcAddr`, `BccAddr`, `ReplyToAddr`, `Subject` (all `string`), `Date *time.Time`, `MessageID *string`, `InReplyTo *string`, `References []string`, `BodyText string`, `BodyHTML string`, `Attachments []DBAttachment` (without id/message_id), `HasExternalImages bool`.

**For all other entities** (Folder, Identity, Contact, Filter, SpamFilterSettings): examine the ogen-generated structs first. If their fields can be scanned from SQLite directly (converting INTEGER 0/1 to bool in a thin scanning helper), use them. Add a package-level comment in `internal/model/model.go` listing which types are ogen aliases vs. DB-native, so future contributors know where each type lives.


## T04: Database Initialization & Schema (`internal/repository`)

Implement database setup in `internal/repository/db.go`:

**Schema version:** 1 (initial schema, no migrations yet). The migration system uses `PRAGMA user_version`. On startup:
1. Read current `user_version`.
2. If `v < 1`: apply the full initial schema (all `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`, `CREATE TRIGGER IF NOT EXISTS`, `CREATE VIRTUAL TABLE IF NOT EXISTS` statements exactly as specified in `spec/IMPLEMENTATION.md` → Database section), then `PRAGMA user_version = 1` inside the same transaction. Each `if v < N` block is independent (not `else if`).

**Tables to create:**
- `folders` (with all columns and constraints)
- `messages` (with all columns; note: `references` column must always be quoted as `"references"` in SQL)
- `messages_fts` (FTS5 virtual table, content table over `messages`)
- `attachments` (with CASCADE delete)
- `identities`
- `contacts`
- `filters`
- `spam_filter_settings`

**Indexes and triggers:** create all as specified in IMPLEMENTATION.md (messages_updated_at, attachments_insert_flag, attachments_delete_flag, messages_fts_insert, messages_fts_delete, messages_fts_update).

**Functions to expose:**
- `OpenDB(path string, busyTimeout int) (*sql.DB, error)` — opens SQLite, sets WAL mode (only in init mode), sets busy_timeout pragma, runs migrations.
- `InitSchema(db *sql.DB) error` — applies migrations if needed.

**SQLite WAL and busy timeout:**
- Init mode: set `PRAGMA journal_mode=WAL` before schema init.
- Server: `PRAGMA busy_timeout=5000`.
- LDA: `PRAGMA busy_timeout=30000`.
- Import: `PRAGMA busy_timeout=5000`.

**File permissions:**
- Data directory: `os.MkdirAll(dir, 0700)`.
- Database file: `os.Chmod(dbPath, 0600)` after creation.

**Database existence check:** server, LDA, and import modes must check that the database file exists (via `os.Stat`) before opening and exit fatally if absent.


## T05: Init Mode & Built-in Seed Data

Implement `mymail -init` as a function callable from `main.go`.

**Steps:**
1. Parse flags: `-data`, `-identity-address` (required), `-identity-name` (optional, default `""`).
2. Validate `-identity-address` using `net/mail.ParseAddress` — accept only when the returned `Address.Name` is empty (bare addr-spec). Exit 1 on invalid.
3. Create data directory with `os.MkdirAll(dir, 0700)`.
4. Create/open SQLite database, set `PRAGMA journal_mode=WAL`, apply schema (via T04), set `PRAGMA user_version=1`.
5. Set database file permissions to `0600`.
6. Seed the seven built-in folders in a single transaction (exact ids, names, slugs, positions as in IMPLEMENTATION.md → folders table).
7. Seed the spam filter settings row:
   ```sql
   INSERT OR IGNORE INTO spam_filter_settings (id, enabled, score_header, score_threshold) VALUES (1, 1, 'X-Spam-Score', 5.0)
   ```
8. Apply Unicode simple casefolding to the identity address (using `golang.org/x/text/cases` with `language.Und` and `cases.Fold()`), then insert the identity row with `is_default=1`, `position=0`.
9. Exit 0 on success, 1 on any error (log error to stderr).


## T06: Repository — Folders

Implement `internal/repository/folder_repo.go` with a `FolderRepository` struct backed by `*sql.DB`.

**Methods:**

- `ListFolders(ctx) ([]oas.Folder, error)` — SELECT all folders ORDER BY position ASC, id ASC. For each, compute `unread_count` via `SELECT COUNT(*) FROM messages WHERE folder_id = ? AND read = 0`.
- `CreateFolder(ctx, name string, position *int) (oas.Folder, error)` — generate slug (see slug algorithm below), generate ID via `SELECT COALESCE(MAX(id), 99) + 1 FROM folders WHERE id >= 100` with retry on SQLITE_CONSTRAINT, INSERT, return created folder.
- `UpdateFolder(ctx, id int64, name *string, position *int) (oas.Folder, error)` — PATCH semantics: only update non-nil fields. Reject rename if name already exists (return 409 sentinel). Slug is never updated.
- `DeleteFolder(ctx, id int64) error` — reject if id < 100 (built-in). Move all messages in that folder to Trash (folder_id=4) via UPDATE before deleting folder row.
- `ReorderFolders(ctx, ids []int64) (int, error)` — validate: all existing folder IDs present exactly once (no missing, no unknown, no duplicates); update positions 0,1,2,… in a single transaction.
- `DeleteAllMessagesInFolder(ctx, folderID int64) error` — permanently delete messages if folder is Trash or Junk; otherwise move to Trash. (For Trash/Junk: `DELETE FROM messages WHERE folder_id = ?`. For others: `UPDATE messages SET folder_id = 4 WHERE folder_id = ?`.)
- `MarkAllRead(ctx, folderID int64) (int, error)` — `UPDATE messages SET read = 1 WHERE folder_id = ? AND read = 0`, return rows affected.
- `GetFolderByID(ctx, id int64) (oas.Folder, error)` — return 404 sentinel if not found.

**Slug generation algorithm** (applied in CreateFolder):
1. Unicode NFKD normalization.
2. Lowercase.
3. Replace any run of non-alphanumeric ASCII characters with a single hyphen.
4. Trim leading and trailing hyphens.
5. If the resulting slug collides with an existing slug, append `-2`, `-3`, etc. until unique.


## T07: Repository — Messages

Implement `internal/repository/message_repo.go`.

**Methods:**

- `ListMessages(ctx, folderID int64, limit, offset int) ([]oas.MessageSummary, int, error)` — list messages in folder ordered by `date DESC`. Return total count separately.
- `GetMessage(ctx, id int64) (model.DBMessage, error)` — full row including raw BLOB. `"references"` column split on `\n` to produce `[]string`.
- `GetMessageSummary(ctx, id int64) (oas.MessageSummary, error)`
- `InsertMessage(ctx, msg model.DBMessage) (int64, error)` — insert in a transaction. `"references"` joined with `\n` for storage. Return inserted ID.
- `UpdateMessage(ctx, id int64, fields map[string]any) (model.DBMessage, error)` — PATCH: update only provided fields (folder_id, read, flagged). Use `updated_at` trigger.
- `BulkUpdateMessages(ctx, ids []int64, read *bool, flagged *bool) (int, error)` — UPDATE with IN clause. Return `changes()`.
- `DeleteMessage(ctx, id int64) error` — if message is in Trash (folder_id=4) or Junk (folder_id=7): permanently delete. Otherwise: move to Trash.
- `BulkDeleteMessages(ctx, ids []int64) error` — same Trash/permanent logic per message, or batch: move non-Trash/Junk to Trash, permanently delete those in Trash/Junk. Apply ALL-or-nothing: return 404 if any ID does not exist (check before acting).
- `MoveMessages(ctx, ids []int64, folderID int64) (int, error)` — move all to target folder. Return 404 if any ID missing.
- `GetRawMessage(ctx, id int64) ([]byte, error)` — return raw BLOB (may be nil for drafts).
- `GetMessageThread(ctx, id int64) ([]oas.MessageSummary, bool, error)` — implement the iterative Go query loop for transitive closure (forward query + backward query), capped at 1000. Subject-based fallback if only the seed message found. Return truncated flag. See IMPLEMENTATION.md → Thread Algorithm for the exact SQL and loop logic.
- `SnoozeMessage(ctx, id int64, until time.Time) (model.DBMessage, error)` — validate `until >= now + 60s`. First-snooze vs re-snooze logic (two separate UPDATE statements). Return 400 if message in forbidden folder.
- `CancelSnooze(ctx, id int64) (model.DBMessage, error)` — move to COALESCE(snooze_folder, 1), clear snoozed_until and snooze_folder, set read=0. Return 400 if not in Snoozed folder.
- `MarkJunk(ctx, id int64) (model.DBMessage, error)` — move to Junk (folder_id=7), set read=1.
- `MarkNotJunk(ctx, id int64) (model.DBMessage, error)` — move to Inbox (folder_id=1), set read=0.
- `SearchMessages(ctx, q string, folderID *int64, dateFrom, dateTo *time.Time, limit, offset int) ([]oas.MessageSummary, int, error)` — FTS5 search. Sanitize q: replace `"` with `""`, wrap in outer `"`. Append folder and date filters. Snippet via `snippet(messages_fts, 4, '**', '**', '…', 15)`. HTML-escape snippet. Count query runs separately.

**Bulk operation ID limit:** return 400 if len(ids) > 1000.

**`"references"` quoting:** every SQL query that reads or writes this column must quote it as `"references"`.


## T08: Repository — Attachments

Implement `internal/repository/attachment_repo.go`.

**Methods:**

- `InsertAttachment(ctx, att model.DBAttachment) (int64, error)`
- `ListAttachments(ctx, messageID int64) ([]oas.AttachmentMeta, error)` — without data blob.
- `GetAttachment(ctx, id int64) (model.DBAttachment, error)` — with data blob.
- `DeleteAttachment(ctx, id int64, messageID int64) error` — verify attachment belongs to messageID; return 404 otherwise.


## T09: Repository — Identities

Implement `internal/repository/identity_repo.go`.

**Methods:**

- `ListIdentities(ctx) ([]oas.Identity, error)` — ORDER BY position ASC, id ASC.
- `GetIdentity(ctx, id int64) (oas.Identity, error)`
- `GetDefaultIdentity(ctx) (oas.Identity, error)` — return error if none.
- `GetAllIdentityAddresses(ctx) ([]string, error)` — used for Reply-All exclusion.
- `CreateIdentity(ctx, identity oas.Identity) (oas.Identity, error)` — validate address (bare addr-spec, casefold before storage). If is_default=true, clear all others in same transaction. Position default: `COALESCE(MAX(position), -1) + 1`.
- `UpdateIdentity(ctx, id int64, identity oas.Identity) (oas.Identity, error)` — full replace (PUT semantics). If is_default=true, clear others. If is_default absent/false, preserve current default status. If address changed: `UPDATE messages SET from_addr = ? WHERE identity_id = ? AND folder_id = 3`.
- `DeleteIdentity(ctx, id int64) error` — reject if only one exists. If deleting default, promote the identity with lowest position (then id). After delete (FK cascade sets identity_id=NULL): `UPDATE messages SET from_addr = '' WHERE identity_id IS NULL AND folder_id = 3 AND from_addr = ?`.
- `ReorderIdentities(ctx, ids []int64) (int, error)` — same validation as folder reorder.


## T10: Repository — Contacts

Implement `internal/repository/contact_repo.go`.

**Methods:**

- `ListContacts(ctx, q *string, limit, offset int) ([]oas.Contact, int, error)` — with optional substring filter on name+address. ORDER BY `CASE WHEN name = '' THEN 1 ELSE 0 END, LOWER(name), LOWER(address)`. Count query uses same filter.
- `GetContact(ctx, id int64) (oas.Contact, error)`
- `CreateContact(ctx, contact oas.Contact) (oas.Contact, error)` — validate address (bare addr-spec), casefold address.
- `UpdateContact(ctx, id int64, contact oas.Contact) (oas.Contact, error)` — explicit `updated_at = strftime(...)` in UPDATE.
- `DeleteContact(ctx, id int64) error`
- `UpsertContact(ctx, address, name string) error` — single atomic `INSERT INTO contacts ... ON CONFLICT(address) DO UPDATE SET name = excluded.name, updated_at = ... WHERE contacts.name = ''`. Apply Unicode simple casefolding to address before calling.


## T11: Repository — Filters & Spam Filter Settings

Implement `internal/repository/filter_repo.go` and `internal/repository/spam_repo.go`.

**Filter methods:**

- `ListFilters(ctx) ([]oas.Filter, error)` — ORDER BY position ASC, id ASC.
- `GetFilter(ctx, id int64) (oas.Filter, error)`
- `CreateFilter(ctx, f oas.Filter) (oas.Filter, error)` — validate: at least one of match_from/match_to/match_subject must be non-empty (TRIM check). Validate action value. If action=move, validate folder_id is Inbox(1), Trash(4), Junk(7), or ≥100. Position default: `COALESCE(MAX(position), -1) + 1`.
- `UpdateFilter(ctx, id int64, f oas.Filter) (oas.Filter, error)` — full replace (PUT).
- `DeleteFilter(ctx, id int64) error`
- `ReorderFilters(ctx, ids []int64) (int, error)` — same validation as folder reorder.
- `ListFiltersOrdered(ctx) ([]oas.Filter, error)` — used by LDA; same as ListFilters.

**Spam filter methods:**

- `GetSpamFilterSettings(ctx) (oas.SpamFilterSettings, error)`
- `UpdateSpamFilterSettings(ctx, s oas.SpamFilterSettings) (oas.SpamFilterSettings, error)` — full replace (PUT). Validate score_header length ≤ 200.


## T12: Repository — Drafts & Scheduled Messages

Implement draft-specific queries in `internal/repository/draft_repo.go` (may reuse message_repo internals).

**Methods:**

- `GetDraft(ctx, id int64) (model.DBMessage, error)` — return 404 if not found or folder_id ≠ 3.
- `CreateDraft(ctx, msg model.DBMessage) (int64, error)` — INSERT into messages with folder_id=3, raw=NULL. Resolve identity_id to from_addr (if identity_id is nil and no identities exist, store from_addr=''; if identity_id is nil and a default identity exists, use its address). Return 400 if supplied identity_id does not exist.
- `UpdateDraft(ctx, id int64, msg model.DBMessage) error` — verify folder_id=3 (else 404). Update all draft fields. Resolve identity_id to from_addr same as CreateDraft.
- `DeleteDraft(ctx, id int64) error` — verify folder_id=3 (else 404). DELETE (attachments cascade).
- `GetScheduledMessages(ctx) ([]model.DBMessage, error)` — `SELECT ... FROM messages WHERE folder_id = 5 AND send_at <= CURRENT_TIMESTAMP ORDER BY send_at ASC`.
- `ConditionalUpdateScheduled(ctx, id int64, updates map[string]any) (bool, error)` — UPDATE with `AND send_at IS NOT NULL AND folder_id = 5`; return whether 1 row was affected.
- `CancelScheduled(ctx, id int64) (model.DBMessage, error)` — single UPDATE: `SET send_at = NULL, send_failure_count = 0, send_error = NULL, folder_id = 3 WHERE id = ? AND folder_id = 5`. Return 404 if 0 rows affected.


## T13: HTML Sanitization (`internal/sanitize`)

Implement `internal/sanitize/sanitize.go` using `github.com/microcosm-cc/bluemonday`.

**Policy construction** (`NewEmailPolicy() *bluemonday.Policy`):

Build a custom policy (not `UGCPolicy`) that permits exactly:

- **Elements:** `a`, `b`, `blockquote`, `br`, `code`, `del`, `div`, `em`, `h1`–`h6`, `hr`, `i`, `img`, `li`, `ol`, `p`, `pre`, `s`, `span`, `strong`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`
- **Attributes** (scoped per element):
  - `href` on `a`: must match `http://`, `https://`, or `mailto:` (use bluemonday's `AllowStandardURLs()` equivalent or a custom regexp).
  - `src` on `img`: must match `http://`, `https://`, or `data:image/` (with base64 pattern).
  - `alt` on `img`
  - `colspan`, `rowspan` on `td`, `th`: numeric values only.
  - `align` on `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `p`, `h1`–`h6`, `div`: one of `left`, `right`, `center`, `justify`.
  - `style` on all allowed elements: restricted CSS (see below).
- **CSS allowlist** (exact property names, no prefix matching):
  `color`, `background-color`, `font-family`, `font-size`, `font-style`, `font-variant`, `font-weight`, `letter-spacing`, `line-height`, `text-align`, `text-decoration`, `text-indent`, `vertical-align`, `white-space`, `word-spacing`, `border`, `border-color`, `border-style`, `border-width`, `border-collapse`, `border-spacing`, `padding`, `margin`, `width`, `max-width`, `height`
- **Forbidden CSS values** (checked before property allowlist): reject any declaration whose value contains `url(`, `expression(`, `-moz-binding`, or `/*`.
- **Link decoration:** add `target="_blank"` and `rel="noopener noreferrer"` to all `<a href>` via `AddTargetBlankToFullyQualifiedLinks`.

**Functions to export:**

- `SanitizeHTML(html string) string` — apply the policy and return sanitized HTML.
- `ResolveCID(html string, cidMap map[string][]byte, cidContentTypes map[string]string) string` — resolve `cid:` src attributes to data URIs before sanitization (see IMPLEMENTATION.md → cid: Resolution Algorithm). Max 64 inline images; max 10 MiB total; max 1 MiB per image. Process in document order. Run bluemonday after replacement.
- `HasExternalImages(html string) bool` — scan sanitized HTML for `<img src="http://...">` or `<img src="https://...">` (case-insensitive prefix check). `data:` URIs do not count. Uses `golang.org/x/net/html` tokenizer.

**Unit tests** (`internal/sanitize/sanitize_test.go`):
- Verify `<script>` is stripped.
- Verify `<img src="data:image/png;base64,…">` is preserved.
- Verify `<img src="javascript:…">` src is stripped.
- Verify `colspan` is allowed on `td` but stripped on `p`.
- Verify `style="background: url(…)"` is stripped.
- Verify links get `target="_blank" rel="noopener noreferrer"`.


## T14: LDA — Message Parsing (`internal/lda`)

Implement `internal/lda/parse.go` for parsing a single RFC 5322 message.

**Function:** `ParseMessage(raw []byte) (*model.ParsedMessage, error)`

`model.ParsedMessage` is the internal type defined in T03 (no ogen equivalent). It contains all extracted fields needed for storage: from_addr, to_addr, cc_addr, bcc_addr, reply_to_addr, subject, date (time.Time, nullable), message_id (nullable), in_reply_to (nullable string, first ID only, angle brackets stripped), references ([]string, angle brackets stripped), body_text, body_html, attachments ([]model.DBAttachment without id/message_id), has_external_images (bool).

**Parsing steps:**

1. Call `net/mail.ReadMessage(bytes.NewReader(raw))`. Hard failure (exit 1) if this returns an error.
2. Decode RFC 2047 encoded words in all header values using `mime.WordDecoder`.
3. Parse `Date` header; if absent or unparseable, return nil date (LDA falls back to current time; import uses format-specific fallback).
4. Parse `Message-ID`: strip angle brackets from the first token if present.
5. Parse `In-Reply-To`: take the first Message-ID, strip angle brackets.
6. Parse `References`: split on whitespace, strip angle brackets from each token. Join with `\n` for storage. Apply 16 KiB truncation (drop oldest IDs from front if joined value exceeds 16 KiB).
7. Decode `From`, `To`, `Cc`, `Bcc`, `Reply-To` headers using `net/mail.ParseAddressList`. Store as the raw header string (not re-formatted), or as `""` if absent/unparseable. (The stored form is the raw header value after RFC 2047 decoding.)
8. **MIME traversal:** depth-first search of the MIME tree. Skip `message/rfc822` sub-parts entirely. Within `multipart/alternative`, prefer `text/html` over `text/plain`. Record first `text/plain` and first `text/html` found. All other non-inline parts become attachments. Inline parts referenced by `cid:` in the HTML are **not** stored as attachments.
9. **Charset decoding:** for each text part, use `golang.org/x/net/html/charset.NewReader` to decode to UTF-8. Unknown charsets: pass bytes through and replace invalid UTF-8 with U+FFFD.
10. **RFC 2231 parameter decoding** for `Content-Disposition` and `Content-Type` parameter values (filename extraction).
11. Build `cidMap` (Content-ID → bytes) and `cidContentTypes`.
12. Call `sanitize.ResolveCID(rawHTML, cidMap, cidContentTypes)` then `sanitize.SanitizeHTML(...)` to produce `body_html`.
13. If no `text/plain` part: derive `body_text` from sanitized `body_html` via `html2text.FromString(html, html2text.Options{PrettyTables: false, OmitLinks: false})`.
14. Compute `has_external_images` via `sanitize.HasExternalImages(body_html)`.

**Unit tests** (`internal/lda/parse_test.go`):
- Parse a minimal RFC 5322 message (plain text only).
- Parse a multipart/alternative message; verify HTML preferred over plain.
- Parse a message with a `cid:` inline image; verify it becomes a data URI, not an attachment.
- Parse a message with `charset=ISO-8859-1`; verify correct UTF-8 output.
- Parse a message with no Date header; verify nil date returned.


## T15: LDA — Storage & Filter Application (`internal/lda`)

Implement `internal/lda/lda.go`: the top-level `Run(db *sql.DB, rawBytes []byte) error` function called from main.

**Steps:**

1. Call `ParseMessage(rawBytes)` (T14). On hard parse error: log to stderr, exit 1.
2. **Duplicate check:** `SELECT EXISTS(SELECT 1 FROM messages WHERE message_id = ?)`. If true (and message_id is non-null): log skip, exit 0.
3. **Message-ID generation:** if message_id is nil (no `Message-ID` header), generate `<uuid@domain>` where domain is from the first `To` address, fallback `localhost`.
4. **Spam detection** (using `SpamFilterSettings` loaded at startup or read from DB):
   - Check `X-Spam-Flag: YES` (case-insensitive, trimmed, first occurrence).
   - Check `X-Spam-Status` starts with `Yes` followed by non-alpha or end (first occurrence).
   - Check configured score header (first occurrence), parse as float64, compare ≥ threshold.
   - If any trigger fires and spam filter is enabled: initial folder = Junk (7), else Inbox (1).
5. **Filter evaluation** (load filters ordered by position ASC, id ASC):
   - For each filter: check if message matches (case-insensitive substring on from_addr/to_addr/cc_addr/subject). All non-empty criteria ANDed. `match_to` checks both to_addr AND cc_addr.
   - Apply action: `drop` → log at INFO (from + message_id), exit 0 without storing. `move` → set folder (if folder deleted, skip and use spam-determined folder). `trash` → folder = 4. `mark_read` → read = 1.
   - If `stop=1` after a match, stop evaluating.
6. **Store:** INSERT into messages with `INSERT OR IGNORE`. Check `RowsAffected`; if 0 (race with concurrent LDA), exit 0.
7. **Store attachments** in same transaction.
8. **Contacts upsert:** casefold From address, call `UpsertContact(address, displayName)`.
9. Exit 0 on success. SQLITE_BUSY: retry with exponential backoff up to 30 seconds, then exit 75. All other errors: log, exit 75.


## T16: HTML-to-Plain-Text Derivation (unit test coverage)

This is a testing-only task. Add unit tests in `internal/lda/parse_test.go` (or a new file) to verify the `html2text` derivation path:

- A message with only a `text/html` part gets a non-empty `body_text` derived via `html2text`.
- A message with both `text/plain` and `text/html` uses the native plain text (not derived).
- Verify `<br>` becomes a newline in the derived text.
- Verify FTS input sanitization (T07) passes a unit test: inputs containing `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are treated as literals.


## T17: Outgoing Mail — MIME Construction & Sendmail (`internal/service`)

Implement `internal/service/send.go`.

**`ResolveSendmailPath(configured string) (string, error)`** — use `exec.LookPath`. Return error if not found/executable. Call this at server startup; fatal if it fails.

**`BuildMIMEMessage(fields SendFields, attachments []model.DBAttachment) ([]byte, error)`** where `SendFields` contains all compose fields plus the resolved identity:

1. Set `Date` to `time.Now()` in RFC 5322 format.
2. Generate `Message-ID` as `<uuid@domain>` from sender's address domain.
3. Strip CR, LF, NUL from all user-supplied header values (to_addr, cc_addr, bcc_addr, reply_to_addr, subject, in_reply_to, each references element, identity display name).
4. Encode non-ASCII header values as RFC 2047 encoded words.
5. Construct MIME body:
   - Neither body: single empty `text/plain`.
   - Text only: single `text/plain`.
   - HTML only: single `text/html`.
   - Both: `multipart/alternative` (text first, then html).
   - If attachments: wrap in `multipart/mixed`. Encode attachment data as base64.
6. Add `Reply-To` header only if `reply_to_addr` non-empty.
7. Add `In-Reply-To` header only if `in_reply_to` non-empty (wrap in angle brackets at emission time).
8. Add `References` header only if `references` non-empty (wrap each in angle brackets, join with spaces).
9. Add `Bcc` header to the MIME message (MTA strips it from delivery copies).
10. Return the complete RFC 5322 byte slice.

**`SendMail(sendmailPath string, message []byte) (stderr string, err error)`**:
- Pipe message to `sendmail -t -oi` with a 30-second timeout.
- On non-zero exit or timeout: capture last 4 KB of stderr, return as error string.
- On success: return `("", nil)`.

**Outgoing sanitization:** Sanitize `body_html` before building the MIME message (same policy as incoming). Compute `has_external_images` after sanitization.

**`in_reply_to` strip:** strip angle brackets from `in_reply_to` before storage (same stripping applied to `references` elements). On emission, re-wrap in angle brackets.


## T18: Background Scheduler (`internal/service`)

Implement `internal/service/scheduler.go`.

**`Scheduler` struct** with a `sync.Mutex` (re-entrance guard), `*sql.DB`, `sendmailPath string`, `contactRepo`.

**`Start(ctx context.Context)`** — launch a goroutine that ticks every 60 seconds (use `time.NewTicker`). On each tick, acquire the mutex (skip tick if already locked). Process:

1. **Deferred send:** query `SELECT id FROM messages WHERE folder_id = 5 AND send_at <= CURRENT_TIMESTAMP ORDER BY send_at ASC`. For each:
   a. `ConditionalUpdateScheduled` to atomically claim the message (prevents race with HTTP cancel).
   b. Load full message, resolve identity, load attachments.
   c. `BuildMIMEMessage` → `SendMail`.
   d. On success: move message to Sent (folder_id=2), clear `send_at`, set `read=1`. Upsert To/Cc/Bcc contacts.
   e. On failure (send_failure_count < 2): `UPDATE messages SET send_failure_count = send_failure_count + 1, send_error = ? WHERE id = ? AND folder_id = 5 AND send_failure_count < 2`.
   f. On failure (send_failure_count ≥ 2): `UPDATE messages SET folder_id = 3, send_at = NULL, send_failure_count = send_failure_count + 1, send_error = ? WHERE id = ? AND folder_id = 5 AND send_failure_count >= 2`.

2. **Snooze expiry:** query `SELECT id, snooze_folder FROM messages WHERE folder_id = 6 AND snoozed_until <= CURRENT_TIMESTAMP ORDER BY snoozed_until ASC`. For each:
   ```sql
   UPDATE messages SET folder_id = COALESCE(snooze_folder, 1), snoozed_until = NULL, snooze_folder = NULL, read = 0 WHERE id = ? AND folder_id = 6
   ```

Each UPDATE holds the write lock only for its own duration. Stop cleanly when `ctx` is cancelled.


## T19: Batch Import (`internal/lda` or `internal/import`)

Implement `internal/lda/import.go` (or a separate `internal/mboximport` package) for `mymail -import`.

**Folder resolution:**
- If value matches built-in slug (`inbox`, `sent`, `drafts`, `trash`, `junk`) case-sensitively: use built-in id.
- If value is `scheduled` or `snoozed`: exit 1 with error.
- Otherwise: `LOWER(name) = LOWER(?)` search in folders; if not found, create user folder (standard slug algorithm; same folder reused across multiple mapping triplets with the same name).

**Maildir import (`importMaildir(dir string, folderID int64, db *sql.DB) (imported, skipped int, err error)`):**
- Use `maildir.Dir(dir)`.
- Sort keys lexicographically (ascending) via `Keys()`.
- Skip `tmp/` files.
- `new/` files: `read=0`, `flagged=0`. `cur/` files: parse `:2,` flags; `S` → `read=1`, `F` → `flagged=1`.
- For each key: parse via `ParseMessage`. Skip if `message_id` already in DB. Store in batches of 500. Upsert From contact.
- `date` field: use `Date` header; fallback to Maildir file mtime. If no fallback available, log warning and skip.

**mbox import (`importMbox(path string, folderID int64, db *sql.DB) (imported, skipped int, err error)`):**
- **Two-pass approach:** First pass: use `bufio.Scanner` to collect timestamp suffix from every line matching `^From \S` (these are the separator lines). Second pass: seek to start, use `mbox.NewReader` + `NextMessage()`.
- Capture file mtime via `os.Stat(path)` **before** opening (before the scanner pass).
- Match nth timestamp to nth message from `NextMessage()`. If counts differ (mboxo with embedded unescaped `From ` lines), fall back to file mtime for all messages.
- Timestamp parsing: try layouts in order: `"Mon Jan _2 15:04:05 2006"`, `"Mon Jan _2 15:04:05 MST 2006"`, `"Mon Jan _2 15:04:05 -0700 2006"`. On failure: use file mtime. If mtime also unavailable: log warning and skip message.
- Strip `From ` separator line from raw BLOB before storage.
- `date` field: use `Date` header; fallback to `From ` separator timestamp.
- Skip duplicate message_ids. Upsert From contact.

**Batching:** commit every 500 messages per transaction. On batch failure, log error, retain prior batches, continue.

**Output:** per-folder line to stdout (`inbox: 1042 imported, 3 skipped`) and final summary (`Total: 2381 imported, 17 skipped`). Exit 0 on success, 1 on any error (a single unparseable message logs warning and continues — not an exit-1 condition).

**Lock check:** acquire exclusive `flock(2)` on `<data>/mymail.lock` at startup. If held by server, exit 1 naming the holding PID.


## T20: Authentication & CSRF Middleware (`internal/auth`)

Implement `internal/auth/middleware.go` using `github.com/mikaelstaldal/go-server-common`.

**Basic Auth middleware** (`NewBasicAuth(htpasswdPath, realm string) func(http.Handler) http.Handler`):
- If `htpasswdPath` is empty, return a no-op middleware (all requests pass through).
- Otherwise, load the htpasswd file (bcrypt) and verify credentials on each request.
- Exempt `GET /api/v1/health` from auth.
- On missing/invalid credentials: respond 401 with `WWW-Authenticate: Basic realm="<realm>"`.

**CSRF middleware** (`NewCSRF(serverOrigin string) func(http.Handler) http.Handler`):
- Skip GET requests entirely.
- Extract origin from `Origin` header; if absent, derive from `Referer` header (scheme+host+port only).
- Reject `Origin: null` with 403.
- Allow requests with no `Origin` and no `Referer` (native clients).
- Compare derived origin against `serverOrigin`; reject with 403 if mismatch.

**Security headers middleware** (`SecurityHeaders(next http.Handler) http.Handler`):
All responses (API and static) must include:
```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'
Strict-Transport-Security: max-age=31536000
```

## Fix OpenAPI specification

Update @openapi.yaml and add default error response to all endpoints, to enable ogen's convenient errors https://ogen.dev/docs/concepts/convenient_errors


## T21: REST API — Folder & Folder-Message Endpoints

Implement handler methods in `internal/handler/handler.go` for:

- `GET /api/v1/folders` → `ListFolders` — call `FolderRepository.ListFolders`. Return `{total, items}`.
- `POST /api/v1/folders` → `CreateFolder` — validate name (trim, max 200 chars, non-empty after trim). Validate position if supplied. Call repo. Return 201.
- `PATCH /api/v1/folders/{id}` → `UpdateFolder` — trim name, validate. Return 409 on name collision. Return 404 for unknown id.
- `DELETE /api/v1/folders/{id}` → `DeleteFolder` — reject id < 100 with 400 ("cannot delete built-in folder"). Move messages to Trash before delete.
- `PATCH /api/v1/folders/reorder` → `ReorderFolders` — validate ids (see IMPLEMENTATION.md reorder semantics: exact set, no duplicates, no unknowns, no missing). Return `{updated: N}`.
- `DELETE /api/v1/folders/{folder_id}/messages` → `DeleteFolderMessages` — Trash/Junk: permanent delete. Others: move to Trash. Return 204.
- `POST /api/v1/folders/{folder_id}/mark-all-read` → `MarkAllRead` — return `{updated: N}`.

All handlers validate input length limits from IMPLEMENTATION.md. Unknown folder_id returns 404.


## T22: REST API — Message Endpoints

Implement handler methods for:

- `GET /api/v1/folders/{folder_id}/messages` → paginated list. Validate folder exists. Return `{total, items}`.
- `GET /api/v1/messages/search` → `SearchMessages` — validate `q` length ≤ 500. Apply FTS sanitization. Return `{total, items}` with snippets.
- `GET /api/v1/messages/{id}` → `GetMessage` — return full `MessageDetail`. Serialize `references` as JSON array with angle brackets re-added to each element. `send_failed = send_failure_count > 0`.
- `GET /api/v1/messages/{id}/raw` → `GetRawMessage` — if raw is NULL (draft): return `200 application/json {}`. Otherwise: `Content-Type: message/rfc822`, `Content-Disposition: attachment; filename=<id>.eml`.
- `GET /api/v1/messages/{id}/thread` → `GetThread` — return `{messages: [...], truncated: bool}` ordered by date ASC.
- `PATCH /api/v1/messages/{id}` → `UpdateMessage` — update folder_id, read, and/or flagged. Return updated message.
- `PATCH /api/v1/messages` → `BulkUpdateMessages` — validate ids ≤ 1000. Return `{updated: N}`.
- `DELETE /api/v1/messages/{id}` → `DeleteMessage`.
- `DELETE /api/v1/messages` → `BulkDeleteMessages` — validate ids ≤ 1000. Return 404 if any id missing.
- `POST /api/v1/messages/move` → `MoveMessages` — validate ids ≤ 1000, validate target folder exists. Return `{updated: N}`.
- `POST /api/v1/messages/{id}/snooze` → `SnoozeMessage` — validate `until >= now + 60s`. Return updated message.
- `DELETE /api/v1/messages/{id}/snooze` → `CancelSnooze` — return updated message with new folder_id.
- `POST /api/v1/messages/{id}/mark-junk` → `MarkJunk`.
- `POST /api/v1/messages/{id}/mark-not-junk` → `MarkNotJunk`.
- `GET /api/v1/attachments/{id}` → `GetAttachment` — `Content-Type: application/octet-stream`. Sanitize filename for `Content-Disposition` (strip CR, LF, NUL, `"`; RFC 8187 encoding for non-ASCII). Return 404 if not found.


## T23: REST API — Send & Draft Endpoints

Implement handler methods for:

**Send:**
- `POST /api/v1/messages/send` → `SendMessage` — validate at least one of to/cc/bcc non-empty. Resolve identity. Determine immediate vs scheduled (send_at > now+60s). Sanitize body_html. Build MIME. SendMail or insert to Scheduled. Upsert contacts on immediate send.
- `POST /api/v1/messages/send-with-attachments` → `SendMessageWithAttachments` — parse `multipart/form-data` (max 32 MiB body). Extract JSON fields from first form part. Extract file parts (filename from Content-Disposition, content-type from part header; defaults `untitled` / `application/octet-stream`). Same pipeline as above.

**Drafts:**
- `POST /api/v1/drafts` → `CreateDraft` — validate identity_id if supplied. Resolve from_addr. Insert with folder_id=3, raw=NULL. Return 201.
- `POST /api/v1/drafts-with-attachments` → `CreateDraftWithAttachments` — multipart form-data. Same as above plus attachment storage.
- `PUT /api/v1/drafts/{id}` → `UpdateDraft` — verify folder_id=3. Update message fields only (no attachment change). Resolve identity.
- `PUT /api/v1/drafts-with-attachments/{id}` → `UpdateDraftWithAttachments` — verify folder_id=3. Replace message fields AND attachments wholesale (delete all existing attachments for this draft, insert new ones from form parts).
- `DELETE /api/v1/drafts/{id}` → `DeleteDraft` — verify folder_id=3. DELETE (cascade attachments).
- `DELETE /api/v1/drafts/{id}/attachments/{attachment_id}` → `DeleteDraftAttachment` — verify draft folder_id=3. Delete specific attachment by id and message_id.
- `POST /api/v1/drafts/{id}/send` → `SendDraft` — read draft (404 if not found or not in Drafts). Validate at least one recipient. Resolve identity. Load attachments. Determine mode. Execute send or schedule. Delete draft on success.

**Scheduled:**
- `DELETE /api/v1/scheduled/{id}` → `CancelScheduled` — single UPDATE clearing send_at, resetting failure fields, moving to Drafts. Return updated message.

**Input validation** for all send/draft endpoints:
- `to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr`: parse with `net/mail.ParseAddressList` if non-empty; each address must have non-empty `.Address`. Max length 8192 chars each.
- `subject`: max 998 chars.
- `in_reply_to`: strip angle brackets before storage.
- `references` elements: strip angle brackets; apply 16 KiB joined truncation.
- Strip CR, LF, NUL from all header values.


## T24: REST API — Filter, Spam Filter, Identity & Contact Endpoints

Implement handler methods for:

**Filters:**
- `GET /api/v1/filters` → list all, `{total, items}`.
- `POST /api/v1/filters` → validate at least one match field non-empty; validate action; validate folder_id for `move` action. Position default.
- `PUT /api/v1/filters/{id}` → full replace.
- `DELETE /api/v1/filters/{id}`.
- `PATCH /api/v1/filters/reorder` → same reorder semantics as folders.

**Spam filter:**
- `GET /api/v1/spam-filter`.
- `PUT /api/v1/spam-filter` → validate score_header ≤ 200 chars.

**Identities:**
- `GET /api/v1/identities` → `{total, items}`.
- `POST /api/v1/identities` → validate address (bare addr-spec), casefold, check uniqueness. Position default. Handle is_default.
- `PUT /api/v1/identities/{id}` → full replace. is_default handling (preserve if absent/false). Address-change triggers draft from_addr update. Casefold address.
- `DELETE /api/v1/identities/{id}` → reject if last identity. If deleting default, promote next. Identity deletion cleanup for drafts.
- `PATCH /api/v1/identities/reorder`.

**Contacts:**
- `GET /api/v1/contacts` → paginated with optional `q` substring filter. `{total, items}`.
- `POST /api/v1/contacts` → validate address (bare addr-spec), casefold. Max name 200, address 254.
- `PUT /api/v1/contacts/{id}` → full replace. Explicit updated_at in UPDATE.
- `DELETE /api/v1/contacts/{id}`.

**Health:**
- `GET /api/v1/health` → always 200 `{"status": "ok"}` (no auth).


## T25: Main Entry Point & Server Startup (`main.go`)

Rewrite `main.go` to implement all operational modes.

**Flags:**
```
-init                  Init mode
-lda                   LDA mode
-import                Import mode
-port int (8080)
-addr string (127.0.0.1)
-data string (data/)
-basic-auth-file string
-basic-auth-realm string (mymail)
-sendmail string (sendmail)
-identity-name string
-identity-address string
```

**Mode dispatch:**
- `-init`: call init logic (T05). Exit 0/1.
- `-lda`: check DB exists, open with 30s busy timeout, read stdin, call `lda.Run`. Exit per LDA conventions.
- `-import`: check DB exists, acquire flock on `<data>/mymail.lock`, open with 5s busy timeout, run import (T19), release lock. Exit 0/1.
- Default (server):
  1. Check DB file exists, exit fatally if not.
  2. Open DB with 5s busy timeout, run migrations.
  3. Resolve sendmail path via `exec.LookPath`; fatal if not found.
  4. Create `<data>/mymail.lock`, acquire exclusive flock (exit 1 if import holds it).
  5. Initialize repositories and services.
  6. Build HTTP handler (ogen router + middleware stack: security headers → CSRF → basic auth → router).
  7. All non-API paths serve `index.html` (hash-based routing — serve same `index.html` for all non-`/api/` paths).
  8. Start background scheduler goroutine.
  9. Start HTTP server on `<addr>:<port>`.
  10. On SIGTERM/SIGINT: cancel context (stops scheduler), release flock, close DB, exit 0.

**Advisory lock:** `flock(2)` via `syscall.Flock` on `<data>/mymail.lock`. Lock file contains server PID as text. Released automatically on process exit.


## T26: Web UI — Build Setup & TypeScript Configuration

Set up the frontend build pipeline in `web/static/`.

1. Create `tsconfig.json` for TypeScript compilation with `tsc` only (no bundler). Target ES2020, module ESNext, jsx react-jsx with `preact/jsx-runtime`.
2. Create an import map in `index.html` (or a standalone `importmap.json`) mapping:
   - `preact` → `./vendor/preact/preact.module.js`
   - `preact/hooks` → `./vendor/preact/hooks.module.js`
   - `preact/jsx-runtime` → `./vendor/preact/jsx-runtime.module.js`
3. Vendor Preact (download or copy the ESM builds of `preact`, `preact/hooks`, `preact/jsx-runtime`) into `web/static/vendor/preact/`.
4. Vendor Quill into `web/static/vendor/quill/` (JS + CSS).
5. Create a `Makefile` or `build.sh` that runs `tsc --noEmit` (type-check) and produces `.js` output files alongside `.ts` sources.
6. Update `web/embed.go` to embed all files under `web/static/` including vendor and compiled JS.
7. Generate the TypeScript API client from `openapi.yaml` using `openapi-typescript` and place output in `web/static/api/types.ts`.


## T27: Web UI — Core Layout, Router & API Client (`web/static/`)

Implement the application shell.

**Files:**
- `web/static/app.tsx` — root Preact component; renders layout with sidebar + content area.
- `web/static/router.ts` — hash-based router. Parses `window.location.hash` and maps to route components. Listens to `hashchange`. Routes:
  - `/#/` or `/#/inbox` → Inbox
  - `/#/folder/:slug` → FolderView
  - `/#/message/:id` → MessageDetail
  - `/#/compose[?reply=:id][?replyall=:id][?forward=:id]` → ComposeForm
  - `/#/search?q=...` → SearchView
  - `/#/settings[/:tab]` → SettingsPage
  - On first load: read `localStorage.selectedFolder`; if absent, navigate to `/#/inbox`.
- `web/static/api/client.ts` — typed API client wrapping `fetch`. Uses types from `api/types.ts`. All calls include `Content-Type: application/json`. 401 response: trigger browser Basic Auth dialog by reloading with credentials prompt.
- `web/static/layout/Sidebar.tsx` — folder list with unread counts. Gear icon → `/#/settings`. Active folder highlighted. Unread badge on Inbox tab title.
- `web/static/layout/Toolbar.tsx` — Compose button + search bar (search navigates to `/#/search?q=...`).

**Polling:** `web/static/poll.ts` — poll `GET /api/v1/folders` every 30 seconds. Suspend when `document.visibilityState === 'hidden'`; resume on `visibilitychange`. On Inbox `unread_count` increase: update sidebar badge, update `document.title` (e.g. `(3) mymail`), fire browser notification if permission granted.


## T28: Web UI — Folder View & Message List

Implement `web/static/views/FolderView.tsx`.

- Paginated message list for a given `folder_id` or slug. Calls `GET /api/v1/folders/{folder_id}/messages` with `limit` and `offset`.
- Unread messages shown in **bold**.
- Columns: From, Subject, Date (adaptive format per REQUIREMENTS.md → Date and Time Display).
- Click on a row → navigate to `/#/message/:id`. Store selected folder in `localStorage.selectedFolder`.
- **Mark all as read** button → `POST /api/v1/folders/{folder_id}/mark-all-read`.
- **Empty** button (Trash and Junk only, with confirmation prompt) → `DELETE /api/v1/folders/{folder_id}/messages`.
- Bulk action toolbar (appears when rows selected): Mark read, Mark unread, Delete, Move to folder.
- `web/static/components/MessageList.tsx` — reusable list component (shared with search results).


## T29: Web UI — Message Detail View

Implement `web/static/views/MessageDetail.tsx`.

- Calls `GET /api/v1/messages/{id}` on mount. After successful GET, issue `PATCH /api/v1/messages/{id}` with `{"read": true}` if `read` was false.
- Display: From, To, Cc, Date (full form "Apr 3, 14:32 CEST"), Subject, attachment list (download links to `GET /api/v1/attachments/{id}`).
- **HTML body:** render in `<iframe srcdoc="...">` with `sandbox` attribute (no additional tokens). External images blocked by default. If `has_external_images=true`: show "Load external images" button; on click, re-render iframe with a document containing `<meta http-equiv="Content-Security-Policy" content="default-src 'none'; img-src https:; style-src 'unsafe-inline'">`.
- **Body toggle:** if both `body_text` and `body_html` are non-empty, show HTML/Plain toggle. Store preference in `localStorage.preferredBodyView`.
- **Thread strip:** call `GET /api/v1/messages/{id}/thread`. If more than 1 message in thread, show collapsed conversation strip below body. Click entry to expand (fetch `GET /api/v1/messages/{entry_id}`). If `truncated=true`: show "thread too long" indicator.
- **Action buttons:**
  - Reply → `/#/compose?reply=:id`
  - Reply All → `/#/compose?replyall=:id`
  - Forward → `/#/compose?forward=:id`
  - Move (folder picker)
  - Delete → `DELETE /api/v1/messages/{id}`
  - Snooze (available only when folder is Inbox, Snoozed, or user-created folder ≥100) → time picker → `POST /messages/{id}/snooze`
  - Mark as junk (available unless folder is Snoozed, Scheduled, or Drafts) → `POST /messages/{id}/mark-junk`
  - Mark not junk (available only in Junk) → `POST /messages/{id}/mark-not-junk`
  - Cancel schedule (Scheduled folder only) → `DELETE /api/v1/scheduled/{id}`
- `send_failed` badge: show when `send_failed=true` AND `folder_id ≠ 4` (Trash).


## T30: Web UI — Compose Form

Implement `web/static/views/ComposeForm.tsx`.

**Quill integration:** embed Quill with toolbar: Bold, Italic, Underline, Ordered list, Bullet list, Link, Clean.

**Pre-population** (from `?reply=`, `?replyall=`, `?forward=` query params):
- Load source message via `GET /api/v1/messages/{id}`.
- Apply pre-population rules from REQUIREMENTS.md (Subject prefix stripping, In-Reply-To, References, From identity matching, To/Cc population for reply/forward).
- **Identity matching for Reply/Reply-All:** iterate identities in position ASC, id ASC order; select first whose casefolded address matches any of the original To/Cc addr-specs.
- **Reply-All exclusion:** exclude all own identity addresses (from `GET /api/v1/identities`) from To and Cc.
- **Body quoting:** insert attribution line + quoted text per REQUIREMENTS.md spec. HTML: wrap in `<blockquote style="...">`. Plain text: prefix each line with `> `.
- **Signature:** load from selected identity. Convert to HTML per REQUIREMENTS.md → Signature HTML Conversion (delimiter `-- ` → `<hr>`, escape HTML entities, `\n` → `<br>`). Insert at top of Quill content. On identity change, swap signature block.

**Draft auto-save:**
- For Forward: call `POST /api/v1/drafts` immediately on form open (to copy source attachments server-side).
- For Reply, Reply-All, new compose: defer first `POST` to the first 30-second tick.
- Navigate-away before first tick and no draft_id: call `POST /api/v1/drafts` immediately, show warning if it fails.
- Subsequent saves: `PUT /api/v1/drafts/{id}`.
- On error: show transient toast; retry on next tick.

**Send button:**
- Disabled while in-flight.
- Perform immediate draft save before sending.
- Call `POST /api/v1/drafts/{id}/send`.
- On failure: keep form open, show error inline.
- On 202 (scheduled): navigate to Scheduled folder.
- On 200 (sent): navigate to Sent folder.

**Send later toggle:** reveals `<input type="datetime-local">` for `send_at`.

**Address fields** (To, Cc, Bcc): contact autocomplete via `GET /api/v1/contacts?q=...&limit=10`. If `total > 10`, show "type more to narrow" hint.

**File attachments:** `<input type="file" multiple>` for drafts with attachments (`POST /api/v1/drafts-with-attachments`, `PUT /api/v1/drafts-with-attachments/{id}`).


## T31: Web UI — Search View

Implement `web/static/views/SearchView.tsx`.

- Search bar (pre-filled from `?q=` query param), folder selector dropdown (optional, defaults to global search), date-from and date-to native HTML date pickers.
- On search: call `GET /api/v1/messages/search?q=...` with optional `folder_id`, `date_from` (start of selected day in user's local timezone, ISO 8601), `date_to` (start of day after, exclusive).
- Results shown as paginated message list using the shared `MessageList` component. Snippet shown below subject.
- Empty snippet shown silently (no placeholder text) per spec.


## T32: Web UI — Settings Pages

Implement `web/static/views/SettingsPage.tsx` with tabbed layout. Tabs and slugs: `identities`, `folders`, `filters`, `spam`, `contacts`, `preferences`. URL: `/#/settings/:tab`. Default tab: first (identities).

**Identities tab (`web/static/views/settings/Identities.tsx`):**
- List identities (name, address, default badge). CRUD forms. Drag-to-reorder → `PATCH /api/v1/identities/reorder`. Default identity visually marked.
- Signature field: plain `<textarea>`.

**Folders tab (`web/static/views/settings/Folders.tsx`):**
- List user folders only (id ≥ 100). Create, rename, delete, drag-to-reorder.

**Filters tab (`web/static/views/settings/Filters.tsx`):**
- List filters. CRUD. `match_to` field labelled "To / Cc". Drag-to-reorder → `PATCH /api/v1/filters/reorder`. Action selector. Folder selector for `move` action.

**Spam filter tab (`web/static/views/settings/SpamFilter.tsx`):**
- Enable/disable toggle, threshold input, header name input. `GET`/`PUT /api/v1/spam-filter`.

**Contacts tab (`web/static/views/settings/Contacts.tsx`):**
- Paginated list. Add/edit/delete. Address validation client-side (must look like an email). `PUT /api/v1/contacts/{id}` for edit.

**Preferences tab (`web/static/views/settings/Preferences.tsx`):**
- Dark mode toggle (stored in `localStorage.darkMode`, applies CSS class to `<html>`).
- Message list density: Compact / Normal / Relaxed (stored in `localStorage.density`, applies CSS class).
- Default body view: HTML / Plain text (stored in `localStorage.preferredBodyView`).
- Browser notifications toggle (requests `Notification.requestPermission()` on enable; auto-disables and degrades silently if denied or revoked, stored in `localStorage.notificationsEnabled`).


## T33: Web UI — Error Handling & UX Polish

Implement error handling consistently throughout the UI.

- **Transient API errors:** `web/static/components/Toast.tsx` — bottom-right toast, auto-dismisses after 5 seconds.
- **Network failures:** retry once after 2 seconds; on second failure show persistent toast with Retry button.
- **Form validation errors (400):** show inline below submit button.
- **404 on navigation:** show "Not found" inline in detail pane (not a full-page error).
- **401:** reload page (triggers browser Basic Auth dialog).
- **Date/time display:** implement adaptive format from REQUIREMENTS.md → Date and Time Display in `web/static/util/date.ts`. Message detail shows full "Apr 3, 14:32 CEST" form. Simple timestamps have a `title` tooltip with the full form.
- **Dark mode:** CSS variables-based theming; class toggled on `<html>` element.
- **Draft recovery on page reload:** read `localStorage.composeDraft` (JSON with `id`, `savedAt`, and field values). Compare `savedAt` vs server `updated_at`; load whichever is newer. On 404 from server: use localStorage state, clear stale id.
