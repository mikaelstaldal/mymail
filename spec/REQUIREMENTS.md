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
| `-sendmail`         | `sendmail`          | Path to the sendmail binary                            |

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
- Duplicate detection: if a message with the same `Message-ID` already exists anywhere in the database, it is skipped. Messages without a `Message-ID` are always imported.
- Filters are **not** applied during import — messages go directly to the specified target folder.
- A running count is printed to stdout as each folder completes: `inbox: 1042 imported, 3 skipped`.
- On completion, a summary line is printed: `Total: 2381 imported, 17 skipped`.
- Exit code `0` on success, `1` on any error. A single unparseable message logs a warning and continues. A message is considered unparseable when `net/mail.ReadMessage()` returns an error (missing or malformed headers); missing optional fields (e.g. absent `Date`) are warnings, not failures.
- **Concurrency:** Running import concurrently with a running server against the same data directory is not supported.


## Data Model

### Folders

Each folder has a name, a URL-safe slug, and a display order position.

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
- Original raw RFC 5322 message bytes
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
- Email address (unique, must be a valid RFC 5322 addr-spec)
- Default flag (exactly one identity is default at all times)
- Display order position
- Plain-text signature

**Constraints:**
- At least one identity must exist at all times.
- Exactly one identity has the default flag. When a new default is set, all others are cleared. When the default is deleted, the identity with the lowest position (then lowest id) becomes the new default.

### Contacts

Each contact has:
- Email address (lower-cased using Unicode simple casefolding, unique)
- Display name (may be empty)
- Created and updated timestamps

Contacts are upserted automatically:
- On message receipt: the `From` address is upserted. A manually set name is never overwritten automatically (only updated when the stored name is empty).
- On send: `To`, `Cc`, and `Bcc` addresses are upserted.

### Filters

Each filter has:
- Display order position
- Human-readable name
- Match criteria: `match_from`, `match_to`, `match_subject` (all ANDed; at least one must be non-empty)
  - `match_to` matches against both the `To` and `Cc` headers
  - Matching is case-insensitive substring search
- Action: `move` (to a specific folder), `trash`, `mark_read`, or `drop`
- Stop flag: whether to halt evaluation after this filter matches

**Actions:**
- `move` — deliver to the specified folder. If the target folder was deleted, the filter is skipped and delivery continues to Inbox.
- `trash` — deliver directly to Trash.
- `mark_read` — deliver to the folder chosen by spam detection, but mark as read.
- `drop` — discard the message entirely; nothing is stored.

### Spam Filter Settings

A single global configuration:
- Enabled/disabled flag
- Score header name (default: `X-Spam-Score`)
- Score threshold (default: 5.0)

Spam detection triggers on any of:
- `X-Spam-Flag` header equals `YES` (case-insensitive)
- `X-Spam-Status` header starts with `Yes` (case-insensitive)
- The configured score header is present and its numeric value is ≥ the threshold


## Incoming Mail (LDA)

When invoked as `mymail -lda`:

1. Opens the database (creating it if necessary).
2. Reads the raw message from stdin.
3. Parses the RFC 5322 message:
   - Extracts all standard headers.
   - Extracts plain-text and HTML body parts.
   - If no plain-text part exists but an HTML part is present, derives plain text by stripping HTML tags.
   - Resolves `cid:` inline image references to `data:` URIs before sanitizing.
   - Sanitizes the HTML body.
   - Collects attachments.
   - Falls back to current time if no `Date` header; generates a `Message-ID` if absent.
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

### LDA Error Handling

- Database locked: retry up to 30 seconds, then exit `75`.
- Parse failure: log to stderr, exit `1`.
- All other errors: log to stderr, exit `75`.


## Outgoing Mail

The send flow:

1. Constructs a MIME message from the provided fields (subject, to, cc, bcc, reply-to, body, attachments).
   - `Date` is set to the current time at send (not compose time).
   - `Message-ID` is generated as `<uuid@domain>` using the sender's address domain.
   - Body is a single `text/plain` or `text/html` part when only one body type is provided; `multipart/alternative` when both are provided; wrapped in `multipart/mixed` if attachments are present.
   - User-supplied header values are sanitized to strip control characters.
2. Pipes the message to `sendmail -t -oi` with a 30-second timeout.
3. On failure: returns the sendmail stderr as an error. No retries.
4. On success: upserts recipients into the contacts table, stores the sent message in the Sent folder with `Bcc` header preserved in the raw blob.


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

Each message stored as a separate file. A Maildir root contains `new/`, `cur/`, and `tmp/` subdirectories. The `S` (Seen) flag in the filename maps to `read=1`. The `F` (Flagged) flag maps to `flagged=1`.

#### MBX (not supported)

Users with MBX files should pre-convert them using `mb2md` or a similar tool.


## HTML Sanitization

Incoming HTML bodies and the HTML part of outgoing messages are sanitized with a strict email-appropriate policy.

**Allowed elements:** `a`, `b`, `blockquote`, `br`, `code`, `del`, `em`, `h1`–`h6`, `hr`, `i`, `img`, `li`, `ol`, `p`, `pre`, `s`, `strong`, `table`, `tbody`, `td`, `tfoot`, `th`, `thead`, `tr`, `ul`

**Allowed attributes:**
- `href` on `a` (must be `http://`, `https://`, or `mailto:`)
- `src` on `img` (must be `http://`, `https://`, or `data:image/…;base64,…`)
- `alt` on `img`
- `align`, `colspan`, `rowspan`, `style` (restricted CSS properties only)

**Stripped always:** `script`, `style` (standalone), `iframe`, `object`, `embed`, `form`, `input`

**Allowed CSS properties** (all others stripped):

`color`, `background-color`, `font-family`, `font-size`, `font-style`, `font-variant`, `font-weight`, `letter-spacing`, `line-height`, `text-align`, `text-decoration`, `text-indent`, `vertical-align`, `white-space`, `word-spacing`, `border`, `border-color`, `border-style`, `border-width`, `border-collapse`, `border-spacing`, `padding`, `margin`, `width`, `max-width`, `height`

**Explicitly forbidden regardless of property name:** any value containing `url(`, `expression(`, `-moz-binding`, or a CSS comment (`/*`).

**Not allowed:** `background` (shorthand), `position`, `display`, `overflow`, `content`, `z-index`, `opacity`, and all vendor-prefixed properties.

Links inside email bodies have `target="_blank"` and `rel="noopener noreferrer"` added by the sanitizer.

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

External images in email bodies are blocked by the CSP (no `https:` in `img-src`) to prevent tracking pixels. A per-message opt-in for external images is provided via `has_external_images` in the message detail response.

### Authentication

Optional HTTP Basic Auth over all endpoints (API + static UI). Passwords stored as bcrypt hashes in an htpasswd file. If not configured, all requests are accepted without authentication (loopback-only deployments).

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
| `/#/search?q=...`               | Search results                                                      |
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

2. **Message detail** — Full headers, sanitized HTML body in a sandboxed iframe (or plain-text fallback), attachment download links. Reply/Reply All/Forward/Move/Delete/Snooze/Mark as junk buttons. Opening an unread message immediately marks it as read. When the message has both body types, a toggle switches between HTML and plain text; the preference is stored. Thread display: if the message is part of a thread, a collapsed conversation strip is shown below the body; clicking an entry expands it.

3. **Compose / Reply / Reply All / Forward** — Form with From selector, To/Cc/Bcc/Reply-To fields (To/Cc/Bcc offer address autocomplete), Subject, rich-text body editor (Quill), file upload for attachments. A **Send later** toggle reveals a date/time picker. Auto-saves to Drafts every 30 seconds. Navigate-away triggers an immediate draft save.

   Pre-population rules:

   | Field           | Reply                                                        | Reply All                                                          | Forward                                         |
   |-----------------|--------------------------------------------------------------|--------------------------------------------------------------------|-------------------------------------------------|
   | **From**        | Identity matching original To/Cc; falls back to default      | Same as Reply                                                      | Default identity                                |
   | **To**          | Original `Reply-To` if present; otherwise original `From`    | Same as Reply, minus own address                                   | Empty                                           |
   | **Cc**          | Empty                                                        | Original To + Cc minus own address                                 | Empty                                           |
   | **Subject**     | `Re: <original>` (no double Re:)                             | `Re: <original>` (no double Re:)                                   | `Fwd: <original>`                               |
   | **In-Reply-To** | Original `Message-ID`                                        | Original `Message-ID`                                              | Empty                                           |
   | **References**  | Original references + original `Message-ID`                  | Original references + original `Message-ID`                        | Empty                                           |
   | **Attachments** | Empty                                                        | Empty                                                              | Copies of all original attachments (server-side) |

   Signatures are pre-populated from the selected identity, with `\n-- \n` delimiter. Changing the From identity swaps the signature block.

   **Send button behavior:** Disabled while in-flight. Performs an immediate draft save before sending. On send failure, keeps the compose form open and shows the error inline.

4. **Scheduled folder message detail** — Shows scheduled send time; **Cancel schedule** button moves message to Drafts.

5. **Search** — Global full-text search results as a message list.

6. **Filter management** — CRUD UI with drag-to-reorder. The `match_to` field is labelled "To / Cc".

7. **Folder management** — Create/rename/delete/reorder user folders.

8. **Identity management** — CRUD UI with drag-to-reorder. Default identity marked visually. Signature field is a plain-text textarea.

9. **Spam filter settings** — Enable/disable toggle, score threshold field, score header name field.

10. **Contact management** — Paginated list with add/edit/delete.

11. **Preferences** — Client-side display preferences: dark mode toggle, message list density (Compact/Normal/Relaxed), default body view (HTML/Plain text), browser notifications toggle.

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

**Junk folder:** Message detail shows a **Not junk** button (moves to Inbox) and standard Move controls (allows moving to any folder directly). All other views show a **Mark as junk** button.

**Empty folder button:** Trash and Junk views show an **Empty** button (with confirmation prompt).

### New Message Notifications

The UI polls the REST API every 30 seconds. When the Inbox `unread_count` increases:
1. Updates the unread badge in the sidebar.
2. Updates `document.title` (e.g. `(3) mymail`).
3. If the Notifications API permission is granted, fires a browser notification.

Polling is suspended while the browser tab is hidden.

Permission is requested only when the user explicitly enables browser notifications in Preferences.

### Client-Side Storage (`localStorage`)

- Selected folder
- Compose draft state (JSON with all field values, draft `id`, and `savedAt` timestamp)
- Dark mode toggle
- Message list density preference
- Notification permission state (cached)
- Preferred body view (`"html"` or `"text"`)

**Draft recovery on page reload:** compares `savedAt` in localStorage against the server draft's `updated_at`; whichever is newer is loaded. If the server returns 404, the localStorage state is used and the stale id is cleared.


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
