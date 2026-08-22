#!/usr/bin/env bash
# Exhaustive real-TCP Go/Rust parity gate for the nine dashboard API-token
# routes.  It owns two disposable PostgreSQL databases and two Valkey
# instances; no production listener, credential, or datastore is accepted.
set -euo pipefail
# A caller may invoke this script with `bash -x`; disable tracing before any
# generated credential, URL, or temporary configuration enters shell state.
set +x

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
allow_current_oracle=${LMM_API_TOKEN_PARITY_ALLOW_CURRENT_ORACLE:-0}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
# Each invocation owns fresh loopback ports. Every listener is randomized and
# all five retain two pre-bind checks. The Rust test instance receives its
# allocated Valkey port through LMM_RS_TEST_VALKEY_PORT.
pg_port=${LMM_API_TOKEN_PARITY_PG_PORT:-}
go_port=${LMM_API_TOKEN_PARITY_GO_PORT:-}
rust_port=${LMM_API_TOKEN_PARITY_RUST_PORT:-}
go_valkey_port=${LMM_API_TOKEN_PARITY_GO_VALKEY_PORT:-}
rust_valkey_port=${LMM_API_TOKEN_PARITY_RUST_VALKEY_PORT:-}
curl_connect_timeout=2
curl_max_time=15
approval_mode=${LMM_API_TOKEN_PARITY_APPROVAL:-0}
probe_only=${LMM_API_TOKEN_PARITY_PROBE_ONLY:-0}
keep_runtime=${LMM_KEEP_API_TOKEN_PARITY_RUNTIME:-0}
result_dir=${LMM_API_TOKEN_PARITY_RESULT_DIR:-}
# The 164 inherited route vectors plus three explicit healthy-cache -> DEL
# vectors (PUT, single DELETE, and batch DELETE).
expected_scenarios=168
case "$approval_mode" in 0|1) ;; *) echo 'LMM_API_TOKEN_PARITY_APPROVAL must be 0 or 1' >&2; exit 2 ;; esac
case "$probe_only" in 0|1) ;; *) echo 'LMM_API_TOKEN_PARITY_PROBE_ONLY must be 0 or 1' >&2; exit 2 ;; esac
if [[ $approval_mode == 1 && $probe_only == 1 ]]; then
  echo 'approval mode refuses probe-only; run the complete 164-scenario matrix' >&2
  exit 2
fi
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_API_TOKEN_PARITY_RESULT_DIR must be an absolute path without ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
build_root=${TMPDIR:-/dev/shm}
[[ -d $build_root && -w $build_root ]] || { echo "temporary build directory is not writable: $build_root" >&2; exit 1; }
# PostgreSQL requires a normal filesystem for its data directory on this host;
# the frozen Go build is the only sizeable transient tree and belongs in RAM.
runtime_root=${LMM_API_TOKEN_PARITY_RUNTIME_ROOT:-/tmp}
[[ $runtime_root == /* && $runtime_root != *..* ]] || {
  echo 'LMM_API_TOKEN_PARITY_RUNTIME_ROOT must be an absolute path without ..' >&2
  exit 2
}
mkdir -p -- "$runtime_root"
[[ -d $runtime_root && -w $runtime_root ]] || {
  echo "API-token parity runtime root is not writable: $runtime_root" >&2
  exit 1
}
runtime=$(mktemp -d "$runtime_root/lmm-api-token-parity-current.XXXXXX")
go_build=$(mktemp -d "$build_root/lmm-api-token-parity-current-go.XXXXXX")
go_static_keys="$runtime/go.static.keys"
rust_static_keys="$runtime/rust.static.keys"
go_generated_keys="$runtime/go.generated.keys"
rust_generated_keys="$runtime/rust.generated.keys"
: >"$go_static_keys"
: >"$rust_static_keys"
: >"$go_generated_keys"
: >"$rust_generated_keys"
mismatch_count=0
mismatch_names=()
# Each disposable Valkey gets a distinct 32-byte hexadecimal password.  Keep
# credentials only in 0600 files/environment; never put them in an argv,
# URL printed by this script, or a command trace.
go_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
rust_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
go_valkey_config="$runtime/go-valkey.conf"
rust_valkey_config="$runtime/rust-valkey.conf"
go_env_file="$runtime/go.env"
rust_env_file="$runtime/rust.env"
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
go_pid=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
go_pid_start=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
rust_pid=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
rust_pid_start=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
go_valkey_pid=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
go_valkey_pid_start=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
rust_valkey_pid=
# shellcheck disable=SC2034 # Names are intentionally read via safe indirection.
rust_valkey_pid_start=
cleanup_started=false
# The anonymous shared-driver probe is the first completed scenario.  Each
# route assertion below records one additional scenario after all of its
# response and database checks have run.
scenario_total=0
scenario_mismatch_count=0
last_scenario_had_mismatch=false
frozen_go_manifest_sha256=
rust_source_sha256=
rust_binary_sha256=
rust_build_manifest_before="$runtime/rust-build-inputs.before.sha256"
rust_build_manifest_after="$runtime/rust-build-inputs.after.sha256"

pid_start_time() {
  local pid=$1
  [[ -r /proc/$pid/stat ]] || return 1
  awk '{print $22}' "/proc/$pid/stat"
}
record_pid() {
  local pid_name=$1 pid=$2 start
  printf -v "$pid_name" '%s' "$pid"
  start=$(pid_start_time "$pid") || {
    # Do not signal a PID whose identity could not be captured: it may have
    # exited and been recycled. `wait` only observes this shell's child.
    wait "$pid" 2>/dev/null || true
    printf -v "$pid_name" ''
    printf -v "${pid_name}_start" ''
    echo "failed to record child PID $pid" >&2
    return 1
  }
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
  local pid_name=$1 pid
  pid=${!pid_name:-}
  if [[ -n $pid ]]; then
    if owned_pid_is_live "$pid_name"; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    else
      echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2
    fi
  fi
  # Clear immediately after kill/wait (or refusal) so repeated traps cannot
  # target a recycled PID, including a partially recorded child.
  printf -v "$pid_name" ''
  printf -v "${pid_name}_start" ''
}
cleanup() {
  [[ $cleanup_started == false ]] || return 0
  cleanup_started=true
  stop_owned_process go_pid
  stop_owned_process rust_pid
  stop_owned_process go_valkey_pid
  stop_owned_process rust_valkey_pid
  rm -f -- "$go_env_file" "$rust_env_file" "$go_valkey_config" "$rust_valkey_config"
  [[ -d $runtime/pg ]] && pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    "$runtime_root"/lmm-api-token-parity-current.*)
      if [[ $keep_runtime == 1 ]]; then
        echo "preserved API-token parity runtime: $runtime" >&2
      else
        rm -rf "$runtime"
      fi
      ;;
    *) echo "refusing unexpected runtime: $runtime" >&2 ;;
  esac
  case "$go_build" in
    "$build_root"/lmm-api-token-parity-current-go.*) rm -rf "$go_build" ;;
    *) echo "refusing unexpected Go build directory: $go_build" >&2 ;;
  esac
}
on_signal() {
  local signal=$1 exit_code=128
  case "$signal" in
    INT) exit_code=130 ;;
    TERM) exit_code=143 ;;
  esac
  echo "API-token parity differential received $signal; exiting $exit_code after cleanup" >&2
  trap '' INT TERM
  cleanup
  trap - EXIT
  exit "$exit_code"
}
trap cleanup EXIT
trap 'on_signal INT' INT
trap 'on_signal TERM' TERM
trap 'echo "API-token parity differential failed at line $LINENO" >&2' ERR

for command in cargo curl createdb git go initdb jq od openssl pg_ctl postgres psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }

write_rust_build_manifest() {
  local destination=$1
  (
    cd "$repo_root"
    printf '%s\0' apps/api-rust/Cargo.toml apps/api-rust/Cargo.lock apps/api-rust/Cargo.toml
    [[ ! -f apps/api-rust/build.rs ]] || printf '%s\0' apps/api-rust/build.rs
    for directory in apps/api-rust/src apps/api-rust/assets apps/api-rust/crates; do
      [[ -d $directory ]] || continue
      find "$directory" -type f \( -path '*/src/*' -o -path '*/assets/*' -o -name Cargo.toml -o -name build.rs \) -print0
    done
  ) | LC_ALL=C sort -z | xargs -0r sha256sum | LC_ALL=C sort >"$destination"
  [[ -s $destination ]] || { echo 'Rust build-input manifest is empty' >&2; return 1; }
}
rust_manifest_sha256() { sha256sum "$1" | awk '{print $1}'; }
assert_rust_build_inputs_unchanged() {
  local phase=$1
  write_rust_build_manifest "$rust_build_manifest_after"
  if ! cmp -s "$rust_build_manifest_before" "$rust_build_manifest_after"; then
    echo "Rust build inputs changed during $phase; refusing differential evidence" >&2
    diff -u "$rust_build_manifest_before" "$rust_build_manifest_after" >&2 || true
    return 1
  fi
}
assert_frozen_inputs() {
  if [[ $allow_current_oracle == 1 ]]; then
    [[ -d $legacy_root && ${legacy_root##*/} != "$legacy_revision" ]] || {
      echo 'current Go oracle must be a distinct external snapshot' >&2
      return 1
    }
    legacy_revision=${LMM_API_TOKEN_PARITY_GO_REVISION:-current-go-oracle}
  else
    [[ -d $legacy_root && ${legacy_root##*/} == "$legacy_revision" ]] || {
      echo "frozen Go archive is missing or revision-named incorrectly: $legacy_root" >&2
      return 1
    }
  fi
  [[ -f $legacy_root/SHA256SUMS && -f $legacy_root/GIT-LS-FILES-S.tsv ]] || {
    echo 'frozen Go archive lacks its pinned content manifest' >&2
    return 1
  }
  (cd "$legacy_root" && sha256sum --check --status SHA256SUMS) || {
    echo 'frozen Go archive content hash verification failed' >&2
    return 1
  }
  frozen_go_manifest_sha256=$(sha256sum "$legacy_root/SHA256SUMS" "$legacy_root/GIT-LS-FILES-S.tsv" | sha256sum | awk '{print $1}')
  # This filesystem manifest intentionally includes untracked local Rust
  # sources/assets: Cargo compiles files, not Git's index.
  write_rust_build_manifest "$rust_build_manifest_before"
  rust_source_sha256=$(rust_manifest_sha256 "$rust_build_manifest_before")
  [[ $frozen_go_manifest_sha256 =~ ^[[:xdigit:]]{64}$ && $rust_source_sha256 =~ ^[[:xdigit:]]{64}$ ]] || {
    echo 'input content hashing failed' >&2
    return 1
  }
}
assert_frozen_inputs

assert_distinct_ports() {
  local label port other_label other_port
  for label in "$@"; do
    port=${label#*:}
    [[ $port =~ ^[1-9][0-9]{0,4}$ && $port -le 65535 ]] || { echo "invalid port: $label" >&2; exit 1; }
    for other_label in "$@"; do
      [[ $label == "$other_label" ]] && continue
      other_port=${other_label#*:}
      [[ $port != "$other_port" ]] || { echo "ports must be pairwise distinct: $label and $other_label" >&2; exit 1; }
    done
  done
}
preflight_port() {
  local name=$1 port=$2 listeners
  if ! listeners=$(ss -H -ltn "sport = :$port" 2>/dev/null); then
    echo "unable to inspect $name port: 127.0.0.1:$port" >&2
    exit 1
  fi
  if [[ -n $listeners ]]; then
    echo "$name port is already occupied: 127.0.0.1:$port" >&2
    exit 1
  fi
}
random_free_port() {
  local label=$1 candidate remaining=256
  while (( remaining-- > 0 )); do
    candidate=$((20000 + (16#$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000)))
    case " $pg_port $go_port $rust_port $go_valkey_port $rust_valkey_port " in *" $candidate "*) continue ;; esac
    if ! ss -H -ltn "sport = :$candidate" 2>/dev/null | grep -q .; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  echo "unable to allocate isolated $label port" >&2
  return 1
}
[[ -n $pg_port ]] || pg_port=$(random_free_port PostgreSQL)
[[ -n $go_port ]] || go_port=$(random_free_port Go_HTTP)
[[ -n $rust_port ]] || rust_port=$(random_free_port Rust_HTTP)
[[ -n $go_valkey_port ]] || go_valkey_port=$(random_free_port Go_Valkey)
[[ -n $rust_valkey_port ]] || rust_valkey_port=$(random_free_port Rust_Valkey)
assert_distinct_ports "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port"
# First occupancy audit occurs at allocation time.  Repeat it for every port
# before long builds; each start helper performs the second adjacent-to-bind
# audit so a competing runner can never be mistaken for this run's listener.
preflight_port PostgreSQL "$pg_port"
preflight_port Go_HTTP "$go_port"
preflight_port Rust_HTTP "$rust_port"
preflight_port Go_Valkey "$go_valkey_port"
preflight_port Rust_Valkey "$rust_valkey_port"
listener_owned_by() {
  local port=$1 expected_pid=$2
  local -a listener_lines owner_pids
  # `ss` can report more than one process on a reuseport listener. A readiness
  # response is accepted only when exactly one listener and one owner PID are
  # reported, and that PID is the child this run started.
  mapfile -t listener_lines < <(ss -H -ltnp "sport = :$port" 2>/dev/null | sed '/^[[:space:]]*$/d' || true)
  (( ${#listener_lines[@]} == 1 )) || return 1
  mapfile -t owner_pids < <(grep -oE 'pid=[0-9]+' <<<"${listener_lines[0]}" | sort -u || true)
  (( ${#owner_pids[@]} == 1 )) || return 1
  [[ ${owner_pids[0]} == "pid=$expected_pid" ]]
}

go_database=api_token_parity_go
rust_database=lmm_test_api_token_parity
rust_role=lmm_test_api_token_parity
rust_schema=lmm_test_api_token_parity_v1
go_dsn="postgresql://127.0.0.1:$pg_port/$go_database?sslmode=disable"
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path%3D$rust_schema"

sql() { psql -h 127.0.0.1 -p "$pg_port" -d "$1" -qAt -v ON_ERROR_STOP=1 -c "$2"; }
sql_both() {
  sql "$go_database" "$1" >/dev/null
  sql "$rust_database" "$1" >/dev/null
}
assert_go_schema_ready() {
  # The frozen Go listener must initialize the exact disposable PostgreSQL
  # database before the shared fixture is seeded.  `/api/status` alone is not
  # evidence of that: a listener with a missing SQL_DSN can be healthy against
  # its SQLite fallback.  Keep this probe before every fixture write so a
  # broken environment is diagnosed at the ownership boundary.
  local probe
  probe=$(sql "$go_database" "SELECT current_database() || '|' || current_schema() || '|' || COALESCE(to_regclass('public.users')::text, '')")
  if [[ $probe != "$go_database|public|users" ]]; then
    echo "frozen Go schema probe failed: expected $go_database|public|users, got ${probe:-<empty>}" >&2
    return 1
  fi
}
wait_for_go_token_table() {
  # `/api/status` can answer while the frozen Go process is still finishing
  # its first-run AutoMigrate.  Do not install the disposable compatibility
  # trigger until the token table exists.
  for _ in {1..300}; do
    if [[ $(sql "$go_database" "SELECT to_regclass('public.tokens') IS NOT NULL") == t ]]; then
      return 0
    fi
    sleep .05
  done
  echo 'frozen Go schema did not materialize public.tokens after readiness' >&2
  return 1
}
key_registry_file() {
  case "$1" in
    "$go_database") printf '%s\n' "$go_static_keys" ;;
    "$rust_database") printf '%s\n' "$rust_static_keys" ;;
    *) echo "unknown API-token database: $1" >&2; return 1 ;;
  esac
}
generated_registry_file() {
  case "$1" in
    "$go_database") printf '%s\n' "$go_generated_keys" ;;
    "$rust_database") printf '%s\n' "$rust_generated_keys" ;;
    *) echo "unknown API-token database: $1" >&2; return 1 ;;
  esac
}
remember_static_keys() {
  local database=$1 registry
  registry=$(key_registry_file "$database")
  sql "$database" "SELECT COALESCE(key, '') FROM tokens ORDER BY id" |
    while IFS= read -r key; do
      [[ -n $key ]] && printf '%s\n' "$key"
    done >"$registry"
}
refresh_generated_keys() {
  local database=$1 static_registry generated_registry key
  # Fault-injection scenarios deliberately rename `tokens`.  Do not turn a
  # correctly compared route-level DB error into a runner-side psql failure;
  # any real connectivity/query failure still propagates from `sql`.
  if [[ $(sql "$database" "SELECT to_regclass('tokens') IS NOT NULL") != t ]]; then
    return 0
  fi
  static_registry=$(key_registry_file "$database")
  generated_registry=$(generated_registry_file "$database")
  while IFS= read -r key; do
    [[ $key =~ ^[A-Za-z0-9]{48}$ ]] || continue
    grep -Fqx -- "$key" "$static_registry" && continue
    grep -Fqx -- "$key" "$generated_registry" && continue
    printf '%s\n' "$key" >>"$generated_registry"
  done < <(sql "$database" "SELECT COALESCE(key, '') FROM tokens ORDER BY id")
}
generated_keys_json() {
  local database=$1
  jq -Rsc 'split("\n") | map(select(length > 0))' "$(generated_registry_file "$database")"
}
flush_valkey() {
  VALKEYCLI_AUTH="$go_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$go_valkey_port" flushdb >/dev/null
  VALKEYCLI_AUTH="$rust_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$rust_valkey_port" flushdb >/dev/null
}
token_snapshot() {
  local database=$1 generated
  refresh_generated_keys "$database"
  generated=$(generated_keys_json "$database")
  local snapshot_filter=''
  if [[ $allow_current_oracle != 1 ]]; then
    # The frozen 5418ce6 Go schema predates Rust's optional auto-groups
    # extension. It is deliberately not part of the historical token-table
    # comparison; the route wire contract above still verifies that frozen
    # responses omit the field.
    snapshot_filter='del(.auto_groups) | '
  fi
  sql "$database" "SELECT COALESCE(json_agg(to_jsonb(token) ORDER BY id), '[]'::json) FROM tokens AS token" |
    jq -S --argjson generated "$generated" --arg snapshot_filter "$snapshot_filter" '
      def canonical_generated_key:
        . as $candidate
        | if ($candidate | type) == "string"
            and ($candidate | test("^[A-Za-z0-9]{48}$"))
            and (($generated | index($candidate)) != null)
          then "<KEY>"
          else .
          end;
      map((if $snapshot_filter == "" then . else (del(.auto_groups)) end)
        | .key |= canonical_generated_key
        | .created_time = "<TIME>"
        | .accessed_time = "<TIME>"
        | .deleted_at = (if .deleted_at == null then null else "<DELETED>" end))'
}
business_table_snapshot() {
  local database=$1 table=$2
  sql "$database" "SELECT COALESCE(json_agg(to_jsonb(row) ORDER BY to_jsonb(row)::text), '[]'::json) FROM $table AS row" | jq -S .
}
business_snapshot_without_tokens() {
  # Every dashboard-token request is forbidden from changing any business
  # table except `tokens`.  Keep Go and Rust snapshots per-engine: their
  # independently seeded rows are intentionally not cross-compared, but each
  # engine must remain byte-for-byte stable across a request. Rust owns the
  # schema-contract table; Go is required to keep it absent.
  local database=$1 contract
  contract=$(sql "$database" "SELECT to_regclass('lmm_schema_contract') IS NOT NULL")
  jq -n \
    --argjson users "$(business_table_snapshot "$database" users)" \
    --argjson options "$(business_table_snapshot "$database" options)" \
    --argjson user_sessions "$(business_table_snapshot "$database" user_sessions)" \
    --argjson auth_flows "$(business_table_snapshot "$database" auth_flows)" \
    --argjson custom_oauth_providers "$(business_table_snapshot "$database" custom_oauth_providers)" \
    --argjson setups "$(business_table_snapshot "$database" setups)" \
    --argjson two_fas "$(business_table_snapshot "$database" two_fas)" \
    --argjson casbin_rule "$(business_table_snapshot "$database" casbin_rule)" \
    --argjson two_fa_backup_codes "$(business_table_snapshot "$database" two_fa_backup_codes)" \
    --argjson lmm_schema_contract "$(if [[ $contract == t ]]; then business_table_snapshot "$database" lmm_schema_contract; else printf '[]'; fi)" \
    --arg contract "$contract" \
    '{users:$users,options:$options,user_sessions:$user_sessions,auth_flows:$auth_flows,custom_oauth_providers:$custom_oauth_providers,setups:$setups,two_fas:$two_fas,casbin_rule:$casbin_rule,two_fa_backup_codes:$two_fa_backup_codes,lmm_schema_contract:{present:($contract == "t"),rows:$lmm_schema_contract}}' | jq -S .
}
assert_only_tokens_may_change() {
  local name=$1 go_before=$2 rust_before=$3 allow_console_activation=${4:-false}
  # Go must never quietly acquire Rust's test-only schema contract.  This is
  # checked before and after every scenario snapshot rather than inferred from
  # an unchanged empty value.
  if ! jq -e '.lmm_schema_contract.present == false and (.lmm_schema_contract.rows | length) == 0' "$go_before" >/dev/null ||
    ! jq -e '.lmm_schema_contract.present == false and (.lmm_schema_contract.rows | length) == 0' <(business_snapshot_without_tokens "$go_database") >/dev/null; then
    record_mismatch "$name: Go lmm_schema_contract must remain absent"
  fi
  if ! jq -e '.lmm_schema_contract.present == true' "$rust_before" >/dev/null ||
    ! jq -e '.lmm_schema_contract.present == true' <(business_snapshot_without_tokens "$rust_database") >/dev/null; then
    record_mismatch "$name: Rust lmm_schema_contract must remain present"
  fi
  local go_after rust_after
  go_after=$(business_snapshot_without_tokens "$go_database")
  rust_after=$(business_snapshot_without_tokens "$rust_database")
  if [[ $allow_console_activation == true ]]; then
    # The disposable Go database installs the same first-token activation
    # trigger for frozen and current-oracle runs. Permit only that one
    # explicitly scoped create side effect while keeping every other
    # non-token column strict.
    if ! diff -u \
      <(jq '.users |= map(del(.console_activated_at))' "$go_before") \
      <(jq '.users |= map(del(.console_activated_at))' <<<"$go_after"); then
      record_mismatch "$name: Go changed a non-token business table"
    fi
    if ! diff -u \
      <(jq '.users |= map(del(.console_activated_at))' "$rust_before") \
      <(jq '.users |= map(del(.console_activated_at))' <<<"$rust_after"); then
      record_mismatch "$name: Rust changed a non-token business table"
    fi
  else
    if ! diff -u "$go_before" <(printf '%s\n' "$go_after"); then
      record_mismatch "$name: Go changed a non-token business table"
    fi
    if ! diff -u "$rust_before" <(printf '%s\n' "$rust_after"); then
      record_mismatch "$name: Rust changed a non-token business table"
    fi
  fi
}
valkey_cli() {
  local engine=$1
  shift
  case "$engine" in
    go) VALKEYCLI_AUTH="$go_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$go_valkey_port" "$@" ;;
    rust) VALKEYCLI_AUTH="$rust_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$rust_valkey_port" "$@" ;;
    *) echo "unknown Valkey engine: $engine" >&2; return 1 ;;
  esac
}
token_cache_key() {
  # Both listeners receive this fixed synthetic CRYPTO_SECRET through the
  # private runtime env file.  Use OpenSSL's HMAC implementation so the
  # explicitly primed key is exactly the production cache namespace rather
  # than merely a token:-prefixed lookalike.
  local raw_key=$1
  printf '%s' "$raw_key" |
    openssl dgst -sha256 -hmac 'ApiTokenParity-Crypto-2026-LongEnough' -binary |
    od -An -v -tx1 | tr -d ' \n' |
    sed 's/^/token:/'
}
prime_healthy_token_cache() {
  local name=$1 raw_keys=$2 raw_key cache_key engine
  IFS=',' read -r -a cache_raw_keys <<<"$raw_keys"
  for raw_key in "${cache_raw_keys[@]}"; do
    [[ -n $raw_key ]] || continue
    cache_key=$(token_cache_key "$raw_key")
    for engine in go rust; do
      # The field and TTL are deliberately sufficient for the runner's HMAC
      # health contract; mutation must DEL this exact cache key, not merely
      # preserve an equivalent token row.
      valkey_cli "$engine" hset "$cache_key" Id 1 >/dev/null
      valkey_cli "$engine" expire "$cache_key" 60 >/dev/null
    done
    printf '%s\t%s\n' "$raw_key" "$cache_key" >>"$runtime/cache-prime.$name.tsv"
  done
  for engine in go rust; do
    # `token_hmac_snapshot` intentionally removes TTL for cross-engine
    # comparisons.  The prime precondition is the opposite: it must prove a
    # live positive TTL before the route runs, so inspect the full snapshot.
    if ! jq -e 'length > 0 and all(.[]; .class == "token_hmac" and .type == "hash" and .ttl > 0 and (.fields | length) > 0)' \
      <(tracked_valkey_snapshot "$engine") >/dev/null; then
      record_mismatch "$name: $engine failed to construct healthy token HMAC cache prime"
    fi
  done
}
assert_primed_token_caches_deleted() {
  local name=$1 raw_keys=$2 raw_key cache_key engine exists
  IFS=',' read -r -a cache_raw_keys <<<"$raw_keys"
  for raw_key in "${cache_raw_keys[@]}"; do
    [[ -n $raw_key ]] || continue
    cache_key=$(token_cache_key "$raw_key")
    for engine in go rust; do
      # Legacy Go invalidation is asynchronous. Bound the observation so a
      # missing DEL cannot masquerade as a timing allowance.
      exists=1
      for _ in {1..100}; do
        exists=$(valkey_cli "$engine" exists "$cache_key")
        [[ $exists == 0 ]] && break
        sleep .02
      done
      if [[ $exists != 0 ]]; then
        record_mismatch "$name: $engine did not DEL primed token cache"
      fi
    done
  done
  jq -n --arg name "$name" --arg raw_keys "$raw_keys" \
    '{name:$name,mode:"healthy-cache-prime-to-del",raw_keys:($raw_keys | split(",") | map(select(length > 0)))}' >"$runtime/cache-delete.$name.json"
}
tracked_valkey_snapshot() {
  # SCAN is intentionally followed by one per-key Lua observation.  A 60s
  # token cache may expire after SCAN but before TYPE/TTL/HKEYS; treating that
  # benign expiry as `type=none, ttl=-2` made both implementations appear to
  # violate their HMAC contract.  The script observes existence, type, TTL and
  # fields atomically and omits a key that has genuinely disappeared.
  local engine=$1 key class state
  local -r snapshot_lua='local present=redis.call("EXISTS",KEYS[1]); if present==0 then return cjson.encode({exists=false}) end; local kind=redis.call("TYPE",KEYS[1]).ok; local fields={}; if kind=="hash" then fields=redis.call("HKEYS",KEYS[1]) end; return cjson.encode({exists=true,type=kind,ttl=redis.call("TTL",KEYS[1]),fields=fields})'
  while IFS= read -r key; do
    case "$key" in
      token:*) class=token_hmac ;;
      rateLimit:v2:ip:GA:*) class=GA ;;
      rateLimit:v2:ip:CT:*) class=CT ;;
      rateLimit:v2:user:SR:*) class=SR ;;
      *) continue ;;
    esac
    state=$(valkey_cli "$engine" --raw eval "$snapshot_lua" 1 "$key")
    jq -e . >/dev/null <<<"$state"
    [[ $(jq -r '.exists' <<<"$state") == true ]] || continue
    jq -cn --arg class "$class" --arg key "$key" --argjson state "$state" '
      {class:$class,key_shape:(if $class == "token_hmac" then (if ($key | test("^token:[0-9a-f]{64}$")) then "hmac-sha256" else "invalid" end) else "rate-limit" end),type:$state.type,ttl:$state.ttl,fields:($state.fields | if type == "array" then sort else [] end)}'
  done < <(valkey_cli "$engine" --scan | sort) | jq -Ssc 'sort_by(.class,.key_shape,.type,.fields)'
}
normalized_valkey_snapshot() { jq -S 'map(del(.ttl))' <<<"$1"; }
token_hmac_snapshot() { jq -S 'map(select(.class == "token_hmac")) | map(del(.ttl))' <<<"$1"; }
assert_valkey_snapshot_healthy() {
  local name=$1 engine=$2 snapshot=$3
  # Redis/Valkey returns TTL=0 while a rate-limit key is still valid but has
  # less than one second remaining.  Token HMACs remain stricter: they need a
  # positive TTL, hash type, canonical key form, and at least one field.
  if ! jq -e 'all(.[]; if .class == "token_hmac" then (.ttl > 0 and .type == "hash" and .key_shape == "hmac-sha256" and (.fields | length) > 0) else .ttl >= 0 end)' <<<"$snapshot" >/dev/null; then
    jq -c 'map({class,key_shape,type,ttl,field_count:(.fields|length)})' <<<"$snapshot" >&2
    record_mismatch "$name: $engine Valkey HMAC/hash/TTL contract"
  fi
}
assert_valkey_contract() {
  # `mode=no-change` is for rejected/missing/invalid/database-failure calls:
  # their normalized tracked cache must not change.  A successful mutation may
  # legally DEL an already-primed token cache, so it only requires both engines
  # to retain a valid normalized cache contract.  The cache-prime mode is the
  # sole place that requires a token HSET/hash/TTL to exist.
  local name=$1 go_before=$2 rust_before=$3 mode=${4:-mutation} go_after rust_after
  go_after=$(tracked_valkey_snapshot go)
  rust_after=$(tracked_valkey_snapshot rust)
  assert_valkey_snapshot_healthy "$name" Go "$go_after"
  assert_valkey_snapshot_healthy "$name" Rust "$rust_after"
  if ! diff -u <(normalized_valkey_snapshot "$go_after") <(normalized_valkey_snapshot "$rust_after"); then
    record_mismatch "$name: Valkey key/hash-field differential"
  fi
  if [[ $mode == no-change ]]; then
    # GA/CT/SR may create their first counter key before a malformed or DB
    # failing handler is reached.  That is expected middleware activity; the
    # handler itself must not HSET/DEL a token cache.
    if ! diff -u <(token_hmac_snapshot "$go_before") <(token_hmac_snapshot "$go_after"); then
      record_mismatch "$name: Go rejected request changed token-cache state"
    fi
    if ! diff -u <(token_hmac_snapshot "$rust_before") <(token_hmac_snapshot "$rust_after"); then
      record_mismatch "$name: Rust rejected request changed token-cache state"
    fi
  elif [[ $mode == cache-prime ]]; then
    if [[ $(token_hmac_snapshot "$go_after") == '[]' || $(token_hmac_snapshot "$rust_after") == '[]' ]]; then
      record_mismatch "$name: successful cache-prime did not HSET token HMAC with TTL"
    fi
  fi
  jq -n --argjson go_before "$go_before" --argjson rust_before "$rust_before" --argjson go_after "$go_after" --argjson rust_after "$rust_after" \
    --arg mode "$mode" '{mode:$mode,go:{before:$go_before,after:$go_after},rust:{before:$rust_before,after:$rust_after}}' >"$runtime/valkey.$name.json"
}
assert_tokens_match() { diff -u <(token_snapshot "$go_database") <(token_snapshot "$rust_database"); }
assert_token_count() {
  local expected=$1
  [[ $(sql "$go_database" 'SELECT COUNT(*) FROM tokens WHERE deleted_at IS NULL') == "$expected" && $(sql "$rust_database" 'SELECT COUNT(*) FROM tokens WHERE deleted_at IS NULL') == "$expected" ]]
}
reset_tokens() {
  sql_both 'TRUNCATE tokens RESTART IDENTITY'
  : >"$go_static_keys"
  : >"$rust_static_keys"
  : >"$go_generated_keys"
  : >"$rust_generated_keys"
  flush_valkey
}
seed_tokens() {
  reset_tokens
  local rows=$1
  sql_both "$rows"
  sql_both "SELECT setval('tokens_id_seq', COALESCE((SELECT MAX(id) FROM tokens), 1), true)"
  remember_static_keys "$go_database"
  remember_static_keys "$rust_database"
}
seed_cross_user_tokens() {
  # Every authorization probe gets a fresh fixture.  Do not let a legacy
  # mutation in an earlier negative case make a later mixed-id case ambiguous.
  seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-root-alpha',1,'root-alpha',10,0,-1,100,false,false,'','',0,'default',false),(2,1,'sk-root-beta',1,'root-beta',10,0,-1,100,false,false,'','',0,'default',false),(3,2,'sk-other-alpha',1,'other-alpha',10,0,-1,100,false,false,'','',0,'default',false)"
}
cross_user_session_user_id() {
  local base=$1 bearer=$2
  curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" \
    -H "authorization: Bearer $bearer" "$base/api/user/self" |
    jq -er '.data.id // .data.user.id | numbers'
}
cross_user_owner_snapshot() {
  local database=$1
  sql "$database" "SELECT COALESCE(json_agg(json_build_object('id',id,'user_id',user_id,'active',(deleted_at IS NULL)) ORDER BY id), '[]'::json) FROM tokens WHERE id IN (1,2,3)" | jq -S .
}
assert_cross_user_fixture() {
  local name=$1 phase=$2 go_user rust_user go_owners rust_owners
  go_user=$(cross_user_session_user_id "http://127.0.0.1:$go_port" "$go_other_bearer")
  rust_user=$(cross_user_session_user_id "http://127.0.0.1:$rust_port" "$rust_other_bearer")
  go_owners=$(cross_user_owner_snapshot "$go_database")
  rust_owners=$(cross_user_owner_snapshot "$rust_database")
  jq -n --argjson go_session_user_id "$go_user" --argjson rust_session_user_id "$rust_user" \
    --argjson go_tokens "$go_owners" --argjson rust_tokens "$rust_owners" \
    '{go:{session_user_id:$go_session_user_id,tokens:$go_tokens},rust:{session_user_id:$rust_session_user_id,tokens:$rust_tokens}}' >"$runtime/owners.$name.$phase.json"
  if [[ $go_user != 2 || $rust_user != 2 ]]; then
    record_mismatch "$name: second-user bearer did not resolve to fixture user_id=2"
  fi
  if ! jq -e 'map(.user_id) == [1,1,2]' <<<"$go_owners" >/dev/null || ! jq -e 'map(.user_id) == [1,1,2]' <<<"$rust_owners" >/dev/null; then
    record_mismatch "$name: cross-user token owner fixture is invalid"
  fi
}
set_root() {
  sql_both "UPDATE users SET role = $1, status = $2 WHERE username = 'root'"
  flush_valkey
}

canonical_json() {
  local database=$1 generated
  generated=$(generated_keys_json "$database")
  jq -S --argjson generated "$generated" '
    def canonical_generated_key:
      . as $candidate
      | if ($candidate | type) == "string"
          and ($candidate | test("^[A-Za-z0-9]{48}$"))
          and (($generated | index($candidate)) != null)
        then "<KEY>"
          else .
          end;
    def canonical_response_key:
      . as $candidate
      | if ($candidate | type) == "string"
          and ($candidate | test("^[A-Za-z0-9]{4}\\*{10}[A-Za-z0-9]{4}$"))
          and (($generated
            | map(select(type == "string" and test("^[A-Za-z0-9]{48}$"))
              | .[0:4] + "**********" + .[-4:])
            | index($candidate)) != null)
        then "<MASKED_KEY>"
        else canonical_generated_key
        end;
    walk(
      if type == "object" then
        (if has("key") then .key |= canonical_response_key else . end)
        | (if has("keys") and (.keys | type) == "object" then
            .keys |= with_entries(.value |= canonical_generated_key)
          else . end)
        | (if has("created_time") then .created_time = "<TIME>" else . end)
        | (if has("accessed_time") then .accessed_time = "<TIME>" else . end)
        | (if has("DeletedAt") and .DeletedAt != null then .DeletedAt = "<DELETED_AT>" else . end)
      else . end
    )'
}
assert_static_key_canonicalization_preserves_mismatch() {
  local key_a key_b registered_key registered_mask
  printf -v key_a '%*s' 48 ''
  key_a=${key_a// /A}
  printf -v key_b '%*s' 48 ''
  key_b=${key_b// /B}
  registered_key='Ab12Cd34Ef56Gh78Ij90Kl12Mn34Op56Qr78St90Uv12Wx34' # gitleaks:allow -- synthetic parity key
  registered_mask="${registered_key:0:4}**********${registered_key:44:4}"
  printf '%s\n' "$registered_key" >"$go_generated_keys"
  printf '%s\n' "$registered_key" >"$rust_generated_keys"
  scenario_total=$((scenario_total + 1))
  last_scenario_had_mismatch=false
  if diff -u \
    <(jq -cn --arg key "$key_a" '{data:{key:$key,keys:{"1":$key}}}' | canonical_json "$go_database") \
    <(jq -cn --arg key "$key_b" '{data:{key:$key,keys:{"1":$key}}}' | canonical_json "$rust_database") >/dev/null; then
    record_manual_mismatch 'canonicalization masked static generated-shaped keys'
  fi
  if diff -u \
    <(jq -cn '{data:{key:"AAAA**********AAAA"}}' | canonical_json "$go_database") \
    <(jq -cn '{data:{key:"BBBB**********BBBB"}}' | canonical_json "$rust_database") >/dev/null; then
    record_manual_mismatch 'canonicalization masked arbitrary static masked keys'
  fi
  for engine in go rust; do
    local database="${engine}_database"
    if ! jq -e '.data.key == "<KEY>"' \
      <(jq -cn --arg key "$registered_key" '{data:{key:$key}}' | canonical_json "${!database}") >/dev/null; then
      record_manual_mismatch "canonicalization did not normalize registered raw key: $engine"
    fi
    if ! jq -e '.data.key == "<MASKED_KEY>"' \
      <(jq -cn --arg key "$registered_mask" '{data:{key:$key}}' | canonical_json "${!database}") >/dev/null; then
      record_manual_mismatch "canonicalization did not normalize registered masked key: $engine"
    fi
  done
}

assert_key_shapes() {
  local name=$1 path=$2
  local go_json="$runtime/go.$name.json" rust_json="$runtime/rust.$name.json"
  case "$path" in
    */key|*/batch/keys)
      for file in "$go_json" "$rust_json"; do
        jq -e '
          ([.. | objects | .key? // empty] + [.. | objects | (.keys? // {} | .[])]
            | map(select(type == "string")))
          | all(. | contains("*") | not)
        ' "$file" >/dev/null
      done
      ;;
    *)
      for file in "$go_json" "$rust_json"; do
        jq -e '
          ([.. | objects | .key? // empty] + [.. | objects | (.keys? // {} | .[])]
            | map(select(type == "string" and contains("*"))))
          | all(. | test("^.{4}\\*{10}.{4}$"))
        ' "$file" >/dev/null
      done
      ;;
  esac
}
record_mismatch() {
  mismatch_count=$((mismatch_count + 1))
  mismatch_names+=("$1")
  printf 'API-token parity mismatch: %s\n' "$1" >&2
}
record_manual_mismatch() {
  record_mismatch "$1"
  if [[ ${last_scenario_had_mismatch:-false} == false ]]; then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  fi
}
selected_headers() {
  awk '
    BEGIN { IGNORECASE = 1 }
    /^[^:]+:/ {
      name = tolower($1); sub(/:$/, "", name)
      value = $0; sub(/^[^:]+:[[:space:]]*/, "", value); sub(/\r$/, "", value)
      if (name == "content-type" || name == "cache-control" || name == "pragma" || name == "expires" || name == "auth-version") print name ": " value
    }
  ' "$1" | sort
}
pair() {
  local name=$1 method=$2 path=$3 payload=$4 content_type=$5 language=$6
  local go_auth=${7:-$go_bearer} rust_auth=${8:-$rust_bearer}
  local go_prefix="$runtime/go.$name" rust_prefix="$runtime/rust.$name"
  call() {
    local base=$1 bearer=$2 prefix=$3
    local args=(--silent --show-error --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" --dump-header "$prefix.headers" --output "$prefix.json" --write-out '%{http_code}' --request "$method" --header "authorization: Bearer $bearer")
    [[ -z $language ]] || args+=(--header "accept-language: $language")
    if [[ $content_type == none ]]; then
      [[ $payload == __NONE__ ]] || args+=(--header 'content-type:')
    else
      args+=(--header "content-type: $content_type")
    fi
    [[ $payload == __NONE__ ]] || args+=(--data-binary "$payload")
    curl "${args[@]}" "$base$path" >"$prefix.status"
    if ! jq -e . "$prefix.json" >/dev/null; then
      echo "invalid JSON response: $prefix.json" >&2
      cat "$prefix.status" >&2 || true
      sed -n '1,160p' "$runtime/go.log" >&2 || true
      sed -n '1,160p' "$runtime/rust.log" >&2 || true
      sed -n '1,80p' "$prefix.json" >&2
      return 1
    fi
  }
  call "http://127.0.0.1:$go_port" "$go_auth" "$go_prefix"
  call "http://127.0.0.1:$rust_port" "$rust_auth" "$rust_prefix"
  refresh_generated_keys "$go_database"
  refresh_generated_keys "$rust_database"
  if ! diff -u "$go_prefix.status" "$rust_prefix.status"; then
    record_mismatch "$name: status"
  fi
  if ! diff -u <(canonical_json "$go_database" <"$go_prefix.json") <(canonical_json "$rust_database" <"$rust_prefix.json"); then
    record_mismatch "$name: body"
  fi
  if ! assert_key_shapes "$name" "$path"; then
    record_mismatch "$name: key shape"
  fi
  if ! diff -u <(selected_headers "$go_prefix.headers") <(selected_headers "$rust_prefix.headers"); then
    record_mismatch "$name: headers"
  fi
}
assert_no_token_effect() {
  local name=$1 method=$2 path=$3 payload=$4 content_type=$5 language=$6
  local before_mismatches=$mismatch_count
  token_snapshot "$go_database" >"$runtime/go.before.$name"
  token_snapshot "$rust_database" >"$runtime/rust.before.$name"
  business_snapshot_without_tokens "$go_database" >"$runtime/go.business.before.$name"
  business_snapshot_without_tokens "$rust_database" >"$runtime/rust.business.before.$name"
  local go_valkey_before rust_valkey_before
  go_valkey_before=$(tracked_valkey_snapshot go)
  rust_valkey_before=$(tracked_valkey_snapshot rust)
  pair "$name" "$method" "$path" "$payload" "$content_type" "$language"
  if ! diff -u "$runtime/go.before.$name" <(token_snapshot "$go_database"); then
    record_mismatch "$name: Go token side effect"
  fi
  if ! diff -u "$runtime/rust.before.$name" <(token_snapshot "$rust_database"); then
    record_mismatch "$name: Rust token side effect"
  fi
  if ! assert_tokens_match; then
    record_mismatch "$name: cross-engine token snapshot"
  fi
  assert_only_tokens_may_change "$name" "$runtime/go.business.before.$name" "$runtime/rust.business.before.$name"
  assert_valkey_contract "$name" "$go_valkey_before" "$rust_valkey_before" no-change
  scenario_total=$((scenario_total + 1))
  if (( mismatch_count > before_mismatches )); then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  else
    last_scenario_had_mismatch=false
  fi
}
assert_effect() {
  local name=$1 method=$2 path=$3 payload=$4 content_type=$5 language=$6 expected_count=$7 cache_delete_raw_keys=${8:-}
  local before_mismatches=$mismatch_count
  business_snapshot_without_tokens "$go_database" >"$runtime/go.business.before.$name"
  business_snapshot_without_tokens "$rust_database" >"$runtime/rust.business.before.$name"
  [[ -z $cache_delete_raw_keys ]] || prime_healthy_token_cache "$name" "$cache_delete_raw_keys"
  local go_valkey_before rust_valkey_before
  go_valkey_before=$(tracked_valkey_snapshot go)
  rust_valkey_before=$(tracked_valkey_snapshot rust)
  pair "$name" "$method" "$path" "$payload" "$content_type" "$language"
  if ! assert_tokens_match; then
    record_mismatch "$name: cross-engine token snapshot"
  fi
  if ! assert_token_count "$expected_count"; then
    record_mismatch "$name: expected active token count $expected_count"
  fi
  local allow_console_activation=false
  [[ $method == POST && $path == /api/token/ ]] && allow_console_activation=true
  assert_only_tokens_may_change "$name" "$runtime/go.business.before.$name" "$runtime/rust.business.before.$name" "$allow_console_activation"
  # A successful dashboard PUT routes through each implementation's cache
  # write path.  Creates do not prime Go's legacy token cache, while deletes
  # may only DEL a previously primed entry, so they retain mutation semantics.
  local valkey_mode=mutation
  if [[ -n $cache_delete_raw_keys ]]; then
    # PUT refreshes the existing token cache entry; DELETE and batch DELETE
    # must remove the explicitly primed entry.  Sharing one flag for both
    # paths previously reported a false "PUT did not DEL" mismatch.
    [[ $method == PUT ]] && valkey_mode=cache-prime || valkey_mode=cache-delete
  fi
  assert_valkey_contract "$name" "$go_valkey_before" "$rust_valkey_before" "$valkey_mode"
  # PUT refreshes the primed token hash in place (Go's Update calls
  # cacheSetToken, and Rust mirrors that HSET contract); only DELETE routes
  # must remove the primed key entirely.
  if [[ -n $cache_delete_raw_keys && $method != PUT ]]; then
    assert_primed_token_caches_deleted "$name" "$cache_delete_raw_keys"
  fi
  scenario_total=$((scenario_total + 1))
  if (( mismatch_count > before_mismatches )); then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  else
    last_scenario_had_mismatch=false
  fi
}
install_status_only_update_audit() {
  local database search_path
  for database in "$go_database" "$rust_database"; do
    search_path=$(sql "$database" 'SELECT current_schema()')
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 -v audit_schema="$search_path" -v audit_role="$rust_role" <<'SQL' >/dev/null
SET search_path TO :"audit_schema";
CREATE TABLE token_update_of_audit (column_name TEXT NOT NULL);
GRANT INSERT, SELECT, DELETE ON token_update_of_audit TO :"audit_role";
CREATE OR REPLACE FUNCTION record_token_update_of() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  INSERT INTO token_update_of_audit (column_name) VALUES (TG_ARGV[0]);
  RETURN NEW;
END;
$$;
CREATE TRIGGER token_update_of_status AFTER UPDATE OF status ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('status');
CREATE TRIGGER token_update_of_name AFTER UPDATE OF name ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('name');
CREATE TRIGGER token_update_of_expired_time AFTER UPDATE OF expired_time ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('expired_time');
CREATE TRIGGER token_update_of_remain_quota AFTER UPDATE OF remain_quota ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('remain_quota');
CREATE TRIGGER token_update_of_unlimited_quota AFTER UPDATE OF unlimited_quota ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('unlimited_quota');
CREATE TRIGGER token_update_of_model_limits_enabled AFTER UPDATE OF model_limits_enabled ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('model_limits_enabled');
CREATE TRIGGER token_update_of_model_limits AFTER UPDATE OF model_limits ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('model_limits');
CREATE TRIGGER token_update_of_allow_ips AFTER UPDATE OF allow_ips ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('allow_ips');
CREATE TRIGGER token_update_of_group AFTER UPDATE OF "group" ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('group');
CREATE TRIGGER token_update_of_cross_group_retry AFTER UPDATE OF cross_group_retry ON tokens FOR EACH ROW EXECUTE FUNCTION record_token_update_of('cross_group_retry');
GRANT SELECT, INSERT, UPDATE, DELETE ON token_update_of_audit TO :"audit_role";
SQL
  done
}
status_only_update_of_snapshot() {
  local database=$1
  # Keep the value on one line because the assertions compare this JSON as a
  # shell string (the response artifact remains pretty-printed below).
  sql "$database" "SELECT COALESCE(json_agg(column_name ORDER BY column_name), '[]'::json) FROM token_update_of_audit" | jq -cS .
}
clear_status_only_update_audit() {
  sql_both 'TRUNCATE token_update_of_audit'
}
assert_status_only_update_columns() {
  local name=$1 path=$2 payload=$3 expected_count=$4 before_mismatches=$mismatch_count go_columns rust_columns
  local expected_columns='["allow_ips","cross_group_retry","expired_time","group","model_limits","model_limits_enabled","name","remain_quota","status","unlimited_quota"]'
  clear_status_only_update_audit
  assert_effect "$name" PUT "$path" "$payload" application/json '' "$expected_count"
  go_columns=$(status_only_update_of_snapshot "$go_database")
  rust_columns=$(status_only_update_of_snapshot "$rust_database")
  jq -n --argjson expected "$expected_columns" --argjson go "$go_columns" --argjson rust "$rust_columns" \
    '{expected:$expected,go:$go,rust:$rust}' >"$runtime/status-only-update-of.$name.json"
  # PostgreSQL invokes same-kind triggers in name order, and the snapshot has
  # that order pinned explicitly. Frozen Go's UpdateToken and Rust's matching
  # statement both Select/SET all ten mutable columns even for status_only.
  local expected_columns_canonical go_columns_canonical rust_columns_canonical
  expected_columns_canonical=$(jq -cS . <<<"$expected_columns")
  go_columns_canonical=$(jq -cS . <<<"$go_columns" || true)
  rust_columns_canonical=$(jq -cS . <<<"$rust_columns" || true)
  if [[ $go_columns_canonical != "$expected_columns_canonical" ]]; then
    record_manual_mismatch "$name: Go status_only UPDATE OF columns expected $expected_columns, got $go_columns"
  fi
  if [[ $rust_columns_canonical != "$expected_columns_canonical" ]]; then
    record_manual_mismatch "$name: Rust status_only UPDATE OF columns expected $expected_columns, got $rust_columns"
  fi
  if [[ $go_columns_canonical != "$rust_columns_canonical" ]]; then
    record_manual_mismatch "$name: status_only UPDATE OF cross-engine columns"
  fi
  # Test-only audit rows must not leak into any later fixture/snapshot.
  clear_status_only_update_audit
  if (( mismatch_count > before_mismatches )) && [[ $last_scenario_had_mismatch == false ]]; then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  fi
}
assert_response_only() {
  local name=$1; shift
  local valkey_mode=observe
  if [[ ${!#} == __VALKEY_NO_CHANGE__ ]]; then
    valkey_mode=no-change
    set -- "${@:1:$(($# - 1))}"
  fi
  local before_mismatches=$mismatch_count
  business_snapshot_without_tokens "$go_database" >"$runtime/go.business.before.$name"
  business_snapshot_without_tokens "$rust_database" >"$runtime/rust.business.before.$name"
  local go_valkey_before rust_valkey_before
  go_valkey_before=$(tracked_valkey_snapshot go)
  rust_valkey_before=$(tracked_valkey_snapshot rust)
  pair "$name" "$@"
  assert_only_tokens_may_change "$name" "$runtime/go.business.before.$name" "$runtime/rust.business.before.$name"
  assert_valkey_contract "$name" "$go_valkey_before" "$rust_valkey_before" "$valkey_mode"
  scenario_total=$((scenario_total + 1))
  if (( mismatch_count > before_mismatches )); then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  else
    last_scenario_had_mismatch=false
  fi
}
assert_cross_user_no_token_effect() {
  local name=$1 method=$2 path=$3 payload=$4 content_type=$5 language=$6
  local before_mismatches=$mismatch_count
  assert_cross_user_fixture "$name" before
  token_snapshot "$go_database" >"$runtime/go.before.$name"
  token_snapshot "$rust_database" >"$runtime/rust.before.$name"
  business_snapshot_without_tokens "$go_database" >"$runtime/go.business.before.$name"
  business_snapshot_without_tokens "$rust_database" >"$runtime/rust.business.before.$name"
  local go_valkey_before rust_valkey_before
  go_valkey_before=$(tracked_valkey_snapshot go)
  rust_valkey_before=$(tracked_valkey_snapshot rust)
  pair "$name" "$method" "$path" "$payload" "$content_type" "$language" "$go_other_bearer" "$rust_other_bearer"
  assert_cross_user_fixture "$name" after
  if ! diff -u "$runtime/go.before.$name" <(token_snapshot "$go_database"); then
    record_mismatch "$name: Go cross-user token side effect"
  fi
  if ! diff -u "$runtime/rust.before.$name" <(token_snapshot "$rust_database"); then
    record_mismatch "$name: Rust cross-user token side effect"
  fi
  if ! assert_tokens_match; then
    record_mismatch "$name: cross-user token snapshot"
  fi
  assert_only_tokens_may_change "$name" "$runtime/go.business.before.$name" "$runtime/rust.business.before.$name"
  assert_valkey_contract "$name" "$go_valkey_before" "$rust_valkey_before" no-change
  scenario_total=$((scenario_total + 1))
  if (( mismatch_count > before_mismatches )); then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  else
    last_scenario_had_mismatch=false
  fi
}
assert_cross_user_mixed_effect() {
  local name=$1 method=$2 path=$3 payload=$4 content_type=$5 language=$6
  local before_mismatches=$mismatch_count database
  assert_cross_user_fixture "$name" before
  business_snapshot_without_tokens "$go_database" >"$runtime/go.business.before.$name"
  business_snapshot_without_tokens "$rust_database" >"$runtime/rust.business.before.$name"
  local go_valkey_before rust_valkey_before
  go_valkey_before=$(tracked_valkey_snapshot go)
  rust_valkey_before=$(tracked_valkey_snapshot rust)
  pair "$name" "$method" "$path" "$payload" "$content_type" "$language" "$go_other_bearer" "$rust_other_bearer"
  assert_cross_user_fixture "$name" after
  # A user may delete only their own id=3.  Both root-owned ids must remain
  # active; this is intentionally a precise state contract, not an approved
  # Go/Rust deviation.
  for database in "$go_database" "$rust_database"; do
    if [[ $(sql "$database" "SELECT COUNT(*) FROM tokens WHERE (id IN (1,2) AND user_id=1 AND deleted_at IS NULL) OR (id=3 AND user_id=2 AND deleted_at IS NOT NULL)") != 3 ]]; then
      record_mismatch "$name: $database did not preserve foreign tokens and delete only own token"
    fi
  done
  if ! assert_tokens_match; then
    record_mismatch "$name: cross-engine token snapshot"
  fi
  assert_only_tokens_may_change "$name" "$runtime/go.business.before.$name" "$runtime/rust.business.before.$name"
  assert_valkey_contract "$name" "$go_valkey_before" "$rust_valkey_before" mutation
  scenario_total=$((scenario_total + 1))
  if (( mismatch_count > before_mismatches )); then
    scenario_mismatch_count=$((scenario_mismatch_count + 1))
    last_scenario_had_mismatch=true
  else
    last_scenario_had_mismatch=false
  fi
}
install_list_count_fault() {
  local database
  for database in "$go_database" "$rust_database"; do
    local search_path
    search_path=$(sql "$database" 'SELECT current_schema()')
    psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
      -v fault_schema="$search_path" -v rust_role="$rust_role" <<'SQL' >/dev/null
CREATE TABLE token_list_fault_state (marker INTEGER PRIMARY KEY);
CREATE OR REPLACE FUNCTION token_list_fault_gate(_user_id BIGINT) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = :"fault_schema" AS $$
DECLARE statement TEXT;
BEGIN
  SELECT query INTO statement FROM pg_stat_activity WHERE pid = pg_backend_pid();
  IF statement ILIKE '%count(*)%' AND statement ILIKE '%tokens%' THEN
    IF EXISTS (SELECT 1 FROM token_list_fault_state) THEN
      RAISE EXCEPTION 'injected CountUserTokens fault';
    END IF;
  ELSE
    INSERT INTO token_list_fault_state (marker) VALUES (1) ON CONFLICT DO NOTHING;
  END IF;
  RETURN TRUE;
END;
$$;
ALTER TABLE tokens RENAME TO tokens_data;
CREATE VIEW tokens AS SELECT * FROM tokens_data WHERE token_list_fault_gate(user_id);
GRANT SELECT ON tokens TO :"rust_role";
SQL
  done
}
remove_list_count_fault() {
  sql_both 'DROP VIEW IF EXISTS tokens; ALTER TABLE tokens_data RENAME TO tokens; DROP FUNCTION IF EXISTS token_list_fault_gate(BIGINT); DROP TABLE IF EXISTS token_list_fault_state'
}
assert_exact_message() {
  local name=$1 expected=$2
  shift 2
  assert_response_only "$name" "$@" __VALKEY_NO_CHANGE__
  for engine in go rust; do
    if ! jq -e --arg expected "$expected" '.success == false and .message == $expected' \
      "$runtime/$engine.$name.json" >/dev/null; then
      record_manual_mismatch "$name: $engine exact message"
    fi
  done
}
assert_exact_json_response() {
  local name=$1 expected_status=$2 expected_body=$3
  for engine in go rust; do
    if [[ $(<"$runtime/$engine.$name.status") != "$expected_status" ]]; then
      record_manual_mismatch "$name: $engine exact status"
    fi
    if ! jq -e --argjson expected "$expected_body" '. == $expected' \
      "$runtime/$engine.$name.json" >/dev/null; then
      record_manual_mismatch "$name: $engine exact body"
    fi
  done
}
assert_exact_page_response() {
  local name=$1 expected_page=$2 expected_page_size=$3 expected_items=$4
  local expected_body
  expected_body=$(jq -cn \
    --argjson page "$expected_page" \
    --argjson page_size "$expected_page_size" \
    --argjson items "$expected_items" \
    --argjson current_mode "$([[ $allow_current_oracle == 1 ]] && printf true || printf false)" \
    '{success:true,message:"",data:{page:$page,page_size:$page_size,total:4,items:(if $current_mode then ($items | map(. + {auto_groups:null})) else $items end)}}')
  assert_exact_json_response "$name" 200 "$expected_body"
}
assert_active_token_row() {
  local name=$1 database=$2 id=$3 expected_name=$4 expected_expired_time=$5
  if [[ $(sql "$database" "SELECT COUNT(*) FROM tokens WHERE id=$id AND user_id=1 AND deleted_at IS NULL AND name='$expected_name' AND status=1 AND expired_time=$expected_expired_time AND remain_quota=0 AND unlimited_quota=false AND model_limits_enabled=false AND model_limits='' AND COALESCE(allow_ips,'')='' AND used_quota=0 AND COALESCE(\"group\",'')='' AND cross_group_retry=false") != 1 ]]; then
    record_manual_mismatch "$name: $database active-row side effect"
  fi
}
login_as() {
  local base=$1 username=$2
  curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" \
    -H 'content-type: application/json' -d "{\"username\":\"$username\",\"password\":\"password\"}" "$base/api/user/login" |
    jq -er 'select(.success == true) | .data.access_token | strings'
}
login() { login_as "$1" root; }
wait_for() {
  local port=$1 path=$2 pid_name=$3 pid
  # The frozen Go migration can take longer than 15s on a cold PostgreSQL
  # 18 instance.  Keep the listener-ownership checks on every poll, but allow
  # a bounded 30s startup window so cold-start latency is not reported as a
  # route mismatch.
  for _ in {1..600}; do
    owned_pid_is_live "$pid_name" || return 1
    pid=${!pid_name}
    if listener_owned_by "$port" "$pid" && curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "http://127.0.0.1:$port$path" >/dev/null 2>&1; then
      # The listener must still be this exact child after the response; a
      # stranger winning any bind/readiness TOCTOU race fails closed.
      owned_pid_is_live "$pid_name" && listener_owned_by "$port" "$pid" || return 1
      return 0
    fi
    sleep .05
  done
  return 1
}
wait_for_valkey() {
  local port=$1 pid_name=$2 password=$3 pid
  for _ in {1..100}; do
    owned_pid_is_live "$pid_name" || return 1
    pid=${!pid_name}
    if listener_owned_by "$port" "$pid" && VALKEYCLI_AUTH="$password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" ping >/dev/null 2>&1; then
      owned_pid_is_live "$pid_name" && listener_owned_by "$port" "$pid" || return 1
      return 0
    fi
    sleep .05
  done
  return 1
}
write_valkey_config() {
  local config=$1 port=$2 password=$3
  (umask 077
    # The exhaustive matrix runs longer than the deployed 60-second search
    # window. Keep the counter alive for the whole isolated run so Go and
    # Rust cannot cross the expiry boundary between their paired requests.
    printf '%s\n' \
      'bind 127.0.0.1' \
      "port $port" \
      'save ""' \
      'appendonly no' \
      'daemonize no' \
      "dir $runtime" \
      "requirepass $password" >"$config")
  chmod 600 "$config"
}
start_valkey() {
  local name=$1 port=$2 password=$3 config=$4 pid_name=$5
  # This preflight is deliberately adjacent to bind: do not rely on the
  # earlier configuration check after a long build or database setup.
  # SQL fault scenarios below never restart a service. Recovery must stop and
  # clear the owned child before calling this helper; it refuses to overwrite
  # an old PID and records a fresh identity for the new listener.
  [[ -z ${!pid_name:-} ]] || { echo "refusing to overwrite live PID record: $pid_name" >&2; return 1; }
  write_valkey_config "$config" "$port" "$password"
  preflight_port "$name" "$port"
  valkey-server "$config" >"$runtime/$name.log" 2>&1 &
  record_pid "$pid_name" "$!"
  wait_for_valkey "$port" "$pid_name" "$password"
}
write_app_env() {
  local file=$1 redis_variable=$2 redis_url=$3
  (umask 077
    {
    printf 'export SQL_DSN=%q\n' "$go_dsn"
    printf 'export DATABASE_URL=%q\n' "$rust_dsn"
    printf 'export %s=%q\n' "$redis_variable" "$redis_url"
    # Keep every production limiter enabled in the fixture. The exhaustive
    # matrix intentionally sends far more than the deployed defaults (GA
    # 360/180s, CT 20/1200s, SR 10/60s), so use an explicit shared ceiling
    # rather than disabling middleware or losing the authentication order.
    printf '%s\n' \
      'export SESSION_SECRET=ApiTokenParity-2026!Synthetic-LongEnough' \
      'export CRYPTO_SECRET=ApiTokenParity-Crypto-2026-LongEnough' \
      'export PASSWORD_LOGIN_ENABLED=true' \
      'export GLOBAL_API_RATE_LIMIT_ENABLE=true' \
      'export GLOBAL_API_RATE_LIMIT=100000' \
      'export GLOBAL_API_RATE_LIMIT_DURATION=180' \
      'export CRITICAL_RATE_LIMIT_ENABLE=true' \
      'export CRITICAL_RATE_LIMIT=100000' \
      'export CRITICAL_RATE_LIMIT_DURATION=1200' \
      'export SEARCH_RATE_LIMIT_ENABLE=true' \
      'export SEARCH_RATE_LIMIT=100000' \
      'export SEARCH_RATE_LIMIT_DURATION=3600'
    } >"$file")
  chmod 600 "$file"
}
start_go() {
  [[ -z $go_pid ]] || { echo 'refusing to overwrite live PID record: go_pid' >&2; return 1; }
  write_app_env "$go_env_file" REDIS_CONN_STRING "redis://:$go_valkey_password@127.0.0.1:$go_valkey_port"
  preflight_port Go_HTTP "$go_port"
  (
    # shellcheck disable=SC1090 # Runtime file is created above with mode 0600.
    source "$go_env_file"
    export SQL_DSN REDIS_CONN_STRING SESSION_SECRET CRYPTO_SECRET PASSWORD_LOGIN_ENABLED \
      GLOBAL_API_RATE_LIMIT_ENABLE GLOBAL_API_RATE_LIMIT GLOBAL_API_RATE_LIMIT_DURATION \
      CRITICAL_RATE_LIMIT_ENABLE CRITICAL_RATE_LIMIT CRITICAL_RATE_LIMIT_DURATION \
      SEARCH_RATE_LIMIT_ENABLE SEARCH_RATE_LIMIT SEARCH_RATE_LIMIT_DURATION
    PORT="$go_port" GIN_MODE=release exec "$go_build/legacy-go"
  ) >"$runtime/go.log" 2>&1 &
  record_pid go_pid "$!"
  rm -f -- "$go_env_file"
  wait_for "$go_port" /api/status go_pid
}
start_rust() {
  [[ -z $rust_pid ]] || { echo 'refusing to overwrite live PID record: rust_pid' >&2; return 1; }
  write_app_env "$rust_env_file" VALKEY_URL "redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port"
  preflight_port Rust_HTTP "$rust_port"
  (
    # shellcheck disable=SC1090 # Runtime file is created above with mode 0600.
    source "$rust_env_file"
    export DATABASE_URL VALKEY_URL SESSION_SECRET CRYPTO_SECRET PASSWORD_LOGIN_ENABLED \
      GLOBAL_API_RATE_LIMIT_ENABLE GLOBAL_API_RATE_LIMIT GLOBAL_API_RATE_LIMIT_DURATION \
      CRITICAL_RATE_LIMIT_ENABLE CRITICAL_RATE_LIMIT CRITICAL_RATE_LIMIT_DURATION \
      SEARCH_RATE_LIMIT_ENABLE SEARCH_RATE_LIMIT SEARCH_RATE_LIMIT_DURATION \
      LMM_RS_TEST_VALKEY_PORT
    LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port"
    if [[ $allow_current_oracle == 1 ]]; then
      LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=blue LMM_SCHEMA_CONTRACT=1 VERSION=v0.0.0 exec "$repo_root/apps/api-rust/target/debug/lmm-api-rs"
    else
      LMM_RS_TEST_INSTANCE=1 LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=single LMM_SCHEMA_CONTRACT=1 VERSION=v0.0.0 exec "$repo_root/apps/api-rust/target/debug/lmm-api-rs"
    fi
  ) >"$runtime/rust.log" 2>&1 &
  record_pid rust_pid "$!"
  rm -f -- "$rust_env_file"
  wait_for "$rust_port" /readyz rust_pid
}

assert_static_key_canonicalization_preserves_mismatch

cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
assert_rust_build_inputs_unchanged 'Rust build'
rust_binary_sha256=$(sha256sum "$repo_root/apps/api-rust/target/debug/lmm-api-rs" | awk '{print $1}')
[[ $rust_binary_sha256 =~ ^[[:xdigit:]]{64}$ ]] || { echo 'Rust binary hashing failed' >&2; exit 1; }
# The immutable oracle is intentionally read-only. A plain recursive copy
# keeps the source content while making the disposable Go build tree writable
# for the synthetic web/dist placeholder and compiler cache.
cp -r "$legacy_root/." "$go_build/go-source"
chmod -R u+rwX "$go_build/go-source"
mkdir -p "$go_build/go-source/web/dist"
: >"$go_build/go-source/web/dist/index.html"
(
  cd "$go_build/go-source"
  GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$go_build/legacy-go" .
)
initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
# PostgreSQL has its own disposable data directory, but still must never bind
# over an unrelated listener between initialization and pg_ctl.
preflight_port PostgreSQL "$pg_port"
if ! pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null; then
  sed -n '1,180p' "$runtime/postgres.log" >&2
  exit 1
fi
createdb -h 127.0.0.1 -p "$pg_port" "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" "$rust_database"
start_valkey go-valkey "$go_valkey_port" "$go_valkey_password" "$go_valkey_config" go_valkey_pid
start_valkey rust-valkey "$rust_valkey_port" "$rust_valkey_password" "$rust_valkey_config" rust_valkey_pid

start_go
assert_go_schema_ready
wait_for_go_token_table

# The immutable Go oracle predates the forge console-activation migration,
# while the current route contract (and Rust implementation) permanently
# activates a user's console on the first credential.  Keep that newer schema
# contract explicit in this disposable Go database so the TCP comparison does
# not turn an oracle-era migration gap into a false route mismatch.  This
# trigger is confined to the owned temporary database and is never installed
# in production or in the frozen source tree.
psql -h 127.0.0.1 -p "$pg_port" -d "$go_database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
ALTER TABLE users ADD COLUMN IF NOT EXISTS console_activated_at BIGINT NOT NULL DEFAULT 0;
CREATE OR REPLACE FUNCTION lmm_api_token_activate_console() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  UPDATE users
     SET console_activated_at = EXTRACT(EPOCH FROM NOW())::BIGINT
   WHERE id = NEW.user_id
     AND COALESCE(console_activated_at, 0) = 0;
  RETURN NEW;
END;
$$;
DROP TRIGGER IF EXISTS lmm_api_token_activate_console ON tokens;
CREATE TRIGGER lmm_api_token_activate_console
AFTER INSERT ON tokens
FOR EACH ROW EXECUTE FUNCTION lmm_api_token_activate_console();
SQL

# Keep the Go disposable database on the frozen baseline payment shape even
# when the listener's migration path has not materialized this legacy table.
# The current database user is the same identity used by SQL_DSN below.
# This frozen parity mount bypasses the current dashboard discovery gate; the
# table and grant are reserved for explicit current-policy opt-in tests and
# are not evidence of frozen UserAuth coverage.
psql -h 127.0.0.1 -p "$pg_port" -d "$go_database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE TABLE IF NOT EXISTS top_ups (
  id BIGINT PRIMARY KEY,
  user_id BIGINT,
  amount BIGINT,
  money NUMERIC,
  trade_no VARCHAR(255),
  payment_method VARCHAR(50),
  payment_provider VARCHAR(50) DEFAULT '',
  create_time BIGINT,
  complete_time BIGINT,
  status TEXT
);
CREATE INDEX IF NOT EXISTS idx_top_ups_trade_no ON top_ups (trade_no);
CREATE INDEX IF NOT EXISTS idx_top_ups_user_id ON top_ups (user_id);
GRANT SELECT ON top_ups TO CURRENT_USER;
SQL

psql -h 127.0.0.1 -p "$pg_port" -d "$rust_database" -v ON_ERROR_STOP=1 \
  -v rust_database="$rust_database" -v rust_role="$rust_role" -v rust_schema="$rust_schema" <<'SQL' >/dev/null
CREATE ROLE :"rust_role" LOGIN;
CREATE SCHEMA :"rust_schema" AUTHORIZATION :"rust_role";
ALTER DATABASE :"rust_database" SET search_path TO :"rust_schema";
SET search_path TO :"rust_schema";
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE,1,1);
CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE custom_oauth_providers (id BIGINT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL, icon TEXT, enabled BOOLEAN, client_id TEXT, authorization_endpoint TEXT, scopes TEXT);
CREATE TABLE setups (id BIGINT PRIMARY KEY);
CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT UNIQUE, password TEXT NOT NULL, display_name TEXT, role BIGINT DEFAULT 1, status BIGINT DEFAULT 1, email TEXT, github_id TEXT, discord_id TEXT, wechat_id TEXT, oidc_id TEXT, telegram_id TEXT, access_token TEXT, quota BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0, "group" TEXT DEFAULT 'default', aff_code TEXT, aff_count BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0, aff_history BIGINT DEFAULT 0, inviter_id BIGINT, deleted_at TIMESTAMPTZ, linux_do_id TEXT, setting TEXT DEFAULT '{}', stripe_customer TEXT, last_login_at BIGINT DEFAULT 0, console_activated_at BIGINT NOT NULL DEFAULT 0, auth_version BIGINT NOT NULL DEFAULT 1);
CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT NOT NULL, version BIGINT NOT NULL, user_auth_version BIGINT NOT NULL, status TEXT NOT NULL, refresh_hash CHAR(64) NOT NULL, previous_refresh_hash TEXT, previous_valid_until BIGINT NOT NULL DEFAULT 0, login_method TEXT NOT NULL, ip TEXT, user_agent TEXT, created_at BIGINT NOT NULL, last_active_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revoked_at BIGINT NOT NULL DEFAULT 0, revoked_reason TEXT);
CREATE TABLE two_fas (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL, secret TEXT NOT NULL, is_enabled BOOLEAN NOT NULL DEFAULT FALSE, failed_attempts BIGINT DEFAULT 0, locked_until TIMESTAMPTZ, last_used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
CREATE TABLE casbin_rule (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT);
CREATE TABLE auth_flows (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, token_hash CHAR(64) NOT NULL UNIQUE, purpose TEXT NOT NULL, provider TEXT, intent TEXT, user_id BIGINT, session_id TEXT, payload TEXT, created_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ);
CREATE TABLE two_fa_backup_codes (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL, code_hash TEXT NOT NULL, is_used BOOLEAN DEFAULT FALSE, used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
CREATE SEQUENCE tokens_id_seq;
CREATE TABLE tokens (id BIGINT PRIMARY KEY DEFAULT nextval('tokens_id_seq'), user_id BIGINT NOT NULL, key VARCHAR(128) UNIQUE, status INTEGER DEFAULT 1, name TEXT DEFAULT '', created_time BIGINT DEFAULT 0, accessed_time BIGINT DEFAULT 0, expired_time BIGINT DEFAULT -1, remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN DEFAULT FALSE, model_limits_enabled BOOLEAN DEFAULT FALSE, model_limits TEXT, allow_ips TEXT DEFAULT '', used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN DEFAULT FALSE, auto_groups TEXT, deleted_at TIMESTAMPTZ);
ALTER SEQUENCE tokens_id_seq OWNED BY tokens.id;
-- This baseline table/grant is retained only for explicit current-policy
-- discovery opt-in tests; the frozen parity mount bypasses dashboard discovery
-- and this fixture is not frozen UserAuth coverage evidence.
CREATE TABLE top_ups (id BIGINT PRIMARY KEY, user_id BIGINT, amount BIGINT, money NUMERIC, trade_no VARCHAR(255), payment_method VARCHAR(50), payment_provider VARCHAR(50) DEFAULT '', create_time BIGINT, complete_time BIGINT, status TEXT);
CREATE INDEX idx_top_ups_trade_no ON top_ups (trade_no);
CREATE INDEX idx_top_ups_user_id ON top_ups (user_id);
CREATE TABLE open_source_bounty_projects(id BIGINT,owner_user_id BIGINT,repository_url TEXT,title TEXT,description TEXT,rules TEXT,reward_quota BIGINT,net_reward_quota BIGINT,reward_slots BIGINT,escrow_quota BIGINT,platform_fee_rate_bps BIGINT,platform_fee_quota BIGINT,status TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,published_at TIMESTAMPTZ,closed_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_challenges(id BIGINT,project_id BIGINT,participant_user_id BIGINT,github_handle TEXT,status TEXT,issue_url TEXT,pull_request_url TEXT,submission_note TEXT,review_note TEXT,reward_quota BIGINT,tip_quota BIGINT,owner_rating_score BIGINT,owner_rating_comment TEXT,owner_rated_at TIMESTAMPTZ,contributor_rating_score BIGINT,contributor_rating_comment TEXT,contributor_rated_at TIMESTAMPTZ,owner_rating_overturned BOOLEAN,accepted_at TIMESTAMPTZ,submitted_at TIMESTAMPTZ,reviewed_at TIMESTAMPTZ,rejected_at TIMESTAMPTZ,paid_at TIMESTAMPTZ,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_ledgers(id BIGINT,project_id BIGINT,challenge_id BIGINT,user_id BIGINT,counterparty_user_id BIGINT,kind TEXT,quota BIGINT,note TEXT,reward_payout_key TEXT,recipient_read_at TIMESTAMPTZ,thanked_at TIMESTAMPTZ,created_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_disputes(id BIGINT,challenge_id BIGINT,project_id BIGINT,opened_by_user_id BIGINT,against_user_id BIGINT,reason TEXT,statement TEXT,project_title_snapshot TEXT,repository_url_snapshot TEXT,project_rules_snapshot TEXT,project_escrow_quota_snapshot BIGINT,challenge_status_snapshot TEXT,issue_url_snapshot TEXT,pull_request_url_snapshot TEXT,submission_note_snapshot TEXT,review_note_snapshot TEXT,reward_quota_snapshot BIGINT,tip_quota_snapshot BIGINT,owner_rating_score_snapshot BIGINT,owner_rating_comment_snapshot TEXT,contributor_rating_score_snapshot BIGINT,contributor_rating_comment_snapshot TEXT,status TEXT,resolution TEXT,resolved_by_user_id BIGINT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,resolved_at TIMESTAMPTZ);
GRANT USAGE ON SCHEMA :"rust_schema" TO :"rust_role";
ALTER TABLE open_source_bounty_disputes ADD COLUMN case_key TEXT, ADD COLUMN open_key TEXT;
GRANT SELECT, INSERT, UPDATE, DELETE ON lmm_schema_contract, options, custom_oauth_providers, setups, users, user_sessions, two_fas, casbin_rule, auth_flows, two_fa_backup_codes, tokens, top_ups, open_source_bounty_projects, open_source_bounty_challenges, open_source_bounty_ledgers, open_source_bounty_disputes TO :"rust_role";
GRANT USAGE ON SEQUENCE auth_flows_id_seq, tokens_id_seq TO :"rust_role";
SQL

sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/bounty-forward.sql"
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_database" -v ON_ERROR_STOP=1 \
  -f "$runtime/bounty-forward.sql" >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_database" \
  -v rust_role="$rust_role" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
GRANT SELECT ON open_source_bounty_projects, open_source_bounty_challenges,
  open_source_bounty_ledgers, open_source_bounty_disputes,
  open_source_bounty_mcp_tokens, open_source_bounty_mcp_confirmations,
  open_source_bounty_mcp_operations, open_source_bounty_rest_operations
  TO :"rust_role";
SQL

# The stable password fixture is synthetic and never emitted by this script.
# shellcheck disable=SC2016 # bcrypt hashes contain literal dollar signs.
root_hash='$2a$10$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2'
sql "$go_database" "INSERT INTO users (username,password,display_name,role,status,\"group\",setting,auth_version,quota) VALUES ('root','$root_hash','root',10,1,'default','{}',1,100000000)" >/dev/null
sql "$rust_database" "INSERT INTO users (id,username,password,display_name,role,status,email,\"group\",setting,auth_version,quota) VALUES (1,'root','$root_hash','root',10,1,'','default','{}',1,100000000)" >/dev/null
sql "$go_database" "INSERT INTO users (username,password,display_name,role,status,\"group\",setting,auth_version,quota) VALUES ('other','$root_hash','other',1,1,'default','{}',1,100000000)" >/dev/null
sql "$rust_database" "INSERT INTO users (id,username,password,display_name,role,status,email,\"group\",setting,auth_version,quota) VALUES (2,'other','$root_hash','other',1,1,'','default','{}',1,100000000)" >/dev/null
# This positive paid row is for explicit current-policy discovery opt-in only.
# The frozen parity mount bypasses dashboard discovery, so it is not evidence
# that the frozen UserAuth surface covers paid discovery access.
sql_both "INSERT INTO top_ups (id,user_id,amount,money,trade_no,payment_method,payment_provider,create_time,complete_time,status) VALUES (1,2,100,100,'api-token-parity-stripe-2','stripe','stripe',0,0,'success')"
sql_both "INSERT INTO options (key,value) VALUES ('token_setting','{\"max_user_tokens\":100}'),('QuotaPerUnit','2')"

if ! start_rust; then
  sed -n '1,220p' "$runtime/rust.log" >&2
  exit 1
fi
install_status_only_update_audit
# The frozen Go listener may finish its first-run migration just after its
# lightweight /api/status probe.  That migration's legacy backfill can set
# console_activated_at on the fixture account before the first credential
# scenario, whereas Rust has no asynchronous migration.  Establish the same
# pre-credential zero state after both listeners are ready so the create
# contract compares the route, not startup timing.
sql_both "UPDATE users SET console_activated_at = 0 WHERE username IN ('root','other')"

if [[ $probe_only == 1 ]]; then
  # Exercise both owned listeners with their production middleware mounted,
  # while stopping before the exhaustive scenario matrix.  This is deliberately
  # opt-in so the normal gate retains all nine routes and every test vector.
  curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "http://127.0.0.1:$go_port/api/status" >/dev/null
  curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "http://127.0.0.1:$rust_port/readyz" >/dev/null
  assert_rust_build_inputs_unchanged 'startup probe'
  jq -cn --arg go "$frozen_go_manifest_sha256" --arg rust "$rust_source_sha256" --arg binary "$rust_binary_sha256" --argjson approval_mode "$approval_mode" \
    '{test:"api-token-parity-differential",probe:"startup",approval_mode:($approval_mode == 1),legacy_go_manifest_sha256:$go,rust_build_input_manifest_sha256:$rust,rust_binary_sha256:$binary,isolated:{go_postgres:true,rust_postgres:true,go_valkey:true,rust_valkey:true},rate_limits:{global:100000,critical:100000,search:100000},verification_contract:{non_token_tables:["users","options","user_sessions","auth_flows","custom_oauth_providers","setups","two_fas","casbin_rule","two_fa_backup_codes"],rust_only_table:"lmm_schema_contract",go_schema_contract:"expected_absent",allowed_create_side_effects:["users.console_activated_at"],cache_refresh_scenarios:["cache-prime-del-put"],cache_delete_scenarios:["cache-prime-del-delete","cache-prime-del-batch"],status_only_update_of_columns:["allow_ips","cross_group_retry","expired_time","group","model_limits","model_limits_enabled","name","remain_quota","status","unlimited_quota"]},result:"passed"}'
  exit 0
fi

go_bearer=$(login "http://127.0.0.1:$go_port")
rust_bearer=$(login "http://127.0.0.1:$rust_port")
go_other_bearer=$(login_as "http://127.0.0.1:$go_port" other)
rust_other_bearer=$(login_as "http://127.0.0.1:$rust_port" other)

# Every mapped API-token route must first prove its anonymous behavior. These
# requests use a syntactically non-empty bogus bearer so both legacy stacks
# exercise UserAuth rather than a client-side omission shortcut.
for anonymous_case in \
  'anonymous-list|GET|/api/token/?p=1&size=10|__NONE__|none' \
  'anonymous-search|GET|/api/token/search?keyword=x&p=1&size=10|__NONE__|none' \
  'anonymous-detail|GET|/api/token/1|__NONE__|none' \
  'anonymous-key|POST|/api/token/1/key|{}|application/json' \
  'anonymous-create|POST|/api/token/|{"name":"anonymous"}|application/json' \
  'anonymous-update|PUT|/api/token/|{"id":1,"name":"anonymous"}|application/json' \
  'anonymous-delete|DELETE|/api/token/1|__NONE__|none' \
  'anonymous-batch|POST|/api/token/batch|{"ids":[1]}|application/json' \
  'anonymous-batch-keys|POST|/api/token/batch/keys|{"ids":[1]}|application/json'; do
  IFS='|' read -r anonymous_name anonymous_method anonymous_path anonymous_payload anonymous_content_type <<<"$anonymous_case"
  assert_response_only "$anonymous_name" "$anonymous_method" "$anonymous_path" "$anonymous_payload" "$anonymous_content_type" '' anonymous-bearer anonymous-bearer
done

# Exercise the shared route-differential driver too. It uses an anonymous
# failure request because each real listener intentionally issues a distinct
# dashboard session; the authenticated cases below compare those sessions
# directly while retaining isolated database snapshots.
mkdir -p "$runtime/fixtures" "$runtime/requests"
jq -n '{route:{id:"GET /api/token/ anonymous parity",middleware:["UserAuth"],side_effects:[]},request:{method:"GET",path:"/api/token/?p=1&size=10",headers:{accept:"application/json"},body:null},observe:{db_tables:["tokens"],valkey_patterns:[]},capture_headers:["content-type"],normalization:{rules:[]}}' >"$runtime/requests/api-token-anonymous.json"
"$repo_root/apps/api-rust/tests/scripts/capture-legacy-route-contract.sh" --base-url "http://127.0.0.1:$go_port" --request "$runtime/requests/api-token-anonymous.json" --output "$runtime/fixtures/api-token-anonymous.json" --postgres-url "$go_dsn"
GO_BASE_URL="http://127.0.0.1:$go_port" RUST_BASE_URL="http://127.0.0.1:$rust_port" \
  GO_POSTGRES_URL="$go_dsn" RUST_POSTGRES_URL="$rust_dsn" ROUTE_REQUEST_DIR="$runtime/requests" ROUTE_REQUIRE_EFFECTS=strict \
  "$repo_root/apps/api-rust/tests/scripts/run-route-differential.sh" "$runtime/fixtures"
scenario_total=$((scenario_total + 1))

# Dashboard authorization: guest, ordinary user, and administrator roles;
# enabled, disabled, and banned statuses. None may mutate a token row.
for role in 0 1 10; do
  set_root "$role" 1
  assert_no_token_effect "role-$role" GET '/api/token/?p=1&size=10' __NONE__ none ''
done
for status in 0 1 2; do
  set_root 1 "$status"
  assert_no_token_effect "status-$status" GET '/api/token/?p=1&size=10' __NONE__ none ''
done
set_root 10 1
sql "$go_database" "UPDATE users SET setting='{\"language\":\"zh-TW\"}' WHERE username='root'" >/dev/null
sql "$rust_database" "UPDATE users SET setting='{\"language\":\"zh-TW\"}' WHERE username='root'" >/dev/null
assert_no_token_effect user-setting-locale-batch POST /api/token/batch '{"ids":[1.5]}' application/json en
sql "$go_database" "UPDATE users SET setting='{}' WHERE username='root'" >/dev/null
sql "$rust_database" "UPDATE users SET setting='{}' WHERE username='root'" >/dev/null
flush_valkey
go_bearer=$(login "http://127.0.0.1:$go_port")
rust_bearer=$(login "http://127.0.0.1:$rust_port")

# Go's handler binds JSON independently of Content-Type.  Malformed and
# ill-typed JSON stay failures, while missing/text Content-Type requests
# create the same defaulted tokens as application/json.
for locale in en zh-CN zh-TW; do
  assert_no_token_effect "malformed-$locale" POST /api/token/ '{' application/json "$locale"
done
assert_effect missing-content-type POST /api/token/ '{"name":"missing-content-type"}' none '' 1
assert_effect wrong-content-type POST /api/token/ '{"name":"wrong-content-type"}' text/plain '' 2
oversized_payload_file="$runtime/oversized-body.json"
{
  printf '{"name":"oversized-body","padding":"'
  head -c 2100001 /dev/zero | tr '\0' x
  printf '"}'
} >"$oversized_payload_file"
oversized_payload="@$oversized_payload_file"
assert_effect oversized-body POST /api/token/ "$oversized_payload" application/json '' 3
assert_no_token_effect typed-json POST /api/token/ '{"name":123}' application/json ''
assert_no_token_effect bad-query GET '/api/token/?p=nan&size=wat' __NONE__ none ''
batch_invalid_params_body='{"success":false,"message":"Invalid parameters"}'
for body in 'null' '[]' 'true' '"token"' '1' '{"IDS":null}' '{"ids":[1.5]}' '{"ids":"1"}' '{'; do
  case "$body" in
    null) body_name=4 ;;
    '[]') body_name=top-level-array ;;
    true) body_name=top-level-bool ;;
    '"token"') body_name=top-level-string ;;
    1) body_name=top-level-number ;;
    *) body_name=${#body} ;;
  esac
  assert_no_token_effect "batch-bind-$body_name" POST /api/token/batch "$body" application/json en
  assert_no_token_effect "batch-key-bind-$body_name" POST /api/token/batch/keys "$body" application/json en
  case "$body" in
    null|'[]'|true|'"token"'|1)
      assert_exact_json_response "batch-bind-$body_name" 200 "$batch_invalid_params_body"
      assert_exact_json_response "batch-key-bind-$body_name" 200 "$batch_invalid_params_body"
      ;;
  esac
done
assert_no_token_effect update-malformed-json PUT /api/token/ '{' application/json en
for shape in array bool string number; do
  case "$shape" in
    array) shape_body='[]' ;;
    bool) shape_body='true' ;;
    string) shape_body='"token"' ;;
    number) shape_body='1' ;;
  esac
  if [[ $allow_current_oracle == 1 ]]; then
    shape_error="json: cannot unmarshal $shape into Go value of type controller.tokenRequest"
  else
    shape_error="json: cannot unmarshal $shape into Go value of type model.Token"
  fi
  assert_exact_message "top-level-$shape-create" "$shape_error" POST /api/token/ "$shape_body" application/json en
  assert_exact_message "top-level-$shape-update" "$shape_error" PUT /api/token/ "$shape_body" application/json en
done
assert_no_token_effect batch-localized-type POST /api/token/batch '{"ids":[1.5]}' application/json zh-TW
assert_no_token_effect batch-non-array POST /api/token/batch '{"ids":{}}' application/json en

# Missing fields are intentionally observable legacy behavior. Create applies
# defaults, a normal update applies its zero defaults, and status_only changes
# exactly status even when every other mutable field is absent.
reset_tokens
assert_effect top-level-null-create POST /api/token/ 'null' application/json '' 1
reset_tokens
assert_effect create-missing POST /api/token/ '{"name":"create-missing"}' application/json '' 1
assert_effect create-zero POST /api/token/ '{"name":"create-zero","expired_time":0}' application/json '' 2
assert_effect create-null POST /api/token/ '{"name":"create-null","expired_time":null}' application/json '' 3
assert_response_only generated-key-map POST /api/token/batch/keys '{"ids":[1,2,3]}' application/json ''
for engine in go rust; do
  if ! jq -e '(.data.keys | to_entries | map(.value) | all(test("^[A-Za-z0-9]{48}$")))' \
    "$runtime/$engine.generated-key-map.json" >/dev/null; then
    record_manual_mismatch "generated-key-map: $engine key shape"
  fi
done
assert_response_only auto-groups GET /api/token/auto-groups __NONE__ none ''
assert_effect deleted-at-null-create POST /api/token/ '{"name":"deleted-at-null-create","DeletedAt":null}' application/json '' 4
assert_effect deleted-at-valid-create POST /api/token/ '{"name":"deleted-at-valid-create","DeletedAt":"2026-08-01T12:34:56Z"}' application/json '' 5
assert_no_token_effect deleted-at-invalid-string POST /api/token/ '{"name":"deleted-at-invalid-string","DeletedAt":"not-a-timestamp"}' application/json ''
assert_no_token_effect deleted-at-wrong-type POST /api/token/ '{"name":"deleted-at-wrong-type","DeletedAt":123}' application/json ''
[[ $(sql "$go_database" "SELECT expired_time FROM tokens WHERE name='create-zero'") == -1 ]]
[[ $(sql "$rust_database" "SELECT expired_time FROM tokens WHERE name='create-zero'") == -1 ]]
[[ $(sql "$go_database" "SELECT expired_time FROM tokens WHERE name='create-null'") == -1 ]]
[[ $(sql "$rust_database" "SELECT expired_time FROM tokens WHERE name='create-null'") == -1 ]]
assert_effect update-missing PUT /api/token/ '{"id":1,"name":"update-missing"}' application/json '' 5
assert_effect deleted-at-null-update PUT /api/token/ '{"id":1,"name":"deleted-at-null-update","DeletedAt":null}' application/json '' 5
assert_effect deleted-at-valid-update PUT /api/token/ '{"id":1,"name":"deleted-at-valid-update","DeletedAt":"2026-08-01T12:34:56+00:00"}' application/json '' 5
assert_no_token_effect deleted-at-invalid-update PUT /api/token/ '{"id":1,"name":"deleted-at-invalid-update","DeletedAt":"not-a-timestamp"}' application/json ''
assert_no_token_effect deleted-at-wrong-type-update PUT /api/token/ '{"id":1,"name":"deleted-at-wrong-type-update","DeletedAt":123}' application/json ''

# GORM's encoding/json binder matches DeletedAt case-insensitively. These
# mixed-case vectors freeze that validation and the actual active-row side
# effects: create persists a live row, a normal update applies its documented
# zero defaults plus the intended name, and invalid timestamps never mutate.
reset_tokens
assert_effect mixed-deleted-at-null-create POST /api/token/ '{"name":"mixed-null-create","dElEtEdAt":null}' application/json '' 1
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-null-create "$database" 1 mixed-null-create -1
done
assert_effect mixed-deleted-at-valid-create POST /api/token/ '{"name":"mixed-valid-create","dElEtEdAt":"2026-08-01T12:34:56Z"}' application/json '' 2
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-valid-create "$database" 2 mixed-valid-create -1
done
assert_no_token_effect mixed-deleted-at-invalid-create POST /api/token/ '{"name":"mixed-invalid-create","dElEtEdAt":"not-a-timestamp"}' application/json ''
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-invalid-create "$database" 1 mixed-null-create -1
  assert_active_token_row mixed-deleted-at-invalid-create "$database" 2 mixed-valid-create -1
  if [[ $(sql "$database" "SELECT COUNT(*) FROM tokens WHERE name='mixed-invalid-create'") != 0 ]]; then
    record_manual_mismatch "mixed-deleted-at-invalid-create: $database inserted invalid row"
  fi
done
assert_effect mixed-deleted-at-null-update PUT /api/token/ '{"id":1,"name":"mixed-null-update","dElEtEdAt":null}' application/json '' 2
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-null-update "$database" 1 mixed-null-update 0
done
assert_effect mixed-deleted-at-valid-update PUT /api/token/ '{"id":1,"name":"mixed-valid-update","dElEtEdAt":"2026-08-01T12:34:56+00:00"}' application/json '' 2
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-valid-update "$database" 1 mixed-valid-update 0
done
assert_no_token_effect mixed-deleted-at-invalid-update PUT /api/token/ '{"id":1,"name":"mixed-invalid-update","dElEtEdAt":"not-a-timestamp"}' application/json ''
for database in "$go_database" "$rust_database"; do
  assert_active_token_row mixed-deleted-at-invalid-update "$database" 1 mixed-valid-update 0
  if [[ $(sql "$database" "SELECT COUNT(*) FROM tokens WHERE name='mixed-invalid-update'") != 0 ]]; then
    record_manual_mismatch "mixed-deleted-at-invalid-update: $database mutated invalid row"
  fi
done
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-mixed-null-create',1,'deleted-at-valid-update',10,0,-1,0,false,false,'','',0,'',false),(2,1,'sk-mixed-valid-create',1,'create-zero',10,0,-1,0,false,false,'','',0,'',false),(3,1,'sk-mixed-three',1,'create-null',10,0,-1,0,false,false,'','',0,'',false),(4,1,'sk-mixed-four',1,'deleted-at-null-create',10,0,-1,0,false,false,'','',0,'',false),(5,1,'sk-mixed-five',1,'deleted-at-valid-create',10,0,-1,0,false,false,'','',0,'',false)"
assert_no_token_effect update-negative-id PUT /api/token/ '{"id":-1,"name":"negative-id"}' application/json ''
assert_effect case-insensitive-fields POST /api/token/ '{"NAME":"case-insensitive"}' application/json '' 6
assert_effect name-alias POST /api/token/ '{"Name":"name-alias"}' application/json '' 7
assert_effect unknown-ids-ignored POST /api/token/ '{"name":"unknown-ids-ignored","ids":"not-an-array"}' application/json '' 8
assert_no_token_effect known-unused-field-type POST /api/token/ '{"created_time":"not-an-int","name":"unused-type"}' application/json ''
assert_effect duplicate-fields POST /api/token/ '{"name":"duplicate-first","Name":"duplicate-last"}' application/json '' 9
[[ $(sql "$go_database" "SELECT name FROM tokens WHERE name='duplicate-last'") == duplicate-last ]]
[[ $(sql "$rust_database" "SELECT name FROM tokens WHERE name='duplicate-last'") == duplicate-last ]]
assert_effect trailing-second-json POST /api/token/ '{"name":"trailing-first"}{"name":"trailing-second"}' application/json '' 10
assert_no_token_effect float-int POST /api/token/ '{"remain_quota":1.5}' application/json ''
assert_no_token_effect exponent-int POST /api/token/ '{"remain_quota":1e-1}' application/json ''
assert_no_token_effect overflow-int POST /api/token/ '{"remain_quota":9223372036854775808}' application/json ''
assert_no_token_effect top-level-null-update PUT /api/token/ 'null' application/json ''
before_status_only_go=$(token_snapshot "$go_database")
before_status_only_rust=$(token_snapshot "$rust_database")
assert_status_only_update_columns status-only-missing '/api/token/?status_only=true' '{"id":1,"status":0}' 10
if ! jq -e '.[0].status == 0 and .[0].name == "deleted-at-valid-update"' <(token_snapshot "$go_database") >/dev/null; then
  record_manual_mismatch 'status-only-missing: Go persisted state'
fi
if ! jq -e '.[0].status == 0 and .[0].name == "deleted-at-valid-update"' <(token_snapshot "$rust_database") >/dev/null; then
  record_manual_mismatch 'status-only-missing: Rust persisted state'
fi
if ! [[ $before_status_only_go != "" && $before_status_only_rust != "" ]]; then
  record_manual_mismatch 'status-only-missing: precondition snapshot'
fi

# A mutation is not allowed to merely leave a stale HMAC behind.  Each vector
# inserts the exact healthy token cache shape used by both listeners, then
# requires the target key to disappear after the real HTTP mutation.
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-cache-put',1,'cache-put',10,0,-1,100,false,false,'','',0,'default',false)"
assert_effect cache-prime-del-put PUT '/api/token/?status_only=true' '{"id":1,"status":0}' application/json '' 1 sk-cache-put
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-cache-delete',1,'cache-delete',10,0,-1,100,false,false,'','',0,'default',false)"
assert_effect cache-prime-del-delete DELETE /api/token/1 __NONE__ none '' 0 sk-cache-delete
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-cache-batch-one',1,'cache-batch-one',10,0,-1,100,false,false,'','',0,'default',false),(2,1,'sk-cache-batch-two',1,'cache-batch-two',10,0,-1,100,false,false,'','',0,'default',false)"
assert_effect cache-prime-del-batch POST /api/token/batch '{"ids":[2]}' application/json '' 1 sk-cache-batch-two

# All nine routes, including secret reads and both deletion variants. Seeded
# values make owner scope and side effects comparable without leaking secrets.
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-parity-alpha',1,'alpha',10,0,-1,100,false,false,'','',0,'default',false),(2,1,'sk-parity-beta',1,'beta',10,0,-1,100,false,false,'','',0,'default',false)"
assert_no_token_effect list GET '/api/token/?p=1&size=10' __NONE__ none ''
assert_no_token_effect detail GET /api/token/1 __NONE__ none ''
assert_no_token_effect reveal POST /api/token/1/key '{}' application/json ''
assert_no_token_effect batch-keys POST /api/token/batch/keys '{"ids":[1]}' application/json ''
for path in /api/token/9223372036854775808 /api/token/-9223372036854775809 /api/token/not-an-integer; do
  assert_no_token_effect "detail-id-${path##*/}" GET "$path" __NONE__ none ''
  assert_no_token_effect "key-id-${path##*/}" POST "$path/key" __NONE__ none ''
  assert_no_token_effect "delete-id-${path##*/}" DELETE "$path" __NONE__ none ''
  case "$path" in
    /api/token/9223372036854775808|/api/token/-9223372036854775809)
      assert_exact_json_response "delete-id-${path##*/}" 200 '{"success":false,"message":"record not found"}'
      ;;
  esac
done
assert_effect batch-delete POST /api/token/batch '{"ids":[2]}' application/json '' 1
assert_effect delete DELETE /api/token/1 __NONE__ none '' 0

# A non-admin second user receives its own authenticated session, but must not
# observe or mutate root's tokens. Include mixed owner IDs and repeated writes
# so authorization remains correct after cache fills and denial replay.
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-list GET '/api/token/?p=1&size=10' __NONE__ none ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-search GET '/api/token/search?keyword=root&p=1&size=10' __NONE__ none ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-detail GET /api/token/1 __NONE__ none ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-key POST /api/token/1/key '{}' application/json ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-update PUT /api/token/ '{"id":1,"name":"forbidden"}' application/json ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-delete DELETE /api/token/1 __NONE__ none ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-batch POST /api/token/batch '{"ids":[1]}' application/json ''
seed_cross_user_tokens
assert_cross_user_mixed_effect cross-user-batch-mixed POST /api/token/batch '{"ids":[1,3]}' application/json ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-batch-keys-mixed POST /api/token/batch/keys '{"ids":[1,3]}' application/json ''
seed_cross_user_tokens
assert_cross_user_no_token_effect cross-user-delete-replay DELETE /api/token/1 __NONE__ none ''

# List and search pagination retain Go's odd negative and zero compatibility
# behavior. Search cases cover wildcard counting, short words, Unicode, and
# LIKE escape handling against actual persisted token names.
seed_tokens "INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,1,'sk-page-one',1,'a',10,0,-1,100,false,false,'','',0,'default',false),(2,1,'sk-page-two',1,'a%b',10,0,-1,100,false,false,'','',0,'default',false),(3,1,'sk-page-three',1,'多语令牌',10,0,-1,100,false,false,'','',0,'default',false),(4,1,'sk-page-four',1,'escape!%literal',10,0,-1,100,false,false,'','',0,'default',false)"
for query in 'p=0&size=0' 'p=1&size=1' 'p=2&size=1' 'p=-1&size=1' 'p=1&size=101' 'p=wat&size=wat'; do
  assert_no_token_effect "page-${query//[^[:alnum:]]/_}" GET "/api/token/?$query" __NONE__ none ''
done
for query in 'keyword=%25%25&p=1&size=10' 'keyword=a%25%25b&p=1&size=10' 'keyword=a&p=1&size=1' 'keyword=%E5%A4%9A%E8%AF%AD&p=1&size=10' 'keyword=escape%21%25literal&p=1&size=10' 'keyword=sk-page-one&p=1&size=10' 'token=sk-page-one&p=1&size=10' 'keyword=a&p=wat&size=wat'; do
  assert_no_token_effect "search-${query//[^[:alnum:]]/_}" GET "/api/token/search?$query" __NONE__ none ''
done

# strconv.Atoi overflow is field-specific in the frozen page parser. `p` is
# reparsed on the compatibility path, while ps/size keep Atoi's saturated
# value and page_size only accepts a successful first parse before falling
# through to ps, size, and the default. Freeze each resulting page body rather
# than relying only on Go/Rust equality, and retain the no-write assertion.
overflow_page_items_all=$(jq -cn '[
  {id:4,user_id:1,key:"sk-p**********four",status:1,name:"escape!%literal",created_time:10,accessed_time:0,expired_time:-1,remain_quota:100,unlimited_quota:false,model_limits_enabled:false,model_limits:"",allow_ips:"",used_quota:0,group:"default",cross_group_retry:false,DeletedAt:null},
  {id:3,user_id:1,key:"sk-p**********hree",status:1,name:"多语令牌",created_time:10,accessed_time:0,expired_time:-1,remain_quota:100,unlimited_quota:false,model_limits_enabled:false,model_limits:"",allow_ips:"",used_quota:0,group:"default",cross_group_retry:false,DeletedAt:null},
  {id:2,user_id:1,key:"sk-p**********-two",status:1,name:"a%b",created_time:10,accessed_time:0,expired_time:-1,remain_quota:100,unlimited_quota:false,model_limits_enabled:false,model_limits:"",allow_ips:"",used_quota:0,group:"default",cross_group_retry:false,DeletedAt:null},
  {id:1,user_id:1,key:"sk-p**********-one",status:1,name:"a",created_time:10,accessed_time:0,expired_time:-1,remain_quota:100,unlimited_quota:false,model_limits_enabled:false,model_limits:"",allow_ips:"",used_quota:0,group:"default",cross_group_retry:false,DeletedAt:null}
]')
overflow_page_items_empty='[]'
for route in list search; do
  if [[ $route == list ]]; then
    route_prefix='/api/token/?'
  else
    route_prefix='/api/token/search?'
  fi
  for sign in positive negative; do
    if [[ $sign == positive ]]; then
      overflow=9223372036854775808
      expected_page=9223372036854775807
      expected_page_size=100
    else
      overflow=-9223372036854775809
      expected_page=-9223372036854775808
      expected_page_size=-9223372036854775808
    fi
    assert_no_token_effect "${route}-p-${sign}-overflow" GET "${route_prefix}p=$overflow&p=2&size=1" __NONE__ none ''
    assert_exact_page_response "${route}-p-${sign}-overflow" "$expected_page" 1 "$overflow_page_items_empty"

    assert_no_token_effect "${route}-ps-${sign}-overflow" GET "${route_prefix}p=1&ps=$overflow&ps=3" __NONE__ none ''
    assert_exact_page_response "${route}-ps-${sign}-overflow" 1 "$expected_page_size" "$overflow_page_items_all"
    assert_no_token_effect "${route}-size-${sign}-overflow" GET "${route_prefix}p=1&size=$overflow&size=3" __NONE__ none ''
    assert_exact_page_response "${route}-size-${sign}-overflow" 1 "$expected_page_size" "$overflow_page_items_all"

    assert_no_token_effect "${route}-page-size-ps-${sign}-overflow" GET "${route_prefix}p=1&page_size=$overflow&page_size=4&ps=7" __NONE__ none ''
    assert_exact_page_response "${route}-page-size-ps-${sign}-overflow" 1 7 "$overflow_page_items_all"
    assert_no_token_effect "${route}-page-size-size-${sign}-overflow" GET "${route_prefix}p=1&page_size=$overflow&page_size=4&size=8" __NONE__ none ''
    assert_exact_page_response "${route}-page-size-size-${sign}-overflow" 1 8 "$overflow_page_items_all"
    assert_no_token_effect "${route}-page-size-default-${sign}-overflow" GET "${route_prefix}p=1&page_size=$overflow&page_size=4" __NONE__ none ''
    assert_exact_page_response "${route}-page-size-default-${sign}-overflow" 1 10 "$overflow_page_items_all"
  done
  assert_no_token_effect "${route}-page-size-valid-first-repeated" GET "${route_prefix}p=1&page_size=4&page_size=8" __NONE__ none ''
  assert_exact_page_response "${route}-page-size-valid-first-repeated" 1 4 "$overflow_page_items_all"
done
assert_effect encoded-query-key PUT '/api/token/?%73tatus_only=true' '{"id":1,"status":0}' application/json '' 4

# Inject a fault only into the CountUserTokens query after the row SELECT has
# run. The shared state marker makes a count-first implementation observable:
# it would return the non-zero count instead of Go's ignored-error zero.
install_list_count_fault
assert_response_only list-count-fault GET '/api/token/?p=1&size=10' __NONE__ none ''
for engine in go rust; do
  if ! jq -e '.success == true and .data.total == 0 and (.data.items | length) > 0' \
    "$runtime/$engine.list-count-fault.json" >/dev/null; then
    record_manual_mismatch "list-count-fault: $engine response/order"
  fi
done
remove_list_count_fault

# Search exposes two frozen database-failure messages. Renaming the isolated
# table makes both engines fail at the same query boundary without touching a
# production database: fuzzy search fails during the user-token pre-count,
# while exact search reaches the result-count query.
sql_both 'ALTER TABLE tokens RENAME TO tokens_unavailable'
db_error='ERROR: relation "tokens" does not exist (SQLSTATE 42P01)'
assert_exact_message list-db-failure "$db_error" GET '/api/token/?p=1&size=10' __NONE__ none ''
assert_exact_message detail-db-failure "$db_error" GET /api/token/1 __NONE__ none ''
assert_exact_message key-db-failure "$db_error" POST /api/token/1/key __NONE__ none ''
assert_exact_message create-db-failure "$db_error" POST /api/token/ '{"name":"db-create"}' application/json ''
assert_exact_message update-db-failure "$db_error" PUT /api/token/ '{"id":1,"name":"db-update"}' application/json ''
assert_exact_message delete-db-failure "$db_error" DELETE /api/token/1 __NONE__ none ''
assert_exact_message batch-delete-db-failure "$db_error" POST /api/token/batch '{"ids":[1]}' application/json ''
assert_exact_message batch-keys-db-failure "$db_error" POST /api/token/batch/keys '{"ids":[1]}' application/json ''
assert_exact_message search-fuzzy-count-db-failure '获取令牌数量失败' GET '/api/token/search?keyword=db%25&p=1&size=10' __NONE__ none ''
assert_exact_message search-exact-db-failure '搜索令牌失败' GET '/api/token/search?keyword=db-fault&p=1&size=10' __NONE__ none ''
sql_both 'ALTER TABLE tokens_unavailable RENAME TO tokens'

if (( scenario_total != expected_scenarios )); then
  echo "scenario contract failed: expected $expected_scenarios, got $scenario_total" >&2
  exit 1
fi
assert_rust_build_inputs_unchanged 'differential run'
emit_evidence() {
  local result=$1
  local summary
  summary=$(jq -cn \
    --arg result "$result" \
    --arg legacy_revision "$legacy_revision" \
    --arg frozen_go_manifest_sha256 "$frozen_go_manifest_sha256" \
    --arg rust_source_sha256 "$rust_source_sha256" \
    --arg rust_binary_sha256 "$rust_binary_sha256" \
    --argjson approval_mode "$approval_mode" \
    --argjson scenarios "$scenario_total" \
    --argjson exact_matches "$((scenario_total - scenario_mismatch_count))" \
    --argjson mismatch_assertions "$mismatch_count" \
    '{test:"api-token-parity-differential",routes:10,approval_mode:($approval_mode == 1),probe_only:false,scenarios:$scenarios,exact_matches:$exact_matches,mismatch_assertions:$mismatch_assertions,legacy_go_revision:$legacy_revision,legacy_go_manifest_sha256:$frozen_go_manifest_sha256,rust_build_input_manifest_sha256:$rust_source_sha256,rust_binary_sha256:$rust_binary_sha256,verification_contract:{non_token_tables:["users","options","user_sessions","auth_flows","custom_oauth_providers","setups","two_fas","casbin_rule","two_fa_backup_codes"],rust_only_table:"lmm_schema_contract",go_schema_contract:"expected_absent",cache_prime_delete_scenarios:["cache-prime-del-put","cache-prime-del-delete","cache-prime-del-batch"],status_only_update_of_columns:["allow_ips","cross_group_retry","expired_time","group","model_limits","model_limits_enabled","name","remain_quota","status","unlimited_quota"]},result:$result,mismatches:$ARGS.positional}' \
    --args "${mismatch_names[@]}")
  if [[ -n $result_dir && $result == passed ]]; then
    printf '%s\n' "$summary" >"$result_dir/api-token-parity-summary.json"
    while IFS=$'\t' read -r method route; do
      local slug
      slug=$(printf '%s-%s' "$method" "$route" | tr '[:upper:]' '[:lower:]' | sed -E 's#[^a-z0-9]+#_#g; s/^_+//; s/_+$//')
      jq -cn --arg method "$method" --arg route "$route" \
        '{method:$method,path:$route,differential_verified:true,differential_scope:"api-token",cases:168,isolated_runtime:true,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
        >"$result_dir/api-token-$slug.json"
    done <<'ROUTES'
GET	/api/token/
GET	/api/token/search
GET	/api/token/:id
POST	/api/token/:id/key
POST	/api/token/
PUT	/api/token/
DELETE	/api/token/:id
POST	/api/token/batch
POST	/api/token/batch/keys
GET	/api/token/auto-groups
ROUTES
  fi
  printf '%s\n' "$summary"
}
if (( mismatch_count > 0 )); then
  emit_evidence failed
  exit 1
fi
emit_evidence passed
