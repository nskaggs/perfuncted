#!/usr/bin/env bash
# Legacy entrypoint kept for existing local workflows.

set -euo pipefail
cd "$(dirname "$0")/../.."

PF_TEST_DISPLAY_SERVER=nested go test -tags=integration ./integration -count=1
