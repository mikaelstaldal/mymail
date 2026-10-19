# MyMail — Operations Guide

This guide covers production installation of MyMail on a Linux server, including Postfix integration for incoming mail, TLS termination via a reverse proxy (nginx), and systemd service management.

## Table of Contents

1. [Prerequisites](#prerequisites)
2. [Install the Binary](#install-the-binary)
3. [Initialize the Database](#initialize-the-database)
4. [Create a System User](#create-a-system-user)
5. [Set Up Authentication](#set-up-authentication)
6. [Configure systemd](#configure-systemd)
7. [Configure Postfix](#configure-postfix)
8. [Configure nginx as a Reverse Proxy](#configure-nginx-as-a-reverse-proxy)
9. [First Login](#first-login)
10. [Importing Existing Mail](#importing-existing-mail)
11. [Upgrading](#upgrading)

---

## Prerequisites

- A Linux server with a working Postfix installation (or another MTA that supports `mailbox_command`).
- nginx (or another reverse proxy capable of TLS termination).
- A valid TLS certificate for your domain (e.g. from Let's Encrypt).
- Go 1.26+, `ogen`, `tsc`, and `openapi-typescript` if building from source; otherwise download a pre-built binary.

---

## Install the Binary

### Build from source

```bash
git clone https://github.com/mikaelstaldal/mymail.git
cd mymail
./build.sh -o /usr/local/bin
```

`mymail-lda` is the minimal Local Delivery Agent client (~3 MB). It forwards incoming messages to the running MyMail server via a UNIX socket, keeping per-delivery memory usage low on memory-constrained servers.

---

## Create a System User

Run MyMail as a dedicated non-root user. This user must be the same Unix user that Postfix delivers mail to (see [Configure Postfix](#configure-postfix)).

```bash
useradd --system --home-dir /var/lib/mymail --shell /usr/sbin/nologin mymail
```

---

## Initialize the Database

```bash
mkdir -p /var/lib/mymail
chown mymail:mymail /var/lib/mymail

sudo -u mymail /usr/local/bin/mymail \
  -init \
  -data /var/lib/mymail \
  -identity-address you@example.com \
  -identity-name "Your Name"
```

This creates `/var/lib/mymail/mymail.sqlite` (mode `0600`) and seeds the built-in folders and your initial identity. Run this only once; subsequent server starts apply schema migrations automatically.

---

## Set Up Authentication

MyMail uses HTTP Basic Auth backed by an htpasswd file (bcrypt). Create the file as the `mymail` user:

```bash
htpasswd -Bc /var/lib/mymail/htpasswd myuser
```

Protect the file:

```bash
chown mymail:mymail /var/lib/mymail/htpasswd
chmod 0600 /var/lib/mymail/htpasswd
```

> **Important:** HTTP Basic Auth must only be used over HTTPS. Never expose MyMail on a non-loopback interface without TLS. The reverse proxy (see below) provides TLS termination.

---

## Configure systemd

Create `/etc/systemd/system/mymail.service`:

```ini
[Unit]
Description=MyMail email client
After=network.target

[Service]
Type=exec
User=mymail
Group=mymail

LoadCredential=basic-auth:/var/lib/mymail/htpasswd

ExecStart=/usr/local/bin/mymail \
    -data /var/lib/mymail \
    -addr 127.0.0.1 \
    -port 8080 \
    -public-url https://mail.example.com \
    -basic-auth-file ${CREDENTIALS_DIRECTORY}/basic-auth \
    -sendmail /usr/sbin/sendmail \
    -lda-socket /run/mymail/lda.sock

Restart=on-failure
RestartSec=1

# Hardening
NoNewPrivileges=true
ProtectSystem=full
PrivateTmp=true
ReadWritePaths=/var/lib/mymail
RuntimeDirectory=mymail

[Install]
WantedBy=multi-user.target
```

Replace `mail.example.com` with your domain. Adjust `-sendmail` if your sendmail binary lives elsewhere (`which sendmail` to check).

Enable and start:

```bash
systemctl daemon-reload
systemctl enable mymail
systemctl start mymail
systemctl status mymail
```

View logs:

```bash
journalctl -u mymail -f
```

---

## Configure Postfix

MyMail receives mail via `mymail-lda`, a minimal delivery agent that forwards each message to the running server over a UNIX socket. This keeps per-invocation memory at ~3 MB regardless of how many concurrent deliveries Postfix spawns.

### Deliver to the mymail user

Add or update the following in `/etc/postfix/main.cf`:

```
# Deliver locally to the mymail user via the thin LDA client
mailbox_command = /usr/local/bin/mymail-lda -lda-socket /run/mymail/lda.sock
```

Because Postfix runs `mailbox_command` as the recipient user (`mymail`), the socket — created by the server under `/run/mymail/` — is accessible without any extra permission changes.

If you want to receive mail for multiple local addresses and route them all to MyMail, ensure those addresses are aliased to the `mymail` system user in `/etc/aliases`:

```
you: mymail
postmaster: mymail
```

Then rebuild the alias database:

```bash
newaliases
postfix reload
```

### Virtual mailbox delivery (alternative)

If you use virtual mailboxes (`virtual_mailbox_maps`), you can invoke the LDA via a transport. Add to `master.cf`:

```
mymail unix  -       n       n       -       -       pipe
  flags=DRhu user=mymail argv=/usr/local/bin/mymail-lda -lda-socket /run/mymail/lda.sock
```

And reference that transport in `main.cf`:

```
virtual_transport = mymail
```

### Fallback: direct database mode

If you do not run a persistent `mymail` server (uncommon), the original direct-database LDA mode is still available:

```
mailbox_command = /usr/local/bin/mymail -lda -data /var/lib/mymail
```

This requires no socket but uses ~14 MB RSS per invocation and opens SQLite directly.

### LDA exit codes

| Exit code | Meaning                                  | Postfix action             |
|-----------|------------------------------------------|----------------------------|
| `0`       | Success (or duplicate, silently ignored) | Message accepted           |
| `1`       | Permanent failure                        | Message bounced            |
| `75`      | Temporary failure (e.g. DB locked)       | Message requeued for retry |

### Spam header integration

MyMail reads spam verdict headers set by the MTA pipeline. If you use SpamAssassin or Rspamd, no extra configuration is needed — MyMail reads `X-Spam-Flag`, `X-Spam-Status`, and a configurable score header (`X-Spam-Score` by default) and routes detected spam to the Junk folder automatically.

Spam filter settings (enabled/disabled, score header name, threshold) are managed in the web UI under Settings → Spam Filter.

---

## Configure nginx as a Reverse Proxy

MyMail does not terminate TLS itself. Place it behind nginx.

Create `/etc/nginx/sites-available/mymail`:

```nginx
server {
    listen 80;
    server_name mail.example.com;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    server_name mail.example.com;

    ssl_certificate     /etc/letsencrypt/live/mail.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/mail.example.com/privkey.pem;

    # Modern TLS settings
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_prefer_server_ciphers off;

    # Rate limiting (adjust as needed)
    limit_req_zone $binary_remote_addr zone=mymail:10m rate=30r/m;
    limit_req zone=mymail burst=10 nodelay;

    # Max upload size (attachments)
    client_max_body_size 35m;

    location / {
        proxy_pass         http://127.0.0.1:8080;
        # Preserve the port when the public URL uses a non-default port.
        proxy_set_header   Host              $http_host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;

        # Required for CSRF validation: mymail checks Origin/Referer
        proxy_pass_request_headers on;
    }
}
```

MyMail returns HTTP 421 when a request's Host differs from the configured public URL or a local listener authority.
If an existing reverse proxy rewrites Host to an upstream name, configure it to preserve the public Host. A wildcard
bind address now requires `-public-url`; local health checks may continue to use `localhost` and the listen port.

Enable and test:

```bash
ln -s /etc/nginx/sites-available/mymail /etc/nginx/sites-enabled/mymail
nginx -t
systemctl reload nginx
```

### TLS certificate (Let's Encrypt)

```bash
certbot --nginx -d mail.example.com
```

Certbot will modify the nginx config to handle certificate renewal automatically.

---

## First Login

Open `https://mail.example.com` in your browser. Log in with the username and password you set in the htpasswd file.

On first login:
- Your initial identity (set during `-init`) is already configured. Add additional identities under Settings → Identities.
- Configure spam filter settings under Settings → Spam Filter.
- Set up delivery filters under Settings → Filters.

---

## Importing Existing Mail

Stop the server before running import (concurrent import and server access to the same database is not supported):

```bash
systemctl stop mymail

sudo -u mymail /usr/local/bin/mymail \
  -import -data /var/lib/mymail \
  inbox:mbox:/path/to/Inbox \
  sent:mbox:/path/to/Sent \
  drafts:mbox:/path/to/Drafts \
  trash:mbox:/path/to/Trash

systemctl start mymail
```

Each argument is `<folder>:<format>:<path>`. Supported formats: `mbox`, `maildir`, `mbx`. Messages already in the database (matched by `Message-ID`) are skipped automatically.

---

## Upgrading

1. Build or download the new binaries.
2. Stop the service:
   ```bash
   systemctl stop mymail
   ```
3. Replace both binaries:
   ```bash
   install -o root -g root -m 0755 mymail-new /usr/local/bin/mymail
   install -o root -g root -m 0755 mymail-lda-new /usr/local/bin/mymail-lda
   ```
4. Optionally apply migrations as a separate batch job, using the service account and your data directory:
   ```bash
   sudo -u mymail /usr/local/bin/mymail -migrate -data /var/lib/mymail
   ```
   This opens the existing database, applies pending migrations, runs `VACUUM` if any were applied,
   closes it, and exits without starting a server. Already current databases are not vacuumed.
   It exits `0` when successful (including when already up to date), or `1` with an error message on failure.
   If vacuum fails, migrations remain applied and the command reports the compaction failure.
   Complete this step successfully before restarting the service.
5. Start the service — any remaining schema migrations are applied automatically on startup, without vacuuming:
   ```bash
   systemctl start mymail
   ```
6. Check the logs for any migration or startup errors:
   ```bash
   journalctl -u mymail -n 50
   ```

---

## Firewall

MyMail binds to `127.0.0.1` by default and is never directly exposed to the internet. Ensure your firewall allows:

| Port | Protocol | Purpose                          |
|------|----------|----------------------------------|
| 80   | TCP      | HTTP → redirect to HTTPS (nginx) |
| 443  | TCP      | HTTPS (nginx → MyMail)           |
| 25   | TCP      | SMTP (Postfix incoming mail)     |

The mymail process itself (port 8080) must not be reachable from outside the server.


### Sender authentication with SpamAssassin

This feature is opt-in. Keep Settings → Spam Filter → **Trusted Checker Host**
empty until every route into MyMail has a secured scanning path. A matching
hostname is not proof a scan happened: forged headers survive unchecked delivery.
The existing Junk filter remains independent of this setting.

1. Load `Mail::SpamAssassin::Plugin::SPF`,
   `Mail::SpamAssassin::Plugin::DKIM`, and
   `Mail::SpamAssassin::Plugin::DMARC` in your SpamAssassin 4.x configuration.
   DMARC needs both other plugins and its Perl dependencies, including
   `Mail::DMARC::PurePerl`. Enable DNS/network checks and the corresponding
   nonzero-score rules so their hits appear in the `tests=` list. Run
   `spamassassin --lint` and check scanner debug output for unavailable modules.
   The [4.x DMARC plugin documentation](https://spamassassin.apache.org/full/4.0.x/doc/Mail_SpamAssassin_Plugin_DMARC.html)
   lists `DMARC_PASS`, `DMARC_REJECT`, `DMARC_QUAR`, `DMARC_NONE` and
   `DMARC_MISSING`. `DMARC_NONE` is failed authentication under policy `none`,
   not absence of a policy.
2. Inspect the raw source of a freshly scanned message. Find
   `X-Spam-Checker-Version: SpamAssassin 4.x (...) on your-checker-host` and
   enter exactly the hostname after `on` in MyMail. Confirm one checker header
   and one status header remain, and that injected preexisting `X-Spam-*`
   headers were removed by your actual scanning configuration. Keep the trusted
   host empty if these checks fail. Multiple scanner blocks produce unknown.
3. Verify envelope-sender SPF, rather than assuming HELO SPF is enough. On a
   controlled message with a known envelope sender, compare the MTA envelope,
   the bytes passed into spamc and SpamAssassin debug output for SPF. Seeing only
   `SPF_HELO_NONE` and no `SPF_PASS` on Gmail mail is a diagnostic clue, not proof
   of a missing envelope sender. Check how your Postfix pipe passes `${sender}`
   and whether a trusted MTA-written header is available **before** scanning.
   Configure `envelope_sender_header` only for a header that your MTA replaces or
   removes at ingress; never trust a sender-supplied envelope header. A final
   `Return-Path` added after the scan cannot help the scan. Verify SPF and DMARC
   with controlled pass/fail messages and scanner logs; do not infer SPF failure
   from an absent rule alone. Consult the
   [SpamAssassin configuration reference](https://spamassassin.apache.org/full/4.0.x/doc/Mail_SpamAssassin_Conf.html)
   for envelope-sender and trusted-network settings.
4. Close unchecked pass-through. The
   [spamc 4.x manual](https://spamassassin.apache.org/full/4.0.x/doc/spamc.html)
   specifies `-s` in bytes: messages above it are returned **unprocessed**; the
   default is 500 KB and the maximum is 256 MB. Set it at least as high as the
   largest message accepted by the MTA and MyMail (MyMail LDA caps at 64 MiB;
   for that limit use `-s 67108864`). Check other scanner/MTA size limits too.
   `-x` disables safe fallback on errors; use `-X` with it to turn unavailable
   errors into temporary failures. For example the scanning step can use
   `spamc -x -X -s 67108864`. **Do not pipe unchecked stdout straight into the
   LDA:** capture scanner output privately, check its exit status, and deliver
   that output only on success. Map scanning failures to MTA deferral (normally
   exit 75 / EX_TEMPFAIL); preserve retries and avoid `-E`/`-c` exit semantics.
   `-x` is not a substitute for fixing size bypass. Test with spamd stopped,
   a message over the former 500 KB limit, and a forged matching checker/status
   pair: no unchecked message should be delivered. Also test rejection/defer at
   your actual maximum size.

#### Postfix pipe: supply the envelope sender before scanning

For an after-queue Postfix pipe feeding `spamc`, add `flags=R` to the
`spamassassin` service in `/etc/postfix/master.cf`. It prepends a `Return-Path:`
header containing the SMTP envelope sender **before** `spamc` reads the message:

```text
spamassassin unix -     n       n       -       -       pipe
  flags=R user=debian-spamd
  argv=/usr/bin/spamc -s 30000000 -x -X -e /usr/sbin/sendmail -oi -f ${sender} ${recipient}
```

This example uses a 30,000,000-byte scan limit; check `postconf message_size_limit`
and raise `-s` if necessary to cover all accepted mail. The service account and
paths must match your installation. The `-f ${sender}` after `sendmail` preserves
that sender during reinjection; it does not provide it to the earlier scan.
See the [Postfix pipe flags](https://www.postfix.org/pipe.8.html).

In `/etc/spamassassin/local.cf`, explicitly select the header:

```text
envelope_sender_header Return-Path
```

Run `spamassassin --lint`, reload/restart spamd, and reload Postfix. Verify that
sender-supplied Return-Path headers are removed before scanning: this header must
come from the trusted MTA. Inspect scanner debug logs to confirm the envelope
sender and original sending IP were recognized; trusted/internal relay settings
still matter for SPF. See the
[envelope sender configuration reference](https://spamassassin.apache.org/full/4.0.x/doc/Mail_SpamAssassin_Conf.html).

The operator confirmed that this change produced the following hits on a newly
received Gmail message with SpamAssassin 4.0.1:

```text
X-Spam-Status: No, score=-0.2 required=5.0 tests=DKIM_SIGNED,DKIM_VALID,
 DKIM_VALID_AU,DKIM_VALID_EF,DMARC_PASS,FREEMAIL_FROM,
 RCVD_IN_DNSWL_NONE,SPF_HELO_NONE,SPF_PASS autolearn=ham
 autolearn_force=no version=4.0.1
```

`SPF_PASS` is now present alongside `DKIM_VALID_AU` and `DMARC_PASS`.
`SPF_HELO_NONE` can coexist with envelope-sender `SPF_PASS`; it is a separate
HELO-domain result. This confirms the positive result for that message, not every
failure or bypass path; also perform the scanner-outage and size-limit tests above.

Header position relative to `Received:` is not an authenticity check: scanner
insertion and MTA reinjection vary, and matching forged blocks can occupy the same
position. MyMail rejects duplicate checker/status headers, but cannot recognize a
single forged matching block. Secure direct LDA access too. Imports always start with unknown evidence, even
with a configured host; verify import provenance before opting into backfill.
Only after this verification should you enable the trusted host. Existing raw
mail may predate these protections; leave it unknown unless you can establish
its provenance.

To recompute stored evidence, stop the server and deliveries, then run:

```bash
mymail -data /path/to/data -backfill-auth
```

To process selected folders and skip a large archive, repeat
`-backfill-auth-folder` with each exact folder name or slug:

```bash
mymail -data /path/to/data -backfill-auth \
  -backfill-auth-folder inbox -backfill-auth-folder sent

# Quote display names containing spaces:
mymail -data /path/to/data -backfill-auth -backfill-auth-folder 'Current projects'
```

Without selectors, all folders are processed. Selectors are case-sensitive;
unknown names/slugs or a name matching another folder's slug fail before any
message is changed. Use `name:inbox` or `slug:inbox` to resolve a name/slug
collision explicitly. A name beginning with either prefix can be selected by
prepending `name:` again (e.g. `name:slug:example`). Repeated aliases for the same folder process it only once.
Unselected messages keep their stored verdicts and timestamps, and their raw
message blobs are not read. The entire selected run is still one transaction.
`-backfill-auth-folder` requires `-backfill-auth`.

Changing or clearing the checker host immediately makes all prior results read as
unknown by advancing a configuration generation. Saving settings does not scan or
rewrite messages, raw blobs, or message timestamps, regardless of archive size.
Returning to an earlier hostname does not restore old evidence; run explicit
backfill on the folders you want to re-evaluate. The schema migration also leaves old results unknown. Backfill is explicit and
repeatable; it uses the current host, including an empty host to reset all results
to unknown. It processes one raw message at a time in a single transaction. It
acquires the write lock before reading and can hold it for a large mailbox; run it during maintenance.
