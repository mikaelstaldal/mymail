# Credits

MyMail itself is licensed under the Apache License 2.0 (see [LICENSE](LICENSE)). It builds on the
third-party work listed below, with thanks to all of its authors.

Versions are those pinned in [`go.mod`](go.mod) and
[`web/ts/vendor/package.json`](web/ts/vendor/package.json) at the time of writing; `go.mod` and the
version-stamped filenames under `web/static/vendor/` are the authoritative record.

---

## Go — direct dependencies

Linked into the distributed `mymail` and/or `mymail-lda` binaries.

| Module | Version | License | Used for |
|--------|---------|---------|----------|
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) | v1.52.0 | BSD-3-Clause | Pure-Go SQLite driver (no CGO) — the only datastore |
| [github.com/ogen-go/ogen](https://github.com/ogen-go/ogen) | v1.22.0 | Apache-2.0 | OpenAPI 3 code generator + generated-server runtime (`internal/api/`) |
| [github.com/microcosm-cc/bluemonday](https://github.com/microcosm-cc/bluemonday) | v1.0.27 | BSD-3-Clause | HTML sanitization of message bodies (`internal/sanitize/`) |
| [github.com/jaytaylor/html2text](https://github.com/jaytaylor/html2text) | (2026-03-03) | MIT | HTML → plain-text conversion for text fallbacks and FTS indexing |
| [github.com/emersion/go-mbox](https://github.com/emersion/go-mbox) | v1.0.4 | MIT | mbox parsing for `-import` mode |
| [github.com/emersion/go-maildir](https://github.com/emersion/go-maildir) | v0.6.0 | MIT | Maildir traversal for `-import` mode |
| [github.com/google/uuid](https://github.com/google/uuid) | v1.6.0 | BSD-3-Clause | Message-ID and identifier generation |
| [github.com/mikaelstaldal/go-server-common](https://github.com/mikaelstaldal/go-server-common) | v1.9.0 | Apache-2.0 | HTTP server helpers: Basic Auth (htpasswd/bcrypt), CSRF Origin/Referer validation |
| [github.com/go-faster/errors](https://github.com/go-faster/errors) | v0.7.1 | BSD-3-Clause | Error wrapping used by the generated API code |
| [github.com/go-faster/jx](https://github.com/go-faster/jx) | v1.2.0 | MIT | Streaming JSON encode/decode used by the generated API code |
| [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) | v0.56.0 | BSD-3-Clause | HTML parsing (`html`, `html/atom`) for sanitization |
| [golang.org/x/text](https://pkg.go.dev/golang.org/x/text) | v0.38.0 | BSD-3-Clause | Charset decoding and encoding of MIME message parts |

### SQLite

`modernc.org/sqlite` embeds a machine translation of the upstream
[SQLite](https://sqlite.org/) C sources. SQLite is released into the
[public domain](https://sqlite.org/copyright.html) by its author, D. Richard Hipp.

## Go — transitive dependencies

Not referenced directly by MyMail's own code, but linked into the binaries.

| Module | Version | License | Pulled in by |
|--------|---------|---------|--------------|
| [github.com/aymerick/douceur](https://github.com/aymerick/douceur) | v0.2.0 | MIT | bluemonday |
| [github.com/gorilla/css](https://github.com/gorilla/css) | v1.0.1 | BSD-3-Clause | bluemonday |
| [github.com/cespare/xxhash/v2](https://github.com/cespare/xxhash) | v2.3.0 | MIT | html2text |
| [github.com/clipperhouse/displaywidth](https://github.com/clipperhouse/displaywidth) | v0.10.0 | MIT | html2text |
| [github.com/clipperhouse/uax29/v2](https://github.com/clipperhouse/uax29) | v2.6.0 | MIT | html2text |
| [github.com/mattn/go-runewidth](https://github.com/mattn/go-runewidth) | v0.0.19 | MIT | html2text |
| [github.com/olekukonko/tablewriter](https://github.com/olekukonko/tablewriter) | v1.1.4 | MIT | html2text |
| [github.com/olekukonko/cat](https://github.com/olekukonko/cat) | (2025-09-11) | MIT | tablewriter |
| [github.com/olekukonko/errors](https://github.com/olekukonko/errors) | v1.2.0 | MIT | tablewriter |
| [github.com/olekukonko/ll](https://github.com/olekukonko/ll) | v0.1.6 | MIT | tablewriter |
| [github.com/ssor/bom](https://github.com/ssor/bom) | (2017-07-18) | MIT | html2text |
| [github.com/dlclark/regexp2](https://github.com/dlclark/regexp2) | v1.12.0 | MIT | ogen (`validate`) |
| [github.com/fatih/color](https://github.com/fatih/color) | v1.19.0 | MIT | ogen |
| [github.com/mattn/go-colorable](https://github.com/mattn/go-colorable) | v0.1.14 | MIT | ogen |
| [github.com/ghodss/yaml](https://github.com/ghodss/yaml) | v1.0.0 | MIT AND BSD-3-Clause | ogen |
| [github.com/go-faster/yaml](https://github.com/go-faster/yaml) | v0.4.6 | MIT AND Apache-2.0 | ogen |
| [gopkg.in/yaml.v2](https://github.com/go-yaml/yaml) | v2.4.0 | Apache-2.0 AND MIT | ogen |
| [github.com/shopspring/decimal](https://github.com/shopspring/decimal) | v1.4.0 | MIT | ogen (`conv`) |
| [go.uber.org/zap](https://github.com/uber-go/zap) | v1.28.0 | MIT | ogen (`middleware`) |
| [go.uber.org/multierr](https://github.com/uber-go/multierr) | v1.11.0 | MIT | zap |
| [github.com/segmentio/asm](https://github.com/segmentio/asm) | v1.2.1 | MIT-0 | go-faster/jx |
| [golang.org/x/exp](https://pkg.go.dev/golang.org/x/exp) | (2025-10-23) | BSD-3-Clause | ogen (`json`) |
| [golang.org/x/sync](https://pkg.go.dev/golang.org/x/sync) | v0.21.0 | BSD-3-Clause | ogen (`http`) |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | v0.53.0 | BSD-3-Clause | go-server-common (bcrypt) |
| [github.com/dustin/go-humanize](https://github.com/dustin/go-humanize) | v1.0.1 | MIT | modernc.org/sqlite |
| [github.com/mattn/go-isatty](https://github.com/mattn/go-isatty) | v0.0.22 | MIT | modernc.org/sqlite |
| [github.com/remyoudompheng/bigfft](https://github.com/remyoudompheng/bigfft) | (2023-01-29) | BSD-3-Clause | modernc.org/sqlite |
| [modernc.org/libc](https://gitlab.com/cznic/libc) | v1.72.3 | BSD-3-Clause | modernc.org/sqlite |
| [modernc.org/mathutil](https://gitlab.com/cznic/mathutil) | v1.7.1 | BSD-3-Clause | modernc.org/sqlite |
| [modernc.org/memory](https://gitlab.com/cznic/memory) | v1.11.0 | BSD-3-Clause | modernc.org/sqlite |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | v0.46.0 | BSD-3-Clause | modernc.org/sqlite, libc |

## Go — test-only dependencies

Used by `go test ./...`; not linked into the distributed binaries.

| Module | Version | License | Used for |
|--------|---------|---------|----------|
| [github.com/stretchr/testify](https://github.com/stretchr/testify) | v1.11.1 | MIT | Test assertions (`assert`, `require`) |
| [github.com/davecgh/go-spew](https://github.com/davecgh/go-spew) | v1.1.1 | ISC | testify |
| [github.com/pmezard/go-difflib](https://github.com/pmezard/go-difflib) | v1.0.0 | BSD-3-Clause | testify |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | v3.0.1 | MIT AND Apache-2.0 | testify |

## Go toolchain and standard library

Built with the [Go](https://go.dev/) toolchain and standard library — BSD-3-Clause,
Copyright The Go Authors.

---

## Browser (JavaScript/CSS) — vendored

Vendored (no CDN, no bundler) under `web/static/vendor/` by the maintainer script
`web/ts/vendor/rebuild.sh`. Preact and Quill ship prebuilt and self-contained and are copied
verbatim; the Lucide bundle is generated by `web/ts/vendor/gen-lucide.mjs`, which copies the icon
geometry of just the icons the UI names out of `lucide-static`. Type stubs for Preact are vendored
under `web/ts/vendor/preact/`, and the one for the Lucide bundle is `web/ts/vendor/lucide.d.ts`.

| Library | Version | License | Vendored as | Used for |
|---------|---------|---------|-------------|----------|
| [Preact](https://preactjs.com/) | 10.29.7 | MIT | `vendor/preact/preact-10.29.7.module.js`, `hooks-10.29.7.module.js`, `jsx-runtime-10.29.7.module.js` | UI component runtime, hooks, JSX runtime |
| [Quill](https://quilljs.com/) | 2.0.3 | BSD-3-Clause | `vendor/quill/quill-2.0.3.js`, `quill-2.0.3.snow.css` | Rich-text (HTML) message composer, Snow theme |
| [Lucide](https://lucide.dev/) (`lucide-static`) | 1.25.0 | ISC | `vendor/lucide/lucide-1.25.0.js` | The UI's icon set, rendered inline as SVG by `components/Icon.tsx` |

The prebuilt `quill-2.0.3.js` has Quill's own dependencies bundled inside it, so these are
redistributed as part of that file:

| Library | Version | License |
|---------|---------|---------|
| [parchment](https://github.com/slab/parchment) | 3.0.0 | BSD-3-Clause |
| [quill-delta](https://github.com/slab/delta) | 5.1.0 | MIT |
| [eventemitter3](https://github.com/primus/eventemitter3) | 5.0.4 | MIT |
| [lodash-es](https://github.com/lodash/lodash) | 4.18.1 | MIT |
| [fast-diff](https://github.com/jhchen/fast-diff) | 1.3.0 | Apache-2.0 |


## Build-time tools

Required to build from source but not linked into or redistributed with the binaries. See
[`build.sh`](build.sh) and `web/ts/vendor/rebuild.sh`.

| Tool | License | Used for |
|------|---------|----------|
| [Go](https://go.dev/) | BSD-3-Clause | Compiling the Go binaries |
| [TypeScript](https://www.typescriptlang.org/) (`tsc`) | Apache-2.0 | Compiling `web/ts/` → `web/static/` |
| [openapi-typescript](https://github.com/openapi-ts/openapi-typescript) | MIT | Generating `web/ts/api/types.ts` from `openapi.yaml` |
| [ogen](https://github.com/ogen-go/ogen) | Apache-2.0 | Generating `internal/api/` from `openapi.yaml` |
| [golangci-lint](https://github.com/golangci/golangci-lint) v2 | GPL-3.0 | Linting the Go sources |
| [Node.js](https://nodejs.org/) + [npm](https://www.npmjs.com/) | MIT | Fetching pinned upstream sources in `rebuild.sh` (maintainer only) |
