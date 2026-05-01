# mymail — Functional Requirements

A self-hosted personal (single-user) email client with a backend, storage, REST API, and embedded web UI.
Designed to run on a Linux server alongside a mail system such as Postfix.


## Overview

mymail stores, organizes, and presents email. It does **not** speak IMAP/POP3 or SMTP directly. Instead:

- **Incoming mail** is delivered by the local MTA (Postfix, etc.) via a local delivery agent (LDA) mode.
- **Outgoing mail** is handed off to the system `sendmail` binary.
- The application is a single self-contained binary with an embedded web UI.


## Operational Modes

### Server mode (default)

Starts an HTTP server that serves the REST API and the embedded web UI.

| Flag                | Default             | Description                                            |
|---------------------|---------------------|--------------------------------------------------------|
| `-port`             | `8080`              | HTTP listen port (1–65535)                             |
| `-addr`             | `127.0.0.1`         | Bind address                                           |
| `-data`             | `data/`             | Data directory (stores the database)                   |
| `-basic-auth-file`  | ``                  | Path to htpasswd file; if set, enables HTTP Basic Auth |
| `-basic-auth-realm` | `mymail`            | Auth realm shown to clients                            |
| `-sendmail`         | `sendmail`          | Path to the sendmail binary (resolved via `PATH` if not absolute) |

At startup the server resolves the configured sendmail binary (using `PATH` lookup when the value is not absolute) and verifies that it exists and is executable. If the lookup fails the server logs a fatal error and exits with a non-zero exit code; it does not start serving HTTP. This makes the misconfiguration visible at boot rather than deferring it to the first send (which would otherwise return 500 to the user).

> **Security note:** If `-basic-auth-file` is not set, all requests are accepted without authentication. This mode is only safe when `-addr` is bound to a loopback address (`127.0.0.1` or `::1`), which is the default.
>
> **TLS and reverse proxy note:** mymail does not terminate TLS itself. For any deployment that is not loopback-only, place mymail behind a TLS-terminating reverse proxy. HTTP Basic Auth must not be used over plain HTTP on a non-loopback interface. Rate limiting is also the responsibility of the reverse proxy layer.

Identities are managed entirely through the REST API and the web UI. There is no CLI flag for the initial identity; the first identity is created via the web UI on first use.

### LDA mode (`-lda`)

When invoked with `-lda`, the program reads a single RFC 5322 message from **stdin**, stores it in the database, applies filters, and exits. Only `-data` is used; all server flags are irrelevant.

This allows Postfix configuration like:

```
mailbox_command = /usr/local/bin/mymail -lda -data /var/lib/mymail
```

Exit codes follow standard LDA conventions:
- `0` — success
- `1` — permanent failure (message will bounce)
- `75` — temporary failure (MTA will retry; used e.g. if database is locked)

### Init mode (`-init`)

```
mymail -init -data <dir>
```

Initializes a fresh installation:
- Creates the data directory if it does not exist (mode `0700`).
- Creates the SQLite database file (mode `0600`).
- Sets `PRAGMA journal_mode=WAL`.
- Applies the initial schema (all `CREATE TABLE`, `CREATE INDEX`, `CREATE TRIGGER`, `CREATE VIRTUAL TABLE` statements).
- Sets `PRAGMA user_version` to the current schema version.
- Seeds the database with the seven built-in folders.
- Seeds the spam filter settings row.
- Exits `0` on success, `1` on any error.

The server, LDA, and import modes require the database to already exist (created by `mymail -init`). They exit with a fatal error if the database file is absent.

### Import mode (`-import`)

```
mymail -import -data <dir> <mapping>...
```

Each `<mapping>` argument is a colon-separated triplet `<folder>:<format>:<path>`:

| Part       | Values                                                      | Description                                                          |
|------------|-------------------------------------------------------------|----------------------------------------------------------------------|
| `<folder>` | `inbox`, `sent`, `drafts`, `trash`, or any user-folder name | Target folder in mymail. Created automatically if it does not exist. |
| `<format>` | `mbox`, `maildir`                                           | Source format                                                        |
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
- Duplicate detection: if a message with the same `Message-ID` already exists anywhere in the database, it is skipped. Matching is a case-sensitive byte comparison of the full Message-ID string. Messages without a `Message-ID` are always imported (null Message-IDs are never treated as duplicates of each other). Skipped duplicates are included in the per-folder count as `skipped`.
- Filters are **not** applied during import — messages go directly to the specified target folder.
- A running count is printed to stdout as each folder completes: `inbox: 1042 imported, 3 skipped`.
- On completion, a summary line is printed: `Total: 2381 imported, 17 skipped`.
- Exit code `0` on success, `1` on any error. A single unparseable message logs a warning and continues. A message is considered unparseable when `net/mail.ReadMessage()` returns an error (missing or malformed headers); missing optional fields (e.g. absent `Date`) are warnings, not failures. Exception: if a message has no `Date` header **and** no usable fallback timestamp (mtime or `From ` separator), a warning is logged and the message is skipped.
- For each successfully imported message, the `From` address is upserted into the contacts table using the same logic as LDA (update name only when the stored name is currently empty).
- **Concurrency:** Running import (`-import`) concurrently with a running server against the same data directory is not supported. Concurrent LDA (`-lda`) processes alongside a running server are safe — SQLite WAL mode and the LDA's busy timeout serialize access, and `INSERT OR IGNORE` guards against duplicate inserts from concurrent LDA processes.


## Data Model

### Folders

Each folder has a name, a URL-safe slug, and a display order position.

**Slug generation:** when a user-created folder is added, the slug is derived from the name by: (1) applying Unicode NFKD normalization, (2) lowercasing, (3) replacing any run of non-alphanumeric ASCII characters with a single hyphen, (4) trimming leading and trailing hyphens. If the resulting slug collides with an existing slug, a numeric suffix is appended (`-2`, `-3`, etc.). Slugs (built-in and user-created) are immutable: renaming a user folder via `PATCH /folders/{id}` changes the display name only — the slug is preserved so that bookmarked URLs and stored filter references keep working. Renaming to a name already in use by another folder is rejected with 409.

**Built-in folders** (protected from deletion):

| name      | slug      | Notes                                               |
|-----------|-----------|-----------------------------------------------------|
| Inbox     | inbox     |                                                     |
| Sent      | sent      |                                                     |
| Drafts    | drafts    |                                                     |
| Trash     | trash     |                                                     |
| Scheduled | scheduled | Messages awaiting deferred send                     |
| Snoozed   | snoozed   | Messages awaiting snooze expiry                     |
| Junk      | junk      | Spam messages                                       |

User-created folders are supported.

### Messages

Each message stores:
- Folder assignment
- RFC 5322 `Message-ID`, `In-Reply-To`, `References` headers
- `From`, `To`, `Cc`, `Bcc`, `Reply-To` addresses
- `Subject`, `Date`
- Plain-text body (`body_text`) and HTML body (`body_html`, sanitized)
- Original raw RFC 5322 message bytes (NULL for drafts, which have no raw bytes until sent)
- Read/unread flag
- Flagged (starred) flag
- Whether attachments are present
- Deferred send time (`send_at`) — non-NULL means the message is in the Scheduled folder awaiting send
- Snooze expiry time (`snoozed_until`) and the folder to return to after snooze
- Send error message and consecutive failure count (for scheduled messages)
- Created and last-modified timestamps

### Attachments

MIME parts with `Content-Disposition: attachment` (or non-displayable parts without a `Content-ID` reference) are stored as attachments with filename, content type, size, and raw data.

Inline image parts referenced by `cid:` URLs in the HTML body are **not** stored as attachments; they are embedded as `data:` URIs directly into `body_html` at storage time.

### Identities

Each identity has:
- Display name
- Email address (unique, must be a valid RFC 5322 addr-spec). Stored in Unicode-simple-casefolded form (the same normalization as contacts) so that `User@x.com` and `user@x.com` cannot coexist as separate identities and so that identity matching during compose is deterministic regardless of header case.
- Default flag (exactly one identity is default at all times)
- Display order position
- Plain-text signature

**Constraints:**
- At least one identity must exist at all times.
- Exactly one identity has the default flag. When a new default is set, all others are cleared. When the default is deleted, the identity with the lowest position (then lowest id) becomes the new default.

**Identity matching for Reply / Reply All:**
The compose pre-population logic (see Web UI → Compose) selects the From identity by:
1. Building the candidate set: every addr-spec that appears in the original message's `To` and `Cc` headers (parsed with `net/mail` — group syntax flattened, comments stripped, addr-spec only). Casefold each candidate using Unicode simple casefolding.
2. Iterating identities in `position ASC, id ASC` order. The first identity whose casefolded address matches any candidate is selected.
3. If no identity matches, the default identity is used.
4. If no identities exist, the compose form is unavailable (see Web UI → First-Run Behaviour).

### Contacts

Each contact has:
- Email address (lower-cased using Unicode simple casefolding, unique)
- Display name (may be empty)
- Created and updated timestamps

Contacts are upserted automatically:
- On message receipt: the `From` address is upserted. The display name from the `From` header is stored as the contact name, but only when the currently stored name is empty (a manually set name is never overwritten).
- On send: `To`, `Cc`, and `Bcc` addresses are upserted using the same Unicode simple casefolding normalization as incoming contacts. The display name from each address string is extracted and applied with the same rule: it is stored only when the contact's current name is empty.

The distinction between manually set and automatically populated names is enforced server-side. Clients cannot observe or control this distinction via the API.

### Filters

Each filter has:
- Display order position
- Human-readable name
- Match criteria: `match_from`, `match_to`, `match_subject` (all ANDed; at least one must be non-empty)
  - `match_to` matches against both the `To` and `Cc` headers
  - Matching is case-insensitive substring search
- Action: `move` (to a specific folder), `trash`, `mark_read`, or `drop`
- Stop flag: when true, evaluation halts after this filter matches; no further filters are evaluated for that message, regardless of whether prior matching filters had `stop=false`

**Actions:**
- `move` — deliver to the specified folder. Valid targets are Inbox (id=1), Trash (id=4), Junk (id=7), and any user-created folder (id ≥ 100). Sent (id=2), Drafts (id=3), Scheduled (id=5), and Snoozed (id=6) are rejected with 400 because routing inbound mail there would conflict with the dedicated semantics of those folders (e.g. Drafts is created via `/drafts` only, Scheduled/Snoozed are managed by the scheduler). If the target folder was deleted, the filter is skipped and delivery continues to Inbox. The filter remains visible in the filter list with a warning indicator and can be edited to assign a new folder or deleted.
- `trash` — deliver directly to Trash.
- `mark_read` — deliver to the folder chosen by spam detection, but mark as read.
- `drop` — discard the message entirely; nothing is stored. Dropped messages are logged at INFO level (envelope From and Message-ID) but are otherwise unrecoverable.

### Spam Filter Settings

A single global configuration:
- Enabled/disabled flag
- Score header name (default: `X-Spam-Score`)
- Score threshold (default: 5.0)

Spam detection triggers on any of:
- `X-Spam-Flag` header equals `YES` after trimming surrounding ASCII whitespace (case-insensitive). When multiple `X-Spam-Flag` headers are present, the **first** instance is evaluated.
- `X-Spam-Status` header value, after trimming leading ASCII whitespace, starts with `Yes` (case-insensitive) **and** the next character (if any) is not an ASCII letter (so `Yes,score=…` and `Yes ` match, but a value beginning with `Yesterday…` does not). When multiple `X-Spam-Status` headers are present, the **first** instance is evaluated.
- The configured score header is present and its numeric value is ≥ the threshold. When the score header appears more than once, the **first** instance is evaluated; the others are ignored.

If the configured score header is present but its value cannot be parsed as a floating-point number, it is treated as absent (the score trigger does not fire for that message).


## Incoming Mail (LDA)

When invoked as `mymail -lda`:

1. Opens the database (exits with a fatal error if absent; the database must first be created with `mymail -init`).
2. Reads the raw message from stdin.
3. Parses the RFC 5322 message:
   - Extracts all standard headers.
   - Decodes any non-UTF-8 charset declared in `Content-Type` to UTF-8 before storing `body_text`/`body_html` (see HTML Sanitization → Charset Handling).
   - Extracts plain-text and HTML body parts.
   - If no plain-text part exists but an HTML part is present, derives plain text by passing the sanitized HTML through `github.com/jaytaylor/html2text` (whitespace handling — `<br>` to newlines, block elements to paragraph breaks, list flattening — follows the library's defaults). The pinned version is recorded in `go.mod`; if the library is upgraded, all messages whose `body_text` was derived (i.e. messages without a native plain-text part) should be re-derived and the FTS5 index rebuilt to keep search results consistent.
   - Resolves `cid:` inline image references to `data:` URIs before sanitizing.
   - Sanitizes the HTML body.
   - Collects attachments.
   - Falls back to current time if no `Date` header (import mode uses format-specific metadata instead; see Batch Import); generates a `Message-ID` if absent (LDA mode generates `<uuid@domain>` where `domain` is taken from the first address in the `To` header, falling back to `localhost` if absent or unparseable).
4. Skips duplicate messages (same `Message-ID` already in database).
5. Applies spam detection and user-defined filters to determine the destination folder and read state.
6. Stores the message and attachments.
7. Upserts the sender into the contacts table.

### Filter Application

**Phase 1 — Spam detection:**
- If spam filter is enabled and the message is detected as spam, initial folder is Junk; otherwise Inbox.

**Phase 2 — User-defined filters:**
- Filters are evaluated in position order.
- A `drop` action discards the message entirely (overrides even spam detection).
- A `move` or `trash` action overrides the folder chosen by spam detection.
- A `mark_read` action sets the read flag without changing the folder.
- If `stop=1`, evaluation halts after the first match.

**Worked example — `move` to Junk vs `mark_read` interactions:**

Suppose spam detection has chosen Junk for the message, and the user has two non-stopping filters in this order:

| # | Match           | Action      | Folder | stop |
|---|-----------------|-------------|--------|------|
| 1 | from: newsletter@example.com | `mark_read` | —    | 0    |
| 2 | from: newsletter@example.com | `move`      | News   | 0    |

Phase 1 picks Junk. Phase 2 applies filter 1: read flag is set. Filter 2 then applies: folder becomes News (overriding Junk). Final result: read=1, folder=News. If the order were reversed, the outcome would be identical because actions are accumulated, not chained: `mark_read` only updates the read flag, and the latest non-`mark_read` action (move/trash) wins for the folder.

If filter 2 instead targeted Junk via `move`, the result would still be Junk + read=1 — equivalent to spam detection alone plus `mark_read`.

### LDA Error Handling

- Database locked: retry up to 30 seconds, then exit `75`.
- Parse failure: log to stderr, exit `1`.
- All other errors: log to stderr, exit `75`.


## Outgoing Mail

At least one of `to_addr`, `cc_addr`, or `bcc_addr` must be non-empty; an empty `to_addr` is permitted when `cc_addr` or `bcc_addr` is non-empty.

If `body_html` contains `data:` URIs for embedded images (e.g. from images pasted into the rich-text editor), they are preserved as-is in the outgoing HTML MIME part; no conversion to CID attachments is performed.

**Immediate vs. scheduled:** A message with a `send_at` value more than 60 seconds in the future is placed in the Scheduled folder for deferred delivery by the background scheduler; in every other case (`send_at` is null, in the past, equal to now, or within 60 seconds) the message is sent immediately. The 60-second buffer prevents a race condition between the immediate send path and the scheduler's first tick.

The send flow:

1. Constructs a MIME message from the provided fields (subject, to, cc, bcc, reply-to, in-reply-to, references, body, attachments).
   - `Date` is set to the current time at send (not compose time).
   - `Message-ID` is generated as `<uuid@domain>` using the sender's (`From`) address domain.
   - When `in_reply_to` is non-empty it is emitted as the `In-Reply-To` header. When `references` is a non-empty list its values are joined with single spaces and emitted as the `References` header. Both are required for replies sent through mymail to thread on the recipient side.
   - Body is a single `text/plain` or `text/html` part when only one body type is provided; `multipart/alternative` when both are provided; wrapped in `multipart/mixed` if attachments are present.
   - User-supplied header values (`to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr`, `subject`, `in_reply_to`, every element of `references`, and the identity display name) are sanitized to strip CR, LF, and NUL control characters before encoding.
2. Pipes the message to `sendmail -t -oi` with a 30-second timeout.
3. On failure: returns the sendmail stderr as an error. No retries.
4. On success: upserts recipients into the contacts table, stores the sent message in the Sent folder with `Bcc` header preserved in the raw blob.

> **Bcc on outgoing copies:** mymail relies on the MTA (`sendmail -t`, as implemented by Postfix and Sendmail) to strip the `Bcc` header from envelopes generated for delivery before relaying. Some MTAs may not do this; operators using a non-standard MTA must verify this behaviour. The header is intentionally retained in the raw blob stored under Sent so the user can see who they bcc'ed.


## Background Scheduler

A background process wakes every 60 seconds to:

### Deferred Send

For each message in the Scheduled folder whose `send_at` is in the past (in order):
1. Build and send the RFC 5322 message via `sendmail`.
2. On success: move to Sent, clear `send_at`.
3. On failure: increment the failure count, record the error. After 3 consecutive failures, move to Drafts.

### Snooze Expiry

For each message in the Snoozed folder whose `snoozed_until` is in the past:
1. Move to the stored snooze return folder (defaults to Inbox if none stored or folder was deleted).
2. Mark as unread.


## Batch Import

### Supported Formats

#### mbox

A single file containing multiple RFC 5322 messages. Each message begins with a `From ` separator line. Supports mboxo and mboxrd variants.

#### Maildir

Each message stored as a separate file. A Maildir root contains `new/`, `cur/`, and `tmp/` subdirectories. Files in `new/` have no flag suffix (Maildir convention: they are unread/unflagged); they are imported with `read=0` and `flagged=0`. Files in `cur/` carry an info section after the `:2,` separator: the `S` (Seen) flag maps to `read=1`, the `F` (Flagged) flag maps to `flagged=1`, and absence of either flag means `0`. Files in `tmp/` are skipped (transient delivery state).

#### MBX (not supported)

Users with MBX files should pre-convert them using `mb2md` or a similar tool.

## HTML Sanitization

Incoming HTML bodies and the HTML part of outgoing messages are sanitized with a strict email-appropriate policy.

**Allowed elements:** `a`, `b`, `blockquote`, `br`, `code`, `del`, `em`, `h1`–`h6`, `hr`, `i`, `img`, `li`, `ol`, `p`, `pre`, `s`, `strong`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`

**Allowed attributes** (per element):

| Attribute | Allowed on elements                                                            | Notes                                                          |
|-----------|--------------------------------------------------------------------------------|----------------------------------------------------------------|
| `href`    | `a`                                                                            | Must be `http://`, `https://`, or `mailto:`                    |
| `src`     | `img`                                                                          | Must be `http://`, `https://`, or `data:image/…;base64,…`      |
| `alt`     | `img`                                                                          |                                                                |
| `colspan` | `td`, `th`                                                                     | Numeric value                                                  |
| `rowspan` | `td`, `th`                                                                     | Numeric value                                                  |
| `align`   | `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `p`, `h1`–`h6`, `div`    | One of `left`, `right`, `center`, `justify`                    |
| `style`   | All allowed elements                                                           | Restricted CSS properties only (see below)                     |

Any attribute not listed above is stripped. Any value not matching the listed rule (including unknown URL schemes for `href`/`src`) causes the attribute to be stripped.

**Stripped always:** `script`, `style` (standalone), `iframe`, `object`, `embed`, `form`, `input`

**Allowed CSS properties** (all others stripped):

`color`, `background-color`, `font-family`, `font-size`, `font-style`, `font-variant`, `font-weight`, `letter-spacing`, `line-height`, `text-align`, `text-decoration`, `text-indent`, `vertical-align`, `white-space`, `word-spacing`, `border`, `border-color`, `border-style`, `border-width`, `border-collapse`, `border-spacing`, `padding`, `margin`, `width`, `max-width`, `height`

**Explicitly forbidden regardless of property name:** any value containing `url(`, `expression(`, `-moz-binding`, or a CSS comment (`/*`).

**Not allowed:** `background` (shorthand), `position`, `display`, `overflow`, `content`, `z-index`, `opacity`, and all vendor-prefixed properties.

Links inside email bodies have `target="_blank"` and `rel="noopener noreferrer"` added by the sanitizer.

### Charset Handling

Body parts can declare any charset in `Content-Type` (`charset=ISO-8859-1`, `windows-1252`, `gb2312`, …). Each body part is decoded to UTF-8 using the charset declared in its `Content-Type` header before being stored in `body_text` or `body_html`. If the declared charset is unknown to the decoder, or the part contains bytes that cannot be decoded under the declared charset, the part is decoded as UTF-8 with invalid byte sequences replaced by the Unicode replacement character (U+FFFD). The original encoded bytes remain accessible via the raw RFC 5322 blob.

### `cid:` Inline Image Resolution

Before sanitization, all `<img src="cid:...">` references are resolved to `data:` URIs:
- Per-message limits: maximum 64 inline images; maximum 10 MiB total decoded bytes.
- Per-image limit: images larger than 1 MiB have their `src` removed (browser renders broken image placeholder).
- Images resolved in document order; once the total size limit is reached, remaining `cid:` references have their `src` removed.

This step runs at storage time so `body_html` is stored with `data:` URIs already embedded.


## Security

### HTTP Security Headers

All HTTP responses include:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'
Strict-Transport-Security: max-age=31536000
```

> **Note on HSTS:** Browsers ignore `Strict-Transport-Security` when received over plain HTTP, so mymail emitting it directly has no effect at the mymail layer. When deployed behind a TLS-terminating reverse proxy, the HSTS header should be set by the proxy. mymail emits it regardless so that reverse proxies that forward upstream headers automatically will include it without additional configuration.

External images in email bodies are blocked by the CSP (no `https:` in `img-src`) to prevent tracking pixels. A per-message opt-in for external images is provided via `has_external_images` in the message detail response.

### Authentication

Optional HTTP Basic Auth over all endpoints (API + static UI). Passwords stored as bcrypt hashes in an htpasswd file. If not configured, all requests are accepted without authentication (loopback-only deployments).

When authentication is required and credentials are missing or invalid, all endpoints (except `GET /api/v1/health`) respond with `401 Unauthorized` and `WWW-Authenticate: Basic realm="<realm>"` where `<realm>` is the `-basic-auth-realm` flag value.

### CSRF Protection

All state-changing HTTP methods (POST, PUT, PATCH, DELETE) are protected via Origin/Referer validation:
- Requests whose `Origin` (or derived Referer origin) does not match the server's own origin are rejected.
- `Origin: null` is explicitly rejected.
- Requests without either header (typical of native clients) bypass the check.
- GET requests are fully exempt.

### Attachment Download Security

The response always uses `Content-Type: application/octet-stream`. The filename in `Content-Disposition` is sanitized (CR, LF, NUL, and double-quote characters are stripped). Non-ASCII filenames use RFC 8187 encoding.


## Web UI

### URL Routing

Hash-based routing. The server serves the same `index.html` for all non-API paths.

| Hash pattern                    | View shown                                                          |
|---------------------------------|---------------------------------------------------------------------|
| `/#/` or `/#/inbox`             | Inbox folder view                                                   |
| `/#/folder/:slug`               | Named folder message list                                           |
| `/#/message/:id`                | Message detail                                                      |
| `/#/compose`                    | New compose form (blank)                                            |
| `/#/compose?reply=:id`          | Compose pre-populated for reply                                     |
| `/#/compose?replyall=:id`       | Compose pre-populated for reply-all                                 |
| `/#/compose?forward=:id`        | Compose pre-populated for forward                                   |
| `/#/search?q=...`               | Search results (optional `&folder_id=N` for folder-scoped search)   |
| `/#/settings`                   | Settings page (defaults to first tab)                               |
| `/#/settings/:tab`              | Settings page at a specific tab                                     |

On first load the UI reads `localStorage` for the last selected folder and navigates there; if absent it navigates to `/#/inbox`.

### Layout

```
+-------------------+-----------------------------------+
|  Folder list      |  Message list (subject, from,    |
|  (sidebar)        |  date)                           |
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

### Views

1. **Folder view** — Paginated message list. Unread messages shown in bold. **Mark all as read** button marks all messages in the folder as read.

2. **Message detail** — Full headers, sanitized HTML body in a sandboxed iframe (or plain-text fallback), attachment download links. Reply/Reply All/Forward/Move/Delete/Snooze/Mark as junk buttons. The Snooze button is available only when the message is in Inbox, Snoozed, or a user-created folder. It is not available for messages in Drafts, Sent, Trash, Junk, or Scheduled — each of those folders has its own dedicated lifecycle management that would conflict with snooze behaviour. Opening an unread message causes the UI to issue an explicit `PATCH /messages/{id}` request (with `{"read": true}`) after a successful GET to mark it as read; `GET /messages/{id}` itself does not alter read state. When the message has both body types, a toggle switches between HTML and plain text; the preference is stored. Thread display: if the message is part of a thread, a collapsed conversation strip is shown below the body; clicking an entry expands it.

3. **Compose / Reply / Reply All / Forward** — Form with From selector, To/Cc/Bcc/Reply-To fields (To/Cc/Bcc offer address autocomplete), Subject, rich-text body editor (Quill), file upload for attachments. A **Send later** toggle reveals a date/time picker. Auto-saves to Drafts every 30 seconds. Navigate-away triggers an immediate draft save.

   **Sending a draft:** the **Send** button calls `POST /drafts/{id}/send`, which reads all draft fields and attachments from the server, validates, sends or schedules, then deletes the draft. Attachments are never re-uploaded by the client at send time.

   Pre-population rules:

   | Field           | Reply                                                        | Reply All                                                          | Forward                                         |
   |-----------------|--------------------------------------------------------------|--------------------------------------------------------------------|-------------------------------------------------|
   | **From**        | Identity matching original To/Cc; falls back to default      | Same as Reply                                                      | Default identity                                |
   | **To**          | Original `Reply-To` if present; otherwise original `From`    | Same as Reply, minus own address                                   | Empty                                           |
   | **Cc**          | Empty                                                        | Original To + Cc minus own address                                 | Empty                                           |
   | **Subject**     | `Re: <original>` (no double Re:)                             | `Re: <original>` (no double Re:)                                   | `Fwd: <original>` (no double Fwd:)              |
   | **In-Reply-To** | Original `Message-ID`                                        | Original `Message-ID`                                              | Empty                                           |
   | **References**  | Original references + original `Message-ID`                  | Original references + original `Message-ID`                        | Empty                                           |
   | **Attachments** | Empty                                                        | Empty                                                              | Copies of all original attachments (server-side) |

   The **Reply-To** compose field is not pre-populated for Reply, Reply-All, or Forward; it starts empty and is editable by the user.

   **Subject prefix stripping** (used both for "no double Re:" / "no double Fwd:" in compose and for subject-based thread fallback):

   The recognised prefixes are `Re`, `Fwd`, `Fw`, `AW`, `WG`, `RES`, `ENC`, `VS`, and `SV`. A prefix is matched by the regular expression `^[ \t]*(?i:re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]+` — case-insensitive, optional leading horizontal whitespace, mandatory ASCII colon (no space allowed between the keyword and the colon), and at least one trailing space or tab character. Stripping is applied **repeatedly** to the start of the subject until no further prefix matches. After stripping, the appropriate single prefix (`Re: ` for reply, `Fwd: ` for forward) is prepended. Subjects beginning with non-matching variants such as `RE ` (no colon), `Re :` (space before colon), or `re-:` are not treated as prefixes and are left untouched, except that the new prefix is still added.

   **Body quoting** (Reply / Reply All / Forward):

   The original message body is quoted into the new compose buffer below the cursor position. The exact format is fixed (English-only; localization is out of scope for v1):

   - **Attribution line:** the new body opens with the user's blank composition area (with the identity signature, see below), then a single empty line, then the attribution line:

     `On <RFC 1123 date in the recipient's locale-independent UTC representation>, <From display name if non-empty, otherwise the addr-spec> wrote:`

     The date is formatted using the RFC 1123 layout (`Mon, 02 Jan 2006 15:04:05 MST`) using the original message's `Date` header value. If `Date` is absent, the stored `date` field (which is set from the LDA fallback) is used.
   - **Plain-text quote:** every line of the original `body_text` is prefixed with `> ` (greater-than followed by a single space). Lines that already begin with `>` get an additional `>` (standard convention — quote depth grows with each forward/reply).
   - **HTML quote:** the original `body_html` is wrapped in `<blockquote style="margin:0 0 0 0.8ex; border-left:1px solid #ccc; padding-left:1ex;">…</blockquote>`. The inline `style` is chosen because the values use only properties on the CSS allowlist, so the sanitiser preserves them on subsequent renders. The blockquote nests naturally on subsequent reply rounds.
   - **Signature placement (Reply / Reply-All):** the identity signature (with the standard `\n-- \n` delimiter) is placed at the **top** of the new body, above the attribution line and quoted material — i.e. top-posting. This matches the dominant convention in modern web and desktop mail clients and is what users typing into the compose area expect.
   - **Signature placement (Forward):** the signature appears once at the top in the same position; the forwarded content follows the attribution line.
   - **Forward wrapper:** the forward attribution line is replaced by a four-line block:

     ```
     ---------- Forwarded message ----------
     From: <original From>
     Date: <RFC 1123 date as above>
     Subject: <original Subject>
     To: <original To>
     ```

     followed by a blank line, then the original body (no `> ` prefix and no `<blockquote>` for forwards — the forwarded content is presented as the new message body, with the wrapper acting as the boundary).

   Signatures are pre-populated from the selected identity, with `\n-- \n` delimiter. Changing the From identity swaps the signature block.

   **Signature HTML conversion:** The signature is stored as plain text but must be inserted into Quill's HTML content model. Convert it as follows: the standard email signature delimiter line (`-- ` — two hyphens followed by a space) is rendered as `<hr>`; all other lines have `&`, `<`, and `>` escaped to `&amp;`, `&lt;`, and `&gt;` respectively, and line breaks become `<br>`.

   **Send button behavior:** Disabled while in-flight. Performs an immediate draft save before sending. On send failure, keeps the compose form open and shows the error inline.

   **Auto-save failure:** If an auto-save request fails, a transient error toast is shown. The save is retried on the next 30-second tick. If the navigate-away save fails, a brief warning is shown but navigation is not blocked.

4. **Scheduled folder message detail** — Shows scheduled send time; **Cancel schedule** button moves message to Drafts.

5. **Search** — Full-text search results as a message list. A folder selector (dropdown) allows limiting results to a single folder; when no folder is selected the search is global (all folders). The selected folder is passed as the `folder_id` query parameter to `GET /messages/search`. The search bar and folder selector are shown together in the search view.

6. **Filter management** — CRUD UI with drag-to-reorder. The `match_to` field is labelled "To / Cc".

7. **Folder management** — Create/rename/delete/reorder user folders.

8. **Identity management** — CRUD UI with drag-to-reorder. Default identity marked visually. Signature field is a plain-text textarea.

9. **Spam filter settings** — Enable/disable toggle, score threshold field, score header name field.

10. **Contact management** — Paginated list with add/edit/delete.

11. **Preferences** — Client-side display preferences: dark mode toggle, message list density (Compact/Normal/Relaxed), default body view (HTML/Plain text), browser notifications toggle.

### First-Run Behaviour

A fresh install has no identities. The constraint "at least one identity must exist at all times" is enforced once the first identity is created; until then the UI and API behave as follows:

- On any UI navigation (any hash route), if `GET /api/v1/identities` returns `total: 0` the UI immediately replaces the current route with `/#/settings/identities` and shows an inline banner explaining that an identity must be created before mail can be composed or sent. The banner remains until at least one identity exists. Other settings tabs and read-only views (folder lists, message detail, search) remain navigable but the **Compose** button in the sidebar is disabled and shows a tooltip referring the user to the identities tab.
- The **Reply / Reply All / Forward** buttons in message detail are likewise disabled while no identity exists.
- `POST /api/v1/messages/send`, `POST /api/v1/messages/send-with-attachments`, and `POST /api/v1/drafts/{id}/send` return `400` with `{"error": "no identity configured; create one in Settings → Identities first"}` when called with no identities present and no `identity_id` supplied (or with an `identity_id` that does not resolve).
- `POST /api/v1/drafts` and `POST /api/v1/drafts-with-attachments` are permitted with no identities so that a partially composed message is not lost when the user navigates away — but the draft cannot be sent until an identity exists.

The first identity created is automatically marked as default; the "exactly one default" invariant takes effect from that point on.

### Settings Navigation

A gear icon in the sidebar footer opens `/#/settings`. The page uses a tabbed layout:

| Tab slug       | Content                |
|----------------|------------------------|
| `identities`   | Identity management    |
| `folders`      | Folder management      |
| `filters`      | Filter management      |
| `spam`         | Spam filter settings   |
| `contacts`     | Contact management     |
| `preferences`  | Preferences panel      |

### Date and Time Display

All timestamps displayed in the browser's local timezone. Display format is adaptive:

| Age                    | Format                       | Example            |
|------------------------|------------------------------|--------------------|
| < 1 hour               | Relative ("X minutes ago")   | "42 minutes ago"   |
| 1 hour – 23:59         | Time only (HH:MM, 24-hour)   | "14:32"            |
| Yesterday              | "Yesterday HH:MM"            | "Yesterday 09:15"  |
| 2–6 days ago           | Weekday + time               | "Mon 14:32"        |
| 7 days – same year     | Short date + time            | "Apr 3, 14:32"     |
| Previous years         | Short date with year         | "Apr 3, 2023"      |

Message detail always shows the full "Apr 3, 14:32 CEST" form with timezone abbreviation.

### Error Handling UX

- **Transient API errors:** shown as a toast/snackbar (bottom-right), auto-dismisses after 5 seconds.
- **Form validation errors (400):** shown inline below the submit button.
- **Network failures:** retried once after 2 seconds; if the retry fails, a persistent toast with a **Retry** button is shown.
- **404 on navigation:** shows inline "Not found" in the detail pane.
- **Auth failure (401):** redirects to the browser's built-in Basic Auth dialog.

**Junk folder:** Message detail shows a **Not junk** button (moves to Inbox and marks as unread — mirroring snooze-expiry behaviour so the message appears as new on return to Inbox) and standard Move controls (allows moving to any folder directly). All other views show a **Mark as junk** button.

**Empty folder button:** Trash and Junk views show an **Empty** button (with confirmation prompt). For Trash: messages already in Trash are permanently deleted (standard two-step semantics). For Junk: all messages are permanently deleted immediately, regardless of whether they have been in Trash previously — moving spam to Trash is not useful. Drafts, Scheduled, and Snoozed do not show the Empty button; those folders have dedicated lifecycle management (draft deletion, schedule cancellation, and snooze cancellation respectively) and bulk-emptying them would bypass that logic.

### New Message Notifications

The UI polls the REST API every 30 seconds. When the Inbox `unread_count` increases:
1. Updates the unread badge in the sidebar.
2. Updates `document.title` (e.g. `(3) mymail`).
3. If the Notifications API permission is granted, fires a browser notification.

Polling is suspended while the browser tab is hidden.

Permission is requested only when the user explicitly enables browser notifications in Preferences. If the browser denies permission, or if permission is later revoked, the notifications preference toggle is automatically switched off and the feature degrades silently (polling continues; no browser notifications are shown).

### Client-Side Storage (`localStorage`)

- Selected folder
- Compose draft state (JSON with all field values, draft `id`, and `savedAt` timestamp)
- Dark mode toggle
- Message list density preference
- Notification permission state (cached)
- Preferred body view (`"html"` or `"text"`)

**Draft recovery on page reload:** compares `savedAt` in localStorage against the server draft's `updated_at`; whichever is newer is loaded. If the timestamps are identical, the server version is loaded. If the server returns 404, the localStorage state is used and the stale id is cleared.


## Production Deployment

mymail binds plain HTTP and delegates TLS termination, rate limiting, and access control to the operator's infrastructure:

- **TLS:** Place mymail behind a TLS-terminating reverse proxy before exposing outside localhost.
- **Rate limiting:** Apply a per-IP request rate limit at the reverse proxy to prevent CPU exhaustion from repeated failed authentication attempts.
- **Bind address:** Use `-addr 127.0.0.1` when not behind a reverse proxy on the same host.


## Out of Scope

- **Multiple mailboxes / multi-user support**
- **PGP/S-MIME**
- **Push notifications** (polling every 30 seconds is sufficient for v1)
- **Offline support / PWA**
- **Mobile / responsive layout** (targets desktop browsers only)
- **Inline attachment preview** (only download is provided)
- **Cross-folder Starred view**
