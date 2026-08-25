#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
readonly HERE ROOT
readonly GO_WORKFLOW="$ROOT/.github/workflows/release-go.yml"
readonly WEB_WORKFLOW="$ROOT/.github/workflows/release-web.yml"
readonly PROMOTE_WORKFLOW="$ROOT/.github/workflows/promote-release.yml"
readonly GO_PKGBUILD="$ROOT/packaging/aur/lmm-api-go-bin/PKGBUILD"
readonly WEB_PKGBUILD="$ROOT/packaging/aur/lmm-api-web-bin/PKGBUILD"

fail() {
  printf 'test-release-artifact-contract: %s\n' "$*" >&2
  exit 1
}
require_literal() {
  local file=$1 literal=$2 message=$3
  grep -Fq -- "$literal" "$file" || fail "$message"
}
reject_literal() {
  local file=$1 literal=$2 message=$3
  ! grep -Fq -- "$literal" "$file" || fail "$message"
}

for file in "$GO_WORKFLOW" "$WEB_WORKFLOW" "$PROMOTE_WORKFLOW" "$GO_PKGBUILD" "$WEB_PKGBUILD"; do
  [[ -f $file ]] || fail "missing contract input: $file"
done

# The next Go release is backend-only and includes every package-owned runtime
# contract needed by the split transaction.
reject_literal "$GO_WORKFLOW" 'apps/web' 'Go workflow builds the web application'
reject_literal "$GO_WORKFLOW" 'setup-bun' 'Go workflow installs the web build toolchain'
require_literal "$GO_WORKFLOW" 'cp packaging/common/lmm-api/lmm-api-memory.conf' \
  'Go release omits the package-owned memory drop-in'
require_literal "$GO_WORKFLOW" 'EXPECTED_CONTRACT_REVISION:' \
  'Go release does not safely pass expected contract metadata into the build'
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
require_literal "$GO_WORKFLOW" '"$bundle/API_ROUTE_CONTRACT_REVISION"' \
  'Go release omits API/route contract metadata'
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
require_literal "$GO_WORKFLOW" '[[ ! -e "$bundle/frontend-dist" ]]' \
  'Go release does not fail closed on bundled frontend ownership'
for asset in lmm-api.service lmm-api-go.env edge-policy REVISION API_ROUTE_CONTRACT_REVISION; do
  require_literal "$GO_WORKFLOW" "$asset" "Go release omits $asset"
done
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
require_literal "$GO_WORKFLOW" '-o "../../${bundle}/lmm-api"' \
  'Go release archive does not contain the canonical CLI name'
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
reject_literal "$GO_WORKFLOW" '-o "../../${bundle}/lmm-api-go"' \
  'Go release archive still emits the legacy CLI name'
for gate in 'git merge-base --is-ancestor' 'git rev-list -n 1' \
  'cosign sign-blob' 'cosign verify-blob' 'sha256sum --check'; do
  require_literal "$GO_WORKFLOW" "$gate" "Go release omits gate: $gate"
done
require_literal "$GO_WORKFLOW" 'actions: read' \
  'Go release verifier cannot read workflow runs'
require_literal "$PROMOTE_WORKFLOW" 'timeout-minutes: 15' \
  'promotion job timeout does not cover the full governance poll budget'

# The next Web archive has the same revision generator and remains sole owner
# of the immutable frontend payload.
for asset in dist REVISION API_ROUTE_CONTRACT_REVISION; do
  require_literal "$WEB_WORKFLOW" "$asset" "Web release omits $asset"
done
for gate in 'git merge-base --is-ancestor' 'git rev-list -n 1' \
  'cosign sign-blob' 'cosign verify-blob' 'sha256sum --check'; do
  require_literal "$WEB_WORKFLOW" "$gate" "Web release omits gate: $gate"
done
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
require_literal "$WEB_WORKFLOW" '[[ "$aur_version" != "$version" ]]' \
  'Web release does not reject an already pinned tag version'
require_literal "$WEB_WORKFLOW" "sort -V | tail -n 1" \
  'Web release does not reject tracked versions newer than the tag'

require_literal "$GO_PKGBUILD" '_legacy_bundled_version=0.1.34' \
  'Go package lost its explicit immutable legacy compatibility boundary'
require_literal "$GO_PKGBUILD" '_legacy_cli_archive_version=0.1.57' \
  'Go package lost the explicit legacy CLI archive boundary'
require_literal "$GO_PKGBUILD" 'RELEASE_ASSET_SHA256' \
  'Go package does not preserve its signed release-asset digest'
require_literal "$GO_PKGBUILD" 'lmm_cli_phase_for_binary_release' \
  'Go package does not derive its phase from the shared transition contract'
require_literal "$ROOT/packaging/common/lmm-api/lmm-api-cli-phase.sh" \
  'LMM_CLI_T1_RELEASE=0.1.60' 'shared CLI phase helper lost the T1 boundary'
require_literal "$ROOT/packaging/common/lmm-api/lmm-api-cli-phase.sh" \
  "conflicts+=('lmm-api-deploy' 'lmm-api-deploy-bin')" \
  'T1 helper does not conflict with legacy deploy packages'
require_literal "$ROOT/packaging/common/lmm-api/lmm-api-cli-phase.sh" \
  "replaces+=('lmm-api-deploy-bin')" \
  'T1 helper does not replace the legacy deploy package'
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
require_literal "$GO_PKGBUILD" '[[ ! -e ${bundle}/frontend-dist ]]' \
  'future Go packages do not reject a bundled frontend'
require_literal "$GO_PKGBUILD" 'lmm-api.service.d/20-memory.conf' \
  'future Go package does not own the memory drop-in'
require_literal "$WEB_PKGBUILD" '_legacy_contractless_version=0.1.31' \
  'Web package lost its explicit immutable legacy compatibility boundary'
require_literal "$WEB_PKGBUILD" 'API_ROUTE_CONTRACT_REVISION' \
  'future Web package does not install contract metadata'
require_literal "$WEB_PKGBUILD" 'RELEASE_ASSET_SHA256' \
  'future Web package does not preserve its signed release-asset digest'

for immutable in \
  'pkgver=0.1.62' \
  "'0315baff3a3f89d64bdbb5cdb3d6156adf83068c2f8b060f63fc15e02b30429d'" \
  "'04853eb7eced16de97a197e961b71961fabbb7aff4f662743b40ea2fd5547735'"; do
  require_literal "$GO_PKGBUILD" "$immutable" \
    'tracked Go PKGBUILD no longer pins the existing immutable release'
done
for immutable in \
  'pkgver=0.1.42' \
  "'95e3ecd8c83cdc76a7c4b70b542d3e047e2d8f8972078c005c9b07e67e0229e7'"; do
  require_literal "$WEB_PKGBUILD" "$immutable" \
    'tracked Web PKGBUILD no longer pins the existing immutable release'
done

[[ ! -e $ROOT/packaging/aur/lmm-api-deploy-bin ]] ||
  fail 'legacy deploy-only AUR recipe remains in the final tree'
[[ ! -e $ROOT/packaging/common/lmm-api/lmm-api-launcher ]] ||
  fail 'legacy multi-provider launcher remains in the final tree'
for pkgbuild in \
  "$ROOT/packaging/aur/lmm-api-go/PKGBUILD" \
  "$ROOT/packaging/aur/lmm-api-go-git/PKGBUILD" \
  "$ROOT/packaging/local/lmm-api-go/PKGBUILD"; do
  require_literal "$pkgbuild" 'lmm_cli_phase_install_compatibility_alias' \
    "T0 Go package does not preserve the compatibility CLI: $pkgbuild"
done

TMPDIR=${TMPDIR:?set TMPDIR to a marker-owned test workspace} \
  "$HERE/test-api-route-contract.sh"
printf '%s\n' 'single-CLI Go, Web-owned frontend, metadata, and signature release contracts verified'
