#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly MARKER_NAME='.lmm-deploy-workspace'
readonly ID_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'

usage() {
  printf 'Usage: %s --role controller|target --deployment-id ID [--root ABSOLUTE_PATH]\n' "${0##*/}" >&2
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 2
}

validate_deployment_id() {
  [[ $1 =~ $ID_PATTERN ]] || die 'invalid deployment ID'
}

reject_unsafe_text() {
  local value=$1
  [[ $value != *$'\n'* && $value != *$'\r'* && $value != *$'\t'* ]] ||
    die 'path contains control characters'
  [[ $value != *'~'* && $value != *'$'* && $value != *'*'* &&
     $value != *'?'* && $value != *'['* && $value != *']'* &&
     $value != *'{'* && $value != *'}'* ]] || die 'path contains unresolved shell syntax or a glob'
}

assert_no_symlink_components() {
  local path=$1
  local current='/'
  local component
  local -a components=()

  IFS='/' read -r -a components <<< "${path#/}"
  for component in "${components[@]}"; do
    [[ -n $component ]] || continue
    if [[ $current == '/' ]]; then
      current="/$component"
    else
      current="$current/$component"
    fi
    [[ ! -L $current ]] || die 'path traverses a symbolic link'
    [[ -e $current ]] || break
  done
}

validate_root() {
  local root=$1
  local canonical

  reject_unsafe_text "$root"
  [[ $root == /* ]] || die 'workspace root must be absolute'
  canonical=$(realpath -m -- "$root")
  [[ $canonical == "$root" ]] || die 'workspace root must be canonical'
  case "$canonical" in
    /|/tmp|/tmp/*|/var/tmp|/var/tmp/*)
      die 'workspace root is too broad or uses a forbidden temporary path'
      ;;
  esac
  assert_no_symlink_components "$canonical"
  printf '%s\n' "$canonical"
}

role=''
deployment_id=''
root=''

while (($# > 0)); do
  case "$1" in
    --role)
      (($# >= 2)) || die 'missing value for --role'
      role=$2
      shift 2
      ;;
    --deployment-id)
      (($# >= 2)) || die 'missing value for --deployment-id'
      deployment_id=$2
      shift 2
      ;;
    --root)
      (($# >= 2)) || die 'missing value for --root'
      root=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die 'unknown argument'
      ;;
  esac
done

case "$role" in
  controller)
    if [[ -z $root ]]; then
      root="${XDG_STATE_HOME:-$HOME/.local/state}/lmm-api/deploy-work"
    fi
    ;;
  target)
    [[ -n $root ]] || root='/var/lib/lmm-api/deploy-work'
    ;;
  *)
    die 'role must be controller or target'
    ;;
esac

[[ -n $deployment_id ]] || die 'missing --deployment-id'
validate_deployment_id "$deployment_id"
root=$(validate_root "$root")

mkdir -p -- "$root"
chmod 0700 -- "$root"
assert_no_symlink_components "$root"
[[ -d $root && ! -L $root ]] || die 'workspace root is not a real directory'
[[ $(realpath -e -- "$root") == "$root" ]] || die 'workspace root changed during creation'

workspace="$root/$deployment_id"
[[ ! -e $workspace && ! -L $workspace ]] || die 'deployment workspace already exists'
mkdir -m 0700 -- "$workspace"

cleanup_incomplete=true
cleanup_on_error() {
  if [[ $cleanup_incomplete == true && -d $workspace && ! -L $workspace ]]; then
    rm -rf -- "$workspace"
  fi
}
trap cleanup_on_error EXIT

mkdir -m 0700 -- \
  "$workspace/artifacts" \
  "$workspace/cache" \
  "$workspace/cache/bun-install" \
  "$workspace/cache/cargo-target" \
  "$workspace/cache/go-build" \
  "$workspace/cache/go-mod" \
  "$workspace/logs" \
  "$workspace/manifests" \
  "$workspace/staging" \
  "$workspace/state" \
  "$workspace/tmp"

created_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
marker="$workspace/$MARKER_NAME"
{
  printf 'format=1\n'
  printf 'deployment_id=%s\n' "$deployment_id"
  printf 'role=%s\n' "$role"
  printf 'workspace=%s\n' "$workspace"
  printf 'created_at_utc=%s\n' "$created_at"
} > "$marker"
chmod 0600 -- "$marker"
printf 'CREATED\n' > "$workspace/state/status"
chmod 0600 -- "$workspace/state/status"

[[ $(realpath -e -- "$workspace") == "$workspace" ]] || die 'workspace changed during creation'
[[ -f $marker && ! -L $marker ]] || die 'workspace marker was not created safely'

cleanup_incomplete=false
trap - EXIT

printf 'LMM_DEPLOY_WORKSPACE=%q\n' "$workspace"
printf 'LMM_DEPLOY_MARKER=%q\n' "$marker"
printf 'LMM_DEPLOY_STATE_FILE=%q\n' "$workspace/state/status"
printf 'TMPDIR=%q\n' "$workspace/tmp"
printf 'GOCACHE=%q\n' "$workspace/cache/go-build"
printf 'GOMODCACHE=%q\n' "$workspace/cache/go-mod"
printf 'CARGO_TARGET_DIR=%q\n' "$workspace/cache/cargo-target"
printf 'BUN_INSTALL_CACHE_DIR=%q\n' "$workspace/cache/bun-install"
