#!/usr/bin/env bash
# Run only on an independent builder with the complete pinned source checkout.
# This does not install tools, build images, push, deploy, or run the user's CLI.
set -euo pipefail
if [[ $# -ne 3 ]]; then
  echo 'usage: bash build_components.sh core|core-admin|extensions|web SOURCE_ROOT OUTPUT_DIR' >&2
  exit 2
fi
component=$1
source_root=$(cd -- "$2" && pwd)
mkdir -p -- "$3"
output=$(cd -- "$3" && pwd)
expected=${SOURCE_COMMIT:-72667564c0431754d4856dc2e0db55f360bd2745}
[[ $(git -C "$source_root" rev-parse --verify HEAD) == "$expected" ]] || { echo 'Source revision mismatch' >&2; exit 2; }
[[ -z $(git -C "$source_root" status --porcelain --untracked-files=all) ]] || { echo 'Source tree is not clean' >&2; exit 2; }
case "$output/" in "$source_root/"*) echo 'Output must be outside the source checkout' >&2; exit 2;; esac
case "$component" in
  core|core-admin)
    command -v cargo >/dev/null
    [[ $(rustc --version) == 'rustc 1.99.0 '* ]] || { echo 'Builder needs pinned Rust 1.99.0' >&2; exit 2; }
    mkdir -p "$output/bin" "$output/debug"
    (
      cd "$source_root/apps/lmm-core"
      # Preserve a full debug binary ON THE BUILDER, strip a separate runtime copy.
      CARGO_PROFILE_RELEASE_STRIP=false CARGO_PROFILE_RELEASE_DEBUG=1 \
        CARGO_TARGET_DIR="$output/build-cache/rust" cargo build --locked --release --bin "lmm-$component"
    )
    cp "$output/build-cache/rust/release/lmm-$component" "$output/debug/lmm-$component.full"
    objcopy --only-keep-debug "$output/debug/lmm-$component.full" "$output/debug/lmm-$component.debug"
    cp "$output/debug/lmm-$component.full" "$output/bin/lmm-$component"
    strip --strip-unneeded "$output/bin/lmm-$component"
    objcopy --add-gnu-debuglink="$output/debug/lmm-$component.debug" "$output/bin/lmm-$component"
    ;;
  extensions)
    command -v go >/dev/null
    [[ $(go version) == 'go version go1.27.2 '* ]] || { echo 'Builder needs pinned Go 1.27.2' >&2; exit 2; }
    mkdir -p "$output/bin" "$output/debug"
    (
      cd "$source_root/apps/lmm-extensions"
      export GOWORK=off GOTOOLCHAIN=local CGO_ENABLED=0
      go build -mod=readonly -trimpath -buildvcs=false -o "$output/debug/lmm-extensions.full" ./cmd/extensions
      go build -mod=readonly -trimpath -buildvcs=false -ldflags='-s -w' -o "$output/bin/lmm-extensions" ./cmd/extensions
    )
    ;;
  web)
    command -v bun >/dev/null
    [[ $(bun --version) == 1.3.14 ]] || { echo 'Builder needs pinned Bun 1.3.14' >&2; exit 2; }
    (cd "$source_root" && bun install --frozen-lockfile && bun run --filter @lmm/web build)
    mkdir -p "$output/web"
    cp -a "$source_root/apps/web/dist/." "$output/web/"
    ;;
  *) echo 'Unknown component; no build started' >&2; exit 2;;
esac
# Build artifacts and debug files have separate manifests. Do not include debug/
# or build-cache/ in the runtime build context. Export only bin/ or web/.
for dir in "$output/bin" "$output/web"; do
  if [[ -d $dir ]]; then find "$dir" -type f -exec sha256sum {} \;; fi
done
