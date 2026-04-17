#!/usr/bin/env bash
set -euo pipefail

git config core.hooksPath .githooks
echo "Set core.hooksPath to .githooks"
