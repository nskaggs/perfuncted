#!/usr/bin/env bash
# scripts/test-session.sh — tests the session lifecycle integration suite.
#
# Usage:
#   bash scripts/test-session.sh

set -euo pipefail
cd "$(dirname "$0")/.."

echo "▶ running session lifecycle test..."
PF_TEST_DISPLAY_SERVER=wayland go test -tags=integration ./integration -run TestSessionLifecycle -count=1
