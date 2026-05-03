# mymail

A self-hosted personal (single-user) email client with backend storage, REST API, and embedded web UI.

## Specification

Adhere to the functional requirements in `spec/REQUIREMENTS.md` and the architecture in `spec/ARCHITECTURE.md`.
Update the architecture spec if necessary. Detailed implementation decisions (SQL schema, endpoint semantics, edge cases) are in `spec/IMPLEMENTATION.md`.

## Build & Development Commands

```bash
# Build (single static binary, no CGO)
go build -tags netgo

# Regenerate API server stubs from openapi.yaml (run after editing openapi.yaml)
go generate ./internal

# Run tests
go test ./...

# Run a single test
go test ./internal/handler/... -run TestFolderCreate
```

The `go generate` directive in `internal/generate.go` runs `ogen --target ./api --clean --package api ../openapi.yaml`. The `internal/api/` package is fully generated — do not edit it manually.

## Architecture

### Backend (Go + SQLite)

Layered architecture: `handler → service → repository → SQLite`

```
main.go                  # Entry point: CLI flags, HTTP routing, server startup
internal/
  api/                   # Generated ogen server stubs — DO NOT EDIT
  auth/                  # HTTP Basic Auth middleware (htpasswd/bcrypt)
  handler/               # REST API endpoint handlers
  lda/                   # Local Delivery Agent: parse RFC 5322 from stdin → SQLite
  model/                 # Shared data types
  repository/            # SQLite queries and schema (migrations via PRAGMA user_version)
  sanitize/              # HTML sanitization (bluemonday) + cid: resolution
  service/               # Business logic, orchestration
web/static/              # Embedded web UI assets (HTML/CSS/TypeScript)
openapi.yaml             # REST API contract — source of truth for code generation
```

Web UI assets are embedded in the binary via `//go:embed`. The deployed artifact is a single `mymail` binary + one SQLite file.

### Four Operating Modes

| Flag | Mode | Purpose |
|------|------|---------|
| (none) | Server | HTTP REST API + embedded web UI on `127.0.0.1:8080` |
| `-init` | Init | Create SQLite DB with schema; seed built-in folders and optional initial identity |
| `-lda` | LDA | Read RFC 5322 from stdin, store in DB, apply filters; exit 0/1/75 |
| `-import` | Import | Batch import from mbox/Maildir with duplicate detection |

The database must be created by `-init` before any other mode will start.

### Database

- Pure-Go SQLite (`modernc.org/sqlite`) — no CGO.
- Single file at `<data>/mymail.sqlite`.
- Schema versioned via `PRAGMA user_version`; migrations applied on server startup.
- **Current schema version: 1** (see `spec/IMPLEMENTATION.md` for full DDL).
- FTS5 content table (`messages_fts`) kept in sync with `messages` via triggers.
- All timestamps stored as UTC RFC 3339 strings.
- `messages.references` column name collides with SQL reserved word — always quote it as `"references"` in queries.

### Key Built-in Folder IDs

| id | slug | Notes |
|----|------|-------|
| 1 | inbox | |
| 2 | sent | |
| 3 | drafts | raw IS NULL |
| 4 | trash | |
| 5 | scheduled | send_at IS NOT NULL |
| 6 | snoozed | snoozed_until IS NOT NULL |
| 7 | junk | |

User-created folders have `id >= 100`.

### REST API

Base path: `/api/v1`. Full contract in `openapi.yaml`. Error format: `{"error": "message"}`. Max request body: 32 MiB. Bulk endpoints cap at 1000 message IDs.

`GET /api/v1/health` is exempt from authentication.

### Web UI (TypeScript + Preact)

- TypeScript compiled with `tsc` only — no bundler.
- ES6 modules with import maps.
- Preact + JSX and Quill rich-text editor are vendored (no CDN).
- Hash-based routing (`/#/inbox`, `/#/compose`, `/#/settings/:tab`, etc.).
- `openapi-typescript` generates the TypeScript client from `openapi.yaml`.

### Authentication & CSRF

- HTTP Basic Auth via htpasswd file (bcrypt). Create with: `htpasswd -Bc htpasswd myuser`
- CSRF protection via Origin/Referer validation (`github.com/mikaelstaldal/go-server-common`).

### Outgoing Mail

Piped to `sendmail -t -oi` (no internal send queue). Path resolved at startup via `exec.LookPath`. 30-second timeout; non-zero exit → HTTP 500 with stderr.

### Scheduled Sends & Snooze

60-second polling goroutine (background, mutex-guarded). Deferred send threshold: `send_at > now + 60 seconds`. Snooze minimum: `until >= now + 60 seconds`.

## Testing

Each layer has its own test scope:

- **Repository:** use an in-memory SQLite DB (`modernc.org/sqlite` supports `file::memory:?cache=shared`). Run the full schema migration before each test. Test SQL queries and constraints directly.
- **Service:** unit-test business logic with a fake/stub repository interface. No SQLite required.
- **Handler:** integration-test HTTP endpoints by wiring the full `handler → service → repository` stack against an in-memory DB. Use `net/http/httptest`.
- **LDA:** test the parsing pipeline end-to-end by feeding raw RFC 5322 messages and asserting what lands in the DB.
- **FTS search input sanitization:** `spec/IMPLEMENTATION.md` requires a unit test verifying that `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are all treated as literals.

Place tests in `_test.go` files alongside the package under test. Use table-driven tests for endpoint/edge-case coverage.

## Important Implementation Notes

- **`references` quoting:** always use `"references"` (quoted) in SQL, never bare.
- **FTS search input:** escape `"` as `""` then wrap in outer `"..."` before passing to FTS5 MATCH.
- **Thread algorithm:** iterative Go loop (not recursive CTE) for transitive closure; capped at 1000 messages.
- **User folder ID generation:** explicitly compute `MAX(id)+1 WHERE id >= 100`; do not rely on AUTOINCREMENT.
- **Position append semantics:** `COALESCE(MAX(position), -1) + 1` within the same transaction as INSERT.
- **Reorder endpoints:** require *all* existing IDs; partial reorders are rejected.
- **Bulk operations:** all-or-nothing; 404 if any ID is missing.
- **HTML display:** render in `<iframe srcdoc="...">` with `sandbox` (no tokens); per-message opt-in for external images via `<meta>` CSP inside the iframe.
- **LDA exit codes:** 0 = success or duplicate, 1 = parse failure, 75 = transient error.
- **`send_failed` badge:** suppress in Trash (`folder_id = 4`) even when `send_failure_count > 0`.
