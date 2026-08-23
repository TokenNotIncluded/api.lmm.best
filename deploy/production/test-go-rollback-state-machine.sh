#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

here=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
: "${TMPDIR:?set TMPDIR to a marker-owned workspace}"
tmp=$(mktemp -d "$TMPDIR/lmm-go-rollback-state-test.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
bin=$tmp/bin
mkdir -p "$bin"

fail() {
  printf 'go-rollback-state-test: %s\n' "$*" >&2
  exit 1
}

cat >"$bin/hostnamectl" <<'EOF'
#!/usr/bin/env bash
printf 'arch-dmit\n'
EOF

cat >"$bin/systemctl" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
command=$1
shift
unit=''
for argument in "$@"; do
  [[ $argument == --* ]] || unit=$argument
done
state_file() {
  case $1 in
    lmm-api.service) printf '%s/new' "$LMM_TEST_SERVICE_STATE" ;;
    lmm-api-go.service) printf '%s/old' "$LMM_TEST_SERVICE_STATE" ;;
    *.timer) printf '%s/timer' "$LMM_TEST_SERVICE_STATE" ;;
    *.service) printf '%s/rollback' "$LMM_TEST_SERVICE_STATE" ;;
    *) return 1 ;;
  esac
}
case $command in
  is-active)
    file=$(state_file "$unit")
    [[ -f $file.active ]]
    ;;
  is-enabled)
    file=$(state_file "$unit")
    [[ -f $file.enabled ]]
    ;;
  enable)
    file=$(state_file "$unit")
    if [[ ${LMM_TEST_FAIL_ROLLBACK_START:-0} == 1 && -f $LMM_TEST_SERVICE_STATE/rollback.attempted ]]; then
      exit 89
    fi
    : >"$file.enabled"
    [[ " $* " != *' --now '* ]] || : >"$file.active"
    ;;
  disable)
    file=$(state_file "$unit")
    if [[ ${LMM_TEST_FAIL_ROLLBACK_TIMER_DISABLE:-0} == 1 && $unit == *.timer ]]; then
      exit 89
    fi
    rm -f -- "$file.enabled"
    [[ " $* " != *' --now '* ]] || rm -f -- "$file.active"
    ;;
  stop)
    file=$(state_file "$unit")
    rm -f -- "$file.active"
    ;;
  cat)
    [[ $unit != lmm-api.service || -f $LMM_DEPLOY_TEST_CANONICAL_SERVICE ]]
    ;;
  daemon-reload|reset-failed) ;;
  *) printf 'unexpected systemctl command: %s %s\n' "$command" "$*" >&2; exit 90 ;;
esac
EOF

cat >"$bin/systemd-run" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
unit=''
for argument in "$@"; do
  case $argument in --unit=*) unit=${argument#--unit=} ;; esac
  last=$argument
done
case $unit in
  lmm-api-go-schema-*)
    printf 'lmm_prod_contract\n' >"$last"
    chmod 0600 "$last"
    ;;
  lmm-api-go-token-*)
		printf '%s\n' "$@" >"$LMM_TEST_SERVICE_STATE/token.args"
		grep -Fq 'AND users.role >= 10' "$LMM_TEST_SERVICE_STATE/token.args" || exit 91
    printf 'sk-%032d' 0 >"$last"
    chmod 0600 "$last"
    ;;
	lmm-api-go-migrate-candidate-apply-*)
		printf '%s\n' "$@" >"$LMM_TEST_SERVICE_STATE/migrate.apply.args"
		: >"$LMM_TEST_SERVICE_STATE/migrate.apply"
		;;
	lmm-api-go-migrate-candidate-verify-*)
		printf '%s\n' "$@" >"$LMM_TEST_SERVICE_STATE/migrate.verify.args"
		[[ -f $LMM_TEST_SERVICE_STATE/migrate.apply ]] || exit 91
		: >"$LMM_TEST_SERVICE_STATE/migrate.verify"
		;;
	lmm-api-go-migrate-rollback-verify-*)
		printf '%s\n' "$@" >"$LMM_TEST_SERVICE_STATE/migrate.rollback.verify.args"
		[[ -f $LMM_TEST_SERVICE_STATE/migrate.verify ]] || exit 91
		: >"$LMM_TEST_SERVICE_STATE/migrate.rollback.verify"
		;;
  *) printf 'unexpected transient unit: %s\n' "$unit" >&2; exit 91 ;;
esac
EOF

cat >"$bin/pacman" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case $1 in
  -Qp)
    case ${2##*/} in
      candidate.pkg.tar.zst) printf '%s %s-%s\n' "$LMM_TEST_PACKAGE_NAME" "$LMM_TEST_NEW_VERSION" "$LMM_TEST_NEW_PKGREL" ;;
      rollback-core.pkg.tar.zst) printf 'lmm-api %s-1\n' "$LMM_TEST_OLD_CORE_VERSION" ;;
      rollback-go.pkg.tar.zst) printf '%s %s-%s\n' "$LMM_TEST_PACKAGE_NAME" "$LMM_TEST_OLD_VERSION" "$LMM_TEST_OLD_PKGREL" ;;
      *) exit 92 ;;
    esac
    ;;
  -Q)
    case $2 in
      lmm-api)
        [[ $LMM_TEST_PREVIOUS_LAYOUT == split ]] || exit 1
        printf 'lmm-api %s-1\n' "$LMM_TEST_OLD_CORE_VERSION"
        ;;
      lmm-api-go|lmm-api-go-bin)
        [[ $2 == "$LMM_TEST_PACKAGE_NAME" ]] || exit 1
        printf '%s %s-%s\n' "$LMM_TEST_PACKAGE_NAME" "$(<"$LMM_TEST_SERVICE_STATE/version")" \
          "$(<"$LMM_TEST_SERVICE_STATE/pkgrel")"
        ;;
      *) exit 93 ;;
    esac
    ;;
  -Qkk)
    printf 'backup file: %s: /etc/lmm-api-go/lmm-api-go.env (SHA256 checksum mismatch)\n' "$2"
    if [[ ${LMM_TEST_FAIL_ROLLBACK_QKK:-0} == 1 && -f $LMM_TEST_SERVICE_STATE/rollback.attempted ]]; then
      if [[ ${LMM_TEST_ROLLBACK_QKK_STOPS_TIMER:-0} == 1 ]]; then
        rm -f -- "$LMM_TEST_SERVICE_STATE/timer.active"
      fi
      printf '%s: 42 total files, 10 altered files\n' "$2"
    else
      printf '%s: 42 total files, 0 altered files\n' "$2"
    fi
    ;;
  -Rdd)
    shift
    [[ ${1:-} == --noconfirm ]] || exit 94
    shift
    [[ $# == 1 && $1 == lmm-api ]] || exit 94
    : >"$LMM_TEST_SERVICE_STATE/core.removed"
    if [[ -f $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env ]]; then
      mv -- "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env" \
        "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env.pacsave"
      mv -- "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/backend.conf" \
        "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/backend.conf.pacsave"
    fi
    rm -f -- "$LMM_DEPLOY_TEST_REMOVED_BINARY" "$LMM_DEPLOY_TEST_REMOVED_SELECTOR" \
      "$LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT/backends/go/lmm-api" \
      "$LMM_DEPLOY_TEST_CANONICAL_SERVICE" "$LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE"
    ;;
  -U)
    shift
    [[ ${1:-} != --noconfirm ]] || shift
    if [[ $# == 1 && ${1##*/} == candidate.pkg.tar.zst ]]; then
      [[ $LMM_TEST_PREVIOUS_LAYOUT == direct || -f $LMM_TEST_SERVICE_STATE/core.removed ]] || exit 94
      [[ -f $LMM_TEST_SERVICE_STATE/migrate.verify ]] || exit 94
      printf '%s\n' "$LMM_TEST_NEW_VERSION" >"$LMM_TEST_SERVICE_STATE/version"
      printf '%s\n' "$LMM_TEST_NEW_PKGREL" >"$LMM_TEST_SERVICE_STATE/pkgrel"
      : >"$LMM_TEST_SERVICE_STATE/new.installed"
      rm -rf -- "$LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT"
      install -d -m0755 "$LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR"
      printf 'new frontend\n' >"$LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR/index.html"
      install -Dm0755 "$LMM_TEST_PROBE_SOURCE" "$LMM_DEPLOY_TEST_PROVIDER_BINARY"
      ln -sfn -- lmm-api-go "$LMM_DEPLOY_TEST_CANONICAL_LAUNCHER"
      install -Dm0644 "$LMM_TEST_CANONICAL_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
      rm -f -- "$LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE"
      if [[ ${LMM_TEST_INJECT_UNSAFE_OLD_CONFIG:-0} == 1 ]]; then
        ln -s -- /run/unsafe "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/injected-link"
      fi
    elif [[ $# == 1 && ${1##*/} == rollback-go.pkg.tar.zst && $LMM_TEST_PREVIOUS_LAYOUT == direct ]]; then
      [[ ${LMM_TEST_FAIL_ROLLBACK_INSTALL:-0} != 1 ]] || exit 88
      : >"$LMM_TEST_SERVICE_STATE/rollback.attempted"
      printf '%s\n' "$LMM_TEST_OLD_VERSION" >"$LMM_TEST_SERVICE_STATE/version"
      printf '%s\n' "$LMM_TEST_OLD_PKGREL" >"$LMM_TEST_SERVICE_STATE/pkgrel"
      rm -f -- "$LMM_TEST_SERVICE_STATE/new.installed" "$LMM_DEPLOY_TEST_CANONICAL_LAUNCHER" \
        "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
      install -Dm0755 "$LMM_TEST_PROBE_SOURCE" "$LMM_DEPLOY_TEST_PROVIDER_BINARY"
      if [[ $LMM_TEST_PACKAGE_NAME == lmm-api-go-bin ]]; then
        ln -sfn -- lmm-api-go "$LMM_DEPLOY_TEST_CANONICAL_LAUNCHER"
        install -Dm0644 "$LMM_TEST_CANONICAL_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
      else
        install -Dm0644 "$LMM_TEST_LEGACY_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE"
      fi
    elif (($# == 2)); then
      [[ ${LMM_TEST_FAIL_ROLLBACK_INSTALL:-0} != 1 ]] || exit 88
      : >"$LMM_TEST_SERVICE_STATE/rollback.attempted"
      printf '%s\n' "$LMM_TEST_OLD_VERSION" >"$LMM_TEST_SERVICE_STATE/version"
      printf '%s\n' "$LMM_TEST_OLD_PKGREL" >"$LMM_TEST_SERVICE_STATE/pkgrel"
      rm -f -- "$LMM_TEST_SERVICE_STATE/new.installed"
      rm -f -- "$LMM_TEST_SERVICE_STATE/core.removed"
      install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_BINARY"
      install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_SELECTOR"
      install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT/backends/go/lmm-api"
      install -Dm0644 "$LMM_TEST_CANONICAL_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
      rm -f -- "$LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE"
    else
      exit 94
    fi
    ;;
  *) printf 'unexpected pacman command: %s\n' "$*" >&2; exit 95 ;;
esac
EOF

cat >"$bin/lmm-api-go" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
case ${1:-} in
  version) cat "$LMM_TEST_SERVICE_STATE/version"; exit 0 ;;
  migrate) exit 0 ;;
  request) shift ;;
  *) exit 96 ;;
esac
output=''
status_file=''
request_path='/'
while (($#)); do
  case $1 in
    --output) output=$2; shift 2 ;;
    --status-file) status_file=$2; shift 2 ;;
    --path) request_path=$2; shift 2 ;;
    --base-url|--timeout|--token-file) shift 2 ;;
    --fail) shift ;;
    *) shift ;;
  esac
done
version=$(<"$LMM_TEST_SERVICE_STATE/version")
status=200
if [[ ${LMM_TEST_FAIL_NEW:-0} == 1 && $version == "$LMM_TEST_NEW_VERSION" ]]; then
  status=503
fi
if [[ ${LMM_TEST_FAIL_ROLLBACK_PROBE:-0} == 1 && -f $LMM_TEST_SERVICE_STATE/rollback.attempted && \
      $version == "$LMM_TEST_OLD_VERSION" ]]; then
  status=503
fi
case $request_path in
  /api/status) printf '{"success":true,"ready":true,"data":{"version":"%s"}}' "$version" >"$output" ;;
  /api/livez) printf '{"success":true,"live":true}' >"$output" ;;
  /v1/models) printf '{"data":[]}' >"$output" ;;
  /) cp -- "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current/index.html" "$output" ;;
  *) exit 97 ;;
esac
printf '%s\n' "$status" >"$status_file"
chmod 0600 "$output" "$status_file"
((status < 400))
EOF

cat >"$bin/frontend-release" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
action=$1
shift
root=''
source=''
release=''
while (($#)); do
  case $1 in
    --root) root=$2; shift 2 ;;
    --source) source=$2; shift 2 ;;
    --release) release=$2; shift 2 ;;
    --keep) shift 2 ;;
    *) shift ;;
  esac
done
case $action in
  publish)
    install -d -m0755 "$root/releases/$release"
    cp -a -- "$source/." "$root/releases/$release/"
    ;;
  rollback) [[ -f $root/releases/$release/index.html ]] ;;
  *) exit 98 ;;
esac
ln -sfn -- "releases/$release" "$root/.current.new"
mv -Tf -- "$root/.current.new" "$root/current"
EOF
chmod 0755 "$bin"/*

export PATH="$bin:$PATH"
export LMM_DEPLOY_TEST_MODE=1
export LMM_DEPLOY_OBSERVED_HOST=arch-dmit
export LMM_DEPLOY_TEST_PROBE_ATTEMPTS=1
export LMM_TEST_NEW_VERSION=0.1.0.r233.gb57eb0977
export LMM_TEST_OLD_VERSION=0.1.0.r122.g27d4df76
export LMM_TEST_OLD_CORE_VERSION=0.1.0.r31.g3e39995.payrate2.cachefix1.txfix1
export LMM_TEST_PACKAGE_NAME=lmm-api-go
export LMM_TEST_NEW_PKGREL=1
export LMM_TEST_OLD_PKGREL=1

setup_case() {
  local id=$1 layout=${2:-split} package=${3:-lmm-api-go} case_root workspace
  export LMM_TEST_PREVIOUS_LAYOUT=$layout
  export LMM_TEST_PACKAGE_NAME=$package
  case_root=$tmp/$id
  export LMM_DEPLOY_TEST_WORK_ROOT=$case_root/work
  export LMM_DEPLOY_TEST_BACKUP_ROOT=$case_root/backups
  export LMM_DEPLOY_TEST_LOCK_FILE=$case_root/run/deploy.lock
  export LMM_DEPLOY_TEST_FRONTEND_ROOT=$case_root/frontend
  export LMM_DEPLOY_TEST_SYSTEMD_UNIT_ROOT=$case_root/systemd
  export LMM_DEPLOY_TEST_OLD_CONFIG_DIR=$case_root/etc/lmm-api
  export LMM_DEPLOY_TEST_NEW_CONFIG_DIR=$case_root/etc/lmm-api-go
  if [[ $layout == split || $package == lmm-api-go-bin ]]; then
    export LMM_DEPLOY_TEST_OLD_DROPIN_DIR=$case_root/etc/systemd/lmm-api.service.d
  else
    export LMM_DEPLOY_TEST_OLD_DROPIN_DIR=$case_root/etc/systemd/lmm-api-go.service.d
  fi
  export LMM_DEPLOY_TEST_NEW_DROPIN_DIR=$case_root/etc/systemd/lmm-api.service.d
  export LMM_DEPLOY_TEST_INSTALLED_BINARY=$case_root/usr/bin/lmm-api
  export LMM_DEPLOY_TEST_PROVIDER_BINARY=$case_root/usr/bin/lmm-api-go
  export LMM_DEPLOY_TEST_CANONICAL_LAUNCHER=$case_root/usr/bin/lmm-api
  export LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR=$case_root/usr/share/lmm-api-go/frontend-dist
  export LMM_DEPLOY_TEST_REMOVED_BINARY=$case_root/usr/bin/lmm-api
  export LMM_DEPLOY_TEST_REMOVED_SELECTOR=$case_root/usr/bin/lmm-api-select
  export LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT=$case_root/usr/lib/lmm-api
  export LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE=$case_root/usr/lib/systemd/system/lmm-api-go.service
  export LMM_DEPLOY_TEST_CANONICAL_SERVICE=$case_root/usr/lib/systemd/system/lmm-api.service
  export LMM_DEPLOY_TEST_TRANSACTION_LOCK=$case_root/transaction-lock
  export LMM_TEST_SERVICE_STATE=$case_root/service-state
  export LMM_TEST_PROBE_SOURCE=$bin/lmm-api-go
  export LMM_TEST_OLD_EXECUTABLE=$bin/hostnamectl
  export LMM_TEST_CANONICAL_SERVICE_SOURCE=$case_root/canonical-service.fixture
  export LMM_TEST_LEGACY_SERVICE_SOURCE=$case_root/legacy-service.fixture

  workspace=$LMM_DEPLOY_TEST_WORK_ROOT/$id
  install -d -m0700 "$workspace/staging" "$LMM_DEPLOY_TEST_BACKUP_ROOT/$id" \
    "$LMM_DEPLOY_TEST_FRONTEND_ROOT/releases/old" "$LMM_DEPLOY_TEST_SYSTEMD_UNIT_ROOT" \
    "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR" "$LMM_DEPLOY_TEST_OLD_DROPIN_DIR" \
    "$LMM_TEST_SERVICE_STATE"
  printf 'format=1\ndeployment_id=%s\n' "$id" >"$workspace/.lmm-deploy-workspace"
  printf '%s\n' "$LMM_TEST_OLD_VERSION" >"$LMM_TEST_SERVICE_STATE/version"
  printf '%s\n' "$LMM_TEST_OLD_PKGREL" >"$LMM_TEST_SERVICE_STATE/pkgrel"
  printf 'old frontend\n' >"$LMM_DEPLOY_TEST_FRONTEND_ROOT/releases/old/index.html"
  ln -s releases/old "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current"
  install -d -m0700 "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/credentials"
  printf 'credential fixture\n' >"$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/credentials/backup.identity"
  printf 'backup fixture\n' >"$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/postgresql-backup.env"
  printf 'history fixture\n' >"$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env.before-fixture"
  printf '[Service]\nExecStart=/usr/bin/lmm-api\n' >"$LMM_TEST_CANONICAL_SERVICE_SOURCE"
  printf '[Service]\nExecStart=/usr/bin/lmm-api-go serve\n' >"$LMM_TEST_LEGACY_SERVICE_SOURCE"
  if [[ $layout == split ]]; then
    : >"$LMM_TEST_SERVICE_STATE/new.active"
    : >"$LMM_TEST_SERVICE_STATE/new.enabled"
    printf 'SQL_DSN=postgres://fixture\nSESSION_SECRET=fixture\n' >"$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env"
    chmod 0600 "$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env"
    printf 'LMM_API_BACKEND=go\n' >"$LMM_DEPLOY_TEST_OLD_CONFIG_DIR/backend.conf"
    printf '[Service]\nMemoryHigh=224M\n' >"$LMM_DEPLOY_TEST_OLD_DROPIN_DIR/50-memory.conf"
    install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_BINARY"
    install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_SELECTOR"
    install -Dm0755 "$LMM_TEST_OLD_EXECUTABLE" "$LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT/backends/go/lmm-api"
    install -Dm0644 "$LMM_TEST_CANONICAL_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
    tar -C "$case_root/etc" -cf "$LMM_DEPLOY_TEST_BACKUP_ROOT/$id/configuration.archive" lmm-api
  else
    install -d -m0700 "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR"
    if [[ $package == lmm-api-go-bin ]]; then
      : >"$LMM_TEST_SERVICE_STATE/new.active"
      : >"$LMM_TEST_SERVICE_STATE/new.enabled"
    else
      : >"$LMM_TEST_SERVICE_STATE/old.active"
      : >"$LMM_TEST_SERVICE_STATE/old.enabled"
    fi
    printf 'SQL_DSN=postgres://fixture\nSESSION_SECRET=fixture\nPGOPTIONS="-c search_path=lmm_prod_contract"\n' \
      >"$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env"
    chmod 0600 "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env"
    printf '[Service]\nMemoryHigh=224M\n' >"$LMM_DEPLOY_TEST_OLD_DROPIN_DIR/50-memory.conf"
    install -Dm0755 "$LMM_TEST_PROBE_SOURCE" "$LMM_DEPLOY_TEST_PROVIDER_BINARY"
    if [[ $package == lmm-api-go-bin ]]; then
      ln -sfn -- lmm-api-go "$LMM_DEPLOY_TEST_CANONICAL_LAUNCHER"
      install -Dm0644 "$LMM_TEST_CANONICAL_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_CANONICAL_SERVICE"
    else
      install -Dm0644 "$LMM_TEST_LEGACY_SERVICE_SOURCE" "$LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE"
    fi
    install -d -m0755 "$LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR"
    printf 'old packaged frontend\n' >"$LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR/index.html"
    tar -C "$case_root/etc" -cf "$LMM_DEPLOY_TEST_BACKUP_ROOT/$id/configuration.archive" lmm-api-go
  fi
  for file in candidate.pkg.tar.zst rollback-core.pkg.tar.zst rollback-go.pkg.tar.zst; do
    printf '%s\n' "$file" >"$workspace/staging/$file"
  done
  [[ $layout == split ]] || printf 'direct\n' >"$workspace/staging/rollback-core.pkg.tar.zst"
  install -Dm0755 "$bin/lmm-api-go" "$workspace/staging/lmm-api-go"
  install -Dm0755 "$bin/frontend-release" "$workspace/staging/frontend-release.sh"
  install -Dm0755 "$here/activate-go-release.sh" "$workspace/staging/activate-go-release.sh"
  CASE_WORKSPACE=$workspace
}

activate_case() {
  local workspace=$1 id candidate
  id=${workspace##*/}
  candidate=$workspace/staging/candidate.pkg.tar.zst
  local core=$workspace/staging/rollback-core.pkg.tar.zst go=$workspace/staging/rollback-go.pkg.tar.zst
  local probe=$workspace/staging/lmm-api-go frontend_sha
  local -a layout_args=()
  [[ $LMM_TEST_PREVIOUS_LAYOUT == split ]] || layout_args=(--rollback-layout direct)
  if [[ $LMM_TEST_PACKAGE_NAME == lmm-api-go-bin ]]; then
    frontend_sha=$(sha256sum "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current/index.html" | awk '{print $1}')
  else
    frontend_sha=$(printf 'new frontend\n' | sha256sum | awk '{print $1}')
  fi
  "$workspace/staging/activate-go-release.sh" activate \
    --workspace "$workspace" \
    --package "$candidate" --package-sha256 "$(sha256sum "$candidate" | awk '{print $1}')" \
    --rollback-core-package "$core" --rollback-core-sha256 "$(sha256sum "$core" | awk '{print $1}')" \
    --rollback-go-package "$go" --rollback-go-sha256 "$(sha256sum "$go" | awk '{print $1}')" \
    --probe-binary "$probe" --probe-binary-sha256 "$(sha256sum "$probe" | awk '{print $1}')" \
    --expected-version "$LMM_TEST_NEW_VERSION" --old-version "$LMM_TEST_OLD_VERSION" \
    --frontend-index-sha256 "$frontend_sha" \
    --frontend-release-script "$workspace/staging/frontend-release.sh" \
    --backup-dir "$LMM_DEPLOY_TEST_BACKUP_ROOT/$id" --rollback-seconds 600 \
    "${layout_args[@]}"
}

CASE_WORKSPACE=''
setup_case insecure-work-root
insecure_workspace=$CASE_WORKSPACE
chmod 0777 "$LMM_DEPLOY_TEST_WORK_ROOT"
if activate_case "$insecure_workspace" >"$tmp/activate-insecure.out" 2>"$tmp/activate-insecure.err"; then
  fail 'activation accepted a service-writable deployment root'
fi
grep -Fq 'deployment path component is not root-controlled' "$tmp/activate-insecure.err" ||
  fail 'activation did not identify the unsafe deployment root'

setup_case confirm-case
confirm_workspace=$CASE_WORKSPACE
activate_case "$confirm_workspace" >"$tmp/activate-confirm.out"
grep -Fq 'AWAITING_CONFIRMATION' "$confirm_workspace/state/status" || fail 'activation did not await confirmation'
[[ -f $LMM_TEST_SERVICE_STATE/core.removed ]] || fail 'activation did not explicitly remove the old core package'
[[ -f $LMM_TEST_SERVICE_STATE/migrate.apply && -f $LMM_TEST_SERVICE_STATE/migrate.verify &&
  -f $LMM_TEST_SERVICE_STATE/migrate.rollback.verify ]] ||
  fail 'activation did not prove candidate migration and rollback compatibility before package replacement'
grep -Fqx -- '--setenv=PGOPTIONS=-c search_path=lmm_prod_contract' \
  "$LMM_TEST_SERVICE_STATE/migrate.apply.args" || fail 'migration did not use the captured production schema'
grep -Fqx -- "--property=WorkingDirectory=$confirm_workspace/tmp/migrations/candidate-apply" \
  "$LMM_TEST_SERVICE_STATE/migrate.apply.args" || fail 'migration did not use its release-scoped disposable directory'
grep -Fqx -- "--property=WorkingDirectory=$confirm_workspace/tmp/migrations/rollback-verify" \
  "$LMM_TEST_SERVICE_STATE/migrate.rollback.verify.args" || fail 'rollback verification did not use an isolated directory'
grep -Fqx 'database_schema=lmm_prod_contract' "$confirm_workspace/state/deployment.env" ||
  fail 'deployment manifest did not freeze the production schema'
grep -Fqx 'PGOPTIONS="-c search_path=lmm_prod_contract"' \
  "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" || fail 'new service config did not preserve the production schema'
grep -Fqx 'SESSION_COOKIE_SECURE=true' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'new service config did not require secure refresh cookies'
grep -Fqx 'SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'new service config did not pin the trusted public origin'
grep -Fqx 'TRUSTED_PROXIES=127.0.0.1/32,::1/128' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'new service config did not restrict trusted proxies to the local reverse proxy'
[[ $(readlink "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current") == "releases/$LMM_TEST_NEW_VERSION" ]] || fail 'new frontend was not published'
[[ -f $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'rollback timer was not armed'
[[ -f $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/credentials/backup.identity ]] || fail 'auxiliary credentials were removed'
[[ -f $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/postgresql-backup.env ]] || fail 'auxiliary backup config was removed'
[[ -f $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env.before-fixture ]] || fail 'operator config history was removed'
[[ ! -e $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env &&
  ! -e $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/lmm-api.env.pacsave &&
  ! -e $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/backend.conf &&
  ! -e $LMM_DEPLOY_TEST_OLD_CONFIG_DIR/backend.conf.pacsave ]] ||
  fail 'old application configuration was retained after cutover'
"$confirm_workspace/staging/activate-go-release.sh" confirm --workspace "$confirm_workspace" >"$tmp/confirm.out"
grep -Fq 'CONFIRMED' "$confirm_workspace/state/status" || fail 'confirmation state was not recorded'
[[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'confirmation did not stop the timer'
[[ ! -e $confirm_workspace/state/probe-token ]] || fail 'confirmation retained the probe token'

setup_case rollback-case
rollback_workspace=$CASE_WORKSPACE
export LMM_TEST_FAIL_NEW=1
if activate_case "$rollback_workspace" >"$tmp/activate-rollback.out" 2>"$tmp/activate-rollback.err"; then
  fail 'injected post-install probe failure unexpectedly succeeded'
fi
unset LMM_TEST_FAIL_NEW
grep -Fq 'ROLLED_BACK' "$rollback_workspace/state/status" || fail 'probe failure did not roll back'
[[ $(readlink "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current") == releases/old ]] || fail 'rollback did not restore the frontend'
[[ -x $LMM_DEPLOY_TEST_REMOVED_BINARY && -f $LMM_DEPLOY_TEST_CANONICAL_SERVICE ]] || fail 'rollback did not restore old package paths'
[[ -f $LMM_TEST_SERVICE_STATE/new.active && ! -e $LMM_TEST_SERVICE_STATE/old.active ]] || fail 'rollback did not restore the old service state'
[[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'rollback left its timer active'

setup_case explicit-die-case
explicit_die_workspace=$CASE_WORKSPACE
export LMM_TEST_INJECT_UNSAFE_OLD_CONFIG=1
if activate_case "$explicit_die_workspace" >"$tmp/activate-explicit-die.out" 2>"$tmp/activate-explicit-die.err"; then
  fail 'injected explicit validation failure unexpectedly succeeded'
fi
unset LMM_TEST_INJECT_UNSAFE_OLD_CONFIG
grep -Fq 'ROLLED_BACK' "$explicit_die_workspace/state/status" || fail 'explicit validation failure did not roll back'
[[ -f $LMM_TEST_SERVICE_STATE/new.active && ! -e $LMM_TEST_SERVICE_STATE/old.active ]] ||
  fail 'explicit validation failure did not restore the old service'
[[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'explicit validation rollback left its timer active'

setup_case direct-confirm-case direct
direct_confirm_workspace=$CASE_WORKSPACE
activate_case "$direct_confirm_workspace" >"$tmp/activate-direct-confirm.out"
grep -Fq 'AWAITING_CONFIRMATION' "$direct_confirm_workspace/state/status" ||
  fail 'direct Go upgrade did not await confirmation'
[[ ! -e $LMM_TEST_SERVICE_STATE/core.removed ]] || fail 'direct Go upgrade removed a nonexistent core package'
[[ -f $LMM_TEST_SERVICE_STATE/new.active && ! -e $LMM_TEST_SERVICE_STATE/old.active ]] ||
  fail 'direct Go upgrade did not preserve the Go service architecture'
grep -Fqx 'SESSION_COOKIE_SECURE=true' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'direct Go upgrade did not require secure refresh cookies'
grep -Fqx 'SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'direct Go upgrade did not pin the trusted public origin'
grep -Fqx 'TRUSTED_PROXIES=127.0.0.1/32,::1/128' "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" ||
  fail 'direct Go upgrade did not restrict trusted proxies to the local reverse proxy'
grep -Fqx -- "--property=EnvironmentFile=$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env" \
  "$LMM_TEST_SERVICE_STATE/migrate.apply.args" || fail 'direct migration did not use the active Go environment'
[[ -f $LMM_DEPLOY_TEST_NEW_DROPIN_DIR/50-memory.conf ]] || fail 'direct upgrade removed the Go service drop-in'
"$direct_confirm_workspace/staging/activate-go-release.sh" confirm \
  --workspace "$direct_confirm_workspace" >"$tmp/confirm-direct.out"
grep -Fq 'CONFIRMED' "$direct_confirm_workspace/state/status" || fail 'direct upgrade was not confirmed'
[[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'direct confirmation did not stop the timer'

setup_case direct-rollback-case direct
direct_rollback_workspace=$CASE_WORKSPACE
export LMM_TEST_FAIL_NEW=1
if activate_case "$direct_rollback_workspace" >"$tmp/activate-direct-rollback.out" 2>"$tmp/activate-direct-rollback.err"; then
  fail 'injected direct-upgrade probe failure unexpectedly succeeded'
fi
unset LMM_TEST_FAIL_NEW
grep -Fq 'ROLLED_BACK' "$direct_rollback_workspace/state/status" || fail 'direct probe failure did not roll back'
[[ $(readlink "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current") == releases/old ]] ||
  fail 'direct rollback did not restore the frontend'
[[ -f $LMM_TEST_SERVICE_STATE/old.active && ! -e $LMM_TEST_SERVICE_STATE/new.active ]] ||
  fail 'direct rollback did not restore the Go service'
[[ $(<"$LMM_TEST_SERVICE_STATE/version") == "$LMM_TEST_OLD_VERSION" ]] ||
  fail 'direct rollback did not reinstall the old Go package'
if grep -Eq '^(SESSION_COOKIE_SECURE|SESSION_COOKIE_TRUSTED_URL|TRUSTED_PROXIES)=' \
  "$LMM_DEPLOY_TEST_NEW_CONFIG_DIR/lmm-api-go.env"; then
  fail 'direct rollback did not restore the original Go environment'
fi
[[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail 'direct rollback left its timer active'

setup_case unsafe-migration-directory-case direct
unsafe_migration_workspace=$CASE_WORKSPACE
unsafe_target=$tmp/unsafe-migration-target
mkdir -m0700 "$unsafe_target"
ln -s -- "$unsafe_target" "$unsafe_migration_workspace/tmp"
if activate_case "$unsafe_migration_workspace" >"$tmp/activate-unsafe-migration.out" 2>"$tmp/activate-unsafe-migration.err"; then
  fail 'symlinked migration directory unexpectedly succeeded'
fi
grep -Fq 'ROLLED_BACK' "$unsafe_migration_workspace/state/status" ||
  fail 'unsafe migration directory did not enter the guarded rollback path'
[[ -z $(find "$unsafe_target" -mindepth 1 -print -quit) ]] ||
  fail 'unsafe migration directory wrote outside the deployment workspace'

export LMM_TEST_NEW_PKGREL=2
setup_case aur-direct-confirm-case direct lmm-api-go-bin
aur_direct_confirm_workspace=$CASE_WORKSPACE
activate_case "$aur_direct_confirm_workspace" >"$tmp/activate-aur-direct-confirm.out"
grep -Fq 'AWAITING_CONFIRMATION' "$aur_direct_confirm_workspace/state/status" ||
  fail 'AUR Go upgrade did not await confirmation'
[[ -f $LMM_TEST_SERVICE_STATE/new.active && ! -e $LMM_TEST_SERVICE_STATE/old.active ]] ||
  fail 'AUR Go upgrade switched away from lmm-api.service'
[[ $(readlink "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current") == releases/old ]] ||
  fail 'backend-only AUR upgrade changed the independent frontend'
[[ -f $LMM_DEPLOY_TEST_NEW_DROPIN_DIR/50-memory.conf ]] ||
  fail 'AUR Go upgrade replaced the existing lmm-api.service drop-ins'
"$aur_direct_confirm_workspace/staging/activate-go-release.sh" confirm \
  --workspace "$aur_direct_confirm_workspace" >"$tmp/confirm-aur-direct.out"
grep -Fq 'CONFIRMED' "$aur_direct_confirm_workspace/state/status" || fail 'AUR Go upgrade was not confirmed'

setup_case aur-direct-rollback-case direct lmm-api-go-bin
aur_direct_rollback_workspace=$CASE_WORKSPACE
export LMM_TEST_FAIL_NEW=1
if activate_case "$aur_direct_rollback_workspace" >"$tmp/activate-aur-direct-rollback.out" 2>"$tmp/activate-aur-direct-rollback.err"; then
  fail 'injected AUR Go probe failure unexpectedly succeeded'
fi
unset LMM_TEST_FAIL_NEW
grep -Fq 'ROLLED_BACK' "$aur_direct_rollback_workspace/state/status" || fail 'AUR Go probe failure did not roll back'
[[ -f $LMM_TEST_SERVICE_STATE/new.active && ! -e $LMM_TEST_SERVICE_STATE/old.active ]] ||
  fail 'AUR Go rollback did not restore lmm-api.service'
[[ $(<"$LMM_TEST_SERVICE_STATE/version") == "$LMM_TEST_OLD_VERSION" ]] ||
  fail 'AUR Go rollback did not reinstall the previous lmm-api-go-bin package'
[[ $(readlink "$LMM_DEPLOY_TEST_FRONTEND_ROOT/current") == releases/old ]] ||
  fail 'AUR Go rollback changed the independent frontend'
[[ -f $LMM_DEPLOY_TEST_NEW_DROPIN_DIR/50-memory.conf ]] ||
  fail 'AUR Go rollback removed the existing lmm-api.service drop-ins'
export LMM_TEST_NEW_PKGREL=1

setup_locked_case() {
  local id=$1
  setup_case "$id" direct lmm-api-go-bin
  install -d -m0700 "$LMM_DEPLOY_TEST_TRANSACTION_LOCK"
  printf 'deployment_id=%s\n' "$id" >"$LMM_DEPLOY_TEST_TRANSACTION_LOCK/deployment.env"
}

inject_failed_activation() {
  local id=$1 injection=$2 workspace
  setup_locked_case "$id"
  workspace=$CASE_WORKSPACE
  export LMM_TEST_FAIL_NEW=1
  export "$injection=1"
  if activate_case "$workspace" >"$tmp/activate-$id.out" 2>"$tmp/activate-$id.err"; then
    fail "$id unexpectedly succeeded"
  fi
  unset LMM_TEST_FAIL_NEW "$injection"
}

assert_failed_rollback() {
  local id=$1 injection=$2 step=$3 timer=${4:-active} workspace
  inject_failed_activation "$id" "$injection"
  workspace=$CASE_WORKSPACE
  grep -Fq 'ROLLBACK_FAILED' "$workspace/state/status" || fail "$id was not marked ROLLBACK_FAILED"
  grep -Fq "step=$step" "$workspace/state/status" || fail "$id did not record failing step $step"
  [[ -f $LMM_DEPLOY_TEST_TRANSACTION_LOCK/deployment.env ]] || fail "$id released the transaction lock"
  if [[ $timer == active ]]; then
    [[ -f $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id disarmed the rollback watchdog"
  else
    [[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id retained an unexpectedly active watchdog"
  fi
}

assert_retryable_finalization() {
  local id=$1 injection=$2 workspace
  inject_failed_activation "$id" "$injection"
  workspace=$CASE_WORKSPACE
  grep -Fq 'ROLLED_BACK' "$workspace/state/status" || fail "$id did not persist the terminal rollback"
  [[ -f $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id left no retry path for finalization"
  "$workspace/staging/activate-go-release.sh" rollback --workspace "$workspace"
  grep -Fq 'ROLLED_BACK' "$workspace/state/status" || fail "$id lost its terminal rollback state"
  [[ ! -e $LMM_DEPLOY_TEST_TRANSACTION_LOCK ]] || fail "$id retry did not release the transaction lock"
  [[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id retry did not disarm the watchdog"
}

assert_retryable_confirmation() {
  local id=$1 injection=$2 workspace
  setup_locked_case "$id"
  workspace=$CASE_WORKSPACE
  activate_case "$workspace" >"$tmp/activate-$id.out"
  export "$injection=1"
  if "$workspace/staging/activate-go-release.sh" confirm --workspace "$workspace" \
    >"$tmp/confirm-$id.out" 2>"$tmp/confirm-$id.err"; then
    fail "$id unexpectedly finalized"
  fi
  unset "$injection"
  grep -Fq 'CONFIRMED' "$workspace/state/status" || fail "$id did not persist confirmation"
  [[ -f $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id left no retry path for confirmation"
  "$workspace/staging/activate-go-release.sh" rollback --workspace "$workspace"
  [[ ! -e $LMM_DEPLOY_TEST_TRANSACTION_LOCK ]] || fail "$id retry did not release the transaction lock"
  [[ ! -e $LMM_TEST_SERVICE_STATE/timer.active ]] || fail "$id retry did not disarm the watchdog"
}

assert_failed_rollback rollback-install-failure LMM_TEST_FAIL_ROLLBACK_INSTALL package-install
assert_failed_rollback rollback-integrity-failure LMM_TEST_FAIL_ROLLBACK_QKK package-integrity
assert_failed_rollback rollback-start-failure LMM_TEST_FAIL_ROLLBACK_START service-start
assert_failed_rollback rollback-probe-failure LMM_TEST_FAIL_ROLLBACK_PROBE release-probe
assert_failed_rollback rollback-probe-cleanup-failure LMM_TEST_FAIL_ROLLBACK_PROBE_CLEANUP probe-cleanup
assert_failed_rollback rollback-terminal-status-failure LMM_TEST_FAIL_ROLLED_BACK_STATUS terminal-status
export LMM_TEST_ROLLBACK_QKK_STOPS_TIMER=1
assert_failed_rollback rollback-integrity-stopped-timer LMM_TEST_FAIL_ROLLBACK_QKK package-integrity inactive
unset LMM_TEST_ROLLBACK_QKK_STOPS_TIMER

assert_retryable_finalization rollback-unlock-finalization LMM_TEST_FAIL_ROLLBACK_UNLOCK
assert_retryable_finalization rollback-timer-finalization LMM_TEST_FAIL_ROLLBACK_TIMER_DISABLE
assert_retryable_confirmation confirm-unlock-finalization LMM_TEST_FAIL_ROLLBACK_UNLOCK
assert_retryable_confirmation confirm-timer-finalization LMM_TEST_FAIL_ROLLBACK_TIMER_DISABLE

printf 'Go rollback and confirmation state machine verified\n'
