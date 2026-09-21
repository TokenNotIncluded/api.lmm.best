#!/usr/bin/env bash
# Contract gate for the legacy Stripe/Creem top-up routes.  Static mode reads
# only repository sources.  Execute mode creates a disposable Postgres,
# Valkey, and a 127.0.0.1-only provider fixture; it never accepts a real
# provider URL or credential from the environment.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
route_source="$repo_root/apps/api-rust/src/routes/stripe_creem.rs"
test_instance_source="$repo_root/apps/api-rust/src/test_instance.rs"
pg_port=${LMM_STRIPE_CREEM_PG_PORT:-55493}
go_port=${LMM_STRIPE_CREEM_GO_PORT:-13093}
rust_port=${LMM_STRIPE_CREEM_RUST_PORT:-33093}
fixture_port=${LMM_STRIPE_CREEM_FIXTURE_PORT:-19093}
valkey_port=${LMM_STRIPE_CREEM_VALKEY_PORT:-16393}

# Install cleanup before allocating any temporary resource.  Every executable
# path checks this prefix again, so a malformed variable can never widen a
# recursive removal target.
runtime=''
fixture_secret=''
fixture_secret_file=''
go_pid=''
rust_pid=''
fixture_pid=''
valkey_pid=''

cleanup() {
  local pid
  for pid in "$go_pid" "$rust_pid" "$fixture_pid" "$valkey_pid"; do
    [[ -z $pid ]] || kill "$pid" 2>/dev/null || true
  done
  for pid in "$go_pid" "$rust_pid" "$fixture_pid" "$valkey_pid"; do
    [[ -z $pid ]] || wait "$pid" 2>/dev/null || true
  done
  if [[ -n $runtime && -d $runtime/pg ]] && command -v pg_ctl >/dev/null 2>&1; then
    pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  fi
  case "$runtime" in
    '') ;;
    /tmp/lmm-stripe-creem.*) rm -rf "$runtime" ;;
    *) echo "refusing unexpected runtime cleanup target: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

require_commands() {
  local command
  for command in "$@"; do
    command -v "$command" >/dev/null || {
      echo "required command unavailable: $command" >&2
      return 1
    }
  done
}

require_source_contract() {
  local production_source
  production_source=$(sed '/^#\[cfg(test)\]/,$d' "$route_source")
  rg -Fq '.route("/api/user/stripe/amount", post(stripe_amount))' "$route_source"
  rg -Fq '.route("/api/user/stripe/pay", post(stripe_pay))' "$route_source"
  rg -Fq '.route("/api/user/creem/pay", post(creem_pay))' "$route_source"
  rg -Fq 'extract::{Request, State}' "$route_source"
  rg -Fq 'const MAX_LEGACY_TOPUP_BODY_BYTES: usize = 1024 * 1024;' "$route_source"
  rg -Fq 'async fn legacy_json<T: DeserializeOwned + Default>' "$route_source"
  rg -Fq 'enum LegacyJsonError' "$route_source"
  rg -Fq 'LoopbackStripeCreemGateway' "$route_source"
  rg -Fq 'endpoint.scheme() != "http" || endpoint.host_str() != Some("127.0.0.1")' "$route_source"
  rg -Fq 'BLOCKED(shared listener)' "$route_source"
  rg -Fq 'DisabledStripeCreemGateway' "$test_instance_source"
  if rg -Fq 'JsonRejection' "$route_source"; then
    echo "route source must parse JSON only after UserAuth" >&2
    return 1
  fi
  if grep -Eq 'StripeApiSecret|CreemApiKey' <<<"$production_source"; then
    echo "route source contains a production credential implementation" >&2
    return 1
  fi
}

require_legacy_order() {
  local stripe_pay creem_pay
  stripe_pay=$(sed -n '/func (\*StripeAdaptor) RequestPay/,/^}/p' "$legacy_root/controller/topup_stripe.go")
  creem_pay=$(sed -n '/func (\*CreemAdaptor) RequestPay/,/^}/p' "$legacy_root/controller/topup_creem.go")
  [[ $(grep -n 'genStripeLink' <<<"$stripe_pay" | cut -d: -f1) -lt $(grep -n 'topUp.Insert' <<<"$stripe_pay" | cut -d: -f1) ]]
  [[ $(grep -n 'topUp.Insert' <<<"$creem_pay" | cut -d: -f1) -lt $(grep -n 'genCreemLink' <<<"$creem_pay" | cut -d: -f1) ]]
  rg -Fq 'if request.product_id.is_empty() {' "$route_source"
  rg -Fq 'money: (request.amount as f64) * ratio' "$route_source"
  rg -Fq 'money: product.price' "$route_source"
  rg -Fq 'legacy_trade_no("new-api-ref", actor.user_id)' "$route_source"
  rg -Fq 'legacy_trade_no("creem-api-ref", actor.user_id)' "$route_source"
  rg -Fq 'amount_discounts' "$route_source"
  rg -Fq 'quota_display_type == "TOKENS"' "$route_source"
}

preflight_port() {
  local name=$1 port=$2
  if (exec 3<>"/dev/tcp/127.0.0.1/$port"; exec 3>&-) 2>/dev/null; then
    echo "$name port is already occupied: 127.0.0.1:$port" >&2
    return 1
  fi
}

wait_for_http_pid() {
  local pid=$1 url=$2 status
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || {
      echo "listener exited before readiness: $url" >&2
      return 1
    }
    status=$(curl --silent --output /dev/null --write-out '%{http_code}' "$url" || true)
    [[ $status != 000 ]] && return 0
    sleep .05
  done
  echo "listener did not become ready: $url" >&2
  return 1
}

wait_for_valkey() {
  for _ in {1..100}; do
    kill -0 "$valkey_pid" 2>/dev/null || {
      echo "disposable Valkey exited before readiness" >&2
      return 1
    }
    valkey-cli --no-auth-warning -a "$fixture_secret" -h 127.0.0.1 -p "$valkey_port" ping \
      >/dev/null 2>&1 && return 0
    sleep .05
  done
  echo "disposable Valkey did not become ready" >&2
  return 1
}

prepare_runtime() {
  runtime=$(mktemp -d /tmp/lmm-stripe-creem.XXXXXX)
  fixture_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
  fixture_secret_file="$runtime/provider.secret"
  printf '%s' "$fixture_secret" >"$fixture_secret_file"
  chmod 600 "$fixture_secret_file"
}

start_disposable_dependencies() {
  initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
  pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
    -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
  pg_ctl -D "$runtime/pg" status >/dev/null
  valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no \
    --requirepass "$fixture_secret" --dir "$runtime" --logfile "$runtime/valkey.log" \
    >/dev/null 2>&1 &
  valkey_pid=$!
  wait_for_valkey
}

start_loopback_fixture() {
  FIXTURE_PORT="$fixture_port" FIXTURE_SECRET_FILE="$fixture_secret_file" FIXTURE_LOG="$runtime/provider.requests.jsonl" \
    python3 - <<'PY' &
import http.server
import json
import os
import pathlib
import socketserver

port = int(os.environ["FIXTURE_PORT"])
secret = pathlib.Path(os.environ["FIXTURE_SECRET_FILE"]).read_text()
log_path = pathlib.Path(os.environ["FIXTURE_LOG"])

class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        if self.path != "/healthz":
            self.send_error(404)
            return
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"ok")

    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        self.rfile.read(length)
        # Never persist request headers or bodies: they could contain a secret.
        log_path.open("a").write(json.dumps({"method": "POST", "path": self.path}) + "\n")
        if self.headers.get("x-fixture-secret") != secret:
            self.send_error(403)
            return
        if self.path == "/stripe/checkout":
            response = {"pay_link": "http://127.0.0.1/stripe-checkout"}
        elif self.path == "/creem/checkout":
            response = {"checkout_url": "http://127.0.0.1/creem-checkout"}
        else:
            self.send_error(404)
            return
        encoded = json.dumps(response).encode()
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.send_header("content-length", str(len(encoded)))
        self.end_headers()
        self.wfile.write(encoded)

class LoopbackServer(socketserver.TCPServer):
    allow_reuse_address = False

with LoopbackServer(("127.0.0.1", port), Fixture) as server:
    server.serve_forever()
PY
  fixture_pid=$!
  wait_for_http_pid "$fixture_pid" "http://127.0.0.1:$fixture_port/healthz"
}

fixture_post() {
  local path=$1
  local config="$runtime/fixture.curl"
  printf 'header = "x-fixture-secret: %s"\nheader = "content-type: application/json"\ndata = "{}"\n' \
    "$fixture_secret" >"$config"
  chmod 600 "$config"
  curl --silent --show-error --fail --config "$config" "http://127.0.0.1:$fixture_port$path"
}

fixture_request_count() {
  [[ -f $runtime/provider.requests.jsonl ]] || { printf '0\n'; return; }
  jq -s 'length' "$runtime/provider.requests.jsonl"
}

assert_fixture_request() {
  local path=$1 expected=$2
  [[ $(jq -s --arg path "$path" '[.[] | select(.method == "POST" and .path == $path)] | length' \
    "$runtime/provider.requests.jsonl") == "$expected" ]]
}

bootstrap_listener_database() {
  local database=$1
  createdb -h 127.0.0.1 -p "$pg_port" "$database"
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
CREATE TABLE IF NOT EXISTS top_ups (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  amount BIGINT NOT NULL,
  money NUMERIC NOT NULL,
  trade_no TEXT NOT NULL UNIQUE,
  payment_method TEXT NOT NULL,
  payment_provider TEXT NOT NULL,
  create_time BIGINT NOT NULL,
  status TEXT NOT NULL
);
SQL
}

topup_count() {
  local database=$1
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -Atqc 'SELECT COUNT(*) FROM top_ups'
}

# A caller may supply already-built isolated listener binaries.  The command
# itself is deliberately not synthesized from arbitrary shell text: this keeps
# execute mode auditable and prevents environment-provided command injection.
start_candidate_listener() {
  local name=$1 binary=$2 port=$3 database=$4
  local database_url="postgresql://127.0.0.1:$pg_port/$database?sslmode=disable"
  [[ -x $binary ]] || { echo "$name listener is not executable: $binary" >&2; return 1; }
  DATABASE_URL="$database_url" SQL_DSN="$database_url" REDIS_CONN_STRING="redis://:$fixture_secret@127.0.0.1:$valkey_port/0" \
    LISTEN_ADDR="127.0.0.1:$port" "$binary" >"$runtime/$name.listener.log" 2>&1 &
  local pid=$!
  if [[ $name == go ]]; then go_pid=$pid; else rust_pid=$pid; fi
  wait_for_http_pid "$pid" "http://127.0.0.1:$port/api/status"
}

assert_unauthenticated_route_is_inert() {
  local name=$1 port=$2 database=$3 status
  status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
    --header 'content-type: application/json' --data '{"amount":1}' \
    "http://127.0.0.1:$port/api/user/stripe/amount" || true)
  [[ $status == 401 ]] || {
    echo "$name unauthenticated route expected HTTP 401, got $status" >&2
    return 1
  }
  [[ $(fixture_request_count) == 2 ]] || {
    echo "$name unauthenticated route reached the provider fixture" >&2
    return 1
  }
  [[ $(topup_count "$database") == 0 ]] || {
    echo "$name unauthenticated route wrote a pending top-up" >&2
    return 1
  }
}

require_commands awk curl git grep jq rg sed
[[ -f $route_source ]] || { echo "missing Rust route source: $route_source" >&2; exit 1; }
[[ -f $test_instance_source ]] || { echo "missing Rust test instance source: $test_instance_source" >&2; exit 1; }
[[ -f $legacy_root/controller/topup_stripe.go ]] || { echo "missing frozen Stripe handler" >&2; exit 1; }
[[ -f $legacy_root/controller/topup_creem.go ]] || { echo "missing frozen Creem handler" >&2; exit 1; }
require_source_contract
require_legacy_order

if [[ ${LMM_STRIPE_CREEM_EXECUTE:-0} != 1 ]]; then
  jq -cn \
    --arg source "$route_source" \
    --argjson ports "[$pg_port,$go_port,$rust_port,$fixture_port,$valkey_port]" \
    '{test:"stripe-creem-provider-differential",mode:"static-contract",source:$source,ports:$ports,provider:"loopback-only fixture",credentials:"none materialized in static mode",approval_credit:false,reason:"The compiled Rust test instance injects DisabledStripeCreemGateway; shared Auth-Version and CriticalRateLimit are explicitly blocked at this route slice."}'
  exit 0
fi

require_commands createdb curl initdb jq od pg_ctl psql python3 rg valkey-cli valkey-server
preflight_port PostgreSQL "$pg_port"
preflight_port Go_HTTP "$go_port"
preflight_port Rust_HTTP "$rust_port"
preflight_port Provider_Fixture "$fixture_port"
preflight_port Valkey "$valkey_port"
prepare_runtime
start_disposable_dependencies
start_loopback_fixture

# Validate both fixture contracts without exposing request data or secrets.
[[ $(fixture_post /stripe/checkout | jq -r '.pay_link') == http://127.0.0.1/stripe-checkout ]]
[[ $(fixture_post /creem/checkout | jq -r '.checkout_url') == http://127.0.0.1/creem-checkout ]]
[[ $(fixture_request_count) == 2 ]]
assert_fixture_request /stripe/checkout 1
assert_fixture_request /creem/checkout 1

go_binary=${LMM_STRIPE_CREEM_GO_BINARY:-}
rust_binary=${LMM_STRIPE_CREEM_RUST_BINARY:-}
if [[ -n $go_binary && -n $rust_binary ]]; then
  bootstrap_listener_database lmm_stripe_creem_go
  bootstrap_listener_database lmm_stripe_creem_rust
  start_candidate_listener go "$go_binary" "$go_port" lmm_stripe_creem_go
  start_candidate_listener rust "$rust_binary" "$rust_port" lmm_stripe_creem_rust
  assert_unauthenticated_route_is_inert go "$go_port" lmm_stripe_creem_go
  assert_unauthenticated_route_is_inert rust "$rust_port" lmm_stripe_creem_rust
fi

jq -cn \
  --arg go_listener "$go_binary" \
  --arg rust_listener "$rust_binary" \
  '{test:"stripe-creem-provider-differential",mode:"isolated-fixture-safety",provider:"127.0.0.1 only",fixture_contracts:["stripe","creem"],listener_commands_reserved:true,listeners_started:($go_listener != "" and $rust_listener != ""),approval_credit:false,reason:"Positive provider route assertions remain deliberately blocked: the production test instance mounts DisabledStripeCreemGateway and this slice cannot own shared Auth-Version or CriticalRateLimit."}'
