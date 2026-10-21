# MyMail — Architecture

This document describes system structure, technical decisions, and trust boundaries.
User-visible behavior is specified in [REQUIREMENTS.md](REQUIREMENTS.md), the HTTP contract in
[openapi.yaml](../openapi.yaml), and the current database DDL in [schema.sql](schema.sql).

## System structure

MyMail is a Go server with an embedded browser UI and a single SQLite database. The host mail transfer
agent (MTA) handles mail transport; MyMail handles storage, application logic, and presentation.
Web assets are embedded with `//go:embed`, so deployment needs one server binary and one database file.

The backend follows `handler → service → repository → SQLite`: handlers adapt HTTP requests,
services coordinate business logic, and repositories own database access. OpenAPI is the source
for generated Go server stubs and TypeScript API types.

```text
main.go                     Server startup, routing, and batch-mode dispatch
cmd/
  cli/                      Script-oriented REST client
  lda/                      Thin UNIX-socket delivery client
  mcp/                      Stdio MCP-to-REST adapter
  token/                    Token-management REST client
internal/
  api/                      Generated ogen server stubs
  attachmentname/           Shared attachment filename normalization
  clihttp/                  Shared client transport and URL handling
  demo/                     Shared demo dataset and browser export
  handler/                  REST handlers
  lda/                      MIME ingestion and delivery socket server
  model/                    Shared data types
  phishing/                 Read-time warning computation
  repository/               SQLite queries and migrations
  sanitize/                 HTML sanitization and bounded normalization
  senderauth/               Interpretation of trusted MTA evidence
  service/                  Application orchestration
  token/                    Bearer authorization, token management, and device grants
web/
  ts/                       Preact UI and browser-demo sources
  static/                   Embedded assets and compiled JavaScript
```

## Mail transport and processing

Incoming mail arrives from the host MTA rather than through IMAP or POP3. The thin `mymail-lda`
client forwards raw RFC 5322 mail over a UNIX socket to the running server. Keeping SQLite, web
assets, and HTTP code out of this client reduces memory use when the MTA invokes deliveries
concurrently. The full server binary also supports direct LDA and batch-import processing.

The ingestion pipeline parses MIME, sanitizes HTML, resolves CID images, evaluates trusted MTA
headers and delivery filters, and writes through the repository. MIME traversal shares resource
limits across nested branches, bounding work before database writes. Spam classification uses
upstream header evidence rather than a built-in classifier. Delivery-time filter evaluation keeps
arrival counts and notifications consistent with the persisted destination.

Outgoing mail is piped to `sendmail -t -oi`; the MTA owns transport queuing, retries, TLS, and DKIM.
Deferred sends and snooze expiry are driven by a polling goroutine over indexed database state,
so timers do not need to survive process restarts.

## Persistence

Pure-Go SQLite (`modernc.org/sqlite`) is the only server-side store. Messages, attachment BLOBs,
identities, contacts, filters, and settings share one database, avoiding an external file store.
Raw RFC 5322 BLOBs retain the original mail alongside parsed fields and sanitized bodies.
FTS5 indexes message content with triggers keeping the index synchronized with message rows.

Schema migrations are versioned with `PRAGMA user_version`. Normal startup applies migrations;
the standalone migration path can also vacuum after committed migrations without initializing
server subsystems. Migration history belongs in `internal/repository/db.go`; `schema.sql` records
the resulting schema rather than the sequence of upgrades.

Message identity combines Message-ID, a SHA-256 content fingerprint, and immutable outgoing
provenance. Fingerprints cover stable headers and raw bodies so delivery deduplication can
retain distinct messages with colliding sender-controlled IDs. Duplicate checks and insertion
share a transaction writer lock; legacy rows without fingerprints use raw-mail comparison.

Durable correspondence history is stored separately from contacts and messages. SQLite triggers
maintain it in the sending transaction. Generated normalized-domain and Unicode-skeleton columns
support indexed warning lookups without scanning all sent mail. These expressions require
application-registered SQLite functions, including during external SQL restoration; database-file
backups do not require those functions to be registered by the copying tool.

## HTTP and authorization boundaries

An outer `go-server-common/hostguard.Policy` validates Host before routing or authentication,
ignores forwarded-host headers, and supplies canonical origins to the CSRF middleware.
HTTP Basic authentication uses an htpasswd file with bcrypt. TLS termination and rate limiting
belong to the deployment's reverse proxy.

Folder-scoped bearer tokens have a separate authorization path from full-access token management.
Only secret hashes are persisted. Bearer reads validate the token, grants, and current resource
folder in the same SQLite read snapshot used to assemble the response. Database reads finish
before encoding; the snapshot is released at the first response write, so slow downloads cannot
pin a WAL snapshot. Lazy database-backed response readers would violate this design.
Authorized token writes check secret, expiry, and folder membership in the SQL update itself.

Device authorization uses `go-server-common/devicegrant` and persisted authorization records.
Consent and polling are separate: polling creates the token and consumes the approved record in
one transaction. Public device endpoints retain Host, CSRF, and body-size protection; approval
uses the full-access path. The shared protocol is specified in
[MySuite device login](../../mysuite/spec/device-login.md).

API discovery embeds the same `openapi.yaml` used for generation. Discovery runs behind Host
validation and before authentication; service-description headers are attached by outer middleware
so API errors also carry them. The shared contract is
[MySuite API discovery](../../mysuite/spec/api-discovery.md).

## Untrusted content and warning analysis

Inbound and outgoing HTML policies share a sanitizer allowlist. CID resolution uses tokenizer
passes, and tree-building passes apply `sanitize.BoundFormatting` first to bound HTML amplification.
Its tree-builder model depends on the sanitizer's allowed element set; expanding that set requires
reviewing the bound. CID resolution returns a bounded set of image IDs actually embedded in sanitized
HTML; ingestion uses that set to distinguish inline images from attachments rather than scanning raw HTML. The
browser renders message HTML in a sandboxed iframe with a restrictive CSP.

`internal/senderauth` interprets headers under a configured checker-host trust boundary and stores
its result separately from raw mail. A generation counter invalidates old evidence when that trust
configuration changes, avoiding mailbox-wide rewrites on settings updates.

`internal/phishing` computes warnings at read time from stored evidence, body links, attachment
metadata, identities, explicit contacts, and durable correspondence. Message data and warning
context share a read snapshot, including for full-access requests. Warning analysis uses indexed
correspondence candidates and pinned Unicode confusables; normalization changes require rebuilding
persisted generated values as well as regenerating browser tables. Shared attachment-name
normalization keeps ingestion, risk analysis, and download headers consistent, including legacy rows.
If the document parser rejects excessive nesting, a streaming tokenizer still extracts and rewrites
links so deeply nested markup cannot suppress all body warnings.

Link rewriting operates on response copies of message bodies. Confirmation routes identify a
stored message and anchor index, then resolve the destination through the authorized message API;
a caller-supplied destination does not bypass that boundary. Warning presentation belongs to the
UI, while signal computation remains on the server.

## Browser UI and external clients

The UI uses TypeScript, Preact/JSX, Quill, Lucide SVG icons, and plain CSS. `tsc` emits ES modules
loaded through import maps without a bundler. Dependencies are vendored and assets are served
locally. Thread views fetch summaries first and individual bodies on expansion; this N+1 pattern
is accepted because personal mail threads are short. Shared theme state is managed by `util/theme.ts`; shared
visual contracts live in the
sibling MySuite repository as described in `web/AGENTS.md`.

`mymail-cli`, `mymail-token`, and `mymail-mcp` are clients of the running HTTP server, with no
direct database access. `internal/clihttp` shares deployment-prefix handling and transport policy:
remote connections require HTTPS, and redirects and environment proxies are disabled to avoid
forwarding credentials to another endpoint. MCP uses `github.com/modelcontextprotocol/go-sdk`
and delegates authorization to the same bearer middleware as `mymail-cli`. Its shared contract
is [MySuite MCP server](../../mysuite/spec/mcp-server.md).

Calendar integration calls MyCal directly from the browser under a shared origin and auth realm.
The Go server only injects configuration; MyMail has no URL-fetching proxy. Calendar URLs from
mail remain untrusted, and MyCal owns SSRF protection through address validation, dial-time DNS
checks, redirect revalidation, and bounded response reads. The browser's `util/icslinks.ts` selects
calendar links; it cannot enforce SSRF protection. Changes to MyCal's fetch guards must account for
this cross-repository dependency on URLs supplied by untrusted mail.

## Browser demo

The demo serves the same UI without a database or REST backend. A service worker intercepts
`/api/v1` and implements the handler/repository behavior in TypeScript over IndexedDB. Network-layer
interception also covers downloads that bypass the frontend API client. Attachment bytes occupy
a separate object store, avoiding rewrites when a draft changes.

The page waits for service-worker control before rendering. Sandboxed body-iframe navigations
cannot use that worker, so demo bodies are fetched by the page and passed as `srcdoc`, with CSP
repeated in a meta element. Warning analysis runs in the worker, delegating inert HTML parsing
and link rewriting to a controlled page because workers have no DOM.

Worker lifetime prevents a persistent scheduler goroutine, so scheduled work runs at the start
of requests. `internal/demo` defines the dataset once, seeds SQLite, and exports the same rows as
`demo-data.json`. Server changes must be mirrored in the worker; accepted behavioral divergences
are recorded in [REQUIREMENTS.md](REQUIREMENTS.md#demo-mode).

## Verification structure

Go tests cover repository, service, handler, and ingestion layers. Frontend unit tests run against
compiled JavaScript using Node's test runner and vendored jsdom. Playwright end-to-end tests live
in `e2e/`, outside the browser-independent `build.sh`; `test-e2e.sh` provisions their server and
database. CI runs them after the build and before publication. Execution details and shared UI
contract coverage are documented in `AGENTS.md` and `e2e/AGENTS.md`.
