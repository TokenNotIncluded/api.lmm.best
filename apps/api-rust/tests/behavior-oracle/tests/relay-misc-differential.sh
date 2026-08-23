#!/usr/bin/env bash
# Fail-closed TCP probe for the frozen Go relay-misc surface and the Rust
# candidate seam. Everything runs on loopback with disposable PostgreSQL 18,
# Valkey, listeners, and provider fixture processes. It deliberately exits
# non-zero while the candidate owns no TokenAuth/distribution/accounting
# executor: a 503 is evidence of a safe boundary, not parity approval.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || {
  echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2
  exit 2
}
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || {
  echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2
  exit 2
}
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root" | "$repo_root"/*)
  echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2
  exit 2
  ;;
esac
runtime_base=${LMM_RELAY_MISC_RUNTIME_BASE:-/tmp}
pg_port=${LMM_RELAY_MISC_PG_PORT:-55483}
go_port=${LMM_RELAY_MISC_GO_PORT:-13053}
rust_port=${LMM_RELAY_MISC_RUST_PORT:-33083}
rust_valkey_requested_port=${LMM_RELAY_MISC_RUST_VALKEY_PORT:-6380}
provider_port=${LMM_RELAY_MISC_PROVIDER_PORT:-38083}
go_valkey_requested_port=${LMM_RELAY_MISC_GO_VALKEY_PORT:-16433}
# shellcheck disable=SC2034 # Read indirectly through the PID variable names.
go_pid='' rust_pid='' go_valkey_pid='' rust_valkey_pid='' provider_pid='' \
  go_pid_start='' rust_pid_start='' go_valkey_pid_start='' rust_valkey_pid_start='' provider_pid_start=''
runtime=$(mktemp -d "$runtime_base/lmm-relay-misc-differential.XXXXXX")
rust_binary=${LMM_RELAY_MISC_RUST_BINARY:-"$runtime/cargo-target/debug/lmm-api-rs"}
rust_cargo_target=${LMM_RELAY_MISC_CARGO_TARGET_DIR:-"$runtime/cargo-target"}
rust_schema=lmm_test_relay_misc
rust_role=lmm_test_relay_misc
rust_database=lmm_test_relay_misc
# Hex-only material has just two character classes and is rejected by the
# Rust session-secret validator. Base64 supplies mixed classes while remaining
# synthetic, task-owned, and absent from diagnostics.
# Redis URLs embed these values in userinfo; keep the random material URL-safe
# while adding upper/lowercase classes required by the Rust validator.
go_valkey_password="A$(openssl rand -hex 32)z"
rust_valkey_password="A$(openssl rand -hex 32)z"
go_session_secret=$(openssl rand -base64 48 | tr -d '\n')
go_crypto_secret=$(openssl rand -base64 48 | tr -d '\n')
rust_session_secret=$(openssl rand -base64 48 | tr -d '\n')
rust_crypto_secret=$(openssl rand -base64 48 | tr -d '\n')
# The isolated candidate surface intentionally accepts one non-secret fixture
# credential. Keep this in sync with `enforce_test_instance_relay_misc_auth`;
# it is not a production token and never leaves loopback.
fixture_bearer='lmm-test-relay-fixture'

# shellcheck disable=SC2329 # invoked through the process-exit trap below
cleanup() {
  for pid_name in go_pid rust_pid go_valkey_pid rust_valkey_pid provider_pid; do
    stop_owned_process "$pid_name" || true
  done
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  if [[ ${LMM_RELAY_MISC_KEEP_RUNTIME:-0} == 1 ]]; then
    echo "keeping relay-misc differential runtime: $runtime" >&2
    return
  fi
  case "$runtime" in
  "$runtime_base"/lmm-relay-misc-differential.*) rm -rf "$runtime" ;;
  *) echo "refusing unexpected runtime removal: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'echo "relay-misc differential failed at line $LINENO" >&2' ERR

for command in cargo createdb createuser curl git go initdb jq pg_ctl postgres psql python3 valkey-cli valkey-server; do
  command -v "$command" >/dev/null || {
    echo "required command unavailable: $command" >&2
    exit 127
  }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || {
  echo "requires PostgreSQL 18" >&2
  exit 1
}
[[ -d $legacy_root ]] || {
  echo "missing frozen Go source: $legacy_root" >&2
  exit 1
}
[[ -d $runtime_base && -w $runtime_base ]] || {
  echo "runtime base is not writable: $runtime_base" >&2
  exit 1
}

preflight_port() {
  local name=$1 port=$2
  # The descriptor lives in this subshell and is closed automatically.  Do
  # not put an explicit close after the probe: `exec 3>&-` would return zero
  # even when the connection attempt itself failed, making every port appear
  # occupied.
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    echo "refusing occupied $name port: 127.0.0.1:$port" >&2
    exit 1
  fi
}
pid_start_time() {
  [[ -r /proc/$1/stat ]] || return 1
  awk '{print $22}' "/proc/$1/stat"
}
record_pid() {
  local pid_name=$1 pid=$2 start
  printf -v "$pid_name" '%s' "$pid"
  start=$(pid_start_time "$pid") || {
    echo "failed to record pid $pid" >&2
    wait "$pid" 2>/dev/null || true
    printf -v "$pid_name" ''
    printf -v "${pid_name}_start" ''
    return 1
  }
  printf -v "${pid_name}_start" '%s' "$start"
}
# shellcheck disable=SC2329 # Called through the cleanup PID-name dispatch.
owned_pid_is_live() {
  local pid_name=$1 pid start_name expected
  pid=${!pid_name:-}
  start_name="${pid_name}_start"
  expected=${!start_name:-}
  [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]
}
# shellcheck disable=SC2329 # Called through the cleanup PID-name dispatch.
stop_owned_process() {
  local pid_name=$1 pid
  pid=${!pid_name:-}
  if [[ -n $pid ]]; then if owned_pid_is_live "$pid_name"; then
    kill "$pid" 2>/dev/null || true
    wait "$pid" 2>/dev/null || true
  else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi; fi
  printf -v "$pid_name" ''
  printf -v "${pid_name}_start" ''
}
port_free() { [[ -z $(ss -H -ltn "sport = :$1" 2>/dev/null) ]]; }
random_free_port() {
  local p
  while :; do
    p=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000))
    [[ -z $(ss -H -ltn "sport = :$p" 2>/dev/null) ]] && {
      echo "$p"
      return
    }
  done
}
select_unused_port() {
  local requested=$1 label=$2 candidate
  if port_free "$requested"; then
    echo "$requested"
    return 0
  fi
  echo "requested $label port $requested is occupied, selecting a free port" >&2
  for _ in {1..200}; do
    candidate=$(random_free_port)
    if port_free "$candidate"; then
      echo "$candidate"
      return 0
    fi
  done
  return 1
}
for entry in \
  "postgres:$pg_port" "go:$go_port" "rust:$rust_port" "provider:$provider_port"; do
  preflight_port "${entry%%:*}" "${entry##*:}"
done
go_valkey_port=$(select_unused_port "$go_valkey_requested_port" go-valkey) || {
  echo "unable to allocate an unused go valkey port" >&2
  exit 1
}
[[ $go_valkey_port == "$go_valkey_requested_port" ]] || echo "using fallback go valkey port: $go_valkey_port" >&2
rust_valkey_port=$(select_unused_port "$rust_valkey_requested_port" rust-valkey) || {
  echo "unable to allocate an unused rust valkey port" >&2
  exit 1
}
[[ $rust_valkey_port == "$rust_valkey_requested_port" ]] || echo "using fallback rust valkey port: $rust_valkey_port" >&2

wait_for_pid_http() {
  local pid=$1 port=$2 path=$3
  for _ in {1..300}; do
    kill -0 "$pid" 2>/dev/null || return 1
    [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$port$path" || true) =~ ^(200|204)$ ]] && return 0
    sleep .05
  done
  return 1
}

start_valkey() {
  local name=$1 port=$2 password=$3 pid_var=$4
  local config="$runtime/$name-valkey.conf"
  umask 077
  printf '%s\n' \
    'bind 127.0.0.1' \
    "port $port" \
    'save ""' \
    'appendonly no' \
    'protected-mode yes' \
    "requirepass $password" \
    "dir $runtime" \
    "logfile $runtime/$name-valkey.log" >"$config"
  chmod 600 "$config"
  env -i PATH="$PATH" valkey-server "$config" &
  local pid=$!
  record_pid "$pid_var" "$pid" || return 1
  for _ in {1..200}; do
    kill -0 "$pid" 2>/dev/null || return 1
    VALKEYCLI_AUTH="$password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" ping >/dev/null 2>&1 && return 0
    sleep .05
  done
  return 1
}

start_loopback_provider() {
  : >"$runtime/provider-hits.log"
  python3 -u - "$provider_port" "$runtime/provider-hits.log" <<'PY' &
import http.server
import sys

port = int(sys.argv[1])
hits = sys.argv[2]

class Fixture(http.server.BaseHTTPRequestHandler):
    def log_message(self, *_args):
        pass

    def do_GET(self):
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"fixture":"healthy"}')

    def do_POST(self):
        length = int(self.headers.get("content-length", "0"))
        body = self.rfile.read(length)
        with open(hits, "a", encoding="utf-8") as output:
            output.write(
                f"{self.path}\t{self.headers.get('authorization', '')}\t"
                f"{self.headers.get('accept-encoding', '')}\t{len(body)}\n"
            )
        if self.headers.get("authorization") != "Bearer provider-owned-secret":
            self.send_response(400)
            self.send_header("content-type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":"credential-boundary"}')
            return
        if self.headers.get("x-fixture-mode") == "sse":
            self.send_response(200)
            self.send_header("content-type", "text/event-stream")
            self.send_header("cache-control", "no-cache")
            self.end_headers()
            self.wfile.write(b'data: {"fixture":true}\n\ndata: [DONE]\n\n')
            self.wfile.flush()
            return
        if self.headers.get("x-fixture-mode") == "error":
            self.send_response(429)
            self.send_header("content-type", "application/json")
            self.send_header("retry-after", "7")
            self.end_headers()
            self.wfile.write(b'{"error":"fixture-rate-limit"}')
            return
        self.send_response(200)
        self.send_header("content-type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"fixture":"loopback"}')

http.server.ThreadingHTTPServer(("127.0.0.1", port), Fixture).serve_forever()
PY
  record_pid provider_pid "$!" || return 1
  wait_for_pid_http "$provider_pid" "$provider_port" /health || {
    sed -n '1,160p' "$runtime/provider-hits.log" 2>/dev/null || true
    return 1
  }
}

start_loopback_provider
start_valkey go "$go_valkey_port" "$go_valkey_password" go_valkey_pid
start_valkey rust "$rust_valkey_port" "$rust_valkey_password" rust_valkey_pid

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createuser -h 127.0.0.1 -p "$pg_port" "$rust_role"
createdb -h 127.0.0.1 -p "$pg_port" -O "$rust_role" "$rust_database"
sed "s/public\\./$rust_schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/baseline.sql"
psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$rust_database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE SCHEMA $rust_schema AUTHORIZATION $rust_role;
SET search_path TO $rust_schema;
\i $runtime/baseline.sql
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
SQL
# The baseline predates the four bounty tables that are part of the current
# readiness contract. Keep empty, role-owned fixtures so readiness failures
# cannot mask the relay route result.
psql -h 127.0.0.1 -p "$pg_port" -U "$rust_role" -d "$rust_database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
SET search_path TO lmm_test_relay_misc;
CREATE TABLE open_source_bounty_projects (
  id BIGINT, owner_user_id BIGINT, repository_url TEXT, title TEXT,
  description TEXT, rules TEXT, reward_quota BIGINT, net_reward_quota BIGINT,
  reward_slots BIGINT, escrow_quota BIGINT, platform_fee_rate_bps BIGINT,
  platform_fee_quota BIGINT, status TEXT, created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ, published_at TIMESTAMPTZ, closed_at TIMESTAMPTZ
);
CREATE TABLE open_source_bounty_challenges (
  id BIGINT, project_id BIGINT, participant_user_id BIGINT,
  github_handle TEXT, status TEXT, issue_url TEXT, pull_request_url TEXT,
  submission_note TEXT, review_note TEXT, reward_quota BIGINT, tip_quota BIGINT,
  owner_rating_score BIGINT, owner_rating_comment TEXT, owner_rated_at TIMESTAMPTZ,
  contributor_rating_score BIGINT, contributor_rating_comment TEXT,
  contributor_rated_at TIMESTAMPTZ, owner_rating_overturned BOOLEAN,
  accepted_at TIMESTAMPTZ, submitted_at TIMESTAMPTZ, reviewed_at TIMESTAMPTZ,
  rejected_at TIMESTAMPTZ, paid_at TIMESTAMPTZ, created_at TIMESTAMPTZ,
  updated_at TIMESTAMPTZ
);
CREATE TABLE open_source_bounty_ledgers (
  id BIGINT, project_id BIGINT, challenge_id BIGINT, user_id BIGINT,
  counterparty_user_id BIGINT, kind TEXT, quota BIGINT, note TEXT,
  recipient_read_at TIMESTAMPTZ, thanked_at TIMESTAMPTZ, created_at TIMESTAMPTZ
);
CREATE TABLE open_source_bounty_disputes (
  id BIGINT, challenge_id BIGINT, project_id BIGINT, opened_by_user_id BIGINT,
  against_user_id BIGINT, reason TEXT, statement TEXT,
  project_title_snapshot TEXT, repository_url_snapshot TEXT,
  project_rules_snapshot TEXT, project_escrow_quota_snapshot BIGINT,
  challenge_status_snapshot TEXT, issue_url_snapshot TEXT,
  pull_request_url_snapshot TEXT, submission_note_snapshot TEXT,
  review_note_snapshot TEXT, reward_quota_snapshot BIGINT,
  tip_quota_snapshot BIGINT, owner_rating_score_snapshot BIGINT,
  owner_rating_comment_snapshot TEXT, contributor_rating_score_snapshot BIGINT,
  contributor_rating_comment_snapshot TEXT, status TEXT, resolution TEXT,
  resolved_by_user_id BIGINT, created_at TIMESTAMPTZ, updated_at TIMESTAMPTZ,
  resolved_at TIMESTAMPTZ
);
SQL

mkdir -p "$runtime/go-source/web/dist"
cp -a "$legacy_root/." "$runtime/go-source/"
: >"$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  env -i PATH="$PATH" HOME="$HOME" GOTOOLCHAIN=local CGO_ENABLED=1 \
    go build -buildvcs=false -o "$runtime/legacy-go" .
)
if [[ ${LMM_RELAY_MISC_SKIP_RUST_BUILD:-0} != 1 ]]; then
  env -i PATH="$PATH" HOME="$HOME" CARGO_HOME="${CARGO_HOME:-$HOME/.cargo}" \
    RUSTUP_HOME="${RUSTUP_HOME:-$HOME/.rustup}" CARGO_TARGET_DIR="$rust_cargo_target" \
    cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
fi

# This ignored module test is the only candidate with an injected distributor,
# provider credential, and accounting seam. It must actually reach the
# loopback fixture; a listener-only 503 check is not called a differential.
if [[ ${LMM_RELAY_MISC_SKIP_RUST_BUILD:-0} != 1 ]]; then
  env -i PATH="$PATH" HOME="$HOME" CARGO_HOME="${CARGO_HOME:-$HOME/.cargo}" \
    RUSTUP_HOME="${RUSTUP_HOME:-$HOME/.rustup}" CARGO_TARGET_DIR="$rust_cargo_target" \
    LMM_RELAY_MISC_PROVIDER_URL="http://127.0.0.1:$provider_port" \
    cargo test --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --lib --locked \
    migration_routes::relay_misc::tests::loopback_provider_contract -- --ignored --exact
fi

env -i PATH="$PATH" HOME="$HOME" SQL_DSN=local SQLITE_PATH="$runtime/go.db" PORT="$go_port" \
  REDIS_CONN_STRING="redis://:$go_valkey_password@127.0.0.1:$go_valkey_port" \
  SESSION_SECRET="$go_session_secret" CRYPTO_SECRET="$go_crypto_secret" \
  GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
  MODEL_REQUEST_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release \
  "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
record_pid go_pid "$!" || exit 1
wait_for_pid_http "$go_pid" "$go_port" /api/status || {
  sed -n '1,240p' "$runtime/go.log" >&2
  exit 1
}

rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path%3D$rust_schema"
env -i PATH="$PATH" HOME="$HOME" LMM_RS_TEST_INSTANCE=1 LMM_RS_SLOT=single \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" DATABASE_URL="$rust_dsn" \
  VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port" LMM_SCHEMA_CONTRACT=1 \
  LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" \
  SESSION_SECRET="$rust_session_secret" CRYPTO_SECRET="$rust_crypto_secret" \
  GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
  PASSWORD_LOGIN_ENABLED=false TRUSTED_PROXIES=none VERSION=v0.0.0 \
  "$rust_binary" >"$runtime/rust.log" 2>&1 &
record_pid rust_pid "$!" || exit 1
wait_for_pid_http "$rust_pid" "$rust_port" /readyz || {
  sed -n '1,240p' "$runtime/rust.log" >&2
  exit 1
}

call() {
  local engine=$1 name=$2 method=$3 path=$4 bearer=$5 body=${6:-}
  local port=$go_port
  [[ $engine == rust ]] && port=$rust_port
  local prefix="$runtime/$engine-$name"
  local args=(--silent --show-error --dump-header "$prefix.headers" --output "$prefix.body" --write-out '%{http_code}' --request "$method")
  [[ -z $bearer ]] || args+=(--header "authorization: Bearer $bearer")
  [[ -z $body ]] || args+=(--header 'content-type: application/json' --data-binary "$body")
  curl "${args[@]}" "http://127.0.0.1:$port$path" >"$prefix.status"
}

assert_status() {
  local engine=$1 name=$2 expected=$3 actual
  actual=$(<"$runtime/$engine-$name.status")
  [[ $actual == "$expected" ]] || {
    echo "$engine $name expected $expected, got $actual" >&2
    return 1
  }
}

fixture_hits_before=$(wc -l <"$runtime/provider-hits.log")
provider_authorization='authorization: Bearer provider-owned-secret' # gitleaks:allow -- synthetic provider token
curl --silent --show-error --no-buffer --request POST \
  --header "$provider_authorization" \
  --header 'x-fixture-mode: sse' \
  "http://127.0.0.1:$provider_port/sse" >"$runtime/provider.sse"
grep -Fx 'data: {"fixture":true}' "$runtime/provider.sse" >/dev/null
grep -Fx 'data: [DONE]' "$runtime/provider.sse" >/dev/null

routes=(
  'POST|/v1/alpha/search|{"model":"gpt-test","query":"hello"}'
  'POST|/v1/embeddings|{"model":"text-embedding-3-small","input":"hello"}'
  'POST|/v1/engines/text-embedding-004/embeddings|{"model":"text-embedding-004","input":"hello"}'
  'POST|/v1/rerank|{"model":"rerank-v3","query":"hello","documents":["hello"]}'
  'POST|/v1/moderations|{"model":"text-moderation-stable","input":"hello"}'
  'POST|/v1/images/variations|{}'
  'GET|/v1/files|'
  'POST|/v1/files|{}'
  'DELETE|/v1/files/file-1|'
  'GET|/v1/files/file-1|'
  'GET|/v1/files/file-1/content|'
  'GET|/v1/fine-tunes|'
  'POST|/v1/fine-tunes|{}'
  'GET|/v1/fine-tunes/ft-1|'
  'POST|/v1/fine-tunes/ft-1/cancel|{}'
  'GET|/v1/fine-tunes/ft-1/events|'
)

for route in "${routes[@]}"; do
  IFS='|' read -r method path body <<<"$route"
  name=$(printf '%s-%s' "$method" "$path" | tr '/:{}' '-----')
  call go "$name-anonymous" "$method" "$path" '' "$body"
  call rust "$name-anonymous" "$method" "$path" '' "$body"
  assert_status go "$name-anonymous" 401
  assert_status rust "$name-anonymous" 401
done

for route in "${routes[@]:0:5}"; do
  IFS='|' read -r method path body <<<"$route"
  name=$(printf '%s-%s' "$method" "$path" | tr '/:{}' '-----')
  call rust "$name-fixture" "$method" "$path" "$fixture_bearer" "$body"
  assert_status rust "$name-fixture" 503
done

for route in "${routes[@]:5}"; do
  IFS='|' read -r method path body <<<"$route"
  name=$(printf '%s-%s' "$method" "$path" | tr '/:{}' '-----')
  call rust "$name-fixture" "$method" "$path" "$fixture_bearer" "$body"
  assert_status rust "$name-fixture" 501
  jq -e '.error.message == "API not implemented" and .error.type == "new_api_error" and .error.code == "api_not_implemented"' \
    <"$runtime/rust-$name-fixture.body" >/dev/null
done

fixture_hits_after=$(wc -l <"$runtime/provider-hits.log")
((fixture_hits_after == fixture_hits_before + 1)) || {
  echo "candidate contacted the provider fixture despite fail-closed relay state" >&2
  exit 1
}

jq -cn \
  --arg result blocked \
  --arg reason 'Rust relay-misc has no production TokenAuth/distribution/provider/accounting executor' \
  --arg provider "http://127.0.0.1:$provider_port" \
  --argjson frozen_501_routes 11 \
  --argjson real_relay_routes 5 \
  '{test:"relay-misc-differential",result:$result,reason:$reason,provider_fixture:$provider,provider_loopback_only:true,frozen_501_routes:$frozen_501_routes,real_relay_routes:$real_relay_routes,unauthenticated_order_checked:true,candidate_real_routes_fail_closed_503:true,candidate_frozen_routes_501_checked:true}'
echo 'relay-misc remains unapproved; a production executor and an independent Go/Rust valid-token/channel/accounting differential are required.' >&2
exit 1
