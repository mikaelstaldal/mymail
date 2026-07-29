# MyMail

A self-hosted personal (single-user) email client with backend storage, REST API, and embedded web UI.

## Specification

Adhere to the functional requirements in `spec/REQUIREMENTS.md` and the architecture in `spec/ARCHITECTURE.md`.
Update those when functionality is added or changed.

## Build & Development Commands

**Prerequisites:** `go` must be on PATH. `build.sh` additionally needs `tsc` (TypeScript compiler), `openapi-typescript`, `ogen` (Go code generator), `golangci-lint`, and `node` (for the frontend tests). No package manager is required — jsdom is unpacked from a committed tarball with `tar`.

```bash
# Full build: TypeScript compilation + frontend tests + Go binary + tests + lint
bash build.sh

# Build Go binary only (requires web/static/*.js already compiled)
go build -tags netgo .

# Build thin LDA client binary (no SQLite / web assets — minimal memory footprint)
go build -tags netgo ./cmd/lda/

# Compile TypeScript → web/static/*.js (sources in web/ts/)
tsc --project web/ts/tsconfig.json

# Type-check TypeScript without emitting files
tsc --project web/ts/tsconfig.json --noEmit

# Regenerate TypeScript API types from openapi.yaml
openapi-typescript openapi.yaml -o web/ts/api/types.ts

# Regenerate Go API server stubs from openapi.yaml (run after editing openapi.yaml)
go generate ./internal

# Run tests
go test ./...

# Run a single test
go test ./internal/handler/... -run TestFolderCreate

# Run the frontend tests (needs web/static/*.js compiled first; unpack.sh is a
# no-op once web/ts/vendor/test/node_modules/ exists)
web/ts/vendor/test/unpack.sh
node --test web/ts/quotetext.test.mjs

# Run a single frontend test
node --test --test-name-pattern 'depth cap' web/ts/quotetext.test.mjs
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
web/ts/                  # TypeScript sources (compiled to web/static/)
web/static/              # Embedded web UI assets (HTML/CSS/compiled JS/favicons)
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
- **Current schema version: 4** (see `internal/repository/db.go` for full DDL).
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

### Web UI (TypeScript + Preact)

- TypeScript sources in `web/ts/`; compiled to `web/static/` by `tsc` — no bundler.
- ES6 modules with import maps.
- Preact + JSX and Quill rich-text editor are vendored (no CDN) in `web/static/vendor/`.
- Hash-based routing (`/#/inbox`, `/#/compose`, `/#/settings/:tab`, etc.).
- `openapi-typescript` generates `web/ts/api/types.ts` from `openapi.yaml`.
- Unit-tested with `node --test` against the compiled `web/static/` output; see **Testing → Frontend**.

### Authentication & CSRF

- HTTP Basic Auth via htpasswd file (bcrypt). Create with: `htpasswd -Bc htpasswd myuser`
- CSRF protection via Origin/Referer validation (`github.com/mikaelstaldal/go-server-common`).

### Outgoing Mail

Piped to `sendmail -t -oi` (no internal send queue). Path resolved at startup via `exec.LookPath`. 30-second timeout; non-zero exit → HTTP 500 with stderr.

### HTML Sanitization — two policies, one allowlist

`internal/sanitize` exposes **two** policies built by the same `newPolicy`
helper, so they differ only in their allowlists, never in how they validate:

- `HTML()` / `NewEmailPolicy()` — **inbound**, attacker-controlled. Used by
  `internal/lda/parse.go` only.
- `OutgoingHTML()` / `NewOutgoingPolicy()` — mail **we** send. Used by
  `handler/send_draft.go` (×2) and `service/send.go`.

`outgoingOnlyElements` / `outgoingOnlyCSS` are **empty on purpose**, so the two
are currently equivalent. The invariant to protect is: **MyMail must render
everything MyMail will send.** Otherwise a message to another MyMail instance —
or to yourself — arrives stripped of styling this same instance produced.
`TestSentHTMLSurvivesBeingReceived` pins `HTML(OutgoingHTML(x)) ==
OutgoingHTML(x)` and fails the moment either list gains an entry. That failure is
the point: adding one is a decision to accept that degradation.

Note there is often no lossless fallback for a dropped property. A one-sided
border rewritten as `border:none;border-top:…` degrades to an **invisible** rule,
not a plain one — so an allowlist asymmetry is rarely merely cosmetic.

All three send sites must use `OutgoingHTML`. **If one reverts to `HTML`, the
stricter pass silently wins**, since the send paths sanitize more than once.

Adding a CSS property does *not* reintroduce `url()`: bluemonday binds
`MatchingHandler` (our `cssValueAllowed`) to every property uniformly, replacing
per-property defaults. The residual CSS risk is layout/positioning spoofing,
which is why `position`, `z-index`, `display`, `opacity`, `visibility`, `float`
and `transform` stay out. `<style>` must never be allowed: bluemonday does not
validate stylesheet text, so `@import url(…)` would bypass the value handler
entirely.

`TestOutgoingKeepsEverySecurityGate`, `TestOutgoingIsSupersetOfInbound` and
`TestSentHTMLSurvivesBeingReceived` are the regression gates — extend them when
the allowlist grows.

### Scheduled Sends & Snooze

60-second polling goroutine (background, mutex-guarded). Deferred send threshold: `send_at > now + 60 seconds`. Snooze minimum: `until >= now + 60 seconds`.

## Testing

Each layer has its own test scope:

- **Repository:** use an in-memory SQLite DB (`modernc.org/sqlite` supports `file::memory:?cache=shared`). Run the full schema migration before each test. Test SQL queries and constraints directly.
- **Service:** unit-test business logic with a fake/stub repository interface. No SQLite required.
- **Handler:** integration-test HTTP endpoints by wiring the full `handler → service → repository` stack against an in-memory DB. Use `net/http/httptest`.
- **LDA:** test the parsing pipeline end-to-end by feeding raw RFC 5322 messages and asserting what lands in the DB.
- **Assertions:** use `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require` for all test assertions.
- **FTS search input sanitization:** a unit test verifying that `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are all treated as literals.

Place tests in `_test.go` files alongside the package under test. Use table-driven tests for endpoint/edge-case coverage.

### Frontend (TypeScript)

Plain `node --test` with `node:assert/strict` — no test framework, no package
manager. Tests are `web/ts/*.test.mjs` and import the **compiled** output from
`web/static/`, not the `.ts` sources, so `tsc` must have run first (`build.sh`
orders it that way).

DOM-dependent code gets a DOM from jsdom, imported via
`web/ts/vendor/test/jsdom.js` and installed on `globalThis` *before* the module
under test is imported — a module reading `DOMParser`/`Node` at load time would
otherwise see nothing. Use a dynamic `await import()` for that ordering.

jsdom is vendored as one deterministic `jsdom-node_modules.tar.gz` (it can't be
bundled: it reads data files from its own package dir at runtime).
`web/ts/vendor/test/unpack.sh` extracts it with `tar` alone and is idempotent;
`web/ts/vendor/rebuild.sh` regenerates the tarball and is maintainer-only (it is
the only thing here that needs npm).

Only logic reachable from a plain function is covered this way — component
rendering and Quill interaction are not tested. That means a function worth
testing generally belongs in `web/ts/util/`, exported, rather than kept private
inside a `.tsx` view (this is why `quoteHtmlToText` lives in
`web/ts/util/quotetext.ts` and not in `ComposeForm.tsx`).

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
