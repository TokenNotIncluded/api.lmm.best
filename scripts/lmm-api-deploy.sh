#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
readonly SCRIPT_DIR
REPO_ROOT=$(cd -- "$SCRIPT_DIR/.." && pwd -P)
readonly REPO_ROOT

# Public development entrypoint. Installed packages use the locked-down script
# in packaging/common/lmm-api instead.
provider=${LMM_API_PROVIDER_BINARY:-$REPO_ROOT/apps/api-go/out/lmm-api}
if [[ $# -eq 0 ]]; then
  printf 'Usage: %s build|frontend|production ...\n' "${0##*/}" >&2
  exit 64
fi
exec "$provider" operator "$@"
