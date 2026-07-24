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
run go generate ./...
run go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail" .
run go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail-lda" ./cmd/lda/
run go test ./...
run golangci-lint run ./...
