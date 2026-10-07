#!/usr/bin/env bash
set -Eeuo pipefail

# The preceding pinned sigstore/cosign-installer action verifies this binary.
# CI then needs the same root-owned canonical path as the native command runner.
[[ ${GITHUB_ACTIONS:-} == true ]] || { printf 'GitHub Actions preparation only\n' >&2; exit 2; }
cosign_source=$(command -v cosign)
[[ $cosign_source == /* && -f $cosign_source && -x $cosign_source ]]
cosign_source_sha=$(sha256sum -- "$cosign_source")
cosign_source_sha=${cosign_source_sha%% *}
[[ $cosign_source_sha =~ ^[0-9a-f]{64}$ ]]
cosign_stage=$(mktemp "${RUNNER_TEMP:?}/native-cosign.XXXXXX")
trap 'rm -f -- "$cosign_stage"' EXIT
install --mode=0755 -- "$cosign_source" "$cosign_stage"
cosign_stage_sha=$(sha256sum -- "$cosign_stage")
[[ ${cosign_stage_sha%% *} == "$cosign_source_sha" ]]
sudo install --owner=root --group=root --mode=0755 -- "$cosign_stage" /usr/local/bin/cosign
[[ -f /usr/local/bin/cosign && ! -L /usr/local/bin/cosign ]]
cosign_installed_sha=$(sha256sum -- /usr/local/bin/cosign)
[[ ${cosign_installed_sha%% *} == "$cosign_source_sha" ]]
[[ $(stat --format='%u:%g:%a' /usr/local/bin/cosign) == 0:0:755 ]]
/usr/local/bin/cosign version
