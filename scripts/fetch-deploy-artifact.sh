#!/usr/bin/env bash
# Download one existing official release. Never build, publish, or contact a server.
set -Eeuo pipefail
umask 077

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
log() { printf '%s\n' "$*" >&2; }
usage() {
  echo 'Usage: fetch-deploy-artifact.sh web|go TAG SOURCE_SHA NEW_DIRECTORY [amd64|arm64]'
}
if [[ ${1:-} == --help || ${1:-} == -h ]]; then usage; exit 0; fi
[[ $# -ge 4 && $# -le 5 ]] || { usage >&2; exit 2; }
component=$1 tag=$2 revision=$3 output=$4 arch=${5:-amd64}
[[ $component == web || $component == go ]] || fail 'Component must be web or go.'
[[ $tag =~ ^${component}-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || fail 'Invalid release tag.'
[[ $revision =~ ^[0-9a-f]{40}$ ]] || fail 'SOURCE_SHA must be a full lowercase Git commit SHA.'
[[ $arch == amd64 || $arch == arm64 ]] || fail 'Architecture must be amd64 or arm64.'
[[ $component != web || $# == 4 ]] || fail 'Web artifacts do not take an architecture.'
[[ -n $output && $output != */ ]] || fail 'Give a new output directory, without a trailing slash.'
for command in curl cosign tar sha256sum mktemp mv; do
  command -v "$command" >/dev/null || fail "Required command not found: $command"
done

parent=$(cd -- "$(dirname -- "$output")" && pwd -P)
name=$(basename -- "$output")
[[ $name != . && $name != .. ]] || fail 'Invalid output directory.'
output=$parent/$name
[[ ! -e $output && ! -L $output ]] || fail 'Output already exists; nothing was changed.'
work=$(mktemp -d "$parent/.lmm-artifact.XXXXXX")
# Only remove the temporary directory created by this invocation.
trap 'rm -rf -- "$work"' EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

repository=TokenNotIncluded/api.lmm.best
version=${tag#*-v}
if [[ $component == web ]]; then
  asset="lmm-api-web-${version}.tar.gz"
  revision_member=REVISION
else
  asset="lmm-api-go-${version}-linux-${arch}.tar.gz"
  revision_member="${asset%.tar.gz}/REVISION"
fi
log "[$component $tag] Download official artifact and signature."
for suffix in '' .sha256 .sigstore.json; do
  curl --proto '=https' --proto-redir '=https' \
    --fail --silent --show-error --location \
    --connect-timeout 10 --max-time 180 \
    --output "$work/$asset$suffix" \
    "https://github.com/$repository/releases/download/$tag/$asset$suffix"
done

# Do not allow the downloaded checksum file to select arbitrary local paths.
mapfile -t checksums < "$work/$asset.sha256"
[[ ${#checksums[@]} == 1 ]] || fail 'Expected one checksum entry.'
read -r digest checksum_name extra <<< "${checksums[0]}"
[[ $digest =~ ^[0-9a-f]{64}$ && $checksum_name == "$asset" && -z $extra ]] || fail 'Invalid checksum entry.'
printf '%s  %s\n' "$digest" "$asset" | (cd -- "$work"; sha256sum --check --status)
log "[$component $tag] Verify the official workflow signature."
cosign verify-blob \
  --bundle "$work/$asset.sigstore.json" \
  --certificate-identity "https://github.com/$repository/.github/workflows/release-$component.yml@refs/tags/$tag" \
  --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' \
  "$work/$asset" >&2

# Read only this member after signature verification. Do not unpack the archive.
actual_revision=$(tar -xOzf "$work/$asset" -- "$revision_member")
[[ $actual_revision == "$revision" ]] || fail 'Signed artifact does not match SOURCE_SHA.'
# GNU mv: do not merge into, follow, or replace an output created concurrently.
mv --no-clobber --no-target-directory -- "$work" "$output"
[[ ! -e $work ]] || fail 'Output appeared during download; nothing was replaced.'
trap - EXIT
log "[$component $tag] Verified source $revision."
printf '%s\n' "$output"
