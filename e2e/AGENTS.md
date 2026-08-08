# MyMail — End-to-end tests

Instructions for working under `e2e/`: the Playwright suite and the `test-e2e.sh` script that
runs it. The repo-root `AGENTS.md` is always loaded as well and covers everything else. File
paths here are written relative to the repo root, as they are there.

`tests/sidebar-footer.spec.ts` is this repo's whole half of the cross-repo sidebar-footer
contract. **The rules that say which of its values may change, and why, are not here** — they
are in `../mysuite/spec/sidebar-footer.md`, and the list of ordinary-looking edits that break
it silently is in `web/AGENTS.md`. Read those before changing an assertion; a value in this
suite is almost never MyMail's to decide.

**Nothing runs this suite but a person.** The CI step is committed and has never executed — the
workflow triggers on push to `main` and this work is on an unpushed branch — so a change you make
here is verified by you running it and by nothing else. That is a status fact rather than a
mechanic, which is why it is stated in the root `AGENTS.md` too; the mechanics below are not
repeated anywhere.

## Running one spec

The root `AGENTS.md` has the command for the whole suite; it is not repeated here. What it does
not say: `test-e2e.sh` passes its arguments straight through to `playwright test`, so
`./test-e2e.sh tests/sidebar-footer.spec.ts -g "focus"` works, and it still does the fresh
database, the fresh server and the served-vs-disk check around that one spec.

*Important:* interactively, use the `playwright-test` command from `e2e/` and nothing else —
do not invent variants. `test-e2e.sh` falls back to `./node_modules/.bin/playwright test` when
that wrapper is absent, which is the case in CI; that fallback is sanctioned and is the only
one. `e2e/package.json`'s `npm test` / `npm run test:headed` exist for parity with the sibling
repos and are **not** that path — they run `playwright test` against a server you must already
have started yourself, with no port check and no freshness check, so they skip everything the
next section is about.

## Why not start a server by hand

Prefer the script. The three things it exists to prevent are easy to hit and none of them
announces itself:

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
