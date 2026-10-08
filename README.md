# MyMail

A self-hosted, single-user email client with a REST API and embedded web UI. Designed to run on a Linux server alongside a standard MTA such as Postfix.

See live demo at <https://mikaelstaldal.github.io/mymail/>.

## Clients

In addition to the built-in web interface, there is also 

* A [native Android app](https://github.com/mikaelstaldal/mymail-android)

## Features

- **Single binary deployment** — web UI assets are embedded; one binary plus one SQLite file is all you need.
- **No IMAP/POP3** — incoming mail is delivered by the local MTA via a local delivery agent (LDA) interface; outgoing mail is handed off to the system `sendmail` binary.
- **Folders** — built-in Inbox, Sent, Drafts, Trash, Scheduled, Snoozed, Junk, plus user-created folders.
- **Compose** — plain-text and rich-text (HTML) composition with attachments, reply, reply-all, forward.
- **Threading** — messages are grouped by conversation.
- **Deferred send** — schedule messages for future delivery; the background scheduler retries on failure.
- **Snooze** — temporarily hide messages and have them reappear later.
- **Search** — full-text search powered by SQLite FTS5, refinable by folder, date range, and From/To address.
- **Filters** — rule-based delivery routing (move, trash, mark-read, drop) evaluated at delivery time.
- **Spam detection** — reads spam verdict headers set by the MTA pipeline (SpamAssassin, Rspamd, etc.).
- **Contacts** — auto-populated from incoming and outgoing mail.
- **Multiple identities** — manage multiple From addresses with per-identity signatures.
- **Import** — batch import from mbox and Maildir sources.
- **No CGO** — pure-Go SQLite (`modernc.org/sqlite`); no C toolchain required at runtime.

## Technology

| Layer   | Technology                                                            |
|---------|-----------------------------------------------------------------------|
| Backend | Go, SQLite (via `modernc.org/sqlite`)                                 |
| API     | OpenAPI 3 contract, generated server stubs (`ogen`)                   |
| Web UI  | TypeScript, Preact, Quill (vendored; no CDN dependency)               |
| Auth    | HTTP Basic Auth (htpasswd/bcrypt), CSRF via Origin/Referer validation |

## Quick Start

```bash
# 1. Build
./build.sh

# 2. Initialize the database
./mymail -init -data /var/lib/mymail \
  -identity-address you@example.com \
  -identity-name "Your Name"

# 3. Run the server (loopback-only by default)
./mymail -data /var/lib/mymail
```

The web UI is available at `http://127.0.0.1:8080`.

## Command-Line Reference

### Init mode

```
mymail -init -data <dir> -identity-address <addr> [-identity-name <name>]
```

Creates the data directory and SQLite database and seeds the built-in folders and an initial identity. Must be run before any other mode.

### Server mode (default)

```
mymail [flags]
```

| Flag                | Default     | Description                                                                 |
|---------------------|-------------|-----------------------------------------------------------------------------|
| `-addr`             | `127.0.0.1` | Bind address                                                                |
| `-port`             | `8080`      | HTTP listen port                                                            |
| `-data`             | `data/`     | Data directory (contains the SQLite file)                                   |
| `-public-url`       | _(auto)_    | Public URL for CSRF and Host validation; required with a wildcard `-addr`   |
| `-basic-auth-file`  | _(none)_    | Path to htpasswd file; enables HTTP Basic Auth when set                     |
| `-basic-auth-realm` | `mymail`    | Auth realm shown to browsers                                                |
| `-sendmail`         | `sendmail`  | Path or name of the sendmail binary                                         |
| `-demo-server`      | _(off)_     | Serve the web UI in demo mode: no database, no REST API (see below)         |

Without Basic Auth, use a loopback bind address. Requests with an unrecognized Host are rejected, including requests
through a DNS-rebound hostname. For a reverse proxy, set `-public-url` to its external URL and preserve that Host when
forwarding requests. The proxy must also prevent direct access to the backend if it provides authentication.

### LDA mode

```
mymail -lda -data <dir>
```

Reads a single RFC 5322 message from stdin, applies spam detection and filters, and stores it in the database. Intended to be called by the MTA as a mailbox command.

Exit codes: `0` success, `1` permanent failure (message bounces), `75` temporary failure (MTA retries).

### Import mode

```
mymail -import -data <dir> <folder>:<format>:<path> [...]
```

Bulk-imports messages from mbox files or Maildir directories. Each argument is a colon-separated triplet specifying the target folder, source format (`mbox` or `maildir`), and source path. Duplicate messages (matched by `Message-ID`) are skipped.

Example:

```bash
mymail -import -data /var/lib/mymail \
  inbox:mbox:/home/user/Inbox \
  sent:mbox:/home/user/Sent \
  work:maildir:/home/user/Maildir/.Work
```

## API token CLI

`MYMAIL_TOKEN_FILE` sets the default token file for `mymail-cli`. Explicit `-token-file`
or `-token-stdin` overrides it.

`mymail-token` manages folder-scoped API tokens. When Basic auth is configured, put one
`username:password` line in a private file, pipe it to `-credentials-stdin`, or use `-user`
for a password prompt with terminal echo disabled. Credentials are optional on servers
without Basic auth. `MYMAIL_USER` supplies the default username for the prompt.
`MYMAIL_URL` sets the default server URL for both `mymail-token` and `mymail-cli`; an explicit
`-url` overrides it. Both accept a path prefix, for example `https://mail.example.com/mymail`,
with or without a trailing slash; API requests then use `/mymail/api/v1/...`. Remote URLs require HTTPS. Plain HTTP is accepted for literal loopback
IP addresses. The clients reject embedded URL credentials, bypass proxy settings, and do not
follow redirects.

```bash
MYMAIL_URL=https://mail.example.com mymail-token -credentials-file ./basic-credentials \
  create -name 'Archive reader' -lifetime 7d -folders 1,100
MYMAIL_URL=https://mail.example.com mymail-token -credentials-file ./basic-credentials \
  revoke archive-reader
```

Creation prints **only the token secret to stdout** and its slug to stderr. Save that
slug for revocation. Lifetimes accept `s`, `m`, `h`, or `d` (up to ten years). Use
`-credentials-stdin` instead of `-credentials-file` to read credentials from standard input.
Put global flags before the command, as in `mymail-cli`. Keep credential and token files
readable only by their owner. Run `mymail-token help` and
`mymail-cli help` for the full command syntax.

## Demo mode

Demo mode runs the full web UI with no backend at all. A service worker
intercepts every `/api/v1` request and answers it from storage in the browser,
so messages, folders, drafts, contacts, and settings are created, searched,
edited, and deleted exactly as they are against the real server — they just
never leave the machine. The mailbox starts out holding the same content `-demo`
seeds, and clearing the site's data resets it to that. A modal on the first
visit says as much, so nobody writes anything they care about into it.

```bash
./mymail -demo-server                    # serve the demo on http://127.0.0.1:8080
./mymail -demo-bundle /tmp/mymail-demo   # or write it out as a static site
```

Neither mode opens a database, so neither takes `-data`.

Mail you send in the demo does not go anywhere: it lands in Sent, and a
generated reply from the recipient arrives in the Inbox 20 seconds later, so a
thread can be seen forming. Scheduled sends and snoozes work too, resolved on
the next request rather than by a background timer.

`-demo-bundle` writes plain files that any web server can host — no backend, no
build step, no configuration. Everything the app requests is a relative URL and
routing is on the fragment, so the same bundle works at the origin root or under
any path. One caveat comes with the browser being the backend: **service workers
need a secure context**, so serve the bundle over HTTPS or from `localhost`.

There is no MyCal integration in demo mode, and nothing is sent anywhere.
Attachments are capped at 8 MiB each, since browser storage is a modest shared
quota rather than a disk.

## Building from Source

**Prerequisites:** Go 1.26+, `tsc` (TypeScript compiler), `openapi-typescript`, `ogen`.

```bash
# Full build: TypeScript + Go binary + tests + lint
./build.sh

# Go binary only (requires web/static/*.js already compiled)
go build -tags netgo

# TypeScript only
tsc --project web/static/tsconfig.json

# Run tests
go test ./...
```

After editing `openapi.yaml`, regenerate the Go server stubs:

```bash
go generate ./internal
```

## Operations Guide

See [OPERATIONS.md](OPERATIONS.md) for production installation, Postfix integration, reverse proxy configuration, systemd service setup, and authentication.

## Credits

See [CREDITS.md](CREDITS.md) for the third-party libraries and other artifacts MyMail is built on,
with their licenses.

## License

Copyright 2026 Mikael Ståldal.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
