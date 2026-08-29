#!/usr/bin/env bash

set -euo pipefail
umask 077

readonly ID_PATTERN='^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$'
readonly SAFE_VALUE_PATTERN='^[A-Za-z0-9][A-Za-z0-9._:+@,-]{0,255}$'
readonly HOST_PATTERN='^[A-Za-z0-9][A-Za-z0-9.-]{0,252}$'
readonly SHA256_PATTERN='^[A-Fa-f0-9]{64}$'
readonly UTC_PATTERN='^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$'

usage() {
  printf 'Usage: %s --role local|test|production --deployment-id ID --copy COPY_ROLE=/absolute/path [--copy ...]\n' "${0##*/}" >&2
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
    [[ ! -L $current ]] || die 'backup path traverses a symbolic link'
    [[ -e $current ]] || die 'backup path does not exist'
  done
}

validate_copy_path() {
  local path=$1
  local canonical

  reject_unsafe_text "$path"
  [[ $path == /* ]] || die 'backup copy path must be absolute'
  canonical=$(realpath -m -- "$path")
  [[ $canonical == "$path" ]] || die 'backup copy path must be canonical'
  case "$canonical" in
    /|/tmp|/tmp/*|/var/tmp|/var/tmp/*)
      die 'backup copy path is too broad or uses a forbidden temporary path'
      ;;
  esac
  assert_no_symlink_components "$canonical"
  [[ -d $canonical && ! -L $canonical ]] || die 'backup copy path is not a real directory'
  printf '%s\n' "$canonical"
}

validate_relative_file() {
  local value=$1
  [[ $value =~ ^[A-Za-z0-9][A-Za-z0-9._+-]{0,255}$ ]] || die 'manifest contains an unsafe archive filename'
  [[ $value != '.' && $value != '..' ]] || die 'manifest contains an unsafe archive filename'
}

normalize_mode() {
  local value=$1
  [[ $value =~ ^0?[0-7]{3}$ ]] || die 'manifest contains an invalid file mode'
  value=${value#0}
  printf '%s\n' "$value"
}

declare -A reference_identity=()
reference_initialized=false

verify_copy() {
  local copy_role=$1
  local directory=$2
  local manifest="$directory/manifest.env"
  local checksums="$directory/SHA256SUMS"
  local line key value
  local checksum separator filename actual_checksum
  local kind file expected_size actual_size expected_mode actual_mode expected_mtime actual_mtime epoch
  local -a archive_kinds=(application frontend configuration database)
  local -a identity_keys=(deployment_id deployment_role verified_host release_id artifact_sha256 git_revision database_engine service_state frontend_release)
  declare -A data=()
  declare -A checksum_by_file=()
  declare -A used_archive_files=()

  [[ -f $manifest && ! -L $manifest ]] || die "copy $copy_role is missing a safe manifest.env"
  [[ -f $checksums && ! -L $checksums ]] || die "copy $copy_role is missing a safe SHA256SUMS"

  while IFS= read -r line || [[ -n $line ]]; do
    [[ -n $line && $line != '#'* ]] || continue
    [[ $line == *=* ]] || die "copy $copy_role has a malformed manifest line"
    key=${line%%=*}
    value=${line#*=}
    [[ $key =~ ^[a-z][a-z0-9_]*$ ]] || die "copy $copy_role has an invalid manifest key"
    [[ ! -v data[$key] ]] || die "copy $copy_role has a duplicate manifest key"
    [[ $value != *$'\n'* && $value != *$'\r'* && $value != *$'\t'* ]] ||
      die "copy $copy_role has a control character in its manifest"
    data[$key]=$value
  done < "$manifest"

  local -a required_keys=(
    format created_at_utc deployment_id copy_role deployment_role verified_host release_id artifact_sha256 git_revision
    database_engine service_state frontend_release
    application_file application_size application_mode application_mtime_utc
    frontend_file frontend_size frontend_mode frontend_mtime_utc
    configuration_file configuration_size configuration_mode configuration_mtime_utc configuration_encrypted
    database_file database_size database_mode database_mtime_utc database_encrypted
  )
  for key in "${required_keys[@]}"; do
    [[ -v data[$key] ]] || die "copy $copy_role manifest is missing required metadata"
  done

  [[ ${data[format]} == 1 ]] || die "copy $copy_role uses an unsupported manifest format"
  [[ ${data[created_at_utc]} =~ $UTC_PATTERN ]] || die "copy $copy_role has an invalid creation timestamp"
  [[ ${data[deployment_id]} == "$deployment_id" ]] || die "copy $copy_role has a different deployment ID"
  [[ ${data[copy_role]} == "$copy_role" ]] || die "copy $copy_role has a mismatched copy role"
  [[ ${data[deployment_role]} == "$role" ]] || die "copy $copy_role has a mismatched deployment role"
  [[ ${data[verified_host]} =~ $HOST_PATTERN ]] || die "copy $copy_role has an invalid verified host"
  [[ ${data[release_id]} =~ $SAFE_VALUE_PATTERN ]] || die "copy $copy_role has an invalid release ID"
  [[ ${data[artifact_sha256]} =~ $SHA256_PATTERN ]] || die "copy $copy_role has an invalid artifact checksum"
  [[ ${data[git_revision]} =~ ^[A-Fa-f0-9]{7,64}$ ]] || die "copy $copy_role has an invalid Git revision"
  case "${data[database_engine]}" in
    sqlite|postgres|mysql) ;;
    *) die "copy $copy_role has an invalid database engine" ;;
  esac
  [[ ${data[service_state]} =~ $SAFE_VALUE_PATTERN ]] || die "copy $copy_role has an invalid service state"
  [[ ${data[frontend_release]} =~ $SAFE_VALUE_PATTERN ]] || die "copy $copy_role has an invalid frontend identity"
  case "${data[configuration_encrypted]}" in true|false) ;; *) die "copy $copy_role has invalid encryption metadata" ;; esac
  case "${data[database_encrypted]}" in true|false) ;; *) die "copy $copy_role has invalid encryption metadata" ;; esac
  if [[ $copy_role == controller || $copy_role == off-host ]]; then
    [[ ${data[configuration_encrypted]} == true && ${data[database_encrypted]} == true ]] ||
      die "copy $copy_role does not mark secret-bearing archives as encrypted"
  fi

  while IFS= read -r line || [[ -n $line ]]; do
    [[ -n $line ]] || continue
    if [[ $line =~ ^([A-Fa-f0-9]{64})[[:space:]]([*\ ]?)([^[:space:]]+)$ ]]; then
      checksum=${BASH_REMATCH[1],,}
      separator=${BASH_REMATCH[2]}
      filename=${BASH_REMATCH[3]}
      : "$separator"
    else
      die "copy $copy_role has a malformed checksum line"
    fi
    validate_relative_file "$filename"
    [[ ! -v checksum_by_file[$filename] ]] || die "copy $copy_role has a duplicate checksum entry"
    checksum_by_file[$filename]=$checksum
  done < "$checksums"

  ((${#checksum_by_file[@]} == 4)) || die "copy $copy_role checksum list must contain exactly four archives"

  for kind in "${archive_kinds[@]}"; do
    filename=${data[${kind}_file]}
    validate_relative_file "$filename"
    [[ ! -v used_archive_files[$filename] ]] || die "copy $copy_role reuses an archive filename"
    used_archive_files[$filename]=true
    [[ -v checksum_by_file[$filename] ]] || die "copy $copy_role is missing an archive checksum"
    file="$directory/$filename"
    [[ -f $file && ! -L $file && -s $file ]] || die "copy $copy_role has a missing, empty, or unsafe archive"

    expected_size=${data[${kind}_size]}
    [[ $expected_size =~ ^[1-9][0-9]*$ ]] || die "copy $copy_role has an invalid archive size"
    actual_size=$(stat -c '%s' -- "$file")
    [[ $actual_size == "$expected_size" ]] || die "copy $copy_role archive size does not match its manifest"

    expected_mode=$(normalize_mode "${data[${kind}_mode]}")
    actual_mode=$(stat -c '%a' -- "$file")
    [[ $actual_mode == "$expected_mode" ]] || die "copy $copy_role archive mode does not match its manifest"

    expected_mtime=${data[${kind}_mtime_utc]}
    [[ $expected_mtime =~ $UTC_PATTERN ]] || die "copy $copy_role has an invalid archive timestamp"
    epoch=$(stat -c '%Y' -- "$file")
    actual_mtime=$(date -u -d "@$epoch" '+%Y-%m-%dT%H:%M:%SZ')
    [[ $actual_mtime == "$expected_mtime" ]] || die "copy $copy_role archive timestamp does not match its manifest"

    actual_checksum=$(sha256sum -- "$file")
    actual_checksum=${actual_checksum%% *}
    [[ ${actual_checksum,,} == "${checksum_by_file[$filename]}" ]] || die "copy $copy_role archive checksum verification failed"
  done

  if [[ $reference_initialized == false ]]; then
    for key in "${identity_keys[@]}"; do
      reference_identity[$key]=${data[$key]}
    done
    reference_initialized=true
  else
    for key in "${identity_keys[@]}"; do
      [[ ${data[$key]} == "${reference_identity[$key]}" ]] || die "copy $copy_role identity disagrees with another copy"
    done
  fi

  printf 'verified_copy=%s\n' "$copy_role"
}

role=''
deployment_id=''
declare -A copies=()
declare -a copy_order=()

while (($# > 0)); do
  case "$1" in
    --role)
      (($# >= 2)) || die 'missing value for --role'
      role=$2
      shift 2
      ;;
    --deployment-id)
      (($# >= 2)) || die 'missing value for --deployment-id'
      deployment_id=$2
      shift 2
      ;;
    --copy)
      (($# >= 2)) || die 'missing value for --copy'
      [[ $2 == *=* ]] || die '--copy must use COPY_ROLE=/absolute/path'
      copy_role=${2%%=*}
      copy_path=${2#*=}
      case "$copy_role" in target|controller|off-host) ;; *) die 'invalid backup copy role' ;; esac
      [[ ! -v copies[$copy_role] ]] || die 'duplicate backup copy role'
      copies[$copy_role]=$(validate_copy_path "$copy_path")
      copy_order+=("$copy_role")
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
  local) required_roles=(controller) ;;
  test) required_roles=(target controller) ;;
  production) required_roles=(target controller off-host) ;;
  *) die 'role must be local, test, or production' ;;
esac
[[ $deployment_id =~ $ID_PATTERN ]] || die 'invalid deployment ID'
for copy_role in "${required_roles[@]}"; do
  [[ -v copies[$copy_role] ]] || die "missing required $copy_role backup copy"
done

for copy_role in "${copy_order[@]}"; do
  verify_copy "$copy_role" "${copies[$copy_role]}"
done
printf 'backup_set=verified\n'
