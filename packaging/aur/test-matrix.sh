#!/usr/bin/env bash
# shellcheck disable=SC2030,SC2031,SC2034 # Fixture variables are consumed by sourced PKGBUILDs in subshells.
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
readonly ROOT
readonly SHARED="$HERE/../common/lmm-api"
readonly PACKAGES=(
  lmm-api-go
  lmm-api-go-bin
  lmm-api-go-git
  lmm-api-rs-bin
  lmm-api-rs-git
  lmm-api-web-bin
)

die() {
  printf 'test-aur-matrix: %s\n' "$*" >&2
  exit 1
}

contains_srcinfo() {
  local package=$1 expected=$2
  grep -Fqx "$expected" "$HERE/$package/.SRCINFO" ||
    die "$package .SRCINFO is missing: $expected"
}

contains_srcinfo_prefix() {
  local package=$1 expected=$2
  grep -Fq "$expected" "$HERE/$package/.SRCINFO" ||
    die "$package .SRCINFO is missing: $expected"
}

for removed in lmm-api-bin lmm-api-git lmm-api-deploy-bin; do
  [[ ! -e $HERE/$removed/PKGBUILD ]] || die "removed core package still has a PKGBUILD: $removed"
done
for removed in backend.conf lmm-api.install lmm-api-go.service lmm-api.env lmm-api-launcher; do
  [[ ! -e $SHARED/$removed ]] || die "removed launcher/provider asset remains: $removed"
done

CLI_PHASE_HELPER=$(realpath -e "$SHARED/lmm-api-cli-phase.sh")
readonly CLI_PHASE_HELPER
readonly CLI_PHASE_HELPER_SHA256=2b93864b302a7901a4688fd5b7df9b7e262f193a666a915718f434db20054935
[[ -f $CLI_PHASE_HELPER && ! -L $CLI_PHASE_HELPER ]] || die 'canonical CLI phase helper is missing'
BINARY_T1_RELEASE=$(sed -n 's/^readonly LMM_CLI_T1_RELEASE=//p' "$CLI_PHASE_HELPER")
readonly BINARY_T1_RELEASE
[[ $BINARY_T1_RELEASE =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'canonical CLI phase helper has an invalid T1 release'
[[ $(sha256sum "$CLI_PHASE_HELPER") == "$CLI_PHASE_HELPER_SHA256  $CLI_PHASE_HELPER" ]] ||
  die 'canonical CLI phase helper digest changed without updating package pins'
for package in lmm-api-go lmm-api-go-bin lmm-api-go-git; do
  helper="$HERE/$package/lmm-api-cli-phase.sh"
  [[ -L $helper && $(realpath -e "$helper") == "$CLI_PHASE_HELPER" ]] ||
    die "$package does not use the canonical CLI phase helper"
done
local_helper="$HERE/../local/lmm-api-go/lmm-api-cli-phase.sh"
[[ -L $local_helper && $(realpath -e "$local_helper") == "$CLI_PHASE_HELPER" ]] ||
  die 'local Go package does not use the canonical CLI phase helper'
for script in check-candidate-version.sh export-go-package-base.sh test-export-go-package-base.sh; do
  bash -n "$HERE/$script"
done
"$HERE/test-export-go-package-base.sh"

for package in "${PACKAGES[@]}"; do
  pkgbuild="$HERE/$package/PKGBUILD"
  [[ -f $pkgbuild ]] || die "missing $pkgbuild"
  bash -n "$pkgbuild"
  if command -v shellcheck >/dev/null 2>&1; then
    shellcheck -s bash -e SC1091,SC2034,SC2154 "$pkgbuild"
  fi
  srcinfo=$(cd -- "$HERE/$package" && makepkg --printsrcinfo)
  cmp -s <(printf '%s\n' "$srcinfo") "$HERE/$package/.SRCINFO" ||
    die "$package .SRCINFO is stale"
  contains_srcinfo "$package" $'pkgbase = '"$package"
  if grep -Fq $'\tdepends = lmm-api' "$HERE/$package/.SRCINFO"; then
    die "$package retains the removed shared launcher dependency"
  fi
done

binary_cli_phase=$(
  cd -- "$HERE/lmm-api-go-bin"
  CARCH=x86_64 startdir="$PWD" bash -c 'source ./PKGBUILD; printf "%s\n" "$_lmm_cli_phase"'
)
[[ $binary_cli_phase == t0 || $binary_cli_phase == t1 ]] ||
  die 'Go binary package declares an invalid CLI transition phase'
for package in lmm-api-go lmm-api-go-bin lmm-api-go-git; do
  contains_srcinfo_prefix "$package" $'\tprovides = lmm-api'
  if [[ $package == lmm-api-go-bin && $binary_cli_phase == t1 ]]; then
    if grep -Fq $'\tprovides = lmm-api-go' "$HERE/$package/.SRCINFO"; then
      die "$package retains the removed legacy CLI capability"
    fi
    contains_srcinfo "$package" $'\tconflicts = lmm-api-deploy-bin'
    contains_srcinfo "$package" $'\treplaces = lmm-api-deploy-bin'
  else
    contains_srcinfo_prefix "$package" $'\tprovides = lmm-api-go'
    if grep -Fq $'\tconflicts = lmm-api-deploy-bin' "$HERE/$package/.SRCINFO" ||
      grep -Fq $'\treplaces = lmm-api-deploy-bin' "$HERE/$package/.SRCINFO"; then
      die "$package applies the T1 deploy-package transition during T0"
    fi
  fi
  contains_srcinfo "$package" $'\tbackup = etc/lmm-api-go/lmm-api-go.env'
done
contains_srcinfo lmm-api-go-git $'\tprovides = lmm-api'
contains_srcinfo lmm-api-go-git $'\tprovides = lmm-api-go'
if grep -Eq $'^\tprovides = lmm-api(-go)?=' "$HERE/lmm-api-go-git/.SRCINFO"; then
  die 'Git Go package embeds a stale bootstrap version in provides'
fi
contains_srcinfo lmm-api-go-bin $'\tconflicts = lmm-api-go-git'
contains_srcinfo lmm-api-go-git $'\tconflicts = lmm-api-go-bin'
for variant in lmm-api-go-bin lmm-api-go-git; do
  contains_srcinfo lmm-api-go $'\tconflicts = '"$variant"
done
declare -a conflicts replaces provides
(
  # shellcheck disable=SC1090
  source "$CLI_PHASE_HELPER"
  lmm_cli_phase_apply_metadata "$LMM_CLI_PHASE_T1" "$LMM_CLI_T1_RELEASE" \
    'lmm-api' 'lmm-api-bin' 'lmm-api-git' 'lmm-api-go' 'lmm-api-go-git'
  [[ " ${conflicts[*]} " == *' lmm-api-deploy-bin '* ]] || die 'T1 Go package does not conflict with the legacy deploy package'
  [[ " ${replaces[*]} " == *' lmm-api-deploy-bin '* ]] || die 'T1 Go package does not replace the legacy deploy package'
  [[ " ${provides[*]} " != *' lmm-api-go='* ]] || die 'T1 Go package still provides the legacy CLI capability'
)
(
  # A newly versioned bootstrap remains T0 when its signed package phase says
  # so; version ordering must not silently remove the compatibility CLI.
  # shellcheck disable=SC1090
  source "$CLI_PHASE_HELPER"
  lmm_cli_phase_apply_metadata "$LMM_CLI_PHASE_T0" 999.0.0 \
    'lmm-api' 'lmm-api-bin' 'lmm-api-git' 'lmm-api-go' 'lmm-api-go-git'
  [[ " ${conflicts[*]} " != *' lmm-api-deploy-bin '* ]] || die 'explicit high-version T0 conflicts with the legacy deploy package'
  [[ " ${provides[*]} " == *' lmm-api-go=999.0.0 '* ]] || die 'explicit high-version T0 lost its compatibility CLI capability'
)

for package in lmm-api-rs-bin lmm-api-rs-git; do
  contains_srcinfo_prefix "$package" $'\tprovides = lmm-api-rs'
done
contains_srcinfo lmm-api-rs-bin $'\tconflicts = lmm-api-rs-git'
contains_srcinfo lmm-api-rs-git $'\tconflicts = lmm-api-rs-bin'

for package in lmm-api-go lmm-api-go-bin lmm-api-go-git lmm-api-rs-bin lmm-api-rs-git; do
  for removed_core in lmm-api lmm-api-bin lmm-api-git; do
    contains_srcinfo "$package" $'\tconflicts = '"$removed_core"
  done
done

for package in lmm-api-go-bin lmm-api-rs-bin; do
  pkgbuild="$HERE/$package/PKGBUILD"
  grep -Fq 'cosign verify-blob' "$pkgbuild" || die "$package lacks Sigstore verification"
  grep -Fq 'sha256sum' "$pkgbuild" || die "$package lacks SHA-256 verification"
  grep -Fq 'noextract=(' "$pkgbuild" || die "$package extracts before verification"
  if grep -Eq '(^|[[:space:]])(go|bun|cargo)([[:space:]]|$)' "$pkgbuild"; then
    die "$package invokes a project compiler"
  fi
done
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
grep -Fq '_release_tag="go-v${pkgver}"' "$HERE/lmm-api-go-bin/PKGBUILD" ||
  die 'Go binary package does not use the independent Go release tag'
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
grep -Fq '.github/workflows/release-go.yml@refs/tags/${_release_tag}' \
  "$HERE/lmm-api-go-bin/PKGBUILD" ||
  die 'Go binary package does not verify the independent Go release identity'
pkgbuild="$HERE/lmm-api-web-bin/PKGBUILD"
grep -Fq 'cosign verify-blob' "$pkgbuild" || die 'lmm-api-web-bin lacks Sigstore verification'
grep -Fq 'sha256sum' "$pkgbuild" || die 'lmm-api-web-bin lacks SHA-256 verification'
grep -Fq 'noextract=(' "$pkgbuild" || die 'lmm-api-web-bin extracts before verification'
contains_srcinfo_prefix lmm-api-web-bin $'\tprovides = lmm-api-web'
grep -Fq 'systemctl reload nginx.service' "$HERE/lmm-api-web-bin/lmm-api-web-activate" ||
  die 'web package activation does not reload nginx'
if grep -Eq '(^|[[:space:]])curl([[:space:]]|$)' "$HERE/lmm-api-web-bin/lmm-api-web-activate"; then
  die 'web package activation performs a network probe inside the package transaction'
fi
if grep -Eq 'systemctl (restart|reload) lmm-api' "$HERE/lmm-api-web-bin/lmm-api-web-activate"; then
  die 'web package activation controls the backend service'
fi
web_release_workflow="$ROOT/.github/workflows/release-web.yml"
[[ -f $web_release_workflow ]] || die 'web release workflow is missing'
grep -Fq '  workflow_dispatch:' "$web_release_workflow" ||
  die 'web release lacks a manual recovery trigger for an existing immutable tag'
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
if grep -Fq 'if: ${{ github.ref_protected }}' "$web_release_workflow"; then
  die 'web release is gated by ref_protected and tag pushes may silently skip publication'
fi
grep -Fq 'git merge-base --is-ancestor' "$web_release_workflow" ||
  die 'web release lacks the default-branch ancestry gate'
grep -Fq 'cosign sign-blob' "$web_release_workflow" ||
  die 'web release does not sign its immutable artifact'
grep -Fq 'cosign verify-blob' "$web_release_workflow" ||
  die 'web release does not verify its new signature'

contains_srcinfo lmm-api-go-git $'\tmakedepends = bun'
contains_srcinfo lmm-api-go-git $'\tmakedepends = go>=1.25.1'
contains_srcinfo lmm-api-go $'\tmakedepends = bun'
contains_srcinfo lmm-api-go $'\tmakedepends = git'
contains_srcinfo lmm-api-go $'\tmakedepends = go>=1.25.1'
go_release_commit=1db462ebe08cc99e32014d478eb866e85af3badd
readonly go_release_commit
grep -Fqx "_commit=$go_release_commit" "$HERE/lmm-api-go/PKGBUILD" ||
  die 'canonical Go package is not pinned to the reviewed direct-package revision'
git -C "$ROOT" merge-base --is-ancestor "$go_release_commit" origin/main ||
  die 'canonical Go source pin is not reachable from main'
readonly go_source_pkgver_epoch=0.1.20
readonly last_published_go_source_version=0.1.19.r1279.g0c463f094-1
go_release_pkgver="$go_source_pkgver_epoch.r$(git -C "$ROOT" rev-list --count "$go_release_commit").g$(git -C "$ROOT" rev-parse --short=9 "$go_release_commit")"
go_release_pkgrel=$(sed -n 's/^pkgrel=//p' "$HERE/lmm-api-go/PKGBUILD")
[[ $go_release_pkgrel =~ ^[1-9][0-9]*$ ]] || die 'canonical Go package has an invalid pkgrel'
go_release_version="$go_release_pkgver-$go_release_pkgrel"
grep -Fqx "pkgver=$go_release_pkgver" "$HERE/lmm-api-go/PKGBUILD" ||
  die "canonical Go package version does not match pinned revision: $go_release_pkgver"
(($(vercmp "$last_published_go_source_version" "$go_release_version") < 0)) ||
  die "canonical Go package version is not newer than the published floor: $last_published_go_source_version"
"$HERE/check-candidate-version.sh" lmm-api-go "$go_release_version" "$go_release_version" >/dev/null ||
  die 'AUR exact source candidate was rejected'
if "$HERE/check-candidate-version.sh" lmm-api-go "$go_release_version" \
  "$go_release_pkgver-$((go_release_pkgrel + 1))" >/dev/null 2>&1; then
  die 'AUR source candidate accepted a strictly newer published pkgrel'
fi
grep -Fqx '_source_pkgver_epoch=0.1.20' "$HERE/lmm-api-go-git/PKGBUILD" ||
  die 'Git Go package lost the monotonic source-version epoch'
contains_srcinfo lmm-api-rs-git $'\tmakedepends = cargo'

grep -Fqx 'ExecStart=/usr/bin/lmm-api serve' "$SHARED/lmm-api.service" ||
  die 'Go systemd service does not execute the backend directly'
grep -Fqx 'Environment=LMM_API_FRONTEND_DIR=/srv/lmm-api-frontend/current' \
  "$SHARED/lmm-api.service" || die 'Go service does not use the split web activation link'
expected_memory=$'[Service]\nMemoryAccounting=yes\nMemoryHigh=320M\nMemoryMax=384M\nMemorySwapMax=256M\nEnvironment=GOMEMLIMIT=256MiB'
[[ $(<"$SHARED/lmm-api-memory.conf") == "$expected_memory" ]] ||
  die 'package-owned service memory drop-in does not match the production limits'
if grep -R -Eq 'lmm-api-launcher|backends/(go|rs)' \
  "$HERE"/*/PKGBUILD "$SHARED/lmm-api.service"; then
  die 'package layout retains a launcher or provider directory'
fi

: "${TMPDIR:?set TMPDIR to a marker-owned build workspace}"
tmp=$(mktemp -d "$TMPDIR/lmm-aur-matrix.XXXXXXXX")
cleanup() { rm -rf -- "$tmp"; }
trap cleanup EXIT

stage="$tmp/stage"
go_bin_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-go-bin/PKGBUILD")
rs_bin_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-rs-bin/PKGBUILD")
[[ $go_bin_pkgver =~ ^[0-9]+(\.[0-9]+)*$ ]] || die 'Go binary package version is not fixture-safe'
[[ $rs_bin_pkgver =~ ^[0-9]+(\.[0-9]+)*$ ]] || die 'Rust binary package version is not fixture-safe'
go_bundle="$stage/go/lmm-api-go-${go_bin_pkgver}-linux-amd64"
go_next_bundle="$stage/go-next/lmm-api-go-${go_bin_pkgver}-linux-amd64"
rs_bundle="$stage/rs/lmm-api-rs-${rs_bin_pkgver}-linux-amd64"
mkdir -p "$go_bundle/frontend-dist" "$go_bundle/edge-policy/nginx" \
  "$go_next_bundle/edge-policy/nginx" "$rs_bundle"
printf '#!/bin/sh\n' >"$go_bundle/lmm-api-go"
printf '#!/bin/sh\n' >"$go_next_bundle/lmm-api"
printf '#!/bin/sh\n' >"$rs_bundle/lmm-api-rs"
printf '#!/bin/sh\n' >"$rs_bundle/lmm-db-migrate"
chmod 0755 "$go_bundle/lmm-api-go" "$go_next_bundle/lmm-api" \
  "$rs_bundle/lmm-api-rs" "$rs_bundle/lmm-db-migrate"
printf '<!doctype html>\n' >"$go_bundle/frontend-dist/index.html"
for bundle in "$go_bundle" "$go_next_bundle"; do
  cp "$SHARED/lmm-api.service" "$SHARED/lmm-api-go.env" "$bundle/"
  for file in http-map.conf lmm-api-locations.conf mime.types new-api.conf lmm-api-region-policy.conf; do
    printf 'fixture\n' >"$bundle/edge-policy/nginx/$file"
  done
  for file in geoip2-country-update.service geoip2-country-update.timer; do
    printf 'fixture\n' >"$bundle/edge-policy/$file"
  done
done
cp "$SHARED/lmm-api-memory.conf" "$go_next_bundle/lmm-api-memory.conf"
cp "$SHARED/lmm-api-operator.sysusers" "$SHARED/lmm-api-operator.tmpfiles" \
  "$SHARED/lmm-api-operator.sudoers" "$go_next_bundle/"
contract_revision=$("$ROOT/deploy/production/api-route-contract-revision.sh" print)
printf '%s\n' "$contract_revision" >"$go_next_bundle/API_ROUTE_CONTRACT_REVISION"
for bundle in "$go_bundle" "$go_next_bundle" "$rs_bundle"; do
  for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
    printf 'fixture\n' >"$bundle/$file"
  done
  printf '%040d\n' 0 >"$bundle/REVISION"
done
printf 'fixture archive\n' >"$stage/go/lmm-api-go-${go_bin_pkgver}-linux-amd64.tar.gz"
printf 'fixture archive\n' >"$stage/go-next/lmm-api-go-${go_bin_pkgver}-linux-amd64.tar.gz"

(
  CARCH=x86_64
  startdir="$HERE/lmm-api-go-bin"
  srcdir="$stage/go"
  pkgdir="$tmp/pkg-go-legacy"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-go-bin/PKGBUILD"
  # Exercise the retained bundled-frontend branch independently of which
  # immutable release the canonical prebuilt package currently pins.
  _legacy_bundled_version=$pkgver
  _lmm_cli_phase=$LMM_CLI_PHASE_T0
  package
)
(
  CARCH=x86_64
  startdir="$HERE/lmm-api-go-bin"
  srcdir="$stage/go-next"
  pkgdir="$tmp/pkg-go-t0"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-go-bin/PKGBUILD"
  pkgver=0.1.59
  _lmm_cli_phase=$LMM_CLI_PHASE_T0
  package
)
(
  CARCH=x86_64
  startdir="$HERE/lmm-api-go-bin"
  srcdir="$stage/go-next"
  pkgdir="$tmp/pkg-go-t1"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-go-bin/PKGBUILD"
  pkgver=$LMM_CLI_T1_RELEASE
  _lmm_cli_phase=$LMM_CLI_PHASE_T1
  package
)
(
  srcdir="$stage/rs"
  pkgdir="$tmp/pkg-rs"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-rs-bin/PKGBUILD"
  package
)

web_src="$stage/web"
web_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-web-bin/PKGBUILD")
[[ $web_pkgver =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die 'canonical Web package has an invalid version'
mkdir -p "$web_src/dist"
printf '<!doctype html>\n' >"$web_src/dist/index.html"
cp "$HERE/lmm-api-web-bin/lmm-api-web-activate" "$web_src/lmm-api-web-activate.local"
cp "$ROOT/deploy/frontend-release.sh" "$web_src/"
chmod 0755 "$web_src/lmm-api-web-activate.local" "$web_src/frontend-release.sh"
for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do printf 'fixture\n' >"$web_src/$file"; done
printf '%040d\n' 0 >"$web_src/REVISION"
printf '%s\n' "$contract_revision" >"$web_src/API_ROUTE_CONTRACT_REVISION"
printf 'fixture archive\n' >"$web_src/lmm-api-web-${web_pkgver}.tar.gz"
(
  srcdir="$web_src"
  pkgdir="$tmp/pkg-web-next"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-web-bin/PKGBUILD"
  pkgver=999.0.0
  package
)

for packaged_path in \
  pkg-go-legacy/usr/bin/lmm-api \
  pkg-go-legacy/usr/lib/systemd/system/lmm-api.service \
  pkg-go-legacy/etc/lmm-api-go/lmm-api-go.env \
  pkg-go-legacy/usr/share/lmm-api-go/frontend-dist/index.html \
  pkg-go-t0/usr/bin/lmm-api \
  pkg-go-t0/usr/lib/sysusers.d/lmm-api-operator.conf \
  pkg-go-t1/usr/bin/lmm-api \
  pkg-go-t1/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf \
  pkg-go-t1/usr/lib/sysusers.d/lmm-api-operator.conf \
  pkg-go-t1/usr/lib/tmpfiles.d/lmm-api-operator.conf \
  pkg-go-t1/etc/sudoers.d/lmm-api-operator \
  pkg-go-t1/usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION \
  pkg-go-t1/usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256 \
  pkg-go-t1/usr/share/lmm-api-go/edge-policy/nginx/http-map.conf \
  pkg-rs/usr/bin/lmm-api-rs \
  pkg-rs/usr/bin/lmm-db-migrate \
  pkg-web-next/usr/share/lmm-api-web/frontend-dist/index.html \
  pkg-web-next/usr/share/doc/lmm-api-web-bin/API_ROUTE_CONTRACT_REVISION \
  pkg-web-next/usr/share/doc/lmm-api-web-bin/RELEASE_ASSET_SHA256; do
  [[ -f $tmp/$packaged_path ]] || die "mock package layout is missing $packaged_path"
done
for root in pkg-go-legacy pkg-go-t0; do
  [[ -L $tmp/$root/usr/bin/lmm-api-go ]] || die "$root lacks the T0 compatibility symlink"
  [[ $(readlink "$tmp/$root/usr/bin/lmm-api-go") == lmm-api ]] ||
    die "$root compatibility symlink does not resolve to the canonical CLI"
done
[[ ! -e $tmp/pkg-go-t1/usr/bin/lmm-api-go ]] || die 'T1 Go package still exposes lmm-api-go'
[[ ! -e $tmp/pkg-go-t1/usr/bin/lmm-api-deploy ]] || die 'T1 Go package exposes lmm-api-deploy'
[[ $(find "$tmp/pkg-go-t1/usr/bin" -mindepth 1 -maxdepth 1 -printf '%f\n') == lmm-api ]] ||
  die 'T1 Go package exposes more than one public backend CLI'
[[ $(stat -c '%a' "$tmp/pkg-go-t1/etc/sudoers.d/lmm-api-operator") == 440 ]] ||
  die 'integrated operator sudoers policy mode is not 0440'
visudo -cf "$tmp/pkg-go-t1/etc/sudoers.d/lmm-api-operator" >/dev/null ||
  die 'integrated operator sudoers policy fails visudo validation'
sudoers="$tmp/pkg-go-t1/etc/sudoers.d/lmm-api-operator"
cmp -s "$sudoers" "$SHARED/lmm-api-operator.sudoers" ||
  die 'integrated operator sudoers policy differs from the shared policy'
go_pacman_regex='^--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/[A-Za-z0-9][A-Za-z0-9._-]{0,79}/staging/lmm-api-go-bin-[A-Za-z0-9][A-Za-z0-9._+@~-]*\.pkg\.tar\.(zst|xz|gz|bz2|lz4|lrz|lzo|Z)$'
web_pacman_regex='^--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/[A-Za-z0-9][A-Za-z0-9._-]{0,79}/staging/lmm-api-web-bin-[A-Za-z0-9][A-Za-z0-9._+@~-]*\.pkg\.tar\.(zst|xz|gz|bz2|lz4|lrz|lzo|Z)$'
grep -Fqx "lmm-api-deploy ALL=(root) NOPASSWD: /usr/bin/pacman $go_pacman_regex" "$sudoers" ||
  die 'operator exact Go pacman sudo rule is missing'
grep -Fqx "lmm-api-deploy ALL=(root) NOPASSWD: /usr/bin/pacman $web_pacman_regex" "$sudoers" ||
  die 'operator exact Web pacman sudo rule is missing'
for allowed in \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/go-abc_123/staging/lmm-api-go-bin-1.2.3-1-x86_64.pkg.tar.zst' \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/web-abc.123/staging/lmm-api-web-bin-1.2.3-1-aarch64.pkg.tar.xz'; do
  [[ $allowed =~ $go_pacman_regex || $allowed =~ $web_pacman_regex ]] || die "safe pacman argv rejected: $allowed"
done
for rejected in \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/go-abc/staging/lmm-api-go-bin-1-1-x86_64.pkg.tar.zst /var/lib/lmm-api-go-deploy/work/go-abc/staging/lmm-api-web-bin-1-1-x86_64.pkg.tar.zst' \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/go-abc/staging/../lmm-api-go-bin-1-1-x86_64.pkg.tar.zst' \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/go abc/staging/lmm-api-go-bin-1-1-x86_64.pkg.tar.zst' \
  '--upgrade --noconfirm -- /var/lib/lmm-api-go-deploy/work/go-abc/staging/lmm-api-go-bin-*.pkg.tar.zst' \
  '--upgrade --noconfirm -- /tmp/lmm-api-go-bin-1-1-x86_64.pkg.tar.zst'; do
  [[ ! $rejected =~ $go_pacman_regex && ! $rejected =~ $web_pacman_regex ]] || die "malicious pacman argv accepted: $rejected"
done
[[ ! -e $tmp/pkg-go-t1/usr/share/lmm-api-go/frontend-dist ]] ||
  die 'T1 Go package owns a bundled frontend'
for pkgbuild in "$HERE/lmm-api-go/PKGBUILD" "$HERE/lmm-api-go-git/PKGBUILD" "$HERE/../local/lmm-api-go/PKGBUILD"; do
  # shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
  grep -Fq 'lmm_cli_phase_install_compatibility_alias "$_lmm_cli_phase" "$pkgdir"' "$pkgbuild" ||
    die "Go package does not apply the shared CLI phase install contract: $pkgbuild"
done
[[ ! -e $tmp/pkg-rs/usr/bin/lmm-api && ! -L $tmp/pkg-rs/usr/bin/lmm-api ]] ||
  die 'Rust package exposes the Go provider command'

printf '%s\n' 'single-CLI split backend and Web AUR matrix verified'
