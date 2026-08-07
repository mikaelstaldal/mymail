# MyMail

A self-hosted personal (single-user) email client with backend storage, REST API, and embedded web UI.

## Specification

Adhere to the functional requirements in `spec/REQUIREMENTS.md` and the architecture in `spec/ARCHITECTURE.md`.
Update those when functionality is added or changed.

Some UI is **not MyMail's to define**. MyMail is one of three sibling apps — MyCal, MyMail, MyNotes — that must
look like one product, and the elements required to be identical across them are specified in the `mysuite`
repository (`../mysuite`, alongside this one; no remote yet, so it is referenced by path). Read
`../mysuite/AGENTS.md` before changing anything it covers, and make the change there first: **changing any of it
is a change in all three repositories**, however local the edit looks from here. Currently binding:
`spec/sidebar-footer.md`, the theme toggle and Settings button in the sidebar footer.

### Edits that silently break that contract

The rule for `.sidebar-theme-toggle, .sidebar-settings-link` in `web/static/app.css` looks like ordinary CSS with
a verbose comment. Every declaration in it is load-bearing, nothing in this repo tests any of it, and MyMail has
no e2e suite — so each of the following is a plausible tidy-up that breaks the suite's consistency with no test
failing anywhere:

- **Normalising `0.80rem` to `0.8rem`.** The trailing zero is the convention that makes one grep find the value in
  all three repos. The rest of this file uses `0.8rem`, so the canonical spelling looks like the odd one out — and
  any formatter would "fix" it unprompted.
- **Deleting `flex-shrink: 0`, `text-align: center`, `font-weight: 400` or `font-style: normal` as redundant.**
  They are no-ops *today*, pinned because the two controls reach the same values by different routes: the toggle
  is a `<button>` taking them from the UA stylesheet, Settings is an `<a>` inheriting them from `body`.
- **Adding `font-weight`, `font-style` or `font` to a base `button` rule.** That moves the toggle and not the
  anchor — a divergence inside one app, between two controls 6px apart.
- **Restoring `outline: none` on their `:focus-visible`,** or adding a `@media (forced-colors: active)` block
  containing `outline: revert`. Both silently undo a WCAG 1.4.11 fix; the second looks like it is protecting it.
- **Removing `--focus-ring` as unused.** These two controls no longer use it, but many other rules still do.
- **Re-adding `class="folder-icon"` to the two footer icons.** It dims them to `opacity: 0.85`; the contract wants
  full opacity.
- **Changing `.sidebar-footer`'s padding.** It is not spacing — it *is* the buttons' (8, 8) position on screen,
  and below 4px it starts cropping the focus outline.
- **Folding the pair into a generic icon-button class**, or renaming either selector.

Also: `web/static/` is embedded with `//go:embed`, so **a running server keeps serving the CSS it started with**.
Rebuilding does not change what an already-running server serves, and a stale measurement looks exactly like a
passing one. See `../mysuite/spec/measurement-protocol.md` before measuring anything here.

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

# Compile the demo-mode service worker (separate project — worker code, not DOM code)
tsc --project web/ts/demo/tsconfig.json

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
node --test web/ts/quotetext.test.mjs web/ts/wrap.test.mjs web/ts/address.test.mjs web/ts/signature.test.mjs web/ts/confirm.test.mjs web/ts/demo.test.mjs

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

`wrap.test.mjs` needs no DOM — `web/ts/util/wrap.ts` is pure string handling —
so it skips the jsdom install entirely. It is deliberately the whole of the
wrapping logic: `ComposeForm` only turns its edits into a Quill delta and
decides which breaks are the editor's own. Anything about *where* a line breaks
belongs in `wrap.ts`, where it can be tested; the Quill wiring cannot be.

`address.test.mjs` needs no DOM either. `web/ts/util/address.ts` decides whether
the Send button is offered at all — in ComposeForm and on a draft in
MessageDetail — by answering the question the server answers with a 400: is
there at least one recipient, and is every address list well-formed. It is a
pre-flight check, never the authority; the server's 400 still surfaces inline.
Its parser is a third copy of the same rules (`service.ParseAddressList`,
`demo/text.ts`), unavoidably so — the demo backend has no imports to share with
— and the three must move together.

`signature.test.mjs` needs no DOM either. `web/ts/util/signature.ts` is the
arithmetic behind the signature mark — ops in, a span or a set of ops out — kept
apart from ComposeForm's Quill wiring so it can be exercised without a Quill.
The ops in that test are shapes the vendored Quill actually produces, so a
change in what Quill does to a `<br>`, an `<hr>` or a split block shows up as a
test that no longer describes reality rather than as a silently wrong span.

`confirm.test.mjs` needs no DOM either. `web/ts/util/confirm.ts` is the store
behind every confirmation the UI asks for, holding the promise a caller is
awaiting; the dialog that renders it (`components/ConfirmDialog.tsx`) is not
reachable from here. What the test pins is the two ways an answer could go
missing — a question superseded before it was answered resolves `false` rather
than stranding its caller's `await`, and answering a stale id is ignored instead
of resolving the question that replaced it. A stranded promise is a button that
silently stops working, with nothing in the console to say so.

`demo.test.mjs` needs no DOM either, but it does something the other four do not:
the demo backend is a set of classic worker scripts sharing one global scope, so
the test evaluates them into *this* realm with `vm.runInThisContext`, exactly as
`importScripts` would, and reads the declarations back out. `store.js` is the
one file left out — it is nothing but IndexedDB — and a stub for its five entry
points is evaluated in its place. That is what makes `api.js` testable here:
everything above the store is real code answering real `Request` objects, so the
parity rules (which folders refuse a move, what deleting does where, how threads
close over References) are asserted against the code that implements them rather
than a paraphrase of it.

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
- **Compose soft-break mark:** the editor wraps at the `wrapColumn` preference (default 80, `0` off) and marks its own breaks with a Quill block format rendered as `class="ql-softwrap-y"`, so a paragraph can be re-filled rather than only broken further. Two things keep it working: the sanitiser must never allow `class` (or the mark ships to recipients), and the format's attribute name must stay equal to the class prefix (or Quill's clipboard cannot map the class back when a draft is reopened). Enter inside a wrapped paragraph must clear the mark explicitly — splitting a paragraph copies it to both halves, and a marked break is one the wrapper may dissolve.
- **Compose signature mark:** the identity signature is found by a second block format, `class="ql-signature-y"` (`web/ts/util/signature.ts`), never by searching the editor's HTML for what `signatureToHtml` produced — Quill does not give that string back, and the swap that relied on it left the old identity's signature in the message. It carries the same two obligations as the soft-break mark, plus two of its own. A wrap break made inside the signature must carry the mark, or the half above it stops counting as signature and a swap leaves it behind (Quill puts the block format only on the half inheriting the old newline, so `autoWrapEditor` has to add it back). Enter at the end of the signature must clear it, or a paragraph written below the signature is deleted by the next swap.
