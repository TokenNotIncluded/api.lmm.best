#!/usr/bin/env bash

set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly script_dir
readonly cleanup=${script_dir%/scripts/tests}/scripts/cleanup-owned-workspace.sh
if ! grep -Fq "root='/var/lib/lmm-api-go-deploy/work'" "$cleanup"; then
  printf 'FAIL: target default root drifted from the production path map\n' >&2
  exit 1
fi
test_base=${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/skill-tests
mkdir -p "$test_base"
test_root=$(mktemp -d -p "$test_base")
root=$test_root/workspaces
mkdir -p "$root"
trap 'rm -rf -- "$test_root"' EXIT

deployment_id='cleanup-contract-01'
readonly deployment_id
workspace=$root/$deployment_id
mkdir -p "$workspace"/{state,artifacts,staging,tmp,cache,packages,aur}
printf 'payload\n' >"$workspace/artifacts/package"
printf 'payload\n' >"$workspace/staging/package"
printf 'payload\n' >"$workspace/tmp/file"
printf 'payload\n' >"$workspace/cache/file"
printf 'payload\n' >"$workspace/packages/package"
printf 'payload\n' >"$workspace/aur/checkout"
mkdir -p "$workspace/cache/read-only/module"
printf 'module\n' >"$workspace/cache/read-only/module/go.mod"
chmod -R u-w "$workspace/cache/read-only"
cat >"$workspace/.lmm-deploy-workspace" <<EOF
format=1
deployment_id=$deployment_id
role=target
created_at_utc=2026-08-14T00:00:00Z
EOF
printf 'ROLLED_BACK reason=test\n' >"$workspace/state/status"

output=$("$cleanup" --role target --deployment-id "$deployment_id" --root "$root")
grep -Fq 'final_state=ROLLED_BACK' <<<"$output"
output=$("$cleanup" --role target --deployment-id "$deployment_id" --root "$root" --execute)
grep -Fq 'removed=artifacts,staging,tmp,cache,packages,aur' <<<"$output"
[[ -d $workspace && -f $workspace/.lmm-deploy-workspace && -f $workspace/state/status ]]
[[ ! -e $workspace/artifacts && ! -e $workspace/staging && ! -e $workspace/tmp && ! -e $workspace/cache && ! -e $workspace/packages && ! -e $workspace/aur ]]

aborted_id='cleanup-contract-aborted'
aborted=$root/$aborted_id
mkdir -p "$aborted"/{state,artifacts,staging,tmp,cache,backups}
printf 'payload\n' >"$aborted/cache/file"
printf 'verified backup proof\n' >"$aborted/backups/proof"
cat >"$aborted/.lmm-deploy-workspace" <<EOF
format=1
deployment_id=$aborted_id
role=controller
workspace=$aborted
created_at_utc=2026-08-14T00:00:00Z
EOF
printf 'ABORTED reason=pre-switch-test\n' >"$aborted/state/status"
output=$("$cleanup" --role controller --deployment-id "$aborted_id" --root "$root")
grep -Fq 'final_state=ABORTED' <<<"$output"
output=$("$cleanup" --role controller --deployment-id "$aborted_id" --root "$root" --execute)
grep -Fq 'removed=artifacts,staging,tmp,cache,backups' <<<"$output"
[[ ! -e $aborted/cache && ! -e $aborted/backups ]]

validated_id='cleanup-contract-validated'
validated=$root/$validated_id
mkdir -p "$validated"/{state,artifacts,staging,tmp,cache}
printf 'payload\n' >"$validated/cache/file"
cat >"$validated/.lmm-deploy-workspace" <<EOF
format=1
deployment_id=$validated_id
role=controller
workspace=$validated
created_at_utc=2026-08-14T00:00:00Z
EOF
printf 'VALIDATED reason=pre-release-tests-complete no-switch=true processes=stopped\n' >"$validated/state/status"
output=$("$cleanup" --role controller --deployment-id "$validated_id" --root "$root")
grep -Fq 'final_state=VALIDATED' <<<"$output"
output=$("$cleanup" --role controller --deployment-id "$validated_id" --root "$root" --execute)
grep -Fq 'removed=artifacts,staging,tmp,cache' <<<"$output"
[[ ! -e $validated/cache ]]

printf 'cleanup retains terminal audit state and removes only disposable children, including pre-switch validation and aborts\n'
