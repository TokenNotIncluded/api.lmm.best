#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "$(readlink -f -- "${BASH_SOURCE[0]}")")" && pwd -P)
readonly SCRIPT_DIR
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd -P)
readonly REPO_ROOT

case ${1:-help} in
  help|-h|--help)
    cat <<'USAGE'
Usage: scripts/lmm-api-deploy.sh <command> [options]

Frontend-only deployment from a workstation (GitHub CLI; no backend build):
  web ship [web-vX.Y.Z]            Tag origin/main, sign-publish, deploy, wait
  web release [web-vX.Y.Z]         Tag origin/main and sign-publish only
  web deploy web-vX.Y.Z            Dispatch an existing signed release once
  web list                        List recent frontend deployment runs
  web status RUN_ID               Inspect one exact run
  web watch RUN_ID                Wait for that run; fail when it fails
  web --help                      Frontend deployment prerequisites

Existing standalone systemd installation (run on the target as root):
  systemd doctor [--migrate]       Check prerequisites without changing the host
  systemd upgrade --release ID --binary FILE --frontend DIR --confirm api.lmm.best
                                  Stage and apply once; confirmation stays explicit
  systemd status [--release ID]    Inspect transactions without creating state
  systemd confirm --release ID --confirm api.lmm.best
  systemd rollback --release ID --confirm api.lmm.best
  systemd --help                  All flags and granular stage/apply actions

Shared PostgreSQL maintenance (reviewed plan, normal deployment follows):
  shared-postgres validate --plan FILE --plan-sha256 SHA
  shared-postgres run --plan FILE --plan-sha256 SHA --work NEWDIR --confirm HOST --execute-migration
  shared-postgres recover --plan FILE --plan-sha256 SHA --work NEWDIR --recovery-id ID --confirm HOST

Package-owned installation:
  Use the installed /usr/bin/lmm-api-deploy production workflow.
  Standalone systemd upgrades refuse package-owned providers.

Developer/native operator commands:
  package                         Build a local package once (workspace required)
  build|frontend|production ...    Delegate to the selected provider

See docs/deployment-workflow.md, docs/manual-systemd-deployment.md and docs/seamless-upgrades.md.
USAGE
    exit 0
    ;;
esac

# Workstation commands must not resolve, build, or execute a backend provider.
if [[ ${1:-} == web ]]; then
  shift
  action=${1:-help}
  if (( $# )); then shift; fi
  case $action in
    help|-h|--help)
      cat <<'USAGE'
Usage: scripts/lmm-api-deploy.sh web {ship [TAG]|release [TAG]|deploy TAG|list|status RUN_ID|watch RUN_ID}

ship/release tag origin/main (next patch version by default) with your signing
key only after its Go/Web release checks are green, then run release-web.yml.
ship also deploys that release and waits for both workflows.

Requires an authenticated GitHub CLI (gh). Check compatibility with BOTH active
Go backends before deploying a frontend-only hotfix. Use the combined signed
transaction for changes that require a backend upgrade.

Repository: LMM_API_GITHUB_REPOSITORY (default: TokenNotIncluded/api.lmm.best).
Deploy uses the reviewed main workflow, NOT code from the release tag.
Dispatch success means requested, not deployed. Inspect the exact run with
status/watch; do not blindly redispatch after an error or a partial rollout.
USAGE
      exit 0
      ;;
    ship|release)
      cd -- "$REPO_ROOT"
      exec python3 -B "$SCRIPT_DIR/web-ship.py" "$action" "$@"
      ;;
    deploy)
      if [[ $# != 1 || ! $1 =~ ^web-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
        echo 'web deploy requires one signed release tag: web-vX.Y.Z' >&2
        exit 2
      fi
      args=(workflow run deploy-web-frontend.yml --ref main --raw-field "release_tag=$1")
      ;;
    list)
      if (( $# )); then echo 'web list takes no arguments' >&2; exit 2; fi
      args=(run list --workflow deploy-web-frontend.yml --event workflow_dispatch --limit 10)
      ;;
    status|watch)
      if [[ $# != 1 || ! $1 =~ ^[1-9][0-9]*$ ]]; then
        echo "web $action requires one numeric run ID" >&2
        exit 2
      fi
      if [[ $action == status ]]; then
        args=(run view "$1" --exit-status)
      else
        args=(run watch "$1" --exit-status --interval 10)
      fi
      ;;
    *) echo "unknown web action: $action" >&2; exit 2 ;;
  esac
  repository=${LMM_API_GITHUB_REPOSITORY:-TokenNotIncluded/api.lmm.best}
  if [[ ! $repository =~ ^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$ ]]; then
    echo 'LMM_API_GITHUB_REPOSITORY must be owner/repo' >&2
    exit 2
  fi
  # No automatic dispatch retries: a failed response can hide an accepted run.
  exec gh "${args[@]}" --repo "$repository"
fi

if [[ ${1:-} == systemd ]]; then
  shift
  exec python3 "$SCRIPT_DIR/deploy-systemd.py" "$@"
fi

if [[ ${1:-} == shared-postgres ]]; then
  shift
  exec python3 -B "$SCRIPT_DIR/deploy-shared-postgres.py" "$@"
fi

# The native build command already builds BOTH artifacts. Bootstrap only its
# CLI when missing, rather than running `just build` before rebuilding them.
package_build=false
if [[ ${1:-} == package ]]; then
  if [[ $# != 1 ]]; then echo 'package takes no arguments' >&2; exit 2; fi
  if [[ -z ${LMM_API_BUILD_WORKSPACE:-} ]]; then
    echo 'Set LMM_API_BUILD_WORKSPACE to an existing marker-owned build workspace' >&2
    exit 2
  fi
  package_build=true
  set -- build --repo "$REPO_ROOT" --workspace "$LMM_API_BUILD_WORKSPACE"
fi

# Public development entrypoint. Installed packages use the locked-down script
# in packaging/common/lmm-api instead.
provider=${LMM_API_PROVIDER_BINARY:-$REPO_ROOT/apps/api-go/out/lmm-api}
if [[ -z ${LMM_API_PROVIDER_BINARY:-} && ! -x $provider ]]; then
  provider=/usr/bin/lmm-api
fi
if [[ ! -x $provider && $package_build == true && -z ${LMM_API_PROVIDER_BINARY:-} ]]; then
  (cd -- "$REPO_ROOT" && bun run build:go)
  provider=$REPO_ROOT/apps/api-go/out/lmm-api
fi
if [[ ! -x $provider ]]; then
  printf 'Provider not executable: %s\nUse --help for deployment paths; build or select LMM_API_PROVIDER_BINARY for native commands.\n' "$provider" >&2
  exit 127
fi
exec "$provider" operator "$@"
