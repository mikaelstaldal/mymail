# MyMail user manual

[Project overview](README.md) · [Development guide](DEVELOPMENT.md)

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

## Quick Start

To build from source, put Go (at least the version in [go.mod](go.mod)),
`tsc`, `openapi-typescript`, `ogen`, `golangci-lint`, and `node` on `$PATH`.
See the [development guide](DEVELOPMENT.md) for build details.

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

Creates the data directory and SQLite database and seeds the built-in folders and an initial identity. Must be run before server, LDA, import, or demo-seeding mode. The browser-only demo modes do not need a database.

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

Settings → API Tokens lists existing tokens and their folder scope and expiry,
and lets you revoke them after confirmation or approve a device login. Token
creation uses the CLI commands below.

`mymail-token` manages folder-scoped API tokens. Use `login` to obtain a token
without giving the CLI your Basic-auth credentials:

```bash
MYMAIL_URL=https://mail.example.com mymail-token login \
  -name 'Archive reader' -lifetime 7d -folders 1,100 -out ./token
```

The CLI prints an approval URL and user code to stderr, then waits while you
review the request on a dedicated approval page. Compare the
code with your CLI, select the folders to grant, and choose a lifetime before
approving. After Approve or Deny, the page attempts to close; if your browser
blocks this, close the tab yourself. The login request expires after ten minutes.
Settings → API Tokens → Approve device also accepts a code entered manually.

`-name` is required. `-folders` is an optional suggestion; the approver chooses
the actual scope. Requested resources and lifetime are pre-filled and can be
changed before approval. Omit `-lifetime` to request `30d`; durations accept `s`, `m`,
`h`, or `d`, up to ten years. With `-out`, the token is saved to a new file with
mode `0600`; existing files are never overwritten. Without `-out`, only the token
secret is printed to stdout. The revocation slug is printed to stderr; save it
for `revoke`. `login` reads no credentials, ignores `MYMAIL_USER`, and rejects
explicit `-user`, `-credentials-file`, or `-credentials-stdin` options.

For `create` and `revoke`, when Basic auth is configured, put one
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
  create -name 'Archive reader' -lifetime 7d -folders 1,100 -out ./token
MYMAIL_URL=https://mail.example.com mymail-token -credentials-file ./basic-credentials \
  revoke archive-reader
```

Creation writes the token secret to a new private file with `-out` (mode `0600`,
no overwrite), or prints it to stdout without `-out`. Its slug goes to stderr. Save that
slug for revocation. Lifetimes accept `s`, `m`, `h`, or `d` (up to ten years). Use
`-credentials-stdin` instead of `-credentials-file` to read credentials from standard input.
Put global flags before the command, as in `mymail-cli`. Keep credential and token files
readable only by their owner. Run `mymail-token help` and
`mymail-cli help` for the full command syntax.

## Use with an AI assistant (MCP)

`./build.sh` also builds `mymail-mcp`, a [Model Context Protocol](https://modelcontextprotocol.io/)
server that gives an MCP client such as Claude Code access to your mail with a
folder-scoped token. The client starts it and talks to it over stdin/stdout. For
example, in Claude Code:

```bash
claude mcp add mymail -- /path/to/mymail-mcp -url https://mail.example.com -token-file /path/to/token
```

It takes the same `-url` and `-token-file` flags and `MYMAIL_URL` and
`MYMAIL_TOKEN_FILE` defaults as `mymail-cli`; without a token it sends no
Authorization header. It offers these resources:

| URI | Contents |
|---|---|
| `mymail://folders` | the folders the token can read |
| `mymail://folders/{folder_id}/messages{?limit,offset,unread,flagged}` | a page of a folder's messages |
| `mymail://messages/{id}` | one message as JSON |
| `mymail://messages/{id}/text` | its plain-text body |
| `mymail://messages/{id}/headers` | its header block |
| `mymail://messages/{id}/body{?external}` | its sanitized HTML body |
| `mymail://messages/{id}/raw` | the original message; drafts have none |
| `mymail://attachments/{id}` | an attachment's bytes |

Two tools: `search_messages` searches one folder, taking `folder_id` and `q` and
optionally `date_from`, `date_to`, `from_addr`, `to_addr`, `sort`, `limit` and
`offset`. `mark_message_read` marks a message as read, the only change the server
can make. A resource or search response over 1 MiB is refused rather than
truncated. Run `./mymail-mcp -help` for details.

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

## Operations Guide

See [OPERATIONS.md](OPERATIONS.md) for production installation, Postfix integration, reverse proxy configuration, systemd service setup, and authentication.


### Trusted sender-authentication checker

Settings → Spam Filter includes **Trusted Checker Host**. Leave it empty unless
your operator has secured the SpamAssassin scanning path as described in the
[operations guide](OPERATIONS.md#sender-authentication-with-spamassassin).
This setting records authentication evidence for delivered messages independently
of whether automatic Junk routing is enabled. It does not add a verified badge.
Changing or clearing the host immediately makes prior evidence read as unknown
without rewriting the mailbox; the operator
can explicitly recompute it with `-backfill-auth` after checking historical mail provenance.

### Phishing warnings

Message detail JSON includes `warnings` for suspicious sender authentication,
embedded addresses in display names, lookalike domains, different Reply-To
registrable domains, first-time senders, deceptive links and risky attachments.
`mymail-cli messages get ID` and the corresponding `mymail-mcp` message resource
include this list. Each warning supplies the relevant address, domain, link or
filename.

The web UI shows these warnings above the message body, in both HTML and plain
views. Authentication failures and lookalike domains receive stronger emphasis.
Warnings cannot be dismissed. The From header and thread entries show the name
and address separately; internationalized domains are decoded, marked IDN, and
accompanied by their ASCII address. Reply and Reply All show a note beside To
when the original message redirects replies to a different domain.

Risky attachments are marked with the reason. Clicking one asks for confirmation
before downloading; Cancel leaves it undownloaded. Check the source before
choosing Download. Other attachments download as usual.

First-time means you have never sent to that address, including To, Cc and Bcc.
Receiving mail alone does not make a sender familiar. Adding a contact or saving an edit (even unchanged) suppresses this warning. Settings → Contacts labels these “Added or edited by you”; this does not verify the sender or suppress authentication failures or other warnings. Adding an auto-collected address promotes it; adding an already saved address reports a duplicate. Existing contacts start as auto-collected on upgrade. Delete a contact to remove this trust; collecting it again does not restore trust. Explicitly saved contact domains also help detect lookalike domains. History is
backfilled from Sent on upgrade and survives deleting sent messages afterwards.
Warnings refresh when you read mail, so a later send can remove the first-time
signal. Drafts, Sent, Scheduled, and own-identity mail with trusted DMARC pass are
exempt. A warning is a reason to check the message; no warnings is no guarantee
of safety. Legitimate internationalized domains can also produce an advisory.
Only the first 1000 anchors are checked. The browser demo includes a deliberately
suspicious message and a harmless placeholder attachment for trying every warning in the UI and API.

A folder-scoped API token also receives warnings derived from global identities
and sent history, even if Sent is outside its grant. It can infer whether you have
emailed a sender in its allowed folders and see a domain named by `resembles`.
Consider this derived metadata when granting a token access to a folder.

### Deceptive-link confirmation

A suspicious link opens a MyMail confirmation page before leaving your mailbox.
This happens when the link text names a different domain from its destination,
or the destination uses an IP address or an internationalised domain. Other
links open normally.

Check the visible text against the full destination and the bold destination host.
For internationalised domains, MyMail also shows the ASCII (punycode) host when
it differs from the decoded host. Choose **Go back** to return to the message,
or **Continue to destination** to open the link. MyMail never continues
automatically, and continuing sends no referrer. This works in the browser demo too.
