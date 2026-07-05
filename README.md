# MyMail

A self-hosted, single-user email client with a REST API and embedded web UI. Designed to run on a Linux server alongside a standard MTA such as Postfix.

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
- **Search** — full-text search powered by SQLite FTS5.
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
| `-public-url`       | _(auto)_    | Public-facing base URL for CSRF validation, e.g. `https://mail.example.com` |
| `-basic-auth-file`  | _(none)_    | Path to htpasswd file; enables HTTP Basic Auth when set                     |
| `-basic-auth-realm` | `mymail`    | Auth realm shown to browsers                                                |
| `-sendmail`         | `sendmail`  | Path or name of the sendmail binary                                         |

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
