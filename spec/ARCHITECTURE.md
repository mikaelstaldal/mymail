# MyMail — Architecture

## Backend

### Technology Stack

- Go
- SQLite

### Project Structure

Layered architecture: `handler → service → repository → SQLite`

```
mymail/
├── main.go                   # Entry point, CLI flags, routing, startup
├── cmd/
│   └── lda/                  # Thin LDA client binary (forwards to server via UNIX socket)
├── internal/
│   ├── api/                  # Code generated from OpenAPI specification (do not edit)
│   ├── auth/                 # HTTP Basic Auth middleware (htpasswd)
│   ├── demo/                 # Demo dataset; seeds SQLite and exports demo-data.json
│   ├── handler/              # HTTP handlers (REST API)
│   ├── lda/                  # Local delivery agent (parse & store incoming mail + socket server)
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
- `node --test` for frontend unit tests, run against the compiled output in
  `web/static/` with jsdom supplying the DOM (vendored, no package-manager
  install at build time).


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

### Socket-Based LDA Delivery
The full `mymail` binary carries embedded web assets, ogen-generated HTTP stubs, and SQLite — roughly 14 MB RSS per process. When Postfix spawns two concurrent LDA invocations this overhead doubles. To reduce per-invocation memory cost a separate minimal `mymail-lda` binary (≈3 MB RSS, no SQLite, no HTTP server code) forwards raw RFC 5322 messages to the running server over a UNIX socket. The server handles all database access; the LDA client has no direct DB dependency. If the socket is unreachable the client exits 75 so the MTA retries later.

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

### Demo Mode Runs the Backend in the Browser

`-demo-server` and `-demo-bundle` serve the web UI with no database and no REST
API: a service worker (`web/ts/demo-sw.ts` + `web/ts/demo/`) intercepts
`/api/v1` and answers it from IndexedDB.

Intercepting at the network layer, rather than swapping the API client for a
mock, keeps the frontend the same in both modes — including the requests that do
not go through `api/client.ts` at all. The cost is that `web/ts/demo/`
re-implements the handler and repository layers in TypeScript and has to be kept
in step with them; the accepted divergences are listed in REQUIREMENTS.md §
Demo Mode.

The demo content is not written twice: `internal/demo` defines it once, seeds
SQLite with it for `-demo`, and exports the same rows as `demo-data.json` for the
browser.

### Snooze Restores Unread State
`read` reset to 0 when a snoozed message returns — it should behave like a new arrival.
