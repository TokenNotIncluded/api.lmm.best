#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly HERE
: "${TMPDIR:?set TMPDIR to a marker-owned test workspace}"
work=$(mktemp -d "$TMPDIR/lmm-test-go-release-pins.XXXXXXXX")
cleanup() { rm -rf -- "$work"; }
trap cleanup EXIT

fail() {
  printf 'test-verify-go-release-pins: %s\n' "$*" >&2
  exit 1
}

mkdir -p "$work/aur/lmm-api-go-bin" "$work/aur/lmm-api-go" "$work/assets"
cp "$HERE/verify-go-release-pins.sh" "$HERE/check-candidate-version.sh" "$work/aur/"
printf 'pkgver=0.2.14\npkgrel=1\n' >"$work/aur/lmm-api-go-bin/PKGBUILD"
cp "$work/aur/lmm-api-go-bin/PKGBUILD" "$work/aur/lmm-api-go/PKGBUILD"
printf '{"draft":false,"prerelease":false,"assets":[]}\n' >"$work/release.json"
for arch in amd64 arm64; do
  artifact="lmm-api-go-0.2.14-linux-$arch.tar.gz"
  printf 'fixture archive %s\n' "$arch" >"$work/assets/$artifact"
  digest=$(sha256sum "$work/assets/$artifact")
  printf '%s  %s\n' "${digest%% *}" "$artifact" >"$work/assets/$artifact.sha256"
  printf 'fixture bundle %s\n' "$arch" >"$work/assets/$artifact.sigstore.json"
  carch=x86_64
  [[ $arch == arm64 ]] && carch=aarch64
  for name in "$artifact" "$artifact.sha256" "$artifact.sigstore.json"; do
    digest=$(sha256sum "$work/assets/$name")
    digest=${digest%% *}
    printf '\tsha256sums_%s = %s\n' "$carch" "$digest" >>"$work/aur/lmm-api-go-bin/.SRCINFO"
    jq --arg name "$name" --arg digest "$digest" '.assets += [{
      name: $name, digest: ("sha256:" + $digest),
      browser_download_url: ("https://assets.example/" + $name)
    }]' "$work/release.json" >"$work/release.next"
    mv "$work/release.next" "$work/release.json"
  done
done
cp "$work/release.json" "$work/release.valid.json"

# Replace external services and signature verification, retaining real jq and
# SHA-256 checks. Unexpected requests fail closed; these tests never use network.
cat >"$work/mocks.sh" <<'MOCKS'
curl() {
  local url=${!#} output= arg previous= fixture
  printf '%s\n' "$url" >>"$PIN_FIXTURES/calls"
  for arg in "$@"; do
    [[ $previous != --output ]] || output=$arg
    previous=$arg
  done
  case $url in
    'https://api.example/repos/example/repo/releases?per_page=100') fixture=latest.json ;;
    https://api.example/repos/example/repo/git/ref/tags/go-v0.2.14) fixture=tag-ref.json ;;
    https://api.example/repos/example/repo/git/tags/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa) fixture=tag.json ;;
    https://api.example/repos/example/repo/compare/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb...main) fixture=compare.json ;;
    https://api.example/repos/example/repo/releases/tags/go-v0.2.14) fixture=release.json ;;
    'https://aur.archlinux.org/rpc/v5/info?arg[]=lmm-api-go&arg[]=lmm-api-go-bin') fixture=aur.json ;;
    https://assets.example/*) fixture="assets/${url##*/}" ;;
    *) printf 'unexpected request: %s\n' "$url" >&2; return 1 ;;
  esac
  if [[ -n $output ]]; then
    cp "$PIN_FIXTURES/$fixture" "$output"
  else
    cat "$PIN_FIXTURES/$fixture"
  fi
}
cosign() {
  printf 'cosign %s\n' "$*" >>"$PIN_FIXTURES/calls"
  [[ $* == *'--certificate-identity https://github.com/example/repo/.github/workflows/release-go.yml@refs/tags/go-v0.2.14'* &&
     $* == *'--certificate-oidc-issuer https://token.actions.githubusercontent.com'* &&
     ! -e $PIN_FIXTURES/reject-signature ]]
}
# Only fixture inputs are accepted; Arch vercmp's general version semantics are
# covered by test-matrix.sh with the real executable.
vercmp() {
  case "$1:$2" in
    0.1.19.r1279.g0c463f094-1:0.2.14-1) printf '%s\n' -1 ;;
    0.2.14-1:0.2.14-1) printf '%s\n' 0 ;;
    0.2.15-1:0.2.14-1|0.1.19.r1279.g0c463f094-1:0.1.18-1) printf '%s\n' 1 ;;
    *) printf 'unexpected version comparison: %s %s\n' "$1" "$2" >&2; return 1 ;;
  esac
}
MOCKS

reset_fixtures() {
  cp "$work/release.valid.json" "$work/release.json"
  printf '{"object":{"type":"tag","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}\n' >"$work/tag-ref.json"
  printf '{"verification":{"verified":true},"object":{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}\n' >"$work/tag.json"
  printf '{"status":"ahead"}\n' >"$work/compare.json"
  printf '[{"tag_name":"go-v0.2.14","draft":false,"prerelease":false},{"tag_name":"go-v0.2.99","draft":false,"prerelease":true}]\n' >"$work/latest.json"
  printf '{"results":[{"Name":"lmm-api-go","Version":"0.2.14-1"},{"Name":"lmm-api-go-bin","Version":"0.2.14-1"}]}\n' >"$work/aur.json"
  rm -f -- "$work/reject-signature"
  : >"$work/calls"
}

verify_fixture() {
  BASH_ENV="$work/mocks.sh" PIN_FIXTURES="$work" \
    GITHUB_API_URL=https://api.example GITHUB_REPOSITORY=example/repo GITHUB_TOKEN='' \
    bash "$work/aur/verify-go-release-pins.sh" "$@"
}

expect_rejected() {
  local expected=$1
  shift
  if verify_fixture "$@" >"$work/rejected.out" 2>&1; then
    fail "unexpectedly accepted: $expected"
  fi
  grep -Fq -- "$expected" "$work/rejected.out" || {
    cat "$work/rejected.out" >&2
    fail "expected rejection: $expected"
  }
}

reset_fixtures
verify_fixture --pinned >/dev/null
[[ $(grep -c '^cosign ' "$work/calls") == 2 ]] || fail 'both architectures must verify signatures'
! grep -Eq 'releases\?per_page|aur.archlinux.org' "$work/calls" || fail 'pinned mode queried mutable publication state'

# Publishing the next release must leave checked-in pins valid in CI, while
# the default and explicit publication audit must still reject stale recipes.
printf '[{"tag_name":"go-v0.2.15","draft":false,"prerelease":false}]\n' >"$work/latest.json"
printf '{"results":[{"Name":"lmm-api-go","Version":"0.2.15-1"},{"Name":"lmm-api-go-bin","Version":"0.2.15-1"}]}\n' >"$work/aur.json"
verify_fixture --pinned >/dev/null
expect_rejected 'not pinned to the latest Go release: go-v0.2.15'
expect_rejected 'not pinned to the latest Go release: go-v0.2.15' --latest

reset_fixtures
verify_fixture --latest >/dev/null
grep -Fq 'aur.archlinux.org' "$work/calls" || fail 'latest mode did not check AUR versions'
printf '{"results":[{"Name":"lmm-api-go","Version":"0.2.15-1"},{"Name":"lmm-api-go-bin","Version":"0.2.14-1"}]}\n' >"$work/aur.json"
expect_rejected 'lmm-api-go candidate failed the published-version contract' --latest

# CI's pinned mode retains every authenticity and integrity gate.
reset_fixtures
printf '{"object":{"type":"commit","sha":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}\n' >"$work/tag-ref.json"
expect_rejected 'not an annotated tag' --pinned
reset_fixtures
printf '{"verification":{"verified":false},"object":{"type":"commit","sha":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}\n' >"$work/tag.json"
expect_rejected 'not a GitHub-verified signed commit tag' --pinned
reset_fixtures
printf '{"status":"diverged"}\n' >"$work/compare.json"
expect_rejected 'does not identify an ancestor of main' --pinned
reset_fixtures
jq '.prerelease = true' "$work/release.valid.json" >"$work/release.json"
expect_rejected 'not a final release' --pinned
reset_fixtures
jq '.assets[0].digest = "sha256:bad"' "$work/release.valid.json" >"$work/release.json"
expect_rejected 'does not match its PKGBUILD and GitHub release digest' --pinned
reset_fixtures
printf 'tampered\n' >>"$work/assets/lmm-api-go-0.2.14-linux-arm64.tar.gz"
expect_rejected 'does not match its PKGBUILD and GitHub release digest' --pinned
printf 'fixture archive arm64\n' >"$work/assets/lmm-api-go-0.2.14-linux-arm64.tar.gz"
reset_fixtures
touch "$work/reject-signature"
expect_rejected 'Sigstore bundle is invalid' --pinned
reset_fixtures
printf 'pkgver=0.1.18\npkgrel=1\n' >"$work/aur/lmm-api-go/PKGBUILD"
expect_rejected 'source package version is not newer than the published floor' --pinned
expect_rejected 'usage:' --invalid

printf 'Go pinned-release integrity and latest-publication policy fixtures verified\n'
