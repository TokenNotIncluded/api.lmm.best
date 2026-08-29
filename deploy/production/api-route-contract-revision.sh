#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
REPO_ROOT=$(cd -- "$HERE/../.." && pwd -P)
readonly REPO_ROOT
readonly CONTRACT="$REPO_ROOT/contracts/api-route/VERSION"

fail() { printf 'api-route-contract-revision: %s\n' "$*" >&2; exit 1; }
usage() {
  printf '%s\n' \
    'Usage: api-route-contract-revision.sh print' \
    '       api-route-contract-revision.sh generate OUTPUT' \
    '       api-route-contract-revision.sh verify REVISION_FILE'
}

validate_contract() {
  local value
  [[ -f $CONTRACT && ! -L $CONTRACT ]] || fail "contract is missing or unsafe: $CONTRACT"
  value=$(<"$CONTRACT")
  [[ $value =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    fail 'contract must contain exactly one stable semantic version'
  [[ $(wc -l <"$CONTRACT") -eq 1 ]] || fail 'contract must end after one newline-terminated version'
  [[ $(tail -c 1 -- "$CONTRACT" | od -An -tuC | tr -d '[:space:]') == 10 ]] ||
    fail 'contract must end with a newline'
}

revision() {
  local digest
  digest=$(LC_ALL=C sha256sum -- "$CONTRACT")
  printf '%s\n' "${digest%% *}"
}

validate_contract
case ${1:-} in
  print)
    (($# == 1)) || { usage >&2; exit 2; }
    revision
    ;;
  generate)
    (($# == 2)) || { usage >&2; exit 2; }
    [[ -n $2 && ! -d $2 && ! -L $2 ]] || fail 'output must be a non-symlink file path'
    mkdir -p -- "$(dirname -- "$2")"
    revision >"$2"
    chmod 0644 "$2"
    ;;
  verify)
    (($# == 2)) || { usage >&2; exit 2; }
    [[ -f $2 && ! -L $2 ]] || fail 'revision file is missing or unsafe'
    [[ $(wc -l <"$2") -eq 1 ]] || fail 'revision file must contain one line'
    expected=$(<"$2")
    [[ $expected =~ ^[0-9a-f]{64}$ ]] || fail 'revision file is not a lowercase SHA-256 digest'
    [[ $expected == $(revision) ]] || fail 'revision does not match API_ROUTE_CONTRACT'
    ;;
  -h|--help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
