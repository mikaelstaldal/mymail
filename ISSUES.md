# Open Issues

Issues requiring a decision or further research before the spec can be finalised.

---

## Security

### S1 — iframe sandbox attributes (§13)
`sandbox="allow-same-origin"` lets the sandboxed iframe run as the same origin as the parent UI. Without `allow-scripts` this is mostly safe, but it means CSS-level tracking pixels in email HTML are not blocked by the sandbox, and any future accidental addition of `allow-scripts` would be catastrophic. Decide: use `sandbox` with no attributes (safest, breaks no-JS CSS layouts), or `sandbox="allow-popups allow-popups-to-escape-sandbox"` to let links open in a new tab without same-origin privileges.

### S2 — CSRF protection (§12)
The spec uses HTTP Basic Auth but specifies no CSRF tokens or `SameSite` cookie policy. When auth is disabled (the default), any web page can issue authenticated API requests from the user's browser. Decide: require `Origin` / `Referer` header validation, add a CSRF token, or accept the risk given the "local server only" use case and document the requirement to bind to `127.0.0.1`.

### S3 — `style` attribute allowed properties list (§10)
The sanitisation policy permits the `style` attribute with a "restricted property list" that is never defined. Unfiltered `style` allows CSS-exfiltration via `background-image: url(https://tracker.example.com)`. Decide which CSS properties are allowed (e.g. `color`, `font-*`, `text-*`, `background-color`) and add the list to the spec.

### S4 — `Content-Disposition` filename encoding (§5.2)
Attachment filenames come from untrusted email headers. A filename containing `\r\n` or `"` can break the `Content-Disposition` header. Decide: sanitise (strip non-printable characters, escape quotes) and/or encode using RFC 5987 (`filename*=UTF-8''...`).

### S5 — Rate limiting (§12)
No rate limiting is specified on any endpoint. Without it, Basic Auth is trivially brute-forceable. Decide: add a configurable rate limit (e.g. `-rate-limit` flag, default 20 req/s per IP), or document that rate limiting is expected from a reverse proxy.

### S6 — HTTPS / TLS (§12)
The spec describes HTTP-only operation but recommends Basic Auth, which sends credentials in plaintext without TLS. Decide: add native TLS support (`-tls-cert` / `-tls-key` flags), or add an explicit warning that Basic Auth requires a TLS-terminating reverse proxy (nginx, Caddy, etc.) and document a recommended setup.

---

## Reliability

### R1 — Schema version tracking (§4)
Migrations are implemented as `CREATE TABLE IF NOT EXISTS` plus `ALTER TABLE ADD COLUMN`, but there is no migrations table to record which migrations have been applied. This is fragile once a second or third migration is added. Decide on a migration strategy (e.g. `PRAGMA user_version`, a `schema_migrations` table, or a third-party library).

### R2 — Thread view: full bodies, no pagination (§5.8)
The thread endpoint returns full message objects including `body_html` and `body_text`. Long threads (100+ messages) or messages with large HTML bodies will produce very large responses. Decide: return message summaries (same shape as the list endpoint) with a separate fetch for the selected message, or add a `limit`/`offset` parameter.

---

## Missing features

### F1 — Reply All (§13)
Reply-All is absent from the compose view description. It is standard for email. Decide whether to add it; if so, the pre-population logic (recipients = original To + Cc minus own identity address) needs specifying.

### F2 — Empty Trash / Empty Junk (§5.2, §13)
There is no endpoint to delete all messages in a folder at once. Users cannot empty Trash or Junk without selecting every message individually. Consider adding `DELETE /api/v1/folders/{id}/messages` (delete all) or a bulk-select-all UI action.

### F3 — Inline images (`cid:` references) (§10)
The sanitiser strips `cid:` from `<img src>`, so inline images (common in formatted email) display as broken images. Options: (a) add a proxy endpoint `GET /api/v1/messages/{id}/parts/{content_id}` and rewrite `cid:` URLs to it during sanitisation; (b) embed inline parts as `data:` URIs; (c) document the limitation.

### F4 — Per-identity signature (§4.5, §13)
No sender signature is specified. It is a standard email client feature. If desired, add a `signature` TEXT column to the `identities` table and pre-populate the compose body with it.

### F5 — Address autocomplete / contact list (§13)
Users must type addresses from memory. Consider a `GET /api/v1/contacts?q=...` endpoint that queries distinct `from_addr` / `to_addr` values from the `messages` table and returns matching name+address pairs.

### F6 — Access to Snoozed folder (§13)
The Snoozed folder is hidden from the sidebar with no UI path to browse or bulk-cancel snoozed messages. Consider showing it in the sidebar like the Scheduled folder, or providing a dedicated "Snoozed" view.

### F7 — "Mark all as read" folder action (§13)
The bulk PATCH endpoint supports marking all messages read, but the UI spec does not describe a "Mark all as read" button per folder. This is a common email action.

### F8 — Plain text / HTML view toggle (§13)
When a message has both `body_html` and `body_text`, the spec does not say which is shown by default or whether the user can switch. Specify default behaviour and whether a toggle is provided.

### F9 — Forward with attachments (§13)
"Forward" is listed as an available action but the compose flow for forwarding is not specified. Clarify: how are original attachments pre-populated into the compose form (copied as new attachment rows? referenced lazily?), and what API call initiates a forward.

### F10 — Auto-save draft recovery UX (§13)
The spec says drafts auto-save every 30 seconds and `localStorage` is a fallback, but does not describe what happens on page reload if both a server draft and a `localStorage` draft exist (e.g. tab crashed mid-edit). Specify the recovery prompt UX.

### F11 — Draft + attachment flow (§5.3)
There is no `POST /api/v1/drafts-with-attachments` equivalent. The spec says "Attachments in the send flow are handled via a separate endpoint" but only defines that endpoint for `/messages/send`. Specify how attachments are saved with drafts (e.g. multipart draft creation, or a separate `POST /api/v1/drafts/{id}/attachments` endpoint).

---

## Ambiguous / under-specified behaviour

### A1 — §5.7 is missing
The spec jumps from §5.6 (Spam Filter) to §5.8 (Thread View). Either a section was removed without renumbering, or the numbering was incremented by mistake. Verify and correct.

### A2 — BCC in sent copies (§9)
`sendmail -t` reads recipients from headers, so if the `Bcc` header is included in the piped message it will be delivered to BCC recipients correctly. However, the raw BLOB stored in the Sent folder will contain the `Bcc` header, exposing recipients if the raw message is later inspected. Decide: strip `Bcc` from the raw BLOB before storage (as most MUAs do), or preserve it.

### A3 — Message-ID hostname source (§9)
The spec says generate `<uuid@hostname>` but does not specify how `hostname` is determined (`os.Hostname()`? a configurable `-hostname` flag?). This matters for uniqueness and for message threading interoperability. Specify.

### A4 — `match_to` filter UI label (§4.6, §13)
The `match_to` column matches both the `To` **and** `Cc` headers (as documented in the SQL comment), but the column name and any auto-generated UI label would read as "To" only. Specify that the filter management UI must label this field "To / Cc" to avoid user confusion.
