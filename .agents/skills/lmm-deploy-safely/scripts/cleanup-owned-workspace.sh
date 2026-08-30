#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly MARKER_NAME='.lmm-deploy-workspace'
readonly ID_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'

usage() {
  printf 'Usage: %s --role controller|target --deployment-id ID [--root ABSOLUTE_PATH] [--execute]\n' "${0##*/}" >&2
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 2
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
    [[ -e $current ]] || die 'path does not exist'
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
    /|/home|/var|/var/lib|/srv|/opt|/usr|/etc|/tmp|/tmp/*|/var/tmp|/var/tmp/*|/var/lib/lmm-api|/var/lib/lmm-api/deploy-backups|/var/lib/lmm-api/deploy-staging|/srv/lmm-api-frontend|/srv/lmm-api-frontend/releases|*/backup|*/backup/*|*/backups|*/backups/*|*/deploy-backups|*/deploy-backups/*|*/releases|*/releases/*)
      die 'workspace root is broad, a backup/release root, or a forbidden temporary path'
      ;;
  esac
  if [[ -n ${HOME:-} && $canonical == "$HOME" ]]; then
    die 'workspace root must not be the home directory'
  fi
  assert_no_symlink_components "$canonical"
  [[ -d $canonical && ! -L $canonical ]] || die 'workspace root is not a real directory'
  printf '%s\n' "$canonical"
}

read_final_state() {
  local workspace=$1
  local role=$2
  local text_state="$workspace/state/status"
  local json_state="$workspace/state/status.json"
  local state

  if [[ -f $text_state && ! -L $text_state ]]; then
    state=$(<"$text_state")
    state=${state%% *}
  elif [[ $role == target && -f $json_state && ! -L $json_state ]]; then
    command -v jq >/dev/null 2>&1 || die 'jq is required to read target deployment state'
    state=$(jq -er '.phase | select(type == "string")' "$json_state") ||
      die 'target deployment state is malformed'
  else
    die 'durable deployment state is missing or unsafe'
  fi
  [[ $state =~ ^[A-Z][A-Z_]*$ ]] || die 'durable deployment state is malformed'
  printf '%s\n' "$state"
}

read_marker() {
  local marker=$1
  local line key value
  declare -gA marker_data=()

  [[ -f $marker && ! -L $marker ]] || die 'workspace marker is missing or unsafe'
  while IFS= read -r line || [[ -n $line ]]; do
    [[ $line == *=* ]] || die 'workspace marker is malformed'
    key=${line%%=*}
    value=${line#*=}
    case "$key" in format|deployment_id|role|workspace|created_at_utc) ;; *) die 'workspace marker contains an unknown key' ;; esac
    [[ ! -v marker_data[$key] ]] || die 'workspace marker contains a duplicate key'
    marker_data[$key]=$value
  done < "$marker"
  for key in format deployment_id role created_at_utc; do
    [[ -v marker_data[$key] ]] || die 'workspace marker is incomplete'
  done
}

role=''
deployment_id=''
root=''
execute=false

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
    --execute)
      execute=true
      shift
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
  *) die 'role must be controller or target' ;;
esac
[[ $deployment_id =~ $ID_PATTERN ]] || die 'invalid deployment ID'

root=$(validate_root "$root")
workspace="$root/$deployment_id"
[[ -d $workspace && ! -L $workspace ]] || die 'exact deployment workspace does not exist safely'
assert_no_symlink_components "$workspace"
[[ $(realpath -e -- "$workspace") == "$workspace" ]] || die 'workspace is not canonical'

marker="$workspace/$MARKER_NAME"
read_marker "$marker"
[[ ${marker_data[format]} == 1 ]] || die 'workspace marker format is unsupported'
[[ ${marker_data[deployment_id]} == "$deployment_id" ]] || die 'workspace marker deployment ID mismatch'
[[ ${marker_data[role]} == "$role" ]] || die 'workspace marker role mismatch'
if [[ -v marker_data[workspace] ]]; then
  [[ ${marker_data[workspace]} == "$workspace" ]] || die 'workspace marker path mismatch'
elif [[ $role != target ]]; then
  die 'controller workspace marker path is missing'
fi
[[ ${marker_data[created_at_utc]} =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$ ]] ||
  die 'workspace marker timestamp is invalid'

final_state=$(read_final_state "$workspace" "$role")
case "$final_state" in
  CONFIRMED|ROLLED_BACK|ABORTED|FAILED_PREARM) ;;
  VALIDATED)
    [[ $role == controller ]] || die 'VALIDATED cleanup is limited to controller workspaces'
    ;;
  *) die 'workspace is not in a cleanup-safe terminal state' ;;
esac

marker_checksum=$(sha256sum -- "$marker")
marker_checksum=${marker_checksum%% *}

if [[ $execute == false ]]; then
  printf 'cleanup=preview\n'
  printf 'deployment_id=%s\n' "$deployment_id"
  printf 'role=%s\n' "$role"
  printf 'final_state=%s\n' "$final_state"
  printf 'workspace=%q\n' "$workspace"
  exit 0
fi

[[ -d $workspace && ! -L $workspace ]] || die 'workspace changed before deletion'
[[ $(realpath -e -- "$workspace") == "$workspace" ]] || die 'workspace changed before deletion'
current_marker_checksum=$(sha256sum -- "$marker")
current_marker_checksum=${current_marker_checksum%% *}
[[ $current_marker_checksum == "$marker_checksum" ]] || die 'workspace marker changed before deletion'
read_marker "$marker"
[[ ${marker_data[deployment_id]} == "$deployment_id" && ${marker_data[role]} == "$role" ]] ||
  die 'workspace ownership changed before cleanup'
if [[ -v marker_data[workspace] ]]; then
  [[ ${marker_data[workspace]} == "$workspace" ]] || die 'workspace ownership changed before cleanup'
fi
final_state=$(read_final_state "$workspace" "$role")
case "$final_state" in
  CONFIRMED|ROLLED_BACK|ABORTED|FAILED_PREARM) ;;
  VALIDATED)
    [[ $role == controller ]] || die 'deployment state changed before deletion'
    ;;
  *) die 'deployment state changed before deletion' ;;
esac

removed=none
for name in artifacts staging tmp cache packages aur; do
  path="$workspace/$name"
  [[ -e $path || -L $path ]] || continue
  [[ -d $path && ! -L $path ]] || die "disposable child is not a real directory: $name"
  [[ $(realpath -e -- "$path") == "$path" ]] || die "disposable child is not canonical: $name"
  # Go's module cache intentionally removes owner-write bits. Restore them
  # only inside this already validated disposable subtree before deletion.
  chmod -R u+w -- "$path"
  rm -rf -- "$path"
  [[ ! -e $path && ! -L $path ]] || die "disposable child cleanup failed: $name"
  if [[ $removed == none ]]; then
    removed=$name
  else
    removed="$removed,$name"
  fi
done
printf 'cleanup=executed\n'
printf 'deployment_id=%s\n' "$deployment_id"
printf 'role=%s\n' "$role"
printf 'removed=%s\n' "$removed"
printf 'audit_retained=%q\n' "$workspace"
