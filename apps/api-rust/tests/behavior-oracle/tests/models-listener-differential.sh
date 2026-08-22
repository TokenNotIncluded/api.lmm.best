#!/usr/bin/env bash
# Real-listener differential for GET /v1/models. Both implementations receive
# the same synthetic fixture, but retain their production database engines.
set -euo pipefail
# Never leak generated listener credentials when this runner is invoked through
# `bash -x` by a CI wrapper.
set +x

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
runtime_root=${LMM_MODELS_TEST_RUNTIME_ROOT:-/tmp}
crypto_secret='models-oracle-crypto-secret-2026'
session_secret='ModelsListener-2026!FixedSyntheticSecret'
rust_binary=${LMM_MODELS_RUST_BINARY:-"$repo_root/apps/api-rust/target/debug/lmm-api-rs"}
curl_connect_timeout=2
curl_max_time=15
approval_mode=${LMM_MODELS_PARITY_APPROVAL:-0}
probe_only=${LMM_MODELS_PARITY_PROBE_ONLY:-0}
expected_scenarios=32
frozen_go_manifest_sha256=
rust_build_input_manifest_sha256=
rust_build_input_manifest_sha256_after=
rust_binary_sha256=
scenario_total=0
pg_pid=
# shellcheck disable=SC2034 # Indirectly referenced by ownership helpers.
pg_pid_start=
go_pid=
rust_pid=
# shellcheck disable=SC2034 # Indirectly referenced by ownership helpers.
go_valkey_pid=
# shellcheck disable=SC2034 # Indirectly referenced by ownership helpers.
rust_valkey_pid=
cleanup_started=false
go_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
rust_valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')

case "$approval_mode" in 0|1) ;; *) echo 'LMM_MODELS_PARITY_APPROVAL must be 0 or 1' >&2; exit 2 ;; esac
case "$probe_only" in 0|1) ;; *) echo 'LMM_MODELS_PARITY_PROBE_ONLY must be 0 or 1' >&2; exit 2 ;; esac
if [[ $approval_mode == 1 && $probe_only == 1 ]]; then
  echo 'approval mode refuses probe-only; run the complete models alias matrix' >&2
  exit 2
fi
runtime=$(mktemp -d "$runtime_root/lmm-models-listener.XXXXXX")
early_runtime_cleanup() {
  # This guard is installed immediately after mktemp. It covers every
  # preflight/hash/probe failure before the full process-aware cleanup exists.
  case "$runtime" in
    "$runtime_root"/lmm-models-listener.*) rm -rf -- "$runtime" ;;
    *) echo "refusing unexpected early models runtime: $runtime" >&2 ;;
  esac
}
trap early_runtime_cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

random_free_port() {
  local port
  for _ in {1..100}; do
    port=$(shuf -i 20000-60000 -n 1)
    [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] && { printf '%s\n' "$port"; return 0; }
  done
  echo 'unable to select an unused isolated TCP port' >&2
  return 1
}
pg_port=${LMM_MODELS_TEST_PG_PORT:-$(random_free_port)}
go_port=${LMM_MODELS_TEST_GO_PORT:-$(random_free_port)}
rust_port=${LMM_MODELS_TEST_RUST_PORT:-$(random_free_port)}
go_valkey_port=${LMM_MODELS_TEST_GO_VALKEY_PORT:-$(random_free_port)}
rust_valkey_port=${LMM_MODELS_TEST_RUST_VALKEY_PORT:-$(random_free_port)}

pid_start_time() {
  local pid=$1
  [[ -r /proc/$pid/stat ]] || return 1
  awk '{print $22}' "/proc/$pid/stat"
}
record_pid() {
  local name=$1 pid=$2 start
  printf -v "$name" '%s' "$pid"
  start=$(pid_start_time "$pid") || { wait "$pid" 2>/dev/null || true; printf -v "$name" ''; printf -v "${name}_start" ''; return 1; }
  printf -v "${name}_start" '%s' "$start"
}
owned_pid_is_live() {
  local name=$1 start_name pid start
  start_name="${name}_start"
  pid=${!name:-}
  start=${!start_name:-}
  [[ -n $pid && -n $start ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$start" ]]
}
stop_owned_process() {
  local name=$1 port=${2:-} pid=${!1:-}
  if [[ -n $pid ]]; then
    if owned_pid_is_live "$name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
    else echo "cleanup: $name: refusing to signal state=$(process_state "$name" "$port") pid=$pid" >&2; fi
  fi
  printf -v "$name" ''
  printf -v "${name}_start" ''
}
stop_owned_postgres() {
  local current_pid=
  if [[ -r $runtime/pg/postmaster.pid ]]; then current_pid=$(sed -n '1p' "$runtime/pg/postmaster.pid" 2>/dev/null || true); fi
  if [[ -n ${pg_pid:-} ]]; then
    if [[ $current_pid == "$pg_pid" ]] && owned_pid_is_live pg_pid && listener_owned_by "$pg_port" "$pg_pid"; then
      pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
    else
      echo "cleanup: PostgreSQL: refusing to signal state=$(process_state pg_pid "$pg_port") pid=${pg_pid:-<empty>}" >&2
    fi
  fi
  pg_pid=
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
process_state() {
  local name=$1 port=${2:-} pid actual want line
  pid=${!name:-}; want=${name}_start; want=${!want:-}
  actual=$(pid_start_time "$pid" 2>/dev/null || true)
  [[ -z $actual ]] && { echo child-exited; return; }
  [[ $actual != "$want" ]] && { echo pid-start-time-mismatch; return; }
  if [[ -n $port ]]; then
    line=$(ss -H -ltnp "sport = :$port" 2>/dev/null || true)
    if [[ -n $line ]] && ! listener_owned_by "$port" "$pid"; then echo port-owned-by-other; return; fi
  fi
  [[ -n $port && -z $line ]] && { echo child-not-listening; return; }
  echo child-not-owned
}
preflight_port() {
  local label=$1 port=$2
  [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] || { echo "$label port already occupied: 127.0.0.1:$port" >&2; exit 1; }
}
assert_distinct_ports() {
  local current count
  for current in "$@"; do
    [[ $current =~ ^[1-9][0-9]{0,4}$ && $current -le 65535 ]] || { echo "invalid port: $current" >&2; exit 1; }
  done
  count=$(printf '%s\n' "$@" | LC_ALL=C sort -u | wc -l | tr -d ' ')
  [[ $count == "$#" ]] || { echo 'ports must be pairwise distinct' >&2; exit 1; }
}
assert_frozen_inputs() {
  [[ -d $legacy_root && -f $legacy_root/SHA256SUMS && -f $legacy_root/GIT-LS-FILES-S.tsv ]] || { echo 'frozen Go archive or manifest missing' >&2; return 1; }
  (cd "$legacy_root" && sha256sum --check --status SHA256SUMS) || { echo 'frozen Go content hash check failed' >&2; return 1; }
  frozen_go_manifest_sha256=$(sha256sum "$legacy_root/SHA256SUMS" "$legacy_root/GIT-LS-FILES-S.tsv" | sha256sum | awk '{print $1}')
  rust_build_input_manifest_sha256=$(rust_build_input_manifest | sha256sum | awk '{print $1}')
  [[ $frozen_go_manifest_sha256 =~ ^[[:xdigit:]]{64}$ && $rust_build_input_manifest_sha256 =~ ^[[:xdigit:]]{64}$ ]] || { echo 'source hash calculation failed' >&2; return 1; }
}
rust_build_input_manifest() {
  # `find`, not `git ls-files`, is deliberate: a local untracked Rust source,
  # asset, build script, or path-crate file can affect `cargo build` and must
  # invalidate this evidence just as a tracked file does.
  local input file relative digest
  local -a roots=(
    "$repo_root/apps/api-rust"
    "$repo_root/apps/api-rust/crates/application"
    "$repo_root/apps/api-rust/crates/contracts"
    "$repo_root/apps/api-rust/crates/domain"
    "$repo_root/apps/api-rust/crates/observability"
  )
  {
    for input in "$repo_root/apps/api-rust/Cargo.toml" "$repo_root/apps/api-rust/Cargo.lock"; do
      [[ -f $input ]] && printf '%s\0' "$input"
    done
    for input in "$repo_root/apps/api-rust/.cargo" "$repo_root/apps/api-rust/rust-toolchain.toml" "$repo_root/apps/api-rust/rust-toolchain"; do
      if [[ -d $input ]]; then find "$input" -type f -print0
      elif [[ -f $input ]]; then printf '%s\0' "$input"
      fi
    done
    for input in "${roots[@]}"; do
      [[ -d $input ]] || { echo "required Rust build input root missing: $input" >&2; return 1; }
      find "$input" -type f ! -path '*/target/*' -print0
    done
  } | LC_ALL=C sort -zu | while IFS= read -r -d '' file; do
    relative=${file#"$repo_root/"}
    digest=$(sha256sum -- "$file" | awk '{print $1}')
    printf '%s  %s\n' "$digest" "$relative"
  done | LC_ALL=C sort
}
assert_frozen_inputs
if [[ $probe_only == 1 ]]; then
  if [[ -f $rust_binary ]]; then rust_binary_sha256=$(sha256sum -- "$rust_binary" | awk '{print $1}'); else rust_binary_sha256=null; fi
  jq -cn --arg go_content_sha256 "$frozen_go_manifest_sha256" --arg rust_build_input_manifest_sha256 "$rust_build_input_manifest_sha256" \
    --arg rust_binary_sha256 "$rust_binary_sha256" \
    '{test:"models-listener-differential",mode:"probe",frozen_go_content_sha256:$go_content_sha256,rust_build_input_manifest_sha256:$rust_build_input_manifest_sha256,rust_binary_sha256:(if $rust_binary_sha256 == "null" then null else $rust_binary_sha256 end),build_input_roots:["apps/api-rust/Cargo.toml","apps/api-rust/Cargo.lock","apps/api-rust","apps/api-rust/crates/application","apps/api-rust/crates/contracts","apps/api-rust/crates/domain","apps/api-rust/crates/observability"],result:"passed"}'
  exit 0
fi
assert_distinct_ports "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port"
for entry in "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port"; do preflight_port "${entry%%:*}" "${entry##*:}"; done

cleanup() {
  local exit_code=$? preserve=0
  [[ $cleanup_started == false ]] || return "$exit_code"
  cleanup_started=true
  [[ $exit_code -ne 0 || ${LMM_KEEP_MODELS_LISTENER_RUNTIME:-0} == 1 ]] && preserve=1
  stop_owned_process go_pid "$go_port"
  stop_owned_process rust_pid "$rust_port"
  stop_owned_process go_valkey_pid "$go_valkey_port"
  stop_owned_process rust_valkey_pid "$rust_valkey_port"
  [[ -d "$runtime/pg" ]] && stop_owned_postgres
  case "$runtime" in
    "$runtime_root"/lmm-models-listener.*)
      if [[ $preserve == 1 ]]; then
        echo "preserved models listener runtime: $runtime" >&2
      else
        rm -rf "$runtime"
      fi
      ;;
    *) echo "refusing unexpected models runtime: $runtime" >&2 ;;
  esac
  return "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
trap 'echo "models listener differential failed at line $LINENO" >&2' ERR

for command in cargo curl git go initdb jq openssl pg_ctl postgres psql sqlite3 ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command is unavailable: $command" >&2; exit 1; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }

if [[ ${LMM_MODELS_SKIP_BUILD:-0} != 1 ]]; then
  cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
fi
[[ -x $rust_binary ]] || { echo "Rust listener binary is missing or not executable: $rust_binary" >&2; exit 1; }
rust_binary_sha256=$(sha256sum -- "$rust_binary" | awk '{print $1}')
[[ $rust_binary_sha256 =~ ^[[:xdigit:]]{64}$ ]] || { echo 'Rust binary hash calculation failed' >&2; exit 1; }
cp -a "$legacy_root/." "$runtime/go-source"
# The frozen archive is intentionally immutable; the disposable build copy
# must still be writable so the embedded frontend placeholder can be created.
chmod -R u+rwX -- "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: >"$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .
)

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
preflight_port PostgreSQL "$pg_port"
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
pg_pid=$(sed -n '1p' "$runtime/pg/postmaster.pid")
[[ $pg_pid =~ ^[1-9][0-9]*$ ]] || { echo 'PostgreSQL did not publish a valid postmaster PID' >&2; exit 1; }
record_pid pg_pid "$pg_pid"
if ! owned_pid_is_live pg_pid || ! listener_owned_by "$pg_port" "$pg_pid"; then
  echo 'PostgreSQL did not bind as owned child' >&2
  exit 1
fi
createdb -h 127.0.0.1 -p "$pg_port" lmm_test_models_rust
start_valkey() {
  local name=$1 port=$2 password=$3 pid_name=$4 config pid
  config="$runtime/$name-valkey.conf"
  preflight_port "$name Valkey" "$port"
  umask 077
  printf 'bind 127.0.0.1\nport %s\nprotected-mode yes\nrequirepass %s\nsave \"\"\nappendonly no\ndaemonize no\ndir %s\nlogfile %s\n' \
    "$port" "$password" "$runtime" "$runtime/$name-valkey.log" >"$config"
  printf 'unixsocket %s\nunixsocketperm 600\n' "$runtime/$name-valkey.sock" >>"$config"
  valkey-server "$config" >"$runtime/$name-valkey.stderr" 2>&1 &
  pid=$!
  record_pid "$pid_name" "$pid"
  for _ in {1..100}; do
    owned_pid_is_live "$pid_name" && listener_owned_by "$port" "$pid" && break
    sleep .05
  done
  if ! owned_pid_is_live "$pid_name" || ! listener_owned_by "$port" "$pid"; then
    echo "$name Valkey did not bind as owned child" >&2
    return 1
  fi
  VALKEYCLI_AUTH="$password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" ping >/dev/null
}
go_valkey() { VALKEYCLI_AUTH="$go_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$go_valkey_port" "$@"; }
rust_valkey() { VALKEYCLI_AUTH="$rust_valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$rust_valkey_port" "$@"; }
valkey_for_port() {
  case "$1" in
    "$go_valkey_port") shift; go_valkey "$@" ;;
    "$rust_valkey_port") shift; rust_valkey "$@" ;;
    *) echo "unknown isolated Valkey port: $1" >&2; return 1 ;;
  esac
}
flush_valkey() { go_valkey FLUSHDB >/dev/null; rust_valkey FLUSHDB >/dev/null; }
go_redis_startup_diagnostics() {
  local ping=failed
  if VALKEYCLI_AUTH="$go_valkey_password" valkey-cli --no-auth-warning --command-timeout 1 -h 127.0.0.1 -p "$go_valkey_port" ping >/dev/null 2>&1; then ping=ok; fi
  {
    echo 'Go listener startup diagnostics (credentials redacted):'
    echo "go_http_endpoint=127.0.0.1:$go_port"
    echo "go_redis_endpoint=127.0.0.1:$go_valkey_port"
    echo "go_redis_authenticated_ping=$ping"
    echo 'go_redis_listener_state:'
    ss -H -ltnp "sport = :$go_valkey_port" 2>/dev/null || true
    echo 'go_redis_connection_state:'
    ss -H -tnp "sport = :$go_valkey_port or dport = :$go_valkey_port" 2>/dev/null || true
    echo 'go.log tail:'
    tail -n 120 "$runtime/go.log" 2>/dev/null || true
    echo 'go-valkey.log tail:'
    tail -n 80 "$runtime/go-valkey.log" 2>/dev/null || true
  } >&2
}
start_valkey go "$go_valkey_port" "$go_valkey_password" go_valkey_pid
start_valkey rust "$rust_valkey_port" "$rust_valkey_password" rust_valkey_pid

start_go_listener() {
  preflight_port Go_HTTP "$go_port"
  SQL_DSN=local SQLITE_PATH="$runtime/legacy.db?_busy_timeout=30000" PORT="$go_port" \
    REDIS_CONN_STRING="unix://:$go_valkey_password@$runtime/go-valkey.sock" CRYPTO_SECRET="$crypto_secret" \
    SESSION_SECRET="$session_secret" SYNC_FREQUENCY=60 GLOBAL_API_RATE_LIMIT_ENABLE=false GIN_MODE=release \
    "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
  local child=$!
  if ! record_pid go_pid "$child"; then
    go_redis_startup_diagnostics
    return 1
  fi
  # The first Go boot performs the full SQLite migration before binding. On a
  # constrained shared CI/desktop host that can exceed the old 15-second
  # ownership window even though the listener is healthy and progressing.
  for _ in {1..6000}; do
    owned_pid_is_live go_pid && listener_owned_by "$go_port" "$go_pid" && break
    sleep .05
  done
  if ! owned_pid_is_live go_pid || ! listener_owned_by "$go_port" "$go_pid"; then
    echo 'Go listener did not bind as owned child' >&2
    go_redis_startup_diagnostics
    return 1
  fi
  if ! curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "http://127.0.0.1:$go_port/api/status" >/dev/null; then
    go_redis_startup_diagnostics
    return 1
  fi
}

stop_go_listener() {
  stop_owned_process go_pid "$go_port"
}

# First boot creates Go's native SQLite schema. The full logical fixture must
# exist before the final listener starts: Options, channel abilities, and
# pricing are read into Go startup caches and cannot be made equivalent by
# direct database writes after readiness.
start_go_listener
stop_go_listener
sqlite3 -cmd '.timeout 30000' "$runtime/legacy.db" <<'SQL'
INSERT OR REPLACE INTO options (key, value) VALUES
  ('SelfUseModeEnabled', 'false'),
  ('ModelRatio', '{"gpt-4o":1,"text-embedding-3-small":1}'),
  ('UserUsableGroups', '{"auto":"auto","default":"default","unavailable":"unavailable","deprecated":"deprecated"}'),
  ('AutoGroups', '["vip","default","unavailable"]'),
  ('GroupRatio', '{"vip":1,"default":1,"unavailable":1}'),
  ('group_ratio_setting.group_special_usable_group', '{"default":{"+:vip":"vip","-:unavailable":""}}'),
  ('billing_setting.billing_mode', '{"dynamic-tiered-model":"tiered_expr","vip-tiered-model":"tiered_expr","unavailable-tiered-model":"tiered_expr"}'),
  ('billing_setting.billing_expr', '{"dynamic-tiered-model":"tier(\"base\", p)","vip-tiered-model":"tier(\"base\", p)","unavailable-tiered-model":"tier(\"base\", p)"}');
SQL
sqlite3 -cmd '.timeout 30000' "$runtime/legacy.db" <<'SQL'
INSERT INTO users (id, username, password, role, status, email, quota, "group", setting, auth_version)
VALUES (42, 'oracle-model-user', 'unused', 1, 1, '', 0, 'default', '{}', 1);
INSERT INTO tokens (id, user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry)
VALUES (73, 42, 'oraclemodelstoken', 1, '', 0, 0, -1, 0, 1, 0, '', '', 0, 'auto', 0);
INSERT INTO tokens (id, user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry)
VALUES (74, 42, 'oracleallowedtoken', 1, '', 0, 0, -1, 0, 1, 0, '', '', 0, 'vip', 0), (75, 42, 'oracleforbiddentoken', 1, '', 0, 0, -1, 0, 1, 0, '', '', 0, 'forbidden', 0), (76, 42, 'oracledeprecatedtoken', 1, '', 0, 0, -1, 0, 1, 0, '', '', 0, 'deprecated', 0);
INSERT INTO channels (id, type, key, status) VALUES (101, 1, 'synthetic-key', 1);
INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight)
VALUES ('vip', 'vip-tiered-model', 101, 1, 30, 10), ('default', 'dynamic-tiered-model', 101, 1, 20, 10), ('default', 'gpt-4o', 101, 1, 15, 10), ('default', 'text-embedding-3-small', 101, 1, 10, 10), ('default', 'gpt-hardcoded-prefix-only', 101, 1, 5, 10), ('unavailable', 'unavailable-tiered-model', 101, 1, 1, 10);
SQL
go_valkey FLUSHDB >/dev/null
start_go_listener

psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE SCHEMA IF NOT EXISTS lmm_test_models_rust;
ALTER DATABASE lmm_test_models_rust SET search_path TO lmm_test_models_rust;
SET search_path TO lmm_test_models_rust;
CREATE ROLE lmm_test_models_runtime LOGIN;
ALTER SCHEMA lmm_test_models_rust OWNER TO lmm_test_models_runtime;
SET search_path TO lmm_test_models_rust;
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE custom_oauth_providers (id BIGINT PRIMARY KEY, name TEXT NOT NULL, slug TEXT NOT NULL, icon TEXT, enabled BOOLEAN, client_id TEXT, authorization_endpoint TEXT, scopes TEXT);
CREATE TABLE setups (id BIGINT PRIMARY KEY);
CREATE TABLE users (id BIGINT PRIMARY KEY, username TEXT UNIQUE, password TEXT NOT NULL, display_name TEXT, role BIGINT DEFAULT 1, status INTEGER DEFAULT 1, email TEXT DEFAULT '', github_id TEXT, discord_id TEXT, oidc_id TEXT, wechat_id TEXT, telegram_id TEXT, access_token TEXT, quota BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0, "group" VARCHAR(64) DEFAULT 'default', aff_code TEXT, aff_count BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0, aff_history BIGINT DEFAULT 0, inviter_id BIGINT, linux_do_id TEXT, setting TEXT, stripe_customer TEXT, last_login_at BIGINT DEFAULT 0, console_activated_at BIGINT NOT NULL DEFAULT 0, auth_version BIGINT DEFAULT 1, deleted_at TIMESTAMPTZ);
CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT, version BIGINT, user_auth_version BIGINT, status TEXT, refresh_hash CHAR(64), previous_refresh_hash TEXT, previous_valid_until BIGINT, login_method TEXT, ip TEXT, user_agent TEXT, created_at BIGINT, last_active_at BIGINT, expires_at BIGINT, revoked_at BIGINT, revoked_reason TEXT);
CREATE TABLE two_fas (id BIGINT PRIMARY KEY, user_id BIGINT, is_enabled BOOLEAN, deleted_at TIMESTAMPTZ);
CREATE TABLE casbin_rule (id BIGINT PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT);
CREATE TABLE auth_flows (id BIGINT, token_hash CHAR(64) NOT NULL, purpose VARCHAR(32) NOT NULL, user_id BIGINT, payload TEXT, created_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ);
CREATE SEQUENCE tokens_id_seq;
CREATE TABLE tokens (id BIGINT PRIMARY KEY DEFAULT nextval('tokens_id_seq'), user_id BIGINT NOT NULL, key VARCHAR(128) UNIQUE, name TEXT DEFAULT '', created_time BIGINT DEFAULT 0, accessed_time BIGINT DEFAULT 0, status INTEGER DEFAULT 1, expired_time BIGINT DEFAULT -1, remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN DEFAULT FALSE, model_limits_enabled BOOLEAN DEFAULT FALSE, model_limits TEXT, allow_ips TEXT DEFAULT '', used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN DEFAULT FALSE, deleted_at TIMESTAMPTZ);
ALTER SEQUENCE tokens_id_seq OWNED BY tokens.id;
CREATE TABLE channels (id BIGINT PRIMARY KEY, type INTEGER DEFAULT 0, status INTEGER DEFAULT 1);
CREATE TABLE abilities ("group" VARCHAR(64), model VARCHAR(255), channel_id BIGINT, enabled BOOLEAN, priority INTEGER DEFAULT 0, weight INTEGER DEFAULT 0, PRIMARY KEY ("group", model, channel_id));
ALTER SCHEMA lmm_test_models_rust OWNER TO lmm_test_models_runtime;
GRANT USAGE ON SCHEMA lmm_test_models_rust TO lmm_test_models_runtime;
GRANT SELECT ON lmm_schema_contract, options, custom_oauth_providers, setups, users, user_sessions, two_fas, casbin_rule, tokens, channels, abilities TO lmm_test_models_runtime;
GRANT UPDATE ON users TO lmm_test_models_runtime;
GRANT INSERT, UPDATE ON auth_flows TO lmm_test_models_runtime;
GRANT INSERT, UPDATE, DELETE ON tokens TO lmm_test_models_runtime;
GRANT USAGE ON SEQUENCE tokens_id_seq TO lmm_test_models_runtime;
SQL
sed "s/__LMM_APP_SCHEMA__/lmm_test_models_rust/g" \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/bounty-forward.sql"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -v ON_ERROR_STOP=1 \
  -f "$runtime/bounty-forward.sql" >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
GRANT SELECT ON open_source_bounty_projects, open_source_bounty_challenges,
  open_source_bounty_ledgers, open_source_bounty_disputes,
  open_source_bounty_mcp_tokens, open_source_bounty_mcp_confirmations,
  open_source_bounty_mcp_operations, open_source_bounty_rest_operations
  TO lmm_test_models_runtime;
SQL
export PGOPTIONS='-csearch_path=lmm_test_models_rust'
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
SET search_path TO lmm_test_models_rust;
INSERT INTO options (key, value) VALUES
  ('SelfUseModeEnabled', 'false'),
  ('ModelRatio', '{"gpt-4o":1,"text-embedding-3-small":1}'),
  ('UserUsableGroups', '{"auto":"auto","default":"default","unavailable":"unavailable","deprecated":"deprecated"}'),
  ('AutoGroups', '["vip","default","unavailable"]'),
  ('GroupRatio', '{"vip":1,"default":1,"unavailable":1}'),
  ('group_ratio_setting.group_special_usable_group', '{"default":{"+:vip":"vip","-:unavailable":""}}'),
  ('billing_setting.billing_mode', '{"dynamic-tiered-model":"tiered_expr","vip-tiered-model":"tiered_expr","unavailable-tiered-model":"tiered_expr"}'),
  ('billing_setting.billing_expr', '{"dynamic-tiered-model":"tier(\"base\", p)","vip-tiered-model":"tier(\"base\", p)","unavailable-tiered-model":"tier(\"base\", p)"}');
INSERT INTO users (id, username, password, role, status, email, quota, "group", setting, auth_version)
VALUES (42, 'oracle-model-user', 'unused', 1, 1, '', 0, 'default', '{}', 1);
INSERT INTO tokens (id, user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry)
VALUES (73, 42, 'oraclemodelstoken', 1, '', 0, 0, -1, 0, TRUE, FALSE, '', '', 0, 'auto', FALSE);
INSERT INTO tokens (id, user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, "group", cross_group_retry)
VALUES (74, 42, 'oracleallowedtoken', 1, '', 0, 0, -1, 0, TRUE, FALSE, '', '', 0, 'vip', FALSE), (75, 42, 'oracleforbiddentoken', 1, '', 0, 0, -1, 0, TRUE, FALSE, '', '', 0, 'forbidden', FALSE), (76, 42, 'oracledeprecatedtoken', 1, '', 0, 0, -1, 0, TRUE, FALSE, '', '', 0, 'deprecated', FALSE);
INSERT INTO channels (id, type, status) VALUES (101, 1, 1);
INSERT INTO abilities ("group", model, channel_id, enabled, priority, weight)
VALUES ('vip', 'vip-tiered-model', 101, TRUE, 30, 10), ('default', 'dynamic-tiered-model', 101, TRUE, 20, 10), ('default', 'gpt-4o', 101, TRUE, 15, 10), ('default', 'text-embedding-3-small', 101, TRUE, 10, 10), ('default', 'gpt-hardcoded-prefix-only', 101, TRUE, 5, 10), ('unavailable', 'unavailable-tiered-model', 101, TRUE, 1, 10);
SQL

preflight_port Rust_HTTP "$rust_port"
DATABASE_URL="postgresql://lmm_test_models_runtime@127.0.0.1:$pg_port/lmm_test_models_rust?options=-csearch_path%3Dlmm_test_models_rust" \
  VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port" LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" \
  LMM_RS_TEST_INSTANCE=1 LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" LMM_RS_SLOT=single LMM_SCHEMA_CONTRACT=1 CRYPTO_SECRET="$crypto_secret" SESSION_SECRET="$session_secret" \
  LMM_MODELS_CACHE_TTL_SECONDS=60 PASSWORD_LOGIN_ENABLED=false GLOBAL_API_RATE_LIMIT_ENABLE=false \
  CRITICAL_RATE_LIMIT_ENABLE=false AUTH_COOKIE_SECURE=false VERSION=v0.0.0 \
  "$rust_binary" >"$runtime/rust.log" 2>&1 &
record_pid rust_pid "$!"
for _ in {1..300}; do
  owned_pid_is_live rust_pid && listener_owned_by "$rust_port" "$rust_pid" && break
  sleep .05
done
if ! owned_pid_is_live rust_pid || ! listener_owned_by "$rust_port" "$rust_pid"; then
  echo 'Rust listener did not bind as owned child' >&2
  exit 1
fi
curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" "http://127.0.0.1:$rust_port/readyz" >/dev/null

oracle_authorization='authorization: Bearer sk-oraclemodelstoken' # gitleaks:allow -- synthetic oracle token
request() {
  local name=$1 base=$2
  curl -sS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" -D "$runtime/$name.headers" -o "$runtime/$name.json" \
    -H 'accept: application/json' -H "$oracle_authorization" \
    -H 'x-real-ip: 127.0.0.1' -w '%{http_code}' "$base/v1/models"
}

request_with() {
  local name=$1 base=$2 path=$3
  shift 3
  curl -sS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" -D "$runtime/$name.headers" -o "$runtime/$name.json" \
    -H 'accept: application/json' -H 'x-real-ip: 127.0.0.1' "$@" \
    -w '%{http_code}' "$base$path"
}

assert_headers() {
  local file=$1
  awk 'BEGIN { IGNORECASE=1 } /^content-type:[[:space:]]*application\/json; charset=utf-8\r?$/ { content_type=1 } /^x-new-api-version:[[:space:]]*v0.0.0\r?$/ { version=1 } /^x-oneapi-request-id:[[:space:]]*[^[:space:]]+/ { request_id=1 } END { exit !(content_type && version && request_id) }' "$file"
}

canonical_body() {
  jq -S 'if .error? then .error.message |= sub(" \\(request id: .*\\)$"; " (request id: <REQUEST_ID>)") else . end' "$1"
}

snapshot_rows() {
  local engine=$1
  if [[ $engine == go ]]; then
    sqlite3 -json "$runtime/legacy.db" 'SELECT id, username, status, "group", auth_version FROM users ORDER BY id; SELECT id, user_id, key, status, model_limits_enabled, model_limits, allow_ips FROM tokens ORDER BY id; SELECT "group", model, channel_id, enabled FROM abilities ORDER BY "group", model, channel_id' | jq -S .
  else
    psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -qAt -c "SELECT json_build_object('users', (SELECT COALESCE(json_agg(to_jsonb(x) ORDER BY x.id), '[]'::json) FROM (SELECT id, username, status, \"group\", auth_version FROM users) x), 'tokens', (SELECT COALESCE(json_agg(to_jsonb(x) ORDER BY x.id), '[]'::json) FROM (SELECT id, user_id, key, status, model_limits_enabled, model_limits, allow_ips FROM tokens) x), 'abilities', (SELECT COALESCE(json_agg(to_jsonb(x) ORDER BY x.\"group\", x.model, x.channel_id), '[]'::json) FROM (SELECT \"group\", model, channel_id, enabled FROM abilities) x))" | jq -S .
  fi
}

cache_hash() {
  local port=$1 key=$2
  valkey_for_port "$port" --raw HGETALL "$key" | jq -Rn '[inputs] | [range(0; length; 2) as $i | {key: .[$i], value: .[$i + 1]}] | sort_by(.key)'
}

cache_ttl() { valkey_for_port "$1" TTL "$2"; }

token_digest=$(printf %s oraclemodelstoken | openssl dgst -sha256 -hmac "$crypto_secret" | awk '{print $NF}')
token_key="token:$token_digest"
flush_valkey
for engine in go rust; do snapshot_rows "$engine" >"$runtime/$engine.rows.before"; done
[[ $(request go "http://127.0.0.1:$go_port") == 200 ]]
[[ $(request rust "http://127.0.0.1:$rust_port") == 200 ]]
sleep .2 # Go's legacy cache fill is asynchronous.
assert_headers "$runtime/go.headers"
assert_headers "$runtime/rust.headers"
canonical_body "$runtime/go.json" >"$runtime/go.body"
canonical_body "$runtime/rust.json" >"$runtime/rust.body"
diff -u "$runtime/go.body" "$runtime/rust.body"
scenario_total=$((scenario_total + 1))
# Go loaded the settings before this listener started.  The only unpriced
# gpt-* ability must stay hidden, dynamic tiered_expr models stay visible, and
# the default user's special auto-group adds vip while removing unavailable.
jq -e '
  [.data[].id] as $ids
  | ($ids | sort) == ["dynamic-tiered-model", "gpt-4o", "text-embedding-3-small", "vip-tiered-model"]
  and ($ids | index("gpt-hardcoded-prefix-only") | not)
  and ($ids | index("unavailable-tiered-model") | not)
' "$runtime/go.body" >/dev/null
for engine in go rust; do snapshot_rows "$engine" >"$runtime/$engine.rows.after"; diff -u "$runtime/$engine.rows.before" "$runtime/$engine.rows.after"; done

# Cold and hot effects must expose identical legacy cache keys/values; TTLs
# need only be within the configured window because listener scheduling differs.
for key in "auth:user:version:42" "$token_key" "user:42"; do
  [[ $(cache_ttl "$go_valkey_port" "$key") -ne -2 ]]
  [[ $(cache_ttl "$rust_valkey_port" "$key") -ne -2 ]]
done
[[ $(cache_ttl "$go_valkey_port" "auth:user:version:42") == -1 ]]
[[ $(cache_ttl "$rust_valkey_port" "auth:user:version:42") == -1 ]]
for key in "$token_key" "user:42"; do
  for port in "$go_valkey_port" "$rust_valkey_port"; do
    ttl=$(cache_ttl "$port" "$key")
    (( ttl >= 1 && ttl <= 60 ))
  done
done
cache_hash "$go_valkey_port" "$token_key" >"$runtime/go.token.cache"
cache_hash "$rust_valkey_port" "$token_key" >"$runtime/rust.token.cache"
cache_hash "$go_valkey_port" user:42 >"$runtime/go.user.cache"
cache_hash "$rust_valkey_port" user:42 >"$runtime/rust.user.cache"
diff -u "$runtime/go.token.cache" "$runtime/rust.token.cache"
diff -u "$runtime/go.user.cache" "$runtime/rust.user.cache"

# Concurrent cold reads must never leave a credential hash with a subset of
# fields. The Rust cache writes its token hash and expiry in one Lua command.
flush_valkey
parallel_request() {
  local base=$1
  local pids=()
  for _ in {1..16}; do
    curl -fsS --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" -o /dev/null -H "$oracle_authorization" -H 'x-real-ip: 127.0.0.1' "$base/v1/models" &
    pids+=("$!")
  done
  for pid in "${pids[@]}"; do wait "$pid"; done
}
parallel_request "http://127.0.0.1:$go_port"
parallel_request "http://127.0.0.1:$rust_port"
sleep .2 # Go's cache fill is asynchronous.
for port in "$go_valkey_port" "$rust_valkey_port"; do
  [[ $(valkey_for_port "$port" HLEN "$token_key") == 16 ]]
  [[ $(valkey_for_port "$port" HLEN user:42) == 10 ]]
done
go_hot_ttl=$(cache_ttl "$go_valkey_port" "$token_key")
rust_hot_ttl=$(cache_ttl "$rust_valkey_port" "$token_key")
[[ $(request go "http://127.0.0.1:$go_port") == 200 ]]
[[ $(request rust "http://127.0.0.1:$rust_port") == 200 ]]
(( $(cache_ttl "$go_valkey_port" "$token_key") <= go_hot_ttl ))
(( $(cache_ttl "$rust_valkey_port" "$token_key") <= rust_hot_ttl ))
scenario_total=$((scenario_total + 1))

compare_case() {
  local name=$1 expected=$2
  [[ $(request go "http://127.0.0.1:$go_port") == "$expected" ]]
  [[ $(request rust "http://127.0.0.1:$rust_port") == "$expected" ]]
  canonical_body "$runtime/go.json" >"$runtime/go.$name"
  canonical_body "$runtime/rust.json" >"$runtime/rust.$name"
  diff -u "$runtime/go.$name" "$runtime/rust.$name"
  scenario_total=$((scenario_total + 1))
}

compare_alias_case() {
  local name=$1 expected=$2 path=$3
  shift 3
  [[ $(request_with "go.$name" "http://127.0.0.1:$go_port" "$path" "$@") == "$expected" ]]
  [[ $(request_with "rust.$name" "http://127.0.0.1:$rust_port" "$path" "$@") == "$expected" ]]
  canonical_body "$runtime/go.$name.json" >"$runtime/go.$name"
  canonical_body "$runtime/rust.$name.json" >"$runtime/rust.$name"
  diff -u "$runtime/go.$name" "$runtime/rust.$name"
  scenario_total=$((scenario_total + 1))
}

# TokenAuth only recognizes x-api-key for the OpenAI/Anthropic aliases.  The
# Gemini aliases recognize x-goog-api-key or ?key=, while bare /v1/models with
# a Gemini credential is dispatched to RetrieveModel with an empty model id.
compare_alias_case openai_x_api_key 200 /v1/models -H 'x-api-key: sk-oraclemodelstoken'
compare_alias_case openai_bearer 200 /v1/models -H 'authorization: Bearer sk-oraclemodelstoken'
compare_alias_case anthropic_x_api_key 200 /v1/models -H 'x-api-key: sk-oraclemodelstoken' -H 'anthropic-version: 2023-06-01'
compare_alias_case gemini_bearer 200 /v1beta/models -H 'authorization: Bearer sk-oraclemodelstoken'
compare_alias_case gemini_google_header 200 /v1beta/models -H 'x-goog-api-key: sk-oraclemodelstoken'
compare_alias_case gemini_query_key 200 '/v1beta/models?key=sk-oraclemodelstoken'
jq -e '.models[0] | (.baseModelId == null and .description == null and .inputTokenLimit == null and .maxTemperature == null and .outputTokenLimit == null and .supportedGenerationMethods == null and .temperature == null and .thinking == null and .topK == null and .topP == null and .version == null)' "$runtime/go.gemini_query_key" >/dev/null
compare_alias_case gemini_openai_bearer 200 /v1beta/openai/models -H 'authorization: Bearer sk-oraclemodelstoken'
compare_alias_case gemini_openai_header 200 /v1beta/openai/models -H 'x-goog-api-key: sk-oraclemodelstoken'
compare_alias_case gemini_openai_query 200 '/v1beta/openai/models?key=sk-oraclemodelstoken'
compare_alias_case bare_google_header 200 /v1/models -H 'authorization: Bearer sk-oraclemodelstoken' -H 'x-goog-api-key: sk-oraclemodelstoken'
compare_alias_case bare_google_query 200 '/v1/models?key=sk-oraclemodelstoken' -H 'authorization: Bearer sk-oraclemodelstoken'
compare_alias_case gemini_ignores_x_api_key 401 /v1beta/models -H 'x-api-key: sk-oraclemodelstoken'
compare_alias_case gemini_openai_ignores_x_api_key 401 /v1beta/openai/models -H 'x-api-key: sk-oraclemodelstoken'
compare_alias_case ordinary_user_channel_suffix 403 /v1/models -H 'authorization: Bearer sk-oraclemodelstoken-channel'
jq -e '.error.message == "普通用户不支持指定渠道 (request id: <REQUEST_ID>)" and .error.code == ""' "$runtime/go.ordinary_user_channel_suffix" >/dev/null

# TokenAuth validates explicit token groups before it dispatches any Models
# alias.  The special default user's +:vip applies first; missing usable-group
# access wins over the subsequent GroupRatio deprecation check.
assert_explicit_group_error() {
  local name=$1 message=$2
  jq -e --arg message "$message (request id: <REQUEST_ID>)" \
    '.error.message == $message and .error.type == "new_api_error" and .error.code == ""' \
    "$runtime/go.$name" >/dev/null
}

assert_explicit_allowed_group() {
  local name=$1 alias=$2
  case "$alias" in
    openai | gemini_openai)
      jq -e '
        (.data | type == "array")
        and ([.data[].id] == ["vip-tiered-model"])
        and ([.data[].id] | index("dynamic-tiered-model") | not)
        and ([.data[].id] | index("unavailable-tiered-model") | not)
      ' "$runtime/go.$name" >/dev/null
      ;;
    gemini)
      jq -e '
        (.models | type == "array")
        and ([.models[].name] == ["vip-tiered-model"])
        and ([.models[].name] | index("dynamic-tiered-model") | not)
        and ([.models[].name] | index("unavailable-tiered-model") | not)
      ' "$runtime/go.$name" >/dev/null
      ;;
  esac
}
for alias in openai gemini gemini_openai; do
  case "$alias" in
    openai) path=/v1/models ;;
    gemini) path=/v1beta/models ;;
    gemini_openai) path=/v1beta/openai/models ;;
  esac
  compare_alias_case "explicit_allowed_$alias" 200 "$path" -H 'authorization: Bearer sk-oracleallowedtoken'
  assert_explicit_allowed_group "explicit_allowed_$alias" "$alias"
  compare_alias_case "explicit_forbidden_$alias" 403 "$path" -H 'authorization: Bearer sk-oracleforbiddentoken'
  assert_explicit_group_error "explicit_forbidden_$alias" '无权访问 forbidden 分组'
  compare_alias_case "explicit_deprecated_$alias" 403 "$path" -H 'authorization: Bearer sk-oracledeprecatedtoken'
  assert_explicit_group_error "explicit_deprecated_$alias" '分组 deprecated 已被弃用'
done

# Keep state mutations equivalent and flush both non-authoritative caches before
# each request so the case asserts the authoritative inputs rather than a hot key.
sqlite3 "$runtime/legacy.db" "UPDATE tokens SET model_limits_enabled = 1, model_limits = 'gpt-4o';"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET model_limits_enabled = TRUE, model_limits = 'gpt-4o'" >/dev/null
flush_valkey
compare_case restricted 200
jq -e '.data | length == 1 and .[0].id == "gpt-4o"' "$runtime/go.restricted" >/dev/null

sqlite3 "$runtime/legacy.db" "UPDATE tokens SET allow_ips = '10.0.0.0/8';"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET allow_ips = '10.0.0.0/8'" >/dev/null
flush_valkey
compare_case cidr_denied 403
jq -e '.error.code == "access_denied" and .error.message == "您的 IP 不在令牌允许访问的列表中 (request id: <REQUEST_ID>)"' "$runtime/go.cidr_denied" >/dev/null

sqlite3 "$runtime/legacy.db" "UPDATE tokens SET allow_ips = ''; UPDATE users SET status = 2;"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET allow_ips = ''; UPDATE users SET status = 2" >/dev/null
flush_valkey
compare_case disabled_user 403

# TokenAuth rejects disabled, expired, and exhausted credentials before the
# handler. Keep both engines on fresh caches so these remain auth assertions.
sqlite3 "$runtime/legacy.db" "UPDATE users SET status = 1; UPDATE tokens SET status = 0;"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE users SET status = 1; UPDATE tokens SET status = 0" >/dev/null
flush_valkey
compare_case disabled_token 401

sqlite3 "$runtime/legacy.db" "UPDATE tokens SET status = 1, expired_time = 1;"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET status = 1, expired_time = 1" >/dev/null
flush_valkey
compare_case expired_token 401

sqlite3 "$runtime/legacy.db" "UPDATE tokens SET expired_time = -1, unlimited_quota = 0, remain_quota = 0;"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET expired_time = -1, unlimited_quota = FALSE, remain_quota = 0" >/dev/null
flush_valkey
compare_case exhausted_token 401

# Cache loss is allowed for this read path: Rust must still authorize against
# PostgreSQL. The main readiness policy remains independently fail-closed.
sqlite3 "$runtime/legacy.db" "UPDATE users SET status = 1; UPDATE tokens SET unlimited_quota = 1, remain_quota = 0;"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE users SET status = 1; UPDATE tokens SET unlimited_quota = TRUE, remain_quota = 0" >/dev/null
stop_owned_process rust_valkey_pid "$rust_valkey_port"
[[ $(request rust "http://127.0.0.1:$rust_port") == 200 ]]
start_valkey rust "$rust_valkey_port" "$rust_valkey_password" rust_valkey_pid
[[ $(request rust "http://127.0.0.1:$rust_port") == 200 ]]
[[ $(cache_ttl "$rust_valkey_port" "$token_key") -ne -2 ]]

# Anthropic's legacy handler indexes the empty token-model-limit result before
# writing the response. The recovery middleware returns this stable panic JSON.
sqlite3 "$runtime/legacy.db" "UPDATE tokens SET model_limits_enabled = 1, model_limits = '';"
psql -h 127.0.0.1 -p "$pg_port" -d lmm_test_models_rust -c "UPDATE tokens SET model_limits_enabled = TRUE, model_limits = ''" >/dev/null
flush_valkey
compare_alias_case empty_anthropic_model_limit 500 /v1/models -H 'x-api-key: sk-oraclemodelstoken' -H 'anthropic-version: 2023-06-01'

rust_build_input_manifest_sha256_after=$(rust_build_input_manifest | sha256sum | awk '{print $1}')
[[ $rust_build_input_manifest_sha256_after == "$rust_build_input_manifest_sha256" ]] || {
  echo 'Rust build input manifest changed during differential run; refusing evidence' >&2
  exit 1
}
if [[ $approval_mode == 1 && $scenario_total -ne $expected_scenarios ]]; then
  echo "approval scenario count mismatch: expected $expected_scenarios, got $scenario_total" >&2
  exit 1
fi
models_summary=$(jq -cn --arg go_content_sha256 "$frozen_go_manifest_sha256" --arg rust_build_input_manifest_sha256 "$rust_build_input_manifest_sha256" \
  --arg rust_binary_sha256 "$rust_binary_sha256" --argjson scenarios "$scenario_total" --argjson approval "$approval_mode" \
  '{test:"models-listener-differential",mode:(if $approval == 1 then "approval" else "full" end),postgres_major:18,go_tcp_listener:true,rust_tcp_listener:true,go_valkey_password_required:true,rust_valkey_password_required:true,listener_ownership:"pid-starttime-ss",curl_timeouts:{connect_seconds:2,max_seconds:15},frozen_go_content_sha256:$go_content_sha256,rust_build_input_manifest_sha256:$rust_build_input_manifest_sha256,rust_binary_sha256:$rust_binary_sha256,scenario_total:$scenarios,fixture:"same-logical-go-sqlite-and-rust-postgres",go_options_seeded_before_listener_restart:true,aliases:["/v1/models","/v1beta/models","/v1beta/openai/models"],response_headers:["content-type","x-new-api-version","x-oneapi-request-id"],database_effects:"authoritative rows unchanged for read cases",valkey_effects:["auth:user:version:42","token:HMAC(CRYPTO_SECRET,token)","user:42"],result:"passed"}')

if [[ -n ${LMM_MODELS_RESULT_DIR:-} ]]; then
  [[ $LMM_MODELS_RESULT_DIR == /* && $LMM_MODELS_RESULT_DIR != *..* ]] || {
    echo 'LMM_MODELS_RESULT_DIR must be absolute and contain no ..' >&2
    exit 2
  }
  mkdir -p "$LMM_MODELS_RESULT_DIR"
  printf '%s\n' "$models_summary" >"$LMM_MODELS_RESULT_DIR/models-listener-summary.json"
  while IFS=$'\t' read -r method path cases; do
    slug=$(printf '%s-%s' "$method" "$path" | tr '/:?' '___' | tr -cd '[:alnum:]_-')
    jq -cn --arg method "$method" --arg path "$path" --arg suite "models-listener-summary.json" \
      --argjson cases "$cases" --argjson scenarios "$scenario_total" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"models-listener",cases:$cases,suite_scenarios:$scenarios,suite_summary:$suite,real_tcp:true,production_access:true,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$LMM_MODELS_RESULT_DIR/models-listener-$slug.json"
  done <<'ROUTES'
GET	/v1/models	20
GET	/v1beta/models	6
GET	/v1beta/openai/models	6
ROUTES
fi
printf '%s\n' "$models_summary"
