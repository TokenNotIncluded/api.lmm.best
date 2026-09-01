#!/usr/bin/env bash
# Locks the deliberately narrow normalization contract used by the real
# transaction differential without creating databases or listeners.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
runner="$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-transaction-differential.sh"

[[ -x $runner ]] || { echo "transaction differential runner is not executable: $runner" >&2; exit 1; }
LMM_TRANSACTION_CANONICAL_JSON_SELF_TEST=1 "$runner"
