#!/usr/bin/env bash
set -Eeuo pipefail

# The preceding pinned sigstore/cosign-installer action verifies this binary.
# CI then needs the same root-owned canonical path as the native command runner.
[[ ${GITHUB_ACTIONS:-} == true ]] || { printf 'GitHub Actions preparation only\n' >&2; exit 2; }
# Hosted runners can make /usr/local writable by their unprivileged account.
# Use the native runner's first fixed candidate without changing shared modes.
for cosign_parent in / /usr /usr/bin; do
  [[ -d $cosign_parent && ! -L $cosign_parent ]]
  read -r cosign_parent_uid cosign_parent_mode < <(stat --format='%u %a' -- "$cosign_parent")
  [[ $cosign_parent_uid == 0 && $cosign_parent_mode =~ ^[0-7]{3,4}$ ]]
  (( (8#$cosign_parent_mode & 0022) == 0 ))
done
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
sudo install --owner=root --group=root --mode=0755 -- "$cosign_stage" /usr/bin/cosign
[[ -f /usr/bin/cosign && ! -L /usr/bin/cosign ]]
cosign_installed_sha=$(sha256sum -- /usr/bin/cosign)
[[ ${cosign_installed_sha%% *} == "$cosign_source_sha" ]]
[[ $(stat --format='%u:%g:%a' /usr/bin/cosign) == 0:0:755 ]]
/usr/bin/cosign version
