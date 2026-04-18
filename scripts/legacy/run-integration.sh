#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/../.."

echo "Running integration suite: headless, nested, desktop"
RC=0

echo "=== headless ==="
if bash scripts/test-wayland.sh headless; then
    echo "headless: OK"
else
    echo "headless: FAIL"
    RC=1
fi

echo "=== nested ==="
if bash scripts/test-wayland.sh nested; then
    echo "nested: OK"
else
    echo "nested: FAIL"
    RC=1
fi

echo "=== desktop ==="
if bash scripts/test-desktop.sh; then
    echo "desktop: OK"
else
    echo "desktop: FAIL"
    RC=1
fi

exit $RC
