#!/usr/bin/env bash

set -euo pipefail
umask 077

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
readonly SCRIPT="$SCRIPT_DIR/verify-backup-set.sh"
readonly TEST_STATE_ROOT=${XDG_STATE_HOME:-$HOME/.local/state}
mkdir -p -- "$TEST_STATE_ROOT"
TEST_ROOT=$(mktemp -d "$TEST_STATE_ROOT/lmm-backup-set-test.XXXXXX")
readonly TEST_ROOT
trap 'rm -rf -- "$TEST_ROOT"' EXIT
readonly DEPLOYMENT_ID='backup-policy-test'
passed=0

fail() {
  printf 'test error: %s\n' "$1" >&2
  exit 1
}

make_fixture() {
  local directory=$1 role=$2 copy_role=$3
  local kind file checksum
  mkdir -p -- "$directory"

  # Synthetic bytes test metadata/checksums only, not real encryption or restore.
  for kind in application frontend configuration database; do
    printf 'synthetic %s fixture\n' "$kind" >"$directory/$kind.archive"
  done
  checksum=$(sha256sum -- "$directory/application.archive")
  checksum=${checksum%% *}
  cat >"$directory/manifest.env" <<EOF
format=1
created_at_utc=2026-09-06T00:00:00Z
deployment_id=$DEPLOYMENT_ID
copy_role=$copy_role
deployment_role=$role
verified_host=arch-dmit
release_id=web-v1.2.3
artifact_sha256=$checksum
git_revision=abcdef0123456789
database_engine=postgres
service_state=active
frontend_release=web-v1.2.3
configuration_encrypted=true
database_encrypted=true
EOF
  for kind in application frontend configuration database; do
    file="$directory/$kind.archive"
    {
      printf '%s_file=%s.archive\n' "$kind" "$kind"
      printf '%s_size=%s\n' "$kind" "$(stat -c '%s' -- "$file")"
      printf '%s_mode=%s\n' "$kind" "$(stat -c '%a' -- "$file")"
      printf '%s_mtime_utc=%s\n' "$kind" "$(date -u -r "$file" '+%Y-%m-%dT%H:%M:%SZ')"
    } >>"$directory/manifest.env"
  done
  (
    cd -- "$directory"
    sha256sum -- application.archive frontend.archive configuration.archive database.archive >SHA256SUMS
  )
}

expect_success() {
  local policy=$1 output
  shift
  output=$(bash "$SCRIPT" "$@" 2>&1) || fail "$output"
  grep -Fxq -- 'verified_copy=controller' <<<"$output" || fail 'controller was not verified'
  grep -Fxq -- "backup_policy=$policy" <<<"$output" || fail "unexpected policy: $output"
  grep -Fxq -- 'backup_set=verified' <<<"$output" || fail 'backup set was not verified'
  if [[ $policy == controller-plus-off-host ]]; then
    grep -Fxq -- 'verified_copy=off-host' <<<"$output" || fail 'requested off-host copy was not verified'
  elif grep -Fq -- 'verified_copy=off-host' <<<"$output"; then
    fail 'controller-only verification included an off-host copy'
  fi
  passed=$((passed + 1))
}

expect_failure() {
  local message=$1 output status=0
  shift
  output=$(bash "$SCRIPT" "$@" 2>&1) || status=$?
  [[ $status == 2 ]] || fail "expected exit 2, got $status: $output"
  [[ $output == *"$message"* ]] || fail "missing '$message': $output"
  [[ $output != *'backup_set=verified'* ]] || fail 'failed backup set reported success'
  passed=$((passed + 1))
}

bash -n "$SCRIPT"
bash -n "$0"

# No target/off-host copy is needed for any deployment role.
for role in local test production; do
  make_fixture "$TEST_ROOT/$role" "$role" controller
  expect_success controller-only --role "$role" --deployment-id "$DEPLOYMENT_ID" \
    --copy "controller=$TEST_ROOT/$role"
done

controller="$TEST_ROOT/production"
off_host="$TEST_ROOT/off-host"
make_fixture "$off_host" production off-host
readonly -a BASE=(--role production --deployment-id "$DEPLOYMENT_ID")

expect_success controller-plus-off-host "${BASE[@]}" \
  --copy "controller=$controller" --copy "off-host=$off_host"
expect_success controller-plus-off-host "${BASE[@]}" --require-off-host \
  --copy "off-host=$off_host" --copy "controller=$controller"
expect_failure 'missing required controller backup copy' "${BASE[@]}"
expect_failure 'missing required controller backup copy' "${BASE[@]}" --copy "off-host=$off_host"
expect_failure 'missing requested off-host backup copy' "${BASE[@]}" \
  --copy "controller=$controller" --require-off-host
expect_failure 'target backup copies are disabled' "${BASE[@]}" \
  --copy "controller=$controller" --copy "target=$TEST_ROOT/must-not-exist"
expect_failure 'duplicate backup copy role' "${BASE[@]}" \
  --copy "controller=$controller" --copy "controller=$controller"
expect_failure 'mismatched deployment role' "${BASE[@]}" --copy "controller=$TEST_ROOT/test"
expect_failure 'different deployment ID' --role production --deployment-id other-id \
  --copy "controller=$controller"

cp -a -- "$off_host" "$TEST_ROOT/wrong-identity"
sed -i 's/^verified_host=.*/verified_host=other-host/' "$TEST_ROOT/wrong-identity/manifest.env"
expect_failure 'identity disagrees with another copy' "${BASE[@]}" --require-off-host \
  --copy "controller=$controller" --copy "off-host=$TEST_ROOT/wrong-identity"

cp -a -- "$controller" "$TEST_ROOT/plaintext"
sed -i 's/^configuration_encrypted=true$/configuration_encrypted=false/' "$TEST_ROOT/plaintext/manifest.env"
expect_failure 'does not mark secret-bearing archives as encrypted' "${BASE[@]}" \
  --copy "controller=$TEST_ROOT/plaintext"

cp -a -- "$controller" "$TEST_ROOT/tampered"
printf X | dd of="$TEST_ROOT/tampered/application.archive" bs=1 count=1 conv=notrunc status=none
touch -r "$controller/application.archive" "$TEST_ROOT/tampered/application.archive"
expect_failure 'checksum verification failed' "${BASE[@]}" --copy "controller=$TEST_ROOT/tampered"

cp -a -- "$off_host" "$TEST_ROOT/empty-off-host"
: >"$TEST_ROOT/empty-off-host/database.archive"
expect_failure 'missing, empty, or unsafe archive' "${BASE[@]}" \
  --copy "controller=$controller" --copy "off-host=$TEST_ROOT/empty-off-host"

ln -s -- "$controller" "$TEST_ROOT/linked-copy"
expect_failure 'backup copy path must be canonical' "${BASE[@]}" \
  --copy "controller=$TEST_ROOT/linked-copy"
cp -a -- "$controller" "$TEST_ROOT/linked-archive"
rm -- "$TEST_ROOT/linked-archive/configuration.archive"
ln -s -- "$controller/configuration.archive" "$TEST_ROOT/linked-archive/configuration.archive"
expect_failure 'missing, empty, or unsafe archive' "${BASE[@]}" \
  --copy "controller=$TEST_ROOT/linked-archive"
expect_failure 'forbidden temporary path' "${BASE[@]}" --copy controller=/tmp

printf 'verify-backup-set tests: PASS (%s cases)\n' "$passed"
