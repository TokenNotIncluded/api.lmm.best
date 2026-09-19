#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "$(readlink -f -- "${BASH_SOURCE[0]}")")" && pwd -P)
readonly SCRIPT_DIR
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd -P)
readonly REPO_ROOT

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
if [[ $# -eq 0 ]]; then
  printf 'Usage: %s systemd|build|frontend|production ...\n' "${0##*/}" >&2
  exit 64
fi
exec "$provider" operator "$@"
