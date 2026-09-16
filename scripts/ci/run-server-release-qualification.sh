#!/usr/bin/env bash
set -euo pipefail
set +x

backend=${1:?usage: run-server-release-qualification.sh go|rust}
case "$backend" in go|rust) ;; *) echo "unsupported backend: $backend" >&2; exit 2 ;; esac

repo_root=$(git rev-parse --show-toplevel)
runtime_base=${RUNNER_TEMP:-/tmp}
runtime=$(mktemp -d "$runtime_base/lmm-server-qualification-$backend.XXXXXX")
evidence="$repo_root/qualification-artifacts/$backend"
mkdir -p "$evidence"
server_pid=

cleanup() {
  local exit_code=$?
  if [[ -n ${server_pid:-} ]] && kill -0 "$server_pid" 2>/dev/null; then
    kill -TERM "$server_pid" 2>/dev/null || true
    for _ in {1..40}; do
      kill -0 "$server_pid" 2>/dev/null || break
      sleep .1
    done
    kill -KILL "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  cp -f "$runtime"/*.log "$evidence/" 2>/dev/null || true
  cp -f "$runtime"/*.json "$evidence/" 2>/dev/null || true
  cp -f "$runtime"/*.txt "$evidence/" 2>/dev/null || true
  case "$runtime" in "$runtime_base"/lmm-server-qualification-*) rm -rf "$runtime" ;; esac
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

for command in curl jq psql sed; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done

pg_host=${LMM_QUALIFICATION_PG_HOST:-127.0.0.1}
pg_port=${LMM_QUALIFICATION_PG_PORT:-5432}
pg_user=${LMM_QUALIFICATION_PG_USER:-lmm_test_release}
pg_password=${LMM_QUALIFICATION_PG_PASSWORD:-lmm-release-postgres-password}
pg_database=${LMM_QUALIFICATION_PG_DATABASE:-lmm_test_release}
valkey_port=${LMM_QUALIFICATION_VALKEY_PORT:-6379}
valkey_password=${LMM_QUALIFICATION_VALKEY_PASSWORD:-lmm-release-valkey-password}
server_port=${LMM_QUALIFICATION_PORT:-33017}
server_base="http://127.0.0.1:$server_port"
session_secret='release-qualification-session-2026-Z7p2q9m4x8k1v6c3'
crypto_secret='release-qualification-crypto-2026-H4n8s2b7r5w9y3d6'
export PGPASSWORD="$pg_password"
export LMM_QUALIFICATION_BACKEND="$backend"
export LMM_QUALIFICATION_BASE_URL="$server_base"
export LMM_QUALIFICATION_WORK_DIR="$runtime"
export LMM_QUALIFICATION_ROOT_USERNAME=releaseci
export LMM_QUALIFICATION_ROOT_PASSWORD='ReleaseCI-2026-Local-Only'

database_url="postgresql://$pg_user:$pg_password@$pg_host:$pg_port/$pg_database"
valkey_url="redis://:$valkey_password@127.0.0.1:$valkey_port/0"

if [[ $backend == rust ]]; then
  schema=lmm_test_release
  baseline="$runtime/postgresql-baseline.sql"
  bounty="$runtime/open-source-bounty-forward.sql"
  sed "s/public\\./$schema./g" \
    "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$baseline"
  sed "s/__LMM_APP_SCHEMA__/$schema/g" \
    "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" >"$bounty"

  psql -h "$pg_host" -p "$pg_port" -U "$pg_user" -d "$pg_database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
DROP SCHEMA IF EXISTS $schema CASCADE;
CREATE SCHEMA $schema AUTHORIZATION $pg_user;
SET search_path TO $schema;
\i $baseline
\i $bounty
CREATE TABLE lmm_schema_contract (
  singleton BOOLEAN PRIMARY KEY,
  min_reader_version BIGINT NOT NULL,
  max_reader_version BIGINT NOT NULL
);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
SQL
  rust_database_url="$database_url?options=-csearch_path%3D$schema"
fi

start_backend() {
  local log=$1
  [[ -z ${server_pid:-} ]] || { echo 'server is already running' >&2; return 1; }
  case "$backend" in
    go)
      binary=${LMM_QUALIFICATION_GO_BINARY:?LMM_QUALIFICATION_GO_BINARY is required}
      env \
        SQL_DSN="$database_url?sslmode=disable" \
        REDIS_CONN_STRING="$valkey_url" \
        SESSION_SECRET="$session_secret" \
        CRYPTO_SECRET="$crypto_secret" \
        LMM_API_BIND_ADDRESS=127.0.0.1 \
        LMM_API_PORT="$server_port" \
        PORT="$server_port" \
        LMM_LOCAL_ACCEPTANCE=true \
        GLOBAL_API_RATE_LIMIT_ENABLE=false \
        CRITICAL_RATE_LIMIT_ENABLE=false \
        SEARCH_RATE_LIMIT_ENABLE=false \
        TRUSTED_PROXIES=none \
        RELAY_RESPONSE_HEADER_TIMEOUT=1 \
        RELAY_TIMEOUT=6 \
        GIN_MODE=release \
        VERSION=v0.0.0-release-qualification \
        "$binary" >"$runtime/$log" 2>&1 &
      ;;
    rust)
      binary=${LMM_QUALIFICATION_RUST_BINARY:?LMM_QUALIFICATION_RUST_BINARY is required}
      env \
        LMM_RS_TEST_INSTANCE=1 \
        LMM_RS_SLOT=single \
        LMM_RS_TEST_VALKEY_PORT="$valkey_port" \
        LMM_RS_LISTEN_ADDR="127.0.0.1:$server_port" \
        DATABASE_URL="$rust_database_url" \
        VALKEY_URL="$valkey_url" \
        LMM_SCHEMA_CONTRACT=1 \
        SESSION_SECRET="$session_secret" \
        CRYPTO_SECRET="$crypto_secret" \
        PASSWORD_LOGIN_ENABLED=true \
        AUTH_COOKIE_SECURE=false \
        LMM_LOCAL_ACCEPTANCE=true \
        GLOBAL_API_RATE_LIMIT_ENABLE=false \
        CRITICAL_RATE_LIMIT_ENABLE=false \
        SEARCH_RATE_LIMIT_ENABLE=false \
        TRUSTED_PROXIES=none \
        LMM_RELAY_RESPONSE_HEADER_TIMEOUT_SECONDS=1 \
        LMM_RELAY_TIMEOUT_SECONDS=6 \
        LMM_RELAY_IDLE_TIMEOUT_SECONDS=10 \
        VERSION=v0.0.0-release-qualification \
        "$binary" >"$runtime/$log" 2>&1 &
      ;;
  esac
  server_pid=$!
}

wait_ready() {
  for _ in {1..240}; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      echo "$backend backend exited before readiness" >&2
      tail -n 240 "$runtime/$1" >&2 || true
      return 1
    fi
    code=$(curl --silent --output /dev/null --write-out '%{http_code}' --max-time 2 "$server_base/api/status" || true)
    [[ $code == 200 ]] && return 0
    sleep .25
  done
  echo "$backend backend did not become ready" >&2
  tail -n 240 "$runtime/$1" >&2 || true
  return 1
}

stop_backend() {
  local label=$1
  [[ -n ${server_pid:-} ]] || return 0
  if ! kill -0 "$server_pid" 2>/dev/null; then
    wait "$server_pid" 2>/dev/null || true
    server_pid=
    return 0
  fi
  kill -TERM "$server_pid"
  for _ in {1..120}; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      wait "$server_pid" 2>/dev/null || true
      server_pid=
      return 0
    fi
    sleep .1
  done
  echo "$backend backend failed graceful shutdown during $label" >&2
  return 1
}

bash "$repo_root/scripts/ci/configure-release-wiremock.sh"
start_backend server-first.log
wait_ready server-first.log
bash "$repo_root/scripts/ci/qualify-live-server.sh" full
stop_backend 'first qualification run'

# A release candidate must survive a process restart against the same durable
# PostgreSQL/Valkey state. This catches startup-only migrations and cache/session
# assumptions that ordinary request tests cannot see.
start_backend server-restart.log
wait_ready server-restart.log
bash "$repo_root/scripts/ci/qualify-live-server.sh" restart
stop_backend 'restart qualification run'

printf '{"backend":"%s","result":"passed"}\n' "$backend" >"$evidence/result.json"
echo "[$backend] production-shaped server qualification passed"
