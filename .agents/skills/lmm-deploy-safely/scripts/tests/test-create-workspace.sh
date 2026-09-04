#!/usr/bin/env bash

set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly script_dir
readonly create_workspace=${script_dir%/scripts/tests}/scripts/create-workspace.sh
readonly test_base="${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/skill-tests/create-workspace-$$"

fail() {
  printf 'FAIL: %s\n' "$1" >&2
  exit 1
}

grep -Fq "root='/var/lib/lmm-api-go-deploy/work'" "$create_workspace" || fail 'target default root drifted from the production path map'

cleanup() {
  rm -rf -- "$test_base"
}
trap cleanup EXIT

mkdir -p -- "$test_base"

basic_root="$test_base/basic/lmm-api/deploy-work"
basic_output=$(bash "$create_workspace" --role controller --deployment-id basic --root "$basic_root")
[[ -f $basic_root/basic/.lmm-deploy-workspace ]] || fail 'basic workspace marker missing'
[[ -f $basic_root/basic/state/status ]] || fail 'basic workspace state missing'
grep -Fq "LMM_DEPLOY_WORKSPACE=$basic_root/basic" <<< "$basic_output" || fail 'basic output missing workspace path'

warning_state="$test_base/warning/lmm-api"
warning_root="$warning_state/deploy-work"
mkdir -p -- "$warning_state"
truncate -s $((257 * 1024 * 1024)) "$warning_state/pressure.bin"
warning_error="$test_base/warning.err"
bash "$create_workspace" --role controller --deployment-id warning --root "$warning_root" 2> "$warning_error"
[[ -d $warning_root/warning ]] || fail 'warning-sized state root should still allow creation'
grep -Fq 'warning: state root uses' "$warning_error" || fail 'warning-sized state root did not emit warning'

stop_state="$test_base/stop/lmm-api"
stop_root="$stop_state/deploy-work"
mkdir -p -- "$stop_state"
truncate -s $((513 * 1024 * 1024)) "$stop_state/pressure.bin"
stop_error="$test_base/stop.err"
if bash "$create_workspace" --role controller --deployment-id blocked --root "$stop_root" 2> "$stop_error"; then
  fail 'stop-sized state root unexpectedly allowed creation'
fi
[[ ! -e $stop_root/blocked ]] || fail 'blocked workspace was partially created'
grep -Fq 'clean terminal marker-owned workspaces' "$stop_error" || fail 'stop error lacks cleanup guidance'

target_state="$test_base/target/lmm-api-go-deploy"
target_root="$target_state/work"
mkdir -p -- "$target_state/backups"
truncate -s $((513 * 1024 * 1024)) "$target_state/backups/retained-backup.bin"
target_output=$(bash "$create_workspace" --role target --deployment-id target-budget --root "$target_root")
[[ -f $target_root/target-budget/.lmm-deploy-workspace ]] || fail 'target workspace was blocked by retained sibling backups'
grep -Fq "LMM_DEPLOY_WORKSPACE=$target_root/target-budget" <<< "$target_output" || fail 'target output missing workspace path'

printf 'create-workspace tests: PASS\n'
