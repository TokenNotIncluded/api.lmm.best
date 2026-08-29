#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly SCRIPT_DIR
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../../.." && pwd -P)
readonly REPO_ROOT

WORKSPACE=${LMM_API_BUILD_WORKSPACE:-}
GO_BINARY=${LMM_API_GO_BINARY:-}
FRONTEND_DIST=${LMM_API_FRONTEND_DIST:-$REPO_ROOT/apps/web/dist}
OUTPUT_DIR=${LMM_API_PKGDEST:-}

die() {
  printf 'build-local-lmm-api-go: %s\n' "$*" >&2
  exit 1
}

while (($#)); do
  case $1 in
  --workspace)
    (($# >= 2)) || die '--workspace requires a path'
    WORKSPACE=$2
    shift 2
    ;;
  --binary)
    (($# >= 2)) || die '--binary requires a path'
    GO_BINARY=$2
    shift 2
    ;;
  --frontend)
    (($# >= 2)) || die '--frontend requires a path'
    FRONTEND_DIST=$2
    shift 2
    ;;
  --output-dir)
    (($# >= 2)) || die '--output-dir requires a path'
    OUTPUT_DIR=$2
    shift 2
    ;;
  -h | --help)
    printf '%s\n' 'Usage: build-local-package.sh --workspace PATH --binary PATH [--frontend PATH] [--output-dir PATH]'
    exit 0
    ;;
  *) die "unknown argument: $1" ;;
  esac
done

[[ -n $WORKSPACE && $WORKSPACE == /* ]] || die '--workspace must be an absolute marker-owned path'
[[ -d $WORKSPACE && ! -L $WORKSPACE && -f $WORKSPACE/.lmm-deploy-workspace && ! -L $WORKSPACE/.lmm-deploy-workspace ]] ||
  die 'workspace is missing or does not carry .lmm-deploy-workspace'
[[ $(realpath -e -- "$WORKSPACE") == "$WORKSPACE" ]] || die 'workspace must be canonical'
[[ -n $GO_BINARY && $GO_BINARY == /* && -x $GO_BINARY && ! -L $GO_BINARY ]] ||
  die '--binary must be an absolute executable regular file'
[[ -d $FRONTEND_DIST && ! -L $FRONTEND_DIST && -f $FRONTEND_DIST/index.html && ! -L $FRONTEND_DIST/index.html ]] ||
  die 'frontend dist is missing, unsafe, or lacks index.html'
OUTPUT_DIR=${OUTPUT_DIR:-$WORKSPACE/artifacts}
[[ $OUTPUT_DIR == /* ]] || die '--output-dir must be absolute'
mkdir -p -- "$OUTPUT_DIR" "$WORKSPACE/tmp"
OUTPUT_DIR=$(realpath -e -- "$OUTPUT_DIR")

for command in file install makepkg pacman realpath sha256sum tar; do
  command -v "$command" >/dev/null 2>&1 || die "required command is unavailable: $command"
done
file -Lb "$GO_BINARY" | grep -Eq '^ELF 64-bit LSB (pie )?executable, x86-64,' ||
  die 'Go binary is not an x86-64 ELF executable'

pkgver=$({ "$GO_BINARY" version || true; } | head -n1)
pkgver=${pkgver#v}
[[ $pkgver =~ ^[0-9][0-9A-Za-z._+]*$ ]] || die 'Go binary returned an invalid package version'

build_dir=$(mktemp -d "$WORKSPACE/tmp/lmm-api-go-package.XXXXXXXX")
pkgdest=$(mktemp -d "$WORKSPACE/tmp/lmm-api-go-pkgdest.XXXXXXXX")
cleanup() { rm -rf -- "$build_dir" "$pkgdest"; }
trap cleanup EXIT
mkdir -p -- "$build_dir/makepkg"

install -Dm0644 "$SCRIPT_DIR/PKGBUILD" "$build_dir/PKGBUILD"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/lmm-api-go-package.sh" \
  "$build_dir/lmm-api-go-package.sh"
install -Dm0755 "$GO_BINARY" "$build_dir/lmm-api-go"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/lmm-api.service" "$build_dir/lmm-api.service"
install -Dm0600 "$REPO_ROOT/packaging/common/lmm-api/lmm-api-go.env" "$build_dir/lmm-api-go.env"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/lmm-api-operator.sysusers" "$build_dir/lmm-api-operator.sysusers"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/lmm-api-operator.tmpfiles" "$build_dir/lmm-api-operator.tmpfiles"
install -Dm0440 "$REPO_ROOT/packaging/common/lmm-api/lmm-api-operator.sudoers" "$build_dir/lmm-api-operator.sudoers"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/geoip2-country-update.service" "$build_dir/geoip2-country-update.service"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/geoip2-country-update.timer" "$build_dir/geoip2-country-update.timer"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/edge-policy/nginx/http-map.conf" "$build_dir/nginx-http-map.conf"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/edge-policy/nginx/lmm-api-locations.conf" "$build_dir/nginx-locations.conf"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/edge-policy/nginx/mime.types" "$build_dir/nginx-mime.types"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/edge-policy/nginx/new-api.conf" "$build_dir/nginx-new-api.conf"
install -Dm0644 "$REPO_ROOT/packaging/common/lmm-api/edge-policy/nginx/lmm-api-region-policy.conf" "$build_dir/nginx-region-policy.conf"
for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
  install -Dm0644 "$REPO_ROOT/$file" "$build_dir/$file"
done
git -C "$REPO_ROOT" rev-parse HEAD >"$build_dir/REVISION"
tar --sort=name --mtime='UTC 1970-01-01' --owner=0 --group=0 --numeric-owner \
  -C "$FRONTEND_DIST" -cf "$build_dir/frontend-dist.tar" .

(
  cd -- "$build_dir"
  BUILDDIR="$build_dir/makepkg" PKGDEST="$pkgdest" \
    LMM_API_PKGVER="$pkgver" LMM_API_PKGREL=1 \
    makepkg --force --nodeps --noconfirm --cleanbuild
)

matches=("$pkgdest/lmm-api-go-bin-$pkgver-1-x86_64.pkg.tar."*)
[[ ${#matches[@]} -eq 1 && -f ${matches[0]} ]] || die 'local lmm-api-go package was not produced exactly once'
destination="$OUTPUT_DIR/${matches[0]##*/}"
install -Dm0644 "${matches[0]}" "$destination"
sha256sum "$destination" >"$destination.sha256"
pacman -Qip "$destination"
printf 'package=%s\nsha256_file=%s\n' "$destination" "$destination.sha256"
