#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
readonly HERE ROOT
readonly VERSION="$ROOT/contracts/api-route/VERSION"
: "${TMPDIR:?set TMPDIR to a marker-owned test workspace}"

fail() { printf 'test-api-route-contract: %s\n' "$*" >&2; exit 1; }
[[ -f $VERSION && ! -L $VERSION ]] || fail 'contracts/api-route/VERSION is missing or unsafe'
version=$(<"$VERSION")
[[ $version =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
  fail 'VERSION is not a stable semantic version'
[[ $(wc -l <"$VERSION") == 1 ]] || fail 'VERSION is not exactly one newline-terminated line'

work=$(mktemp -d "$TMPDIR/lmm-api-route-contract.XXXXXXXX")
trap 'rm -rf -- "$work"' EXIT
(
  cd "$ROOT/apps/api-go"
  go build -trimpath -o "$work/lmm-api-go" .
)
ln -s lmm-api-go "$work/lmm-api"
[[ $(readlink "$work/lmm-api") == lmm-api-go ]] || fail 'workspace CLI is not a one-hop provider symlink'
provider_sha=$(sha256sum "$work/lmm-api-go")
provider_sha=${provider_sha%% *}
alias_sha=$(sha256sum "$work/lmm-api")
alias_sha=${alias_sha%% *}
[[ $alias_sha == "$provider_sha" ]] || fail 'workspace CLI target hash does not match provider'
cli="$work/lmm-api"
(
  cd "$ROOT"
  "$cli" deploy contract route generate "$work/go/API_ROUTE_CONTRACT_REVISION"
  "$cli" deploy contract route generate "$work/web/API_ROUTE_CONTRACT_REVISION"
  cmp -s "$work/go/API_ROUTE_CONTRACT_REVISION" "$work/web/API_ROUTE_CONTRACT_REVISION" ||
    fail 'Go and Web generation is not deterministic'
  "$cli" deploy contract route verify "$work/go/API_ROUTE_CONTRACT_REVISION"
  [[ $(<"$work/go/API_ROUTE_CONTRACT_REVISION") == $("$cli" deploy contract route print) ]] ||
    fail 'print and generated revisions differ'
  printf '%064d\n' 0 >"$work/tampered"
  if "$cli" deploy contract route verify "$work/tampered" >/dev/null 2>&1; then
    fail 'tampered revision was accepted'
  fi
)

printf 'API/route contract %s revision %s verified\n' \
  "$version" "$(cd "$ROOT" && "$cli" deploy contract route print)"
