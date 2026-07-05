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
        proxy_set_header   Host              $host;
        proxy_set_header   X-Real-IP         $remote_addr;
        proxy_set_header   X-Forwarded-For   $proxy_add_x_forwarded_for;
        proxy_set_header   X-Forwarded-Proto $scheme;

        # Required for CSRF validation: mymail checks Origin/Referer
        proxy_pass_request_headers on;
    }
}
```

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
4. Start the service — schema migrations are applied automatically on startup:
   ```bash
   systemctl start mymail
   ```
5. Check the logs for any migration or startup errors:
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
