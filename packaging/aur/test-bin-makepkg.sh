#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly SHARED="$HERE/../common/lmm-api"
readonly CONTRACT="$HERE/../../contracts/api-route/VERSION"

die() {
  printf 'test-bin-makepkg: %s\n' "$*" >&2
  exit 1
}

: "${TMPDIR:?set TMPDIR to a marker-owned build workspace}"
tmp=$(mktemp -d "$TMPDIR/lmm-bin-makepkg.XXXXXXXX")
cleanup() { rm -rf -- "$tmp"; }
trap cleanup EXIT
mkdir -p "$tmp/bin"
install -Dm0755 /usr/bin/true "$tmp/bin/cosign"

add_metadata() {
  local bundle=$1 file
  for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
    printf 'fixture\n' >"$bundle/$file"
  done
  printf '%040d\n' 0 >"$bundle/REVISION"
  sha256sum "$CONTRACT" | awk '{print $1}' >"$bundle/API_ROUTE_CONTRACT_REVISION"
}

add_go_runtime() {
  local bundle=$1 file
  cp "$SHARED/lmm-api.service" "$SHARED/lmm-api-go.env" \
    "$SHARED/lmm-api-memory.conf" "$SHARED/lmm-api-operator.sysusers" \
    "$SHARED/lmm-api-operator.tmpfiles" "$SHARED/lmm-api-operator.sudoers" \
    "$bundle/"
  mkdir -p "$bundle/edge-policy/nginx"
  for file in http-map.conf lmm-api-locations.conf mime.types new-api.conf lmm-api-region-policy.conf; do
    printf 'fixture\n' >"$bundle/edge-policy/nginx/$file"
  done
  for file in geoip2-country-update.service geoip2-country-update.timer; do
    printf 'fixture\n' >"$bundle/edge-policy/$file"
  done
  add_metadata "$bundle"
}

create_archive() {
  local work=$1 artifact=$2
  tar -czf "$work/${artifact}.tar.gz" -C "$work/stage" "$artifact"
  (cd "$work" && sha256sum "${artifact}.tar.gz" >"${artifact}.tar.gz.sha256")
  printf '{}\n' >"$work/${artifact}.tar.gz.sigstore.json"
}

pin_fixture_hashes() {
  local pkgbuild=$1 sums=$2 source hash
  shift 2
  [[ $sums =~ ^sha256sums(_[[:alnum:]_]+)?$ ]] || die "invalid checksum array: $sums"
  printf '\n%s=(\n' "$sums" >>"$pkgbuild"
  for source in "$@"; do
    hash=$(sha256sum "$source")
    printf "  '%s'\n" "${hash%% *}" >>"$pkgbuild"
  done
  printf ')\n' >>"$pkgbuild"
}

build_package() {
  local label=$1 work=$2
  shift 2
  local archive expected
  PATH="$tmp/bin:$PATH" BUILDDIR="$work/build" PKGDEST="$work/packages" \
    makepkg --dir "$work" --force --nodeps --noconfirm --cleanbuild >&2
  archive=$(printf '%s\n' "$work/packages"/*.pkg.tar.*)
  [[ -f $archive ]] || die "$label did not produce a package archive"
  for expected in "$@"; do
    bsdtar -tf "$archive" | grep -Fqx "$expected" || die "$label archive is missing $expected"
  done
  printf '%s\n' "$archive"
}

prepare_go_fixture() {
  local work=$1 version=$2 payload=$3
  local artifact="lmm-api-go-${version}-linux-amd64"
  local bundle="$work/stage/$artifact"
  mkdir -p "$bundle"
  cp "$HERE/lmm-api-go-bin/PKGBUILD" "$work/"
  cp -L "$HERE/lmm-api-go-bin/lmm-api-go-package.sh" "$work/"
  sed -i "s/^pkgver=.*/pkgver=${version}/" "$work/PKGBUILD"
  printf '#!/bin/sh\nexit 0\n' >"$bundle/$payload"
  chmod 0755 "$bundle/$payload"
  if [[ $version == 0.1.69 ]]; then
    printf 't0\n' >"$bundle/CLI_TRANSITION_PHASE"
  fi
  add_go_runtime "$bundle"
  create_archive "$work" "$artifact"
  pin_fixture_hashes "$work/PKGBUILD" sha256sums_x86_64 \
    "$work/${artifact}.tar.gz" "$work/${artifact}.tar.gz.sha256" \
    "$work/${artifact}.tar.gz.sigstore.json"
}

legacy_work="$tmp/go-legacy"
mkdir -p "$legacy_work"
prepare_go_fixture "$legacy_work" 0.1.69 lmm-api
legacy_archive=$(build_package go-legacy "$legacy_work" \
  usr/bin/lmm-api usr/bin/lmm-api-go usr/lib/systemd/system/lmm-api.service \
  usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION)
legacy_extract="$tmp/go-legacy-extract"
mkdir -p "$legacy_extract"
bsdtar -xf "$legacy_archive" -C "$legacy_extract"
[[ -f $legacy_extract/usr/bin/lmm-api && ! -L $legacy_extract/usr/bin/lmm-api ]] ||
  die 'verified legacy package lost its real generic binary'
[[ -L $legacy_extract/usr/bin/lmm-api-go && $(readlink "$legacy_extract/usr/bin/lmm-api-go") == lmm-api ]] ||
  die 'verified legacy package lost its exact reverse alias'

next_work="$tmp/go-next"
mkdir -p "$next_work"
prepare_go_fixture "$next_work" 0.2.0 lmm-api-go
next_archive=$(build_package go-next "$next_work" \
  usr/bin/lmm-api-go usr/lib/systemd/system/lmm-api.service \
  usr/lib/systemd/system/lmm-api.service.d/20-memory.conf \
  usr/lib/sysusers.d/lmm-api-operator.conf usr/lib/tmpfiles.d/lmm-api-operator.conf \
  etc/sudoers.d/lmm-api-operator usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION)
if bsdtar -tf "$next_archive" | grep -Eq 'usr/bin/lmm-api$|usr/bin/lmm-api-deploy$|CLI_TRANSITION_PHASE|frontend-dist'; then
  die 'new Go provider package contains a generic/reverse/deploy/phase/frontend payload'
fi
next_pkginfo=$(bsdtar -xOf "$next_archive" .PKGINFO)
grep -Fqx 'provides = lmm-api-go=0.2.0' <<<"$next_pkginfo" || die 'new Go package lacks provider capability'
grep -Fqx 'provides = lmm-api-provider' <<<"$next_pkginfo" || die 'new Go package lacks virtual provider capability'
if grep -Eq '^(provides|conflict|replaces) = lmm-api($|=)' <<<"$next_pkginfo"; then
  die 'new Go provider package claims a generic package identity'
fi
if bsdtar -tf "$next_archive" | grep -Fxq '.INSTALL'; then
  die 'Go provider package contains a root install hook'
fi

web_work="$tmp/web"
web_pkgver=0.1.52
mkdir -p "$web_work/stage/dist"
cp "$HERE/lmm-api-web-bin/PKGBUILD" "$web_work/"
sed -i "s/^pkgver=.*/pkgver=${web_pkgver}/" "$web_work/PKGBUILD"
cp "$SHARED/lmm-api-web.install" "$web_work/lmm-api-web.install"
printf '<!doctype html>\n' >"$web_work/stage/dist/index.html"
cp "$SHARED/lmm-api-web.install" "$web_work/stage/lmm-api-web.install"
add_metadata "$web_work/stage"
web_artifact="lmm-api-web-${web_pkgver}"
tar -czf "$web_work/${web_artifact}.tar.gz" -C "$web_work/stage" \
  dist lmm-api-web.install LICENSE NOTICE THIRD-PARTY-LICENSES.md REVISION API_ROUTE_CONTRACT_REVISION
(cd "$web_work" && sha256sum "${web_artifact}.tar.gz" >"${web_artifact}.tar.gz.sha256")
printf '{}\n' >"$web_work/${web_artifact}.tar.gz.sigstore.json"
pin_fixture_hashes "$web_work/PKGBUILD" sha256sums \
  "$web_work/${web_artifact}.tar.gz" "$web_work/${web_artifact}.tar.gz.sha256" \
  "$web_work/${web_artifact}.tar.gz.sigstore.json"
web_archive=$(build_package web "$web_work" \
  usr/share/lmm-api-web/frontend-dist/index.html \
  usr/share/licenses/lmm-api-web-bin/LICENSE \
  usr/share/doc/lmm-api-web-bin/REVISION \
  usr/share/doc/lmm-api-web-bin/API_ROUTE_CONTRACT_REVISION .INSTALL)
if bsdtar -tf "$web_archive" | grep -Eq 'frontend-release\.sh|lmm-api-web-activate'; then
  die 'Web package still contains a shell publisher'
fi
# shellcheck disable=SC2016 # Deliberately inspect the literal package-hook argument.
grep -Fq '/usr/bin/lmm-api deploy frontend package-activate --package-version "$1"' \
  "$web_work/lmm-api-web.install" || die 'Web install hook does not invoke the public backend CLI'

printf '%s\n' 'prebuilt provider and Web packages built with makepkg'
