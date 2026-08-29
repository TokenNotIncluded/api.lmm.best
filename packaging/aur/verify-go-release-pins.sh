#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
readonly REPOSITORY=${GITHUB_REPOSITORY:-LIghtJUNction/api.lmm.best}
readonly API_ROOT=${GITHUB_API_URL:-https://api.github.com}
readonly PKGBUILD="$HERE/lmm-api-go-bin/PKGBUILD"
readonly SRCINFO="$HERE/lmm-api-go-bin/.SRCINFO"
readonly SOURCE_PKGBUILD="$HERE/lmm-api-go/PKGBUILD"
readonly VERSION_CHECK="$HERE/check-candidate-version.sh"
readonly PUBLISHED_SOURCE_FLOOR=0.1.19.r1279.g0c463f094-1
readonly CURL_RETRY_ARGS=(--retry 4 --retry-all-errors --retry-delay 2 --connect-timeout 15)

fail() {
  printf 'verify-go-release-pins: %s\n' "$*" >&2
  exit 1
}

api_get() {
  local url=$1
  if [[ -n ${GITHUB_TOKEN:-} ]]; then
    curl --fail --location --silent --show-error "${CURL_RETRY_ARGS[@]}" --max-time 60 \
      --header "Authorization: Bearer $GITHUB_TOKEN" \
      --header 'Accept: application/vnd.github+json' "$url"
  else
    curl --fail --location --silent --show-error "${CURL_RETRY_ARGS[@]}" --max-time 60 \
      --header 'Accept: application/vnd.github+json' "$url"
  fi
}

download_asset() {
  local url=$1 output=$2
  if [[ -n ${GITHUB_TOKEN:-} ]]; then
    curl --fail --location --silent --show-error "${CURL_RETRY_ARGS[@]}" --max-time 300 \
      --header "Authorization: Bearer $GITHUB_TOKEN" \
      --output "$output" "$url"
  else
    curl --fail --location --silent --show-error "${CURL_RETRY_ARGS[@]}" --max-time 300 \
      --output "$output" "$url"
  fi
}

for command in cosign curl git jq makepkg sha256sum sort vercmp; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done
[[ -x $VERSION_CHECK ]] || fail 'AUR candidate version checker is missing or not executable'
: "${TMPDIR:?set TMPDIR to a marker-owned verification workspace}"
work=$(mktemp -d "$TMPDIR/lmm-go-release-pins.XXXXXXXX")
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

pkgver=$(sed -n 's/^pkgver=//p' "$PKGBUILD")
[[ $pkgver =~ ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
  fail 'binary PKGBUILD has an invalid release version'
latest_tag=$(api_get "$API_ROOT/repos/$REPOSITORY/releases?per_page=100" |
  jq -r '.[] | select(.draft == false and (.tag_name | test("^go-v[0-9]+\\.[0-9]+\\.[0-9]+$"))) | .tag_name' |
  sort -V | tail -n 1)
[[ -n $latest_tag ]] || fail 'no published Go release was found'
[[ $latest_tag == "go-v$pkgver" ]] ||
  fail "binary package is not pinned to the latest Go release: $latest_tag"

tag_ref=$(api_get "$API_ROOT/repos/$REPOSITORY/git/ref/tags/$latest_tag")
tag_object_sha=$(jq -r '.object.sha' <<<"$tag_ref")
[[ $(jq -r '.object.type' <<<"$tag_ref") == tag && $tag_object_sha =~ ^[0-9a-f]{40}$ ]] ||
  fail "$latest_tag is not an annotated tag"
tag_object=$(api_get "$API_ROOT/repos/$REPOSITORY/git/tags/$tag_object_sha")
tag_revision=$(jq -r '.object.sha' <<<"$tag_object")
[[ $(jq -r '.verification.verified' <<<"$tag_object") == true &&
   $(jq -r '.object.type' <<<"$tag_object") == commit && $tag_revision =~ ^[0-9a-f]{40}$ ]] ||
  fail "$latest_tag is not a GitHub-verified signed commit tag"
comparison=$(api_get "$API_ROOT/repos/$REPOSITORY/compare/$tag_revision...main")
case $(jq -r '.status' <<<"$comparison") in
  ahead|identical) ;;
  *) fail "$latest_tag does not identify an ancestor of main" ;;
esac
release_json="$work/release.json"
api_get "$API_ROOT/repos/$REPOSITORY/releases/tags/$latest_tag" >"$release_json"
[[ $(jq -r '.draft' "$release_json") == false && $(jq -r '.prerelease' "$release_json") == false ]] ||
  fail "$latest_tag is not a final release"

mapfile -t amd64_pins < <(awk '$1 == "sha256sums_x86_64" { print $3 }' "$SRCINFO")
mapfile -t arm64_pins < <(awk '$1 == "sha256sums_aarch64" { print $3 }' "$SRCINFO")
[[ ${#amd64_pins[@]} -eq 3 && ${#arm64_pins[@]} -eq 3 ]] ||
  fail 'binary .SRCINFO must pin archive, checksum, and Sigstore bundle per architecture'

for arch in amd64 arm64; do
  artifact="lmm-api-go-$pkgver-linux-$arch.tar.gz"
  names=("$artifact" "$artifact.sha256" "$artifact.sigstore.json")
  if [[ $arch == amd64 ]]; then
    pins=("${amd64_pins[@]}")
  else
    pins=("${arm64_pins[@]}")
  fi
  for index in 0 1 2; do
    name=${names[$index]}
    url=$(jq -r --arg name "$name" '.assets[] | select(.name == $name) | .browser_download_url' "$release_json")
    digest=$(jq -r --arg name "$name" '.assets[] | select(.name == $name) | .digest' "$release_json")
    [[ -n $url && $url != null && $digest == sha256:* ]] || fail "$latest_tag is missing $name or its digest"
    output="$work/$name"
    download_asset "$url" "$output"
    actual=$(sha256sum "$output")
    actual=${actual%% *}
    [[ $actual == "${pins[$index]}" && $actual == "${digest#sha256:}" ]] ||
      fail "$name does not match its PKGBUILD and GitHub release digest"
  done
  expected=$(awk 'NR == 1 { print $1 }' "$work/$artifact.sha256")
  [[ $expected == "${pins[0]}" ]] || fail "$artifact checksum asset does not bind the archive"
  cosign verify-blob \
    --bundle "$work/$artifact.sigstore.json" \
    --certificate-identity "https://github.com/$REPOSITORY/.github/workflows/release-go.yml@refs/tags/$latest_tag" \
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
    "$work/$artifact" >"$work/cosign-$arch.log" || fail "$artifact Sigstore bundle is invalid"
done

source_pkgver=$(sed -n 's/^pkgver=//p' "$SOURCE_PKGBUILD")
source_pkgrel=$(sed -n 's/^pkgrel=//p' "$SOURCE_PKGBUILD")
[[ -n $source_pkgver && $source_pkgrel =~ ^[1-9][0-9]*$ ]] ||
  fail 'source PKGBUILD has an invalid pkgver or pkgrel'
source_candidate="$source_pkgver-$source_pkgrel"
(( $(vercmp "$PUBLISHED_SOURCE_FLOOR" "$source_candidate") < 0 )) ||
  fail "source package version is not newer than the published floor: $PUBLISHED_SOURCE_FLOOR"
aur_json=$(curl --fail --location --silent --show-error "${CURL_RETRY_ARGS[@]}" --max-time 60 \
  'https://aur.archlinux.org/rpc/v5/info?arg[]=lmm-api-go&arg[]=lmm-api-go-bin')
for package in lmm-api-go lmm-api-go-bin; do
  published=$(jq -r --arg package "$package" '.results[] | select(.Name == $package) | .Version' <<<"$aur_json")
  [[ -n $published && $published != null ]] || fail "AUR did not report $package"
  candidate=$source_candidate
  [[ $package == lmm-api-go-bin ]] && candidate="$pkgver-1"
  "$VERSION_CHECK" "$package" "$candidate" "$published" >/dev/null ||
    fail "$package candidate failed the published-version contract"
done

printf 'latest Go release, AUR monotonicity, checksums, and Sigstore pins verified: %s\n' "$latest_tag"
