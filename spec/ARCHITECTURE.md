# mymail — Architecture

## Backend

### Technology Stack

- Go
- SQLite

### Project Structure

Layered architecture: `handler → service → repository → SQLite`

```
mymail/
├── main.go                   # Entry point, CLI flags, routing, startup
├── internal/
│   ├── api/                  # Code generated from OpenAPI specification (do not edit)
│   ├── auth/                 # HTTP Basic Auth middleware (htpasswd)
│   ├── handler/              # HTTP handlers (REST API)
│   ├── lda/                  # Local delivery agent (parse & store incoming mail)
│   ├── model/                # Data types
│   ├── repository/           # SQLite data access layer
│   ├── sanitize/             # HTML sanitization
│   └── service/              # Business logic
└── web/                      # Embedded frontend assets
```

Web UI assets embedded in the binary via `//go:embed`.


## Web UI

### Technology Stack

- TypeScript compiled with `tsc` only (no bundler).
- ES6 modules with import maps.
- Preact + JSX for reactive components (vendored, no CDN dependency).
- Quill rich-text editor (vendored, no CDN dependency).
- Plain CSS for styling.


## Key Architectural Decisions

### No IMAP/POP3
Relies entirely on the host MTA for mail retrieval. Postfix handles TLS, authentication, queuing, and delivery retries.

### Raw Message Storage
Every message stored as a raw RFC 5322 BLOB — lossless, allows original download regardless of parse errors or future schema changes.

### SQLite as the Only Store
All data (messages, attachments, identities, contacts, filters, settings) lives in a single SQLite file. Simplifies deployment to a single binary + one data file.

### FTS5 Content Table
FTS index is a content table over `messages` — tokens in FTS, rows in `messages`. Trigger-based sync avoids duplicating large body text.

### Attachment Storage in SQLite BLOBs
Acceptable for personal email workloads. No external file store needed.

### Sendmail for Outgoing Mail
Outgoing mail piped to `sendmail -t -oi`. No send queue in mymail — if `sendmail` fails, return HTTP 500. The MTA handles queueing, TLS, DKIM.

### Sender Identities in SQLite
REST API is the single source of truth. "Exactly one default" invariant enforced in the service layer for human-readable errors.

### Scheduling via Polling
60-second polling loop — simpler than a priority queue, no persistent timer state across restarts. Partial index on `send_at`/`snoozed_until` keeps queries fast.

### Filter Evaluation at Delivery Time
Filters run in the LDA before the message lands in Inbox so badge counts and notifications are always correct. No retroactive filter application.

### Header-Based Spam Detection
Reads spam verdicts from headers set by the MTA pipeline (SpamAssassin, Rspamd, etc.). No built-in classifier.

### Thread View N+1 Fetch Pattern
`/thread` returns summaries; expanding an entry requires a separate fetch per message. Acceptable because personal email threads are short.

### Bulk Operation Atomicity
Bulk endpoints return 404 if any ID is missing — all-or-nothing, no partial success.

### Web UI: No Bundler
TypeScript compiled with `tsc` only. ES6 modules + import maps. Preact and Quill vendored. All assets embedded in the binary.

### Authentication
HTTP Basic Auth via htpasswd file (bcrypt). CSRF protection via Origin/Referer validation middleware.

### `send_failure_count` Exposed as Boolean Only
API exposes only `send_failed` (true when count > 0). Raw count is an implementation detail without UI value.

### Snooze Restores Unread State
`read` reset to 0 when a snoozed message returns — it should behave like a new arrival.
