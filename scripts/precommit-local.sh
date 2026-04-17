#!/usr/bin/env bash
set -euo pipefail

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
