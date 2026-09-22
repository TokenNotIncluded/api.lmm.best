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

Existing standalone systemd installation (run on the target as root):
  systemd doctor [--migrate]       Check prerequisites without changing the host
  systemd upgrade --release ID --binary FILE --frontend DIR --confirm api.lmm.best
                                  Stage and apply once; confirmation stays explicit
  systemd status [--release ID]    Inspect transactions without creating state
  systemd confirm --release ID --confirm api.lmm.best
  systemd rollback --release ID --confirm api.lmm.best
  systemd --help                  All flags and granular stage/apply actions

Package-owned installation:
  Use the installed /usr/bin/lmm-api-deploy production workflow.
  Standalone systemd upgrades refuse package-owned providers.

Developer/native operator commands:
  build|frontend|production ...    Delegate to the selected provider

See docs/manual-systemd-deployment.md and docs/seamless-upgrades.md.
USAGE
    exit 0
    ;;
esac

if [[ ${1:-} == systemd ]]; then
  shift
  exec python3 "$SCRIPT_DIR/deploy-systemd.py" "$@"
fi

# Public development entrypoint. Installed packages use the locked-down script
# in packaging/common/lmm-api instead.
provider=${LMM_API_PROVIDER_BINARY:-$REPO_ROOT/apps/api-go/out/lmm-api}
if [[ -z ${LMM_API_PROVIDER_BINARY:-} && ! -x $provider ]]; then
  provider=/usr/bin/lmm-api
fi
if [[ ! -x $provider ]]; then
  printf 'Provider not executable: %s\nUse --help for deployment paths; build or select LMM_API_PROVIDER_BINARY for native commands.\n' "$provider" >&2
  exit 127
fi
exec "$provider" operator "$@"
