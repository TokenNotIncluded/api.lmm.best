#!/usr/bin/env bash
set -Eeuo pipefail

report_error() {
  local status=$1 line=$2
  printf 'automatic deployment failed at line %s (exit %s)\n' "$line" "$status" >&2
  exit "$status"
}
trap 'report_error "$?" "$LINENO"' ERR

: "${RELEASE_TAG:?RELEASE_TAG is required}"
: "${RELEASE_SHA:?RELEASE_SHA is required}"
: "${REPOSITORY:?REPOSITORY is required}"
: "${GITHUB_TOKEN:?GITHUB_TOKEN is required}"
: "${PRODUCTION_SSH_PRIVATE_KEY:?PRODUCTION_SSH_PRIVATE_KEY is required}"
: "${PRODUCTION_SSH_KNOWN_HOSTS:?PRODUCTION_SSH_KNOWN_HOSTS is required}"
export GH_TOKEN="$GITHUB_TOKEN"

case "$RELEASE_TAG" in
  go-v*) component=go; version=${RELEASE_TAG#go-v} ;;
  web-v*) component=web; version=${RELEASE_TAG#web-v} ;;
  *) printf 'unsupported release tag: %s\n' "$RELEASE_TAG" >&2; exit 2 ;;
esac
[[ "$RELEASE_SHA" =~ ^[0-9a-f]{40}$ ]]
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]

umask 077
root=$(mktemp -d /tmp/lmm-auto-deploy.XXXXXX)
trap 'rm -rf -- "$root"' EXIT
mkdir -p "$root/assets" "$root/rollback" "$root/probe" "$root/pkg" "$root/controller" "$root/cosign-home"
chmod 700 "$root/cosign-home"

ssh_dir="$HOME/.ssh"
mkdir -p "$ssh_dir"
chmod 700 "$ssh_dir"
printf '%s\n' "$PRODUCTION_SSH_PRIVATE_KEY" >"$ssh_dir/lmm-production"
printf '%s\n' "$PRODUCTION_SSH_KNOWN_HOSTS" >"$ssh_dir/known_hosts"
chmod 600 "$ssh_dir/lmm-production" "$ssh_dir/known_hosts"
cat >"$ssh_dir/config" <<'EOF'
Host ArchDmit
  HostName 45.59.187.63
  Port 222
  User root
  IdentityFile ~/.ssh/lmm-production
  IdentitiesOnly yes
  UserKnownHostsFile ~/.ssh/known_hosts
  StrictHostKeyChecking yes
EOF
chmod 600 "$ssh_dir/config"

gh release view "$RELEASE_TAG" --repo "$REPOSITORY" --json tagName,targetCommitish,isDraft,isPrerelease \
  >"$root/release.json"
jq -e --arg tag "$RELEASE_TAG" --arg sha "$RELEASE_SHA" \
  '.tagName == $tag and .targetCommitish == $sha and .isDraft == false and .isPrerelease == false' \
  "$root/release.json" >/dev/null

fetch_release_tag() {
  local tag=$1
  git fetch --no-tags origin "refs/tags/${tag}:refs/tags/${tag}"
}

# workflow_run checkouts are pinned to a commit SHA and may not include the
# tag refs that the deployment CLI uses to bind signed packages to releases.
fetch_release_tag "$RELEASE_TAG"

download_release() {
  local tag=$1 dir=$2 release_component=$3 asset_version pattern workflow
  case "$release_component" in
    go)
      asset_version=${tag#go-v}
      pattern="lmm-api-go-${asset_version}-linux-amd64*"
      workflow=release-go.yml
      ;;
    web)
      asset_version=${tag#web-v}
      pattern="lmm-api-web-${asset_version}*"
      workflow=release-web.yml
      ;;
    *)
      printf 'unsupported release component: %s\n' "$release_component" >&2
      return 2
      ;;
  esac
  mkdir -p "$dir"
  gh release download "$tag" --repo "$REPOSITORY" --dir "$dir" --pattern "$pattern" --clobber
  [[ $(find "$dir" -maxdepth 1 -type f | wc -l) -eq 3 ]]
  for archive in "$dir"/*.tar.gz; do
    expected=$(awk 'NR == 1 {print $1}' "$archive.sha256")
    [[ "$expected" =~ ^[0-9a-f]{64}$ ]]
    printf '%s  %s\n' "$expected" "$archive" | sha256sum --check --status -
    cosign verify-blob --bundle "$archive.sigstore.json" \
      --certificate-identity "https://github.com/${REPOSITORY}/.github/workflows/${workflow}@refs/tags/${tag}" \
      --certificate-oidc-issuer https://token.actions.githubusercontent.com "$archive" >/dev/null
  done
}

download_release "$RELEASE_TAG" "$root/assets" "$component"

normalize_controller_file() {
  local path=$1
  local temporary="${path}.controller-copy"
  cp --reflink=auto "$path" "$temporary"
  mv -f "$temporary" "$path"
}

inventory=$(ssh ArchDmit 'set -eu
for package in lmm-api-go-bin lmm-api-web-bin; do
  version=$(pacman -Q "$package" | awk "{print \$2}")
  printf "%s\t%s\n" "$package" "$version"
done')
declare -A installed_version
while IFS=$'\t' read -r package pkgver; do
  [[ "$package" =~ ^lmm-api-(go|web)-bin$ && "$pkgver" =~ ^[0-9]+\.[0-9]+\.[0-9]+-[1-9][0-9]*(\.[0-9]+)?$ ]]
  installed_version["$package"]=$pkgver
done <<<"$inventory"

fetch_rollback() {
  local package=$1 release_component=$2 release_prefix=$3
  local pkgver=${installed_version[$package]}
  local release_version=${pkgver%-*}
  fetch_release_tag "${release_prefix}${release_version}"
  download_release "${release_prefix}${release_version}" "$root/rollback/$package" "$release_component"
}
fetch_rollback lmm-api-go-bin go go-v
fetch_rollback lmm-api-web-bin web web-v

deployment_image="lmm-production-deploy:${GITHUB_RUN_ID:-manual}"
mkdir -p "$root/image-context"
cat >"$root/image-context/Dockerfile" <<'EOF'
FROM archlinux:base-devel
ARG RUNNER_UID
ARG RUNNER_GID
ARG RUNNER_HOME
RUN pacman -Sy --noconfirm --needed cosign git openssh
RUN if ! getent group "${RUNNER_GID}" >/dev/null; then groupadd --gid "${RUNNER_GID}" lmm-runner; fi; \
    if getent passwd "${RUNNER_UID}" >/dev/null; then \
      usermod --home "${RUNNER_HOME}" "$(getent passwd "${RUNNER_UID}" | cut -d: -f1)"; \
    else \
      useradd --uid "${RUNNER_UID}" --gid "${RUNNER_GID}" --home-dir "${RUNNER_HOME}" --no-create-home lmm-runner; \
    fi
EOF
docker build --network host --tag "$deployment_image" \
  --build-arg RUNNER_UID="$(id -u)" \
  --build-arg RUNNER_GID="$(id -g)" \
  --build-arg RUNNER_HOME="$root/cosign-home" \
  "$root/image-context"

for rollback_component in go web; do
  rollback_package="lmm-api-${rollback_component}-bin"
  rollback_pkgver=${installed_version[$rollback_package]}
  rollback_version=${rollback_pkgver%-*}
  rollback_pkgrel=${rollback_pkgver##*-}
  rollback_output="$root/rollback/${rollback_package}-${rollback_pkgver}.pkg.tar.zst"
  rollback_container_output="/work/rollback/${rollback_package}-${rollback_pkgver}.pkg.tar.zst"
  docker run --rm --network host -v "$GITHUB_WORKSPACE:/repo:ro" -v "$root:/work" "$deployment_image" \
    bash /repo/scripts/build-release-package.sh "$rollback_component" "$rollback_version" \
      "/work/rollback/$rollback_package" "$rollback_container_output" \
      /repo "$rollback_pkgrel"
  normalize_controller_file "$rollback_output"
done

if [[ "$component" == go ]]; then
  candidate_output="$root/pkg/lmm-api-go-bin-${version}-1.pkg.tar.zst"
  docker run --rm --network host -v "$GITHUB_WORKSPACE:/repo:ro" -v "$root:/work" "$deployment_image" \
    bash /repo/scripts/build-release-package.sh go "$version" /work/assets "/work/pkg/$(basename "$candidate_output")" /repo
  normalize_controller_file "$candidate_output"
else
  candidate_output="$root/pkg/lmm-api-web-bin-${version}-1.pkg.tar.zst"
  docker run --rm --network host -v "$GITHUB_WORKSPACE:/repo:ro" -v "$root:/work" "$deployment_image" \
    bash /repo/scripts/build-release-package.sh web "$version" /work/assets "/work/pkg/$(basename "$candidate_output")" /repo
  normalize_controller_file "$candidate_output"
fi

go_rollback="$root/rollback/lmm-api-go-bin-${installed_version[lmm-api-go-bin]}.pkg.tar.zst"
web_rollback="$root/rollback/lmm-api-web-bin-${installed_version[lmm-api-web-bin]}.pkg.tar.zst"
go_candidate="$go_rollback"; web_candidate="$web_rollback"
[[ "$component" == go ]] && go_candidate="$candidate_output"
[[ "$component" == web ]] && web_candidate="$candidate_output"
go_asset=$(find "$root/assets" "$root/rollback/lmm-api-go-bin" -maxdepth 1 -type f -name 'lmm-api-go-*.tar.gz' -print -quit)
go_bundle="$go_asset.sigstore.json"
web_asset=$(find "$root/assets" "$root/rollback/lmm-api-web-bin" -maxdepth 1 -type f -name 'lmm-api-web-*.tar.gz' -print -quit)
web_bundle="$web_asset.sigstore.json"
go_rollback_asset=$(find "$root/rollback/lmm-api-go-bin" -maxdepth 1 -type f -name 'lmm-api-go-*.tar.gz' -print -quit)
web_rollback_asset=$(find "$root/rollback/lmm-api-web-bin" -maxdepth 1 -type f -name 'lmm-api-web-*.tar.gz' -print -quit)

tar -xzf "$go_asset" -C "$root/probe"
probe=$(find "$root/probe" -type f -name lmm-api-go -print -quit)
[[ -x "$probe" ]]

run_probe() {
  docker run --rm --network host --user "$(id -u):$(id -g)" \
    -e HOME="$root/cosign-home" \
    -v "$GITHUB_WORKSPACE:$GITHUB_WORKSPACE:ro" \
    -v "$root:$root" \
    -v "$ssh_dir:$root/cosign-home/.ssh:ro" \
    -w "$GITHUB_WORKSPACE" \
    "$deployment_image" "$@"
}

source "$GITHUB_WORKSPACE/scripts/production-deployment-id.sh"
deployment_id=$(production_deployment_id "$RELEASE_TAG" "${GITHUB_RUN_ID:?GITHUB_RUN_ID is required}" "${GITHUB_RUN_ATTEMPT:?GITHUB_RUN_ATTEMPT is required}")
printf 'format=1\ndeployment_id=%s\nrole=controller\ncreated_at_utc=%s\n' "$deployment_id" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"$root/controller/.lmm-deploy-workspace"
chmod 600 "$root/controller/.lmm-deploy-workspace"
plan_result=$(run_probe "$probe" deploy production plan --repo "$GITHUB_WORKSPACE" --workspace "$root/controller" --deployment-id "$deployment_id" \
  --go-package "$go_candidate" --go-release-asset "$go_asset" --go-release-bundle "$go_bundle" \
  --go-rollback-package "$go_rollback" --go-rollback-release-asset "$go_rollback_asset" --go-rollback-release-bundle "$go_rollback_asset.sigstore.json" \
  --web-package "$web_candidate" --web-release-asset "$web_asset" --web-release-bundle "$web_bundle" \
  --web-rollback-package "$web_rollback" --web-rollback-release-asset "$web_rollback_asset" --web-rollback-release-bundle "$web_rollback_asset.sigstore.json" \
  --probe-binary "$probe" --operator-binary "$probe" --preserve-edge-policy)
plan=$(jq -er '.plan' <<<"$plan_result")
plan_sha=$(jq -er '.plan_sha256' <<<"$plan_result")
run_probe "$probe" deploy production stage --plan "$plan" --plan-sha256 "$plan_sha" --confirm api.lmm.best
source "$GITHUB_WORKSPACE/scripts/production-promote-retry.sh"
production_promote_with_transport_retry "$root/promote.stderr" \
  run_probe "$probe" deploy production promote --plan "$plan" --plan-sha256 "$plan_sha" --confirm api.lmm.best
