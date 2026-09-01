#!/usr/bin/env bash
# A pre-build failure must remain non-zero even though the wrapper tees output.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
runtime=$(mktemp -d /tmp/lmm-route-compatibility-wrapper-test.XXXXXX)
cleanup() { rm -rf "$runtime"; }
trap cleanup EXIT

log_dir="$runtime/log"
oracle_root="$runtime/oracle"
mkdir -p "$oracle_root"
if LMM_GO_ORACLE_ROOT="$oracle_root" \
  LMM_ROUTE_COMPATIBILITY_RUNTIME_BASE="$runtime/absent" \
  LMM_ROUTE_COMPATIBILITY_LOG_DIR="$log_dir" \
  "$repo_root/apps/api-rust/tests/behavior-oracle/run-route-compatibility-local-listeners.sh"; then
  echo "route-compatibility wrapper converted a failing runner into success" >&2
  exit 1
fi

grep -Fq 'runtime base does not exist:' "$log_dir/run.log"
