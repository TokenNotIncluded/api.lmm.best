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

for removed in lmm-api-bin lmm-api-git lmm-api-deploy-bin lmm-api-rs-bin; do
  [[ ! -e $HERE/$removed ]] || die "retired package remains tracked: $removed"
done
for removed in backend.conf lmm-api.install lmm-api-go.service lmm-api.env lmm-api-launcher; do
  [[ ! -e $SHARED/$removed ]] || die "removed launcher/provider asset remains: $removed"
done

GO_PACKAGE_HELPER=$(realpath -e "$SHARED/lmm-api-go-package.sh")
readonly GO_PACKAGE_HELPER
readonly GO_PACKAGE_HELPER_SHA256=655e9346a6d87baa1cb81d97dcc412243d7ee305f90371b99d89033ea0e99bb1
[[ -f $GO_PACKAGE_HELPER && ! -L $GO_PACKAGE_HELPER ]] || die 'canonical Go package helper is missing'
grep -Fqx 'readonly LMM_GO_VERIFIED_LEGACY_VERSION=0.1.69' "$GO_PACKAGE_HELPER" ||
  die 'Go package helper lost the explicit N-1 migration boundary'
[[ $(sha256sum "$GO_PACKAGE_HELPER") == "$GO_PACKAGE_HELPER_SHA256  $GO_PACKAGE_HELPER" ]] ||
  die 'canonical Go package helper digest changed without updating package pins'
for package in lmm-api-go lmm-api-go-bin lmm-api-go-git; do
  helper="$HERE/$package/lmm-api-go-package.sh"
  [[ -L $helper && $(realpath -e "$helper") == "$GO_PACKAGE_HELPER" ]] ||
    die "$package does not use the canonical Go package helper"
done
local_helper="$HERE/../local/lmm-api-go/lmm-api-go-package.sh"
[[ -L $local_helper && $(realpath -e "$local_helper") == "$GO_PACKAGE_HELPER" ]] ||
  die 'local Go package does not use the canonical Go package helper'
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
  if grep -Fqx $'\tdepends = lmm-api' "$HERE/$package/.SRCINFO"; then
    die "$package retains the removed shared launcher dependency"
  fi
done

for package in lmm-api-go lmm-api-go-bin lmm-api-go-git; do
  contains_srcinfo_prefix "$package" $'\tprovides = lmm-api-go'
  contains_srcinfo "$package" $'\tprovides = lmm-api-provider'
  contains_srcinfo "$package" $'\tbackup = etc/lmm-api-go/lmm-api-go.env'
  if [[ $package == lmm-api-go-bin && $(sed -n 's/^pkgver=//p' "$HERE/$package/PKGBUILD") == 0.1.69 ]]; then
    contains_srcinfo_prefix "$package" $'\tprovides = lmm-api='
  elif grep -Eq $'^\t(provides|conflicts|replaces) = lmm-api($|=)|^\t(conflicts|replaces) = lmm-api-(bin|git|deploy)' \
      "$HERE/$package/.SRCINFO"; then
    die "$package claims a generic/core/deploy package identity"
  fi
done
if grep -Eq $'^\tprovides = lmm-api-go=' "$HERE/lmm-api-go-git/.SRCINFO"; then
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
  source "$GO_PACKAGE_HELPER"
  lmm_go_package_apply_metadata 999.0.0 lmm-api-go-bin \
    lmm-api-go lmm-api-go-bin lmm-api-go-git
  [[ " ${provides[*]} " == *' lmm-api-go=999.0.0 '* ]] || die 'provider capability is missing'
  [[ " ${conflicts[*]} " == *' lmm-api-go '* && " ${conflicts[*]} " == *' lmm-api-go-git '* ]] ||
    die 'Go variants do not conflict with each other'
  ((${#replaces[@]} == 0)) || die 'Go provider unexpectedly replaces another package'
)

contains_srcinfo_prefix lmm-api-rs-git $'\tprovides = lmm-api-rs'
contains_srcinfo lmm-api-rs-git $'\tprovides = lmm-api-provider'
# Keep the historical package conflict until the remote AUR package has been
# separately retired; otherwise an existing binary package can overwrite the
# same preview executable during the repository-side compatibility window.
contains_srcinfo lmm-api-rs-git $'\tconflicts = lmm-api-rs-bin'
if grep -Eq $'^\t(provides|conflicts|replaces) = lmm-api($|=)' "$HERE/lmm-api-rs-git/.SRCINFO"; then
  die 'Rust provider package claims the generic lmm-api identity'
fi


pkgbuild="$HERE/lmm-api-go-bin/PKGBUILD"
grep -Fq 'cosign verify-blob' "$pkgbuild" || die 'lmm-api-go-bin lacks Sigstore verification'
grep -Fq 'sha256sum' "$pkgbuild" || die 'lmm-api-go-bin lacks SHA-256 verification'
grep -Fq 'noextract=(' "$pkgbuild" || die 'lmm-api-go-bin extracts before verification'
if grep -Eq '(^|[[:space:]])(go|bun|cargo)([[:space:]]|$)' "$pkgbuild"; then
  die 'lmm-api-go-bin invokes a project compiler'
fi
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
web_pkgver=$(sed -n 's/^pkgver=//p' "$HERE/lmm-api-web-bin/PKGBUILD")
if (( $(vercmp "$web_pkgver" 0.1.52) >= 0 )); then
  contains_srcinfo lmm-api-web-bin $'\tdepends = lmm-api-provider'
  # shellcheck disable=SC2016 # Deliberately inspect the literal package-hook argument.
  grep -Fq '/usr/bin/lmm-api deploy frontend package-activate --package-version "$1"' \
    "$HERE/lmm-api-web-bin/lmm-api-web.install" ||
    die 'Web package install hook does not use the public backend CLI'
else
  # shellcheck disable=SC2016 # Deliberately inspect the literal legacy hook argument.
  grep -Fq '/usr/lib/lmm-api-web/lmm-api-web-activate "$1"' \
    "$HERE/lmm-api-web-bin/lmm-api-web.install" ||
    die 'pinned legacy Web recipe no longer reproduces its signed install hook'
fi
[[ ! -e $HERE/lmm-api-web-bin/lmm-api-web-activate && ! -L $HERE/lmm-api-web-bin/lmm-api-web-activate ]] ||
  die 'repository retains an unsigned local Web activation wrapper'
# shellcheck disable=SC2016 # Deliberately inspect the future signed package hook.
grep -Fq '/usr/bin/lmm-api deploy frontend package-activate --package-version "$1"' \
  "$SHARED/lmm-api-web.install" ||
  die 'future Web release hook does not use the public backend CLI'
go_release_workflow="$ROOT/.github/workflows/release-go.yml"
[[ -f $go_release_workflow ]] || die 'Go release workflow is missing'
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
grep -Fq 'gh release create "$RELEASE_TAG"' "$go_release_workflow" ||
  die 'Go release workflow does not create a new immutable release'
grep -Fq 'immutable releases cannot be edited or overwritten' "$go_release_workflow" ||
  die 'Go release workflow does not fail closed when a release already exists'
grep -Fq 'already exists and exactly matches the immutable contract' "$go_release_workflow" ||
  die 'Go release workflow cannot safely resume an exactly matching immutable release'
grep -Fq '|| return 1' "$go_release_workflow" ||
  die 'Go release readback checks are not explicit in conditional resume mode'
if grep -Eq 'gh release (edit|upload)|--clobber' "$go_release_workflow"; then
  die 'Go release workflow can mutate an existing release'
fi
# shellcheck disable=SC2016 # Deliberately inspect the asset digest readback query.
grep -Fq '.assets[] | select(.name == $name) | .digest' "$go_release_workflow" ||
  die 'Go release workflow does not read back published asset digests'
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
# shellcheck disable=SC2016 # Deliberately inspect workflow source literals.
grep -Fq 'gh release create "$RELEASE_TAG"' "$web_release_workflow" ||
  die 'web release workflow does not create a new immutable release'
grep -Fq 'immutable releases cannot be edited or overwritten' "$web_release_workflow" ||
  die 'web release workflow does not fail closed when a release already exists'
grep -Fq 'already exists and exactly matches the immutable contract' "$web_release_workflow" ||
  die 'web release workflow cannot safely resume an exactly matching immutable release'
grep -Fq '|| return 1' "$web_release_workflow" ||
  die 'web release readback checks are not explicit in conditional resume mode'
if grep -Eq 'gh release (edit|upload)|--clobber' "$web_release_workflow"; then
  die 'web release workflow can mutate an existing release'
fi
# shellcheck disable=SC2016 # Deliberately inspect the asset digest readback query.
grep -Fq '.assets[] | select(.name == $name) | .digest' "$web_release_workflow" ||
  die 'web release workflow does not read back published asset digests'

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

sudoers="$SHARED/lmm-api-operator.sudoers"
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

for pkgbuild in "$HERE/lmm-api-go/PKGBUILD" "$HERE/lmm-api-go-git/PKGBUILD" "$HERE/../local/lmm-api-go/PKGBUILD"; do
  grep -Fq 'usr/bin/lmm-api-go"' "$pkgbuild" || die "Go package omits real provider payload: $pkgbuild"
  if grep -Fq 'usr/bin/lmm-api"' "$pkgbuild"; then die "Go package owns canonical selection link: $pkgbuild"; fi
  if grep -Fq 'CLI_TRANSITION_PHASE' "$pkgbuild"; then die "Go package retains CLI phase metadata: $pkgbuild"; fi
done

printf '%s\n' 'single-CLI Go, source-built Rust preview, and Web AUR matrix verified'
