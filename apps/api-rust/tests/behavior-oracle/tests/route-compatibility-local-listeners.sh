#!/usr/bin/env bash
# Start only disposable local dependencies, then replay the safe (non-mutating)
# half of route-compatibility-matrix.tsv.  No listener or datastore outside the
# mktemp directory is read, written, or stopped.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
pg_port=${LMM_ROUTE_COMPATIBILITY_PG_PORT:-55477}
rust_port=${LMM_ROUTE_COMPATIBILITY_RUST_PORT:-33047}
oracle_port=${LMM_ROUTE_COMPATIBILITY_GO_PORT:-13017}
requested_valkey_port=${LMM_ROUTE_COMPATIBILITY_VALKEY_PORT:-6380}
requested_oracle_valkey_port=${LMM_ROUTE_COMPATIBILITY_GO_VALKEY_PORT:-16397}
runtime_base=${LMM_ROUTE_COMPATIBILITY_RUNTIME_BASE:-/home/lightjunction/.cache}
include_classes=${ROUTE_COMPATIBILITY_INCLUDE_CLASSES:-no-side-effect,external-gateway}
ready_attempts=${LMM_ROUTE_COMPATIBILITY_READY_ATTEMPTS:-1200}
result_dir=${ROUTE_COMPATIBILITY_RESULT_DIR:-}
go_pid=
go_valkey_pid=
rust_pid=
valkey_pid=
go_pid_start=
go_valkey_pid_start=
rust_pid_start=
valkey_pid_start=
[[ -d $runtime_base ]] || { echo "runtime base does not exist: $runtime_base" >&2; exit 1; }
[[ $ready_attempts =~ ^[1-9][0-9]*$ ]] || {
  echo "LMM_ROUTE_COMPATIBILITY_READY_ATTEMPTS must be a positive integer" >&2
  exit 2
}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo "ROUTE_COMPATIBILITY_RESULT_DIR must be an absolute path without '..'" >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
runtime=$(mktemp -d "$runtime_base/lmm-route-compatibility-listeners.XXXXXX")
cargo_target="$runtime/cargo-target"
rust_binary=${LMM_ROUTE_COMPATIBILITY_RUST_BINARY:-"$repo_root/apps/api-rust/target/debug/lmm-api-rs"}
schema=lmm_test_route_compatibility
role=lmm_test_route_compatibility
database=lmm_test_route_compatibility

cleanup() {
  for name in go_pid go_valkey_pid rust_pid valkey_pid; do
    stop_owned_process "$name" || true
  done
  [[ -d $runtime/pg ]] && pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    "$runtime_base"/lmm-route-compatibility-listeners.*) rm -rf "$runtime" ;;
    *) echo "refusing to remove unexpected runtime: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM

for command in cargo createdb createuser curl go initdb jq pg_ctl postgres psql valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }

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

valkey_port=$(select_unused_port "$requested_valkey_port") || { echo "unable to allocate local valkey port" >&2; exit 1; }
oracle_valkey_port=$(select_unused_port "$requested_oracle_valkey_port") || { echo "unable to allocate local go oracle valkey port" >&2; exit 1; }
[[ $valkey_port == "$requested_valkey_port" ]] || echo "using fallback rust valkey port: $valkey_port" >&2
[[ $oracle_valkey_port == "$requested_oracle_valkey_port" ]] || echo "using fallback go oracle valkey port: $oracle_valkey_port" >&2

for port in "$pg_port" "$rust_port" "$oracle_port" "$valkey_port" "$oracle_valkey_port"; do
  if ss -ltn "sport = :$port" | grep -q LISTEN; then
    echo "refusing to reuse occupied local port: $port" >&2
    exit 1
  fi
done

if [[ ${LMM_ROUTE_COMPATIBILITY_SKIP_RUST_BUILD:-0} == 1 ]]; then
  [[ -x $rust_binary ]] || { echo "Rust listener binary unavailable: $rust_binary" >&2; exit 1; }
else
  TMPDIR="$runtime" CARGO_TARGET_DIR="$cargo_target" \
    cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
  rust_binary="$cargo_target/debug/lmm-api-rs"
fi

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createuser -h 127.0.0.1 -p "$pg_port" "$role"
createdb -h 127.0.0.1 -p "$pg_port" -O "$role" "$database"

# The baseline is installed into a fresh schema owned by the disposable role.
# This makes every SQL reference resolve through the exact test namespace that
# Config validates when LMM_RS_TEST_INSTANCE=1.
sed "s/public\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" > "$runtime/baseline.sql"
sed "s/__LMM_APP_SCHEMA__/$schema/g" "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" > "$runtime/bounty-forward.sql"
psql -h 127.0.0.1 -p "$pg_port" -U "$role" -d "$database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE SCHEMA $schema AUTHORIZATION $role;
SET search_path TO $schema;
\i $runtime/baseline.sql
\i $runtime/bounty-forward.sql
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
SQL

valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no \
  --dir "$runtime" --logfile "$runtime/valkey.log" > /dev/null 2>&1 &
record_pid valkey_pid "$!"
for _ in {1..100}; do valkey-cli -h 127.0.0.1 -p "$valkey_port" ping >/dev/null 2>&1 && break; sleep .05; done
valkey-cli -h 127.0.0.1 -p "$valkey_port" ping >/dev/null

# The frozen Go oracle is built from a copy under the same home-backed
# runtime.  The root filesystem may intentionally be too small for PostgreSQL
# or a Go build; using /tmp here would make that host constraint look like a
# route failure.
cp -a "$legacy_root/." "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: > "$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .
)
valkey-server --bind 127.0.0.1 --port "$oracle_valkey_port" --save '' --appendonly no \
  --dir "$runtime" --logfile "$runtime/go-valkey.log" > /dev/null 2>&1 &
record_pid go_valkey_pid "$!"
for _ in {1..100}; do valkey-cli -h 127.0.0.1 -p "$oracle_valkey_port" ping >/dev/null 2>&1 && break; sleep .05; done
valkey-cli -h 127.0.0.1 -p "$oracle_valkey_port" ping >/dev/null

# Redis is optional in the frozen Go oracle.  The default keeps the two
# listeners on separate Valkey instances; disabling it is useful for a
# transport-only replay when the host cannot sustain the second cache process.
go_redis_conn="redis://127.0.0.1:$oracle_valkey_port"
if [[ ${LMM_ROUTE_COMPATIBILITY_GO_REDIS_ENABLED:-1} != 1 ]]; then
  go_redis_conn=
fi
env \
  SQLITE_PATH="$runtime/oracle.db" \
  PORT="$oracle_port" \
  REDIS_CONN_STRING="$go_redis_conn" \
  SESSION_SECRET='MissingRoutes-2026!SyntheticOnly' \
  GLOBAL_API_RATE_LIMIT_ENABLE=false \
  TRUSTED_PROXIES=none \
  GIN_MODE=release \
  "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
record_pid go_pid "$!"
for ((attempt = 0; attempt < ready_attempts; attempt++)); do
  [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$oracle_port/api/status" || true) == 200 ]] && break
  sleep .05
done
if [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$oracle_port/api/status" || true) != 200 ]]; then
  echo "Go oracle did not become ready; log follows" >&2
  sed -n '1,240p' "$runtime/go.log" >&2
  exit 1
fi

database_url="postgresql://$role@127.0.0.1:$pg_port/$database?options=-csearch_path=$schema"
# Test instances accept only the single-slot identity; blue/green slot names
# are intentionally rejected outside the normal deployment policy.
env \
  LMM_RS_TEST_INSTANCE=1 \
  LMM_RS_SLOT=single \
  LMM_RS_TEST_VALKEY_PORT="$valkey_port" \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" \
  DATABASE_URL="$database_url" \
  VALKEY_URL="redis://127.0.0.1:$valkey_port" \
  LMM_SCHEMA_CONTRACT=1 \
  SESSION_SECRET='MissingRoutes-2026!SyntheticOnly' \
  CRYPTO_SECRET='MissingRoutesCrypto-2026!SyntheticOnly' \
  GLOBAL_API_RATE_LIMIT_ENABLE=false \
  PASSWORD_LOGIN_ENABLED=false \
  TRUSTED_PROXIES=none \
  VERSION=v0.0.0 \
  "$rust_binary" >"$runtime/rust.log" 2>&1 &
record_pid rust_pid "$!"
for ((attempt = 0; attempt < ready_attempts; attempt++)); do
  [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$rust_port/readyz" || true) == 200 ]] && break
  sleep .05
done
if [[ $(curl --silent --output /dev/null --write-out '%{http_code}' "http://127.0.0.1:$rust_port/readyz" || true) != 200 ]]; then
  echo "Rust test-instance did not become ready; log follows" >&2
  sed -n '1,240p' "$runtime/rust.log" >&2
  exit 1
fi
GO_BASE_URL="http://127.0.0.1:$oracle_port" RUST_BASE_URL="http://127.0.0.1:$rust_port" \
  ROUTE_COMPATIBILITY_MODE=transport \
  ROUTE_COMPATIBILITY_INCLUDE_CLASSES="$include_classes" \
  ROUTE_COMPATIBILITY_RESULT_DIR="$result_dir" \
  "$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-listener-differential.sh"
