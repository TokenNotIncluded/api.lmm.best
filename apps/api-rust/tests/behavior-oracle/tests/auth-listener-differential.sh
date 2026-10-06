#!/usr/bin/env bash
# Real-listener authentication differential.  It deliberately does not reuse
# the in-process adapter tests: both implementations are reached over TCP.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
curl_connect_timeout=2
curl_max_time=15
listener_wait_attempts=${LMM_AUTH_LISTENER_WAIT_ATTEMPTS:-1200}
approval_mode=${LMM_AUTH_LISTENER_APPROVAL:-0}
probe_only=${LMM_AUTH_LISTENER_PROBE_ONLY:-0}
expected_scenarios=35
scenario_total=0
exact_matches=0
mismatch_count=0
mismatch_names=()
case "$approval_mode" in 0|1) ;; *) echo 'LMM_AUTH_LISTENER_APPROVAL must be 0 or 1' >&2; exit 2 ;; esac
case "$probe_only" in 0|1) ;; *) echo 'LMM_AUTH_LISTENER_PROBE_ONLY must be 0 or 1' >&2; exit 2 ;; esac
[[ $listener_wait_attempts =~ ^[1-9][0-9]*$ ]] || {
  echo 'LMM_AUTH_LISTENER_WAIT_ATTEMPTS must be a positive integer' >&2
  exit 2
}
if [[ $approval_mode == 1 && $probe_only == 1 ]]; then
  echo 'approval mode refuses probe-only; run the full authenticated listener matrix' >&2
  exit 2
fi

# All listener ports are selected per run.  Optional overrides exist only for
# debugging; every requested port is still checked for uniqueness and vacancy.
allocate_port() {
  local port
  for _ in {1..200}; do
    port=$(shuf -i 20000-55000 -n 1)
    [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] && { printf '%s\n' "$port"; return 0; }
  done
  echo 'unable to allocate an isolated TCP port' >&2
  return 1
}
pg_port=${LMM_AUTH_TEST_PG_PORT:-$(allocate_port)}
go_port=${LMM_AUTH_TEST_GO_PORT:-$(allocate_port)}
rust_port=${LMM_AUTH_TEST_RUST_PORT:-$(allocate_port)}
go_valkey_port=${LMM_AUTH_TEST_GO_VALKEY_PORT:-$(allocate_port)}
rust_valkey_port=${LMM_AUTH_TEST_RUST_VALKEY_PORT:-$(allocate_port)}
# A probe verifies only frozen inputs.  It must not allocate a runtime tree.
runtime=''
go_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
rust_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
go_valkey_config=''
rust_valkey_config=''
auth_gate_database="auth_gate_${BASHPID}_${RANDOM}"
auth_gate_database_created=false
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
go_pid=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
go_pid_start=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
rust_pid=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
rust_pid_start=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
go_valkey_pid=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
go_valkey_pid_start=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
rust_valkey_pid=''
# shellcheck disable=SC2034 # Names are deliberately read through safe indirection.
rust_valkey_pid_start=''
pg_pid=''
# shellcheck disable=SC2034 # Name is deliberately read through safe indirection.
pg_pid_start=''
frozen_go_manifest_sha256=
rust_build_input_sha256=
rust_binary_sha256=

# Every HTTP call, including readiness and negative-path checks, is bounded.
curl() { command curl --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "$@"; }

pid_start_time() { [[ -r /proc/$1/stat ]] && awk '{print $22}' "/proc/$1/stat"; }
record_pid() {
  local pid_name=$1 pid=$2 start
  printf -v "$pid_name" '%s' "$pid"
  start=$(pid_start_time "$pid") || { wait "$pid" 2>/dev/null || true; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; return 1; }
  printf -v "${pid_name}_start" '%s' "$start"
}
record_existing_pid() {
  local pid_name=$1 pid=$2 start
  start=$(pid_start_time "$pid") || return 1
  printf -v "$pid_name" '%s' "$pid"
  printf -v "${pid_name}_start" '%s' "$start"
}
owned_pid_is_live() {
  local pid_name=$1 start_name pid expected_start
  start_name="${pid_name}_start"
  pid=${!pid_name:-}
  expected_start=${!start_name:-}
  [[ -n $pid && -n $expected_start ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected_start" ]]
}
stop_owned_process() {
  local pid_name=$1 pid=${!1:-}
  if [[ -n $pid ]]; then
    if owned_pid_is_live "$pid_name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
    else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi
  fi
  printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''
}
listener_owned_by() {
  local port=$1 expected_pid=$2
  local -a lines pids
  mapfile -t lines < <(ss -H -ltnp "sport = :$port" 2>/dev/null | sed '/^[[:space:]]*$/d' || true)
  (( ${#lines[@]} == 1 )) || return 1
  mapfile -t pids < <(grep -oE 'pid=[0-9]+' <<<"${lines[0]}" | sort -u || true)
  (( ${#pids[@]} == 1 )) || return 1
  [[ ${pids[0]} == "pid=$expected_pid" ]]
}
preflight_port() {
  local name=$1 port=$2
  [[ $port =~ ^[1-9][0-9]{0,4}$ && $port -le 65535 ]] || { echo "invalid $name port: $port" >&2; return 1; }
  [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] || { echo "$name port is occupied: 127.0.0.1:$port" >&2; return 1; }
}
assert_distinct_ports() {
  local a b
  for a in "$@"; do for b in "$@"; do [[ $a == "$b" || ${a#*:} != "${b#*:}" ]] || { echo "ports must be distinct: $a and $b" >&2; return 1; }; done; done
}
valkey_cli_for() {
  local port=$1 password
  shift
  case "$port" in
    "$go_valkey_port") password=$go_valkey_password ;;
    "$rust_valkey_port") password=$rust_valkey_password ;;
    *) echo "unknown isolated Valkey port: $port" >&2; return 2 ;;
  esac
  VALKEYCLI_AUTH="$password" command valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" "$@"
}
write_valkey_config() {
  local config=$1 port=$2 password=$3
  local socket="${config%.conf}.sock"
  (umask 077; printf '%s\n' 'bind 127.0.0.1' "port $port" "unixsocket $socket" 'unixsocketperm 600' 'save ""' 'appendonly no' 'daemonize no' "dir $runtime" "requirepass $password" >"$config")
  chmod 600 "$config"
}
wait_for_listener() {
  local port=$1 path=$2 pid_name=$3 pid attempt
  # Go's disposable migration may take longer on a cold PostgreSQL 18 data
  # directory under concurrent build load. Keep the wait bounded, but do not
  # turn a slow startup into a false zero-scenario differential result.
  for ((attempt = 0; attempt < listener_wait_attempts; attempt++)); do
    owned_pid_is_live "$pid_name" || return 1; pid=${!pid_name}
    if listener_owned_by "$port" "$pid" && curl -fsS "http://127.0.0.1:$port$path" >/dev/null 2>&1; then
      owned_pid_is_live "$pid_name" && listener_owned_by "$port" "$pid" && return 0
    fi
    sleep .05
  done
  return 1
}
wait_for_valkey() {
  local port=$1 pid_name=$2 pid
  for _ in {1..100}; do
    owned_pid_is_live "$pid_name" || return 1; pid=${!pid_name}
    if listener_owned_by "$port" "$pid" && valkey_cli_for "$port" ping >/dev/null 2>&1; then
      owned_pid_is_live "$pid_name" && listener_owned_by "$port" "$pid" && return 0
    fi
    sleep .05
  done
  return 1
}
start_valkey() {
  local name=$1 port=$2 password=$3 config=$4 pid_name=$5
  [[ -z ${!pid_name:-} ]] || { echo "refusing to overwrite PID record: $pid_name" >&2; return 1; }
  write_valkey_config "$config" "$port" "$password"; preflight_port "$name" "$port"
  valkey-server "$config" >"$runtime/$name-valkey.log" 2>&1 & record_pid "$pid_name" "$!"
  wait_for_valkey "$port" "$pid_name"
}
create_owned_auth_gate_database() {
  [[ $auth_gate_database =~ ^auth_gate_[0-9]+_[0-9]+$ ]] || { echo 'invalid auth integration database name' >&2; return 1; }
  [[ $(psql -h 127.0.0.1 -p "$pg_port" -d postgres -At -v ON_ERROR_STOP=1 -c "SELECT COUNT(*) FROM pg_database WHERE datname = '$auth_gate_database'") == 0 ]] || {
    echo "refusing to reuse existing auth integration database: $auth_gate_database" >&2
    return 1
  }
  createdb -h 127.0.0.1 -p "$pg_port" "$auth_gate_database"
  auth_gate_database_created=true
  [[ $(psql -h 127.0.0.1 -p "$pg_port" -d "$auth_gate_database" -At -v ON_ERROR_STOP=1 -c 'SELECT current_database()') == "$auth_gate_database" ]] || {
    echo 'auth integration database ownership verification failed' >&2
    return 1
  }
}
drop_owned_auth_gate_database() {
  [[ $auth_gate_database_created == true ]] || return 0
  [[ $auth_gate_database =~ ^auth_gate_[0-9]+_[0-9]+$ ]] || { echo 'refusing to drop invalid auth integration database name' >&2; return 1; }
  [[ $(psql -h 127.0.0.1 -p "$pg_port" -d postgres -At -v ON_ERROR_STOP=1 -c "SELECT COUNT(*) FROM pg_database WHERE datname = '$auth_gate_database'") == 1 ]] || {
    echo "refusing to drop missing or non-unique auth integration database: $auth_gate_database" >&2
    return 1
  }
  dropdb -h 127.0.0.1 -p "$pg_port" "$auth_gate_database"
  auth_gate_database_created=false
}
record_exact_match() {
  scenario_total=$((scenario_total + 1))
  exact_matches=$((exact_matches + 1))
}
record_mismatch() {
  local name=$1
  scenario_total=$((scenario_total + 1))
  mismatch_count=$((mismatch_count + 1))
  mismatch_names+=("$name")
  echo "authentication parity mismatch: $name" >&2
  return 1
}

cleanup() {
  [[ -n $runtime ]] || return 0
  stop_owned_process go_pid; stop_owned_process rust_pid
  stop_owned_process go_valkey_pid; stop_owned_process rust_valkey_pid
  drop_owned_auth_gate_database || true
  if [[ -n ${pg_pid:-} ]]; then
    if owned_pid_is_live pg_pid; then pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
    else echo "refusing to stop PostgreSQL with an unowned PID (${pg_pid})" >&2; fi
    pg_pid=''
    # shellcheck disable=SC2034 # Name is deliberately read through safe indirection.
    pg_pid_start=''
  fi
  rm -f -- "$go_valkey_config" "$rust_valkey_config"
  case "$runtime" in
    /tmp/lmm-auth-listener.*)
      if [[ ${LMM_KEEP_AUTH_LISTENER_RUNTIME:-0} == 1 ]]; then
        echo "preserved auth listener runtime: $runtime" >&2
      else
        rm -rf "$runtime"
      fi
      ;;
    *) echo "refusing unexpected runtime: $runtime" >&2 ;;
  esac
}
emit_failure_summary() {
  local line=$1 exit_code=$2
  # A full-run mismatch must leave machine-readable accounting even when
  # `set -e` stops at the first failed contract assertion.
  jq -cn \
    --argjson line "$line" \
    --argjson exit_code "$exit_code" \
    --argjson expected_scenarios "$expected_scenarios" \
    --argjson scenarios "$scenario_total" \
    --argjson exact_matches "$exact_matches" \
    --argjson mismatches "$mismatch_count" \
    --arg mismatch_names "${mismatch_names[*]:-}" \
    '{test:"auth-listener-differential",mode:"full",result:"failed",line:$line,exit_code:$exit_code,expected_scenarios:$expected_scenarios,scenarios:$scenarios,exact_matches:$exact_matches,mismatches:$mismatches,mismatch_names:$mismatch_names}' >&2 || true
}
trap cleanup EXIT INT TERM
trap 'emit_failure_summary "$LINENO" "$?"' ERR

for command in cargo curl dropdb git go initdb jq pg_ctl postgres psql createdb valkey-cli valkey-server od shuf ss sha256sum; do
  command -v "$command" >/dev/null || { echo "required command is unavailable: $command" >&2; exit 1; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
assert_distinct_ports "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port"
for item in "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port"; do preflight_port "${item%%:*}" "${item##*:}"; done

assert_frozen_inputs() {
  [[ -d $legacy_root && ${legacy_root##*/} == "$legacy_revision" && -f $legacy_root/SHA256SUMS && -f $legacy_root/GIT-LS-FILES-S.tsv ]] || { echo 'frozen Go archive or manifest missing' >&2; return 1; }
  (cd "$legacy_root" && sha256sum --check --status SHA256SUMS) || { echo 'frozen Go content hash verification failed' >&2; return 1; }
  frozen_go_manifest_sha256=$(sha256sum "$legacy_root/SHA256SUMS" "$legacy_root/GIT-LS-FILES-S.tsv" | sha256sum | awk '{print $1}')
  rust_build_input_sha256=$(rust_build_input_manifest_sha256)
  [[ $frozen_go_manifest_sha256 =~ ^[[:xdigit:]]{64}$ && $rust_build_input_sha256 =~ ^[[:xdigit:]]{64}$ ]]
}
rust_build_input_manifest() {
  # `find`, rather than Git's index, intentionally includes untracked source
  # inputs.  This catches a local edit racing the oracle as well as ordinary
  # Cargo workspace changes. The runner and its cited ignored integration test
  # are explicit scenario inputs even though neither belongs under app/src.
  (
    cd "$repo_root"
    {
      printf '%s\0' \
        apps/api-rust/Cargo.toml \
        apps/api-rust/Cargo.lock \
        apps/api-rust/Cargo.toml \
        apps/api-rust/tests/auth_pg_valkey.rs \
        apps/api-rust/tests/behavior-oracle/tests/auth-listener-differential.sh
      find apps/api-rust/src apps/api-rust/assets apps/api-rust/crates -type f -print0
    } | LC_ALL=C sort -z | xargs -0 -r sha256sum
  )
}
rust_build_input_manifest_sha256() { rust_build_input_manifest | sha256sum | awk '{print $1}'; }
assert_rust_build_input_manifest_coverage() {
  local manifest input
  manifest=$(rust_build_input_manifest)
  for input in apps/api-rust/tests/auth_pg_valkey.rs apps/api-rust/tests/behavior-oracle/tests/auth-listener-differential.sh; do
    grep -Fq "  $input" <<<"$manifest" || { echo "Rust build-input manifest omits required scenario input: $input" >&2; return 1; }
  done
}
assert_rust_build_input_manifest_coverage
assert_frozen_inputs
if [[ $probe_only == 1 ]]; then
  jq -cn --arg legacy_revision "$legacy_revision" --arg frozen_go_manifest_sha256 "$frozen_go_manifest_sha256" --arg rust_build_input_sha256 "$rust_build_input_sha256" '{test:"auth-listener-differential",mode:"probe",approval:false,legacy_revision:$legacy_revision,frozen_go_manifest_sha256:$frozen_go_manifest_sha256,rust_build_input_sha256:$rust_build_input_sha256,result:"passed"}'
  exit 0
fi
runtime=$(mktemp -d /tmp/lmm-auth-listener.XXXXXX)
go_valkey_config="$runtime/go-valkey.conf"
rust_valkey_config="$runtime/rust-valkey.conf"

normalize_json() {
  jq -S '
    del(.data.access_token)
    | if .data.access_expires_at? then .data.access_expires_at = "<EPOCH>" else . end
    | if .data.session? then .data.session.sid = "<SID>" else . end
    | if .data.session? then .data.session.created_at = "<EPOCH>" else . end
    | if .data.session? then .data.session.last_active_at = "<EPOCH>" else . end
    | if .data.session? then .data.session.expires_at = "<EPOCH>" else . end
    | if .data.flow_token? then .data.flow_token = "<FLOW_TOKEN>" else . end
    | if .data.expires_at? then .data.expires_at = "<EPOCH>" else . end
    | if .data.revoked_sid? then .data.revoked_sid = "<SID>" else . end
  ' "$1"
}

normalize_headers() {
  awk '
    BEGIN { IGNORECASE = 1 }
    /^[^:]+:/ {
      name = tolower($1)
      sub(/^[^:]+:[[:space:]]*/, "")
      value = $0
      sub(/\r$/, "", value)
      if (name == "x-oneapi-request-id:") value = "<REQUEST_ID>"
      if (name == "retry-after:") value = "<RETRY_AFTER>"
      if (name == "set-cookie:") {
        sub(/new_api_refresh=[^;]+/, "new_api_refresh=<REFRESH_TOKEN>", value)
        sub(/Expires=[^;]+/, "Expires=<EXPIRY>", value)
        sub(/Max-Age=[0-9]+/, "Max-Age=<MAX_AGE>", value)
      }
      headers[name] = value
    }
    END {
      split("auth-version: cache-control: content-type: expires: pragma: retry-after: set-cookie: x-new-api-version: x-oneapi-request-id:", ordered, " ")
      for (i in ordered) {
        name = ordered[i]
        if (name in headers) print name " " headers[name]
      }
    }
  ' "$1"
}

assert_listener_response_match() {
  local name=$1
  local go_prefix="$runtime/127.0.0.1:$go_port.$name"
  local rust_prefix="$runtime/127.0.0.1:$rust_port.$name"
  if diff -u "$go_prefix.status" "$rust_prefix.status" &&
    diff -u <(normalize_json "$go_prefix.json") <(normalize_json "$rust_prefix.json") &&
    diff -u <(normalize_headers "$go_prefix.headers") <(normalize_headers "$rust_prefix.headers"); then
    record_exact_match
  else
    record_mismatch "$name"
  fi
}

# The two real TCP listeners schedule the racing requests independently.  A
# request label (a or b) is therefore not a cross-process observable. Compare
# the pair by role instead: exactly one winner returns its own user-agent and
# the other returns the login snapshot. This preserves full body/header
# comparison without making a false claim that the same curl process won both
# database races.
canonicalize_refresh_pair() {
  local prefix=$1
  local race request_ua response_ua role path
  for race in a b; do
    request_ua=$([[ $race == a ]] && echo curl || echo race-b)
    path="$prefix.refresh-race-$race"
    response_ua=$(jq -r '.data.session.user_agent' "$path.json")
    if [[ $response_ua == "$request_ua" ]]; then
      role=winner
    elif [[ $response_ua == login-agent ]]; then
      role=loser
    else
      echo "unexpected refresh user-agent: $response_ua" >&2
      return 1
    fi
    printf '%s status ' "$role"
    cat "$path.status"
    printf '%s body ' "$role"
    normalize_json "$path.json" | jq -S --arg role "<$role>" '.data.session.user_agent = $role'
    printf '%s headers\n' "$role"
    normalize_headers "$path.headers"
  done | sort
}

assert_listener_refresh_pair_match() {
  local schedule=$1
  local go_prefix="$runtime/127.0.0.1:$go_port.$schedule"
  local rust_prefix="$runtime/127.0.0.1:$rust_port.$schedule"
  if diff -u <(canonicalize_refresh_pair "$go_prefix") <(canonicalize_refresh_pair "$rust_prefix"); then
    record_exact_match
  else
    record_mismatch "$schedule refresh-pair"
  fi
}

assert_named_file_match() {
  local name=$1 left=$2 right=$3
  if diff -u "$left" "$right"; then
    record_exact_match
  else
    record_mismatch "$name"
  fi
}

capture_listener_response() {
  local prefix=$1
  shift
  curl -sS -D "$prefix.headers" -o "$prefix.json" -w '%{http_code}' "$@" >"$prefix.status"
}

# A 2FA challenge must create only short-lived durable auth-flow state.  The
# session cache has no 2FA-flow entry, so its stable hash must remain unchanged.
snapshot_non_flow_business_tables() {
  local database=$1 output=$2 table
  : >"$output"
  # These tables are shared by the frozen Go schema and the Rust listener.
  for table in users user_sessions two_fas two_fa_backup_codes casbin_rule tokens options custom_oauth_providers setups; do
    printf '%s\t' "$table" >>"$output"
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
      -c "SELECT encode(convert_to(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY row_to_json(t)::text), ''), 'UTF8'), 'base64') FROM (SELECT * FROM $table) AS t" >>"$output"
  done
}
assert_and_summarize_common_business_tables() {
  local database=$1 before=$2 after=$3 summary=$4 table
  : >"$summary"
  for table in users user_sessions two_fas two_fa_backup_codes casbin_rule tokens options custom_oauth_providers setups; do
    # Each table is independently strict.  Initial seeds and schema-specific
    # row shapes may differ across engines, but a 2FA challenge must not alter
    # any of these shared business tables on either engine.
    if diff -u \
      <(awk -F '\t' -v table="$table" '$1 == table { print; found = 1 } END { exit(found ? 0 : 1) }' "$before") \
      <(awk -F '\t' -v table="$table" '$1 == table { print; found = 1 } END { exit(found ? 0 : 1) }' "$after"); then
      printf '%s\tunchanged\n' "$table" >>"$summary"
    else
      echo "unexpected 2FA mutation in $database.$table" >&2
      return 1
    fi
  done
}
snapshot_schema_contract_capability() {
  local database=$1 output=$2
  if [[ $database == auth_go ]]; then
    # Go intentionally has no Rust reader-contract table.  Record that fact
    # rather than issuing an invalid query or treating it as a side effect.
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
      -c "SELECT CASE WHEN to_regclass('public.lmm_schema_contract') IS NULL THEN 'expected-absent' ELSE 'unexpected-present' END" >"$output"
  else
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
      -c "SELECT COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY row_to_json(t)::text), '') FROM (SELECT * FROM lmm_schema_contract) AS t" >"$output"
  fi
}
snapshot_auth_session_valkey() {
  local port=$1 output=$2 key
  : >"$output"
  while IFS= read -r key; do
    [[ -n $key ]] || continue
    printf '%s\n' "$key" >>"$output"
    valkey_cli_for "$port" --raw hgetall "$key" | paste - - | LC_ALL=C sort >>"$output"
  done < <(valkey_cli_for "$port" --scan --pattern 'auth:session:*' | LC_ALL=C sort)
}
invalidate_fixture_user_cache() {
  local port=$1 label=$2 before after deleted
  if [[ $label == /* ]]; then
    before="$label.before-non-user-keys"
    after="$label.after-non-user-keys"
  else
    before="$runtime/$label.before-non-user-keys"
    after="$runtime/$label.after-non-user-keys"
  fi
  # Direct SQL fixtures bypass each implementation's application invalidator.
  # Delete only the authoritative per-user cache entry, then prove no other
  # durable Valkey key was touched before taking the request-side-effect
  # baseline. Fixed-window limiter keys are deliberately excluded because
  # their short TTL may expire between these two snapshots without a request
  # mutating application state.
  valkey_cli_for "$port" --scan |
    awk '$0 != "user:1" && $0 !~ /^rateLimit:v2:/' |
    LC_ALL=C sort >"$before"
  deleted=$(valkey_cli_for "$port" del user:1)
  [[ $deleted == 0 || $deleted == 1 ]] || { echo "unexpected user cache DEL result: $deleted" >&2; return 1; }
  [[ $(valkey_cli_for "$port" exists user:1) == 0 ]] || { echo 'fixture user cache survives invalidation' >&2; return 1; }
  valkey_cli_for "$port" --scan |
    awk '$0 != "user:1" && $0 !~ /^rateLimit:v2:/' |
    LC_ALL=C sort >"$after"
  diff -u "$before" "$after"
}
snapshot_two_factor_flow_contract() {
  local database=$1 output=$2
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
    -c "SELECT json_build_object('count', COUNT(*), 'all_pending_2fa', COALESCE(bool_and(purpose = '2fa_login' AND user_id = 1 AND consumed_at IS NULL AND expires_at > NOW() AND COALESCE(payload, '') <> ''), FALSE)) FROM auth_flows" | jq -S . >"$output"
}
snapshot_auth_flow_rows() {
  local database=$1 output=$2
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
    -c "SELECT encode(convert_to(COALESCE(string_agg(row_to_json(t)::text, E'\\n' ORDER BY row_to_json(t)::text), ''), 'UTF8'), 'base64') FROM (SELECT * FROM auth_flows) AS t" >"$output"
}
snapshot_self_policy_side_effects() {
  local database=$1 valkey_port=$2 prefix=$3
  snapshot_non_flow_business_tables "$database" "$prefix.business"
  snapshot_auth_flow_rows "$database" "$prefix.auth-flows"
  snapshot_auth_session_valkey "$valkey_port" "$prefix.auth-session-valkey"
}
assert_self_policy_side_effects_unchanged() {
  local before=$1 after=$2
  diff -u "$before.business" "$after.business"
  diff -u "$before.auth-flows" "$after.auth-flows"
  diff -u "$before.auth-session-valkey" "$after.auth-session-valkey"
}
assert_self_error_contract() {
  local prefix=$1 status=$2 code=$3
  grep -qx "$status" "$prefix.status"
  jq -e --arg code "$code" '.success == false and .code == $code and (has("data") | not)' "$prefix.json" >/dev/null
}

cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
rust_binary_sha256=$(sha256sum "$repo_root/apps/api-rust/target/debug/lmm-api-rs" | awk '{print $1}')
[[ $rust_binary_sha256 =~ ^[[:xdigit:]]{64}$ ]] || { echo 'Rust binary hashing failed' >&2; exit 1; }
# The ignored legacy backup intentionally excludes the generated frontend.
# Build the disposable Go listener from a copied source tree so this oracle
# never modifies the preserved backup nor depends on a developer's web build.
cp -a "$legacy_root/." "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: >"$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  # The frozen source is intentionally copied without `.git`; disabling VCS
  # stamping preserves the original build inputs instead of probing host state.
  GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .
)
initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
preflight_port PostgreSQL "$pg_port"
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
record_existing_pid pg_pid "$(head -n 1 "$runtime/pg/postmaster.pid")"
listener_owned_by "$pg_port" "$pg_pid" || { echo 'PostgreSQL listener ownership check failed' >&2; exit 1; }
createdb -h 127.0.0.1 -p "$pg_port" auth_go
createdb -h 127.0.0.1 -p "$pg_port" auth_rust
start_valkey go "$go_valkey_port" "$go_valkey_password" "$go_valkey_config" go_valkey_pid
start_valkey rust "$rust_valkey_port" "$rust_valkey_password" "$rust_valkey_config" rust_valkey_pid

# The ignored integration owns a third, random database.  It must never reset
# or create tables in the Rust listener database that this runner provisions.
create_owned_auth_gate_database
LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1 \
  LMM_AUTH_TEST_DATABASE_URL="postgresql://127.0.0.1:$pg_port/$auth_gate_database" \
  LMM_AUTH_TEST_VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port" \
  cargo test --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --test auth_pg_valkey --locked -- --ignored --test-threads=1
drop_owned_auth_gate_database
# The integration test intentionally creates several sessions in the Rust-only
# Valkey instance.  It is a fixture-only cache, so reset it before listeners.
valkey_cli_for "$rust_valkey_port" flushdb >/dev/null

# The Go listener owns its own migration; PostgreSQL, not SQLite, is the oracle
# datastore.  Rust receives the same production-shaped tables and a least-
# privilege runtime role.  The temporary superuser only provisions the fixture.
# The frozen Go limiter setting is minutes; Rust's equivalent setting is
# seconds, so the two listeners use 1 and 60 respectively.
# pgx's TCP connect can remain in EINPROGRESS on this host even while the
# marker-owned PostgreSQL listener accepts psql connections.  Keep the
# database loopback-only, but use the runtime-owned Unix socket for the Go
# oracle; the Rust listener and all side-effect snapshots still use the same
# PostgreSQL instance and TCP endpoint.  Go-Redis has the same host-specific
# TCP stall, so its client uses the marker-owned Valkey Unix socket while the
# Rust client and CLI probes continue to use loopback TCP.
preflight_port Go_HTTP "$go_port"
SQL_DSN="postgresql:///auth_go?host=$runtime&port=$pg_port&sslmode=disable" PORT="$go_port" \
  REDIS_CONN_STRING="unix://:$go_valkey_password@$runtime/go-valkey.sock" SESSION_SECRET='AuthListener-2026!FixedSyntheticSecret' \
  GLOBAL_API_RATE_LIMIT_ENABLE=true GLOBAL_API_RATE_LIMIT=360 GLOBAL_API_RATE_LIMIT_DURATION=1 \
  SESSION_COOKIE_SECURE=true SESSION_COOKIE_TRUSTED_URL=https://trusted.example \
  PASSWORD_LOGIN_ENABLED=true GIN_MODE=release \
  "$runtime/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
wait_for_listener "$go_port" /api/status go_pid

# Go's migration is the schema source for the disposable legacy database.  We
# provision the Rust schema explicitly so the harness also detects missing
# auth tables and permissions before sending a request.  The normal Rust
# listener also mounts the open-source bounty read surface, and /readyz
# verifies its four core tables before serving any auth request.  Reuse the
# canonical forward migration here so this TCP fixture cannot drift from the
# mounted schema.
sed 's/__LMM_APP_SCHEMA__/public/g' \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" |
  psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -v ON_ERROR_STOP=1 >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE ROLE lmm_auth_runtime LOGIN;
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
-- Empty subscription surface for the normal listener readiness probes only.
CREATE TABLE user_subscriptions (
  amount_total BIGINT, amount_used BIGINT,
  reset_amount BIGINT, renewal_amount BIGINT
);
GRANT SELECT, UPDATE ON user_subscriptions TO lmm_auth_runtime;
CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE custom_oauth_providers (
  id BIGINT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL, icon TEXT,
  enabled BOOLEAN, client_id TEXT, authorization_endpoint TEXT, scopes TEXT
);
CREATE TABLE setups (id BIGINT PRIMARY KEY);
CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT UNIQUE, password TEXT NOT NULL, display_name TEXT, role BIGINT DEFAULT 1, status BIGINT DEFAULT 1, email TEXT, github_id TEXT, discord_id TEXT, oidc_id TEXT, wechat_id TEXT, telegram_id TEXT, access_token TEXT, quota BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0, "group" TEXT DEFAULT 'default', aff_code TEXT, aff_count BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0, aff_history BIGINT DEFAULT 0, inviter_id BIGINT, deleted_at TIMESTAMPTZ, linux_do_id TEXT, setting TEXT DEFAULT '{}', stripe_customer TEXT, last_login_at BIGINT DEFAULT 0, auth_version BIGINT NOT NULL DEFAULT 1, console_activated_at BIGINT NOT NULL DEFAULT 0);
CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT NOT NULL, version BIGINT NOT NULL, user_auth_version BIGINT NOT NULL, status TEXT NOT NULL, refresh_hash CHAR(64) NOT NULL, previous_refresh_hash TEXT, previous_valid_until BIGINT NOT NULL DEFAULT 0, login_method TEXT NOT NULL, ip TEXT, user_agent TEXT, created_at BIGINT NOT NULL, last_active_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revoked_at BIGINT NOT NULL DEFAULT 0, revoked_reason TEXT);
CREATE TABLE two_fas (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL, secret TEXT NOT NULL, is_enabled BOOLEAN NOT NULL DEFAULT FALSE, failed_attempts BIGINT DEFAULT 0, locked_until TIMESTAMPTZ, last_used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
CREATE TABLE casbin_rule (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT);
CREATE TABLE auth_flows (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, token_hash CHAR(64) NOT NULL UNIQUE, purpose TEXT NOT NULL, provider TEXT, intent TEXT, user_id BIGINT, session_id TEXT, payload TEXT, created_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ);
CREATE TABLE two_fa_backup_codes (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL, code_hash TEXT NOT NULL, is_used BOOLEAN DEFAULT FALSE, used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
CREATE SEQUENCE tokens_id_seq;
CREATE TABLE tokens (id BIGINT PRIMARY KEY DEFAULT nextval('tokens_id_seq'), user_id BIGINT NOT NULL, key VARCHAR(128) UNIQUE, status INTEGER DEFAULT 1, name TEXT DEFAULT '', created_time BIGINT DEFAULT 0, accessed_time BIGINT DEFAULT 0, expired_time BIGINT DEFAULT -1, remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN DEFAULT FALSE, model_limits_enabled BOOLEAN DEFAULT FALSE, model_limits TEXT, allow_ips TEXT DEFAULT '', used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN DEFAULT FALSE, deleted_at TIMESTAMPTZ);
ALTER SEQUENCE tokens_id_seq OWNED BY tokens.id;
GRANT USAGE ON SCHEMA public TO lmm_auth_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON options, custom_oauth_providers, setups, users, user_sessions, two_fas, casbin_rule, auth_flows, two_fa_backup_codes, lmm_schema_contract TO lmm_auth_runtime;
GRANT SELECT, INSERT, UPDATE, DELETE ON tokens TO lmm_auth_runtime;
-- Readiness validates these mounted bounty tables before the auth matrix runs.
-- Keep this fixture least-privilege: the auth differential never exercises
-- bounty mutations, so it must not grant their write path accidentally.
GRANT SELECT ON open_source_bounty_projects, open_source_bounty_challenges,
  open_source_bounty_ledgers, open_source_bounty_disputes TO lmm_auth_runtime;
GRANT USAGE ON SEQUENCE auth_flows_id_seq, tokens_id_seq TO lmm_auth_runtime;
SQL
# The gateway no longer bootstraps a root account during startup.  Seed the same
# synthetic, well-known bcrypt fixture on both disposable PostgreSQL databases.
# This hash is only for the `password` test credential and is never emitted.
# shellcheck disable=SC2016 # bcrypt contains literal dollar signs.
root_hash='$2a$10$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2'
psql -h 127.0.0.1 -p "$pg_port" -d auth_go -v ON_ERROR_STOP=1 \
  -c "INSERT INTO users (username, password, display_name, role, status, \"group\", setting, auth_version, quota) VALUES ('root', '$root_hash', 'root', 100, 1, 'default', '{}', 1, 100000000)" >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -v ON_ERROR_STOP=1 \
  -c "INSERT INTO users (id, username, password, display_name, role, status, email, \"group\", setting, auth_version, quota) VALUES (1, 'root', '$root_hash', 'root', 100, 1, '', 'default', '{}', 1, 100000000)" >/dev/null

preflight_port Rust_HTTP "$rust_port"
DATABASE_URL="postgresql://lmm_auth_runtime@127.0.0.1:$pg_port/auth_rust" VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port" \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=blue LMM_SCHEMA_CONTRACT=1 SESSION_SECRET='AuthListener-2026!FixedSyntheticSecret' \
  PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=true GLOBAL_API_RATE_LIMIT=360 GLOBAL_API_RATE_LIMIT_DURATION=60 \
  CRITICAL_RATE_LIMIT_ENABLE=false AUTH_COOKIE_SECURE=true SESSION_COOKIE_TRUSTED_URL=https://trusted.example VERSION=v0.0.0 \
  "$repo_root/apps/api-rust/target/debug/lmm-api-rs" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"
wait_for_listener "$rust_port" /readyz rust_pid

# Login input errors and anonymous auth failures are comparable immediately.
# Captures are retained only when explicitly requested, never from production.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  prefix="$runtime/$(basename "$base")"
  capture_listener_response "$prefix.input" -H 'content-type: application/json' -d '{}' "$base/api/user/login"
  grep -qx 200 "$prefix.input.status"
  capture_listener_response "$prefix.failure" -H 'content-type: application/json' -d '{"username":"missing","password":"wrong"}' "$base/api/user/login"
  grep -qx 200 "$prefix.failure.status"
  capture_listener_response "$prefix.anonymous-self" "$base/api/user/self"
  grep -qx 401 "$prefix.anonymous-self.status"
  capture_listener_response "$prefix.anonymous-refresh" -X POST -H 'origin: https://trusted.example' "$base/api/user/auth/refresh"
  grep -qx 401 "$prefix.anonymous-refresh.status"
  capture_listener_response "$prefix.anonymous-logout" -X POST -H 'origin: https://trusted.example' "$base/api/user/auth/logout"
  grep -qx 200 "$prefix.anonymous-logout.status"
  # The account-security and personal-token routes are now composed by the
  # normal Rust listener. Compare their anonymous contracts over TCP instead
  # of treating their deliberate pre-migration 404s as the expected result.
  capture_listener_response "$prefix.login-2fa" -X POST "$base/api/user/login/2fa"
  capture_listener_response "$prefix.token" "$base/api/user/token"
done
for name in input failure anonymous-self anonymous-refresh anonymous-logout login-2fa token; do
  assert_listener_response_match "$name"
done

# Exercise the successful listener lifecycle on both sides.  Tokens and SIDs
# are intentionally random, so compare their observable shape while checking
# the Rust authoritative row and Valkey tombstone directly.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  for schedule in a-first b-first; do
    prefix="$runtime/$(basename "$base").$schedule"
    capture_listener_response "$prefix.login" -H 'user-agent: login-agent' -H 'content-type: application/json' \
      -d '{"username":"root","password":"password"}' "$base/api/user/login"
    grep -qx 200 "$prefix.login.status"
    jq -e '.success == true and (.data.access_token | type == "string") and (.data.session.sid | type == "string")' "$prefix.login.json" >/dev/null
    token=$(jq -r '.data.access_token' "$prefix.login.json"); sid=$(jq -r '.data.session.sid' "$prefix.login.json")
    cookie=$(awk 'tolower($1) == "set-cookie:" {print $2; exit}' "$prefix.login.headers" | tr -d '\r')
    capture_listener_response "$prefix.self" -H "authorization: Bearer $token" "$base/api/user/self"
    grep -qx 200 "$prefix.self.status"
    jq -e '.success == true and .data.username == "root"' "$prefix.self.json" >/dev/null
    capture_listener_response "$prefix.origin-missing" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" "$base/api/user/auth/refresh"
    capture_listener_response "$prefix.origin-evil" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" -H 'origin: https://evil.example' "$base/api/user/auth/refresh"
    for origin_case in missing evil; do
      grep -qx 403 "$prefix.origin-$origin_case.status"
      jq -e '.success == false and .code == "AUTH_ORIGIN_FORBIDDEN"' "$prefix.origin-$origin_case.json" >/dev/null
      if rg -qi '^(cache-control|expires|pragma):' "$prefix.origin-$origin_case.headers"; then
        echo "origin rejection unexpectedly included cache-control headers" >&2
        exit 1
      fi
    done
    if [[ $schedule == a-first ]]; then
      capture_listener_response "$prefix.refresh-race-a" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" -H 'origin: https://trusted.example' -H 'user-agent: curl' "$base/api/user/auth/refresh" & refresh_a_pid=$!
      capture_listener_response "$prefix.refresh-race-b" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" -H 'origin: https://trusted.example' -H 'user-agent: race-b' "$base/api/user/auth/refresh" & refresh_b_pid=$!
    else
      capture_listener_response "$prefix.refresh-race-b" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" -H 'origin: https://trusted.example' -H 'user-agent: race-b' "$base/api/user/auth/refresh" & refresh_b_pid=$!
      capture_listener_response "$prefix.refresh-race-a" -X POST -H "cookie: $cookie" -H "x-auth-session: $sid" -H 'origin: https://trusted.example' -H 'user-agent: curl' "$base/api/user/auth/refresh" & refresh_a_pid=$!
    fi
    wait "$refresh_a_pid" "$refresh_b_pid"
    winner=""
    for race in a b; do
      request_ua=$([[ $race == a ]] && echo curl || echo race-b)
      grep -qx 200 "$prefix.refresh-race-$race.status"
      jq -e '.success == true and (.data.session.sid == $sid)' --arg sid "$sid" "$prefix.refresh-race-$race.json" >/dev/null
      response_ua=$(jq -r '.data.session.user_agent' "$prefix.refresh-race-$race.json")
      if [[ $response_ua == "$request_ua" ]]; then
        [[ -z $winner ]] || { echo "two refresh winners" >&2; exit 1; }
        winner=$race
      else
        # Frozen Go sends its pre-rotation/login snapshot for a CAS loser.
        [[ $response_ua == login-agent ]] || { echo "unexpected loser user-agent: $response_ua" >&2; exit 1; }
      fi
  done
  [[ -n $winner ]] || { echo "no refresh winner" >&2; exit 1; }
  # Both listeners preserve login-row metadata on refresh CAS; only the winner
  # response reflects request metadata.  Keep this invariant explicit for
  # durability checks.
  if [[ $base == *":$go_port" ]]; then database=auth_go; valkey_port=$go_valkey_port
  else database=auth_rust; valkey_port=$rust_valkey_port; fi
  persisted_user_agent=login-agent
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -Atc "SELECT status || ':' || user_agent FROM user_sessions WHERE sid = '$sid'" | grep -qx "active:$persisted_user_agent"
    active_cache_key=""
    while IFS= read -r cache_key; do
      [[ -z $cache_key ]] && continue
      if [[ $(valkey_cli_for "$valkey_port" hget "$cache_key" Status) == active ]]; then
        [[ -z $active_cache_key ]] || { echo "multiple active session cache keys" >&2; exit 1; }
        active_cache_key=$cache_key
      fi
    done < <(valkey_cli_for "$valkey_port" --scan --pattern 'auth:session:*')
    [[ -n $active_cache_key ]] || { echo "missing active session cache key" >&2; exit 1; }
    valkey_cli_for "$valkey_port" hget "$active_cache_key" UserAgent | grep -qx "$persisted_user_agent"
    rotated_token=$(jq -r '.data.access_token' "$prefix.refresh-race-$winner.json")
    rotated_cookie=$(awk 'tolower($1) == "set-cookie:" {print $2; exit}' "$prefix.refresh-race-$winner.headers" | tr -d '\r')
    capture_listener_response "$prefix.logout" -X POST -H "authorization: Bearer $rotated_token" -H "cookie: $rotated_cookie" -H "x-auth-session: $sid" -H 'origin: https://trusted.example' "$base/api/user/auth/logout"
    grep -qx 200 "$prefix.logout.status"
    jq -e '.success == true' "$prefix.logout.json" >/dev/null
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -Atc "SELECT status FROM user_sessions WHERE sid = '$sid'" | grep -qx revoked
  done
done
for schedule in a-first b-first; do
  for name in login self logout; do
    assert_listener_response_match "$schedule.$name"
  done
  assert_listener_refresh_pair_match "$schedule"
  for name in origin-missing origin-evil; do
    assert_listener_response_match "$schedule.$name"
  done
done

# Expire the just-rotated old token in the real database, then prove a replay
# revokes the family over each TCP listener.  This avoids a wall-clock sleep
# while exercising the same expired-grace branch as production.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  prefix="$runtime/$(basename "$base").replay"
  capture_listener_response "$prefix.login" -H 'user-agent: login-agent' -H 'content-type: application/json' \
    -d '{"username":"root","password":"password"}' "$base/api/user/login"
  grep -qx 200 "$prefix.login.status"
  replay_sid=$(jq -r '.data.session.sid' "$prefix.login.json")
  replay_cookie=$(awk 'tolower($1) == "set-cookie:" {print $2; exit}' "$prefix.login.headers" | tr -d '\r')
  capture_listener_response "$prefix.rotate" -X POST -H "cookie: $replay_cookie" -H "x-auth-session: $replay_sid" -H 'origin: https://trusted.example' "$base/api/user/auth/refresh"
  grep -qx 200 "$prefix.rotate.status"
  if [[ $base == *":$go_port" ]]; then database=auth_go; else database=auth_rust; fi
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "UPDATE user_sessions SET previous_valid_until = 0 WHERE sid = '$replay_sid'" >/dev/null
  capture_listener_response "$prefix.expired-replay" -X POST -H "cookie: $replay_cookie" -H "x-auth-session: $replay_sid" -H 'origin: https://trusted.example' "$base/api/user/auth/refresh"
  grep -qx 401 "$prefix.expired-replay.status"
  jq -e '.success == false and .code == "AUTH_SESSION_REVOKED"' "$prefix.expired-replay.json" >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -Atc "SELECT status || ':' || revoked_reason FROM user_sessions WHERE sid = '$replay_sid'" | grep -qx 'revoked:refresh_reuse'
done
for name in replay.login replay.rotate replay.expired-replay; do
  assert_listener_response_match "$name"
done

# Login's 2FA challenge is localized by legacy Accept-Language negotiation.
# Enable a matching factor after the ordinary browser lifecycle so this exact
# message comparison does not alter its session side effects.
for database in auth_go auth_rust; do
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "INSERT INTO two_fas (user_id, secret, is_enabled) VALUES (1, 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP', TRUE)" >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
    -c "SELECT COUNT(*) FROM two_fas WHERE user_id = 1 AND is_enabled = TRUE AND deleted_at IS NULL" | grep -qx 1
  snapshot_non_flow_business_tables "$database" "$runtime/$database.before-2fa-business"
  snapshot_schema_contract_capability "$database" "$runtime/$database.before-2fa-schema-contract"
done
snapshot_auth_session_valkey "$go_valkey_port" "$runtime/go.before-2fa-auth-session-valkey"
snapshot_auth_session_valkey "$rust_valkey_port" "$runtime/rust.before-2fa-auth-session-valkey"
for locale in en zh-CN zh-TW; do
  case "$locale" in
    en) expected_message='Please enter two-factor authentication code' ;;
    zh-CN) expected_message='请输入两步验证码' ;;
    zh-TW) expected_message='請輸入雙重驗證碼' ;;
  esac
  for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
    prefix="$runtime/$(basename "$base").locale-2fa-$locale"
    capture_listener_response "$prefix" -H "accept-language: $locale" -H 'content-type: application/json' \
      -d '{"username":"root","password":"password"}' "$base/api/user/login"
    grep -qx 200 "$prefix.status"
    jq -e --arg message "$expected_message" \
      '.success == true and .message == $message and .data.require_2fa == true and (.data.flow_token | type == "string")' \
      "$prefix.json" >/dev/null
  done
  assert_listener_response_match "locale-2fa-$locale"
done

# These challenges must create exactly three pending durable 2FA flows (one
# per locale probe), leave every unrelated business table untouched, and leave
# the session cache untouched.  `auth_flows` is the durable 2FA transport;
# session-Valkey is deliberately not used for flow tokens.
for database in auth_go auth_rust; do
  snapshot_two_factor_flow_contract "$database" "$runtime/$database.2fa-flow-contract.json"
  snapshot_non_flow_business_tables "$database" "$runtime/$database.after-2fa-business"
  assert_and_summarize_common_business_tables "$database" "$runtime/$database.before-2fa-business" "$runtime/$database.after-2fa-business" "$runtime/$database.2fa-business-delta.tsv"
  snapshot_schema_contract_capability "$database" "$runtime/$database.after-2fa-schema-contract"
  diff -u "$runtime/$database.before-2fa-schema-contract" "$runtime/$database.after-2fa-schema-contract"
done
grep -qx expected-absent "$runtime/auth_go.before-2fa-schema-contract"
grep -qx expected-absent "$runtime/auth_go.after-2fa-schema-contract"
assert_named_file_match two-factor-common-business-side-effects "$runtime/auth_go.2fa-business-delta.tsv" "$runtime/auth_rust.2fa-business-delta.tsv"
snapshot_auth_session_valkey "$go_valkey_port" "$runtime/go.after-2fa-auth-session-valkey"
snapshot_auth_session_valkey "$rust_valkey_port" "$runtime/rust.after-2fa-auth-session-valkey"
diff -u "$runtime/go.before-2fa-auth-session-valkey" "$runtime/go.after-2fa-auth-session-valkey"
diff -u "$runtime/rust.before-2fa-auth-session-valkey" "$runtime/rust.after-2fa-auth-session-valkey"
jq -e '.all_pending_2fa == true and .count == 3' "$runtime/auth_go.2fa-flow-contract.json" >/dev/null
assert_named_file_match two-factor-durable-flow-contract "$runtime/auth_go.2fa-flow-contract.json" "$runtime/auth_rust.2fa-flow-contract.json"

# Remove the synthetic factor before the following self-policy login probes.
# The fixture is disposable and must not affect later authentication behavior.
for database in auth_go auth_rust; do
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "DELETE FROM two_fas WHERE user_id = 1 AND secret = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'" >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -At -v ON_ERROR_STOP=1 \
    -c "SELECT COUNT(*) FROM two_fas WHERE user_id = 1 AND secret = 'JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP'" | grep -qx 0
  if [[ $database == auth_go ]]; then
    invalidate_fixture_user_cache "$go_valkey_port" "go.after-2fa-fixture-cleanup"
  else
    invalidate_fixture_user_cache "$rust_valkey_port" "rust.after-2fa-fixture-cleanup"
  fi
done

# Compare production-observable row mutations without comparing random SIDs,
# refresh hashes, or timestamps. Both backends must leave three revoked,
# rotated browser sessions (two race schedules plus expired replay) and a
# recorded successful login.
for database in auth_go auth_rust; do
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -Atc "SELECT json_build_object('last_login_recorded', last_login_at > 0, 'sessions', (SELECT json_agg(json_build_object('user_id', user_id, 'version', version, 'user_auth_version', user_auth_version, 'status', status, 'rotated', previous_valid_until > 0, 'revoked', revoked_at > 0, 'reason', revoked_reason) ORDER BY user_id) FROM user_sessions)) FROM users WHERE username = 'root'" | jq -S . >"$runtime/$database.effects.json"
done
assert_named_file_match durable-session-effects "$runtime/auth_go.effects.json" "$runtime/auth_rust.effects.json"
for port in "$go_valkey_port" "$rust_valkey_port"; do
  mapfile -t keys < <(valkey_cli_for "$port" --scan --pattern 'auth:session:*')
  [[ ${#keys[@]} == 3 ]]
  for key in "${keys[@]}"; do
    valkey_cli_for "$port" hget "$key" Status | grep -qx revoked
    valkey_cli_for "$port" hget "$key" UserID | grep -qx 1
    [[ $(valkey_cli_for "$port" ttl "$key") -gt 0 ]]
  done
done

# `/api/user/self` resolves both dashboard sessions and opaque PATs before the
# Go-compatible UserAuth policy is applied.  Exercise the policy over real TCP
# and prove every rejected GET is read-only: no user/session/flow/cache change
# beyond the explicit fixture mutation immediately preceding that case.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  prefix="$runtime/$(basename "$base").self-policy"
  capture_listener_response "$prefix.login" -H 'content-type: application/json' \
    -d '{"username":"root","password":"password"}' "$base/api/user/login"
  grep -qx 200 "$prefix.login.status"
  policy_session=$(jq -r '.data.access_token' "$prefix.login.json")
  [[ $policy_session != null && -n $policy_session ]]
  if [[ $base == *":$go_port" ]]; then
    database=auth_go; valkey_port=$go_valkey_port
  else
    database=auth_rust; valkey_port=$rust_valkey_port
  fi

  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "UPDATE users SET role = 0, status = 1, access_token = 'auth-policy-pat' WHERE id = 1" >/dev/null
  invalidate_fixture_user_cache "$valkey_port" "$prefix.role-zero.fixture"
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.role-zero.before"
  capture_listener_response "$prefix.role-zero-session" -H "authorization: Bearer $policy_session" "$base/api/user/self"
  capture_listener_response "$prefix.role-zero-pat" -H 'authorization: Bearer auth-policy-pat' "$base/api/user/self"
  assert_self_error_contract "$prefix.role-zero-session" 403 AUTH_INSUFFICIENT_PRIVILEGE
  assert_self_error_contract "$prefix.role-zero-pat" 403 AUTH_INSUFFICIENT_PRIVILEGE
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.role-zero.after"
  assert_self_policy_side_effects_unchanged "$prefix.role-zero.before" "$prefix.role-zero.after"

  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "UPDATE users SET role = 2, status = 1 WHERE id = 1" >/dev/null
  invalidate_fixture_user_cache "$valkey_port" "$prefix.role-two.fixture"
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.role-two.before"
  capture_listener_response "$prefix.role-two-pat" -H 'authorization: Bearer auth-policy-pat' "$base/api/user/self"
  assert_self_error_contract "$prefix.role-two-pat" 401 AUTH_USER_INVALID
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.role-two.after"
  assert_self_policy_side_effects_unchanged "$prefix.role-two.before" "$prefix.role-two.after"

  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "UPDATE users SET role = 1, status = 2 WHERE id = 1" >/dev/null
  invalidate_fixture_user_cache "$valkey_port" "$prefix.disabled.fixture"
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.disabled.before"
  capture_listener_response "$prefix.disabled-session" -H "authorization: Bearer $policy_session" "$base/api/user/self"
  capture_listener_response "$prefix.disabled-pat" -H 'authorization: Bearer auth-policy-pat' "$base/api/user/self"
  # The frozen Go middleware validates an internal dashboard session (and its
  # cached user status) before the generic user-status policy, so a disabled
  # session is reported as revoked. PATs resolve the user directly and retain
  # the dedicated disabled-user error.
  assert_self_error_contract "$prefix.disabled-session" 401 AUTH_SESSION_REVOKED
  assert_self_error_contract "$prefix.disabled-pat" 401 AUTH_USER_DISABLED
  snapshot_self_policy_side_effects "$database" "$valkey_port" "$prefix.disabled.after"
  assert_self_policy_side_effects_unchanged "$prefix.disabled.before" "$prefix.disabled.after"

  # Do not leave a disabled fixture behind for the remaining listener checks.
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "UPDATE users SET role = 100, status = 1, access_token = NULL WHERE id = 1" >/dev/null
  invalidate_fixture_user_cache "$valkey_port" "$prefix.restore.fixture"
done
for name in role-zero-session role-zero-pat role-two-pat disabled-session disabled-pat; do
  assert_listener_response_match "self-policy.$name"
done

# Consume the enabled fixed-window limiter through each real listener.  The
# absolute request that first receives 429 is schedule-dependent, but the
# final response itself is an exact cross-listener contract.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  prefix="$runtime/$(basename "$base").global-limit"
  for _ in {1..400}; do
    capture_listener_response "$prefix" -H 'content-type: application/json' -d '{}' "$base/api/user/login"
    [[ $(cat "$prefix.status") == 429 ]] && break
  done
  grep -qx 429 "$prefix.status"
  grep -Eqi '^Retry-After: [1-9][0-9]*' "$prefix.headers"
done
assert_listener_response_match global-limit

# Probe every auth read dependency independently: deny then restore SELECT and
# require readiness to fail then recover. A separate auth-flow INSERT check
# covers a required 2FA write capability, while stopping Valkey covers the live
# cache dependency.
for table in users user_sessions two_fas casbin_rule; do
  psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -c "REVOKE SELECT ON $table FROM lmm_auth_runtime" >/dev/null
  for _ in {1..100}; do [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 503 ]] && break; sleep .05; done
  [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 503 ]]
  psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -c "GRANT SELECT ON $table TO lmm_auth_runtime" >/dev/null
  for _ in {1..100}; do [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]] && break; sleep .05; done
  [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]]
done
# A 2FA login creates a durable auth flow; read-only readiness must not claim
# success when the runtime role cannot perform that required mutation.
psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -c "REVOKE INSERT ON auth_flows FROM lmm_auth_runtime" >/dev/null
for _ in {1..100}; do [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 503 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 503 ]]
psql -h 127.0.0.1 -p "$pg_port" -d auth_rust -c "GRANT INSERT ON auth_flows TO lmm_auth_runtime" >/dev/null
for _ in {1..100}; do [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]]
# The global limiter fails closed on Valkey loss. Stop both independent
# caches, compare the externally visible 500, then restore before readiness.
stop_owned_process go_valkey_pid
stop_owned_process rust_valkey_pid
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  prefix="$runtime/$(basename "$base").valkey-down"
  capture_listener_response "$prefix" -H 'content-type: application/json' -d '{}' "$base/api/user/login"
  grep -qx 500 "$prefix.status"
done
assert_listener_response_match valkey-down
start_valkey go-restored "$go_valkey_port" "$go_valkey_password" "$go_valkey_config" go_valkey_pid
start_valkey rust-restored "$rust_valkey_port" "$rust_valkey_password" "$rust_valkey_config" rust_valkey_pid
for _ in {1..100}; do [[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]] && break; sleep .05; done
[[ $(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$rust_port/readyz") == 200 ]]

rust_build_input_sha256_after=$(rust_build_input_manifest_sha256)
[[ $rust_build_input_sha256_after == "$rust_build_input_sha256" ]] || {
  echo 'Rust build inputs changed while the differential was running; refusing approval evidence' >&2
  exit 1
}
[[ $scenario_total == "$expected_scenarios" && $exact_matches == "$expected_scenarios" && $mismatch_count == 0 ]] || {
  printf 'scenario accounting failed: expected=%s scenarios=%s exact_matches=%s mismatches=%s names=%s\n' \
    "$expected_scenarios" "$scenario_total" "$exact_matches" "$mismatch_count" "${mismatch_names[*]:-}" >&2
  exit 1
}

auth_summary=$(jq -cn \
  --arg legacy_revision "$legacy_revision" \
  --arg frozen_go_manifest_sha256 "$frozen_go_manifest_sha256" \
  --arg rust_build_input_sha256 "$rust_build_input_sha256" \
  --arg rust_binary_sha256 "$rust_binary_sha256" \
  --argjson approval "$([[ $approval_mode == 1 ]] && echo true || echo false)" \
  --argjson expected_scenarios "$expected_scenarios" \
  --argjson scenarios "$scenario_total" \
  --argjson exact_matches "$exact_matches" \
  --argjson mismatches "$mismatch_count" \
  '{test:"auth-listener-differential",mode:"full",approval:$approval,legacy_revision:$legacy_revision,frozen_go_manifest_sha256:$frozen_go_manifest_sha256,rust_build_input_sha256:$rust_build_input_sha256,rust_binary_sha256:$rust_binary_sha256,expected_scenarios:$expected_scenarios,scenarios:$scenarios,exact_matches:$exact_matches,mismatches:$mismatches,postgres_major:18,go_tcp_listener:true,rust_tcp_listener:true,random_isolated_ports:true,owned_listener_lifecycle:true,password_protected_valkey:true,curl_timeouts:true,covered_routes:["POST /api/user/login","POST /api/user/login/2fa","GET /api/user/token","POST /api/user/auth/refresh","POST /api/user/auth/logout","GET /api/user/self"],self_policy_cases:["session-role-0-403","pat-role-0-403","pat-role-2-401","session-disabled-401","pat-disabled-401"],self_policy_rejections_read_only:true,refresh_pair_multiset:["a-first","b-first"],origin_rejection_no_cache_headers:true,two_factor_durable_flow_and_side_effects:true,expired_refresh_replay:true,global_limiter_429:true,acl_revoke_restore:["users","user_sessions","two_fas","casbin_rule"],auth_flow_insert_revoke_restore:true,valkey_stop_restore:true,result:"passed"}')

if [[ -n ${LMM_AUTH_RESULT_DIR:-} ]]; then
  [[ $LMM_AUTH_RESULT_DIR == /* && $LMM_AUTH_RESULT_DIR != *..* ]] || {
    echo 'LMM_AUTH_RESULT_DIR must be absolute and contain no ..' >&2
    exit 2
  }
  mkdir -p "$LMM_AUTH_RESULT_DIR"
  printf '%s\n' "$auth_summary" >"$LMM_AUTH_RESULT_DIR/auth-listener-summary.json"
  while IFS=$'\t' read -r method path cases; do
    slug=$(printf '%s-%s' "$method" "$path" | tr '/:?' '___' | tr -cd '[:alnum:]_-')
    jq -cn --arg method "$method" --arg path "$path" --arg suite "auth-listener-summary.json" \
      --argjson cases "$cases" --argjson scenarios "$scenario_total" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"auth-listener",cases:$cases,suite_scenarios:$scenarios,suite_summary:$suite,real_tcp:true,production_access:true,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$LMM_AUTH_RESULT_DIR/auth-listener-$slug.json"
  done <<'ROUTES'
POST	/api/user/login	7
POST	/api/user/login/2fa	4
GET	/api/user/token	1
POST	/api/user/auth/refresh	9
POST	/api/user/auth/logout	3
GET	/api/user/self	8
ROUTES
fi
printf '%s\n' "$auth_summary"
