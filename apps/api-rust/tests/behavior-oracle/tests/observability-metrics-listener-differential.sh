#!/usr/bin/env bash
# Real TCP differential for the PostgreSQL-backed performance metric reads.
# The Go tree is an external immutable oracle; both listeners use disposable
# PostgreSQL databases and separate Valkey logical databases.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
go_root=${LMM_GO_ORACLE_ROOT:-/tmp/5418ce6b6d45ed69167b0aad53f2f595e5bc8de9}
result_dir=${LMM_OBSERVABILITY_RESULT_DIR:-}
runtime=$(mktemp -d /tmp/lmm-observability-metrics-differential.XXXXXX)
# shellcheck disable=SC2034 # These variables are read indirectly by the shared process helpers.
go_pid='' rust_pid='' valkey_pid=''
# shellcheck disable=SC2034 # These variables are read indirectly by the shared process helpers.
go_pid_start='' rust_pid_start='' valkey_pid_start=''
pg_port=${LMM_OBSERVABILITY_PG_PORT:-}
go_port=${LMM_OBSERVABILITY_GO_PORT:-}
rust_port=${LMM_OBSERVABILITY_RUST_PORT:-}
valkey_port=${LMM_OBSERVABILITY_VALKEY_PORT:-}
seed_epoch=$(date +%s)
bucket_ts=$((seed_epoch - seed_epoch % 3600))

[[ $go_root == /* && -d $go_root && ! -L $go_root ]] || {
  echo "LMM_GO_ORACLE_ROOT must be an absolute, non-symlink immutable Go tree" >&2
  exit 2
}
case "$go_root" in
"$repo_root" | "$repo_root"/*)
  echo 'LMM_GO_ORACLE_ROOT must be external to the Rust repository' >&2
  exit 2
  ;;
esac
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_OBSERVABILITY_RESULT_DIR must be absolute and contain no ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi

pid_start_time() {
  [[ -r /proc/$1/stat ]] || return 1
  awk '{print $22}' "/proc/$1/stat"
}

record_pid() {
  local name=$1 pid=$2 start
  start=$(pid_start_time "$pid") || return 1
  printf -v "$name" '%s' "$pid"
  printf -v "${name}_start" '%s' "$start"
}

owned_pid_is_live() {
  local name=$1 pid start_name expected
  pid=${!name:-}
  start_name="${name}_start"
  expected=${!start_name:-}
  [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null &&
    [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]
}

stop_owned_process() {
  local name=$1 pid=${!1:-}
  if [[ -n $pid ]]; then
    if owned_pid_is_live "$name"; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    else
      echo "refusing to signal unowned or recycled PID $pid ($name)" >&2
    fi
  fi
  printf -v "$name" ''
  printf -v "${name}_start" ''
}

cleanup() {
  stop_owned_process go_pid || true
  stop_owned_process rust_pid || true
  stop_owned_process valkey_pid || true
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
  /tmp/lmm-observability-metrics-differential.*)
    if [[ ${LMM_KEEP_OBSERVABILITY_RUNTIME:-0} == 1 ]]; then
      echo "keeping observability differential runtime: $runtime" >&2
    else
      rm -rf -- "$runtime"
    fi
    ;;
  *) echo "refusing unexpected runtime removal: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'echo "observability metrics differential failed at line $LINENO" >&2' ERR

for command in cargo createdb curl go initdb jq openssl pg_dump pg_ctl psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || {
    echo "required command unavailable: $command" >&2
    exit 127
  }
done

allocate_port() {
  local candidate
  for _ in {1..200}; do
    candidate=$(shuf -i 20000-55000 -n 1)
    [[ -z $(ss -H -ltn "sport = :$candidate" 2>/dev/null) ]] && {
      printf '%s\n' "$candidate"
      return 0
    }
  done
  return 1
}
pg_port=${pg_port:-$(allocate_port)}
go_port=${go_port:-$(allocate_port)}
rust_port=${rust_port:-$(allocate_port)}
valkey_port=${valkey_port:-$(allocate_port)}
for port in "$pg_port" "$go_port" "$rust_port" "$valkey_port"; do
  [[ -z $(ss -H -ltn "sport = :$port" 2>/dev/null) ]] || {
    echo "refusing occupied port: $port" >&2
    exit 2
  }
done

go_database=observability_metrics_go
rust_database=observability_metrics_rust
go_database_url="postgresql://postgres@/$go_database?host=$runtime&port=$pg_port&sslmode=disable"
rust_database_url="postgresql://postgres@127.0.0.1:$pg_port/$rust_database"
go_valkey_url="redis://127.0.0.1:$valkey_port/1"
rust_valkey_url="redis://127.0.0.1:$valkey_port/2"
go_session_secret=$(openssl rand -base64 48 | tr -d '\n')
go_crypto_secret=$(openssl rand -base64 48 | tr -d '\n')
rust_session_secret='ObservabilityMetrics-2026!SyntheticSessionSecret'
rust_crypto_secret='ObservabilityMetrics-2026!SyntheticCryptoSecret'
dashboard_authorization='Bearer observability-root-pat-000000001'

initdb -D "$runtime/pg" --username=postgres --no-locale --encoding=UTF8 --auth=trust >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$rust_database"

mkdir "$runtime/valkey"
valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no \
  --dir "$runtime/valkey" >"$runtime/valkey.log" 2>&1 &
record_pid valkey_pid "$!"
for _ in {1..200}; do
  owned_pid_is_live valkey_pid || break
  [[ $(valkey-cli -h 127.0.0.1 -p "$valkey_port" ping 2>/dev/null || true) == PONG ]] && break
  sleep .05
done
[[ $(valkey-cli -h 127.0.0.1 -p "$valkey_port" ping 2>/dev/null || true) == PONG ]] || {
  sed -n '1,160p' "$runtime/valkey.log" >&2
  exit 1
}

cp -a "$go_root/." "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: >"$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  env -i PATH="$PATH" HOME="$HOME" GOTOOLCHAIN=local CGO_ENABLED=1 \
    go build -buildvcs=false -o "$runtime/current-go" .
)

wait_http() {
  local pid=$1 port=$2 path=$3
  for _ in {1..400}; do
    owned_pid_is_live "$pid" || return 1
    case $(curl --silent --output /dev/null --write-out '%{http_code}' \
      "http://127.0.0.1:$port$path" || true) in
    200 | 204) return 0 ;;
    esac
    sleep .05
  done
  return 1
}

start_go() {
  env -i PATH="$PATH" HOME="$HOME" TMPDIR="$runtime" \
    SQL_DSN="$go_database_url" PORT="$go_port" NODE_TYPE=master \
    LMM_DB_MIGRATION_MODE=apply MEMORY_CACHE_ENABLED=true \
    REDIS_CONN_STRING="$go_valkey_url" SYNC_FREQUENCY=60 \
    SESSION_SECRET="$go_session_secret" CRYPTO_SECRET="$go_crypto_secret" \
    GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
    TRUSTED_PROXIES=none GIN_MODE=release \
    "$runtime/current-go" >"$runtime/go.log" 2>&1 &
  record_pid go_pid "$!"
  wait_http go_pid "$go_port" /api/status || {
    sed -n '1,240p' "$runtime/go.log" >&2
    return 1
  }
}

start_go
stop_owned_process go_pid

# Clone the Go-migrated schema into the Rust database, then add Rust's startup
# contract table. This keeps the two listeners on the same production-shaped
# PostgreSQL schema rather than a hand-written subset.
pg_dump -h 127.0.0.1 -p "$pg_port" -U postgres -d "$go_database" \
  --schema-only --no-owner --no-privileges >"$runtime/go-schema.sql"
psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$rust_database" \
  -v ON_ERROR_STOP=1 -f "$runtime/go-schema.sql" >/dev/null
# The immutable Go oracle predates the current Rust-owned readiness contract's
# bounty tables. Install the current additive contract in both disposable
# databases so readiness checks cover the same mounted surface.
sed 's/__LMM_APP_SCHEMA__/public/g' \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/open-source-bounty-compat.sql"
for database in "$go_database" "$rust_database"; do
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" \
    -v ON_ERROR_STOP=1 -f "$runtime/open-source-bounty-compat.sql" >/dev/null
done
for database in "$go_database" "$rust_database"; do
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 \
    -v seed_epoch="$seed_epoch" -v bucket_ts="$bucket_ts" <<'SQL' >/dev/null
-- The immutable Go oracle may be at a migration revision predating the Rust
-- readiness probe's auth capability column. Keep the fixture production-shaped
-- without changing the oracle source or weakening readiness checks.
ALTER TABLE users
  ADD COLUMN IF NOT EXISTS console_activated_at BIGINT NOT NULL DEFAULT 0;
CREATE TABLE IF NOT EXISTS lmm_schema_contract (
  singleton BOOLEAN PRIMARY KEY,
  min_reader_version BIGINT NOT NULL,
  max_reader_version BIGINT NOT NULL
);
INSERT INTO lmm_schema_contract(singleton,min_reader_version,max_reader_version)
VALUES (TRUE,1,1)
ON CONFLICT (singleton) DO UPDATE SET min_reader_version=1,max_reader_version=1;
INSERT INTO options(key,value) VALUES
  ('GroupRatio','{"default":1,"vip":1}'),
  ('GroupGroupRatio','{}'),
  ('perf_metrics_setting','{"bucket_time":"hour"}'),
  ('TurnstileCheckEnabled','false')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value;
INSERT INTO users
  (id,username,password,role,status,access_token,auth_version,console_activated_at)
VALUES
  (999,'observability-root','unused-password',100,1,
   'observability-root-pat-000000001',1,0)
ON CONFLICT (id) DO UPDATE SET
  username=EXCLUDED.username,password=EXCLUDED.password,role=EXCLUDED.role,
  status=EXCLUDED.status,access_token=EXCLUDED.access_token,
  auth_version=EXCLUDED.auth_version,console_activated_at=EXCLUDED.console_activated_at;
INSERT INTO perf_metrics
  (id,model_name,"group",bucket_ts,request_count,success_count,total_latency_ms,
   ttft_sum_ms,ttft_count,output_tokens,generation_ms)
VALUES
  (901,'gpt-test','default',:'bucket_ts'::BIGINT - 3600,4,3,1000,200,2,300,2000),
  (902,'gpt-test','default',:'bucket_ts'::BIGINT,2,2,500,100,1,100,500),
  (903,'gpt-test','vip',:'bucket_ts'::BIGINT,1,0,300,0,0,50,500),
  (904,'gpt-test','hidden',:'bucket_ts'::BIGINT,9,9,90,0,0,90,900),
  (905,'other-model','default',:'bucket_ts'::BIGINT,2,1,600,0,0,80,800)
ON CONFLICT (id) DO UPDATE SET
  model_name=EXCLUDED.model_name,"group"=EXCLUDED."group",bucket_ts=EXCLUDED.bucket_ts,
  request_count=EXCLUDED.request_count,success_count=EXCLUDED.success_count,
  total_latency_ms=EXCLUDED.total_latency_ms,ttft_sum_ms=EXCLUDED.ttft_sum_ms,
  ttft_count=EXCLUDED.ttft_count,output_tokens=EXCLUDED.output_tokens, -- gitleaks:allow -- SQL column fixture
  generation_ms=EXCLUDED.generation_ms;
SQL
done

usage_cache_key=$'new-api:channel_affinity_usage_cache_stats:v1:rule-a\ndefault\nfp-a'
usage_cache_value='{"cached_token_rate_mode":"cached_over_prompt","hit":3,"total":4,"window_seconds":3600,"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"cached_tokens":80,"prompt_cache_hit_tokens":70,"last_seen_at":1700000000}'
valkey-cli -h 127.0.0.1 -p "$valkey_port" -n 1 SET "$usage_cache_key" "$usage_cache_value" >/dev/null
valkey-cli -h 127.0.0.1 -p "$valkey_port" -n 2 SET "$usage_cache_key" "$usage_cache_value" >/dev/null

start_go
env -i PATH="$PATH" \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=blue \
  DATABASE_URL="$rust_database_url" VALKEY_URL="$rust_valkey_url" \
  LMM_SCHEMA_CONTRACT=1 SESSION_SECRET="$rust_session_secret" \
  CRYPTO_SECRET="$rust_crypto_secret" PASSWORD_LOGIN_ENABLED=false \
  GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
  TRUSTED_PROXIES=none VERSION=v0.0.0 \
  "$repo_root/apps/api-rust/target/debug/lmm-api-rs" >"$runtime/rust.log" 2>&1 &
record_pid rust_pid "$!"
wait_http rust_pid "$rust_port" /readyz || {
  sed -n '1,240p' "$runtime/rust.log" >&2
  exit 1
}

capture() {
  local engine=$1 port=$2 name=$3 path=$4
  local prefix="$runtime/$engine-$name"
  local -a curl_args=(--silent --show-error --dump-header "$prefix.headers")
  curl_args+=(--output "$prefix.body" --write-out '%{http_code}')
  curl_args+=(--header "Authorization: $dashboard_authorization")
  curl "${curl_args[@]}" "http://127.0.0.1:$port$path" >"$prefix.status"
}

compare_case() {
  local name=$1 path=$2 expected_status=$3
  capture go "$go_port" "$name" "$path"
  capture rust "$rust_port" "$name" "$path"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$name.status") == "$expected_status" ]] || {
      echo "$engine $name returned $(<"$runtime/$engine-$name.status")" >&2
      sed -n '1,120p' "$runtime/$engine-$name.body" >&2
      return 1
    }
    jq -S . "$runtime/$engine-$name.body" >"$runtime/$engine-$name.sorted"
  done
  cmp -s "$runtime/go-$name.sorted" "$runtime/rust-$name.sorted" || {
    echo "Go/Rust $name response differs" >&2
    diff -u "$runtime/go-$name.sorted" "$runtime/rust-$name.sorted" >&2 || true
    return 1
  }
}

compare_case model '/api/perf-metrics?model=gpt-test&hours=24' 200
compare_case group '/api/perf-metrics?model=gpt-test&group=default&hours=24' 200
compare_case summary '/api/perf-metrics/summary?hours=24' 200
compare_case missing-model '/api/perf-metrics' 400
compare_case affinity '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default&key_fp=fp-a' 200
compare_case affinity-missing-rule '/api/log/channel_affinity_usage_cache?using_group=default&key_fp=fp-a' 400
compare_case affinity-missing-key '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default' 400

jq -e '.success == true and (.data.model_name == "gpt-test") and (.data.series_schema == "dbcd0a3c01b55203") and ((.data.groups | map(.group)) == ["default","vip"]) and (.data.groups[0].avg_latency_ms == 250) and (.data.groups[0].success_rate == 83.33333333333334)' \
  "$runtime/go-model.body" >/dev/null
jq -e '.success == true and (.data.models | length == 2) and (.data.models[0].model_name == "gpt-test") and (.data.models[0].avg_latency_ms == 257) and (.data.models[0].success_rate == 71.43)' \
  "$runtime/go-summary.body" >/dev/null
for engine in go rust; do
  jq -e '.success == true and (has("message") | not)' "$runtime/$engine-model.body" >/dev/null
  jq -e '.success == false and .message == "model is required"' "$runtime/$engine-missing-model.body" >/dev/null
done
jq -e '.success == true and .message == "" and .data.rule_name == "rule-a" and .data.using_group == "default" and .data.key_fp == "fp-a" and .data.hit == 3 and .data.total == 4 and .data.cached_token_rate_mode == "cached_over_prompt"' \
  "$runtime/go-affinity.body" >/dev/null
for engine in go rust; do
  jq -e '.success == false and .message == "missing param: rule_name"' "$runtime/$engine-affinity-missing-rule.body" >/dev/null
  jq -e '.success == false and .message == "missing param: key_fp"' "$runtime/$engine-affinity-missing-key.body" >/dev/null
done

for database in "$go_database" "$rust_database"; do
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -At -v ON_ERROR_STOP=1 \
    -c 'SELECT COUNT(*), COALESCE(SUM(request_count),0), COALESCE(SUM(success_count),0) FROM perf_metrics' \
    >"$runtime/$database.snapshot"
done
cmp -s "$runtime/$go_database.snapshot" "$runtime/$rust_database.snapshot"

if [[ -n $result_dir ]]; then
  for route in perf-metrics perf-metrics-summary; do
    jq -cn \
      --arg method GET \
      --arg path "$([[ $route == perf-metrics ]] && printf '/api/perf-metrics' || printf '/api/perf-metrics/summary')" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"observability-metrics",cases:4,isolated_runtime:true,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$result_dir/observability-$route.json"
  done
  jq -cn \
    --arg method GET \
    --arg path '/api/log/channel_affinity_usage_cache' \
    '{method:$method,path:$path,differential_verified:true,differential_scope:"observability-channel-affinity",cases:3,isolated_runtime:true,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
    >"$result_dir/observability-channel-affinity.json"
fi
jq -cn '{test:"observability-metrics-listener-differential",routes:3,cases:7,postgres_valkey_isolated:true,differential_verified:true,approval_credit:false,result:"passed"}'
