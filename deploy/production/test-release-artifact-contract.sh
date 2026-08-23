#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
readonly HERE ROOT
readonly GO_WORKFLOW="$ROOT/.github/workflows/release-go.yml"
readonly WEB_WORKFLOW="$ROOT/.github/workflows/release-web.yml"
readonly GO_PKGBUILD="$ROOT/packaging/aur/lmm-api-go-bin/PKGBUILD"
readonly WEB_PKGBUILD="$ROOT/packaging/aur/lmm-api-web-bin/PKGBUILD"
readonly DEPLOY_PKGBUILD="$ROOT/packaging/aur/lmm-api-deploy-bin/PKGBUILD"

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

for file in "$GO_WORKFLOW" "$WEB_WORKFLOW" "$GO_PKGBUILD" "$WEB_PKGBUILD" "$DEPLOY_PKGBUILD"; do
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
for gate in 'git merge-base --is-ancestor' 'git rev-list -n 1' \
  'cosign sign-blob' 'cosign verify-blob' 'sha256sum --check'; do
  require_literal "$GO_WORKFLOW" "$gate" "Go release omits gate: $gate"
done

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
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
require_literal "$GO_PKGBUILD" '[[ ! -e ${bundle}/frontend-dist ]]' \
  'future Go packages do not reject a bundled frontend'
require_literal "$GO_PKGBUILD" 'lmm-api.service.d/20-memory.conf' \
  'future Go package does not own the memory drop-in'
require_literal "$WEB_PKGBUILD" '_legacy_contractless_version=0.1.31' \
  'Web package lost its explicit immutable legacy compatibility boundary'
require_literal "$WEB_PKGBUILD" 'API_ROUTE_CONTRACT_REVISION' \
  'future Web package does not install contract metadata'

for immutable in \
  'pkgver=0.1.48' \
  "'26f18420836479c0663a422fd2c6379d4214e1c7d3595a9bba8b91671340a0cd'" \
  "'957be07dc14091d8e37b0e2acbb4a66cc815387905245f23ecb58420748fd841'"; do
  require_literal "$GO_PKGBUILD" "$immutable" \
    'tracked Go PKGBUILD no longer pins the existing immutable release'
done
for immutable in \
  'pkgver=0.1.33' \
  "'934f4963d60a0311c9a6a03734c7263f88579762d8489eeb5ccff453aa7559ec'"; do
  require_literal "$WEB_PKGBUILD" "$immutable" \
    'tracked Web PKGBUILD no longer pins the existing immutable release'
done

require_literal "$DEPLOY_PKGBUILD" 'pkgver=0.1.49' \
  'operator bootstrap does not pin the reviewed Go release'
require_literal "$DEPLOY_PKGBUILD" '_release_revision=148fd59336b25de68742c4c8c499f0b2863ad13b' \
  'operator bootstrap does not pin the release Git identity'
require_literal "$DEPLOY_PKGBUILD" 'usr/lib/lmm-api-deploy/lmm-api-go' \
  'operator payload is not independent from the application package'
require_literal "$DEPLOY_PKGBUILD" 'usr/bin/lmm-api-deploy' \
  'operator package does not own the canonical command'
for forbidden in 'lmm-api.service"' 'lmm-api-go.env"' 'frontend-dist/'; do
  reject_literal "$DEPLOY_PKGBUILD" "$forbidden" \
    "tooling-only operator recipe owns application payload: $forbidden"
done

TMPDIR=${TMPDIR:?set TMPDIR to a marker-owned test workspace} \
  "$HERE/test-api-route-contract.sh"
printf '%s\n' 'Go-only, Web-owned frontend, metadata, signature, and operator release contracts verified'
