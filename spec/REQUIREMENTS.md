# MyMail — Functional Requirements

A self-hosted personal (single-user) email client with a backend, storage, REST API, and embedded web UI.
Designed to run on a Linux server alongside a mail system such as Postfix.

## Overview

mymail stores, organizes, and presents email. It does **not** speak IMAP/POP3 or SMTP directly. Instead:

- **Incoming mail** is delivered by the local MTA (Postfix, etc.) via a local delivery agent (LDA) mode.
- **Outgoing mail** is handed off to the system `sendmail` binary.
- The application is a single self-contained binary with an embedded web UI.

## Operational Modes

### Init mode (`-init`)

```
mymail -init -data <dir> [-identity-name <name>] [-identity-address <address>]
```

Initializes a fresh installation:

- Creates the data directory if it does not exist (mode `0700`).
- Creates the SQLite database file (mode `0600`).
- Sets `PRAGMA journal_mode=WAL`.
- Applies the initial schema (all `CREATE TABLE`, `CREATE INDEX`, `CREATE TRIGGER`, `CREATE VIRTUAL TABLE` statements).
- Sets `PRAGMA user_version` to the current schema version.
- Seeds the database with the seven built-in folders.
- Seeds the spam filter settings row.
- `-identity-address` is mandatory, it creates an initial identity with that address and the given name (defaults to
  empty string if `-identity-name` is omitted) and marks it as default.
- Exits `0` on success, `1` on any error.

| Flag                | Default | Description                                                 |
|---------------------|---------|-------------------------------------------------------------|
| `-data`             | `data/` | Data directory (stores the database)                        |
| `-identity-address` |         | Email address for the initial identity (RFC 5322 addr-spec) |
| `-identity-name`    | ``      | Display name for the initial identity                       |

The server, LDA, and import modes require the database to already exist (created by `mymail -init`). They exit with a
fatal error if the database file is absent.

### Server mode (default)

Starts an HTTP server that serves the REST API and the embedded web UI.

| Flag                | Default     | Description                                                                                                          |
|---------------------|-------------|----------------------------------------------------------------------------------------------------------------------|
| `-port`             | `8080`      | HTTP listen port (1–65535)                                                                                           |
| `-addr`             | `127.0.0.1` | Bind address                                                                                                         |
| `-data`             | `data/`     | Data directory (stores the database)                                                                                 |
| `-basic-auth-file`  | ``          | Path to htpasswd file; if set, enables HTTP Basic Auth                                                               |
| `-basic-auth-realm` | `mymail`    | Auth realm shown to clients                                                                                          |
| `-sendmail`         | `sendmail`  | Path to the sendmail binary (resolved via `PATH` if not absolute)                                                    |
| `-lda-socket`       | ``          | UNIX socket path for LDA delivery; if set, the server listens on this socket for incoming messages from `mymail-lda` |

At startup the server resolves the configured sendmail binary (using `PATH` lookup when the value is not absolute) and
verifies that it exists and is executable. If the lookup fails the server logs a fatal error and exits with a non-zero
exit code; it does not start serving HTTP. This makes the misconfiguration visible at boot rather than deferring it to
the first send (which would otherwise return 500 to the user).

> **Security note:** If `-basic-auth-file` is not set, all requests are accepted without authentication. This mode is
> only safe when `-addr` is bound to a loopback address (`127.0.0.1` or `::1`), which is the default.
>
> **TLS and reverse proxy note:** mymail does not terminate TLS itself. For any deployment that is not loopback-only,
> place mymail behind a TLS-terminating reverse proxy. HTTP Basic Auth must not be used over plain HTTP on a non-loopback
> interface. Rate limiting is also the responsibility of the reverse proxy layer.

Identities are managed entirely through the REST API and the web UI. The initial identity is created at init time via
`-identity-address` (see Init mode).
The server assumes that exactly one identity marked as default exists at all times, operations that require a default
identity return 500 otherwise — an internal data-integrity violation that should never occur under normal operation.

### LDA mode (`-lda`)

When invoked with `-lda`, the program reads a single RFC 5322 message from **stdin**, stores it in the database, applies
filters, and exits. Only `-data` is used; all server flags are irrelevant.

This allows Postfix configuration like:

```
mailbox_command = /usr/local/bin/mymail -lda -data /var/lib/mymail
```

Exit codes follow standard LDA conventions:

- `0` — success
- `1` — permanent failure (message will bounce)
- `75` — temporary failure (MTA will retry; used e.g. if database is locked)

### Thin LDA client (`mymail-lda`)

A separate minimal binary `mymail-lda` (built from `cmd/lda/`) forwards incoming mail to a running mymail server via a
UNIX socket instead of accessing SQLite directly. This reduces per-invocation memory from ~14 MB to ~3 MB RSS —
important on memory-constrained servers where Postfix may invoke multiple concurrent LDA processes.

```
mymail-lda -lda-socket /run/mymail/lda.sock
```

Postfix configuration when using the thin client:

```
mailbox_command = /usr/local/bin/mymail-lda -lda-socket /run/mymail/lda.sock
```

The corresponding server must be started with the matching socket path:

```
mymail -lda-socket /run/mymail/lda.sock -data /var/lib/mymail ...
```

| Flag          | Default | Description                                     |
|---------------|---------|-------------------------------------------------|
| `-lda-socket` |         | Path to the mymail server LDA socket (required) |

Exit codes are the same as LDA mode:

- `0` — success (or duplicate)
- `1` — permanent failure (parse error)
- `75` — temporary failure (socket unreachable, server busy, or transient error)

If the socket is unreachable (server not running), the client exits `75` so the MTA queues and retries. The existing
`-lda` mode remains available as a fallback for deployments that do not run a persistent server process.

### Import mode (`-import`)

```
mymail -import -data <dir> <mapping>...
```

Each `<mapping>` argument is a colon-separated triplet `<folder>:<format>:<path>`:

| Part       | Values                                                              | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                             |
|------------|---------------------------------------------------------------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| `<folder>` | `inbox`, `sent`, `drafts`, `trash`, `junk`, or any user-folder name | Target folder in mymail. Created automatically if it does not exist. Lookup is by slug for built-in folders (`inbox`, `sent`, `drafts`, `trash`, `junk`) and by name (case-insensitive) for user-created folders. If the same `<folder>` value appears in multiple mapping triplets, all triplets share the same target folder. **`scheduled` and `snoozed` are rejected with an error** — they have semantic fields (`send_at`/`snoozed_until`) that won't be populated by import, making them invalid import targets. |
| `<format>` | `mbox`, `maildir`, `mbx`                                            | Source format                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `<path>`   | file or directory path                                              | Source mbox file or Maildir root directory                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |

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
- Duplicate detection: if a message with the same `Message-ID` already exists anywhere in the database, it is skipped.
  Matching is a case-sensitive byte comparison of the full Message-ID string. Messages without a `Message-ID` are always
  imported (null Message-IDs are never treated as duplicates of each other). Skipped duplicates are included in the
  per-folder count as `skipped`.
- Filters are **not** applied during import — messages go directly to the specified target folder.
- A running count is printed to stdout as each folder completes: `inbox: 1042 imported, 3 skipped`.
- On completion, a summary line is printed: `Total: 2381 imported, 17 skipped`.
- Exit code `0` on success, `1` on any error. A single unparseable message logs a warning and continues. A message is
  considered unparseable when `net/mail.ReadMessage()` returns an error (missing or malformed headers); missing optional
  fields (e.g. absent `Date`) are warnings, not failures. Exception: if a message has no `Date` header **and** no usable
  fallback timestamp (mtime or `From ` separator), a warning is logged and the message is skipped.
- For each successfully imported message, the `From` address is upserted into the contacts table using the same logic as
  LDA (update name only when the stored name is currently empty).
- **Concurrency:** Running import (`-import`) concurrently with a running server against the same data directory is not
  supported. Concurrent LDA (`-lda`) processes alongside a running server are safe — SQLite WAL mode and the LDA's busy
  timeout serialize access, and `INSERT OR IGNORE` guards against duplicate inserts from concurrent LDA processes.

## Data Model

### Folders

Each folder has a name, a URL-safe slug, and a display order position.

**Slug generation:** when a user-created folder is added, the slug is derived from the name by: (1) applying Unicode
NFKD normalization, (2) lowercasing, (3) replacing any run of non-alphanumeric ASCII characters with a single hyphen, 
(4) trimming leading and trailing hyphens. If the resulting slug collides with an existing slug, a numeric suffix is
appended (`-2`, `-3`, etc.). Slugs (built-in and user-created) are immutable: renaming a user folder via
`PATCH /folders/{id}` changes the display name only — the slug is preserved so that bookmarked URLs and stored filter
references keep working. Renaming to a name already in use by another folder is rejected with 409.

**Built-in folders** (protected from deletion):

| name      | slug      | Notes                           |
|-----------|-----------|---------------------------------|
| Inbox     | inbox     |                                 |
| Sent      | sent      |                                 |
| Drafts    | drafts    |                                 |
| Trash     | trash     |                                 |
| Scheduled | scheduled | Messages awaiting deferred send |
| Snoozed   | snoozed   | Messages awaiting snooze expiry |
| Junk      | junk      | Spam messages                   |

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

MIME parts with `Content-Disposition: attachment` (or non-displayable parts without a `Content-ID` reference) are stored
as attachments with filename, content type, size, and raw data.

Inline image parts referenced by `cid:` URLs in the HTML body are **not** stored as attachments; they are embedded as
`data:` URIs directly into `body_html` at storage time.

### Identities

Each identity has:

- Display name
- Email address (unique, must be a valid RFC 5322 addr-spec). Stored in Unicode-simple-casefolded form (the same
  normalization as contacts) so that `User@x.com` and `user@x.com` cannot coexist as separate identities and so that
  identity matching during compose is deterministic regardless of header case.
- Default flag (exactly one identity is default at all times)
- Display order position
- Plain-text signature

**Constraints:**

- At least one identity must exist at all times.
- Exactly one identity has the default flag. When a new default is set, all others are cleared. When the default is
  deleted, the identity with the lowest position (then lowest id) becomes the new default. Deleting the last identity is
  not allowed.

**Identity matching for Reply / Reply All:**
The compose pre-population logic (see Web UI → Compose) selects the From identity by:

1. Building the candidate set: every addr-spec that appears in the original message's `To` and `Cc` headers (parsed with
   `net/mail` — group syntax flattened, comments stripped, addr-spec only). Casefold each candidate using Unicode simple
   casefolding.
2. Iterating identities in `position ASC, id ASC` order. The first identity whose casefolded address matches any
   candidate is selected.
3. If no identity matches, the default identity is used.

### Contacts

Each contact has:

- Email address (lower-cased using Unicode simple casefolding, unique)
- Display name (may be empty)
- Created and updated timestamps

Contacts are upserted automatically:

- On message receipt: the `From` address is upserted. The display name from the `From` header is stored as the contact
  name, but only when the currently stored name is empty (a manually set name is never overwritten).
- On send: `To`, `Cc`, and `Bcc` addresses are upserted at **actual send time** — when `sendmail` is invoked, whether
  that is immediately or when the background scheduler fires for a deferred message. Contact upserts are never performed
  at schedule-creation time. This rule applies identically to `POST /messages/send`,
  `POST /messages/send-with-attachments`, and `POST /drafts/{id}/send`. The upsert uses the same Unicode simple
  casefolding normalization as incoming contacts; the display name from each address string is extracted and applied
  with the same rule: it is stored only when the contact's current name is empty.

The distinction between manually set and automatically populated names is enforced server-side. Clients cannot observe
or control this distinction via the API.

### Filters

Each filter has:

- Display order position
- Human-readable name
- Match criteria: `match_from`, `match_to`, `match_subject` (all ANDed; at least one must be non-empty)
    - `match_to` matches against both the `To` and `Cc` headers
    - Matching is case-insensitive substring search
- Action: `move` (to a specific folder), `trash`, `mark_read`, or `drop`
- Stop flag: when true, evaluation halts after this filter matches; no further filters are evaluated for that message,
  regardless of whether prior matching filters had `stop=false`

**Actions:**

- `move` — deliver to the specified folder. Valid targets are Inbox (id=1), Trash (id=4), Junk (id=7), and any
  user-created folder (id ≥ 100). Sent (id=2), Drafts (id=3), Scheduled (id=5), and Snoozed (id=6) are rejected with 400
  because routing inbound mail there would conflict with the dedicated semantics of those folders (e.g. Drafts is
  created via `/drafts` only, Scheduled/Snoozed are managed by the scheduler). If the target folder was deleted, the
  filter is skipped and delivery continues to Inbox. The filter remains visible in the filter list with a warning
  indicator and can be edited to assign a new folder or deleted.
- `trash` — deliver directly to Trash.
- `mark_read` — deliver to the folder chosen by spam detection, but mark as read.
- `drop` — discard the message entirely; nothing is stored. Dropped messages are logged at INFO level (envelope From and
  Message-ID) but are otherwise unrecoverable.

### Spam Filter Settings

A single global configuration:

- Enabled/disabled flag
- Score header name (default: `X-Spam-Score`)
- Score threshold (default: 5.0)

Spam detection triggers on any of:

- `X-Spam-Flag` header equals `YES` after trimming surrounding ASCII whitespace (case-insensitive). When multiple
  `X-Spam-Flag` headers are present, the **first** instance is evaluated.
- `X-Spam-Status` header value, after trimming leading ASCII whitespace, starts with `Yes` (case-insensitive) **and**
  the next character (if any) is not an ASCII letter (so `Yes,score=…` and `Yes ` match, but a value beginning with
  `Yesterday…` does not). When multiple `X-Spam-Status` headers are present, the **first** instance is evaluated.
- The configured score header is present and its numeric value is ≥ the threshold. When the score header appears more
  than once, the **first** instance is evaluated; the others are ignored.

If the configured score header is present but its value cannot be parsed as a floating-point number, it is treated as
absent (the score trigger does not fire for that message).

## Incoming Mail (LDA)

When invoked as `mymail -lda`:

1. Opens the database (exits with a fatal error if absent; the database must first be created with `mymail -init`).
2. Reads the raw message from stdin.
3. Parses the RFC 5322 message:
    - Extracts all standard headers.
    - Decodes any non-UTF-8 charset declared in `Content-Type` to UTF-8 before storing `body_text`/`body_html` (see HTML
      Sanitization → Charset Handling).
    - Extracts plain-text and HTML body parts.
    - If no plain-text part exists but an HTML part is present, derives plain text by passing the sanitized HTML through
      `github.com/jaytaylor/html2text` (whitespace handling — `<br>` to newlines, block elements to paragraph breaks,
      list flattening — follows the library's defaults). The pinned version is recorded in `go.mod`; if the library is
      upgraded, all messages whose `body_text` was derived (i.e. messages without a native plain-text part) should be
      re-derived and the FTS5 index rebuilt to keep search results consistent.
    - Resolves `cid:` inline image references to `data:` URIs before sanitizing.
    - Sanitizes the HTML body.
    - Collects attachments.
    - Falls back to current time if no `Date` header (import mode uses format-specific metadata instead; see Batch
      Import); generates a `Message-ID` if absent (LDA mode generates `<uuid@domain>` where `domain` is taken from the
      first address in the `To` header, falling back to `localhost` if absent or unparseable).
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

| # | Match                        | Action      | Folder | stop |
|---|------------------------------|-------------|--------|------|
| 1 | from: newsletter@example.com | `mark_read` | —      | 0    |
| 2 | from: newsletter@example.com | `move`      | News   | 0    |

Phase 1 picks Junk. Phase 2 applies filter 1: read flag is set. Filter 2 then applies: folder becomes News (overriding
Junk). Final result: read=1, folder=News. If the order were reversed, the outcome would be identical because actions are
accumulated, not chained: `mark_read` only updates the read flag, and the latest non-`mark_read` action (move/trash)
wins for the folder.

If filter 2 instead targeted Junk via `move`, the result would still be Junk + read=1 — equivalent to spam detection
alone plus `mark_read`.

### LDA Error Handling

- Database locked: retry up to 30 seconds, then exit `75`.
- Parse failure: log to stderr, exit `1`.
- All other errors: log to stderr, exit `75`.

## Outgoing Mail

At least one of `to_addr`, `cc_addr`, or `bcc_addr` must be non-empty; an empty `to_addr` is permitted when `cc_addr` or
`bcc_addr` is non-empty.

If `body_html` contains `data:` URIs for embedded images (e.g. from images pasted into the rich-text editor), they are
preserved as-is in the outgoing HTML MIME part; no conversion to CID attachments is performed.

**Immediate vs. scheduled:** A message with a `send_at` value more than 60 seconds in the future is placed in the
Scheduled folder for deferred delivery by the background scheduler; in every other case (`send_at` is null, in the past,
equal to now, or within 60 seconds) the message is sent immediately. The 60-second buffer prevents a race condition
between the immediate send path and the scheduler's first tick.

The send flow:

1. Constructs a MIME message from the provided fields (subject, to, cc, bcc, reply-to, in-reply-to, references, body,
   attachments).
    - `Date` is set to the current time at send (not compose time).
    - `Message-ID` is generated as `<uuid@domain>` using the sender's (`From`) address domain.
    - When `in_reply_to` is non-empty it is emitted as the `In-Reply-To` header. When `references` is a non-empty list
      its values are joined with single spaces and emitted as the `References` header. Both are required for replies
      sent through mymail to thread on the recipient side.
    - Body is a single `text/plain` or `text/html` part when only one body type is provided; `multipart/alternative`
      when both are provided; wrapped in `multipart/mixed` if attachments are present.
    - User-supplied header values (`to_addr`, `cc_addr`, `bcc_addr`, `reply_to_addr`, `subject`, `in_reply_to`, every
      element of `references`, and the identity display name) are sanitized to strip CR, LF, and NUL control characters
      before encoding.
2. Pipes the message to `sendmail -t -oi` with a 30-second timeout.
3. On failure: returns the sendmail stderr as an error. No retries.
4. On success: upserts recipients into the contacts table, stores the sent message in the Sent folder with `Bcc` header
   preserved in the raw blob.

> **Bcc on outgoing copies:** mymail relies on the MTA (`sendmail -t`, as implemented by Postfix and Sendmail) to strip
> the `Bcc` header from envelopes generated for delivery before relaying. Some MTAs may not do this; operators using a
> non-standard MTA must verify this behaviour. The header is intentionally retained in the raw blob stored under Sent so
> the user can see who they bcc'ed.

## Background Scheduler

A background process wakes every 60 seconds to:

### Deferred Send

For each message in the Scheduled folder whose `send_at` is in the past (in order):

1. Build and send the RFC 5322 message via `sendmail`.
2. On success: move to Sent, clear `send_at`.
3. On failure: increment the failure count, record the error. After 3 consecutive failures, move to Drafts and clear
   `send_at` (set to NULL) so the invariant that `send_at` is non-null only for messages in the Scheduled folder is
   maintained.

### Snooze Expiry

For each message in the Snoozed folder whose `snoozed_until` is in the past:

1. Move to the stored snooze return folder (defaults to Inbox if none stored or folder was deleted).
2. Mark as unread.

## Batch Import

### Supported Formats

#### mbox

A single file containing multiple RFC 5322 messages. Each message begins with a `From ` separator line. Supports mboxo
and mboxrd variants.

#### Maildir

Each message stored as a separate file. A Maildir root contains `new/`, `cur/`, and `tmp/` subdirectories. Files in
`new/` have no flag suffix (Maildir convention: they are unread/unflagged); they are imported with `read=0` and
`flagged=0`. Files in `cur/` carry an info section after the `:2,` separator: the `S` (Seen) flag maps to `read=1`, the
`F` (Flagged) flag maps to `flagged=1`, and absence of either flag means `0`. Files in `tmp/` are skipped (transient
delivery state).

#### MBX (UW-IMAP)

The binary mailbox format used by UW-IMAP and Pine. Files begin with a 2048-byte header whose first bytes are
`*mbx*\r\n`. Each message is preceded by a variable-length per-message header line with the format
`<IMAP-date>,<size>;<8hex-uflags><4hex-sysflags>-<8hex-uid>\r\n`, followed by exactly `<size>` bytes of RFC 822 message
content.

Flag mapping: the `\Seen` system flag (bit `0x1`) maps to `read=1`; the `\Flagged` system flag (bit `0x4`) maps to
`flagged=1`. Messages with the internal `\Expunged` flag (bit `0x8000`) are logical holes left by IMAP EXPUNGE and are
skipped during import.

The IMAP internal date from the per-message header is used as the date fallback when the message has no `Date:` header,
followed by the file mtime if the internal date cannot be parsed.

## HTML Sanitization

Incoming HTML bodies and the HTML part of outgoing messages are sanitized with a
strict email-appropriate policy.

There are **two policy objects** — an inbound one (incoming bodies, delivered via
the LDA) and an outgoing one (the HTML part of messages this instance sends:
immediate, scheduled, and via draft send). They share the same attribute rules,
URL schemes, and CSS value validation, and the outgoing one is by construction a
superset of the inbound one. **Their allowlists are currently identical**, so
everything below applies in both directions.

The separation exists as a seam, not for a present-day difference: the two
directions have genuinely different threat models, so if something ever needs to
be sendable without being renderable, there is one place to put it. It is left
empty on purpose. **MyMail must be able to render everything MyMail is willing to
send** — otherwise a message to another MyMail instance, or to yourself, arrives
stripped of styling this same instance produced. That round trip is pinned by a
test.

The allowlist is not the narrowest that could be written, because narrowness is
only worth paying for where the excluded thing carries risk. Inert elements and
CSS that cannot reference a resource are permitted; what stays out is what can
execute, fetch, or spoof. The sanitizer is also not the only gate: message bodies
are served by `GET /messages/{id}/body` with `default-src 'none'` and no
`script-src`, inside an iframe sandboxed without `allow-scripts` or
`allow-same-origin`.

**Allowed elements:** `a`, `abbr`, `b`, `blockquote`, `br`, `caption`, `cite`, `code`, `col`, `colgroup`, `dd`, `del`,
`dfn`, `div`, `dl`, `dt`, `em`, `figcaption`, `figure`, `h1`–`h6`, `hr`, `i`, `img`, `ins`, `kbd`, `li`, `mark`, `ol`,
`p`, `pre`, `q`, `s`, `samp`, `small`, `span`, `strong`, `sub`, `sup`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`,
`tr`, `tt`, `u`, `ul`, `var`

**Allowed attributes** (per element):

| Attribute | Allowed on elements                                                         | Notes                                                     |
|-----------|-----------------------------------------------------------------------------|-----------------------------------------------------------|
| `href`    | `a`                                                                         | Must be `http://`, `https://`, or `mailto:`               |
| `src`     | `img`                                                                       | Must be `http://`, `https://`, or `data:image/…;base64,…` |
| `alt`     | `img`                                                                       |                                                           |
| `colspan` | `td`, `th`                                                                  | Numeric value                                             |
| `rowspan` | `td`, `th`                                                                  | Numeric value                                             |
| `align`   | `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `p`, `h1`–`h6`, `div` | One of `left`, `right`, `center`, `justify`               |
| `style`   | All allowed elements                                                        | Restricted CSS properties only (see below)                |

Any attribute not listed above is stripped. Any value not matching the listed rule (including unknown URL schemes for
`href`/`src`) causes the attribute to be stripped.

**Stripped always:** `script`, `style` (standalone), `iframe`, `object`, `embed`, `form`, `input`

**Allowed CSS properties** (all others stripped):

`color`, `background-color`, `font-family`, `font-size`, `font-style`, `font-variant`, `font-weight`, `letter-spacing`,
`line-height`, `text-align`, `text-decoration`, `text-indent`, `vertical-align`, `white-space`, `word-spacing`,
`border`, `border-color`, `border-style`, `border-width`, `border-collapse`, `border-spacing`, `padding`, `margin`,
`width`, `max-width`, `height`

…plus the per-side longhands of those box shorthands — `margin-top`/`-right`/`-bottom`/`-left`, `padding-*`,
`border-top`/`-right`/`-bottom`/`-left` and their `-width`/`-style`/`-color` forms — and `border-radius`, `list-style`,
`list-style-type`, `list-style-position`, `min-width`, `min-height`, `max-height`.

The longhands add no expressive power (the shorthands can already address any single side), but they have no lossless
fallback: a one-sided border rewritten as `border: none; border-top: …` degrades to an *invisible* rule rather than a
plain one, so dropping them would be more than cosmetic.

**Value validation (regardless of property name):** declaration values are checked against an allowlist, not a
blocklist. A value is stripped if it contains a backslash CSS escape (e.g. `u\72l(`), a CSS comment (`/*`, `*/`), or any
functional notation other than the color functions `rgb()`/`rgba()`/`hsl()`/`hsla()`. This blocks `url()`,
`expression()`, `image-set()`, `-moz-binding`, and similar — including escape- and comment-obfuscated spellings — and
also rejects stray/unbalanced parentheses.

**Not allowed:** `background` (shorthand), `position`, `display`, `overflow`, `content`, `z-index`, `opacity`, and all
vendor-prefixed properties.

Links inside email bodies have `target="_blank"` and `rel="noopener noreferrer"` added by the sanitizer.

### Send/receive symmetry

A message this instance sends is sanitized on the way out by the outgoing policy
and, if it is delivered to a MyMail instance (including this one, when sending to
yourself), again on arrival by the inbound one. Because the two allowlists are
identical, the second pass is a no-op: **what is sent is exactly what is
received**, byte for byte.

Adding an entry to the outgoing-only lists breaks that property by definition,
and is therefore a deliberate decision to accept the resulting degradation on the
MyMail-to-MyMail path — never an incidental one. The round-trip test fails as
soon as either list becomes non-empty.

### Charset Handling

Body parts and email headers (From, To, Subject, etc.) can declare any charset in `Content-Type` or RFC 2047 encoded
words (`charset=ISO-8859-1`, `windows-1252`, `gb2312`, …). Each body part and header is decoded to UTF-8 using the
declared charset before being stored in the database. If the declared charset is unknown to the decoder, or the content
contains bytes that cannot be decoded under the declared charset, the content is decoded as UTF-8 with invalid byte
sequences replaced by the Unicode replacement character (U+FFFD). The original encoded bytes remain accessible via the
raw RFC 5322 blob.

### `cid:` Inline Image Resolution

Before sanitization, all `<img src="cid:...">` references are resolved to `data:` URIs:

- Per-message limits: maximum 64 inline images; maximum 10 MiB total decoded bytes.
- Per-image limit: images larger than 1 MiB have their `src` removed (browser renders broken image placeholder).
- Images resolved in document order; once the total size limit is reached, remaining `cid:` references have their `src`
  removed.

This step runs at storage time so `body_html` is stored with `data:` URIs already embedded.

## Security

### HTTP Security Headers

All HTTP responses include:

```
X-Content-Type-Options: nosniff
X-Frame-Options: DENY
Referrer-Policy: same-origin
Content-Security-Policy: default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' '<importmap-hash>'
Strict-Transport-Security: max-age=31536000
```

`<importmap-hash>` is a `'sha256-…'` token computed at server startup by hashing the exact text content of the
`<script type="importmap">` element in the embedded `index.html` (see `web.ImportMapCSPHash`). This permits the
importmap and same-origin `.js` files while excluding `'unsafe-inline'`, preserving XSS protection.

> **Note on HSTS:** Browsers ignore `Strict-Transport-Security` when received over plain HTTP, so mymail emitting it
> directly has no effect at the mymail layer. When deployed behind a TLS-terminating reverse proxy, the HSTS header should
> be set by the proxy. mymail emits it regardless so that reverse proxies that forward upstream headers automatically will
> include it without additional configuration.

External images in email bodies are blocked by the CSP (no `https:` in `img-src`) to prevent tracking pixels. A
per-message opt-in for external images is provided via `has_external_images` in the message detail response.

### Authentication

Optional HTTP Basic Auth over all endpoints (API + static UI). Passwords stored as bcrypt hashes in an htpasswd file. If
not configured, all requests are accepted without authentication (loopback-only deployments).

When authentication is required and credentials are missing or invalid, all endpoints respond with `401 Unauthorized`
and `WWW-Authenticate: Basic realm="<realm>"` where `<realm>` is the `-basic-auth-realm` flag value.

### CSRF Protection

All state-changing HTTP methods (POST, PUT, PATCH, DELETE) are protected via Origin/Referer validation:

- Requests whose `Origin` (or derived Referer origin) does not match the server's own origin are rejected.
- `Origin: null` is explicitly rejected.
- Requests without either header (typical of native clients) bypass the check.
- GET requests are fully exempt.

### Attachment Download Security

The response always uses `Content-Type: application/octet-stream`. The filename in `Content-Disposition` is sanitized (
CR, LF, NUL, and double-quote characters are stripped). Non-ASCII filenames use RFC 8187 encoding.

## Web UI

### URL Routing

Hash-based routing. The server serves the same `index.html` for all non-API paths.

| Hash pattern              | View shown                                                        |
|---------------------------|-------------------------------------------------------------------|
| `/#/` or `/#/inbox`       | Inbox folder view                                                 |
| `/#/folder/:slug`         | Named folder message list                                         |
| `/#/message/:id`          | Message detail                                                    |
| `/#/compose`              | New compose form (blank)                                          |
| `/#/compose?reply=:id`    | Compose pre-populated for reply                                   |
| `/#/compose?replyall=:id` | Compose pre-populated for reply-all                               |
| `/#/compose?forward=:id`  | Compose pre-populated for forward                                 |
| `/#/search?q=...`         | Search results (optional `&folder_id=N` for folder-scoped search) |
| `/#/settings`             | Settings page (defaults to first tab)                             |
| `/#/settings/:tab`        | Settings page at a specific tab                                   |

On first load the UI reads `localStorage` for the last selected folder and navigates there; if absent it navigates to
`/#/inbox`.

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

1. **Folder view** — Paginated message list. Unread messages shown in bold. **Mark all as read** button marks all
   messages in the folder as read.

2. **Message detail** — Full headers, sanitized HTML body in a sandboxed iframe (or plain-text fallback), attachment
   download links. Reply/Reply All/Forward/Move/Delete/Snooze/Mark as junk buttons. An **All headers** toggle button (
   hidden for drafts) fetches and displays the raw RFC 5322 header block via `GET /messages/{id}/headers`; clicking
   again collapses the panel. **Draft messages** show **Send**, **Edit** (opens the compose form) and **Discard**
   (permanently deletes the draft after confirmation) buttons instead of Reply/Reply-All/Forward. **Send** sends the
   draft in its stored state, without opening the compose form: after confirmation it calls `POST /drafts/{id}/send`
   and navigates to Sent, or to Scheduled when the draft carries a `send_at` more than 60 seconds ahead (in which case
   the button is labelled **Schedule** instead). It is disabled unless the draft has a valid recipient — at least one
   of To/Cc/Bcc non-empty, and none of the three malformed — which is the same condition the server enforces. The Snooze button is available only
   when the message is in Inbox, Snoozed, or a user-created folder. It is not available for messages in Drafts, Sent,
   Trash, Junk, or Scheduled — each of those folders has its own dedicated lifecycle management that would conflict with
   snooze behaviour. The snooze `until` time must be at least 1 minute ahead of the current server time; a shorter value
   is rejected. **Re-snooze / edit snooze:** if the message is already in Snoozed, the Snooze button is labelled "Edit
   snooze" and pre-fills the datetime picker with the current snooze time; submitting updates the expiry time and
   preserves the original return folder. Opening an unread message causes the UI to issue an explicit
   `PATCH /messages/{id}` request (with `{"read": true}`) after a successful GET to mark it as read;
   `GET /messages/{id}` itself does not alter read state. When the message has both body types, a toggle switches
   between HTML and plain text; the preference is stored. Thread display: if the message is part of a thread, a
   collapsed conversation strip is shown below the body; clicking an entry expands it inline. Each entry other than the
   currently displayed message also offers an **Open** button (revealed on hover or keyboard focus) that navigates to
   that message, replacing the main view with it. The thread panel is resizable by dragging the divider between the body
   and the thread strip. When the thread is truncated at the 1000-message cap (`truncated: true` in the API response),
   a "thread too long" indicator is shown in place of the missing entries.

   **Importing an event into MyCal.** When the **MyCal URL** preference resolves to a non-empty value (see Settings →
   Preferences), the message detail offers an **Import to Calendar** action for each iCalendar event it can find in the
   message. Both forms POST to `{mycal}/api/v1/import-single` from the browser — MyMail's backend is not involved, and
   the two apps are assumed to be same-origin behind one auth realm, which is what makes that request possible without
   CORS. A `201` is reported as **Imported**; any other status shows the `error` field of the response body beside the
   button. The state is per item and is reset when another message is opened.

   - **From an attachment.** Any attachment whose content type is `text/calendar` or `application/ics`, or whose file
     name ends in `.ics`. The attachment bytes are fetched from MyMail and sent as the `text/calendar` request body.
   - **From a link in the HTML body.** The `<a href>` links of `body_html` are parsed (as a document, not by regex) and
     those pointing at iCalendar data are listed in a **Calendar links** block below the attachments, each with its
     text and its URL. An `http:`/`https:` link qualifies on any of three tests, all applied to the path or the query
     and never to the fragment, all case-insensitive:

     1. the URL **path** ends in `.ics` — the static-file case;
     2. a whole **path segment** is `ics`, `ical` or `icalendar` — `…/api/Events/<id>/iCalendar`, `…/events/7/ical`,
        `…/download/ics`. Extensionless endpoints of this shape are what bulk-mail platforms generate, so rule 1 alone
        misses most real invitations. The comparison is against a whole segment, so `/medical/`, `/basics` and
        `/icalendars/` do not match;
     3. a **format parameter** — `format`, `type`, `fmt`, `output` or `calendar` — whose *value* is exactly one of
        those three words: `?format=ical`, `?type=ics`.

     A file name in the query (`?file=e.ics`) does **not** qualify: it says nothing about what the response will be.
     Link *text* is never consulted — "Add to calendar" is unbounded and differs per language.

     The two errors are not equally costly, which is why the rules are generous: a false positive is cheap and visible
     (MyCal fetches, fails to parse, answers 400, and the error appears beside the button the user pressed), while a
     false negative is invisible — no button, and nothing to indicate one was owed.

     A `webcal:` link qualifies on its scheme alone, and is rewritten to `https:` before being sent, since MyCal
     fetches over HTTP. **A `webcal:` link cannot currently reach
     this**: HTML sanitization allows `http://`, `https://` and `mailto:` hrefs only, so an inbound `webcal:` link is
     stored with its `<a>` unwrapped. Allowing that scheme would be a change to the sanitization allowlist (see § HTML
     Sanitization), not to this rule. Every other scheme is ignored, including
     `mailto:`, `javascript:` and `data:`, as is any URL carrying embedded credentials (`https://user:pass@…`), which
     would otherwise have MyCal fetch with a sender's password. Relative hrefs are skipped rather than resolved: a mail
     body has no base URL, and MyMail's own origin is not one. Links are deduplicated by the rewritten URL, since the
     same event commonly appears as both a button and a visible URL, and it is that rewritten URL — the one that would
     be fetched — that is displayed, in full: the query commonly carries the identifiers the endpoint needs, so nothing
     in the display path may truncate or rewrite it. The request body is `{"url": "…"}` as JSON: MyMail never fetches
     the link itself, and does not attempt to judge the host either — MyCal fetches and parses it server-side, under
     its own SSRF guard and size cap (see § Calendar Import in `ARCHITECTURE.md`). Only the HTML body is scanned;
     calendar URLs in a plain-text body are not detected.

3. **Compose / Reply / Reply All / Forward** — Form with From selector, To/Cc/Bcc/Reply-To fields (To/Cc/Bcc offer
   address autocomplete), Subject, rich-text body editor (Quill), file upload for attachments. For Reply / Reply All /
   Forward, a read-only quoted-text block sits below the editor (see Body quoting). A **Send later** toggle reveals a
   date/time picker. Auto-saves to Drafts every 30 seconds. Navigate-away triggers an immediate draft save.

   **Sending a draft:** the **Send** button calls `POST /drafts/{id}/send`, which reads all draft fields and attachments
   from the server, validates, sends or schedules, then deletes the draft. Attachments are never re-uploaded by the
   client at send time.

   Pre-population rules:

   | Field           | Reply                                                     | Reply All                                          | Forward                                          |
   |-----------------|-----------------------------------------------------------|----------------------------------------------------|--------------------------------------------------|
   | **From**        | Identity matching original To/Cc; falls back to default   | Same as Reply                                      | Default identity                                 |
   | **To**          | Original `Reply-To` if present; otherwise original `From` | Same as Reply, minus all own identity addresses    | Empty                                            |
   | **Cc**          | Empty                                                     | Original To + Cc, minus all own identity addresses | Empty                                            |
   | **Subject**     | `Re: <original>` (no double Re:)                          | `Re: <original>` (no double Re:)                   | `Fwd: <original>` (no double Fwd:)               |
   | **In-Reply-To** | Original `Message-ID`                                     | Original `Message-ID`                              | Empty                                            |
   | **References**  | Original references + original `Message-ID`               | Original references + original `Message-ID`        | Empty                                            |
   | **Attachments** | Empty                                                     | Empty                                              | Copies of all original attachments (server-side) |

   **"All own identity addresses"** (used in the Reply-All column above): the set of `address` values from **all**
   identity rows in the database, not just the identity selected for the From field. Plus-addressed variants of an
   identity address are also treated as own — i.e. an address `local+tag@domain` matches identity `local@domain`. For
   example, if the user has identities `alice@example.com` and `alice@work.example.com`, then `alice@example.com`,
   `alice+lists@example.com`, `alice@work.example.com`, and `alice+newsletters@work.example.com` are all excluded from
   Reply-All To/Cc regardless of which identity is selected as From.

   The **Reply-To** compose field is not pre-populated for Reply, Reply-All, or Forward; it starts empty and is editable
   by the user.

   **Subject prefix stripping** (used both for "no double Re:" / "no double Fwd:" in compose and for subject-based
   thread fallback):

   The recognised prefixes are `Re`, `Fwd`, `Fw`, `AW`, `WG`, `RES`, `ENC`, `VS`, and `SV`. A prefix is matched by the
   regular expression `^[ \t]*(?i:re|fwd|fw|aw|wg|res|enc|vs|sv):[ \t]+` — case-insensitive, optional leading horizontal
   whitespace, mandatory ASCII colon (no space allowed between the keyword and the colon), and at least one trailing
   space or tab character. Stripping is applied **repeatedly** to the start of the subject until no further prefix
   matches. After stripping, the appropriate single prefix (`Re: ` for reply, `Fwd: ` for forward) is prepended.
   Subjects beginning with non-matching variants such as `RE ` (no colon), `Re :` (space before colon), or `re-:` are
   not treated as prefixes and are left untouched, except that the new prefix is still added.

   **Body quoting** (Reply / Reply All / Forward):

   The original message body is quoted below the user's composition area. The exact format is fixed (English-only;
   localization is out of scope for v1):

    - **Attribution line:** the new body opens with the user's blank composition area (with the identity signature, see
      below), then a single empty line, then the attribution line:

      `On <RFC 1123 date in the recipient's locale-independent UTC representation>, <From display name if non-empty, otherwise the addr-spec> wrote:`

      The date is formatted using the RFC 1123 layout (`Mon, 02 Jan 2006 15:04:05 MST`) using the original message's
      `Date` header value. If `Date` is absent, the stored `date` field (which is set from the LDA fallback) is used.
    - **Plain-text quote:** every line of the original `body_text` is prefixed with `> ` (greater-than followed by a
      single space). Lines that already begin with `>` get an additional `>` (standard convention — quote depth grows
      with each forward/reply).
    - **HTML quote:** the original `body_html` is wrapped in
      `<blockquote style="margin:0 0 0 0.8ex; border-left:1px solid #ccc; padding-left:1ex;">…</blockquote>`. The inline
      `style` is chosen because the values use only properties on the CSS allowlist, so the sanitiser preserves them on
      subsequent renders. The blockquote nests naturally on subsequent reply rounds.
    - **Leading blank lines are removed** (Reply / Reply All): whatever empty space the original body starts with is
      dropped, so the first quoted line follows the attribution line directly. For an HTML body this means leading
      blocks that carry no text — empty paragraphs, `<br>`s, `&nbsp;` spacers, whitespace-only text — are removed,
      descending into leading `div`/`blockquote`-style wrappers so a wrapped body is trimmed like a bare one; a
      text-free block that is content in itself (one holding an image, rule, table, or other replaced element) is kept,
      as is the inside of a `<pre>`. For a plain-text body, leading empty lines are dropped. Blank lines elsewhere in
      the body are untouched. Without this, the blank lines a mailer leaves above its own body become bare `> ` lines
      that accumulate on every further reply round. Forward is not trimmed — the forwarded body is presented as-is.
    - **Signature placement (Reply / Reply-All):** the identity signature (with the standard `\n-- \n` delimiter) is
      placed at the **top** of the new body, above the attribution line and quoted material — i.e. top-posting. This
      matches the dominant convention in modern web and desktop mail clients and is what users typing into the compose
      area expect.
    - **Signature placement (Forward):** the signature appears once at the top in the same position; the forwarded
      content follows the attribution line.
    - **Forward wrapper:** the forward attribution line is replaced by a four-line block:

      ```
      ---------- Forwarded message ----------
      From: <original From>
      Date: <RFC 1123 date as above>
      Subject: <original Subject>
      To: <original To>
      ```

      followed by a blank line, then the original body (no `> ` prefix and no `<blockquote>` for forwards — the
      forwarded content is presented as the new message body, with the wrapper acting as the boundary).

   **Quoted material is not loaded into the editor.** The rich-text editor receives only the editable half — the blank
   composition area and the signature. The quoted half (attribution line plus quote, or the forward wrapper plus
   forwarded body) is held outside the editor, displayed read-only below it, and concatenated back on when the draft is
   saved or the message is sent. Quoted text is therefore not editable in place; it can only be kept or discarded as a
   whole (see below).

   This is a performance requirement, not a stylistic one. Quill re-derives and diffs the entire document on every DOM
   mutation, so an editable buffer containing the quote makes each keystroke cost O(entire thread). A long `>` chain
   re-quotes everything before it on every round, so its size grows quadratically in reply depth: measured on a 3.3 MB
   chain, opening Reply took 1.4 s and each keystroke blocked the main thread for ~270 ms, which is enough for a browser
   to report the page as unresponsive. Keeping the quote out of the editor makes both costs independent of thread size.

    - **Storage format:** the two halves are joined as `<editable half><!--mymail-quote--><quoted half>` in `body_html`.
      The marker lets a reopened draft be split apart again. Drafts are stored verbatim, so the marker survives a
      save/reopen cycle; the outgoing sanitiser drops comments, so it never reaches a recipient.
    - **Plain-text alternative:** `body_text` is the editor's text, then a blank line, then a plain-text rendering of
      the quoted half. That rendering is derived solely from the quoted HTML — `<br>` and block elements end a line, and
      each `<blockquote>` level adds one `> ` marker to every line it contains — so reopening a draft reconstructs
      exactly the same text. Line endings in the source `body_text` are normalised to `\n` before quoting, so
      CRLF-terminated originals do not produce blank lines between quoted lines. Whitespace in the quoted HTML collapses
      as a browser would render it: runs of spaces, tabs and newlines become a single space, whitespace opening a line
      is dropped as indentation, and only `<br>` and block boundaries end a line. Without this the
      newline-plus-indentation between every pair of tags in a pretty-printed body — a layout-table message especially —
      would render as a screenful of bare `> ` lines. `U+00A0` is exempt (a non-breaking space is content, not layout),
      as is everything inside a `<pre>`, where whitespace is text.
    - **Line breaks at the compose line width:** no line of a composed message exceeds the configured column, **80 by
      default**, counting only what is visible — trailing blanks do not make a line too long, and are never broken at,
      since everything past such a break is whitespace and the continuation line would hold nothing at all. A line
      longer than the column is broken at the last space or tab at or before it, and the break replaces exactly **one**
      character of the whitespace run it lands in: the continuation line never opens with a blank, and a run of several
      survives as trailing whitespace on the line that opened it. Consuming the whole run instead would delete text —
      the second of two spaces after a sentence — and could not be undone, since dissolving a break puts back a single
      space. Spacing elsewhere in the line, blank lines, and the line structure the author typed are all untouched.
      Three cases are deliberately left over-long: a single word wider than the available width (a long URL) is emitted
      whole rather than split, a quote nested so deeply that its markers alone reach the column is left unwrapped, and
      no line is emitted that would hold only quote markers.

      **The column is a preference** (Settings → Preferences → Compose Line Width), stored under `wrapColumn` in
      `localStorage` and accepted between 20 and 998 — the lower bound leaves room for content past a quote marker, the
      upper is RFC 5322's hard limit on a line, so no setting can produce an illegal one. A value outside that range is
      clamped rather than rejected, and a fractional one is rounded. The column is read at each wrap, so changing it
      takes effect on the next edit — and since the editor re-fills paragraphs rather than only breaking them, widening
      the column pulls the existing breaks out again.

      **Zero turns wrapping off**, since there is no column zero and a separate flag could fall out of step with the
      column. Nothing is broken, in the editor or in `body_text`, and the editor's existing breaks are dissolved on the
      next edit rather than frozen in place: turning the feature off takes back what it did, leaving only the breaks the
      author typed. A negative column is read as off for the same reason it is not clamped upwards — there is no
      sensible column there to guess at. Absent, blank and unreadable settings are the one thing that is *not* off: they
      mean a reader who never touched the setting, and they get 80.

      **In the editor.** The wrapping happens as the message is typed, so what the author sees is what the recipient
      gets. Every change — typing, pasting, restoring a draft, undoing — leaves each paragraph filled to the column. A
      break is not merely added: the paragraph is unwrapped and re-filled, so editing text early in a paragraph moves
      the breaks after it instead of pushing one word at a time onto lines of its own. This means the breaks are real
      paragraph breaks in `body_html` too: an HTML alternative composed this way is hard-wrapped and does not re-flow to
      the recipient's window width.

      To re-fill a paragraph the editor has to know which breaks are its own. Each one carries a block format rendered
      as `class="ql-softwrap-y"` on the paragraph. `class` is on no sanitiser allowlist, so the mark is dropped on the
      way out and never reaches a recipient; drafts are stored verbatim, so it survives a save/reopen cycle and
      re-filling keeps working on a reopened draft. Breaks without the mark are the author's own and are never
      dissolved — including the one Enter makes inside a wrapped paragraph, which has to have the mark cleared
      explicitly, since splitting a paragraph otherwise copies it to both halves. If the mark is ever lost, the wrapper
      degrades to only ever adding breaks, never moving them; it does not merge paragraphs the author separated.

      Three things the editor leaves alone. A line that carries a block format of its own — a list item, heading,
      blockquote or code block — because in the editor's model the break carries that format, so splitting the line
      would duplicate it (a wrapped bullet would become two bullets). A line already carrying `> ` quote markers,
      because a break made in the editor has to stay dissolvable and a marker inserted with it could not be told from
      one the author typed; wrapping it there would ship continuation lines with no marker at all, so it is left to the
      save-time wrap, which can add them. And a document holding an embedded object such as a pasted image, whose
      position is counted but contributes no characters, so every index after it would be out of step.

      Leaving a line alone means leaving it whole: the breaks inside it are not dissolved either. Refusing only to
      *re-fill* it would pull it back together, which is how making one visual line of a wrapped paragraph into a list
      item would otherwise swallow the text above it into the bullet. The newline a document ends with is likewise never
      dissolved — it terminates the document rather than separating two lines, and replacing it with a space would only
      make the editor append a fresh one, a blank at a time.

      **On the way out.** `body_text` is wrapped again when the draft is saved or sent. The editor has normally done the
      work already, and wrapping is idempotent, so this is a backstop: it covers the lines the editor will not break in
      place, and the quoted half, which never enters the editor. There, each continuation line repeats the leading
      indentation and `> ` quote markers of the line it came from; without the markers, a wrapped quote's continuation
      lines read as newly written text to the recipient. The quoted-text preview below the editor shows the wrapped
      form, i.e. exactly what will be sent.
    - **Quoted-text control:** the read-only block shows the quote's size and can be expanded to review it as plain
      text, or removed entirely, in which case neither half of the quote is sent. Reply and Reply All open with the
      block already expanded, so the message being answered is visible while writing; Forward and reopened drafts open
      collapsed.

   Signatures are pre-populated from the selected identity, with `\n-- \n` delimiter. Changing the From identity swaps
   the signature block; this affects the editable half only, since the quoted half never contains a signature.

   **Signature HTML conversion:** The signature is stored as plain text but must be inserted into Quill's HTML content
   model. Convert it as follows: the standard email signature delimiter line (`-- ` — two hyphens followed by a space)
   is rendered as `<hr>`; all other lines have `&`, `<`, and `>` escaped to `&amp;`, `&lt;`, and `&gt;` respectively,
   and line breaks become `<br>`.

   **Locating the signature.** The swap has to find the old signature before it can replace it, and the editor does not
   give back the HTML it was handed: a `<br>` becomes a block break, the `<hr>` a delimiter becomes is dropped, runs of
   spaces collapse, and any line past the wrap column is broken into two paragraphs. Searching the editor's HTML for
   the converted signature therefore fails for nearly every real signature, and a swap that cannot find the old one
   leaves it in the message — beside the new one, or, when the new identity has no signature, alone and unannounced.
   Sending the previous identity's name and employer to a recipient is the whole failure the swap exists to prevent, so
   the signature is marked rather than searched for: the blocks it occupies carry a block format rendered as
   `class="ql-signature-y"`, exactly as the wrapper marks its own breaks, and the swap replaces the span from the first
   marked block to the last. `class` is on no sanitiser allowlist, so the mark never reaches a recipient; drafts are
   stored verbatim, so it survives a save and reopen and the swap still works on a reopened draft.

   The mark moves with ordinary editing, and two edits have to keep it deliberately. A break the wrapper makes inside a
   signature line carries the mark, so the half above it stays part of the signature — without that the swap would
   replace only the half below and leave the other above the new signature. Enter at the *end* of the signature does
   the opposite: the paragraph it starts is not signature, so the mark is cleared there, and text written below the
   signature is not deleted by a later swap.

   A signature that cannot be located is not guessed at. On opening a draft written before the mark existed, the
   editor looks for the selected identity's signature as text — with the wrapper's breaks dissolved, so a wrapped
   signature still matches — and marks it if it is found. If it is not, and when the previous identity had no signature
   or the author deleted it, the swap appends the new signature after everything written so far, which is directly
   above the quote.

   **Send button behavior:** Disabled while in-flight, and disabled until the message has a valid recipient — at least
   one of To/Cc/Bcc non-empty, and none of the three malformed. An address typed into an address field but not yet
   committed to a pill counts as a recipient: it is visible to the user, Send folds it into its field before saving, and
   leaving the field commits it as a pill on its own — but only when it is well-formed, since a malformed address list
   makes the server reject the whole draft, which would break every subsequent auto-save. Performs an immediate draft
   save before sending. On send failure, keeps the compose form open and shows the error inline.

   **Address list quoting:** a display name containing a comma (or any other RFC 5322 special) is quoted when a pill is
   built from a contact, otherwise the comma reads as a recipient separator and the list is malformed. Since
   `DecodeAddressHeader` stores address headers with the quoting removed, a stored sender is re-quoted on its way into a
   pill when replying, forwarding, or reopening a draft. This is only possible for one address at a time — a *list* that
   was stored with an unquoted comma is genuinely ambiguous and is split at that comma.

   **Auto-save failure:** If an auto-save request fails, a transient error toast is shown. The save is retried on the
   next 30-second tick. If the navigate-away save fails, a brief warning is shown but navigation is not blocked.

4. **Scheduled folder message detail** — Shows scheduled send time; **Edit schedule** button opens an inline datetime
   picker to update `send_at` (same > 60 s threshold as initial scheduling); **Send now** button immediately sends the
   message (on sendmail failure it is moved to Drafts and an error is shown); **Cancel schedule** button moves message
   to Drafts without sending.

5. **Search** — Full-text search results as a message list. A folder selector (dropdown) allows limiting results to a
   single folder; when no folder is selected the search is global (all folders except Junk, Drafts, and Scheduled). Two
   native HTML date pickers (From date, To date) allow limiting results to a date range; when set they are passed as
   `date_from` and `date_to` to `GET /messages/search` (the From date is sent as the start of the selected day in the
   user's local timezone, the To date as the start of the day after). Two text fields (From, To) allow refining results
   by sender and recipient address; when non-blank they are trimmed and passed as `from_addr` and `to_addr`. `from_addr`
   matches the `From` header, `to_addr` matches the `To` **or** the `Cc` header, both as a case-insensitive substring —
   the same rule as a filter's `match_from`/`match_to`, and with `%` and `_` treated as literals rather than wildcards.
   All refinements are ANDed with each other and with the full-text query. The search bar, folder selector, address
   fields, and date pickers are shown together in the search view; the refinements reset whenever a new query arrives
   from the toolbar, and pagination re-runs the last submitted set rather than the current form contents.

6. **Filter management** — CRUD UI with drag-to-reorder. The `match_to` field is labelled "To / Cc".

7. **Folder management** — Create/rename/delete/reorder user folders.

8. **Identity management** — CRUD UI with drag-to-reorder. Default identity marked visually. Signature field is a
   plain-text textarea.

9. **Spam filter settings** — Enable/disable toggle, score threshold field, score header name field.

10. **Contact management** — Paginated list with add/edit/delete.

11. **Preferences** — Client-side display preferences: dark mode toggle (the same preference the sidebar's
    light/dark button carries; either control moves the other), message list density (Compact/Normal/Relaxed),
    default body view (HTML/Plain text), browser notifications toggle, compose line width (the column composing wraps
    at, or `0` to wrap nothing — see **Compose View → Line breaks**).

### Settings Navigation

The sidebar footer holds two controls: a light/dark mode button, and — to its right — a gear icon that opens
`/#/settings`.

**These two are a shared MySuite contract, not MyMail's to define.** MyCal, MyNotes and MyMail must render them
identically, so that someone with all three open in browser tabs sees nothing move when switching between them.
Their geometry, colours, hover and focus treatment, the width-stable label, and their position on screen are
specified in **`spec/sidebar-footer.md` in the `mysuite` repository** (`../mysuite`, alongside this one —
<https://github.com/mikaelstaldal/mysuite>; referenced by path because that is what resolves with both
checkouts side by side). **Changing any of this is a change in all three repositories.** The
shared values — the box geometry, the colours, the focus treatment, the (8, 8) position — are deliberately not
repeated here, because a second copy is what goes stale. What follows is only what is MyMail's own; where a number
below is also in the contract, it is there as MyMail's budget rather than as the shared value.

What is MyMail's own, and stays here:

- The Settings control is an **`<a href="#/settings">`**, where the sibling apps use a `<button>`. It carries the
  identical rule plus `text-decoration: none`, and takes its accessible name from its own text rather than from a
  `title`/`aria-label` pair. One consequence is user-visible and deliberately left alone for now: with no `title`,
  hovering it shows no native tooltip, where the sibling apps' buttons do. The accessible name is `Settings` in all
  three either way. Tracked in the contract's open items, to be settled across all three rather than here.
- The footer separator resolves through **`--sidebar-footer-border`**, a MyMail-local alias for `--border`, so the
  rule does not reach past the sidebar's token layer. Its value is the contract's; the indirection is not.
- **`--focus-ring` is still MyMail's focus token** and is used by many other rules. These two controls simply stop
  using it; the token stays.
- The sidebar column is **`13.75rem`**, not a pixel width: the buttons are sized in `rem`, so a fixed column would
  let a reader's larger browser font grow them out of it — WCAG 1.4.4 (Resize Text). At the default root that
  leaves **203px** of content box for the pair, which is MyMail's own budget and the reason a wider label does not
  fit (see `Sidebar.tsx`).
- The mode button writes the same stored preference as the Preferences tab's dark mode switch; either control
  moves the other.

The Settings page itself uses a tabbed layout:

| Tab slug      | Content              |
|---------------|----------------------|
| `identities`  | Identity management  |
| `folders`     | Folder management    |
| `filters`     | Filter management    |
| `spam`        | Spam filter settings |
| `contacts`    | Contact management   |
| `preferences` | Preferences panel    |

### Date and Time Display

All timestamps displayed in the browser's local timezone. Display format is adaptive:

| Age                | Format                     | Example           |
|--------------------|----------------------------|-------------------|
| < 1 hour           | Relative ("X minutes ago") | "42 minutes ago"  |
| 1 hour – 23:59     | Time only (HH:MM, 24-hour) | "14:32"           |
| Yesterday          | "Yesterday HH:MM"          | "Yesterday 09:15" |
| 2–6 days ago       | Weekday + time             | "Mon 14:32"       |
| 7 days – same year | Short date + time          | "Apr 3, 14:32"    |
| Previous years     | Short date with year       | "Apr 3, 2023"     |

Message detail always shows the full "Apr 3, 14:32 CEST" form with timezone abbreviation.
Simple timestamps have a tooltip with the full form.

### Confirmation Dialogs

The UI never uses the browser's native `window.confirm`/`alert`/`prompt`. Every confirmation is an in-app modal dialog
(`web/ts/components/ConfirmDialog.tsx`, driven by `confirmDialog()` in `web/ts/util/confirm.ts`) with a title, the
question, and two buttons. It is dismissed by Escape or a click outside the dialog, which both count as declining, and
Tab stays inside it. A destructive question opens with the **declining** button focused, so a reflexive Enter keeps
rather than deletes; every other question opens on the confirming button.

Because the dialog does not block the event loop the way `window.confirm` did, a caller must claim its in-flight guard
*before* awaiting the answer (and release it if declined), and must act on the selection it snapshotted before asking —
not on whatever the selection has become by the time the user answers.

Both buttons are named after what they do — never "OK" and "Cancel" together:

- **Anything that deletes** (delete a message, empty Trash or Junk, discard a draft, delete a folder, identity, filter,
  or contact) is confirmed with **Delete** and declined with **Keep**. The confirming button is styled as destructive.
- **Anything else** is confirmed with its own verb (**Send**, **Schedule**, **Move to Drafts**) and declined with
  **Cancel** — except where "Cancel" is itself the action being confirmed (cancelling a scheduled message), where the
  declining button says **Keep scheduled**.

Confirmation is requested for: deleting a message (from message detail, and in bulk from a message list), emptying
Trash or Junk, discarding a draft (in compose, in message detail, and in bulk from the Drafts list), sending or
scheduling a stored draft, cancelling a scheduled message, and deleting a folder, identity, filter, or contact.

A delete that cannot be undone says so. Deleting from Trash or Junk — single or bulk — is worded "Permanently delete …
This cannot be undone."; deleting from anywhere else says the message will be moved to Trash. Bulk wording names the
count ("Permanently delete these 4 messages?"), and that count is the selection as it stood when the question was
asked. The other bulk actions (Mark read, Mark unread, Move to) are not confirmed: none of them loses anything.

### Error Handling UX

- **Transient API errors:** shown as a toast/snackbar (bottom-right), auto-dismisses after 5 seconds.
- **Form validation errors (400):** shown inline below the submit button.
- **Network failures:** retried once after 2 seconds; if the retry fails, a persistent toast with a **Retry** button is
  shown.
- **404 on navigation:** shows inline "Not found" in the detail pane.
- **Auth failure (401):** redirects to the browser's built-in Basic Auth dialog.

**Junk folder:** The **Delete** button on a Junk message permanently deletes it immediately (no Trash step), consistent
with `DELETE /folders/7/messages` bulk-delete semantics. Message detail shows a **Not junk** button (moves to Inbox and
marks as unread — mirroring snooze-expiry behaviour so the message appears as new on return to Inbox) and standard Move
controls (allows moving to any folder directly). All other views show a **Mark as junk** button, **except** messages in
Snoozed, Scheduled, or Drafts — for those the **Mark as junk** button is not shown. The schedule or snooze must be
cancelled (or the draft discarded) before marking a message as junk. After **Mark as junk** is triggered, the UI stays
in the current folder (it does not navigate to the Junk folder).

**Empty folder button:** Trash and Junk views show an **Empty** button (with confirmation prompt). For Trash: messages
already in Trash are permanently deleted (standard two-step semantics). For Junk: all messages are permanently deleted
immediately, regardless of whether they have been in Trash previously — moving spam to Trash is not useful. Drafts,
Scheduled, and Snoozed do not show the Empty button; those folders have dedicated lifecycle management (draft deletion,
schedule cancellation, and snooze cancellation respectively) and bulk-emptying them would bypass that logic. Inbox,
Sent, and user-created folders also do not show the Empty button in v1 by design; the API endpoint
`DELETE /folders/{folder_id}/messages` supports them (messages are moved to Trash), but no UI button exposes this
capability in v1.

### New Message Notifications

The UI polls the REST API every 30 seconds. When the Inbox `unread_count` increases:

1. Updates the unread badge in the sidebar.
2. Updates `document.title` (e.g. `(3) MyMail`).
3. If the Notifications API permission is granted, fires a browser notification.

Polling is suspended while the browser tab is hidden.

Permission is requested only when the user explicitly enables browser notifications in Preferences. If the browser
denies permission, or if permission is later revoked, the notifications preference toggle is automatically switched off
and the feature degrades silently (polling continues; no browser notifications are shown).

### Client-Side Storage (`localStorage`)

- Selected folder
- Compose draft state (JSON with all field values, draft `id`, and `savedAt` timestamp)
- Dark mode toggle (applied as `data-theme` on `<html>`, `"light"` or `"dark"`)
- Message list density preference
- Notification permission state (cached)
- Preferred body view (`"html"` or `"text"`)
- Compose line width (`wrapColumn`; `0` disables wrapping, absent means the default of 80)
- Demo notice dismissed (`mymail-demo-notice-seen`; demo builds only — kept out of the demo's own IndexedDB store so
  clearing the mailbox does not bring the notice back)

**Draft recovery on page reload:** compares `savedAt` in localStorage against the server draft's `updated_at`; whichever
is newer is loaded. If the timestamps are identical, the server version is loaded. If the server returns 404, the
localStorage state is used and the stale id is cleared.

## Demo Mode

`-demo-server` serves the web UI, and `-demo-bundle DIR` writes it out as a
static site, with **no backend**: a service worker answers every `/api/v1`
request from browser-local storage (IndexedDB). Neither opens a database, so
neither accepts `-data` or `-lda-socket`. The store starts out holding the same
content `-demo` seeds into SQLite, and clearing the site's data resets it to
that. A one-time modal on first visit says so.

The demo must behave as the real server does. `web/ts/demo/` re-implements
`internal/handler` and `internal/repository`, and every function names the Go
original it mirrors. The following divergences are accepted; anything else is a
bug.

### Behaviour that has no server counterpart

- **Sending produces a reply.** There is no MTA, so a sent message would
  otherwise vanish. Each message sent in demo mode is answered once, by its
  first `To` recipient, 20 seconds later (`AUTO_REPLY_DELAY_MS`). Which of a
  fixed set of replies is used is a pure function of the outgoing subject and
  body, so it is reproducible. The reply arrives through the filter chain like
  any inbound message.
- **Sending cannot fail.** `send_failure_count` and `send_error` therefore stay
  at zero/null forever, and the `send_failed` badge and the "moved back to
  Drafts after exhausted retries" path are unreachable.
- **Storage can be full.** A write that exceeds the origin's quota returns
  `507`, a status the real server never produces. Attachments are additionally
  capped at 8 MiB each, where the server caps only the whole request at 32 MiB.

### Behaviour that differs

- **The scheduler runs on request, not on a timer.** A service worker is stopped
  whenever it is idle, so deferred sends, snooze expiry, and reply delivery are
  processed at the start of each API request instead of by a 60-second
  goroutine. The 60-second thresholds themselves are unchanged. In practice the
  UI's 30-second folder poll makes this indistinguishable.
- **Sending a draft reuses its row.** The server creates a new message and
  deletes the draft; the demo moves the draft itself to Sent, so the resulting
  message keeps the draft's id. Nothing in the UI depends on that id. An
  immediate send restamps `date` and `created_at` to now, as the row the server
  inserts would carry, so the reuse is not otherwise observable; the deferred
  and scheduled paths leave `date` alone, matching `MarkSent` and the scheduler.
- **Search ranking is not bm25.** Matching is identical — the server passes the
  query to FTS5 as one quoted phrase, so operators are literals and a multi-word
  query only matches consecutive words — but `ORDER BY rank` needs index
  statistics the demo does not keep, so results are ordered by a weighted
  match count (subject above body) and then by date.
- **Outgoing HTML is not sanitised.** `bluemonday` has no in-worker equivalent.
  Nothing is transmitted and the body is rendered in a sandboxed iframe with no
  `allow-scripts` under a `default-src 'none'` CSP, so the sanitiser's job is
  already done by the iframe; `has_external_images` is still computed.
- **The stored RFC 5322 source is simplified.** `raw` is assembled with the same
  MIME structure (`multipart/alternative` inside `multipart/mixed`), but text
  parts are written as UTF-8 with `Content-Transfer-Encoding: 8bit` rather than
  quoted-printable, and display names and subjects are written literally rather
  than as RFC 2047 encoded words. `raw` is only ever shown in the headers view
  or downloaded as `.eml`.
- **Spam detection never runs.** `spam_filter_settings` is stored and editable,
  but it reads headers that a generated reply does not have.
- **The message body reaches the iframe as `srcdoc`.** A sandboxed iframe has an
  opaque origin, and a browser does not consult a service worker for a
  navigation out of one, so `<iframe src="api/v1/…/body">` would escape to a
  server that is not there. In demo mode the page fetches the document and
  passes it as `srcdoc`; the demo's response repeats its CSP in a `<meta>`,
  since response headers are lost on that path.
- **No MyCal integration.** A demo build injects no MyCal URL — `__serverConfig`
  carries `demo` and nothing else — so neither "Import to Calendar" action is
  offered: not on an `.ics` attachment, and not on a calendar link in the body.
  *(This said the action "is never offered", which overstates it: the MyCal URL
  field is still shown in Preferences, and setting it there brings both actions
  back. Neither needs a MyMail backend — the attachment bytes come from the
  service worker, and the link form is a URL handed to MyCal, which fetches it
  itself — so against a reachable MyCal they would work.)*
- **Schema-violation messages are worded differently.** Where a request breaks a
  constraint declared in `openapi.yaml` rather than one a handler checks — an
  over-long `q` or `from_addr`, a non-numeric `limit` — the server relays the
  generated decoder's wording (`query parameter "from_addr": string: len 201
  greater than maximum 200`) and the demo writes its own. The status is 400 and
  the body is `{"error": …}` either way, which is what the UI depends on.

### Requirements

- Service workers need a secure context: the bundle must be served over HTTPS or
  from `localhost`.
- The seed content is deliberately fresh per build: dates are relative to build
  time and the extra messages are picked at random, so two builds of the same
  commit produce different `demo-data.json`. A bundle pinned to fixed dates
  would show a mailbox that visibly ages the longer it stays published.
- Every URL the app builds is relative and routing is on the fragment, so a
  bundle works at the origin root or under any path with no configuration.
- `-demo-bundle` refuses a directory that already has content.

## Production Deployment

mymail binds plain HTTP and delegates TLS termination, rate limiting, and access control to the operator's
infrastructure:

- **TLS:** Place mymail behind a TLS-terminating reverse proxy before exposing outside localhost.
- **Rate limiting:** Apply a per-IP request rate limit at the reverse proxy to prevent CPU exhaustion from repeated
  failed authentication attempts.
- **Bind address:** Use `-addr 127.0.0.1` when not behind a reverse proxy on the same host.

## Performance and system requirements

mymail should be able to handle a database of at least 10 GiB containing at least 200000 messages on a system with
512 MiB RAM with reasonable performance for a single user.

## Out of Scope

- **Multiple mailboxes / multi-user support**
- **PGP/S-MIME**
- **Push notifications** (polling every 30 seconds is sufficient for v1)
- **Offline support / PWA**
- **Mobile / responsive layout** (targets desktop browsers only)
- **Inline attachment preview** (only download is provided)
- **Cross-folder Starred view**
