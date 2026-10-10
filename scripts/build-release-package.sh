#!/usr/bin/env bash
set -Eeuo pipefail

component=${1:?component (go|web) is required}
version=${2:?release version is required}
assets=${3:?asset directory is required}
output=${4:?output package path is required}
repo=${5:?repository root is required}
package_release=${6:-1}
[[ "$package_release" =~ ^[1-9][0-9]*(\.[0-9]+)?$ ]]

case "$component" in
  go) package_name=lmm-api-go-bin; recipe="$repo/packaging/aur/lmm-api-go-bin" ;;
  web) package_name=lmm-api-web-bin; recipe="$repo/packaging/aur/lmm-api-web-bin" ;;
  *) printf 'unsupported package component\n' >&2; exit 2 ;;
esac

# makepkg's AUR prepare() hook verifies the signed release bundle. The
# ArchLinux build container does not include cosign by default.
if ! command -v cosign >/dev/null 2>&1; then
  pacman -Sy --noconfirm --needed cosign
fi

build=$(mktemp -d /tmp/lmm-package.XXXXXX)
trap 'rm -rf -- "$build"' EXIT
# AUR recipes share helper files through repository-relative symlinks. The
# isolated build directory needs the helper contents, not broken copied links.
cp -aL "$recipe/." "$build/"
sed -i -E "s/^pkgver=.*/pkgver=${version}/" "$build/PKGBUILD"
sed -i -E "s/^pkgrel=.*/pkgrel=${package_release}/" "$build/PKGBUILD"

asset=$(find "$assets" -maxdepth 1 -type f -name "lmm-api-${component}-${version}*.tar.gz" -print -quit)
[[ -n "$asset" && -f "$asset" ]]
asset_name=$(basename "$asset")
checksum="$assets/${asset_name}.sha256"
bundle="$assets/${asset_name}.sigstore.json"
[[ -f "$checksum" && -f "$bundle" ]]

# Reconstructed rollback packages must use their own signed install hook,
# not today's repository hook (which may require a newer backend CLI).
cosign verify-blob --bundle "$bundle" \
  --certificate-identity "https://github.com/TokenNotIncluded/api.lmm.best/.github/workflows/release-${component}.yml@refs/tags/${component}-v${version}" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com "$asset"
if [[ "$component" == web ]]; then
  bsdtar -xOf "$asset" lmm-api-web.install >"$build/lmm-api-web.install"
  [[ -s "$build/lmm-api-web.install" ]]
fi

# makepkg requires a writable SRCDEST. Keep the verified release inputs in the
# container-local build directory instead of writing to the read-only/mounted
# controller asset directory.
install -m0644 "$asset" "$checksum" "$bundle" "$build/"

asset_sha=$(sha256sum "$asset" | awk '{print $1}')
checksum_sha=$(sha256sum "$checksum" | awk '{print $1}')
bundle_sha=$(sha256sum "$bundle" | awk '{print $1}')
if [[ "$component" == go ]]; then
  perl -0pi -e "s/sha256sums_x86_64=\(.*?\)/sha256sums_x86_64=(\n  '${asset_sha}'\n  '${checksum_sha}'\n  '${bundle_sha}'\n)/s" "$build/PKGBUILD"
else
  perl -0pi -e "s/sha256sums=\(.*?\)/sha256sums=(\n  '${asset_sha}'\n  '${checksum_sha}'\n  '${bundle_sha}'\n)/s" "$build/PKGBUILD"
fi

pkgdest="$build/pkgdest"
builddir="$build/makepkg"
mkdir -p "$pkgdest" "$builddir"
useradd --create-home --uid 1000 package-builder 2>/dev/null || true
chown -R package-builder:package-builder "$build"
runuser -u package-builder -- env SRCDEST="$build" PKGDEST="$pkgdest" BUILDDIR="$builddir" \
  makepkg --nodeps --noconfirm --cleanbuild --clean --holdver --dir "$build"
package=$(find "$pkgdest" -maxdepth 1 -type f \
  -name "${package_name}-${version}-${package_release}-*.pkg.tar.*" -print -quit)
[[ -n "$package" && -f "$package" ]]
install -Dm0644 "$package" "$output"
