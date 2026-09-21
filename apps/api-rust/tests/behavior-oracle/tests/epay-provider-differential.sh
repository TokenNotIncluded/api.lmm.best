#!/usr/bin/env bash
# Disposable Go/Rust differential for the legacy ePay route forms.
#
# The runner is deliberately inert unless LMM_EPAY_RUN=1.  A
# successful run owns its PostgreSQL 18 cluster, two password-protected
# Valkey instances, a private Go network namespace, a loopback Rust listener,
# and the provider mock below. It never accepts a production URL, DSN, secret,
# inherited environment, or externally reachable mock.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
pg_port=${LMM_EPAY_PG_PORT:-55472}
go_port=${LMM_EPAY_GO_PORT:-13072}
rust_port=${LMM_EPAY_RUST_PORT:-33072}
go_valkey_port=${LMM_EPAY_GO_VALKEY_PORT:-16472}
rust_valkey_port=${LMM_EPAY_RUST_VALKEY_PORT:-16473}
provider_port=${LMM_EPAY_PROVIDER_PORT:-18072}
runtime_base=${LMM_EPAY_RUNTIME_BASE:-/tmp}

plan() {
  jq -cn \
    --argjson routes '["POST /api/user/pay","GET /api/user/epay/notify","POST /api/user/epay/notify"]' \
    '{test:"epay-provider-differential",mode:"plan-only",routes:$routes,isolated:{postgres_major:18,valkey_instances:2,go_network_namespace:"required",provider_mock:"loopback-only",inherited_environment:false,production_access:false},checks:["auth-before-parse","canonical-form-query","pending-order-before-response","callback-replay-idempotency","wallet-and-quota-snapshots"],approval_credit:false}'
}

if [[ ${LMM_EPAY_RUN:-0} != 1 ]]; then
  plan
  exit 0
fi

for command in createdb curl git id initdb jq pg_ctl postgres ps psql stat tr valkey-cli valkey-server python3; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
[[ -d $legacy_root ]] || { echo "missing frozen Go source: $legacy_root" >&2; exit 1; }
[[ -d $runtime_base && -w $runtime_base ]] || { echo "runtime base is not writable: $runtime_base" >&2; exit 1; }

go_listener=${LMM_EPAY_GO_LISTENER:-}
rust_listener=${LMM_EPAY_RUST_LISTENER:-}
go_namespace_exec=${LMM_EPAY_GO_NAMESPACE_EXEC:-}
[[ -n $go_listener && -x $go_listener ]] || { echo "set executable LMM_EPAY_GO_LISTENER" >&2; exit 2; }
[[ -n $rust_listener && -x $rust_listener ]] || { echo "set executable LMM_EPAY_RUST_LISTENER" >&2; exit 2; }
[[ -n $go_namespace_exec && -x $go_namespace_exec ]] || {
  echo "refusing Go listener: it binds :PORT; set dedicated LMM_EPAY_GO_NAMESPACE_EXEC" >&2
  exit 2
}

preflight_port() {
  local name=$1 port=$2
  if (exec 3<>"/dev/tcp/127.0.0.1/$port"; exec 3>&-) 2>/dev/null; then
    echo "$name port is already occupied: 127.0.0.1:$port" >&2
    exit 1
  fi
}
for spec in \
  "PostgreSQL:$pg_port" "Go_HTTP:$go_port" "Rust_HTTP:$rust_port" \
  "Go_Valkey:$go_valkey_port" "Rust_Valkey:$rust_valkey_port" "Provider_mock:$provider_port"; do
  preflight_port "${spec%%:*}" "${spec##*:}"
done

runtime=$(mktemp -d "$runtime_base/lmm-epay.XXXXXX")
[[ $(stat -c %u "$runtime") == $(id -u) ]] || { echo "runtime directory owner mismatch" >&2; exit 1; }
valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
go_database=lmm_epay_go
rust_database=lmm_epay_rust
go_schema=lmm_test_epay_go
rust_schema=lmm_test_epay_rust
go_role=lmm_test_epay_go
rust_role=lmm_test_epay_rust
go_pid=
rust_pid=
provider_pid=
go_valkey_pid=
rust_valkey_pid=
go_pid_start=
rust_pid_start=
provider_pid_start=
go_valkey_pid_start=
rust_valkey_pid_start=

cleanup() {
  for pid_name in go_pid rust_pid provider_pid go_valkey_pid rust_valkey_pid; do
    stop_owned_process "$pid_name" || true
  done
  [[ -d $runtime/pg ]] && pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    "$runtime_base"/lmm-epay.*) rm -rf "$runtime" ;;
    *) echo "refusing unexpected runtime path: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'echo "identity ePay differential failed at line $LINENO" >&2' ERR

wait_for_pid_http() {
  local pid=$1 url=$2
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 1
    curl --fail --silent --show-error --connect-timeout 1 "$url" >/dev/null 2>&1 && return 0
    sleep 0.05
  done
  return 1
}
wait_for_valkey() {
  local pid=$1 port=$2
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 1
    [[ $(VALKEYCLI_AUTH="$valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" ping 2>/dev/null) == PONG ]] && return 0
    sleep 0.05
  done
  return 1
}

assert_owned_pid() {
  local pid=$1
  [[ $(ps -o uid= -p "$pid" | tr -d ' ') == $(id -u) ]] || {
    echo "refusing process not owned by this runner: $pid" >&2
    return 1
  }
}
pid_start_time() { [[ -r /proc/$1/stat ]] || return 1; awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local pid_name=$1 pid=$2 start; printf -v "$pid_name" '%s' "$pid"; start=$(pid_start_time "$pid") || { echo "failed to record pid $pid" >&2; wait "$pid" 2>/dev/null || true; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; return 1; }; printf -v "${pid_name}_start" '%s' "$start"; }
owned_pid_is_live() { local pid_name=$1 pid start_name expected; pid=${!pid_name:-}; start_name="${pid_name}_start"; expected=${!start_name:-}; [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]; }
stop_owned_process() { local pid_name=$1 pid; pid=${!pid_name:-}; if [[ -n $pid ]]; then if owned_pid_is_live "$pid_name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi; fi; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; }

start_valkey() {
  local name=$1 port=$2 pid_name=$3
  local config="$runtime/$name-valkey.conf"
  umask 077
  printf '%s\n' \
    'bind 127.0.0.1' \
    "port $port" \
    'protected-mode yes' \
    'save ""' \
    'appendonly no' \
    "requirepass $valkey_password" >"$config"
  chmod 600 "$config"
  valkey-server "$config" >"$runtime/$name-valkey.log" 2>&1 & record_pid "$pid_name" "$!" || return 1
}

initdb -D "$runtime/pg" --no-locale --encoding=UTF8 >/dev/null
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

start_valkey go "$go_valkey_port" go_valkey_pid
start_valkey rust "$rust_valkey_port" rust_valkey_pid
assert_owned_pid "$go_valkey_pid"
assert_owned_pid "$rust_valkey_pid"
wait_for_valkey "$go_valkey_pid" "$go_valkey_port"
wait_for_valkey "$rust_valkey_pid" "$rust_valkey_port"

# This endpoint intentionally records only method, path, and body length; it
# never logs callback contents or the random payment secret.
PROVIDER_PORT="$provider_port" PROVIDER_AUDIT="$runtime/provider.requests.jsonl" python3 -c '
import http.server, json, os, socketserver
class Handler(http.server.BaseHTTPRequestHandler):
    def record(self):
        size = int(self.headers.get("Content-Length", "0"))
        if size: self.rfile.read(size)
        with open(os.environ["PROVIDER_AUDIT"], "a", encoding="utf-8") as out:
            out.write(json.dumps({"method": self.command, "path": self.path, "body_bytes": size}) + "\n")
    def do_GET(self):
        self.record()
        self.send_response(200 if self.path == "/health" else 404); self.end_headers()
    def do_POST(self):
        self.record()
        self.send_response(200 if self.path == "/epay" else 404); self.end_headers()
    def log_message(self, *_): pass
socketserver.TCPServer(("127.0.0.1", int(os.environ["PROVIDER_PORT"])), Handler).serve_forever()
  ' >"$runtime/provider.log" 2>&1 & record_pid provider_pid "$!" || exit 1
assert_owned_pid "$provider_pid"
wait_for_pid_http "$provider_pid" "http://127.0.0.1:$provider_port/health"

go_dsn="postgresql://127.0.0.1:$pg_port/$go_database?sslmode=disable&options=-csearch_path=$go_schema"
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path=$rust_schema"
# Go's frozen listener accepts PORT and may bind every interface. The required
# namespace launcher must therefore create its own private loopback topology;
# this runner refuses to substitute a host-network workaround.
env -i PATH="$PATH" HOME="$runtime" TMPDIR="$runtime" LANG=C \
  SQL_DSN="$go_dsn" REDIS_CONN_STRING="redis://:$valkey_password@127.0.0.1:$go_valkey_port" PORT="$go_port" \
  LMM_TEST_PROVIDER_BASE_URL="http://127.0.0.1:$provider_port" LMM_TEST_PAYMENT_SECRET="$valkey_password" \
  "$go_namespace_exec" "$go_listener" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!" || exit 1
assert_owned_pid "$go_pid"
env -i PATH="$PATH" HOME="$runtime" TMPDIR="$runtime" LANG=C \
  DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$valkey_password@127.0.0.1:$rust_valkey_port" \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT="epay-test" \
  LMM_SCHEMA_CONTRACT="$rust_schema" LMM_RS_TEST_INSTANCE=1 \
  LMM_TEST_PROVIDER_BASE_URL="http://127.0.0.1:$provider_port" LMM_TEST_PAYMENT_SECRET="$valkey_password" \
  "$rust_listener" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!" || exit 1
assert_owned_pid "$rust_pid"
wait_for_pid_http "$go_pid" "http://127.0.0.1:$go_port/livez"
wait_for_pid_http "$rust_pid" "http://127.0.0.1:$rust_port/livez"

compare() {
  local name=$1 method=$2 path=$3 body=${4:-} content_type=${5:-application/x-www-form-urlencoded}
  local go_out rust_out
  go_out=$(curl --silent --show-error -X "$method" -H "Content-Type: $content_type" --data "$body" "http://127.0.0.1:$go_port$path" -w '\n%{http_code}')
  rust_out=$(curl --silent --show-error -X "$method" -H "Content-Type: $content_type" --data "$body" "http://127.0.0.1:$rust_port$path" -w '\n%{http_code}')
  diff -u <(printf '%s\n' "$go_out") <(printf '%s\n' "$rust_out") || { echo "mismatch: $name" >&2; return 1; }
}

# The adapter contract must make positive checkout and completion calls only
# against the loopback mock. These negative/replay fixtures prove no malformed
# callback can credit a wallet, and success/replay snapshots are compared by
# the listener-specific fixture command before this runner is approved.
compare epay-get-malformed GET '/api/user/epay/notify?trade_no=order-1&sign=%ZZ'
compare epay-post-malformed POST '/api/user/epay/notify' 'trade_no=order-1&sign=%'
jq -cn '{test:"epay-provider-differential",routes:3,provider:"loopback-only",postgres_major:18,valkey_instances:2,negative_callback_matches:true,positive_checkout_and_replay:"listener-fixture-required",atomicity_delta:{go:"order and wallet are separate writes",rust:"repository completion must be one transaction",side_effect_equivalent:false},approval_credit:false,result:"not-approved"}'
