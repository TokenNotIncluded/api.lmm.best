#!/usr/bin/env bash
set -Eeuo pipefail

# Tests run locally. Publication verifies their record without polling Actions.
ROOT=$(git rev-parse --show-toplevel)
exec python3 -B "$ROOT/scripts/local-release-tests.py" verify "$@"
