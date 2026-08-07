# MyMail

A self-hosted personal (single-user) email client with backend storage, REST API, and embedded web UI.

Frontend/web UI instructions — the TypeScript build and tests, the Preact conventions, and the CSS half of the MySuite contract — are in `web/AGENTS.md`, loaded automatically when working under `web/`.

## Specification

Adhere to the functional requirements in `spec/REQUIREMENTS.md` and the architecture in `spec/ARCHITECTURE.md`.
Update those when functionality is added or changed.

Some UI is **not MyMail's to define**. MyMail is one of three sibling apps — MyCal, MyMail, MyNotes — that must
look like one product, and the elements required to be identical across them are specified in the `mysuite`
repository (`../mysuite`, alongside this one; no remote yet, so it is referenced by path). Read
`../mysuite/AGENTS.md` before changing anything it covers, and make the change there first: **changing any of it
is a change in all three repositories**, however local the edit looks from here. Currently binding:
`spec/sidebar-footer.md`, the theme toggle and Settings button in the sidebar footer.

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

# Regenerate TypeScript API types from openapi.yaml
openapi-typescript openapi.yaml -o web/ts/api/types.ts

# Regenerate Go API server stubs from openapi.yaml (run after editing openapi.yaml)
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
  demo/                  # The demo dataset, for both -demo and the browser demo
  handler/               # REST API endpoint handlers
  lda/                   # Local Delivery Agent: parse RFC 5322 from stdin → SQLite
  model/                 # Shared data types
  repository/            # SQLite queries and schema (migrations via PRAGMA user_version)
  sanitize/              # HTML sanitization (bluemonday) + cid: resolution
  service/               # Business logic, orchestration
web/ts/                  # TypeScript sources (compiled to web/static/)
  demo/                  # The demo backend (worker code — see "Demo mode")
  demo-sw.ts             # The demo service worker's entry point
  demo-client.ts         # The page half of demo mode
web/static/              # Embedded web UI assets (HTML/CSS/compiled JS/favicons)
openapi.yaml             # REST API contract — source of truth for code generation
```

Web UI assets are embedded in the binary via `//go:embed`. The deployed artifact is a single `mymail` binary + one SQLite file.

### Operating Modes

| Flag | Mode | Purpose |
|------|------|---------|
| (none) | Server | HTTP REST API + embedded web UI on `127.0.0.1:8080` |
| `-init` | Init | Create SQLite DB with schema; seed built-in folders and optional initial identity |
| `-lda` | LDA | Read RFC 5322 from stdin, store in DB, apply filters; exit 0/1/75 |
| `-import` | Import | Batch import from mbox/Maildir with duplicate detection |
| `-demo` | Demo seed | Add the demo dataset (internal/demo) to an existing database |
| `-demo-server` | Demo server | Serve the web UI with no database and no REST API (see "Demo mode") |
| `-demo-bundle DIR` | Demo bundle | Write that same demo out as a static site, then exit |

The database must be created by `-init` before any other mode will start. The
two browser-demo modes are the exception — they never open one, and refuse
`-data` and `-lda-socket` rather than ignoring them.

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

### Demo mode

`-demo-server` and `-demo-bundle DIR` build the web UI with **no backend**: a
service worker (`web/ts/demo-sw.ts` + `web/ts/demo/`) intercepts `/api/v1` and
answers it from IndexedDB. `main.go` injects `window.__serverConfig={demo:true}`
(the same mechanism as the MyCal URL); `app.tsx` then waits for the worker to be
installed and in control before rendering, so the first request cannot escape it.

- **Intercepting at the network layer is the point**: the frontend is otherwise
  unchanged between demo and real, including the `<a href>` that downloads an
  attachment, which never goes through `api/client.ts`.
- **Parity with the Go server is the contract.** `web/ts/demo/` re-implements
  `internal/handler` + `internal/repository` + the scheduler; every function
  names the Go original it mirrors. When you change folder rules, threading,
  search, draft semantics, address validation, or filter evaluation on the
  server, change it there too. The accepted divergences are listed in
  spec/REQUIREMENTS.md § Demo Mode — don't add more silently.
- **Not localStorage**: a service worker cannot reach it (it is synchronous and
  absent from worker scopes), so the store is IndexedDB. Attachment bytes live
  in their own object store so editing a draft does not rewrite them.
- These sources are **worker code**: excluded from `web/ts/tsconfig.json` and
  built by `web/ts/demo/tsconfig.json` against the WebWorker lib. They are
  classic scripts sharing one global scope via `importScripts`, so they use no
  `import`/`export` — adding one silently turns a file into a module and its
  declarations vanish from the shared scope.
- **The message-body iframe is the one thing the worker cannot serve.** A
  sandboxed iframe has an opaque origin, and a browser does not consult a
  service worker for a navigation out of one, so `<iframe src="api/v1/…/body">`
  escapes to the network. `BodyIframe` therefore fetches the document (a
  subresource request, which the worker does see) and passes it as `srcdoc` in
  demo mode only; the demo's response repeats its CSP in a `<meta>` because
  response headers do not survive that.
- **There is no scheduler goroutine.** A worker is stopped whenever it is idle,
  so deferred sends, snooze expiry, and auto-reply delivery all run at the start
  of each request instead (`runScheduler` in `demo/api.ts`).
- The seed content is not duplicated in JavaScript: `internal/demo/bundle.go`
  runs the real `-demo` seeding against an in-memory database and exports the
  result as `demo-data.json`.
- **Sending produces a reply** (`web/ts/demo/reply.ts`) — demo-only behaviour,
  with no counterpart on the server. Which reply is a pure function of the
  outgoing message, so it is testable; the delay is `AUTO_REPLY_DELAY_MS`.

## Testing

Each layer has its own test scope:

- **Repository:** use an in-memory SQLite DB (`modernc.org/sqlite` supports `file::memory:?cache=shared`). Run the full schema migration before each test. Test SQL queries and constraints directly.
- **Service:** unit-test business logic with a fake/stub repository interface. No SQLite required.
- **Handler:** integration-test HTTP endpoints by wiring the full `handler → service → repository` stack against an in-memory DB. Use `net/http/httptest`.
- **LDA:** test the parsing pipeline end-to-end by feeding raw RFC 5322 messages and asserting what lands in the DB.
- **Assertions:** use `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require` for all test assertions.
- **FTS search input sanitization:** a unit test verifying that `"`, non-ASCII characters, and FTS5 operator keywords (`AND`, `OR`, `NOT`, `NEAR`) are all treated as literals.

Place tests in `_test.go` files alongside the package under test. Use table-driven tests for endpoint/edge-case coverage.

## E2E Tests

Playwright end-to-end tests live in `e2e/`. **Run them with `./build.sh && ./test-e2e.sh`** — that
script is what CI runs, and it initialises a fresh database, starts the server itself, checks the
server is actually serving the assets on disk, and tears both down afterwards. It takes the same
arguments as `playwright test`, so `./test-e2e.sh tests/sidebar-footer.spec.ts -g "focus"` works.

`tests/sidebar-footer.spec.ts` is this repo's whole half of the cross-repo sidebar-footer contract
(see § Specification and `web/AGENTS.md`). Nothing else in this repo checks any of it, and the suite
gates publication in CI — it runs after `./build.sh` and before the demo bundle and the release.

Prefer it over starting a server by hand. The three things it exists to prevent are easy to hit and
none of them announces itself:

- **A stale server.** `web/static/` is baked into the binary with `//go:embed`, so a running
  `./mymail` keeps serving the CSS and JS it started with — `./build.sh` alone changes nothing it
  serves. The suite then passes or fails against assets that are not the ones you edited. When a
  measurement disagrees with the source, check this first:
  ```bash
  curl -s http://localhost:8090/app.css | md5sum   # must match
  md5sum web/static/app.css
  ```
- **A stale database, or someone else's server on the port.** Reusing a data directory is how an
  "empty" run silently becomes a run against whatever the last one left behind; and if something
  already holds 8090, a hand-started server exits on bind failure while the tests run happily
  against the squatter.
- **A server that never started.** MyMail will not serve from an empty data directory — `-init` has
  to have run first, and `-init` itself requires `-identity-address`. The script does both.

If you do start one by hand, `-public-url` must match the test baseURL origin
(`http://localhost:8090`), or CSRF rejects mutating requests with 403. Not *every* mutating request:
`go-server-common`'s `csrf.Middleware` allows a non-GET carrying neither `Origin` nor `Referer`
("native client, allow"), so curl and Playwright's `request` fixture pass a mismatched `-public-url`
while anything originating from the page does not. Measured against a mismatched value: no
Origin/Referer → 201, page Origin → 403, Referer only → 403. It binds on this suite because the
fixtures create folders with `fetch` from inside the page, and it would bite the same way for a test
that clicked Save.

You will also need `-sendmail` pointing at something that exists, since the path is resolved at
startup.

*Important:* interactively, use the `playwright-test` command from `e2e/` and nothing else —
do not invent variants. `test-e2e.sh` falls back to `./node_modules/.bin/playwright test` when
that wrapper is absent, which is the case in CI; that fallback is sanctioned and is the only one.

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
