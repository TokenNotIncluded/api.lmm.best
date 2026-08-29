#!/usr/bin/env bash
# shellcheck disable=SC2016 # Literal source snippets are intentional contract assertions.
set -Eeuo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
here=$repo/deploy/production
: "${TMPDIR:?set TMPDIR to a marker-owned workspace}"

fail() { printf 'go-deploy-contract: %s\n' "$*" >&2; exit 1; }
contains() { grep -Fq -- "$1" "$2" || fail "$2 is missing: $1"; }

scripts=(
  "$here/activate-go-release.sh"
  "$here/build-go-binary.sh"
  "$here/build-go-package.sh"
  "$here/build-precutover-packages.sh"
  "$here/capture-precutover-payload.sh"
  "$here/prepare-production-backup.sh"
  "$here/create-backup-copy.sh"
  "$here/promote-production-backups.sh"
)
for script in "${scripts[@]}"; do
  [[ -x $script ]] || fail "production script is not executable: $script"
  bash -n "$script"
done
if command -v shellcheck >/dev/null 2>&1; then
  shellcheck "${scripts[@]}"
fi

for literal in \
  'ExecStart=/usr/bin/lmm-api serve' \
  'Environment=LMM_API_FRONTEND_DIR=/srv/lmm-api-frontend/current' \
  'Environment=LMM_DB_MIGRATION_MODE=verify'; do
  contains "$literal" "$repo/packaging/common/lmm-api/lmm-api.service"
done
contains 'readonly NEW_SERVICE=lmm-api.service' "$here/activate-go-release.sh"
contains 'readonly LEGACY_SERVICE=lmm-api-go.service' "$here/activate-go-release.sh"
contains 'readlink -- "$CANONICAL_LAUNCHER"' "$here/activate-go-release.sh"
for literal in \
  'format=2\ndeployment_id=%s' \
  'write_status "MUTATION_PENDING' \
  'write_status "OBSERVING' \
  'write_status "ROLLBACK_REQUIRED' \
	'write_status "AWAITING_CONFIRMATION' \
	'write_status "CONFIRMED' \
	'write_status "MIGRATING' \
  'probe_authenticated_models' \
	'discover_database_schema' \
	'database_schema=%s' \
	'"$PROBE_BINARY" request' \
	'run_migration apply candidate-apply "$PROBE_BINARY"' \
	'run_migration verify candidate-verify "$PROBE_BINARY"' \
	'run_migration verify rollback-verify "$INSTALLED_BINARY"' \
  'harden_production_environment_config' \
  'SESSION_COOKIE_SECURE=true' \
  'SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best' \
  'TRUSTED_PROXIES=127.0.0.1/32,::1/128' \
  'rollback_layout=%s' \
  'direct Go upgrade unexpectedly found the split core package' \
  'pacman -Rdd --noconfirm lmm-api' \
  'release_transaction_lock'; do
  contains "$literal" "$here/activate-go-release.sh"
done
if grep -Eq 'rollback-seconds|rollback[_-](timer|deadline|unit)|Persistent=true|automatic rollback' "$here/activate-go-release.sh"; then
  fail 'activation script still creates or exposes an automatic rollback timer'
fi
contains 'WORK_ROOT=/var/lib/lmm-api-go-deploy/work' "$here/activate-go-release.sh"
contains 'assert_root_only_path "$WORKSPACE"' "$here/activate-go-release.sh"
if rg -n '/var/lib/lmm-api-go/(deploy-work|deploy-backups|deploy-transaction)' "$here"; then
  fail 'deployment control files remain below the backend-writable StateDirectory'
fi
if grep -Fq 'search_path=public' "$here/activate-go-release.sh" \
  "$repo/packaging/common/lmm-api/lmm-api.service"; then
  fail 'production activation or service unit still hard-codes the public schema'
fi
contains 'PGOPTIONS="-c search_path=public"' "$repo/packaging/common/lmm-api/lmm-api-go.env"
contains 'direct:lmm-api-go:/usr/bin/lmm-api-go' "$here/capture-precutover-payload.sh"
contains '--rollback-layout "$ROLLBACK_LAYOUT"' "$here/promote-production-backups.sh"
[[ -f $here/precutover-lmm-api-go-direct.PKGBUILD && ! -L $here/precutover-lmm-api-go-direct.PKGBUILD ]] || \
  fail 'direct Go rollback package template is missing or unsafe'
for literal in \
  'chmod 0700 "$capture_root/core-root/etc/lmm-api"' \
  'chmod 0600 "$capture_root/core-root/etc/lmm-api/lmm-api.env"' \
  'chmod 0644 "$capture_root/core-root/etc/lmm-api/backend.conf"' \
  'chmod 0755 "$capture_root/core-root/usr/bin/lmm-api"'; do
  contains "$literal" "$here/capture-precutover-payload.sh"
done
if grep -R -nE '(^|[^[:alnum:]_])(curl|wget)([^[:alnum:]_]|$)|SIGKILL|mktemp[^\n]*(/tmp|TMPDIR:-/tmp)' \
  "$here/activate-go-release.sh" "$here/build-go-package.sh" \
  "$here/capture-precutover-payload.sh" "$here/prepare-production-backup.sh"; then
  fail 'Go production path retains a browser-style client, SIGKILL fallback, or /tmp artifact path'
fi
if grep -R --exclude='test-cli-contract.sh' -nE \
  'lmm-api-launcher|backend\.conf.*selector|/usr/lib/lmm-api/backends/go/lmm-api.*ExecStart' \
  "$repo/packaging/common/lmm-api" "$repo/packaging/local/lmm-api-go" "$here/precutover-lmm-api-go-direct.PKGBUILD" \
  "$repo"/packaging/aur/*/PKGBUILD "$repo"/packaging/aur/*/.SRCINFO; then
  fail 'new package path retains launcher/provider architecture'
fi

fixture=$(mktemp -d "$TMPDIR/lmm-go-deploy-env-test.XXXXXXXX")
trap 'rm -rf -- "$fixture"' EXIT
printf 'SQL_DSN=postgresql://fixture.invalid/lmm\n' >"$fixture/safe.env"
LMM_DEPLOY_TEST_MODE=1 LMM_DEPLOY_OBSERVED_HOST=arch-dmit \
  "$here/prepare-production-backup.sh" --check-env-only --env-file "$fixture/safe.env" \
  | grep -Fqx 'database_engine=postgres'
side_effect=$fixture/executed
printf 'SQL_DSN=$(touch %s)\n' "$side_effect" >"$fixture/malicious.env"
if LMM_DEPLOY_TEST_MODE=1 LMM_DEPLOY_OBSERVED_HOST=arch-dmit \
  "$here/prepare-production-backup.sh" --check-env-only --env-file "$fixture/malicious.env" \
  >"$fixture/out" 2>"$fixture/err"; then
  fail 'malicious environment assignment was accepted'
fi
[[ ! -e $side_effect ]] || fail 'malicious environment assignment executed'

TMPDIR=$TMPDIR "$here/test-go-rollback-state-machine.sh"
TMPDIR=$TMPDIR "$here/test-precutover-capture.sh"
if command -v makepkg >/dev/null 2>&1 && command -v pacman >/dev/null 2>&1; then
  TMPDIR=$TMPDIR "$here/test-precutover-packages.sh"
else
  printf 'pre-cutover package reconstruction skipped: Arch makepkg/pacman unavailable\n'
fi
"$here/test-backup-promotion-contract.sh"
"$repo/deploy/test-frontend-release.sh"

printf 'canonical lmm-api production support contract verified\n'
