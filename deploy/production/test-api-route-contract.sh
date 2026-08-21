#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly TOOL="$HERE/api-route-contract-revision.sh"
: "${TMPDIR:?set TMPDIR to a marker-owned test workspace}"

fail() { printf 'test-api-route-contract: %s\n' "$*" >&2; exit 1; }
[[ -x $TOOL ]] || fail 'revision tool is not executable'
bash -n "$TOOL"
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "$TOOL"
fi

version=$(<"$HERE/API_ROUTE_CONTRACT")
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
  fail 'API_ROUTE_CONTRACT is not a stable semantic version'

work=$(mktemp -d "$TMPDIR/lmm-api-route-contract.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
"$TOOL" generate "$work/go/API_ROUTE_CONTRACT_REVISION"
"$TOOL" generate "$work/web/API_ROUTE_CONTRACT_REVISION"
cmp -s "$work/go/API_ROUTE_CONTRACT_REVISION" "$work/web/API_ROUTE_CONTRACT_REVISION" ||
  fail 'Go and Web generation is not deterministic'
"$TOOL" verify "$work/go/API_ROUTE_CONTRACT_REVISION"
[[ $(<"$work/go/API_ROUTE_CONTRACT_REVISION") == $("$TOOL" print) ]] ||
  fail 'print and generated revisions differ'
printf '%064d\n' 0 >"$work/tampered"
if "$TOOL" verify "$work/tampered" >/dev/null 2>&1; then
  fail 'tampered revision was accepted'
fi

printf 'API/route contract %s revision %s verified\n' \
  "$version" "$("$TOOL" print)"
