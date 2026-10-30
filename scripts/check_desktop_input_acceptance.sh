#!/usr/bin/env bash
# Safe read-only live verification. Pass PF_BIN to use a freshly built CLI.
set -euo pipefail
PF_BIN="${PF_BIN:-$(cd "$(dirname "$0")/.." && pwd)/pf}"
"$PF_BIN" info
"$PF_BIN" output list
"$PF_BIN" window list
"$PF_BIN" input coordinate-space || echo 'Pointer coordinate space unavailable; window-relative clicks must fail closed.'
