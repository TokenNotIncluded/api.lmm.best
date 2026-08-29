#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

readonly EXPECTED_HOST=arch-dmit
readonly NEW_SERVICE=lmm-api.service
readonly LEGACY_SERVICE=lmm-api-go.service
readonly SOURCE_PACKAGE=lmm-api-go
readonly AUR_PACKAGE=lmm-api-go-bin
if [[ ${LMM_DEPLOY_TEST_MODE:-0} == 1 ]]; then
  WORK_ROOT=${LMM_DEPLOY_TEST_WORK_ROOT:?}
  BACKUP_ROOT=${LMM_DEPLOY_TEST_BACKUP_ROOT:?}
  LOCK_FILE=${LMM_DEPLOY_TEST_LOCK_FILE:?}
  FRONTEND_ROOT=${LMM_DEPLOY_TEST_FRONTEND_ROOT:?}
  SYSTEMD_UNIT_ROOT=${LMM_DEPLOY_TEST_SYSTEMD_UNIT_ROOT:?}
  OLD_CONFIG_DIR=${LMM_DEPLOY_TEST_OLD_CONFIG_DIR:?}
  NEW_CONFIG_DIR=${LMM_DEPLOY_TEST_NEW_CONFIG_DIR:?}
  OLD_DROPIN_DIR=${LMM_DEPLOY_TEST_OLD_DROPIN_DIR:?}
  NEW_DROPIN_DIR=${LMM_DEPLOY_TEST_NEW_DROPIN_DIR:?}
  INSTALLED_BINARY=${LMM_DEPLOY_TEST_INSTALLED_BINARY:?}
  PACKAGED_FRONTEND_DIR=${LMM_DEPLOY_TEST_PACKAGED_FRONTEND_DIR:?}
  REMOVED_SELECTOR=${LMM_DEPLOY_TEST_REMOVED_SELECTOR:?}
  REMOVED_PROVIDER_ROOT=${LMM_DEPLOY_TEST_REMOVED_PROVIDER_ROOT:?}
  REMOVED_LEGACY_SERVICE=${LMM_DEPLOY_TEST_REMOVED_LEGACY_SERVICE:?}
  CANONICAL_LAUNCHER=${LMM_DEPLOY_TEST_CANONICAL_LAUNCHER:?}
  TRANSACTION_LOCK=${LMM_DEPLOY_TEST_TRANSACTION_LOCK:?}
  PROBE_ATTEMPTS=${LMM_DEPLOY_TEST_PROBE_ATTEMPTS:-1}
else
  WORK_ROOT=/var/lib/lmm-api-go-deploy/work
  BACKUP_ROOT=/var/lib/lmm-api-go-deploy/backups
  LOCK_FILE=/run/lock/lmm-api-go-deploy.lock
  FRONTEND_ROOT=/srv/lmm-api-frontend
  SYSTEMD_UNIT_ROOT=/etc/systemd/system
  OLD_CONFIG_DIR=/etc/lmm-api
  NEW_CONFIG_DIR=/etc/lmm-api-go
  OLD_DROPIN_DIR=/etc/systemd/system/lmm-api-go.service.d
  NEW_DROPIN_DIR=/etc/systemd/system/lmm-api.service.d
  INSTALLED_BINARY=/usr/bin/lmm-api
  PACKAGED_FRONTEND_DIR=/usr/share/lmm-api-go/frontend-dist
  REMOVED_SELECTOR=/usr/bin/lmm-api-select
  REMOVED_PROVIDER_ROOT=/usr/lib/lmm-api
  REMOVED_LEGACY_SERVICE=/usr/lib/systemd/system/lmm-api-go.service
  CANONICAL_LAUNCHER=/usr/bin/lmm-api
  TRANSACTION_LOCK=/var/lib/lmm-api-go-deploy/transaction.lock
  PROBE_ATTEMPTS=45
fi
readonly WORK_ROOT BACKUP_ROOT LOCK_FILE FRONTEND_ROOT SYSTEMD_UNIT_ROOT
readonly OLD_CONFIG_DIR NEW_CONFIG_DIR OLD_DROPIN_DIR NEW_DROPIN_DIR
readonly INSTALLED_BINARY PACKAGED_FRONTEND_DIR
readonly REMOVED_SELECTOR REMOVED_PROVIDER_ROOT REMOVED_LEGACY_SERVICE CANONICAL_LAUNCHER PROBE_ATTEMPTS
readonly TRANSACTION_LOCK

die() { printf 'activate-go-release: %s\n' "$*" >&2; return 2; }
is_sha256() { [[ $1 =~ ^[0-9a-f]{64}$ ]]; }
is_id() { [[ $1 =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,79}$ ]]; }

# Deployment control paths must not be writable by the service account.  The
# check walks every existing component so a symlink or writable parent cannot
# redirect workspace/status/rollback state outside the operator-owned tree.
assert_root_only_path() {
  local path=$1 component=/ mode owner name required_owner=0
  local -a components
  if [[ ${LMM_DEPLOY_TEST_MODE:-0} == 1 ]]; then
    required_owner=$EUID
  fi
  [[ $path == /* && $(realpath -e -- "$path") == "$path" ]] || die 'deployment path is not canonical'
  IFS=/ read -ra components <<<"${path#/}"
  for name in "${components[@]}"; do
    component=${component%/}/$name
    [[ ! -L $component ]] || die "deployment path contains a symlink: $component"
    read -r owner mode < <(stat -c '%u %a' -- "$component")
    [[ ( $owner == 0 || $owner == "$required_owner" ) && $mode =~ ^[0-7]{3,4}$ && $((8#$mode & 8#022)) == 0 ]] || \
      die "deployment path component is not root-controlled: $component"
  done
}

ACTION=${1:-}
[[ -n $ACTION ]] && shift
WORKSPACE=''
PACKAGE=''
PACKAGE_SHA256=''
ROLLBACK_CORE=''
ROLLBACK_CORE_SHA256=''
ROLLBACK_GO=''
ROLLBACK_GO_SHA256=''
PROBE_BINARY=''
PROBE_BINARY_SHA256=''
EXPECTED_VERSION=''
OLD_VERSION=''
FRONTEND_INDEX_SHA256=''
FRONTEND_RELEASE_SCRIPT=''
BACKUP_DIR=''
DATABASE_SCHEMA=''
ROLLBACK_LAYOUT='split'
CANDIDATE_PACKAGE_NAME=''
ROLLBACK_PACKAGE_NAME=''
CANDIDATE_PACKAGE_VERSION=''
ROLLBACK_PACKAGE_VERSION=''
OBSERVATION_SECONDS=120
if [[ ${LMM_DEPLOY_TEST_MODE:-0} == 1 ]]; then
  OBSERVATION_SECONDS=${LMM_TEST_OBSERVATION_SECONDS:-0}
fi
while (($#)); do
  case $1 in
    --workspace) (($# >= 2)) || die '--workspace requires a value'; WORKSPACE=$2; shift 2 ;;
    --package) (($# >= 2)) || die '--package requires a value'; PACKAGE=$2; shift 2 ;;
    --package-sha256) (($# >= 2)) || die '--package-sha256 requires a value'; PACKAGE_SHA256=$2; shift 2 ;;
    --rollback-core-package) (($# >= 2)) || die '--rollback-core-package requires a value'; ROLLBACK_CORE=$2; shift 2 ;;
    --rollback-core-sha256) (($# >= 2)) || die '--rollback-core-sha256 requires a value'; ROLLBACK_CORE_SHA256=$2; shift 2 ;;
    --rollback-go-package) (($# >= 2)) || die '--rollback-go-package requires a value'; ROLLBACK_GO=$2; shift 2 ;;
    --rollback-go-sha256) (($# >= 2)) || die '--rollback-go-sha256 requires a value'; ROLLBACK_GO_SHA256=$2; shift 2 ;;
    --probe-binary) (($# >= 2)) || die '--probe-binary requires a value'; PROBE_BINARY=$2; shift 2 ;;
    --probe-binary-sha256) (($# >= 2)) || die '--probe-binary-sha256 requires a value'; PROBE_BINARY_SHA256=$2; shift 2 ;;
    --expected-version) (($# >= 2)) || die '--expected-version requires a value'; EXPECTED_VERSION=$2; shift 2 ;;
    --old-version) (($# >= 2)) || die '--old-version requires a value'; OLD_VERSION=$2; shift 2 ;;
    --frontend-index-sha256) (($# >= 2)) || die '--frontend-index-sha256 requires a value'; FRONTEND_INDEX_SHA256=$2; shift 2 ;;
    --frontend-release-script) (($# >= 2)) || die '--frontend-release-script requires a value'; FRONTEND_RELEASE_SCRIPT=$2; shift 2 ;;
    --backup-dir) (($# >= 2)) || die '--backup-dir requires a value'; BACKUP_DIR=$2; shift 2 ;;
    --rollback-layout) (($# >= 2)) || die '--rollback-layout requires a value'; ROLLBACK_LAYOUT=$2; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done

case $ROLLBACK_LAYOUT in split|direct) ;; *) die 'rollback layout must be split or direct' ;; esac

case $ACTION in activate|rollback|confirm) ;; *) die 'first argument must be activate, rollback, or confirm' ;; esac
[[ $EUID -eq 0 || ${LMM_DEPLOY_TEST_MODE:-0} == 1 ]] || die 'must run as root'
observed_host=${LMM_DEPLOY_OBSERVED_HOST:-$(hostnamectl --static)}
[[ $observed_host == "$EXPECTED_HOST" ]] || die 'production host identity mismatch'
[[ $WORKSPACE == "$WORK_ROOT"/* && -d $WORKSPACE && ! -L $WORKSPACE ]] || die 'unsafe workspace'
assert_root_only_path "$WORK_ROOT"
assert_root_only_path "$WORKSPACE"
deployment_id=${WORKSPACE##*/}
is_id "$deployment_id" || die 'invalid deployment ID'
marker=$WORKSPACE/.lmm-deploy-workspace
[[ -f $marker && ! -L $marker ]] || die 'workspace marker is missing'
grep -Fqx "deployment_id=$deployment_id" "$marker" || die 'workspace marker mismatch'

state_dir=$WORKSPACE/state
staging_dir=$WORKSPACE/staging
status_file=$state_dir/status
manifest=$state_dir/deployment.env
probe_token=$state_dir/probe-token
install -d -m0700 "$state_dir" "${LOCK_FILE%/*}"
exec 9>"$LOCK_FILE"
flock -w 120 9 || die 'another Go deployment holds the global lock'

write_status() {
  local value=$1 temporary=$status_file.$$.new
  if [[ ${LMM_DEPLOY_TEST_MODE:-0} == 1 && ${LMM_TEST_FAIL_ROLLED_BACK_STATUS:-0} == 1 && $value == ROLLED_BACK\ * ]]; then
    return 87
  fi
  printf '%s\n' "$value" >"$temporary"
  chmod 0600 "$temporary"
  mv -Tf -- "$temporary" "$status_file"
}

status_word() {
  local value=''
  [[ -f $status_file && ! -L $status_file ]] && read -r value <"$status_file"
  printf '%s' "${value%% *}"
}

manifest_value() {
  local key=$1 value
  if ! value=$(awk -F= -v key="$key" '$1 == key { count += 1; value = substr($0, index($0, "=") + 1) } END { if (count == 1) print value; else exit 2 }' "$manifest"); then
    die "deployment manifest is missing or ambiguous: $key"
    return $?
  fi
  printf '%s' "$value"
}

assert_staged_file() {
  local path=$1 checksum=$2 label=$3
  [[ $path == "$staging_dir"/* && -s $path && -f $path && ! -L $path ]] || die "$label is missing or unsafe"
  is_sha256 "$checksum" || die "$label checksum is invalid"
  [[ $(sha256sum "$path" | awk '{print $1}') == "$checksum" ]] || die "$label checksum mismatch"
}

active_environment_file() {
  case $ROLLBACK_LAYOUT in
    split) printf '%s/lmm-api.env' "$OLD_CONFIG_DIR" ;;
    direct) printf '%s/lmm-api-go.env' "$NEW_CONFIG_DIR" ;;
  esac
}

go_package_record() {
  local archive=$1 record name version extra
  if ! record=$(pacman -Qp "$archive"); then
    die 'could not read Go package identity'
    return $?
  fi
  read -r name version extra <<<"$record"
  if [[ -n ${extra:-} || ! $version =~ ^[0-9][0-9A-Za-z._+]*-[1-9][0-9]*(\.[0-9]+)?$ ]]; then
    die 'invalid Go package identity'
    return $?
  fi
  case $name in
    "$SOURCE_PACKAGE"|"$AUR_PACKAGE") ;;
    *) die "unsupported Go package: $name"; return $? ;;
  esac
  printf '%s %s\n' "$name" "$version"
}

package_integrity_clean() {
  local package=$1 output=$2 line summary total found=0
  while IFS= read -r line; do
    [[ -n $line && $line != *$'\r'* && $found == 0 ]] || return 1
    if [[ $line == "backup file: $package: "?* ]]; then
      continue
    fi
    summary=${line#"$package: "}
    [[ $summary != "$line" ]] || return 1
    total=${summary%" total files, 0 altered files"}
    [[ $summary == "$total total files, 0 altered files" && $total =~ ^[0-9]+$ ]] || return 1
    found=1
  done <<<"$output"
  [[ $found == 1 ]]
}

verify_installed_package() {
  local package=$1 expected=$2 actual summary
  actual=$(pacman -Q "$package") || return $?
  [[ $actual == "$expected" ]] || return 1
  summary=$(LC_ALL=C pacman -Qkk "$package") || return $?
  package_integrity_clean "$package" "$summary"
}

load_package_layout() {
  local candidate_record rollback_record
  candidate_record=$(go_package_record "$PACKAGE")
  rollback_record=$(go_package_record "$ROLLBACK_GO")
  read -r CANDIDATE_PACKAGE_NAME CANDIDATE_PACKAGE_VERSION <<<"$candidate_record"
  read -r ROLLBACK_PACKAGE_NAME ROLLBACK_PACKAGE_VERSION <<<"$rollback_record"
  [[ $CANDIDATE_PACKAGE_NAME == "$ROLLBACK_PACKAGE_NAME" ]] || \
    die 'candidate and rollback Go package names differ'
}

uses_legacy_direct_layout() {
  [[ $ROLLBACK_LAYOUT == direct && $ROLLBACK_PACKAGE_NAME == "$SOURCE_PACKAGE" ]]
}

activates_bundled_frontend() {
  [[ $CANDIDATE_PACKAGE_NAME == "$SOURCE_PACKAGE" ]]
}

old_service() {
  if [[ $ROLLBACK_LAYOUT == split || $ROLLBACK_PACKAGE_NAME == "$AUR_PACKAGE" ]]; then
    printf '%s\n' "$NEW_SERVICE"
  else
    printf '%s\n' "$LEGACY_SERVICE"
  fi
}

native_request() {
  local base_url=$1 path=$2 output=$3 status=$4
  shift 4
  "$PROBE_BINARY" request --base-url "$base_url" --path "$path" \
    --output "$output" --status-file "$status" --timeout 8s --fail "$@"
}

probe_status() {
  local base_url=$1 expected=$2 output=$state_dir/status-response.json status=$state_dir/status-code
  native_request "$base_url" /api/status "$output" "$status"
  [[ $(<"$status") == 200 ]] && jq -e --arg version "$expected" \
    '.success == true and .ready == true and .data.version == $version' "$output" >/dev/null
}

probe_doctor() {
  local output=$state_dir/doctor-response.json status=$state_dir/doctor-code
  native_request http://127.0.0.1:3000 /api/livez "$output" "$status"
  [[ $(<"$status") == 200 ]] && jq -e '.success == true and .live == true' "$output" >/dev/null
}

probe_frontend() {
  local expected=$1 output=$state_dir/frontend-response.html status=$state_dir/frontend-code
  native_request https://api.lmm.best / "$output" "$status"
  [[ $(<"$status") == 200 && $(sha256sum "$output" | awk '{print $1}') == "$expected" ]]
}

probe_authenticated_models() {
  local output=$state_dir/models-response.json status=$state_dir/models-code
  [[ -s $probe_token && -f $probe_token && ! -L $probe_token && $(stat -c '%a' "$probe_token") == 600 ]] || return 1
  native_request https://api.lmm.best /v1/models "$output" "$status" --token-file "$probe_token"
  [[ $(<"$status") == 200 ]] && jq -e '.data | type == "array"' "$output" >/dev/null
}

probe_release() {
  local version=$1 frontend_sha=$2 attempt
  for ((attempt = 0; attempt < PROBE_ATTEMPTS; attempt += 1)); do
    if probe_status http://127.0.0.1:3000 "$version" && probe_doctor && \
       probe_status https://api.lmm.best "$version" && probe_frontend "$frontend_sha" && \
       probe_authenticated_models; then
      return 0
    fi
    sleep 1
  done
  return 1
}

copy_old_dropins_for_new_service() {
  local source=$OLD_DROPIN_DIR destination=$NEW_DROPIN_DIR
  [[ ! -e $destination && ! -L $destination ]] || die 'new service drop-in directory already exists'
  if [[ -d $source && ! -L $source ]]; then
    cp -a -- "$source" "$destination"
  else
    install -d -m0755 "$destination"
  fi
  printf 'deployment_id=%s\n' "$deployment_id" >"$destination/.lmm-deploy-owned"
  chmod 0600 "$destination/.lmm-deploy-owned"
}

remove_owned_new_dropins() {
  local destination=$NEW_DROPIN_DIR
  if [[ -f $destination/.lmm-deploy-owned && ! -L $destination/.lmm-deploy-owned ]] && \
     grep -Fqx "deployment_id=$deployment_id" "$destination/.lmm-deploy-owned"; then
    rm -rf -- "$destination"
  fi
}

create_probe_token() {
  local unit="lmm-api-go-token-$deployment_id" environment_file
  environment_file=$(active_environment_file)
  [[ ! -e $probe_token && ! -L $probe_token ]] || die 'probe token path already exists'
  # The single-quoted body is deliberately evaluated by the isolated systemd
  # unit, where EnvironmentFile supplies SQL_DSN without exposing its value.
  # shellcheck disable=SC2016
  systemd-run --quiet --wait --collect --unit="$unit" \
    --property=Type=oneshot \
    --property=EnvironmentFile="$environment_file" \
    /usr/bin/bash -c '
      set -Eeuo pipefail
      umask 077
      token=$(psql -X -v ON_ERROR_STOP=1 --dbname="$SQL_DSN" --no-align --tuples-only --command="
        SELECT tokens.key
        FROM tokens
        JOIN users ON users.id = tokens.user_id
        WHERE tokens.deleted_at IS NULL
          AND tokens.status = 1
          AND users.status = 1
          -- Mirror common.RoleAdminUser and the relay developer-access gate:
          -- admin/root tokens are valid on both sides of this Go cutover.
          AND users.role >= 10
          AND (tokens.expired_time = -1 OR tokens.expired_time > EXTRACT(EPOCH FROM NOW()))
          AND (tokens.unlimited_quota OR tokens.remain_quota > 0)
          AND COALESCE(LENGTH(BTRIM(tokens.allow_ips)), 0) = 0
        ORDER BY tokens.unlimited_quota DESC, tokens.remain_quota DESC, tokens.id DESC
        LIMIT 1")
      [[ $token =~ ^[A-Za-z0-9_-]{16,128}$ ]]
      printf "sk-%s" "$token" >"$1"
      chmod 0600 "$1"
    ' lmm-api-token-writer "$probe_token"
  [[ -s $probe_token && $(stat -c '%a' "$probe_token") == 600 ]] || die 'no safe production probe token is available'
}

discover_database_schema() {
  local unit="lmm-api-go-schema-$deployment_id" schema_file=$state_dir/database-schema schema environment_file
  environment_file=$(active_environment_file)
  [[ ! -e $schema_file && ! -L $schema_file ]] || die 'database schema path already exists'
  # Query through the exact pre-cutover SQL_DSN so the deployment freezes the
  # schema already used by production instead of guessing "public".
  # shellcheck disable=SC2016
  systemd-run --quiet --wait --collect --unit="$unit" \
    --property=Type=oneshot \
    --property=EnvironmentFile="$environment_file" \
    /usr/bin/bash -c '
      set -Eeuo pipefail
      umask 077
      schema=$(psql -X -v ON_ERROR_STOP=1 --dbname="$SQL_DSN" --no-align --tuples-only \
        --command="SELECT pg_catalog.current_schema()")
      [[ $schema =~ ^[a-z_][a-z0-9_]{0,62}$ ]]
      [[ $schema != pg_* && $schema != information_schema ]]
      printf "%s\n" "$schema" >"$1"
      chmod 0600 "$1"
    ' lmm-api-schema-writer "$schema_file"
  [[ -s $schema_file && -f $schema_file && ! -L $schema_file && $(stat -c '%a' "$schema_file") == 600 ]] || \
    die 'production database schema was not captured safely'
  IFS= read -r schema <"$schema_file"
  is_database_schema "$schema" || die 'production database schema is unsafe'
  [[ $(wc -l <"$schema_file") == 1 ]] || die 'production database schema capture is ambiguous'
  DATABASE_SCHEMA=$schema
}

is_database_schema() {
  [[ $1 =~ ^[a-z_][a-z0-9_]{0,62}$ && $1 != pg_* && $1 != information_schema ]]
}

install_new_environment_config() {
  local source=$1 destination temporary
  destination=$NEW_CONFIG_DIR/lmm-api-go.env
  temporary=$destination.$$.new
  is_database_schema "$DATABASE_SCHEMA" || die 'database schema is unavailable for the new service'
  [[ -f $source && ! -L $source ]] || die 'restored environment file is missing or unsafe'
  install -d -m0700 "$NEW_CONFIG_DIR"
  awk '$0 !~ /^[[:space:]]*PGOPTIONS=/' "$source" >"$temporary"
  printf 'PGOPTIONS="-c search_path=%s"\n' "$DATABASE_SCHEMA" >>"$temporary"
  chmod 0600 "$temporary"
  mv -Tf -- "$temporary" "$destination"
}

validate_old_configuration_directory() {
  [[ -d $OLD_CONFIG_DIR && ! -L $OLD_CONFIG_DIR ]] || die 'old configuration directory is missing or unsafe'
  if find "$OLD_CONFIG_DIR" -type l -print -quit | grep -q .; then
    die 'old configuration directory contains a symlink'
  fi
  if find "$OLD_CONFIG_DIR" ! -type d ! -type f -print -quit | grep -q .; then
    die 'old configuration directory contains an unsupported entry'
  fi
}

validate_current_go_configuration_directory() {
  [[ -d $NEW_CONFIG_DIR && ! -L $NEW_CONFIG_DIR ]] || die 'Go configuration directory is missing or unsafe'
  if find "$NEW_CONFIG_DIR" -type l -print -quit | grep -q .; then
    die 'Go configuration directory contains a symlink'
  fi
  if find "$NEW_CONFIG_DIR" ! -type d ! -type f -print -quit | grep -q .; then
    die 'Go configuration directory contains an unsupported entry'
  fi
  [[ -f $NEW_CONFIG_DIR/lmm-api-go.env && ! -L $NEW_CONFIG_DIR/lmm-api-go.env ]] || \
    die 'Go environment file is missing or unsafe'
}

restore_direct_environment_config() {
  local source=$1 destination temporary
  destination=$NEW_CONFIG_DIR/lmm-api-go.env
  temporary=$destination.$$.new
  if [[ ! -f $source || -L $source ]]; then
    die 'restored Go environment file is missing or unsafe'
    return $?
  fi
  install -d -m0700 "$NEW_CONFIG_DIR" || return $?
  install -m0600 "$source" "$temporary" || return $?
  mv -Tf -- "$temporary" "$destination" || return $?
}

harden_production_environment_config() {
  local destination temporary
  destination=$NEW_CONFIG_DIR/lmm-api-go.env
  temporary=$destination.$$.hardened
  [[ -f $destination && ! -L $destination ]] || die 'Go environment file is missing before production hardening'
  awk '
    /^[[:space:]]*SESSION_COOKIE_SECURE[[:space:]]*=/ { next }
    /^[[:space:]]*SESSION_COOKIE_TRUSTED_URL[[:space:]]*=/ { next }
    /^[[:space:]]*TRUSTED_PROXIES[[:space:]]*=/ { next }
    { print }
  ' "$destination" >"$temporary"
  {
    printf 'SESSION_COOKIE_SECURE=true\n'
    printf 'SESSION_COOKIE_TRUSTED_URL=https://api.lmm.best,https://lmm.best\n'
    printf 'TRUSTED_PROXIES=127.0.0.1/32,::1/128\n'
  } >>"$temporary"
  chmod 0600 "$temporary"
  mv -Tf -- "$temporary" "$destination"
}

remove_old_application_configuration() {
  local name path
  validate_old_configuration_directory
  for name in backend.conf backend.conf.pacsave backend.conf.pacnew \
    lmm-api.env lmm-api.env.pacsave lmm-api.env.pacnew; do
    path=$OLD_CONFIG_DIR/$name
    [[ ! -e $path && ! -L $path ]] && continue
    [[ -f $path && ! -L $path ]] || die "old application configuration is unsafe: $path"
    rm -f -- "$path"
  done
  # Auxiliary backup credentials and historical operator snapshots are not
  # package-owned application configuration. Preserve them in place.
  rmdir -- "$OLD_CONFIG_DIR" 2>/dev/null || true
}

run_migration() {
	local mode=$1 name=$2 binary=$3 unit environment_file migration_workdir path
	case $mode in apply|verify) ;; *) die 'candidate migration mode must be apply or verify' ;; esac
	[[ $name =~ ^[a-z][a-z-]{0,31}$ ]] || die 'candidate migration name is invalid'
	is_database_schema "$DATABASE_SCHEMA" || die 'database schema is unavailable for migration'
	environment_file=$(active_environment_file)
	migration_workdir=$WORKSPACE/tmp/migrations/$name
	path=$WORKSPACE
	for component in tmp migrations "$name"; do
		path=$path/$component
		if [[ -e $path || -L $path ]]; then
			[[ -d $path && ! -L $path ]] || die "migration directory is unsafe: $path"
		else
			mkdir -m0700 "$path"
		fi
		chmod 0700 "$path"
	done
	unit="lmm-api-go-migrate-$name-$deployment_id"
	systemd-run --quiet --wait --collect --unit="$unit" \
		--property=Type=oneshot \
		--property=WorkingDirectory="$migration_workdir" \
		--property=EnvironmentFile="$environment_file" \
		--setenv=GIN_MODE=release \
		--setenv="LMM_DB_MIGRATION_MODE=$mode" \
		--setenv="PGOPTIONS=-c search_path=$DATABASE_SCHEMA" \
		"$binary" migrate "--$mode"
}

release_transaction_lock() {
  local lock_marker=$TRANSACTION_LOCK/deployment.env
  [[ ${LMM_DEPLOY_TEST_MODE:-0} != 1 || ${LMM_TEST_FAIL_ROLLBACK_UNLOCK:-0} != 1 ]] || return 88
  if [[ ! -e $TRANSACTION_LOCK && ! -L $TRANSACTION_LOCK ]]; then
    return 0
  fi
  [[ -d $TRANSACTION_LOCK && ! -L $TRANSACTION_LOCK && -f $lock_marker && ! -L $lock_marker ]] || return 1
  grep -Fqx "deployment_id=$deployment_id" "$lock_marker" || return 1
  rm -f -- "$lock_marker" || return $?
  rmdir -- "$TRANSACTION_LOCK" || return $?
}

cleanup_probe_token() {
  [[ ${LMM_DEPLOY_TEST_MODE:-0} != 1 || ${LMM_TEST_FAIL_ROLLBACK_PROBE_CLEANUP:-0} != 1 ]] || return 86
  rm -f -- "$probe_token"
}

finalize_transaction() {
  cleanup_probe_token || return $?
  release_transaction_lock
}

cleanup_failed_prearm() {
  local rc=$? current
  ((rc != 0)) || return 0
  current=$(status_word)
  case $current in
    MUTATION_PENDING|MIGRATING|DEPLOYING|OBSERVING|AWAITING_CONFIRMATION|ROLLBACK_REQUIRED|ROLLING_BACK|CONFIRMED|ROLLED_BACK)
      return 0
      ;;
  esac
  rm -f -- "$probe_token"
  write_status "FAILED_PREARM rc=$rc" || true
  release_transaction_lock || true
}

rollback_failed() {
  local reason=$1 step=$2 rc=$3
  write_status "ROLLBACK_REQUIRED reason=$reason failure_step=$step failure_rc=$rc" || true
  return "$rc"
}

rollback_step() {
  local reason=$1 step=$2 rc
  shift 2
  "$@" && return 0
  rc=$?
  rollback_failed "$reason" "$step" "$rc"
}

perform_rollback() {
  local reason=$1 old_frontend config_restore rc
  if [[ $(status_word) == CONFIRMED ]]; then
    return 0
  fi
  write_status "ROLLING_BACK $reason" || return $?
  old_frontend=$(manifest_value old_frontend_release) || {
    rc=$?; rollback_failed "$reason" manifest-old-frontend "$rc"; return $?
  }
  config_restore=$state_dir/config-restore
  systemctl disable --now "$NEW_SERVICE" >/dev/null 2>&1 || true
  if [[ -d $FRONTEND_ROOT/releases/$old_frontend ]]; then
    rollback_step "$reason" frontend "$FRONTEND_RELEASE_SCRIPT" rollback \
      --root "$FRONTEND_ROOT" --release "$old_frontend" --keep 3 || return $?
  fi
  if [[ $ROLLBACK_LAYOUT == split ]]; then
    rollback_step "$reason" package-install pacman -U --noconfirm "$ROLLBACK_CORE" "$ROLLBACK_GO" || return $?
    rollback_step "$reason" config-directory install -d -m0700 "$OLD_CONFIG_DIR" || return $?
    rollback_step "$reason" config-environment install -m0600 \
      "$config_restore/lmm-api/lmm-api.env" "$OLD_CONFIG_DIR/lmm-api.env" || return $?
    rollback_step "$reason" config-backend install -m0644 \
      "$config_restore/lmm-api/backend.conf" "$OLD_CONFIG_DIR/backend.conf" || return $?
  else
    rollback_step "$reason" package-install pacman -U --noconfirm "$ROLLBACK_GO" || return $?
    rollback_step "$reason" config-environment restore_direct_environment_config \
      "$config_restore/lmm-api-go/lmm-api-go.env" || return $?
    if uses_legacy_direct_layout; then
      rollback_step "$reason" service-dropins remove_owned_new_dropins || return $?
    fi
  fi
  rollback_step "$reason" package-integrity verify_installed_package \
    "$ROLLBACK_PACKAGE_NAME" "$ROLLBACK_PACKAGE_NAME $ROLLBACK_PACKAGE_VERSION" || return $?
  rollback_step "$reason" daemon-reload systemctl daemon-reload || return $?
  rollback_step "$reason" service-start systemctl enable --now "$(old_service)" || return $?
  PROBE_BINARY=$(manifest_value probe_binary) || {
    rc=$?; rollback_failed "$reason" manifest-probe-binary "$rc"; return $?
  }
  OLD_VERSION=$(manifest_value old_version) || {
    rc=$?; rollback_failed "$reason" manifest-old-version "$rc"; return $?
  }
  FRONTEND_INDEX_SHA256=$(manifest_value old_frontend_index_sha256) || {
    rc=$?; rollback_failed "$reason" manifest-frontend-checksum "$rc"; return $?
  }
  rollback_step "$reason" release-probe probe_release "$OLD_VERSION" "$FRONTEND_INDEX_SHA256" || return $?
  rollback_step "$reason" probe-cleanup cleanup_probe_token || return $?
  write_status "ROLLED_BACK $OLD_VERSION $reason" || {
    rc=$?; rollback_failed "$reason" terminal-status "$rc"; return $?
  }
  rollback_step "$reason" finalize finalize_transaction
}

activation_error() {
  local rc=$? failure_line=${BASH_LINENO[0]:-unknown}
  trap - ERR
  write_status "ROLLBACK_REQUIRED reason=activation-or-observation-failure failure_line=$failure_line rc=$rc" || true
  exit "$rc"
}

load_manifest() {
  [[ -f $manifest && ! -L $manifest ]] || die 'deployment manifest is missing'
  [[ $(manifest_value format) == 2 ]] || die 'deployment manifest format is incompatible'
  ROLLBACK_LAYOUT=$(manifest_value rollback_layout)
  case $ROLLBACK_LAYOUT in split|direct) ;; *) die 'deployment manifest rollback layout is invalid' ;; esac
  PACKAGE=$(manifest_value package)
  PACKAGE_SHA256=$(manifest_value package_sha256)
  ROLLBACK_CORE=$(manifest_value rollback_core)
  ROLLBACK_CORE_SHA256=$(manifest_value rollback_core_sha256)
  ROLLBACK_GO=$(manifest_value rollback_go)
  ROLLBACK_GO_SHA256=$(manifest_value rollback_go_sha256)
  PROBE_BINARY=$(manifest_value probe_binary)
  PROBE_BINARY_SHA256=$(manifest_value probe_binary_sha256)
  EXPECTED_VERSION=$(manifest_value expected_version)
  OLD_VERSION=$(manifest_value old_version)
  FRONTEND_INDEX_SHA256=$(manifest_value frontend_index_sha256)
  FRONTEND_RELEASE_SCRIPT=$(manifest_value frontend_release_script)
  BACKUP_DIR=$(manifest_value backup_dir)
  DATABASE_SCHEMA=$(manifest_value database_schema)
  for pair in \
    "$PACKAGE:$PACKAGE_SHA256:candidate package" \
    "$ROLLBACK_CORE:$ROLLBACK_CORE_SHA256:core rollback package" \
    "$ROLLBACK_GO:$ROLLBACK_GO_SHA256:Go rollback package" \
    "$PROBE_BINARY:$PROBE_BINARY_SHA256:probe binary"; do
    path=${pair%%:*}; remainder=${pair#*:}; checksum=${remainder%%:*}; label=${remainder#*:}
    assert_staged_file "$path" "$checksum" "$label"
  done
  load_package_layout
  [[ $FRONTEND_RELEASE_SCRIPT == "$staging_dir"/* && -x $FRONTEND_RELEASE_SCRIPT && ! -L $FRONTEND_RELEASE_SCRIPT ]] || \
    die 'frontend release script is missing or unsafe'
  [[ $BACKUP_DIR == "$BACKUP_ROOT/$deployment_id" && -d $BACKUP_DIR && ! -L $BACKUP_DIR ]] || die 'verified target backup is missing'
  [[ $EXPECTED_VERSION =~ ^[0-9][0-9A-Za-z._+]*$ && $OLD_VERSION =~ ^[0-9][0-9A-Za-z._+]*$ ]] || die 'invalid release version'
  is_database_schema "$DATABASE_SCHEMA" || die 'deployment manifest database schema is unsafe'
  is_sha256 "$FRONTEND_INDEX_SHA256" || die 'frontend checksum is invalid'
  if [[ $ACTION == confirm ]]; then
    OBSERVATION_SECONDS=$(manifest_value observation_seconds)
    OBSERVATION_STARTED_EPOCH=$(manifest_value observation_started_epoch)
    [[ $OBSERVATION_SECONDS =~ ^[0-9]+$ && $OBSERVATION_STARTED_EPOCH =~ ^[0-9]+$ ]] || die 'observation evidence is invalid'
    if [[ ${LMM_DEPLOY_TEST_MODE:-0} != 1 ]]; then
      ((OBSERVATION_SECONDS >= 120)) || die 'confirmation requires at least 120 seconds of observation'
    fi
  fi
}

case $ACTION in
  activate)
    trap cleanup_failed_prearm EXIT
    [[ ! -e $manifest && ! -L $manifest && ! -e $status_file && ! -L $status_file ]] || die 'deployment state already exists'
    for pair in \
      "$PACKAGE:$PACKAGE_SHA256:candidate package" \
      "$ROLLBACK_CORE:$ROLLBACK_CORE_SHA256:core rollback package" \
      "$ROLLBACK_GO:$ROLLBACK_GO_SHA256:Go rollback package" \
      "$PROBE_BINARY:$PROBE_BINARY_SHA256:probe binary"; do
      path=${pair%%:*}; remainder=${pair#*:}; checksum=${remainder%%:*}; label=${remainder#*:}
      assert_staged_file "$path" "$checksum" "$label"
    done
    [[ $FRONTEND_RELEASE_SCRIPT == "$staging_dir"/* && -x $FRONTEND_RELEASE_SCRIPT && ! -L $FRONTEND_RELEASE_SCRIPT ]] || die 'frontend release script is unsafe'
    [[ $BACKUP_DIR == "$BACKUP_ROOT/$deployment_id" && -d $BACKUP_DIR && ! -L $BACKUP_DIR ]] || die 'verified target backup is missing'
    [[ $EXPECTED_VERSION =~ ^[0-9][0-9A-Za-z._+]*$ && $OLD_VERSION =~ ^[0-9][0-9A-Za-z._+]*$ ]] || die 'invalid release version'
    is_sha256 "$FRONTEND_INDEX_SHA256" || die 'frontend checksum is invalid'
    [[ $ROLLBACK_SECONDS =~ ^[0-9]+$ && $ROLLBACK_SECONDS -ge 600 && $ROLLBACK_SECONDS -le 1800 ]] || die 'rollback window must be 600-1800 seconds'
    load_package_layout
    [[ ${CANDIDATE_PACKAGE_VERSION%-*} == "$EXPECTED_VERSION" ]] || die 'candidate package identity mismatch'
    [[ ${ROLLBACK_PACKAGE_VERSION%-*} == "$OLD_VERSION" ]] || die 'Go rollback package version mismatch'
    verify_installed_package "$ROLLBACK_PACKAGE_NAME" "$ROLLBACK_PACKAGE_NAME $ROLLBACK_PACKAGE_VERSION" || \
      die 'Go rollback package identity or integrity mismatch'
    if [[ $ROLLBACK_LAYOUT == split ]]; then
      [[ $(pacman -Qp "$ROLLBACK_CORE") == "$(pacman -Q lmm-api)" ]] || die 'core rollback package identity mismatch'
      systemctl is-active --quiet "$(old_service)" || die 'pre-cutover service is not active'
      systemctl is-enabled --quiet "$(old_service)" || die 'pre-cutover service is not enabled'
      validate_old_configuration_directory
    else
      [[ $(<"$ROLLBACK_CORE") == direct ]] || die 'direct rollback marker is invalid'
      ! pacman -Q lmm-api >/dev/null 2>&1 || die 'direct Go upgrade unexpectedly found the split core package'
      systemctl is-active --quiet "$(old_service)" || die 'pre-upgrade Go service is not active'
      systemctl is-enabled --quiet "$(old_service)" || die 'pre-upgrade Go service is not enabled'
      validate_current_go_configuration_directory
    fi
    old_frontend_link=$(readlink -- "$FRONTEND_ROOT/current")
    [[ $old_frontend_link =~ ^releases/([A-Za-z0-9][A-Za-z0-9._-]{0,127})$ ]] || die 'pre-cutover frontend identity is unsafe'
    old_frontend_release=${BASH_REMATCH[1]}
    old_frontend_index_sha256=$(sha256sum "$FRONTEND_ROOT/current/index.html" | awk '{print $1}')
    if ! activates_bundled_frontend; then
      [[ $FRONTEND_INDEX_SHA256 == "$old_frontend_index_sha256" ]] || \
        die 'backend-only AUR upgrade must preserve the active frontend identity'
    fi
    config_restore=$state_dir/config-restore
    mkdir -m0700 "$config_restore"
    if tar -tf "$BACKUP_DIR/configuration.archive" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
      die 'configuration backup contains an unsafe path'
    fi
    tar --extract --file "$BACKUP_DIR/configuration.archive" --directory "$config_restore" --no-same-owner --no-same-permissions
    if [[ $ROLLBACK_LAYOUT == split ]]; then
      [[ -f $config_restore/lmm-api/lmm-api.env && ! -L $config_restore/lmm-api/lmm-api.env ]] || \
        die 'configuration backup lacks environment file'
      [[ -f $config_restore/lmm-api/backend.conf && ! -L $config_restore/lmm-api/backend.conf ]] || \
        die 'configuration backup lacks backend selection'
    else
      [[ -f $config_restore/lmm-api-go/lmm-api-go.env && ! -L $config_restore/lmm-api-go/lmm-api-go.env ]] || \
        die 'configuration backup lacks the Go environment file'
    fi
    discover_database_schema
    create_probe_token
    probe_status http://127.0.0.1:3000 "$OLD_VERSION" || die 'pre-cutover local status probe failed'
    probe_status https://api.lmm.best "$OLD_VERSION" || die 'pre-cutover public status probe failed'
    probe_authenticated_models || die 'pre-cutover authenticated business probe failed'
		{
      printf 'format=2\ndeployment_id=%s\npackage=%s\npackage_sha256=%s\n' "$deployment_id" "$PACKAGE" "$PACKAGE_SHA256"
      printf 'rollback_layout=%s\n' "$ROLLBACK_LAYOUT"
      printf 'rollback_core=%s\nrollback_core_sha256=%s\n' "$ROLLBACK_CORE" "$ROLLBACK_CORE_SHA256"
      printf 'rollback_go=%s\nrollback_go_sha256=%s\n' "$ROLLBACK_GO" "$ROLLBACK_GO_SHA256"
      printf 'probe_binary=%s\nprobe_binary_sha256=%s\n' "$PROBE_BINARY" "$PROBE_BINARY_SHA256"
      printf 'expected_version=%s\nold_version=%s\n' "$EXPECTED_VERSION" "$OLD_VERSION"
      printf 'frontend_index_sha256=%s\nold_frontend_release=%s\nold_frontend_index_sha256=%s\n' \
        "$FRONTEND_INDEX_SHA256" "$old_frontend_release" "$old_frontend_index_sha256"
      printf 'frontend_release_script=%s\nbackup_dir=%s\n' "$FRONTEND_RELEASE_SCRIPT" "$BACKUP_DIR"
      printf 'database_schema=%s\nobservation_seconds=%s\n' "$DATABASE_SCHEMA" "$OBSERVATION_SECONDS"
    } >"$manifest.new"
    chmod 0600 "$manifest.new"
    mv -T "$manifest.new" "$manifest"
    # This rollback-capable status is durable before the first live mutation.
    write_status "MUTATION_PENDING version=$EXPECTED_VERSION"
		trap activation_error ERR
    if uses_legacy_direct_layout; then
      copy_old_dropins_for_new_service
    fi
    systemctl disable --now "$(old_service)"
		write_status "MIGRATING version=$EXPECTED_VERSION"
		run_migration apply candidate-apply "$PROBE_BINARY"
		run_migration verify candidate-verify "$PROBE_BINARY"
		run_migration verify rollback-verify "$INSTALLED_BINARY"
		write_status "DEPLOYING version=$EXPECTED_VERSION"
    if [[ $ROLLBACK_LAYOUT == split ]]; then
      # pacman does not resolve a local package's conflict with an explicitly
      # installed package when --noconfirm is used. Remove the captured core
      # package first; the armed rollback transaction can reinstall it if the
      # direct package upgrade fails at any later step.
      pacman -Rdd --noconfirm lmm-api
    fi
    pacman -U --noconfirm "$PACKAGE"
    if [[ $ROLLBACK_LAYOUT == split ]]; then
      install_new_environment_config "$config_restore/lmm-api/lmm-api.env"
      remove_old_application_configuration
    else
      restore_direct_environment_config "$config_restore/lmm-api-go/lmm-api-go.env"
    fi
    harden_production_environment_config
    systemctl daemon-reload
    verify_installed_package "$CANDIDATE_PACKAGE_NAME" "$CANDIDATE_PACKAGE_NAME $CANDIDATE_PACKAGE_VERSION" || \
      die 'installed candidate package identity or integrity mismatch'
    [[ $("$INSTALLED_BINARY" version) == "$EXPECTED_VERSION" ]] || die 'installed binary version mismatch'
    for removed in "$REMOVED_SELECTOR" "$REMOVED_PROVIDER_ROOT" "$REMOVED_LEGACY_SERVICE"; do
      [[ ! -e $removed && ! -L $removed ]] || die "removed split-architecture path remains: $removed"
    done
    [[ -L $CANONICAL_LAUNCHER && $(readlink -- "$CANONICAL_LAUNCHER") == lmm-api-go ]] || \
      die 'canonical /usr/bin/lmm-api symlink is missing'
    systemctl enable --now "$NEW_SERVICE"
    if activates_bundled_frontend; then
      "$FRONTEND_RELEASE_SCRIPT" publish --root "$FRONTEND_ROOT" \
        --source "$PACKAGED_FRONTEND_DIR" --release "$EXPECTED_VERSION" --keep 3
    fi
    observation_started_epoch=$(date +%s)
    printf 'observation_started_epoch=%s\n' "$observation_started_epoch" >>"$manifest"
    write_status "OBSERVING version=$EXPECTED_VERSION seconds=$OBSERVATION_SECONDS"
    observation_end=$((observation_started_epoch + OBSERVATION_SECONDS))
    while :; do
      probe_release "$EXPECTED_VERSION" "$FRONTEND_INDEX_SHA256"
      now_epoch=$(date +%s)
      ((now_epoch >= observation_end)) && break
      sleep_seconds=$((observation_end - now_epoch))
      ((sleep_seconds > 10)) && sleep_seconds=10
      sleep "$sleep_seconds"
    done
    trap - ERR
    write_status "AWAITING_CONFIRMATION version=$EXPECTED_VERSION observation_seconds=$OBSERVATION_SECONDS"
    trap - EXIT
    printf 'status=%s\n' "$(<"$status_file")"
    ;;
  rollback)
    load_manifest
    case $(status_word) in
      CONFIRMED|ROLLED_BACK) finalize_transaction; exit $? ;;
		MUTATION_PENDING|MIGRATING|DEPLOYING|OBSERVING|AWAITING_CONFIRMATION|ROLLBACK_REQUIRED|ROLLING_BACK) ;;
      *) die 'rollback state is not eligible' ;;
    esac
    perform_rollback explicit-operator-request
    ;;
  confirm)
    load_manifest
    if [[ $(status_word) == CONFIRMED ]]; then
      finalize_transaction
      exit $?
    fi
    [[ $(status_word) == AWAITING_CONFIRMATION ]] || die 'deployment is not awaiting confirmation'
    now_epoch=$(date +%s)
    ((now_epoch >= OBSERVATION_STARTED_EPOCH + OBSERVATION_SECONDS)) || die 'configured observation window has not completed'
    systemctl is-active --quiet "$NEW_SERVICE" || die 'new service is not active'
    probe_release "$EXPECTED_VERSION" "$FRONTEND_INDEX_SHA256" || die 'final native CLI health and identity probes failed'
    write_status "CONFIRMED version=$EXPECTED_VERSION"
    finalize_transaction
    printf 'confirmed=%s\n' "$EXPECTED_VERSION"
    ;;
esac
