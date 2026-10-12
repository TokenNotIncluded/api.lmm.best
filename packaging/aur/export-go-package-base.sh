#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
CANONICAL_HELPER="$(cd -- "$HERE/../common/lmm-api" && pwd -P)/lmm-api-go-package.sh"
readonly CANONICAL_HELPER
readonly CANONICAL_HELPER_SHA256=655e9346a6d87baa1cb81d97dcc412243d7ee305f90371b99d89033ea0e99bb1

fail() {
  printf 'export-go-package-base: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 2 ]] || fail 'usage: export-go-package-base.sh PACKAGE DESTINATION'
readonly package=$1 destination=$2
case $package in
  lmm-api-go | lmm-api-go-bin | lmm-api-go-git) ;;
  *) fail "unsupported Go AUR package: $package" ;;
esac

readonly source_dir="$HERE/$package"
[[ -d $source_dir && ! -L $source_dir ]] || fail 'package source directory is missing or unsafe'
[[ -f $CANONICAL_HELPER && ! -L $CANONICAL_HELPER ]] || fail 'canonical Go package helper is missing or unsafe'
actual_helper_sha256=$(sha256sum "$CANONICAL_HELPER")
actual_helper_sha256=${actual_helper_sha256%% *}
[[ $actual_helper_sha256 == "$CANONICAL_HELPER_SHA256" ]] ||
  fail 'canonical Go package helper digest changed without updating the export contract'

for required in PKGBUILD .SRCINFO lmm-api-go-package.sh; do
  [[ -e $source_dir/$required || -L $source_dir/$required ]] || fail "package source is missing $required"
done
shopt -s dotglob nullglob
for entry in "$source_dir"/*; do
  name=${entry##*/}
  case $name in
    PKGBUILD | .SRCINFO)
      [[ -f $entry && ! -L $entry ]] || fail "$package/$name is not a regular file"
      ;;
    lmm-api-go-package.sh)
      [[ -L $entry ]] || fail "$package/$name must reference the canonical helper"
      resolved=$(realpath -e -- "$entry") || fail "$package/$name is dangling"
      [[ $resolved == "$CANONICAL_HELPER" ]] || fail "$package/$name escapes the canonical helper"
      ;;
    *) fail "$package contains an unexpected package-base entry: $name" ;;
  esac
done
shopt -u dotglob nullglob

parent=$(dirname -- "$destination")
base=$(basename -- "$destination")
[[ -d $parent && ! -L $parent ]] || fail 'destination parent must already exist and may not be a symlink'
[[ ! -e $destination && ! -L $destination ]] || fail 'destination already exists'
stage=$(mktemp -d "$parent/.${base}.export.XXXXXXXX")
cleanup() { rm -rf -- "$stage"; }
trap cleanup EXIT

install -m0644 "$source_dir/PKGBUILD" "$stage/PKGBUILD"
install -m0644 "$source_dir/.SRCINFO" "$stage/.SRCINFO"
install -m0644 "$CANONICAL_HELPER" "$stage/lmm-api-go-package.sh"
[[ $(find "$stage" -mindepth 1 -maxdepth 1 -type l -print -quit) == "" ]] ||
  fail 'export unexpectedly contains a symlink'
mapfile -t exported < <(find "$stage" -mindepth 1 -maxdepth 1 -printf '%f\n' | LC_ALL=C sort)
[[ ${exported[*]} == '.SRCINFO PKGBUILD lmm-api-go-package.sh' ]] ||
  fail 'exported package-base file inventory is invalid'
exported_helper_sha256=$(sha256sum "$stage/lmm-api-go-package.sh")
exported_helper_sha256=${exported_helper_sha256%% *}
[[ $exported_helper_sha256 == "$CANONICAL_HELPER_SHA256" ]] ||
  fail 'exported Go package helper digest is invalid'

mv -- "$stage" "$destination"
trap - EXIT
printf '%s\n' "$destination"
