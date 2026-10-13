# MyMail development guide

[Project overview](../README.md) · [User manual](USER_MANUAL.md)

See [AGENTS.md](../AGENTS.md) for build prerequisites, architecture, and development conventions.

## Technology

| Layer   | Technology                                                            |
|---------|-----------------------------------------------------------------------|
| Backend | Go, SQLite (via `modernc.org/sqlite`)                                 |
| API     | OpenAPI 3 contract, generated server stubs (`ogen`)                   |
| Web UI  | TypeScript, Preact, Quill (vendored; no CDN dependency)               |
| Auth    | HTTP Basic Auth (htpasswd/bcrypt), CSRF via Origin/Referer validation |

## Building from Source

**Prerequisites:** Go (at least the version in [go.mod](../go.mod)), `tsc` (TypeScript compiler),
`openapi-typescript`, `ogen`, `golangci-lint`, and `node`.

```bash
# Full build: TypeScript + Go binary + tests + lint
./build.sh

# Go binary only (requires web/static/*.js already compiled)
go build -tags netgo

# TypeScript only
tsc --project web/ts/tsconfig.json

# Run tests
go test ./...
```

After editing `openapi.yaml`, regenerate the Go server stubs:

```bash
go generate ./internal
```
