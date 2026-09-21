#!/usr/bin/env bash
# Isolated, fail-closed contract runner for four legacy Waffo wallet routes.
#
# Default mode validates source-ordering only.  Execution is opt-in because the
# standard Rust test instance intentionally injects a deny gateway: passing
# this runner never grants route or production ownership credit.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
route_source="$repo_root/apps/api-rust/src/routes/waffo.rs"
pg_port=${LMM_WAFFO_PG_PORT:-55510}
go_port=${LMM_WAFFO_GO_PORT:-13110}
rust_port=${LMM_WAFFO_RUST_PORT:-33110}
go_valkey_port=${LMM_WAFFO_GO_VALKEY_PORT:-16510}
rust_valkey_port=${LMM_WAFFO_RUST_VALKEY_PORT:-16511}
fixture_port=${LMM_WAFFO_FIXTURE_PORT:-19110}
runtime_base=${LMM_WAFFO_RUNTIME_BASE:-/tmp}

plan() {
  jq -cn \
    --arg source "$route_source" \
    --argjson routes '["POST /api/user/waffo/amount","POST /api/user/waffo/pay","POST /api/user/waffo-pancake/amount","POST /api/user/waffo-pancake/pay"]' \
    '{test:"waffo-provider-differential",mode:"plan-only",source:$source,routes:$routes,isolated:{postgres_major:18,valkey_instances:2,provider_fixture:"127.0.0.1 only",per_run_secret:true,pid_owned_cleanup:true},checks:["UserAuth-before-body","currency-rounding","group-ratio-discount-token-normalization","pending-order-before-provider","failed-order-on-provider-error","credential-redaction"],approval_credit:false}'
}

require_source_contract() {
  [[ -f $route_source ]] || { echo "missing Rust route source: $route_source" >&2; return 1; }
  [[ -f $legacy_root/controller/topup_waffo.go ]] || { echo "missing frozen Go Waffo handler" >&2; return 1; }
  [[ -f $legacy_root/controller/topup_waffo_pancake.go ]] || { echo "missing frozen Go Pancake handler" >&2; return 1; }

  for route in \
    '/api/user/waffo/amount' \
    '/api/user/waffo/pay' \
    '/api/user/waffo-pancake/amount' \
    '/api/user/waffo-pancake/pay'; do
    rg -Fq ".route(\"$route\", post(" "$route_source"
  done
  rg -Fq 'to_bytes(request.into_body()' "$route_source"
  rg -Fq 'extract::{Request, State}' "$route_source"
  rg -Fq 'async fn legacy_json<T: DeserializeOwned + Default>' "$route_source"
  rg -Fq 'let actor = match authenticated(&state, &headers).await' "$route_source"
  rg -Fq 'legacy_json(request).await' "$route_source"
  rg -Fq 'fn format_amount(' "$route_source"
  rg -Fq 'fn normalized_amount(amount: i64, settings: &BTreeMap<String, String>)' "$route_source"
  rg -Fq 'fn waffo_provider_config(settings: &BTreeMap<String, String>)' "$route_source"
  rg -Fq 'fn pancake_provider_config(settings: &BTreeMap<String, String>)' "$route_source"
  rg -Fq 'mark_failed(&state.pg, &order_id)' "$route_source"
  rg -Fq 'impl std::fmt::Debug for WaffoProviderConfig' "$route_source"
  rg -Fq '"[REDACTED]"' "$route_source"

  # Both frozen handlers insert their local pending order before checkout.
  local waffo_insert waffo_create pancake_insert pancake_create
  waffo_insert=$(grep -n 'topUp.Insert' "$legacy_root/controller/topup_waffo.go" | head -n1 | cut -d: -f1)
  waffo_create=$(grep -n 'sdk.Order().Create' "$legacy_root/controller/topup_waffo.go" | head -n1 | cut -d: -f1)
  pancake_insert=$(grep -n 'topUp.Insert' "$legacy_root/controller/topup_waffo_pancake.go" | head -n1 | cut -d: -f1)
  pancake_create=$(grep -n 'CreateWaffoPancakeCheckoutSession' "$legacy_root/controller/topup_waffo_pancake.go" | head -n1 | cut -d: -f1)
  [[ $waffo_insert -lt $waffo_create ]]
  [[ $pancake_insert -lt $pancake_create ]]
}

if [[ ${LMM_WAFFO_RUN:-0} != 1 ]]; then
  require_source_contract
  plan
  exit 0
fi

for command in createdb curl git initdb jq od pg_ctl postgres psql valkey-cli valkey-server python3 rg; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
[[ -d $runtime_base && -w $runtime_base ]] || { echo "runtime base is not writable: $runtime_base" >&2; exit 1; }
go_listener=${LMM_WAFFO_GO_LISTENER:-}
rust_listener=${LMM_WAFFO_RUST_LISTENER:-}
[[ -n $go_listener && -x $go_listener ]] || { echo "set executable LMM_WAFFO_GO_LISTENER" >&2; exit 2; }
[[ -n $rust_listener && -x $rust_listener ]] || { echo "set executable LMM_WAFFO_RUST_LISTENER" >&2; exit 2; }
require_source_contract

preflight_port() {
  local name=$1 port=$2
  if (exec 3<>"/dev/tcp/127.0.0.1/$port"; exec 3>&-) 2>/dev/null; then
    echo "$name port is already occupied: 127.0.0.1:$port" >&2
    exit 1
  fi
}
for spec in \
  "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" \
  "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port" "Provider_fixture:$fixture_port"; do
  preflight_port "${spec%%:*}" "${spec##*:}"
done

runtime=$(mktemp -d "$runtime_base/lmm-waffo.XXXXXX")
fixture_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
chmod 700 "$runtime"
go_database=lmm_waffo_go
rust_database=lmm_waffo_rust
go_schema=lmm_test_waffo_go
rust_schema=lmm_test_waffo_rust
go_role=lmm_test_waffo_go
rust_role=lmm_test_waffo_rust

cleanup() {
  for pid in "${go_pid:-}" "${rust_pid:-}" "${fixture_pid:-}" "${go_valkey_pid:-}" "${rust_valkey_pid:-}"; do
    [[ -z $pid ]] || kill "$pid" 2>/dev/null || true
  done
  for pid in "${go_pid:-}" "${rust_pid:-}" "${fixture_pid:-}" "${go_valkey_pid:-}" "${rust_valkey_pid:-}"; do
    [[ -z $pid ]] || wait "$pid" 2>/dev/null || true
  done
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    "$runtime_base"/lmm-waffo.*) rm -rf "$runtime" ;;
    *) echo "refusing unexpected runtime cleanup target: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM

wait_for_pid_http() {
  local pid=$1 url=$2
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 1
    curl --fail --silent --show-error --connect-timeout 1 "$url" >/dev/null 2>&1 && return 0
    sleep .05
  done
  return 1
}
wait_for_valkey() {
  local pid=$1 port=$2
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 1
    [[ $(valkey-cli --no-auth-warning -a "$fixture_secret" -h 127.0.0.1 -p "$port" ping 2>/dev/null) == PONG ]] && return 0
    sleep .05
  done
  return 1
}

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" "$rust_database"
psql -h 127.0.0.1 -p "$pg_port" -d "$go_database" -v ON_ERROR_STOP=1 -c "CREATE ROLE $go_role LOGIN; CREATE SCHEMA $go_schema AUTHORIZATION $go_role;" >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_database" -v ON_ERROR_STOP=1 -c "CREATE ROLE $rust_role LOGIN; CREATE SCHEMA $rust_schema AUTHORIZATION $rust_role;" >/dev/null
for target in "$go_database:$go_schema" "$rust_database:$rust_schema"; do
  database=${target%%:*}
  schema=${target##*:}
  sed "s/public\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/$schema.sql"
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 -f "$runtime/$schema.sql" >/dev/null
done

for spec in "go:$go_valkey_port" "rust:$rust_valkey_port"; do
  name=${spec%%:*}
  port=${spec##*:}
  config="$runtime/$name-valkey.conf"
  {
    printf 'bind 127.0.0.1\nport %s\nprotected-mode yes\nsave \nappendonly no\ndir %s\nrequirepass %s\n' "$port" "$runtime" "$fixture_secret"
  } >"$config"
  chmod 600 "$config"
  valkey-server "$config" >"$runtime/$name-valkey.log" 2>&1 &
  if [[ $name == go ]]; then go_valkey_pid=$!; else rust_valkey_pid=$!; fi
done
wait_for_valkey "$go_valkey_pid" "$go_valkey_port"
wait_for_valkey "$rust_valkey_pid" "$rust_valkey_port"

# The fixture exposes no credential, request body, or production address.
FIXTURE_PORT="$fixture_port" python3 - <<'PY' >"$runtime/provider.log" 2>&1 &
import http.server
import os
import socketserver

class Handler(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass
    def do_GET(self):
        self.send_response(200 if self.path == "/healthz" else 404)
        self.end_headers()
    def do_POST(self):
        self.send_response(501)
        self.end_headers()

class LoopbackServer(socketserver.TCPServer):
    allow_reuse_address = False

with LoopbackServer(("127.0.0.1", int(os.environ["FIXTURE_PORT"])), Handler) as server:
    server.serve_forever()
PY
fixture_pid=$!
wait_for_pid_http "$fixture_pid" "http://127.0.0.1:$fixture_port/healthz"

go_dsn="postgresql://127.0.0.1:$pg_port/$go_database?sslmode=disable&options=-csearch_path=$go_schema"
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path=$rust_schema"
env DATABASE_URL="$go_dsn" VALKEY_URL="redis://:$fixture_secret@127.0.0.1:$go_valkey_port" PORT="$go_port" \
  LMM_TEST_PROVIDER_BASE_URL="http://127.0.0.1:$fixture_port" "$go_listener" >"$runtime/go.log" 2>&1 &
go_pid=$!
env DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$fixture_secret@127.0.0.1:$rust_valkey_port" PORT="$rust_port" \
  LMM_TEST_PROVIDER_BASE_URL="http://127.0.0.1:$fixture_port" "$rust_listener" >"$runtime/rust.log" 2>&1 &
rust_pid=$!
wait_for_pid_http "$go_pid" "http://127.0.0.1:$go_port/livez"
wait_for_pid_http "$rust_pid" "http://127.0.0.1:$rust_port/livez"

compare_unauthenticated_malformed() {
  local path=$1 go_out rust_out
  go_out=$(curl --silent --show-error -X POST --data 'not-json' "http://127.0.0.1:$go_port$path" -w '\n%{http_code}')
  rust_out=$(curl --silent --show-error -X POST --data 'not-json' "http://127.0.0.1:$rust_port$path" -w '\n%{http_code}')
  diff -u <(printf '%s\n' "$go_out") <(printf '%s\n' "$rust_out") || { echo "UserAuth-before-body mismatch: $path" >&2; return 1; }
}
for path in /api/user/waffo/amount /api/user/waffo/pay /api/user/waffo-pancake/amount /api/user/waffo-pancake/pay; do
  compare_unauthenticated_malformed "$path"
done

jq -cn '{test:"waffo-provider-differential",mode:"isolated-negative",routes:4,auth_before_body:true,provider:"127.0.0.1 deny fixture",approval_credit:false,reason:"Positive checkout, order side-effect, and replay parity require an injected loopback TopUpGateway; the standard test instance intentionally denies it."}'
