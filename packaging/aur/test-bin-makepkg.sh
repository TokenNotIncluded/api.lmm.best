#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly SHARED="$HERE/../common/lmm-api"

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
}

create_archive() {
  local work=$1 artifact=$2
  tar -czf "$work/${artifact}.tar.gz" -C "$work/stage" "$artifact"
  (cd "$work" && sha256sum "${artifact}.tar.gz" >"${artifact}.tar.gz.sha256")
  printf '{}\n' >"$work/${artifact}.tar.gz.sigstore.json"
}

# Published binary packages pin the immutable release asset hashes. The
# makepkg smoke test builds deliberately tiny local fixtures, so give only the
# temporary PKGBUILD a matching checksum array instead of weakening the real
# package back to SKIP.
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
  local package=$1
  shift
  local work="$tmp/$package" archive expected
  PATH="$tmp/bin:$PATH" BUILDDIR="$work/build" PKGDEST="$work/packages" \
    makepkg --dir "$work" --force --nodeps --noconfirm --cleanbuild
  archive=$(printf '%s\n' "$work/packages"/*.pkg.tar.*)
  [[ -f $archive ]] || die "$package did not produce a package archive"
  for expected in "$@"; do
    bsdtar -tf "$archive" | grep -Fqx "$expected" ||
      die "$package archive is missing $expected"
  done
}

# Exercise the retained bundled-frontend archive and historical T0 metadata at
# their real immutable boundary rather than overriding phase after PKGBUILD
# metadata has already been evaluated.
go_work="$tmp/lmm-api-go-bin"
go_legacy_version=0.1.34
go_artifact="lmm-api-go-${go_legacy_version}-linux-amd64"
go_bundle="$go_work/stage/$go_artifact"
mkdir -p "$go_bundle/frontend-dist" "$go_bundle/edge-policy/nginx"
cp "$HERE/lmm-api-go-bin/PKGBUILD" "$go_work/"
cp -L "$HERE/lmm-api-go-bin/lmm-api-cli-phase.sh" "$go_work/"
sed -i \
  -e "s/^pkgver=.*/pkgver=${go_legacy_version}/" \
  -e "s/^_lmm_declared_cli_phase=.*/_lmm_declared_cli_phase=''/" \
  "$go_work/PKGBUILD"
printf '#!/bin/sh\nexit 0\n' >"$go_bundle/lmm-api-go"
chmod 0755 "$go_bundle/lmm-api-go"
printf '<!doctype html>\n' >"$go_bundle/frontend-dist/index.html"
cp "$SHARED/lmm-api.service" "$SHARED/lmm-api-go.env" "$go_bundle/"
for file in http-map.conf lmm-api-locations.conf mime.types new-api.conf lmm-api-region-policy.conf; do
  printf 'fixture\n' >"$go_bundle/edge-policy/nginx/$file"
done
for file in geoip2-country-update.service geoip2-country-update.timer; do
  printf 'fixture\n' >"$go_bundle/edge-policy/$file"
done
add_metadata "$go_bundle"
create_archive "$go_work" "$go_artifact"
pin_fixture_hashes "$go_work/PKGBUILD" sha256sums_x86_64 \
  "$go_work/${go_artifact}.tar.gz" \
  "$go_work/${go_artifact}.tar.gz.sha256" \
  "$go_work/${go_artifact}.tar.gz.sigstore.json"
build_package lmm-api-go-bin \
  usr/bin/lmm-api-go \
  usr/bin/lmm-api \
  usr/lib/systemd/system/lmm-api.service \
  etc/lmm-api-go/lmm-api-go.env \
  usr/share/lmm-api-go/frontend-dist/index.html \
  usr/share/lmm-api-go/edge-policy/nginx/http-map.conf \
  usr/share/lmm-api-go/edge-policy/geoip2-country-update.timer

# Build a real future 0.1.63 package with explicit T0 metadata before the
# top-level provides/conflicts/replaces arrays are evaluated.
go_next_work="$tmp/lmm-api-go-bin-next"
go_next_version=0.1.63
go_next_artifact="lmm-api-go-${go_next_version}-linux-amd64"
go_next_bundle="$go_next_work/stage/$go_next_artifact"
mkdir -p "$go_next_bundle/edge-policy/nginx"
cp "$HERE/lmm-api-go-bin/PKGBUILD" "$go_next_work/"
cp -L "$HERE/lmm-api-go-bin/lmm-api-cli-phase.sh" "$go_next_work/"
sed -i \
  -e "s/^pkgver=.*/pkgver=${go_next_version}/" \
  -e "s/^_lmm_declared_cli_phase=.*/_lmm_declared_cli_phase='t0'/" \
  "$go_next_work/PKGBUILD"
printf '#!/bin/sh\nexit 0\n' >"$go_next_bundle/lmm-api"
chmod 0755 "$go_next_bundle/lmm-api"
cp "$SHARED/lmm-api.service" "$SHARED/lmm-api-go.env" \
  "$SHARED/lmm-api-memory.conf" "$SHARED/lmm-api-operator.sysusers" \
  "$SHARED/lmm-api-operator.tmpfiles" "$SHARED/lmm-api-operator.sudoers" \
  "$go_next_bundle/"
for file in http-map.conf lmm-api-locations.conf mime.types new-api.conf lmm-api-region-policy.conf; do
  printf 'fixture\n' >"$go_next_bundle/edge-policy/nginx/$file"
done
for file in geoip2-country-update.service geoip2-country-update.timer; do
  printf 'fixture\n' >"$go_next_bundle/edge-policy/$file"
done
add_metadata "$go_next_bundle"
printf '%s\n' 't0' >"$go_next_bundle/CLI_TRANSITION_PHASE"
"$HERE/../../deploy/production/api-route-contract-revision.sh" generate \
  "$go_next_bundle/API_ROUTE_CONTRACT_REVISION"
create_archive "$go_next_work" "$go_next_artifact"
pin_fixture_hashes "$go_next_work/PKGBUILD" sha256sums_x86_64 \
  "$go_next_work/${go_next_artifact}.tar.gz" \
  "$go_next_work/${go_next_artifact}.tar.gz.sha256" \
  "$go_next_work/${go_next_artifact}.tar.gz.sigstore.json"
build_package lmm-api-go-bin-next \
  usr/bin/lmm-api \
  usr/bin/lmm-api-go \
  usr/lib/systemd/system/lmm-api.service \
  usr/lib/systemd/system/lmm-api.service.d/20-memory.conf \
  usr/lib/sysusers.d/lmm-api-operator.conf \
  usr/lib/tmpfiles.d/lmm-api-operator.conf \
  etc/sudoers.d/lmm-api-operator \
  usr/share/doc/lmm-api-go-bin/API_ROUTE_CONTRACT_REVISION \
  usr/share/doc/lmm-api-go-bin/CLI_TRANSITION_PHASE \
  usr/share/doc/lmm-api-go-bin/RELEASE_ASSET_SHA256 \
  usr/share/lmm-api-go/edge-policy/nginx/http-map.conf
next_archive=$(printf '%s\n' "$go_next_work/packages"/*.pkg.tar.*)
if bsdtar -tf "$next_archive" | grep -Eq 'usr/bin/lmm-api-deploy$|usr/share/lmm-api-go/frontend-dist'; then
  die 'T0 Go package archive contains deploy-only CLI or bundled frontend content'
fi
next_pkginfo=$(bsdtar -xOf "$next_archive" .PKGINFO)
grep -Fqx 'provides = lmm-api=0.1.63' <<<"$next_pkginfo" || die 'explicit T0 package lacks canonical CLI capability'
grep -Fqx 'provides = lmm-api-go=0.1.63' <<<"$next_pkginfo" || die 'explicit T0 package lacks compatibility CLI capability'
if grep -Eq '^(conflict|replaces) = lmm-api-deploy' <<<"$next_pkginfo"; then
  die 'explicit T0 package removes the legacy deploy package'
fi
next_extract="$tmp/lmm-api-go-bin-next-extract"
mkdir -p "$next_extract"
bsdtar -xf "$next_archive" -C "$next_extract"
[[ $(stat -c %a "$next_extract/etc/sudoers.d") == 750 ]] ||
  die 'Go package archive changes the canonical sudoers.d directory mode'
if bsdtar -tf "$next_archive" | grep -Fxq '.INSTALL'; then
  die 'Go package archive contains a root install hook'
fi
next_mtree=$(bsdtar -xOf "$next_archive" .MTREE | gzip -dc)
grep -Eq '^/set .*uid=0( |$)' <<<"$next_mtree" || die 'Go package .MTREE does not default to root UID'
grep -Eq '^/set .*gid=0( |$)' <<<"$next_mtree" || die 'Go package .MTREE does not default to root GID'
for spec in \
  './etc/sudoers.d type=dir mode=750' \
  './etc/sudoers.d/lmm-api-operator type=file mode=440'; do
  path=${spec%% *}
  entry=$(grep -F "$path " <<<"$next_mtree")
  [[ -n $entry ]] || die "Go package .MTREE lacks $path"
  read -r -a fields <<<"${spec#* }"
  for field in "${fields[@]}"; do
    key=${field%%=*}
    if [[ " $entry " == *" $key="* ]]; then
      [[ " $entry " == *" $field "* ]] || die "Go package .MTREE $path overrides $field"
    else
      grep -Eq "^/set .*$field( |$)" <<<"$next_mtree" || die "Go package .MTREE $path lacks $field"
    fi
  done
done
[[ $(readlink "$next_extract/usr/bin/lmm-api-go") == lmm-api ]] ||
  die 'explicit T0 package has an unsafe compatibility link'

# A package-base declaration that disagrees with the signed release phase must
# fail in prepare(), before any archive can be promoted.
go_mismatch_work="$tmp/lmm-api-go-bin-phase-mismatch"
go_mismatch_bundle="$go_mismatch_work/stage/$go_next_artifact"
mkdir -p "$go_mismatch_work"
cp "$go_next_work/PKGBUILD" "$go_mismatch_work/"
cp -L "$HERE/lmm-api-go-bin/lmm-api-cli-phase.sh" "$go_mismatch_work/"
cp -a "$go_next_work/stage" "$go_mismatch_work/"
printf '%s\n' 't1' >"$go_mismatch_bundle/CLI_TRANSITION_PHASE"
create_archive "$go_mismatch_work" "$go_next_artifact"
pin_fixture_hashes "$go_mismatch_work/PKGBUILD" sha256sums_x86_64 \
  "$go_mismatch_work/${go_next_artifact}.tar.gz" \
  "$go_mismatch_work/${go_next_artifact}.tar.gz.sha256" \
  "$go_mismatch_work/${go_next_artifact}.tar.gz.sigstore.json"
if PATH="$tmp/bin:$PATH" BUILDDIR="$go_mismatch_work/build" PKGDEST="$go_mismatch_work/packages" \
  makepkg --dir "$go_mismatch_work" --force --nodeps --noconfirm --cleanbuild; then
  die 'mismatched package and signed release CLI phases were accepted'
fi

web_work="$tmp/lmm-api-web-bin"
web_pkgver=$(awk -F= '$1 == "pkgver" { print $2; exit }' "$HERE/lmm-api-web-bin/PKGBUILD")
[[ $web_pkgver =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid web binary pkgver: $web_pkgver"
mkdir -p "$web_work/stage/dist"
cp "$HERE/lmm-api-web-bin/PKGBUILD" \
  "$HERE/lmm-api-web-bin/lmm-api-web.install" \
  "$web_work/"
cp "$HERE/lmm-api-web-bin/lmm-api-web-activate" \
  "$web_work/lmm-api-web-activate"
printf '<!doctype html>\n' >"$web_work/stage/dist/index.html"
cp "$HERE/lmm-api-web-bin/lmm-api-web-activate" "$web_work/stage/"
cp "$HERE/lmm-api-web-bin/lmm-api-web.install" "$web_work/stage/"
cp "$HERE/../../deploy/frontend-release.sh" "$web_work/stage/frontend-release.sh"
chmod 0755 "$web_work/stage/lmm-api-web-activate" "$web_work/stage/frontend-release.sh"
add_metadata "$web_work/stage"
"$HERE/../../deploy/production/api-route-contract-revision.sh" generate \
  "$web_work/stage/API_ROUTE_CONTRACT_REVISION"
web_artifact="lmm-api-web-${web_pkgver}"
tar -czf "$web_work/${web_artifact}.tar.gz" -C "$web_work/stage" \
  dist lmm-api-web-activate lmm-api-web.install frontend-release.sh \
  LICENSE NOTICE THIRD-PARTY-LICENSES.md REVISION API_ROUTE_CONTRACT_REVISION
(cd "$web_work" && sha256sum "${web_artifact}.tar.gz" >"${web_artifact}.tar.gz.sha256")
printf '{}\n' >"$web_work/${web_artifact}.tar.gz.sigstore.json"
pin_fixture_hashes "$web_work/PKGBUILD" sha256sums \
  "$web_work/${web_artifact}.tar.gz" \
  "$web_work/${web_artifact}.tar.gz.sha256" \
  "$web_work/${web_artifact}.tar.gz.sigstore.json" \
  "$web_work/lmm-api-web-activate"
build_package lmm-api-web-bin \
  usr/share/lmm-api-web/frontend-dist/index.html \
  usr/lib/lmm-api-web/lmm-api-web-activate \
  usr/lib/lmm-api-web/frontend-release.sh \
  usr/share/licenses/lmm-api-web-bin/LICENSE \
  usr/share/doc/lmm-api-web-bin/REVISION \
  usr/share/doc/lmm-api-web-bin/API_ROUTE_CONTRACT_REVISION \
  .INSTALL

rs_work="$tmp/lmm-api-rs-bin"
rs_pkgver=$(awk -F= '$1 == "pkgver" { print $2; exit }' "$HERE/lmm-api-rs-bin/PKGBUILD")
[[ $rs_pkgver =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "invalid Rust binary pkgver: $rs_pkgver"
rs_artifact="lmm-api-rs-${rs_pkgver}-linux-amd64"
rs_bundle="$rs_work/stage/$rs_artifact"
mkdir -p "$rs_bundle"
cp "$HERE/lmm-api-rs-bin/PKGBUILD" "$rs_work/"
for binary in lmm-api-rs lmm-db-migrate; do
  printf '#!/bin/sh\nexit 0\n' >"$rs_bundle/$binary"
  chmod 0755 "$rs_bundle/$binary"
done
add_metadata "$rs_bundle"
create_archive "$rs_work" "$rs_artifact"
pin_fixture_hashes "$rs_work/PKGBUILD" sha256sums \
  "$rs_work/${rs_artifact}.tar.gz" \
  "$rs_work/${rs_artifact}.tar.gz.sha256" \
  "$rs_work/${rs_artifact}.tar.gz.sigstore.json"
build_package lmm-api-rs-bin usr/bin/lmm-api-rs usr/bin/lmm-db-migrate

printf '%s\n' 'prebuilt operator, direct-backend, and web AUR packages built with makepkg'
