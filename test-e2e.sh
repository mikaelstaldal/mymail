#!/usr/bin/env bash
# Runs the Playwright suite against a freshly started server.
#
# This is the entry point CI uses, and it is the one to use locally when you
# want the same conditions CI gets. Interactively, `playwright-test` from the
# e2e directory is still the way to run a single spec (see AGENTS.md).
#
# It does not build. Run ./build.sh first — and note that the binary embeds
# web/static/, so a server started from a stale binary serves stale assets and
# the suite then measures something other than what you edited. The freshness
# check below exists to make that impossible rather than merely unlikely.
set -euo pipefail

# Every path below is repo-relative, including the freshness check — without
# this, running from elsewhere fails with a "stale app.css" message that is
# actively misleading about what went wrong.
cd "$(dirname "${BASH_SOURCE[0]}")"

BINARY=./mymail
PORT=8090
# A fresh database per run, in a directory this script owns and removes.
# Reusing one is how an "empty" run silently becomes a run against whatever the
# last one left behind.
DATA_DIR=$(mktemp -d)

SERVER_PID=

cleanup() {
    if [ -n "$SERVER_PID" ]; then
        kill "$SERVER_PID" 2>/dev/null || true
        wait "$SERVER_PID" 2>/dev/null || true
    fi
    rm -rf "$DATA_DIR"
}

trap cleanup EXIT
# Belt and braces. In general an untrapped fatal signal terminates bash *without*
# running the EXIT trap, which would leave the server squatting the port — the
# exact condition the pre-flight check below refuses to start on, so the *next*
# run fails rather than this one. One line, and it keeps the three sibling
# scripts from differing by accident.
#
# **It does not fix a leak that can be demonstrated here.** An earlier version of
# this comment claimed `trap cleanup EXIT` alone leaks on SIGPIPE. It does not, in
# this environment. Tested against this script, with and without this line:
#
#     2>&1 | head -5          clean       2>&1 | grep -m1 passed   clean
#     2>file | head -5        clean       SIGINT mid-run           clean
#     SIGTERM mid-run         clean       SIGKILL mid-run          LEAKS
#
# Only SIGKILL leaks, and nothing can trap that. e2e-mynotes found the same for
# its script, independently, which is what prompted the re-measurement.
#
# The original claim came from a confounded test, and the shape is worth knowing
# because it is the measurement apparatus lying in the reassuring direction again:
# the probe printed "CLEANUP RAN" to stderr, was run as `script 2>&1 | head -1`,
# and so wrote that message into the very pipe `head` had already closed. The trap
# had run; its output was discarded. It was then compared against a second run
# that differed in *two* ways — this trap line, and stderr redirected to a file —
# and the difference was attributed to the trap. Re-run with stderr going to a
# file in both, the EXIT-only variant reports CLEANUP RAN.
#
# The servers that were seen squatting 8090 are unexplained rather than explained
# by this; concurrent runs of the suite (a review agent was running it repeatedly
# at the time) account for the observation without any leak.
#
# Re-raising via `exit` rather than adding the signals to the EXIT trap's list:
# listing them there runs cleanup twice, once per trap.
trap 'exit 1' INT TERM PIPE

if [ ! -x "$BINARY" ]; then
    echo "$BINARY not found or not executable — run ./build.sh first" >&2
    exit 1
fi

# Refuse to start if the port is already taken. Ours would exit on bind failure
# while the readiness probe below succeeded against whatever is squatting, and
# the suite would then run against a database and a binary that are not the ones
# under test. That happened during the work that added MyCal's copy of this
# script.
#
# Checked here rather than by watching our own process afterwards: a background
# process that has exited is a zombie until reaped, and `kill -0` succeeds on a
# zombie — so the obvious liveness check silently passes in exactly this case.
if (exec 3<>"/dev/tcp/127.0.0.1/${PORT}") 2>/dev/null; then
    exec 3<&- 3>&-
    echo "Something is already listening on port ${PORT}." >&2
    echo "Stop it first — otherwise these tests would run against it, not against this build." >&2
    exit 1
fi

# MyMail differs from MyCal here: it will not serve from an empty data directory.
# `runServer` calls repository.CheckDBExists and exits with "run 'mymail -init'
# first", so the database has to be created before the server is started — and
# -init itself refuses without -identity-address. Both were confirmed by running
# them: a bare `./mymail -port … -data <empty dir>` exits immediately, and the
# readiness probe below would then have polled a dead server for 20 seconds and
# reported a timeout instead of the real reason.
#
# The identity is never used by these tests; it exists because -init demands one.
"$BINARY" -init -data "$DATA_DIR" \
    -identity-address "e2e@example.com" -identity-name "E2E Test" > /dev/null

# -public-url must match the baseURL origin in e2e/playwright.config.ts, or CSRF
# rejects mutating requests with 403.
#
# **Not "every mutating request"**, which is what MyCal's script says and what
# this one said until mynotes-dev measured it. go-server-common's csrf.Middleware
# allows a non-GET carrying *neither* Origin nor Referer — "native client, allow",
# stated in its own doc comment (v1.8.0, csrf/csrf.go). So a request made with
# Playwright's `request` fixture, or with curl, sails through a mismatched
# -public-url; only a request that carries an origin is checked. Measured here
# against a deliberately mismatched -public-url:
#
#     no Origin, no Referer   201
#     Origin: <the page>      403
#     Referer only            403
#
# The flag is still required, and it binds on this suite rather than in theory:
# the sidebar-footer fixtures create user folders with `fetch` from inside the
# page, which the browser stamps with the page's Origin. It would bite the same
# way the moment a test clicked a Save button. The wrong rationale mattered
# because it predicts a 403 for the API-level calls that are in fact allowed —
# it would send someone debugging a write failure to the wrong flag entirely.
#
# -sendmail is a stub because the real one is resolved with exec.LookPath at
# startup and the server exits if it is missing; neither this sandbox nor the CI
# runner has a sendmail. No test here sends anything, so /bin/true is enough —
# but note that it makes a send *appear* to succeed. A future spec that asserts
# on outgoing mail needs a recording stub rather than this.
"$BINARY" -port "$PORT" -data "$DATA_DIR" -public-url "http://localhost:${PORT}" \
    -sendmail /bin/true &
SERVER_PID=$!

# /api/v1/folders answers on a freshly initialised database — it returns the
# seven built-in folders — so it needs no fixture and no query string.
for i in $(seq 1 40); do
    if curl -sf "http://localhost:${PORT}/api/v1/folders" > /dev/null 2>&1; then
        break
    fi
    if [ "$i" -eq 40 ]; then
        echo "Server failed to start on port ${PORT}" >&2
        exit 1
    fi
    sleep 0.5
done

# Prove the server is serving the build under test. A green suite run against a
# stale binary is not evidence, and it fails in the reassuring direction: the
# tests pass, describing a version of the app that is not the one on disk.
#
# Every emitted asset, not a sample of them. MyCal's version of this check used
# to hash app.css and app.js alone, on the reasoning that a change to web/ts/**
# shows up in the JS — but tsc emits one module per source file, and MyMail has
# ~40 of them under web/static/. Editing views/MessageList.ts leaves BOTH hashed
# files byte-identical, so the check passed a server serving 39 stale modules. A
# guard that samples the assets is not a guard against staleness; it is a guard
# against staleness in the two files nobody was going to edit alone.
#
# vendor/ is excluded because it is committed rather than emitted (see
# .gitignore's `!web/static/vendor/**`), and the demo worker's output —
# demo-sw.js, demo-client.js, demo/**.js — is included: it is served the same way
# and goes stale the same way.
stale=0
checked=0
while IFS= read -r path; do
    asset=${path#web/static/}
    if ! served=$(curl -sf "http://localhost:${PORT}/${asset}" | md5sum | cut -d' ' -f1); then
        echo "Could not fetch /${asset} from the test server." >&2
        exit 1
    fi
    ondisk=$(md5sum "$path" | cut -d' ' -f1)
    if [ "$served" != "$ondisk" ]; then
        echo "Server is serving a stale ${asset} (served $served, on disk $ondisk)." >&2
        stale=$((stale + 1))
    fi
    checked=$((checked + 1))
done < <(find web/static \( -name '*.js' -o -name '*.css' \) -not -path '*/vendor/*' | sort)

# A find that matches nothing would report zero stale files and read as a pass —
# the empty-set failure this apparatus has been bitten by before.
if [ "$checked" -eq 0 ]; then
    echo "Freshness check found no assets to compare — is web/static/ built?" >&2
    exit 1
fi
if [ "$stale" -gt 0 ]; then
    echo "${stale} of ${checked} assets are stale." >&2
    echo "The binary embeds web/static/ — rebuild with ./build.sh and try again." >&2
    exit 1
fi

cd e2e
# `playwright-test` is a local wrapper for exactly this command and is the
# interactive entry point AGENTS.md names; CI has no such wrapper, so fall
# through to the bin npm links. (Both @playwright/test and playwright declare a
# `playwright` bin, so which package npm links is a hoisting detail — but the
# bin is stable either way, which a hardcoded node_modules/playwright/cli.js
# path was not.)
if command -v playwright-test > /dev/null 2>&1; then
    playwright-test "$@"
elif [ -x ./node_modules/.bin/playwright ]; then
    ./node_modules/.bin/playwright test "$@"
else
    echo "Playwright is not installed — run 'npm ci' in e2e/ first." >&2
    exit 1
fi
