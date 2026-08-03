#!/usr/bin/env bash

# On success this script is silent (no stdout/stderr) and exits 0.
# On failure it prints the failing step's output to stderr and exits non-zero.
set -euo pipefail

OUTPUT_DIR="."
while getopts "o:" opt; do
  case $opt in
    o)
      OUTPUT_DIR="$OPTARG"
      ;;
    \?)
      echo "Invalid option: -$OPTARG" >&2
      exit 1
      ;;
  esac
done

# Run a build step silently; on failure, print its combined output to stderr
# and abort with a non-zero exit code.
run() {
  local output
  if ! output=$("$@" 2>&1); then
    printf 'build.sh: step failed: %s\n' "$*" >&2
    printf '%s\n' "$output" >&2
    exit 1
  fi
}

run openapi-typescript openapi.yaml -o web/ts/api/types.ts
run tsc --project web/ts/tsconfig.json

# Compile the demo-mode service worker (worker code, built against the WebWorker
# lib rather than the DOM one — see web/ts/demo/tsconfig.json).
run tsc --project web/ts/demo/tsconfig.json

# Unpack the committed jsdom install tree (idempotent — a no-op once unpacked),
# then run the frontend tests against the compiled output in web/static/.
# tar only; no npm/npx/yarn — see web/ts/vendor/test/unpack.sh.
run web/ts/vendor/test/unpack.sh
run node --test web/ts/quotetext.test.mjs web/ts/wrap.test.mjs web/ts/address.test.mjs web/ts/signature.test.mjs web/ts/confirm.test.mjs web/ts/demo.test.mjs

run go generate ./...
run go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail" .
run go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail-lda" ./cmd/lda/
run go test ./...
run golangci-lint run ./...
