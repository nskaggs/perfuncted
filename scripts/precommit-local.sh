#!/usr/bin/env bash
set -euo pipefail

# Generate CLI autogen (ensure cmd/pf builds locally)
echo "Generating autogen code..."
go run -tags=gencli ./scripts/gen_cli.go

# formatting checks
gofmt_out=$(gofmt -l .)
if [ -n "$gofmt_out" ]; then
  echo "Files not formatted:"
  echo "$gofmt_out"
  exit 1
fi

# fast static checks
go vet ./...

# run tests for most packages but skip cmd/pf (auto-generated CLI artifacts may be missing)
pkgs=$(go list ./... | grep -v "cmd/pf")
if [ -z "$pkgs" ]; then
  exit 0
fi

echo "$pkgs" | xargs -r go test
