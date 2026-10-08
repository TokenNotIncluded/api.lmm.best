#!/usr/bin/env bash
set -Eeuo pipefail
ROOT=$(git rev-parse --show-toplevel)
exec python3 -B "$ROOT/scripts/test-local-release-tests.py"
