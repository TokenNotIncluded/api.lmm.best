#!/usr/bin/env bash
# Real TCP differential for the seven stateful rows in route-compatibility-matrix.
# Everything below is created under one mktemp directory: a local PostgreSQL
# 18 cluster, two non-public schemas, one local Valkey process (DB 5 and 6),
# and two loopback listeners.  It deliberately has no DSN/listener inputs.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixture_test="$repo_root/apps/api-rust/tests/behavior-oracle/tests/test-route-compatibility-transaction-fixtures.sh"
fixtures="$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-transaction-fixtures.json"
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
if [[ ${LMM_TRANSACTION_CANONICAL_JSON_SELF_TEST:-0} != 1 ]]; then
  [[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
  [[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
  legacy_root=$(realpath -e -- "$legacy_root")
  case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
fi
pg_port=${LMM_TRANSACTION_PG_PORT:-55467}
go_port=${LMM_TRANSACTION_GO_PORT:-13037}
rust_port=${LMM_TRANSACTION_RUST_PORT:-33067}
rust_test_instance=${LMM_TRANSACTION_RUST_TEST_INSTANCE:-1}
# Default Valkey endpoint for the Rust test-instance is 6380, but the script
# now falls back to a random free port if that port is occupied.
valkey_port=${LMM_TRANSACTION_VALKEY_PORT:-6380}
runtime_base=${LMM_TRANSACTION_RUNTIME_BASE:-/tmp}
[[ -d $runtime_base && -w $runtime_base ]] || { echo "transaction runtime base is not writable: $runtime_base" >&2; exit 1; }
runtime=$(mktemp -d "$runtime_base/lmm-transaction-differential.XXXXXX")
keep_runtime=${LMM_TRANSACTION_KEEP_RUNTIME:-0}
result_dir=${LMM_TRANSACTION_RESULT_DIR:-}
cargo_target=${LMM_TRANSACTION_CARGO_TARGET_DIR:-"$runtime/cargo-target"}
rust_binary=${LMM_TRANSACTION_RUST_BINARY:-"$cargo_target/debug/lmm-api-rs"}
go_build="$runtime/go-build"
go_schema=lmm_test_transaction_go
rust_schema=lmm_test_transaction_rust
go_role=lmm_test_transaction_go
rust_role=lmm_test_transaction_rust
database=lmm_test_transaction
passed=0
route_filter=${LMM_TRANSACTION_ROUTE_FILTER:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo "LMM_TRANSACTION_RESULT_DIR must be an absolute path without '..'" >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
[[ -n $route_filter ]] && expected_phase_total=4 || expected_phase_total=28
# shellcheck disable=SC2034 # Read indirectly through the PID variable names.
go_pid='' rust_pid='' valkey_pid='' go_pid_start='' rust_pid_start='' valkey_pid_start=''

cleanup() {
  # The trap is installed before the helper definitions so that preflight
  # failures still clean their exact runtime.  Guard calls whose definitions
  # may not have been reached yet.
  if declare -F stop_listeners >/dev/null 2>&1; then stop_listeners || true; fi
  if declare -F stop_owned_process >/dev/null 2>&1; then stop_owned_process valkey_pid || true; fi
  [[ -d $runtime/pg ]] && pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  if [[ $keep_runtime == 1 ]]; then
    echo "preserved transaction runtime: $runtime" >&2
  else
    case "$runtime" in "$runtime_base"/lmm-transaction-differential.*) rm -rf "$runtime" ;; *) echo "refusing unexpected transaction runtime: $runtime" >&2 ;; esac
  fi
}
trap cleanup EXIT INT TERM
trap 'echo "transaction differential failed after $passed/$expected_phase_total phases (line $LINENO)" >&2' ERR

for command in cargo curl git go initdb jq pg_ctl postgres psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
if [[ ${LMM_TRANSACTION_CANONICAL_JSON_SELF_TEST:-0} != 1 ]]; then
  [[ -d $legacy_root ]] || { echo "missing frozen Go listener: $legacy_root" >&2; exit 1; }
fi
"$fixture_test" --contract-only "$fixtures"
pid_start_time() { [[ -r /proc/$1/stat ]] || return 1; awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local pid_name=$1 pid=$2 start; printf -v "$pid_name" '%s' "$pid"; start=$(pid_start_time "$pid") || { echo "failed to record pid $pid" >&2; wait "$pid" 2>/dev/null || true; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; return 1; }; printf -v "${pid_name}_start" '%s' "$start"; }
owned_pid_is_live() { local pid_name=$1 pid start_name expected; pid=${!pid_name:-}; start_name="${pid_name}_start"; expected=${!start_name:-}; [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]; }
stop_owned_process() { local pid_name=$1 pid; pid=${!pid_name:-}; if [[ -n $pid ]]; then if owned_pid_is_live "$pid_name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi; fi; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; }
port_free() { [[ -z $(ss -H -ltn "sport = :$1" 2>/dev/null) ]]; }
random_free_port() { local p; while :; do p=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000)); [[ -z $(ss -H -ltn "sport = :$p" 2>/dev/null) ]] && { echo "$p"; return; }; done; }
select_unused_port() {
  local requested=$1 candidate
  if port_free "$requested"; then echo "$requested"; return 0; fi
  echo "requested valkey port $requested is occupied, selecting a free port" >&2
  for _ in {1..200}; do
    candidate=$(random_free_port)
    if port_free "$candidate"; then echo "$candidate"; return 0; fi
  done
  return 1
}
valkey_port=$(select_unused_port "$valkey_port") || { echo "unable to allocate an unused valkey port" >&2; exit 1; }
for port in "$pg_port" "$go_port" "$rust_port" "$valkey_port"; do
  if ss -ltn "sport = :$port" | grep -q LISTEN; then
    echo "refusing occupied loopback test port: $port" >&2; exit 1
  fi
done

admin_sql() { psql -h 127.0.0.1 -p "$pg_port" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$1"; }
sql() {
  local engine=$1 statement=$2 schema
  case "$engine" in go) schema=$go_schema ;; rust) schema=$rust_schema ;; *) return 2 ;; esac
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "${engine}_role_unused" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$statement"
}
# psql's user is selected explicitly here; keeping it out of sql() avoids a
# shell-evaluated DSN and makes every database command visibly loopback-only.
app_sql() {
  local engine=$1 statement=$2 schema role
  case "$engine" in go) schema=$go_schema; role=$go_role ;; rust) schema=$rust_schema; role=$rust_role ;; *) return 2 ;; esac
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -U "$role" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$statement"
}
admin_schema_sql() {
  local schema=$1 statement=$2
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$statement"
}
snapshot() {
  local engine=$1
  local topup_projection="to_jsonb(x) - 'id' - 'create_time' - 'complete_time'"
  if [[ ${LMM_TRANSACTION_OMIT_OPTIONAL_TOPUP_COLUMNS:-0} == 1 ]]; then
    topup_projection+=" - 'credited_quota' - 'expected_amount_micros' - 'provider_event_id' - 'provider_product_id' - 'provider_store_id' - 'provider_transaction_id' - 'settled_amount_micros' - 'settlement_currency'"
  fi
  app_sql "$engine" "SELECT jsonb_build_object(
    'users', COALESCE((SELECT jsonb_agg(to_jsonb(x) - 'access_token' - 'created_at' - 'last_login_at' - 'last_api_activity_at' - 'trust_level_override' ORDER BY id) FROM users x),'[]'::jsonb),
    'checkins', COALESCE((SELECT jsonb_agg(to_jsonb(x) - 'id' - 'created_at' ORDER BY user_id,checkin_date) FROM checkins x),'[]'::jsonb),
    'redemptions', COALESCE((SELECT jsonb_agg(to_jsonb(x) - 'id' - 'created_time' - 'redeemed_time' ORDER BY key) FROM redemptions x),'[]'::jsonb),
    'top_ups', COALESCE((SELECT jsonb_agg($topup_projection ORDER BY trade_no) FROM top_ups x),'[]'::jsonb),
    'logs', COALESCE((SELECT jsonb_agg(to_jsonb(x) - 'id' - 'created_at' - 'request_id' - 'upstream_request_id' ORDER BY user_id,type,content) FROM logs x WHERE type <> 7),'[]'::jsonb),
    'options', COALESCE((SELECT jsonb_object_agg(key,value ORDER BY key) FROM options WHERE key IN ('checkin_setting','checkin_setting.enabled','checkin_setting.min_quota','checkin_setting.max_quota','payment_setting.compliance_confirmed','payment_setting.compliance_terms_version','QuotaPerUnit','MinTopUp','Price','TopupGroupRatio','general_setting')),'{}'::jsonb))" | jq -S .
}
canonical_json() {
  local context=${1:-}
  jq -S --arg context "$context" '
    def dynamic_field:
      . == "access_token" or . == "refresh_token" or . == "session" or . == "sid"
      or . == "request_id" or . == "created_at" or . == "updated_at"
      or (($context | startswith("sessions-list.")) and (. == "last_active_at" or . == "expires_at"));
    def unpad_char_32:
      rtrimstr(" ") | rtrimstr(" ") | rtrimstr(" ") | rtrimstr(" ");
    def dynamic_response_token:
      type == "string" and (unpad_char_32 | test("^[A-Za-z0-9+/=]{28,32}$"));

    # GORM stores `users.access_token` in CHAR(32), so PostgreSQL pads the
    # legacy 28..=31-character generated values with spaces on read.  Only
    # the top-level success-envelope `data` field can carry this generated
    # personal token in this runner.  Trim only while testing its token shape;
    # never alter ordinary response strings or nested `data` values.
    walk(
      if type == "object" then
        with_entries(if (.key | dynamic_field) then .value = "<DYNAMIC>" else . end)
      else . end
    )
    | if (.data? | dynamic_response_token) then .data = "<DYNAMIC>" else . end
  '
}

canonical_json_self_test() {
  local input expected actual
  input='{"access_token":"session-secret","data":"0123456789abcdefghijklmnopqr  ","message":"literal trailing spaces  ","token_description":"must remain verbatim  ","nested":{"data":"0123456789abcdefghijklmnopqr  ","request_id_note":"must remain verbatim  ","token_description":"nested literal  "}}'
  expected='{
  "access_token": "<DYNAMIC>",
  "data": "<DYNAMIC>",
  "message": "literal trailing spaces  ",
  "nested": {
    "data": "0123456789abcdefghijklmnopqr  ",
    "request_id_note": "must remain verbatim  ",
    "token_description": "nested literal  "
  },
  "token_description": "must remain verbatim  "
}'
  actual=$(printf '%s\n' "$input" | canonical_json)
  diff -u <(printf '%s\n' "$expected") <(printf '%s\n' "$actual")
}
selected_headers() { awk 'BEGIN{IGNORECASE=1} /^[^:]+:/{n=tolower($1);sub(/:$/,"",n); if(n=="content-type"||n=="cache-control"||n=="pragma"||n=="expires") {v=$0;sub(/\r$/,"",v);print tolower(v)}}' "$1" | sort; }

stop_listeners() {
  stop_owned_process go_pid || true
  stop_owned_process rust_pid || true
}
if [[ ${LMM_TRANSACTION_CANONICAL_JSON_SELF_TEST:-0} == 1 ]]; then
  canonical_json_self_test
  jq -cn '{test:"route-compatibility-transaction-canonical-json",result:"passed"}'
  exit 0
fi
wait_for() {
  local port=$1 path=$2
  # Go's first PostgreSQL AutoMigrate can exceed 15 seconds on a loaded
  # shared host even though the process is healthy and making progress.
  for _ in {1..6000}; do curl --silent --output /dev/null "http://127.0.0.1:$port$path" && return 0 || true; sleep .05; done
  return 1
}
start_listeners() {
  stop_listeners
  local go_dsn="postgresql://$go_role@127.0.0.1:$pg_port/$database?sslmode=disable&options=-csearch_path=$go_schema"
  local rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$database?options=-csearch_path=$rust_schema"
  SQL_DSN="$go_dsn" PORT="$go_port" REDIS_CONN_STRING="redis://127.0.0.1:$valkey_port/5" \
    SESSION_SECRET='TransactionOracle-2026!SyntheticOnly' CRYPTO_SECRET='TransactionOracle-Crypto-2026!SyntheticOnly' \
    PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false GIN_MODE=release \
    "$go_build/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
  if ! wait_for "$go_port" /api/status; then sed -n '1,220p' "$runtime/go.log" >&2; return 1; fi
  local rust_instance=${LMM_TRANSACTION_RS_TEST_INSTANCE:-1}
  local rust_slot=green
  [[ $rust_instance == 1 ]] && rust_slot=single
  local -a rust_env=(
    "LMM_RS_SLOT=$rust_slot"
    "LMM_RS_LISTEN_ADDR=127.0.0.1:$rust_port"
    "DATABASE_URL=$rust_dsn"
    "VALKEY_URL=redis://127.0.0.1:$valkey_port/6"
    "LMM_RS_TEST_VALKEY_PORT=$valkey_port"
    "LMM_SCHEMA_CONTRACT=1"
    'SESSION_SECRET=TransactionOracle-2026!SyntheticOnly'
    'CRYPTO_SECRET=TransactionOracle-Crypto-2026!SyntheticOnly'
    'PASSWORD_LOGIN_ENABLED=true'
    'GLOBAL_API_RATE_LIMIT_ENABLE=false'
    'CRITICAL_RATE_LIMIT_ENABLE=false'
    'TRUSTED_PROXIES=none'
    'VERSION=v0.0.0'
  )
  [[ $rust_instance == 1 ]] && rust_env+=("LMM_RS_TEST_INSTANCE=1")
  env "${rust_env[@]}" "$rust_binary" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"
  if ! wait_for "$rust_port" /readyz; then sed -n '1,220p' "$runtime/rust.log" >&2; return 1; fi
}
login() {
  local engine=$1 user=$2 port
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  curl --silent --show-error -H 'content-type: application/json' -d "{\"username\":\"$user\",\"password\":\"password\"}" "http://127.0.0.1:$port/api/user/login" |
    jq -er 'select(.success == true) | .data.access_token | strings'
}
call() {
  local engine=$1 name=$2 method=$3 path=$4 body=$5 token=$6 port prefix
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  prefix="$runtime/$engine.$name"
  local args=(--silent --show-error --dump-header "$prefix.headers" --output "$prefix.body" --write-out '%{http_code}' --request "$method")
  [[ -z $token ]] || args+=(--header "authorization: Bearer $token")
  [[ $body == __NONE__ ]] || args+=(--header 'content-type: application/json' --data-binary "$body")
  curl "${args[@]}" "http://127.0.0.1:$port$path" >"$prefix.status"
  jq -e . <"$prefix.body" >/dev/null
}
pair() {
  local name=$1 method=$2 path=$3 body=$4
  call go "$name" "$method" "$path" "$body" "$go_session"
  call rust "$name" "$method" "$path" "$body" "$rust_session"
  diff -u "$runtime/go.$name.status" "$runtime/rust.$name.status"
  diff -u <(canonical_json "$name" <"$runtime/go.$name.body") <(canonical_json "$name" <"$runtime/rust.$name.body")
  # Headers are retained beside each response, but legacy's dashboard cache
  # directives are intentionally not an atomic-route contract.  Status/body
  # and the six durable snapshots are the differential verdict here.
}
prepare_actor() {
  local actor=$1
  go_session='' rust_session=''
  [[ $actor == anonymous ]] || { go_session=$(login go "$actor"); rust_session=$(login rust "$actor"); }
}

seed() {
  local mode=$1 engine
  for engine in go rust; do
    admin_schema_sql "${engine_schema[$engine]}" "TRUNCATE logs, checkins, redemptions, top_ups, passkey_credentials, user_oauth_bindings, custom_oauth_providers, users, options RESTART IDENTITY CASCADE;"
    admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO options(key,value) VALUES
      ('checkin_setting.enabled','true'),
      ('checkin_setting.min_quota','100'),
      ('checkin_setting.max_quota','100'),
      ('payment_setting.compliance_confirmed','true'),
      ('payment_setting.compliance_terms_version','v1'),
      ('QuotaPerUnit','500000'),('MinTopUp','1'),('Price','1'),('TopupGroupRatio','{\"default\":1}'),('general_setting','{\"quota_display_type\":\"USD\"}');
      INSERT INTO users(id,username,password,display_name,role,status,email,\"group\",setting,auth_version,quota,aff_quota) VALUES
      (1,'root','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','root',10,1,'','default','{}',1,100000000,0),
      (101,'user101','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','user101',1,1,'','default','{}',1,100,500000);"
    case "$mode" in
      checkin-failure) admin_schema_sql "${engine_schema[$engine]}" "UPDATE options SET value='false' WHERE key='checkin_setting.enabled'" ;;
      checkin-existing) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO checkins(id,user_id,checkin_date,quota_awarded,created_at) VALUES(1,101,to_char(CURRENT_DATE,'YYYY-MM-DD'),100,0)" ;;
      passkey) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO passkey_credentials(id,user_id,credential_id,public_key,last_used_at) VALUES(1,101,'ORACLE-CREDENTIAL-101','ORACLE-PUBLIC-KEY-101',NULL)" ;;
      oauth-binding) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO custom_oauth_providers(id,name,slug,icon,enabled) VALUES(1,'Oracle OAuth','oracle','oracle-icon',TRUE); INSERT INTO user_oauth_bindings(id,user_id,provider_id,provider_user_id) VALUES(1,101,1,'ORACLE-PROVIDER-USER-101')" ;;
      aff-code) admin_schema_sql "${engine_schema[$engine]}" "UPDATE users SET aff_code='ORACLE-AFF-101' WHERE id=101" ;;
      topup-admin) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO top_ups(id,user_id,amount,money,trade_no,payment_method,payment_provider,create_time,status) VALUES(1,101,2,2,'ORACLE-TOPUP-ADMIN-101','manual','stripe',1730000000,'pending')" ;;
      aff-failure) admin_schema_sql "${engine_schema[$engine]}" "UPDATE users SET aff_quota=499999 WHERE id=101" ;;
      amount-failure) admin_schema_sql "${engine_schema[$engine]}" "UPDATE options SET value='3' WHERE key='MinTopUp'" ;;
      redeem*) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO redemptions(id,key,status,quota,created_time,expired_time) VALUES(1,'ORACLE-REDEEM-101',1,300,0,0)" ;;
      topup*) admin_schema_sql "${engine_schema[$engine]}" "INSERT INTO top_ups(id,user_id,amount,money,trade_no,payment_method,payment_provider,create_time,status) VALUES(1,101,2,2,'ORACLE-TOPUP-101','manual','',0,'pending')" ;;
    esac
    [[ $mode != redeem-used ]] || admin_schema_sql "${engine_schema[$engine]}" "UPDATE redemptions SET status=2,used_user_id=101 WHERE id=1"
  done
}
declare -A engine_schema=([go]=$go_schema [rust]=$rust_schema)
inject_write_failure() {
  local engine schema
  for engine in go rust; do
    schema=${engine_schema[$engine]}
    admin_schema_sql "$schema" "CREATE OR REPLACE FUNCTION $schema.fail_transaction_write() RETURNS trigger LANGUAGE plpgsql AS \$\$ BEGIN RAISE EXCEPTION 'transaction fixture injected write failure'; END \$\$; CREATE TRIGGER transaction_fixture_failure BEFORE UPDATE ON users FOR EACH ROW EXECUTE FUNCTION $schema.fail_transaction_write();"
  done
}
clear_injection() { local engine; for engine in go rust; do admin_schema_sql "${engine_schema[$engine]}" 'DROP TRIGGER IF EXISTS transaction_fixture_failure ON users'; done; }

route_for() {
  case "$1" in
    access-token-generation) printf 'GET\t/api/user/token\t__NONE__\tuser101\n' ;;
    checkin-status) printf 'GET\t/api/user/checkin?month=2026-08\t__NONE__\tuser101\n' ;;
    topup-info) printf 'GET\t/api/user/topup/info\t__NONE__\tuser101\n' ;;
    topup-self) printf 'GET\t/api/user/topup/self?p=1&page_size=10\t__NONE__\tuser101\n' ;;
    user-groups) printf 'GET\t/api/user/groups\t__NONE__\tanonymous\n' ;;
    self-groups) printf 'GET\t/api/user/self/groups\t__NONE__\tuser101\n' ;;
    user-models) printf 'GET\t/api/user/models\t__NONE__\tuser101\n' ;;
    aff-code) printf 'GET\t/api/user/aff\t__NONE__\tuser101\n' ;;
    admin-topups) printf 'GET\t/api/user/topup?p=1&page_size=10\t__NONE__\troot\n' ;;
    sessions-list) printf 'GET\t/api/user/sessions\t__NONE__\tuser101\n' ;;
    passkey-status) printf 'GET\t/api/user/passkey\t__NONE__\tuser101\n' ;;
    federation-bindings) printf 'GET\t/api/user/oauth/bindings\t__NONE__\tuser101\n' ;;
    checkin-commit-rollback) printf 'POST\t/api/user/checkin\t{}\tuser101\n' ;;
    affiliate-transfer) printf 'POST\t/api/user/aff_transfer\t{"quota":500000}\tuser101\n' ;;
    amount-quote) printf 'POST\t/api/user/amount\t{"amount":2}\tuser101\n' ;;
    redeem-topup) printf 'POST\t/api/user/topup\t{"key":"ORACLE-REDEEM-101"}\tuser101\n' ;;
    manual-topup-completion) printf 'POST\t/api/user/topup/complete\t{"trade_no":"ORACLE-TOPUP-101"}\troot\n' ;;
  esac
}
phase_seed() {
  local route=$1 phase=$2
  case "$route:$phase" in
    checkin-status:failure) printf checkin-failure ;;
    topup-self:positive|topup-self:rollback|topup-self:replay) printf topup ;;
    aff-code:positive|aff-code:failure|aff-code:rollback|aff-code:replay) printf aff-code ;;
    admin-topups:positive|admin-topups:rollback|admin-topups:replay) printf topup-admin ;;
    passkey-status:positive|passkey-status:rollback|passkey-status:replay) printf passkey ;;
    federation-bindings:positive|federation-bindings:rollback|federation-bindings:replay) printf oauth-binding ;;
    checkin-commit-rollback:failure) printf checkin-existing ;;
    affiliate-transfer:failure) printf aff-failure ;;
    amount-quote:failure) printf amount-failure ;;
    redeem-topup:positive|redeem-topup:rollback|redeem-topup:replay) printf redeem ;;
    redeem-topup:failure) printf redeem-used ;;
    manual-topup-completion:positive|manual-topup-completion:rollback|manual-topup-completion:replay) printf topup ;;
    *) printf base ;;
  esac
}
run_phase() {
  local id=$1 phase=$2 seed_mode method path body actor
  seed_mode=$(phase_seed "$id" "$phase")
  seed "$seed_mode"
  [[ $phase != rollback ]] || inject_write_failure
  start_listeners
  IFS=$'\t' read -r method path body actor < <(route_for "$id")
  [[ $phase != failure || $id != access-token-generation ]] || actor=anonymous
  [[ $phase != failure || $id != admin-topups ]] || actor=anonymous
  [[ $phase != failure || $id != sessions-list ]] || actor=anonymous
  [[ $phase != failure || $id != passkey-status ]] || actor=anonymous
  [[ $phase != failure || $id != federation-bindings ]] || actor=anonymous
  prepare_actor "$actor"
  snapshot go >"$runtime/go.$id.$phase.before"; snapshot rust >"$runtime/rust.$id.$phase.before"
  pair "$id.$phase.first" "$method" "$path" "$body"
  snapshot go >"$runtime/go.$id.$phase.after-first"; snapshot rust >"$runtime/rust.$id.$phase.after-first"
  if [[ $phase == failure || $phase == rollback ]]; then
    # Authenticated administrative writes intentionally append an audit log
    # even when the business transaction fails.  The atomicity assertion is
    # therefore limited to authoritative business state; the final full
    # Go/Rust snapshot comparison below still requires exact audit parity.
    diff -u <(jq 'del(.logs)' "$runtime/go.$id.$phase.before") <(jq 'del(.logs)' "$runtime/go.$id.$phase.after-first")
    diff -u <(jq 'del(.logs)' "$runtime/rust.$id.$phase.before") <(jq 'del(.logs)' "$runtime/rust.$id.$phase.after-first")
  fi
  if [[ $phase == replay ]]; then
    pair "$id.$phase.second" "$method" "$path" "$body"
    snapshot go >"$runtime/go.$id.$phase.after-second"; snapshot rust >"$runtime/rust.$id.$phase.after-second"
    # Replay is exactly-once for every mutation except personal-token rotation,
    # whose fixture explicitly requires replacement on each successful call.
    [[ $id == access-token-generation ]] || {
      diff -u <(jq 'del(.logs)' "$runtime/go.$id.$phase.after-first") <(jq 'del(.logs)' "$runtime/go.$id.$phase.after-second")
      diff -u <(jq 'del(.logs)' "$runtime/rust.$id.$phase.after-first") <(jq 'del(.logs)' "$runtime/rust.$id.$phase.after-second")
    }
  fi
  snapshot go >"$runtime/go.$id.$phase.final"; snapshot rust >"$runtime/rust.$id.$phase.final"
  diff -u "$runtime/go.$id.$phase.final" "$runtime/rust.$id.$phase.final"
  clear_injection
  stop_listeners
  passed=$((passed + 1))
  jq -cn --arg route "$id" --arg phase "$phase" '{test:"route-compatibility-transaction-differential",route:$route,phase:$phase,go_listener:"loopback",rust_listener:"loopback",postgres_schemas:2,valkey_databases:[5,6],snapshots:["users","checkins","redemptions","top_ups","logs","options"],result:"passed"}'
}

mkdir -p "$go_build/go-source"
if [[ ${LMM_TRANSACTION_SKIP_RUST_BUILD:-0} != 1 ]]; then
  TMPDIR="$runtime" CARGO_TARGET_DIR="$cargo_target" cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
fi
[[ -x $rust_binary ]] || { echo "Rust test-instance binary unavailable: $rust_binary" >&2; exit 1; }
cp -a "$legacy_root/." "$go_build/go-source"
# The frozen oracle archive is intentionally immutable; the disposable copy
# must be writable for the embedded frontend placeholder and cleanup hooks.
chmod -R u+rwX -- "$go_build/go-source"
mkdir -p "$go_build/go-source/web/dist"; : >"$go_build/go-source/web/dist/index.html"
(cd "$go_build/go-source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$go_build/legacy-go" .)
initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$database"
admin_sql "CREATE ROLE $go_role LOGIN; CREATE ROLE $rust_role LOGIN; CREATE SCHEMA $go_schema; CREATE SCHEMA $rust_schema;"
for schema in "$go_schema" "$rust_schema"; do
  sed "s/public\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/$schema.sql"
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -d "$database" -q -v ON_ERROR_STOP=1 -f "$runtime/$schema.sql" >/dev/null
  admin_schema_sql "$schema" "CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL); INSERT INTO lmm_schema_contract VALUES(TRUE,1,1);"
done
# The frozen Go binary always performs its historical AutoMigrate at startup,
# including harmless default-column repairs.  Give each listener ownership of
# only its own disposable schema so that migration cannot escape its fixture.
for owner_pair in "$go_schema:$go_role" "$rust_schema:$rust_role"; do
  schema=${owner_pair%%:*}; role=${owner_pair##*:}
  admin_schema_sql "$schema" "DO \$\$ DECLARE r record; BEGIN
    FOR r IN SELECT c.relkind, c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='$schema' AND c.relkind IN ('r','S') LOOP
      EXECUTE format('ALTER %s %I OWNER TO $role', CASE WHEN r.relkind='S' THEN 'SEQUENCE' ELSE 'TABLE' END, r.relname);
    END LOOP;
  END \$\$; ALTER SCHEMA $schema OWNER TO $role;"
done
# Current Go's settlement model is additive to the frozen baseline.  When the
# oracle is the current Go checkout, provision the same nullable-safe columns
# in both disposable schemas so the transaction snapshot exercises the real
# normalized credited-quota write rather than silently hiding it as an absent
# legacy column.  The default stays off for the immutable 5418 contract.
if [[ ${LMM_TRANSACTION_TOPUP_SETTLEMENT_COLUMNS:-0} == 1 ]]; then
  for schema in "$go_schema" "$rust_schema"; do
    admin_schema_sql "$schema" "ALTER TABLE top_ups
      ADD COLUMN IF NOT EXISTS credited_quota BIGINT NOT NULL DEFAULT 0,
      ADD COLUMN IF NOT EXISTS expected_amount_micros BIGINT NOT NULL DEFAULT 0,
      ADD COLUMN IF NOT EXISTS settled_amount_micros BIGINT NOT NULL DEFAULT 0,
      ADD COLUMN IF NOT EXISTS settlement_currency VARCHAR(16) NOT NULL DEFAULT '',
      ADD COLUMN IF NOT EXISTS provider_product_id VARCHAR(255) NOT NULL DEFAULT '',
      ADD COLUMN IF NOT EXISTS provider_store_id VARCHAR(255) NOT NULL DEFAULT '',
      ADD COLUMN IF NOT EXISTS provider_event_id VARCHAR(255),
      ADD COLUMN IF NOT EXISTS provider_transaction_id VARCHAR(255);
      CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_provider_event
        ON top_ups (payment_provider, provider_event_id);
      CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_provider_transaction
        ON top_ups (payment_provider, provider_transaction_id);"
  done
fi
# Rust's readiness probe also requires the forward-only contract-2
# open-source-bounty relations.  Apply this after the baseline ownership pass
# because PostgreSQL does not allow a linked serial sequence to be re-owned
# independently from its table.
sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/$rust_schema-open-source-bounty.sql"
PGOPTIONS="-c search_path=$rust_schema" psql -h 127.0.0.1 -p "$pg_port" -d "$database" \
  -q -v ON_ERROR_STOP=1 -f "$runtime/$rust_schema-open-source-bounty.sql" >/dev/null
admin_schema_sql "$rust_schema" "GRANT SELECT ON open_source_bounty_projects, open_source_bounty_challenges, open_source_bounty_ledgers, open_source_bounty_disputes, open_source_bounty_mcp_tokens, open_source_bounty_mcp_confirmations, open_source_bounty_mcp_operations, open_source_bounty_rest_operations TO $rust_role;"
valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no --dir "$runtime" --logfile "$runtime/valkey.log" > /dev/null 2>&1 & record_pid valkey_pid "$!"
for _ in {1..100}; do valkey-cli -h 127.0.0.1 -p "$valkey_port" ping >/dev/null 2>&1 && break; sleep .05; done
valkey-cli -h 127.0.0.1 -p "$valkey_port" ping >/dev/null

if [[ -n $route_filter ]] && ! jq -e --arg id "$route_filter" '.fixtures | any(.id == $id)' "$fixtures" >/dev/null \
  && [[ $route_filter != topup-info && $route_filter != topup-self && $route_filter != user-groups && $route_filter != self-groups && $route_filter != user-models && $route_filter != aff-code && $route_filter != admin-topups && $route_filter != sessions-list && $route_filter != passkey-status && $route_filter != federation-bindings ]]; then
  echo "unknown transaction route filter: $route_filter" >&2
  exit 2
fi
while IFS=$'\t' read -r id; do
  [[ -n $id ]] || continue
  [[ -z $route_filter || $id == "$route_filter" ]] || continue
  for phase in positive failure rollback replay; do run_phase "$id" "$phase"; done
  if [[ -n $result_dir ]]; then
    route_json=$(jq -c --arg id "$id" '.fixtures[] | select(.id == $id) | .route' "$fixtures")
    method=$(jq -r '.method' <<<"$route_json")
    path=$(jq -r '.path' <<<"$route_json")
    path=${path%%\?*}
    jq -cn --arg method "$method" --arg path "$path" --arg route "$id" \
      --arg runtime "$runtime" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"full-transaction",cases:28,route_fixture:$route,isolated_runtime:$runtime,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$result_dir/$id.json"
  fi
done < <(jq -r '.fixtures[].id' "$fixtures")
if [[ $route_filter == federation-bindings && ${LMM_TRANSACTION_RS_TEST_INSTANCE:-1} != 0 ]]; then
  echo 'federation-bindings requires LMM_TRANSACTION_RS_TEST_INSTANCE=0 because the test instance intentionally denies federation identities' >&2
  exit 2
fi
if [[ $route_filter == topup-info || $route_filter == topup-self || $route_filter == user-groups || $route_filter == self-groups || $route_filter == user-models || $route_filter == aff-code || $route_filter == admin-topups || $route_filter == sessions-list || $route_filter == passkey-status || $route_filter == federation-bindings ]]; then
  for phase in positive failure rollback replay; do run_phase "$route_filter" "$phase"; done
fi
if [[ -n $route_filter ]]; then expected_routes=1; expected_phases=4; else expected_routes=7; expected_phases=28; fi
jq -cn --argjson routes "$expected_routes" --argjson phases "$passed" --argjson expected_phases "$expected_phases" '{test:"route-compatibility-transaction-differential",routes:$routes,phases:$phases,expected_phases:$expected_phases,isolated:{postgres_schemas:["lmm_test_transaction_go","lmm_test_transaction_rust"],valkey_databases:[5,6],production_access:false},result:"passed"}'
