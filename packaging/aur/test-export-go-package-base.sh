#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly EXPORTER="$HERE/export-go-package-base.sh"
readonly PACKAGES=(lmm-api-go lmm-api-go-bin lmm-api-go-git)
: "${TMPDIR:?set TMPDIR to a marker-owned test workspace}"

fail() {
  printf 'test-export-go-package-base: %s\n' "$*" >&2
  exit 1
}

for command in cmp find makepkg sha256sum; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done
[[ -x $EXPORTER ]] || fail 'Go AUR package-base exporter is missing or not executable'
work=$(mktemp -d "$TMPDIR/lmm-aur-export.XXXXXXXX")
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

mkdir -p "$work/exported"
for package in "${PACKAGES[@]}"; do
  destination="$work/exported/$package"
  "$EXPORTER" "$package" "$destination" >/dev/null
  [[ -f $destination/lmm-api-go-package.sh && ! -L $destination/lmm-api-go-package.sh ]] ||
    fail "$package export did not materialize the shared helper"
  [[ -z $(find "$destination" -type l -print -quit) ]] || fail "$package export contains a symlink"
  generated=$(cd -- "$destination" && makepkg --printsrcinfo)
  cmp -s <(printf '%s\n' "$generated") "$destination/.SRCINFO" ||
    fail "$package isolated export has stale .SRCINFO"
done

fixture="$work/fixture"
mkdir -p "$fixture/aur" "$fixture/common/lmm-api" "$fixture/output"
cp "$EXPORTER" "$fixture/aur/export-go-package-base.sh"
cp "$HERE/../common/lmm-api/lmm-api-go-package.sh" "$fixture/common/lmm-api/"
for package in "${PACKAGES[@]}"; do
  cp -a "$HERE/$package" "$fixture/aur/"
done
chmod 0755 "$fixture/aur/export-go-package-base.sh"

ln -s /etc/passwd "$fixture/aur/lmm-api-go/external-link"
if "$fixture/aur/export-go-package-base.sh" lmm-api-go "$fixture/output/unexpected-link" \
  >"$work/unexpected-link.out" 2>&1; then
  fail 'exporter accepted an unexpected external symlink'
fi
grep -Fq 'unexpected package-base entry: external-link' "$work/unexpected-link.out" ||
  fail 'unexpected external symlink rejection was not explicit'
rm -- "$fixture/aur/lmm-api-go/external-link"

rm -- "$fixture/aur/lmm-api-go/lmm-api-go-package.sh"
ln -s /etc/passwd "$fixture/aur/lmm-api-go/lmm-api-go-package.sh"
if "$fixture/aur/export-go-package-base.sh" lmm-api-go "$fixture/output/escaped-helper" \
  >"$work/escaped-helper.out" 2>&1; then
  fail 'exporter accepted a helper symlink outside the canonical source'
fi
grep -Fq 'escapes the canonical helper' "$work/escaped-helper.out" ||
  fail 'escaped helper rejection was not explicit'

printf 'isolated Go AUR package-base exports verified\n'
