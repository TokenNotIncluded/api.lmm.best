#!/usr/bin/env bash
# shellcheck disable=SC2034 # Fixture variables are consumed by sourced PKGBUILDs.
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
ROOT=$(git -C "$HERE" rev-parse --show-toplevel)
readonly ROOT
readonly SHARED="$HERE/../common/lmm-api"
readonly PACKAGES=(
  lmm-api-deploy-bin
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

for removed in lmm-api-bin lmm-api-git; do
  [[ ! -e $HERE/$removed/PKGBUILD ]] || die "removed core package still has a PKGBUILD: $removed"
done
for removed in backend.conf lmm-api.install lmm-api-go.service lmm-api.env; do
  [[ ! -e $SHARED/$removed ]] || die "removed launcher/provider asset remains: $removed"
done

for package in "${PACKAGES[@]}"; do
  pkgbuild="$HERE/$package/PKGBUILD"
  [[ -f $pkgbuild ]] || die "missing $pkgbuild"
  bash -n "$pkgbuild"
  if command -v shellcheck >/dev/null 2>&1; then
    shellcheck -s bash -e SC2034,SC2154 "$pkgbuild"
  fi
  srcinfo=$(cd -- "$HERE/$package" && makepkg --printsrcinfo)
  cmp -s <(printf '%s\n' "$srcinfo") "$HERE/$package/.SRCINFO" ||
    die "$package .SRCINFO is stale"
  contains_srcinfo "$package" $'pkgbase = '"$package"
  if grep -Fq $'\tdepends = lmm-api' "$HERE/$package/.SRCINFO"; then
    die "$package retains the removed shared launcher dependency"
  fi
done

contains_srcinfo_prefix lmm-api-deploy-bin $'\tprovides = lmm-api-deploy='
contains_srcinfo lmm-api-deploy-bin $'\tconflicts = lmm-api-deploy'
contains_srcinfo lmm-api-deploy-bin $'\tdepends = sudo'
if grep -Eq $'\t(depends|optdepends) = (nginx|postgresql|valkey)' \
  "$HERE/lmm-api-deploy-bin/.SRCINFO"; then
  die 'deployment operator package has application runtime dependencies'
fi

for package in lmm-api-go-bin lmm-api-go-git; do
  contains_srcinfo_prefix "$package" $'\tprovides = lmm-api-go'
  contains_srcinfo "$package" $'\tbackup = etc/lmm-api-go/lmm-api-go.env'
done
contains_srcinfo lmm-api-go-bin $'\tconflicts = lmm-api-go-git'
contains_srcinfo lmm-api-go-git $'\tconflicts = lmm-api-go-bin'
for variant in lmm-api-go-bin lmm-api-go-git; do
  contains_srcinfo lmm-api-go $'\tconflicts = '"$variant"
done
contains_srcinfo lmm-api-go $'\tbackup = etc/lmm-api-go/lmm-api-go.env'

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

for package in lmm-api-deploy-bin lmm-api-go-bin lmm-api-rs-bin; do
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
# shellcheck disable=SC2016 # Deliberately inspect PKGBUILD source literals.
grep -Fq '.github/workflows/release-go.yml@refs/tags/${_release_tag}' \
  "$HERE/lmm-api-deploy-bin/PKGBUILD" ||
  die 'deployment operator package does not verify the Go release identity'

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
go_release_commit=11217412480e81b58f96f2b9889bd317120ff8f0
readonly go_release_commit
grep -Fqx "_commit=$go_release_commit" "$HERE/lmm-api-go/PKGBUILD" ||
  die 'canonical Go package is not pinned to the reviewed direct-package revision'
readonly reviewed_go_release_pkgver=0.1.1.r490.g112174124
if go_release_description=$(git -C "$ROOT" describe --long --tags --exclude='web-v*' --abbrev=9 "$go_release_commit" 2>/dev/null); then
  go_release_pkgver=$(printf '%s\n' "$go_release_description" |
    sed -E 's/^v//; s/([^-]*-g)/r\1/; s/-/./g')
else
  go_release_pkgver=$reviewed_go_release_pkgver
fi
grep -Fqx "pkgver=$go_release_pkgver" "$HERE/lmm-api-go/PKGBUILD" ||
  die "canonical Go package version does not match pinned revision: $go_release_pkgver"
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
deploy_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-deploy-bin/PKGBUILD")
rs_bin_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-rs-bin/PKGBUILD")
[[ $go_bin_pkgver =~ ^[0-9]+(\.[0-9]+)*$ ]] || die 'Go binary package version is not fixture-safe'
[[ $deploy_pkgver =~ ^[0-9]+(\.[0-9]+)*$ ]] || die 'operator package version is not fixture-safe'
[[ $rs_bin_pkgver =~ ^[0-9]+(\.[0-9]+)*$ ]] || die 'Rust binary package version is not fixture-safe'
go_bundle="$stage/go/lmm-api-go-${go_bin_pkgver}-linux-amd64"
go_next_bundle="$stage/go-next/lmm-api-go-${go_bin_pkgver}-linux-amd64"
deploy_bundle="$stage/deploy/lmm-api-go-${deploy_pkgver}-linux-amd64"
rs_bundle="$stage/rs/lmm-api-rs-${rs_bin_pkgver}-linux-amd64"
mkdir -p "$go_bundle/frontend-dist" "$go_bundle/edge-policy/nginx" \
  "$go_next_bundle/edge-policy/nginx" "$deploy_bundle" "$rs_bundle"
printf '#!/bin/sh\n' >"$go_bundle/lmm-api-go"
printf '#!/bin/sh\n' >"$go_next_bundle/lmm-api-go"
printf '#!/bin/sh\n' >"$deploy_bundle/lmm-api-go"
printf '#!/bin/sh\n' >"$rs_bundle/lmm-api-rs"
printf '#!/bin/sh\n' >"$rs_bundle/lmm-db-migrate"
chmod 0755 "$go_bundle/lmm-api-go" "$go_next_bundle/lmm-api-go" \
  "$deploy_bundle/lmm-api-go" "$rs_bundle/lmm-api-rs" "$rs_bundle/lmm-db-migrate"
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
contract_revision=$("$ROOT/deploy/production/api-route-contract-revision.sh" print)
printf '%s\n' "$contract_revision" >"$go_next_bundle/API_ROUTE_CONTRACT_REVISION"
for bundle in "$go_bundle" "$go_next_bundle" "$deploy_bundle" "$rs_bundle"; do
  for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
    printf 'fixture\n' >"$bundle/$file"
  done
  printf '%040d\n' 0 >"$bundle/REVISION"
done
printf '%s\n' "$contract_revision" >"$deploy_bundle/API_ROUTE_CONTRACT_REVISION"
printf 'fixture archive\n' >"$stage/deploy/lmm-api-go-${deploy_pkgver}-linux-amd64.tar.gz"

(
  CARCH=x86_64
  srcdir="$stage/go"
  pkgdir="$tmp/pkg-go-legacy"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-go-bin/PKGBUILD"
  # Exercise the retained bundled-frontend branch independently of which
  # immutable release the canonical prebuilt package currently pins.
  _legacy_bundled_version=$pkgver
  package
)
(
  CARCH=x86_64
  srcdir="$stage/go-next"
  pkgdir="$tmp/pkg-go-next"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-go-bin/PKGBUILD"
  pkgver=999.0.0
  package
)
(
  CARCH=x86_64
  srcdir="$stage/deploy"
  pkgdir="$tmp/pkg-deploy"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-deploy-bin/PKGBUILD"
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
mkdir -p "$web_src/dist"
printf '<!doctype html>\n' >"$web_src/dist/index.html"
cp "$HERE/lmm-api-web-bin/lmm-api-web-activate" "$web_src/lmm-api-web-activate.local"
cp "$ROOT/deploy/frontend-release.sh" "$web_src/"
chmod 0755 "$web_src/lmm-api-web-activate.local" "$web_src/frontend-release.sh"
for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do printf 'fixture\n' >"$web_src/$file"; done
printf '%040d\n' 0 >"$web_src/REVISION"
printf '%s\n' "$contract_revision" >"$web_src/API_ROUTE_CONTRACT_REVISION"
(
  srcdir="$web_src"
  pkgdir="$tmp/pkg-web-next"
  # shellcheck disable=SC1091
  source "$HERE/lmm-api-web-bin/PKGBUILD"
  pkgver=999.0.0
  package
)

for packaged_path in \
  pkg-go-legacy/usr/bin/lmm-api-go \
  pkg-go-legacy/usr/lib/systemd/system/lmm-api.service \
  pkg-go-legacy/etc/lmm-api-go/lmm-api-go.env \
  pkg-go-legacy/usr/share/lmm-api-go/frontend-dist/index.html \
  pkg-go-next/usr/bin/lmm-api-go \
  pkg-go-next/usr/lib/systemd/system/lmm-api.service.d/20-memory.conf \
  pkg-go-next/usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION \
  pkg-go-next/usr/share/lmm-api-go/edge-policy/nginx/http-map.conf \
  pkg-deploy/usr/lib/lmm-api-deploy/lmm-api-go \
  pkg-deploy/usr/share/doc/lmm-api-deploy-bin/OPERATOR_SHA256 \
  pkg-deploy/usr/share/doc/lmm-api-deploy-bin/RELEASE_ASSET_SHA256 \
  pkg-deploy/usr/lib/sysusers.d/lmm-api-deploy.conf \
  pkg-deploy/usr/lib/tmpfiles.d/lmm-api-deploy.conf \
  pkg-deploy/etc/sudoers.d/lmm-api-deploy \
  pkg-rs/usr/bin/lmm-api-rs \
  pkg-rs/usr/bin/lmm-db-migrate \
  pkg-web-next/usr/share/lmm-api-web/frontend-dist/index.html \
  pkg-web-next/usr/share/doc/lmm-api-web-bin/API_ROUTE_CONTRACT_REVISION; do
  [[ -f $tmp/$packaged_path ]] || die "mock package layout is missing $packaged_path"
done
[[ -L $tmp/pkg-go-legacy/usr/bin/lmm-api ]] || die 'legacy Go package lacks provider symlink'
[[ -L $tmp/pkg-go-next/usr/bin/lmm-api ]] || die 'next Go package lacks provider symlink'
[[ -L $tmp/pkg-deploy/usr/bin/lmm-api-deploy ]] || die 'operator package lacks canonical command'
[[ $(readlink "$tmp/pkg-deploy/usr/bin/lmm-api-deploy") == ../lib/lmm-api-deploy/lmm-api-go ]] ||
  die 'operator command does not resolve to its independent package payload'
cmp -s "$tmp/pkg-deploy/usr/lib/lmm-api-deploy/lmm-api-go" "$deploy_bundle/lmm-api-go" ||
  die 'operator package changed the signed Go release bytes'
[[ $(stat -c '%a' "$tmp/pkg-deploy/etc/sudoers.d/lmm-api-deploy") == 440 ]] ||
  die 'operator sudoers policy mode is not 0440'
sudoers="$tmp/pkg-deploy/etc/sudoers.d/lmm-api-deploy"
visudo -cf "$sudoers" >/dev/null || die 'operator sudoers policy fails visudo validation'
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
[[ $(<"$tmp/pkg-deploy/usr/share/doc/lmm-api-deploy-bin/OPERATOR_SHA256") == $(sha256sum "$deploy_bundle/lmm-api-go" | cut -d' ' -f1) ]] ||
  die 'operator byte hash metadata is incorrect'
[[ ! -e $tmp/pkg-go-next/usr/share/lmm-api-go/frontend-dist ]] ||
  die 'next Go package owns a bundled frontend'
for forbidden in \
  pkg-deploy/usr/lib/systemd/system/lmm-api.service \
  pkg-deploy/etc/lmm-api-go/lmm-api-go.env \
  pkg-deploy/usr/share/lmm-api-go/frontend-dist \
  pkg-deploy/usr/share/lmm-api-web/frontend-dist; do
  [[ ! -e $tmp/$forbidden ]] || die "tooling-only operator owns application path: $forbidden"
done
[[ ! -e $tmp/pkg-rs/usr/bin/lmm-api && ! -L $tmp/pkg-rs/usr/bin/lmm-api ]] ||
  die 'Rust package exposes the Go provider command'

printf '%s\n' 'seven-package split backend, web, and deployment operator AUR matrix verified'
