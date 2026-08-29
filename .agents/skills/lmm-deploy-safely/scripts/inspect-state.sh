#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly HOST_PATTERN='^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$'
readonly DSN_ASSIGNMENT_PATTERN="^[[:space:]]*(export[[:space:]]+)?(SQL_DSN|DATABASE_URL|DB_DSN|DATABASE_DSN)[[:space:]]*=[[:space:]]*['\"]?([^'\"[:space:]]+)"
readonly SQLITE_ASSIGNMENT_PATTERN="^[[:space:]]*(export[[:space:]]+)?(SQLITE_PATH|SQLITE_FILE|SQLITE_DATABASE)[[:space:]]*=[[:space:]]*['\"]?[^'\"[:space:]]+"

usage() {
  printf 'Usage: %s --role local|test|production [--root-prefix ABSOLUTE_PATH] [--expected-host HOST] [--observed-host HOST] [--format kv|json]\n' "${0##*/}" >&2
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 2
}

reject_unsafe_text() {
  local value=$1
  [[ $value != *$'\n'* && $value != *$'\r'* && $value != *$'\t'* ]] ||
    die 'path contains control characters'
  [[ $value != *'~'* && $value != *'$'* && $value != *'*'* &&
     $value != *'?'* && $value != *'['* && $value != *']'* &&
     $value != *'{'* && $value != *'}'* ]] || die 'path contains unresolved shell syntax or a glob'
}

assert_no_symlink_components() {
  local path=$1
  local current='/'
  local component
  local -a components=()

  IFS='/' read -r -a components <<< "${path#/}"
  for component in "${components[@]}"; do
    [[ -n $component ]] || continue
    if [[ $current == '/' ]]; then
      current="/$component"
    else
      current="$current/$component"
    fi
    [[ ! -L $current ]] || die 'root prefix traverses a symbolic link'
    [[ -e $current ]] || break
  done
}

validate_root_prefix() {
  local path=$1
  local canonical

  reject_unsafe_text "$path"
  [[ $path == /* ]] || die 'root prefix must be absolute'
  canonical=$(realpath -m -- "$path")
  [[ $canonical == "$path" ]] || die 'root prefix must be canonical'
  assert_no_symlink_components "$canonical"
  [[ $canonical == '/' || -d $canonical ]] || die 'root prefix does not exist'
  printf '%s\n' "$canonical"
}

validate_host() {
  [[ $1 =~ $HOST_PATTERN ]] || die 'invalid hostname'
}

rooted() {
  local logical=$1
  if [[ $root_prefix == '/' ]]; then
    printf '%s\n' "$logical"
  else
    printf '%s%s\n' "$root_prefix" "$logical"
  fi
}

present() {
  if [[ -e $1 || -L $1 ]]; then
    printf 'true\n'
  else
    printf 'false\n'
  fi
}

safe_release_identity() {
  local link=$1
  local target

  if [[ ! -L $link ]]; then
    printf 'unknown\n'
    return
  fi
  target=$(readlink -- "$link")
  target=${target%/}
  target=${target##*/}
  if [[ $target =~ ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ ]]; then
    printf '%s\n' "$target"
  else
    printf 'unknown\n'
  fi
}

read_backend_selection() {
  local link=$1 target provider

  if [[ -L $link ]]; then
    target=$(readlink -- "$link") || {
      printf 'unsafe\n'
      return
    }
    case "$target" in
      lmm-api-go) provider=go ;;
      lmm-api-rs) provider=rust ;;
      *) printf 'unsafe\n'; return ;;
    esac
    target=$(rooted "/usr/bin/$target")
    if [[ ! -f $target || -L $target || ! -x $target ]]; then
      printf 'unsafe\n'
      return
    fi
    printf '%s\n' "$provider"
  elif [[ -f $link ]]; then
    printf 'legacy-regular\n'
  elif [[ -e $link ]]; then
    printf 'unsafe\n'
  else
    printf 'missing\n'
  fi
}

read_kv_token() {
  local file=$1 key=$2 token value=''
  local found=false

  while IFS= read -r token; do
    [[ $token == "$key="* ]] || continue
    [[ $found == false ]] || return 1
    found=true
    value=${token#*=}
  done < <(tr '[:space:]' '\n' < "$file")
  [[ $value =~ ^[A-Za-z0-9][A-Za-z0-9._:-]{0,255}$ ]] || return 1
  printf '%s\n' "$value"
}

read_cutover_state() {
  local engine=$1
  local boundary journal verification
  local file
  local boundary_transaction boundary_schema boundary_revision
  local journal_transaction journal_schema journal_revision journal_phase
  local verify_transaction verify_schema

  [[ $engine == postgres ]] || {
    printf 'not_required\n'
    return
  }
  boundary=$(rooted /var/lib/lmm-api-cutover/pg-write-boundary)
  journal=$(rooted /var/lib/lmm-api-cutover/cutover-journal)
  verification=$(rooted /var/log/lmm-api-cutover/post-cutover-verify.json)
  for file in "$boundary" "$journal" "$verification"; do
    [[ -f $file && ! -L $file ]] || {
      printf 'missing\n'
      return
    }
  done
  command -v jq >/dev/null 2>&1 || {
    printf 'unverified\n'
    return
  }

  boundary_transaction=$(read_kv_token "$boundary" transaction) || {
    printf 'invalid\n'
    return
  }
  boundary_schema=$(read_kv_token "$boundary" schema) || {
    printf 'invalid\n'
    return
  }
  boundary_revision=$(read_kv_token "$boundary" revision) || {
    printf 'invalid\n'
    return
  }
  journal_transaction=$(read_kv_token "$journal" transaction) || {
    printf 'invalid\n'
    return
  }
  journal_schema=$(read_kv_token "$journal" schema) || {
    printf 'invalid\n'
    return
  }
  journal_revision=$(read_kv_token "$journal" revision) || {
    printf 'invalid\n'
    return
  }
  journal_phase=$(read_kv_token "$journal" phase) || {
    printf 'invalid\n'
    return
  }
  verify_transaction=$(jq -er 'select(
      .status == "verified" and
      .database_engine == "postgresql" and
      .historical_migration_verified == true
    ) | .transaction | strings' "$verification" 2>/dev/null) || {
    printf 'invalid\n'
    return
  }
  verify_schema=$(jq -er '.schema | strings' "$verification" 2>/dev/null) || {
    printf 'invalid\n'
    return
  }

  if [[ $journal_phase == COMPLETE &&
        $boundary_transaction == "$journal_transaction" &&
        $boundary_transaction == "$verify_transaction" &&
        $boundary_schema == "$journal_schema" &&
        $boundary_schema == "$verify_schema" &&
        $boundary_revision == "$journal_revision" ]]; then
    printf 'verified\n'
  else
    printf 'invalid\n'
  fi
}

classify_database() {
  local file line value engine
  local seen_sqlite=false
  local seen_postgres=false
  local seen_mysql=false
  local -a files=(
    "$(rooted /etc/lmm-api-go/lmm-api-go.env)"
    "$(rooted /etc/lmm-api/lmm-api.env)"
    "$(rooted /etc/lmm-api-rs/lmm-api.env)"
    "$(rooted /etc/lmm-api-rs/config.env)"
    "$(rooted /etc/lmm-api-rs-single/lmm-api.env)"
  )

  for file in "${files[@]}"; do
    [[ -f $file && ! -L $file ]] || continue
    while IFS= read -r line || [[ -n $line ]]; do
      engine=''
      if [[ $line =~ $DSN_ASSIGNMENT_PATTERN ]]; then
        value=${BASH_REMATCH[3],,}
        case "$value" in
          sqlite:*|file:*) engine=sqlite ;;
          postgres:*|postgresql:*) engine=postgres ;;
          mysql:*) engine=mysql ;;
        esac
      elif [[ $line =~ $SQLITE_ASSIGNMENT_PATTERN ]]; then
        engine=sqlite
      fi
      case "$engine" in
        sqlite) seen_sqlite=true ;;
        postgres) seen_postgres=true ;;
        mysql) seen_mysql=true ;;
      esac
    done < "$file"
  done

  count=0
  [[ $seen_sqlite == false ]] || ((count += 1))
  [[ $seen_postgres == false ]] || ((count += 1))
  [[ $seen_mysql == false ]] || ((count += 1))
  case "$count" in
    0) printf 'unknown\n' ;;
    1)
      if [[ $seen_sqlite == true ]]; then
        printf 'sqlite\n'
      elif [[ $seen_postgres == true ]]; then
        printf 'postgres\n'
      else
        printf 'mysql\n'
      fi
      ;;
    *) printf 'disagreement\n' ;;
  esac
}

package_identity() {
  local package_root
  local entry name
  local -a names=()

  package_root=$(rooted /var/lib/pacman/local)
  [[ -d $package_root && ! -L $package_root ]] || {
    printf 'unknown\n'
    return
  }
  while IFS= read -r entry; do
    name=${entry##*/}
    [[ $name =~ ^lmm-api-[A-Za-z0-9._+-]+$ ]] || continue
    names+=("$name")
  done < <(find "$package_root" -mindepth 1 -maxdepth 1 -type d -name 'lmm-api-*' -print 2>/dev/null | LC_ALL=C sort)
  if ((${#names[@]} == 0)); then
    printf 'unknown\n'
  else
    local joined
    joined=$(IFS=,; printf '%s' "${names[*]}")
    printf '%s\n' "$joined"
  fi
}

service_state() {
  local unit
  unit=$(rooted /usr/lib/systemd/system/lmm-api.service)
  if [[ ! -f $unit || -L $unit ]]; then
    printf 'absent\n'
  elif [[ $root_prefix != '/' ]]; then
    printf 'fixture-present\n'
  elif command -v systemctl >/dev/null 2>&1; then
    local state
    state=$(systemctl is-active lmm-api.service 2>/dev/null || true)
    case "$state" in
      active|activating|deactivating|inactive|failed) printf '%s\n' "$state" ;;
      *) printf 'unknown\n' ;;
    esac
  else
    printf 'unknown\n'
  fi
}

json_escape() {
  local value=$1
  value=${value//\\/\\\\}
  value=${value//\"/\\\"}
  value=${value//$'\n'/\\n}
  value=${value//$'\r'/\\r}
  value=${value//$'\t'/\\t}
  printf '%s' "$value"
}

role=''
root_prefix='/'
expected_host=''
observed_host=''
format='kv'

while (($# > 0)); do
  case "$1" in
    --role)
      (($# >= 2)) || die 'missing value for --role'
      role=$2
      shift 2
      ;;
    --root-prefix)
      (($# >= 2)) || die 'missing value for --root-prefix'
      root_prefix=$2
      shift 2
      ;;
    --expected-host)
      (($# >= 2)) || die 'missing value for --expected-host'
      expected_host=$2
      shift 2
      ;;
    --observed-host)
      (($# >= 2)) || die 'missing value for --observed-host'
      observed_host=$2
      shift 2
      ;;
    --format)
      (($# >= 2)) || die 'missing value for --format'
      format=$2
      shift 2
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage
      die 'unknown argument'
      ;;
  esac
done

case "$role" in
  local|test|production) ;;
  *) die 'role must be local, test, or production' ;;
esac
case "$format" in
  kv|json) ;;
  *) die 'format must be kv or json' ;;
esac

root_prefix=$(validate_root_prefix "$root_prefix")
if [[ -z $observed_host ]]; then
  if command -v hostname >/dev/null 2>&1; then
    observed_host=$(hostname -s)
  elif [[ -f $(rooted /etc/hostname) ]]; then
    observed_host=$(<"$(rooted /etc/hostname)")
  else
    die 'cannot determine observed hostname'
  fi
fi
validate_host "$observed_host"
if [[ -n $expected_host ]]; then
  validate_host "$expected_host"
elif [[ $role != local ]]; then
  die 'test and production inspection require --expected-host'
fi

host_match=true
if [[ -n $expected_host && $expected_host != "$observed_host" ]]; then
  host_match=false
fi

db_engine=$(classify_database)
backend_selection=$(read_backend_selection "$(rooted /usr/bin/lmm-api)")
package_id=$(package_identity)
service=$(service_state)
frontend_release=$(safe_release_identity "$(rooted /srv/lmm-api-frontend/current)")
cutover_state=$(read_cutover_state "$db_engine")

declare -a keys=(
  role observed_host expected_host host_match db_engine backend_selection package_identity service_state
  public_cli_present provider_link_state app_config_present service_unit_present
  go_provider_present rust_provider_present frontend_root_present frontend_current_present
  frontend_release cutover_state pg_write_boundary_present cutover_journal_present
  post_cutover_verify_present deploy_work_root_present staging_root_present backup_root_present
)
declare -a values=(
  "$role" "$observed_host" "${expected_host:-none}" "$host_match" "$db_engine" "$backend_selection" "$package_id" "$service"
  "$(present "$(rooted /usr/bin/lmm-api)")" "$backend_selection" \
  "$(present "$(rooted /etc/lmm-api-go/lmm-api-go.env)")" \
  "$(present "$(rooted /usr/lib/systemd/system/lmm-api.service)")" \
  "$(present "$(rooted /usr/bin/lmm-api-go)")" \
  "$(present "$(rooted /usr/bin/lmm-api-rs)")" \
  "$(present "$(rooted /srv/lmm-api-frontend)")" "$(present "$(rooted /srv/lmm-api-frontend/current)")" \
  "$frontend_release" "$cutover_state" \
  "$(present "$(rooted /var/lib/lmm-api-cutover/pg-write-boundary)")" \
  "$(present "$(rooted /var/lib/lmm-api-cutover/cutover-journal)")" \
  "$(present "$(rooted /var/log/lmm-api-cutover/post-cutover-verify.json)")" \
  "$(present "$(rooted /var/lib/lmm-api-go-deploy/work)")" \
  "$(present "$(rooted /var/lib/lmm-api-go-deploy/staging)")" "$(present "$(rooted /var/lib/lmm-api-go-deploy/backups)")"
)

if [[ $format == kv ]]; then
  for ((index = 0; index < ${#keys[@]}; index += 1)); do
    printf '%s=%q\n' "${keys[index]}" "${values[index]}"
  done
else
  printf '{'
  for ((index = 0; index < ${#keys[@]}; index += 1)); do
    ((index == 0)) || printf ','
    printf '\n  "%s": "%s"' "${keys[index]}" "$(json_escape "${values[index]}")"
  done
  printf '\n}\n'
fi

[[ $host_match == true ]] || exit 3
[[ $db_engine != disagreement ]] || exit 4
[[ $cutover_state == verified || $cutover_state == not_required ]] || exit 4
