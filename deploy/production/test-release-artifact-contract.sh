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
readonly GO_CLI_PHASE="$ROOT/deploy/production/CLI_TRANSITION_PHASE"

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

for file in "$GO_WORKFLOW" "$WEB_WORKFLOW" "$PROMOTE_WORKFLOW" "$GO_PKGBUILD" "$WEB_PKGBUILD" "$GO_CLI_PHASE"; do
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
for asset in lmm-api.service lmm-api-go.env edge-policy REVISION API_ROUTE_CONTRACT_REVISION CLI_TRANSITION_PHASE; do
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
for asset in dist REVISION API_ROUTE_CONTRACT_REVISION lmm-api-web.install; do
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
  'Go package does not derive its legacy phase from the shared transition contract'
require_literal "$GO_PKGBUILD" 'CLI_TRANSITION_PHASE' \
  'Go package does not preserve the signed explicit CLI transition phase'
# shellcheck disable=SC2016 # Deliberately match the literal PKGBUILD variable.
require_literal "$GO_PKGBUILD" 'install -d -m0750 "${pkgdir}/etc/sudoers.d"' \
  'Go package changes the canonical sudoers.d directory mode'
[[ $(<"$GO_CLI_PHASE") == t0 || $(<"$GO_CLI_PHASE") == t1 ]] ||
  fail 'next Go release CLI transition phase is invalid'
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
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
require_literal "$WEB_PKGBUILD" 'cmp -s "${startdir}/lmm-api-web.install" "${srcdir}/lmm-api-web.install"' \
  'Web package does not compare its install hook with signed release evidence'
require_literal "$WEB_PKGBUILD" 'RELEASE_ASSET_SHA256' \
  'future Web package does not preserve its signed release-asset digest'

for immutable in \
  'pkgver=0.1.69' \
  "'b5f7e5347ce60c30f4a462e5904ef24b39bc9bd2c6e5c5b681d529915ca34d88'" \
  "'d1255b0a0ab4468f3279c5ea62c4bc47d984bcfdf718f8c8e1265e7c7ef8c31a'"; do
  require_literal "$GO_PKGBUILD" "$immutable" \
    'tracked Go PKGBUILD no longer pins the existing immutable release'
done
for immutable in \
  'pkgver=0.1.51' \
  "'51df7dba3eb8f9f1e1858c1a9e7a9b4c6d68510a3a0bd59533744552358011c1'"; do
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
