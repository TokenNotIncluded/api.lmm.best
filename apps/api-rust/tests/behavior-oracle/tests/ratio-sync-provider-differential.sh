#!/usr/bin/env bash
# Isolated differential plan for the root-only ratio synchronisation routes.
#
# The runner is inert unless LMM_RATIO_SYNC_RUN=1.  A real run owns every
# process, datastore, port and secret it touches; it accepts no production
# endpoint, provider credential or pre-existing listener.  Positive provider
# execution additionally requires listener executables that inject a
# loopback-only provider adapter.  The stock Rust test instance intentionally
# uses TestInstanceDisabledRatioSyncUpstream and is therefore insufficient for
# positive-provider approval by design.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
pg_port=${LMM_RATIO_SYNC_PG_PORT:-55487}
go_port=${LMM_RATIO_SYNC_GO_PORT:-13087}
rust_port=${LMM_RATIO_SYNC_RUST_PORT:-33087}
go_valkey_port=${LMM_RATIO_SYNC_GO_VALKEY_PORT:-16487}
rust_valkey_port=${LMM_RATIO_SYNC_RUST_VALKEY_PORT:-16488}
provider_port=${LMM_RATIO_SYNC_PROVIDER_PORT:-18087}
runtime_base=${LMM_RATIO_SYNC_RUNTIME_BASE:-/tmp}

plan() {
  jq -cn \
    --argjson routes '["GET /api/ratio_sync/channels","POST /api/ratio_sync/fetch"]' \
    '{test:"ratio-sync-provider-differential",mode:"plan-only",routes:$routes,isolated:{postgres_major:18,valkey_instances:2,provider_mock:"loopback-only",random_secret:true,owned_pids:true,production_access:false},checks:["root-auth-before-query-or-body","channels-presets-and-db-snapshot","malformed-body-after-auth","provider-json-type1-type2-openrouter-models-dev","difference-merge-confidence","eight-way-provider-concurrency","https-ssrf-dns-pin-redirect-response-limit","no-option-or-channel-writes"],positive_provider:"requires injected loopback adapter",approval_credit:false}'
}

if [[ ${LMM_RATIO_SYNC_RUN:-0} != 1 ]]; then
  plan
  exit 0
fi

for command in createdb curl git initdb jq pg_ctl postgres psql python3 valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
[[ -d $legacy_root ]] || { echo "missing frozen Go source: $legacy_root" >&2; exit 1; }
[[ -d $runtime_base && -w $runtime_base ]] || { echo "runtime base is not writable: $runtime_base" >&2; exit 1; }

go_listener=${LMM_RATIO_SYNC_GO_LISTENER:-}
rust_listener=${LMM_RATIO_SYNC_RUST_LISTENER:-}
root_token=${LMM_RATIO_SYNC_ROOT_TOKEN:-}
[[ -n $go_listener && -x $go_listener ]] || { echo "set executable LMM_RATIO_SYNC_GO_LISTENER" >&2; exit 2; }
[[ -n $rust_listener && -x $rust_listener ]] || { echo "set executable LMM_RATIO_SYNC_RUST_LISTENER" >&2; exit 2; }
[[ -n $root_token ]] || { echo "set synthetic LMM_RATIO_SYNC_ROOT_TOKEN accepted by both disposable listeners" >&2; exit 2; }

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

runtime=$(mktemp -d "$runtime_base/lmm-ratio-sync.XXXXXX")
valkey_password=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
go_database=lmm_ratio_sync_go
rust_database=lmm_ratio_sync_rust
go_role=lmm_test_ratio_sync_go
rust_role=lmm_test_ratio_sync_rust
go_schema=lmm_test_ratio_sync_go
rust_schema=lmm_test_ratio_sync_rust
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
    "$runtime_base"/lmm-ratio-sync.*) rm -rf "$runtime" ;;
    *) echo "refusing unexpected runtime path: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'echo "ratio-sync differential failed at line $LINENO" >&2' ERR

wait_for_valkey() {
  local pid=$1 port=$2
  for _ in {1..100}; do
    kill -0 "$pid" 2>/dev/null || return 1
    [[ $(valkey-cli --no-auth-warning -a "$valkey_password" -h 127.0.0.1 -p "$port" ping 2>/dev/null) == PONG ]] && return 0
    sleep 0.05
  done
  return 1
}
wait_for_http() {
  local pid=$1 url=$2
  for _ in {1..160}; do
    kill -0 "$pid" 2>/dev/null || return 1
    curl --fail --silent --show-error --connect-timeout 1 "$url" >/dev/null 2>&1 && return 0
    sleep 0.05
  done
  return 1
}
pid_start_time() { [[ -r /proc/$1/stat ]] || return 1; awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local pid_name=$1 pid=$2 start; printf -v "$pid_name" '%s' "$pid"; start=$(pid_start_time "$pid") || { echo "failed to record pid $pid" >&2; wait "$pid" 2>/dev/null || true; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; return 1; }; printf -v "${pid_name}_start" '%s' "$start"; }
owned_pid_is_live() { local pid_name=$1 pid start_name expected; pid=${!pid_name:-}; start_name="${pid_name}_start"; expected=${!start_name:-}; [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]; }
stop_owned_process() { local pid_name=$1 pid; pid=${!pid_name:-}; if [[ -n $pid ]]; then if owned_pid_is_live "$pid_name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi; fi; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; }

initdb -D "$runtime/pg" --no-locale --encoding=UTF8 >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" "$rust_database"
for target in "$go_database:$go_role:$go_schema" "$rust_database:$rust_role:$rust_schema"; do
  database=${target%%:*}
  rest=${target#*:}
  role=${rest%%:*}
  schema=${rest#*:}
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 \
    -c "CREATE ROLE $role LOGIN; CREATE SCHEMA $schema AUTHORIZATION $role;" >/dev/null
  sed "s/public\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/$schema.sql"
  if [[ $schema == "$rust_schema" ]]; then
    # Expand only the disposable Rust schema after the frozen baseline.
    sed "s/__LMM_APP_SCHEMA__/$schema/g" \
      "$repo_root/apps/api-rust/migrations/0018_subscription_amount_snapshots.sql" \
      >>"$runtime/$schema.sql"
    # Runtime readiness checks both SELECT and UPDATE privileges.
    printf 'GRANT SELECT, UPDATE ON %s.user_subscriptions TO %s;\n' \
      "$schema" "$rust_role" >>"$runtime/$schema.sql"
  fi
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 -f "$runtime/$schema.sql" >/dev/null
done

for target in "$go_database:$go_schema" "$rust_database:$rust_schema"; do
  database=${target%%:*}
  schema=${target#*:}
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
INSERT INTO $schema.options (key, value) VALUES
  ('ModelRatio', '{"fixture":1.0}'),
  ('CompletionRatio', '{"fixture":1.0}'),
  ('QuotaPerUnit', '750000');
INSERT INTO $schema.channels (id, name, base_url, key, status, type)
  VALUES (7, 'fixture-channel', 'https://provider.invalid', 'fixture-key', 1, 3);
SQL
done

valkey-server --bind 127.0.0.1 --port "$go_valkey_port" --protected-mode yes --save '' --appendonly no --requirepass "$valkey_password" >"$runtime/go-valkey.log" 2>&1 & record_pid go_valkey_pid "$!" || exit 1
valkey-server --bind 127.0.0.1 --port "$rust_valkey_port" --protected-mode yes --save '' --appendonly no --requirepass "$valkey_password" >"$runtime/rust-valkey.log" 2>&1 & record_pid rust_valkey_pid "$!" || exit 1
wait_for_valkey "$go_valkey_pid" "$go_valkey_port"
wait_for_valkey "$rust_valkey_pid" "$rust_valkey_port"

# The provider binds exclusively to loopback.  It intentionally exposes only
# deterministic ratio payloads and redirect/oversize probes; it has no
# credentials and never reaches the network.
PROVIDER_PORT="$provider_port" python3 -c '
import http.server, json, os, socketserver
BODY = json.dumps({"success": True, "data": [{"model_name":"fixture","quota_type":0,"model_ratio":2,"completion_ratio":3}]}).encode()
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/health": self.send_response(200); self.end_headers(); return
        if self.path == "/pricing":
            self.send_response(200); self.send_header("content-type", "application/json"); self.send_header("content-length", str(len(BODY))); self.end_headers(); self.wfile.write(BODY); return
        if self.path == "/redirect": self.send_response(302); self.send_header("location", "/pricing"); self.end_headers(); return
        if self.path == "/oversize": self.send_response(200); self.send_header("content-length", str((10 << 20) + 1)); self.end_headers(); return
        self.send_response(404); self.end_headers()
    def log_message(self, *_): pass
socketserver.TCPServer(("127.0.0.1", int(os.environ["PROVIDER_PORT"])), Handler).serve_forever()
' >"$runtime/provider.log" 2>&1 & record_pid provider_pid "$!" || exit 1
wait_for_http "$provider_pid" "http://127.0.0.1:$provider_port/health"

go_dsn="postgresql://$go_role@127.0.0.1:$pg_port/$go_database?options=-csearch_path%3D$go_schema"
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path%3D$rust_schema"
env DATABASE_URL="$go_dsn" VALKEY_URL="redis://:$valkey_password@127.0.0.1:$go_valkey_port" PORT="$go_port" \
  LMM_RATIO_SYNC_PROVIDER_LOOPBACK="http://127.0.0.1:$provider_port" LMM_RATIO_SYNC_TEST_SECRET="$valkey_password" "$go_listener" >"$runtime/go.log" 2>&1 &
record_pid go_pid "$!" || exit 1
env DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$valkey_password@127.0.0.1:$rust_valkey_port" PORT="$rust_port" \
  LMM_RATIO_SYNC_PROVIDER_LOOPBACK="http://127.0.0.1:$provider_port" LMM_RATIO_SYNC_TEST_SECRET="$valkey_password" "$rust_listener" >"$runtime/rust.log" 2>&1 &
record_pid rust_pid "$!" || exit 1
wait_for_http "$go_pid" "http://127.0.0.1:$go_port/livez"
wait_for_http "$rust_pid" "http://127.0.0.1:$rust_port/livez"

canonical_response() { jq -S . "$1"; }
request() {
  local name=$1 base=$2 method=$3 path=$4 body=${5:-}
  curl --silent --show-error --output "$runtime/$name.json" --write-out '%{http_code}' \
    -X "$method" -H "authorization: Bearer $root_token" -H 'content-type: application/json' \
    --data "$body" "$base$path"
}
compare() {
  local name=$1 method=$2 path=$3 body=${4:-}
  [[ $(request "go-$name" "http://127.0.0.1:$go_port" "$method" "$path" "$body") == 200 ]]
  [[ $(request "rust-$name" "http://127.0.0.1:$rust_port" "$method" "$path" "$body") == 200 ]]
  diff -u <(canonical_response "$runtime/go-$name.json") <(canonical_response "$runtime/rust-$name.json")
}
snapshot() {
  local database=$1 schema=$2 output=$3
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -qAt -c \
    "SELECT json_build_object('channels', COALESCE(json_agg(to_jsonb(c) ORDER BY id), '[]'::json)) FROM $schema.channels c" | jq -S . >"$output"
}

snapshot "$go_database" "$go_schema" "$runtime/go.before"
snapshot "$rust_database" "$rust_schema" "$runtime/rust.before"
compare channels GET /api/ratio_sync/channels
# The listener executables named above must inject a test-only adapter that
# consumes this loopback URL.  The production HttpRatioSyncUpstream rejects it
# before dialing; that protection is separately exercised by Rust unit tests.
compare fetch-type2 POST /api/ratio_sync/fetch "$(jq -cn --arg base_url "http://127.0.0.1:$provider_port" '{upstreams:[{id:7,name:"fixture-channel",base_url:$base_url,endpoint:"/pricing"}],timeout:10}')"
snapshot "$go_database" "$go_schema" "$runtime/go.after"
snapshot "$rust_database" "$rust_schema" "$runtime/rust.after"
diff -u "$runtime/go.before" "$runtime/go.after"
diff -u "$runtime/rust.before" "$runtime/rust.after"

# This must remain an auth error rather than a JSON parser error because the
# middleware group protects the route before body extraction.
for base in "http://127.0.0.1:$go_port" "http://127.0.0.1:$rust_port"; do
  [[ $(curl --silent --output /dev/null --write-out '%{http_code}' -X POST -H 'content-type: application/json' --data '{bad' "$base/api/ratio_sync/fetch") == 401 ]]
done

jq -cn '{test:"ratio-sync-provider-differential",routes:2,postgres_major:18,valkey_instances:2,provider:"loopback-only",checks:["root-auth-before-body","channels","provider-type2","read-only-snapshots"],positive_provider:"injected-listener-required",approval_credit:false,result:"passed-without-production-approval"}'
