#!/usr/bin/env bash

set -euo pipefail
umask 077

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
readonly SCRIPT="$SCRIPT_DIR/inspect-state.sh"
readonly TEST_STATE_ROOT=${XDG_STATE_HOME:-$HOME/.local/state}
mkdir -p -- "$TEST_STATE_ROOT"
TEST_ROOT=$(mktemp -d "$TEST_STATE_ROOT/lmm-inspect-state-test.XXXXXX")
readonly TEST_ROOT
trap 'rm -rf -- "$TEST_ROOT"' EXIT

die() {
  printf 'test error: %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local needle=$1 file=$2
  grep -F -- "$needle" "$file" >/dev/null || die "missing '$needle' in $file"
}

mkdir -p -- \
  "$TEST_ROOT/etc/lmm-api-go" \
  "$TEST_ROOT/usr/bin" \
  "$TEST_ROOT/usr/lib/systemd/system" \
  "$TEST_ROOT/var/lib/lmm-api-cutover" \
  "$TEST_ROOT/var/log/lmm-api-cutover"
printf 'SQL_DSN=postgresql://redacted\n' >"$TEST_ROOT/etc/lmm-api-go/lmm-api-go.env"
printf 'fixture\n' >"$TEST_ROOT/usr/bin/lmm-api-go"
printf 'fixture\n' >"$TEST_ROOT/usr/bin/lmm-api"
printf 'fixture\n' >"$TEST_ROOT/usr/lib/systemd/system/lmm-api.service"
cat >"$TEST_ROOT/var/lib/lmm-api-cutover/pg-write-boundary" <<'EOF'
transaction=cutover-1 schema=lmm_prod revision=go-r1 candidate_sha256=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
EOF
cat >"$TEST_ROOT/var/lib/lmm-api-cutover/cutover-journal" <<'EOF'
version=1 transaction=cutover-1 phase=COMPLETE schema=lmm_prod revision=go-r1
EOF
cat >"$TEST_ROOT/var/log/lmm-api-cutover/post-cutover-verify.json" <<'EOF'
{"status":"verified","database_engine":"postgresql","historical_migration_verified":true,"transaction":"cutover-1","schema":"lmm_prod"}
EOF

bash -n "$SCRIPT"
bash -n "$0"

output="$TEST_ROOT/verified.kv"
"$SCRIPT" --role production --root-prefix "$TEST_ROOT" \
  --expected-host arch-dmit --observed-host arch-dmit >"$output"
assert_contains 'db_engine=postgres' "$output"
assert_contains 'cutover_state=verified' "$output"
assert_contains 'pg_write_boundary_present=true' "$output"
assert_contains 'cutover_journal_present=true' "$output"
assert_contains 'post_cutover_verify_present=true' "$output"

sed -i 's/phase=COMPLETE/phase=FAILED/' "$TEST_ROOT/var/lib/lmm-api-cutover/cutover-journal"
invalid_output="$TEST_ROOT/invalid.kv"
status=0
"$SCRIPT" --role production --root-prefix "$TEST_ROOT" \
  --expected-host arch-dmit --observed-host arch-dmit >"$invalid_output" || status=$?
[[ $status == 4 ]] || die "invalid cutover exited $status instead of 4"
assert_contains 'cutover_state=invalid' "$invalid_output"

printf 'SQL_DSN=sqlite:///state.db\n' >"$TEST_ROOT/etc/lmm-api-go/lmm-api-go.env"
sqlite_output="$TEST_ROOT/sqlite.kv"
"$SCRIPT" --role local --root-prefix "$TEST_ROOT" \
  --observed-host workstation >"$sqlite_output"
assert_contains 'db_engine=sqlite' "$sqlite_output"
assert_contains 'cutover_state=not_required' "$sqlite_output"

printf 'inspect-state tests: PASS\n'
