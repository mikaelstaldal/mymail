# MyMail — Architecture

## Backend

Both API clients use Go flag parsing for global `-help`/`-h` and command flags. They handle
`flag.ErrHelp` by printing usage to stdout and returning successfully before credentials or HTTP requests.

### Technology Stack

- Go
- SQLite

### Project Structure

Layered architecture: `handler → service → repository → SQLite`

```
mymail/
├── main.go                   # Entry point, CLI flags, routing, startup
├── cmd/
│   ├── cli/                  # Token-only REST API client binary
│   ├── lda/                  # Thin LDA client binary (forwards to server via UNIX socket)
│   └── token/                # Basic-authenticated API token management binary
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
├── web/                      # Embedded frontend assets
├── e2e/                      # Playwright end-to-end tests (own package.json; not part of build.sh)
└── test-e2e.sh               # Builds nothing; starts a fresh server and runs e2e/
```

Web UI assets embedded in the binary via `//go:embed`.

The outer HTTP handler uses `go-server-common/hostguard.Policy` to validate the request Host against the configured public URL and local listener authorities
before API, UI, authentication, and CSRF routing. It ignores forwarded-host headers. This prevents a browser request
using a DNS-rebound foreign hostname from reaching an unauthenticated loopback deployment.
The same policy supplies canonical browser origins to the CSRF middleware.

`e2e/` is deliberately outside `build.sh`: that script must keep working without a
browser toolchain, so Playwright is installed by the CI workflow instead. The
workflow step runs after the build and before publication, and **has done since
2026-08-08**; it gates publication rather than preventing a breaking commit, since
the trigger is the push itself. See `AGENTS.md` § E2E Tests.


## Message storage

The `-migrate` command dispatches before server initialization. It checks that the database exists,
opens SQLite without automatic migration, compares `PRAGMA user_version` before and after
`repository.InitSchema`, and runs `VACUUM` if the version advanced. Vacuum runs outside the committed
migration transactions. The database is closed on success or failure; any error yields a non-zero exit,
with vacuum failures explicitly reporting that migrations already succeeded. This logic is confined to
the batch command: `repository.OpenDB` and normal server startup never vacuum after migration.
The command performs no application seeding or server, sendmail, scheduler, or socket setup.

Schema version 12 stores immutable sent/received provenance in `messages.outgoing`.
Its original unique key was `(message_id, outgoing)`, allowing a self-sent message to have both copies;
version 14 replaces it with `(message_id, content_fingerprint, outgoing)` to preserve Message-ID collisions.
LDA duplicate checks only consider received copies, independent of their current folder.
Send paths set outgoing provenance on successful immediate and scheduled sends.
The migration rebuilds messages and its dependent tables, preserving IDs, attachments, references,
FTS contents and autoincrement sequence high-water marks. Import duplicate checks remain database-wide.

Schema version 13 changes no DDL: it resets `messages.date` to `created_at` wherever the stored value is not a
four-digit-year RFC 3339 string. Ingest (`lda.storableDate`, also applied to import
fallbacks: mbx internal date, `From ` line, file mtime) now discards dates whose UTC year is outside 1–9999, which
were previously stored as `10000-…` or `-0001-…` and failed `time.Parse` on every read that touched the row.

Schema v14 permits colliding Message-IDs, retaining outgoing provenance.
New received/imported rows store an indexed SHA-256 `content_fingerprint` of stable headers and raw bodies.
LDA and import probe that index under a transaction writer lock before insertion. Only legacy rows have a NULL
fingerprint and require comparing raw mail; at most two legacy rows can share one ID due to the prior constraint.
Transport-only header changes do not defeat deduplication. Distinct mailing-list and direct copies are retained
when the list changes fingerprinted headers or the body.
Thread backward lookup excludes Junk/Trash copies
of an ID when an ordinary copy exists; distinct ordinary copies remain visible because IDs are sender-controlled.

## Web UI

### Technology Stack

- TypeScript compiled with `tsc` only (no bundler).
- ES6 modules with import maps.
- Preact + JSX for reactive components (vendored, no CDN dependency).
- Quill rich-text editor (vendored, no CDN dependency).
- Lucide icons, rendered inline as SVG by `components/Icon.tsx` (vendored, no
  CDN dependency). The vendored bundle carries only the icons the UI names —
  see the `ICONS` list in `web/ts/third_party/gen-lucide.mjs`. The UI uses no emoji
  or other Unicode glyphs as icons.
- Plain CSS for styling. The palette, type scale and control styling are
  MyCal's, so the two apps read as one product; the theme is selected by
  `data-theme` on `<html>`, the same attribute MyCal and MyNotes switch on.
  `util/theme.ts` owns it — read, persist, apply, and a change event — because
  two controls expose the same preference (the sidebar button and the
  Preferences switch) and either may be mounted while the other is used.
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

During MIME parsing, CID image resolution in `internal/sanitize` returns the IDs of images actually embedded in the sanitized HTML. The LDA uses that bounded set to distinguish inline images from stored attachments; it does not scan raw HTML text for CID substrings. CID resolution rewrites `cid:` sources and sanitizes with tokenizer passes only; the one `html.Parse` that normalizes the body sees sanitized markup after `sanitize.BoundFormatting` has capped its active formatting elements, and a size limit on the serialized result falls back to removing formatting. Any later parse of a stored body (`internal/phishing`) applies `BoundFormatting` first, and so does the soft-wrap join on the outgoing path, after a sanitizer pass that also keeps `class` and comments. That function's model of the tree builder is exact only for the sanitizer's element set — no foreign content, raw-text elements, `<select>`, or markers other than cells and captions — so widening the allowlist with any of those means revisiting it.
The MIME traversal carries shared depth, part-count, cumulative-read, and decoded-byte limits across nested multipart and alternative branches. Limit violations abort parsing before any database write and return the LDA's permanent parse error.

### Script-Oriented REST API Client

`cmd/cli` builds `mymail-cli` as a separate static Go binary using only the standard library at runtime. It exposes exactly the API token routes: scoped folder and message reads, attachment download, and mark-read. It never accepts Basic credentials or an API token on the command line. An optional token comes from a file or stdin. `MYMAIL_TOKEN_FILE` supplies the default file path; an explicit file or stdin option overrides it. Without either token option or a nonempty environment default, it sends no Authorization header and does not read stdin. Response bytes go to stdout, making JSON and downloads usable in pipelines. `messages text ID` decodes the existing message-detail response and writes only `body_text`, without adding a newline. HTTP is restricted to literal loopback addresses; remote servers require HTTPS. Redirects and environment proxies are disabled so a bearer secret cannot be forwarded to a different endpoint.

`cmd/mcp` builds `mymail-mcp`, a Model Context Protocol server on stdio using `github.com/modelcontextprotocol/go-sdk`. Like `mymail-cli` it is an HTTP client of a running server through `internal/clihttp`, and it makes no authorization decision of its own: the bearer middleware decides what a token reads, and answers a message in another folder and a missing one alike with 403. Reads are resources and resource templates under `mymail://`; folder search is the `search_messages` tool and mark-read the `mark_message_read` tool. `mymail://messages/{id}/text` decodes the message-detail response and returns only `body_text`, as `messages text` does. Resource URIs are parsed in `cmd/mcp` rather than by the SDK's template matcher, which cannot extract reordered or repeated query parameters. Every response body read is capped at 1 MiB. The contract is `../mysuite/spec/mcp-server.md`.

`cmd/token` builds `mymail-token`, a separate static Go binary for the full-access token management API. It reads one Basic `username:password` line from a file or stdin, or prompts for a password with terminal echo disabled, and can omit credentials when Basic auth is disabled. It creates tokens from a name, lifetime, and folder IDs, and revokes tokens by slug. The one-time secret goes to stdout; its revocation slug goes to stderr. Both clients share URL validation and transport policy through `internal/clihttp`, and use `MYMAIL_URL` as the default base URL when set. API resource paths are appended beneath the supplied deployment path prefix, preserving URL escaping.

### Header-Based Spam Detection
Reads spam verdicts from headers set by the MTA pipeline (SpamAssassin, Rspamd, etc.). No built-in classifier.

### Thread View N+1 Fetch Pattern
`/thread` returns summaries; expanding an entry requires a separate fetch per message. Acceptable because personal email threads are short.

### Bulk Operation Atomicity
Bulk endpoints return 404 if any ID is missing — all-or-nothing, no partial success.

### Web UI: No Bundler
TypeScript compiled with `tsc` only. ES6 modules + import maps. Preact, Quill and Lucide vendored. All assets embedded in the binary.

### Calendar Import Talks to MyCal from the Browser
"Import to Calendar" — for an `.ics` attachment and for a calendar link in an HTML body — POSTs to
`{mycal}/api/v1/import-single` from the page. No MyMail endpoint is involved in either, and the Go side has no
knowledge of the feature beyond injecting the configured MyCal URL into `__serverConfig`. The sibling apps are assumed
to share an origin and an auth realm, which is why the cross-app request needs no CORS handling in MyCal.

For the link form this is also a security boundary: **MyMail never fetches a URL taken from a message.** MyCal's
`import-single` already accepts `{"url": …}` and fetches it itself, so the alternative — a fetching proxy endpoint in
MyMail — would mean a *second* implementation of the same server-side fetch, and MyMail has none of the machinery that
makes one safe. What the frontend does instead is decide which links qualify (`util/icslinks.ts`), which is where the
scheme allowlist lives.

Note what that does and does not do about SSRF. The URL is still attacker-chosen — it comes from mail — and
`util/icslinks.ts` is **not** a filter against it: a page cannot resolve DNS or re-check at dial time, so a loopback or
link-local URL is deliberately offered rather than silently dropped. The control this design depends on is MyCal's, and
it is a real one: `httputil.ValidateExternalURL` rejects `localhost`, loopback, private, link-local and unspecified
addresses before the request; `SafeDialContext` resolves and re-checks at dial time, which is what defeats DNS
rebinding; `SafeCheckRedirect(10)` re-validates every hop and bounds the chain; and `maxImportSize` (10 MiB) caps the
body. Reaching any of it still takes an explicit click on a named link. **If that guard is ever weakened, this feature
is what points at it** — a change on MyCal's side, in a repository where nothing records that MyMail hands it URLs
from strangers.

### Authentication
HTTP Basic Auth via htpasswd file (bcrypt). CSRF protection via Origin/Referer validation middleware.
Folder-scoped API tokens live in `api_tokens` and `api_token_folders`. Only a SHA-256 hash of a
random secret is stored. Validation fetches the token and its grants in one SQL statement, so
revocation and replacement cannot mix their identities. The bearer middleware selects a narrow set of GET routes and opens a
read transaction. It validates the token and checks the current message or attachment folder
inside that snapshot; repository reads that assemble the response use the same transaction.
This keeps authorization, counts, metadata, and content consistent during concurrent changes.
The generated handlers finish their database reads before encoding responses; raw messages and
attachments are returned as readers over the byte slices already fetched. The bearer middleware
releases the read transaction at the first response write, before bytes reach the client, so slow
downloads do not hold a database connection or pin the WAL snapshot and no second response buffer
is needed. A new bearer-readable route must finish its database reads before its first response
write; a lazy database-backed reader would fail after the transaction closes. Token
management uses separate handlers that are reached only through the full-access auth path.
The token table keeps a non-reusable AUTOINCREMENT ID for folder relations, while the API exposes only a unique
slug derived from the token name with the same algorithm and collision suffixes as folders.
Revocation deletes by slug, allowing that slug to be reused. The token table is introduced
with a required, unique slug in migration v5; migration v6 makes IDs non-reusable.
Token creation deletes expired tokens before inserting the new token in the same transaction;
foreign-key cascades remove their folder grants. A failed creation rolls back the cleanup.
The one allowed token write, `PUT /messages/{id}/read`, checks the original secret's hash,
token expiry, and folder membership inside the SQL `UPDATE`, so concurrent token rotation
or a folder move cannot broaden its reach.

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

## Device login for API tokens

The shared user-visible contract is [MySuite device login](../../mysuite/spec/device-login.md).
The token CLI's `login` command reads no Basic credentials. It prints a short code
and approval URL, then polls while the user reviews and grants resources/lifetime
on a dedicated `device-approval.html` page. Requested scope and lifetime are
pre-filled and editable; a successful decision attempts to close the page, with
a completion message if closing is blocked. Manual code entry remains available
in Settings → API Tokens. Existing `create` and `revoke` commands remain available.

The server uses `go-server-common/devicegrant` with a versioned
`device_authorizations` table. POST `/api/v1/oauth/device_authorization` and
`/api/v1/oauth/token` bypass Basic auth but reject bearer credentials and retain
Host, CSRF and body-size protection. GET/POST `/api/v1/oauth/device/{user_code}`
require full access and approval POSTs require the normal CSRF policy. Consent
records a decision; polling mints through the existing token store and consumes
the device row in the same SQLite transaction. Secrets remain hash-only and
existing scope enforcement, listing and revocation are unchanged. Demo mode
does not support these endpoints. Requester IP is the socket peer; behind a
reverse proxy it is the proxy address, because untrusted forwarded headers are
ignored.

## API discovery

`discovery.go` embeds the root `openapi.yaml` generator input directly. Its
discovery handler runs before authentication, behind Host validation, and
claims both discovery paths in demo mode to return 404 rather than the SPA.
An outer middleware adds the service-description Link header to API responses
including errors from Host validation and authentication, except in demo mode. The HTTP contract
is `../mysuite/spec/api-discovery.md`; ogen is pinned to v1.24.0.


### Sender authentication storage

`internal/senderauth` interprets SpamAssassin header evidence under the configured
checker-host trust boundary documented in REQUIREMENTS.md. LDA delivery persists its JSON result in `messages.sender_authentication`; detail reads
use that column without loading raw message blobs. Schema version 8 also adds
`spam_filter_settings.trusted_checker_host`. The explicit `-backfill-auth` mode
recomputes existing results from raw using current settings, atomically. Optional
repeatable `-backfill-auth-folder` selectors resolve exact folder names/slugs
before any updates; selected-folder scans use the folder index and do not load
raw blobs from excluded folders. Imports default to unknown until explicit backfill. Existing
spam-folder routing is separate and retains its prior behavior.


Checker-host changes increment `spam_filter_settings.auth_generation` instead of
updating message rows. Schema version 9 adds this counter and
`messages.auth_generation`, both defaulting to zero for existing evidence.
Delivery reads the host and generation in its insertion transaction; backfill
reads them under its existing write lock. Detail/full-message projections return
unknown when generations differ or the host is empty. This preserves immediate
revocation while keeping settings saves independent of mailbox/raw-blob size.

## Read-time phishing analysis

`handler.MessagesIDGet` loads the stored authentication verdict and attachment
metadata, parses body anchors once with `internal/phishing`, then asks the
repository for current own identities and relevant correspondence. Those reads
participate in the bearer's existing authorization snapshot; full-access detail
requests open an equivalent read snapshot for metadata and warnings. `ComputeWithLinks`
produces the API warning list without changing stored mail, routing or downloads.
CLI and MCP forward that message JSON unchanged; no new operation is added.

`internal/attachmentname.Normalize` supplies the common filename interpretation for MIME ingestion,
phishing attachment risk checks and RFC 8187 download headers. Applying it again at use time protects
legacy database rows without rewriting stored mail. Warning filenames still identify the original
attachment metadata, so the UI associates the confirmation with the correct attachment.

Migration v10 introduces `sent_recipients`, independent of contacts, with generated
IDNA registrable-domain and Unicode-skeleton columns and a skeleton index. Deterministic SQLite
functions parse each To/Cc/Bcc list and casefold addr-specs. Insert-into-Sent and
Scheduled-to-Sent triggers maintain history in the sending transaction; ordinary
folder moves cannot fabricate it. A migration backfills existing Sent mail.
Indexed skeleton candidates keep each detail read independent of total sent
history size, and duplicate link hosts are collapsed before querying.

Contact provenance (schema v11) is a read-only `contacts.explicit` flag, set by explicit create/edit only. Atomic
create-or-promote preserves the row ID; automatic upserts preserve provenance. Warning context reads it alongside
sent history in the detail snapshot, with a partial address index to enumerate only explicit contacts for domain
comparison. Their domain skeletons are computed at read time so dependency updates cannot invalidate an expression
index. It suppresses only first-time-sender warnings.

The web message-detail sender action navigates to Settings → Contacts with the message ID in the hash route.
Message detail checks the casefolded stored sender address against paginated contact results, offering the action
only after confirming that no explicit contact exists. Auto-collected contacts remain eligible.
Contacts loads the sender and resolves any existing contact across paginated results before opening its existing
editor. Opening or cancelling performs reads only; Save uses the normal contact create/update API, including in
demo mode.

The detector vendors Unicode 16 confusables with its license and provenance.
`generate_demo_data.go` reproduces the browser tables, pinned to x/net's PSL
revision, Go's Unicode scripts, and `phishing.FoldRune` — the per-character form of
`phishing.FoldAddress`, the one address fold for identities, contacts and sent history
(see REQ-PHISH-04). The sender's IDN decision is made on the domain as received, before
IDNA mapping (REQ-PHISH-05). Changing domain normalization or
skeleton mappings requires a migration to rebuild the stored generated values;
regenerating browser tables alone would leave the persisted index stale. Demo warning evaluation lives in
the worker; HTML parsing is an RPC to the page's inert template parser because
workers have no DOM. Link indices use the same first-1000-anchor convention. If Go's document
parser rejects excessive nesting (its 512-element stack bound), a streaming HTML
tokenizer still extracts links; deep markup cannot suppress all body warnings.

The v10 triggers and generated columns require the application's registered
SQLite functions. Direct writes or SQL-dump restoration through an external
SQLite CLI need those functions; copying/restoring a database-file backup does
not. Scope grants include the derived global-history warning metadata described
in the OpenAPI authentication section and REQUIREMENTS.md.

### Deceptive-link confirmation

`MessagesIDBodyGet` computes link warnings and passes them to
`phishing.RewriteLinks` on the response copy. Its iterative document walk uses
`Links`' anchor indexing, with the same tokenizer fallback for excessive nesting.
The replacement href resolves from the body endpoint to `#/link/:id/:index`,
preserving a deployment path prefix. Stored bodies and raw mail are never edited.

`LinkConfirmation` resolves its index against `api.messages.get(id).warnings`;
the existing endpoint supplies authentication and folder-grant checks. It renders
escaped text and an explicit HTTP(S) continuation link with `noreferrer` and
`referrerPolicy="no-referrer"`. No URL query parameter controls the destination.

The demo worker computes the same warnings and asks a controlled page to rewrite
links in an inert template. It returns that document through the existing body
fetch/srcdoc path. The confirmation route uses the ordinary demo message API.

The web message view formats the server warning list without recomputing signals.
`util/phishing.ts` owns plain warning wording and display-only RFC 3492 decoding;
`MessageDetail` owns structured link-warning wording and renders warnings for the
main message and expanded thread entries.
Link-warning labels and destinations use equal flex shares with CSS ellipses.
Hover or keyboard focus opens a fixed-position tooltip with full, selectable values
outside the body/thread scrollers. Tooltip values wrap and scroll; Escape, blur,
leaving the warning, or scrolling its container dismisses it.
The main body pane contains its own scrolling overflow, keeping warnings and body
content above the thread strip. The divider bounds thread height by the combined
body/thread space, reserving at least 60px for the body when that space permits.
Resize measurements retain fractional pixels so rounding cannot reduce that minimum.
A resize observer re-limits a saved thread height when the message pane changes size.
Body iframes ignore pointer events during a divider drag so they cannot capture it.
Sender names and addresses are separate elements. Risky attachment controls have
no direct download href and invoke the shared confirmation dialog before creating
a download link. Compose reads `reply_to_mismatch` from the source detail response
for Reply/Reply All and displays the destination beside To. Demo uses these same views.

Inbound contact collection checks the final delivery folder after spam classification and user filters.
Junk (folder 7) skips contact upserts entirely; the shared import batch writer applies the same rule to Junk imports.
Demo auto-reply delivery mirrors this exclusion. Existing contacts are preserved.

Contact search ordering uses the exact folded address, explicit provenance and durable `sent_recipients`
history ahead of alphabetical order, before pagination. The demo mirrors that ranking. Compose clears selection
on input changes and ignores responses for old input; suggestion replacement requires deliberate selection.
Recipient pills expose the addr-spec rather than hiding it behind a sender-controlled name.

Compose routes pre-populate client state without creating drafts. The compose view tracks in-app edits to gate
autosave and navigate-away persistence. A forward retains its source message ID until the first draft creation,
which copies attachments through the existing draft API; Send may also trigger that first creation.
