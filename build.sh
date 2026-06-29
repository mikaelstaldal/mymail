#!/usr/bin/env bash
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

openapi-typescript openapi.yaml -o web/ts/api/types.ts
tsc --project web/ts/tsconfig.json
go generate ./...
go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail" .
go build -trimpath -buildvcs=true -tags netgo -o "$OUTPUT_DIR/mymail-lda" ./cmd/lda/
go test ./...
golangci-lint run ./...
