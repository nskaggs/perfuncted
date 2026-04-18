#!/usr/bin/env bash
set -euo pipefail

echo "▶ pre-commit: running quality checks..."

# 1. Regenerate CLI (ensure autogen is up to date)
go run -tags=gencli ./scripts/gen_cli.go

# 2. Format
if [ -n "$(gofmt -l .)" ]; then
    echo "  FAIL: Files not formatted (run 'go fmt ./...')"
    gofmt -l .
    exit 1
fi

# 3. Vet
go vet ./...

# 4. Unit tests
go test -short ./...

# 5. Build check
go build ./...

echo "✓ pre-commit: all checks passed"
