#!/usr/bin/env bash
# Current-Go versus Rust relay-misc listener differential for the single
# behaviorally approved OpenAI embeddings/type-1/fixed-price vertical.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
go_root=${LMM_CURRENT_GO_ORACLE_ROOT:-/tmp/lmm-current-go-oracle.7KzEYA}
runtime_base=${LMM_RELAY_MISC_PG_RUNTIME_BASE:-/tmp}
pg_port=${LMM_RELAY_MISC_PG_ORACLE_PORT:-45451}
go_port=${LMM_RELAY_MISC_GO_ORACLE_PORT:-18451}
rust_port=${LMM_RELAY_MISC_RUST_ORACLE_PORT:-38451}
provider_port=${LMM_RELAY_MISC_PROVIDER_ORACLE_PORT:-48451}
valkey_port=${LMM_RELAY_MISC_VALKEY_ORACLE_PORT:-58451}
runtime=$(mktemp -d "$runtime_base/lmm-relay-misc-pg-differential.XXXXXX")
result_dir=${LMM_RELAY_MISC_PG_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_RELAY_MISC_PG_RESULT_DIR must be an absolute path without ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
go_database=relay_misc_go
rust_database=relay_misc_rust
# shellcheck disable=SC2034 # PID start times are read through indirect expansion.
go_pid='' go_pid_start='' rust_pid='' rust_pid_start='' provider_pid='' provider_pid_start=''
# shellcheck disable=SC2034 # PID start times are read through indirect expansion.
valkey_pid='' valkey_pid_start=''
go_binary="$runtime/current-go"
rust_binary=${LMM_RELAY_MISC_RUST_ORACLE_BINARY:-"$repo_root/apps/api-rust/target/debug/examples/relay_misc_pg_listener"}
provider_fixture="$repo_root/apps/api-rust/tests/behavior-oracle/fixtures/relay_misc_provider.py"
seed_epoch=$(date +%s)
go_session_secret=$(openssl rand -base64 48 | tr -d '\n')
go_crypto_secret=$(openssl rand -base64 48 | tr -d '\n')

pid_start_time() {
  [[ -r /proc/$1/stat ]] || return 1
  awk '{print $22}' "/proc/$1/stat"
}

record_pid() {
  local pid_name=$1 pid=$2 start
  printf -v "$pid_name" '%s' "$pid"
  start=$(pid_start_time "$pid") || return 1
  printf -v "${pid_name}_start" '%s' "$start"
}

owned_pid_is_live() {
  local pid_name=$1 pid start_name expected
  pid=${!pid_name:-}
  start_name="${pid_name}_start"
  expected=${!start_name:-}
  [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null \
    && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]
}

stop_owned_process() {
  local pid_name=$1 pid
  pid=${!pid_name:-}
  if [[ -n $pid ]]; then
    if owned_pid_is_live "$pid_name"; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    else
      echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2
    fi
  fi
  printf -v "$pid_name" ''
  printf -v "${pid_name}_start" ''
}

cleanup() {
  stop_owned_process go_pid || true
  stop_owned_process rust_pid || true
  stop_owned_process provider_pid || true
  stop_owned_process valkey_pid || true
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  if [[ ${LMM_RELAY_MISC_PG_KEEP_RUNTIME:-0} == 1 ]]; then
    echo "keeping relay-misc PG differential runtime: $runtime" >&2
    return
  fi
  case "$runtime" in
    "$runtime_base"/lmm-relay-misc-pg-differential.*) find "$runtime" -depth -delete ;;
    *) echo "refusing unexpected runtime removal: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'echo "relay-misc PG differential failed at line $LINENO" >&2' ERR

for command in brotli cargo createdb curl go gzip initdb jq openssl pg_ctl psql python3 ss \
  valkey-cli valkey-server zstd; do
  command -v "$command" >/dev/null || {
    echo "required command unavailable: $command" >&2
    exit 127
  }
done
[[ $go_root == /* && -d $go_root && ! -L $go_root ]] || {
  echo 'LMM_CURRENT_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2
  exit 2
}
[[ -f $provider_fixture ]] || { echo "missing provider fixture: $provider_fixture" >&2; exit 2; }
[[ -d $runtime_base && -w $runtime_base ]] || { echo "runtime base is not writable: $runtime_base" >&2; exit 2; }

for relative_file in \
  middleware/auth.go \
  middleware/distributor.go \
  router/relay-router.go \
  service/billing_session.go \
  service/log_info_generate.go; do
  cmp -s "$repo_root/apps/api-go/$relative_file" "$go_root/$relative_file" || {
    echo "current Go oracle source drifted at $relative_file" >&2
    exit 2
  }
done

for oracle_port in "$pg_port" "$go_port" "$rust_port" "$provider_port" "$valkey_port"; do
  [[ -z $(ss -H -ltn "sport = :$oracle_port" 2>/dev/null) ]] || {
    echo "refusing occupied loopback port: $oracle_port" >&2
    exit 2
  }
done

wait_for_http() {
  local pid=$1 port=$2 route_path=$3
  for _ in {1..400}; do
    kill -0 "$pid" 2>/dev/null || return 1
    case $(curl --silent --output /dev/null --write-out '%{http_code}' \
      "http://127.0.0.1:$port$route_path" || true) in
      200|204) return 0 ;;
    esac
    sleep .05
  done
  return 1
}

(
  cd "$go_root"
  env -i PATH="$PATH" HOME="$HOME" TMPDIR="$runtime" GOTOOLCHAIN=local CGO_ENABLED=1 \
    go build -buildvcs=false -o "$go_binary" .
)

if [[ ${LMM_RELAY_MISC_PG_SKIP_RUST_BUILD:-0} != 1 ]]; then
  env CARGO_BUILD_JOBS=2 cargo build \
    --manifest-path "$repo_root/apps/api-rust/Cargo.toml" \
    -p lmm-api-rs --example relay_misc_pg_listener --locked
fi
[[ -x $rust_binary ]] || { echo "missing Rust relay listener: $rust_binary" >&2; exit 2; }

initdb -D "$runtime/pg" --username=postgres --no-locale --encoding=UTF8 --auth=trust >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$rust_database"

go_database_url="postgresql://postgres@127.0.0.1:$pg_port/$go_database?sslmode=disable"
rust_database_url="postgresql://postgres@127.0.0.1:$pg_port/$rust_database"
go_valkey_url="redis://127.0.0.1:$valkey_port/1"
rust_valkey_url="redis://127.0.0.1:$valkey_port/2"

mkdir "$runtime/valkey"
valkey-server --bind 127.0.0.1 --protected-mode yes --port "$valkey_port" \
  --save '' --appendonly no --dir "$runtime/valkey" \
  >"$runtime/valkey.log" 2>&1 &
record_pid valkey_pid "$!"
for _ in {1..400}; do
  owned_pid_is_live valkey_pid || break
  [[ $(valkey-cli --raw -h 127.0.0.1 -p "$valkey_port" ping 2>/dev/null || true) == PONG ]] \
    && break
  sleep .05
done
[[ $(valkey-cli --raw -h 127.0.0.1 -p "$valkey_port" ping 2>/dev/null || true) == PONG ]] || {
  sed -n '1,160p' "$runtime/valkey.log" >&2
  exit 1
}

start_go() {
  env -i PATH="$PATH" HOME="$HOME" TMPDIR="$runtime" \
    SQL_DSN="$go_database_url" PORT="$go_port" NODE_TYPE=master \
    LMM_DB_MIGRATION_MODE=apply MEMORY_CACHE_ENABLED=true \
    REDIS_CONN_STRING="$go_valkey_url" SYNC_FREQUENCY=60 \
    SESSION_SECRET="$go_session_secret" CRYPTO_SECRET="$go_crypto_secret" \
    GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false \
    MODEL_REQUEST_RATE_LIMIT_ENABLE=true TRUSTED_PROXIES=none GIN_MODE=release \
    "$go_binary" >>"$runtime/go.log" 2>&1 &
  record_pid go_pid "$!"
  wait_for_http "$go_pid" "$go_port" /api/status || {
    sed -n '1,240p' "$runtime/go.log" >&2
    return 1
  }
}

start_rust() {
  env -i PATH="$PATH" \
    LMM_RELAY_MISC_HARNESS_ALLOW=1 \
    LMM_RELAY_MISC_HARNESS_LISTEN="127.0.0.1:$rust_port" \
    LMM_RELAY_MISC_HARNESS_DATABASE_URL="$rust_database_url" \
    LMM_RELAY_MISC_HARNESS_VALKEY_URL="$rust_valkey_url" \
    "$rust_binary" >>"$runtime/rust.log" 2>&1 &
  record_pid rust_pid "$!"
  wait_for_http "$rust_pid" "$rust_port" /readyz || {
    sed -n '1,200p' "$runtime/rust.log" >&2
    return 1
  }
}

# First boot performs current-Go's own PostgreSQL migrations. Seed only after
# that process exits, then restart so every in-memory option/channel cache sees
# the controlled state.
start_go
stop_owned_process go_pid

psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$go_database" -v ON_ERROR_STOP=1 <<SQL >/dev/null
INSERT INTO setups (id,version,initialized_at) VALUES (1,'relay-oracle',1);
INSERT INTO users
  (id,username,password,role,status,email,quota,used_quota,request_count,"group",setting,
   created_at,last_api_activity_at,console_activated_at,auth_version,trust_level_override)
VALUES
  (42,'relay-user','unused',1,1,'relay@example.test',1000,0,0,'default','{}',
   $seed_epoch,0,1,1,NULL);
INSERT INTO top_ups
  (id,user_id,amount,credited_quota,settled_amount_micros,money,trade_no,
   payment_method,payment_provider,create_time,complete_time,status)
VALUES
  (1,42,50000000,0,0,100,'relay-trust-paid','stripe','stripe',
   $seed_epoch,$seed_epoch,'success');
INSERT INTO tokens
  (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,
   unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,"group",cross_group_retry)
VALUES
  (73,42,'relayprobe',1,'relay-token',1,1,-1,1000,FALSE,FALSE,'','',0,'',FALSE);
INSERT INTO channels
  (id,type,key,status,name,weight,created_time,base_url,"group",used_quota,models,
   model_mapping,priority,auto_ban,param_override,header_override,status_code_mapping)
VALUES
  (7,1,'provider-owned-secret',1,'loopback',10,1,'http://127.0.0.1:$provider_port',
   'default',0,'gpt-test','',10,0,'','','{"429":503}');
INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight)
VALUES ('default','gpt-test',7,TRUE,10,10);
INSERT INTO options (key,value) VALUES
  ('performance_setting.monitor_enabled','false'),
  ('ModelRequestRateLimitEnabled','true'),
  ('ModelRequestRateLimitDurationMinutes','1'),
  ('ModelRequestRateLimitCount','10'),
  ('ModelRequestRateLimitSuccessCount','1000'),
  ('ModelRequestRateLimitGroup','{}'),
  ('UserUsableGroups','{"default":"default"}'),
  ('GroupRatio','{"default":1}'),
  ('GroupGroupRatio','{}'),
  ('ModelPrice','{"gpt-test":0.000002}'),
  ('QuotaPerUnit','500000'),
  ('RetryTimes','0')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value;
SQL

psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$rust_database" \
  -v ON_ERROR_STOP=1 -v seed_epoch="$seed_epoch" <<'SQL' >/dev/null
CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE users (
  id BIGINT PRIMARY KEY, username TEXT, password TEXT NOT NULL, role BIGINT DEFAULT 1,
  status BIGINT DEFAULT 1, email TEXT, quota BIGINT DEFAULT 0,
  used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0,
  created_at BIGINT NOT NULL DEFAULT 0,
  last_api_activity_at BIGINT NOT NULL DEFAULT 0,
  trust_level_override BIGINT,
  "group" VARCHAR(64) DEFAULT 'default', setting TEXT, auth_version BIGINT DEFAULT 1,
  deleted_at TIMESTAMPTZ
);
CREATE TABLE tokens (
  id BIGINT PRIMARY KEY, user_id BIGINT, key VARCHAR(128), status BIGINT DEFAULT 1,
  name TEXT, created_time BIGINT, accessed_time BIGINT, expired_time BIGINT DEFAULT -1,
  remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN,
  model_limits_enabled BOOLEAN, model_limits TEXT, allow_ips TEXT DEFAULT '',
  used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN,
  deleted_at TIMESTAMPTZ
);
CREATE TABLE channels (
  id BIGINT PRIMARY KEY, type BIGINT DEFAULT 0, key TEXT NOT NULL,
  status BIGINT DEFAULT 1, name TEXT, weight BIGINT DEFAULT 0,
  base_url TEXT DEFAULT '', "group" VARCHAR(64) DEFAULT 'default',
  used_quota BIGINT DEFAULT 0, model_mapping TEXT, priority BIGINT DEFAULT 0,
  param_override TEXT, header_override TEXT, status_code_mapping TEXT
);
CREATE TABLE abilities (
  "group" VARCHAR(64) NOT NULL, model VARCHAR(255) NOT NULL,
  channel_id BIGINT NOT NULL, enabled BOOLEAN, priority BIGINT DEFAULT 0,
  weight BIGINT DEFAULT 0, PRIMARY KEY ("group",model,channel_id)
);
CREATE TABLE logs (
  user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT,
  username TEXT DEFAULT '', token_name TEXT DEFAULT '', model_name TEXT DEFAULT '',
  quota BIGINT DEFAULT 0, prompt_tokens BIGINT DEFAULT 0,
  completion_tokens BIGINT DEFAULT 0, use_time BIGINT DEFAULT 0,
  is_stream BOOLEAN, channel_id BIGINT, channel_name TEXT, token_id BIGINT DEFAULT 0,
  "group" TEXT, ip TEXT DEFAULT '', request_id VARCHAR(64) DEFAULT '',
  upstream_request_id VARCHAR(128) DEFAULT '', other TEXT
);
CREATE TABLE top_ups (
  id BIGINT PRIMARY KEY, user_id BIGINT, amount BIGINT DEFAULT 0,
  credited_quota BIGINT DEFAULT 0, settled_amount_micros BIGINT DEFAULT 0,
  money DOUBLE PRECISION DEFAULT 0, trade_no TEXT, payment_method TEXT, payment_provider TEXT,
  create_time BIGINT DEFAULT 0, complete_time BIGINT DEFAULT 0, status TEXT
);
INSERT INTO options (key,value) VALUES
  ('performance_setting.monitor_enabled','false'),
  ('ModelRequestRateLimitEnabled','true'),
  ('ModelRequestRateLimitDurationMinutes','1'),
  ('ModelRequestRateLimitCount','10'),
  ('ModelRequestRateLimitSuccessCount','1000'),
  ('ModelRequestRateLimitGroup','{}'),
  ('UserUsableGroups','{"default":"default"}'),
  ('GroupRatio','{"default":1}'),
  ('GroupGroupRatio','{}'),
  ('ModelPrice','{"gpt-test":0.000002}'),
  ('QuotaPerUnit','500000');
INSERT INTO users
  (id,username,password,role,status,email,quota,used_quota,request_count,
   created_at,last_api_activity_at,trust_level_override,"group",setting,auth_version)
VALUES (42,'relay-user','unused',1,1,'relay@example.test',1000,0,0,
        :'seed_epoch'::BIGINT,0,NULL,'default','{}',1);
INSERT INTO top_ups
  (id,user_id,amount,credited_quota,settled_amount_micros,money,trade_no,
   payment_method,payment_provider,create_time,complete_time,status)
VALUES
  (1,42,50000000,0,0,100,'relay-trust-paid','stripe','stripe',
   :'seed_epoch'::BIGINT,:'seed_epoch'::BIGINT,'success');
INSERT INTO tokens
  (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,
   unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,"group",cross_group_retry)
VALUES (73,42,'relayprobe',1,'relay-token',1,1,-1,1000,FALSE,FALSE,'','',0,'',FALSE);
INSERT INTO channels
  (id,type,key,status,name,weight,base_url,"group",used_quota,model_mapping,
   priority,param_override,header_override,status_code_mapping)
VALUES
  (7,1,'provider-owned-secret',1,'loopback',10,'http://127.0.0.1:48451',
   'default',0,'',10,'','','{"429":503}');
INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight)
VALUES ('default','gpt-test',7,TRUE,10,10);
SQL
# Replace the default provider port in the quoted Rust seed without allowing
# shell expansion inside the schema definition above.
psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$rust_database" -v ON_ERROR_STOP=1 \
  -c "UPDATE channels SET base_url='http://127.0.0.1:$provider_port' WHERE id=7" >/dev/null

: >"$runtime/provider-hits.jsonl"
python3 -u "$provider_fixture" "$provider_port" "$runtime/provider-hits.jsonl" \
  >"$runtime/provider.log" 2>&1 &
record_pid provider_pid "$!"
wait_for_http "$provider_pid" "$provider_port" /health || {
  sed -n '1,160p' "$runtime/provider.log" >&2
  exit 1
}

start_go
start_rust

call_listener() {
  local engine=$1 scenario=$2 port=$3 bearer=$4 body=$5
  local prefix="$runtime/$engine-$scenario"
  curl --silent --show-error \
    --dump-header "$prefix.headers" \
    --output "$prefix.body" \
    --write-out '%{http_code}' \
    --request POST \
    --header "authorization: Bearer $bearer" \
    --header 'content-type: application/json' \
    --header 'x-caller-secret: must-not-reach-provider' \
    --data-binary "$body" \
    "http://127.0.0.1:$port/v1/embeddings" >"$prefix.status"
}

call_compressed_listener() {
  local engine=$1 scenario=$2 port=$3 bearer=$4 encoding=$5 body_file=$6
  local prefix="$runtime/$engine-$scenario"
  curl --silent --show-error \
    --dump-header "$prefix.headers" \
    --output "$prefix.body" \
    --write-out '%{http_code}' \
    --request POST \
    --header "authorization: Bearer $bearer" \
    --header 'content-type: application/json' \
    --header "content-encoding: $encoding" \
    --header 'x-caller-secret: must-not-reach-provider' \
    --data-binary "@$body_file" \
    "http://127.0.0.1:$port/v1/embeddings" >"$prefix.status"
}

normalize_request_id() {
  sed -E 's/\(request id: [^)]*\)/(request id: REQUEST_ID)/g' "$1"
}

content_type_value() {
  awk 'BEGIN { IGNORECASE=1 } /^content-type:/ {
    sub(/^[^:]*:[[:space:]]*/, ""); sub(/\r$/, ""); print; exit
  }' "$1"
}

compare_compressed_success() {
  local encoding=$1 body_file=$2 scenario="compressed-$1" engine
  call_compressed_listener go "$scenario" "$go_port" sk-relayprobe "$encoding" "$body_file"
  call_compressed_listener rust "$scenario" "$rust_port" sk-relayprobe "$encoding" "$body_file"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$scenario.status") == 200 ]] || {
      echo "$engine $scenario status was $(<"$runtime/$engine-$scenario.status")" >&2
      sed -n '1,80p' "$runtime/$engine-$scenario.body" >&2
      exit 1
    }
    grep -Eiq '^content-type: application/json' "$runtime/$engine-$scenario.headers" || {
      echo "$engine $scenario did not preserve provider content-type" >&2
      exit 1
    }
  done
  cmp -s "$runtime/go-$scenario.body" "$runtime/rust-$scenario.body" || {
    echo "Go/Rust $scenario bodies differ" >&2
    diff -u "$runtime/go-$scenario.body" "$runtime/rust-$scenario.body" >&2 || true
    exit 1
  }
  cmp -s "$runtime/go-success.body" "$runtime/go-$scenario.body" || {
    echo "$scenario did not preserve the ordinary provider response" >&2
    exit 1
  }
}

compare_malformed_compression() {
  local encoding=$1 body_file=$2 scenario="malformed-$1" engine
  local go_content_type rust_content_type
  call_compressed_listener go "$scenario" "$go_port" sk-relayprobe "$encoding" "$body_file"
  call_compressed_listener rust "$scenario" "$rust_port" sk-relayprobe "$encoding" "$body_file"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$scenario.status") == 400 ]] || {
      echo "$engine $scenario status was $(<"$runtime/$engine-$scenario.status")" >&2
      sed -n '1,80p' "$runtime/$engine-$scenario.body" >&2
      exit 1
    }
  done
  if ! diff -u \
    <(normalize_request_id "$runtime/go-$scenario.body") \
    <(normalize_request_id "$runtime/rust-$scenario.body") >/dev/null; then
    echo "Go/Rust $scenario bodies differ" >&2
    diff -u \
      <(normalize_request_id "$runtime/go-$scenario.body") \
      <(normalize_request_id "$runtime/rust-$scenario.body") >&2 || true
    exit 1
  fi
  go_content_type=$(content_type_value "$runtime/go-$scenario.headers")
  rust_content_type=$(content_type_value "$runtime/rust-$scenario.headers")
  [[ $go_content_type == "$rust_content_type" ]] || {
    echo "Go/Rust $scenario content types differ: go='$go_content_type' rust='$rust_content_type'" >&2
    exit 1
  }
}

compare_upstream_error() {
  local scenario=$1 input=$2 engine
  call_listener go "$scenario" "$go_port" sk-relayprobe \
    "{\"model\":\"gpt-test\",\"input\":\"$input\"}"
  call_listener rust "$scenario" "$rust_port" sk-relayprobe \
    "{\"model\":\"gpt-test\",\"input\":\"$input\"}"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$scenario.status") == 503 ]] || {
      echo "$engine $scenario status was $(<"$runtime/$engine-$scenario.status")" >&2
      sed -n '1,80p' "$runtime/$engine-$scenario.body" >&2
      exit 1
    }
    grep -Eiq '^content-type: application/json' "$runtime/$engine-$scenario.headers" || {
      echo "$engine $scenario did not return a JSON error envelope" >&2
      exit 1
    }
    ! grep -Eiq '^retry-after:' "$runtime/$engine-$scenario.headers" || {
      echo "$engine $scenario unexpectedly forwarded provider Retry-After" >&2
      exit 1
    }
    ! grep -Eiq '^x-request-id: provider-generic-request-id' \
      "$runtime/$engine-$scenario.headers" || {
      echo "$engine $scenario unexpectedly forwarded the provider request ID" >&2
      exit 1
    }
  done
  diff -u \
    <(normalize_request_id "$runtime/go-$scenario.body") \
    <(normalize_request_id "$runtime/rust-$scenario.body") >/dev/null || {
    echo "Go/Rust $scenario bodies differ" >&2
    diff -u \
      <(normalize_request_id "$runtime/go-$scenario.body") \
      <(normalize_request_id "$runtime/rust-$scenario.body") >&2 || true
    exit 1
  }
}

call_listener go success "$go_port" sk-relayprobe '{"model":"gpt-test","input":"hello"}'
call_listener rust success "$rust_port" sk-relayprobe '{"model":"gpt-test","input":"hello"}'

[[ $(<"$runtime/go-success.status") == 200 ]] || {
  echo "current Go relay returned $(<"$runtime/go-success.status")" >&2
  sed -n '1,80p' "$runtime/go-success.body" >&2
  exit 1
}
[[ $(<"$runtime/rust-success.status") == 200 ]] || {
  echo "Rust relay returned $(<"$runtime/rust-success.status")" >&2
  sed -n '1,80p' "$runtime/rust-success.body" >&2
  exit 1
}
cmp -s "$runtime/go-success.body" "$runtime/rust-success.body" || {
  echo 'Go/Rust response bodies differ' >&2
  diff -u "$runtime/go-success.body" "$runtime/rust-success.body" >&2 || true
  exit 1
}

for engine in go rust; do
  grep -Eiq '^content-type: application/json' "$runtime/$engine-success.headers" || {
    echo "$engine did not preserve provider content-type" >&2
    exit 1
  }
  grep -Eiq '^x-request-id: provider-generic-request-id' "$runtime/$engine-success.headers" || {
    echo "$engine did not preserve provider x-request-id" >&2
    exit 1
  }
done
go_connection_named_header_leak=false
if grep -Eiq '^x-hop-leak:' "$runtime/go-success.headers"; then
  go_connection_named_header_leak=true
  echo 'current Go forwards a Connection-named response header; Rust keeps the safer boundary' >&2
fi
! grep -Eiq '^x-hop-leak:' "$runtime/rust-success.headers" || {
  echo 'Rust leaked a Connection-named response header' >&2
  exit 1
}
! grep -Eiq '^x-oneapi-request-id: provider-shadow-request-id' "$runtime/go-success.headers" || {
  echo 'Go leaked the provider instance request ID' >&2
  exit 1
}
! grep -Eiq '^x-oneapi-request-id:' "$runtime/rust-success.headers" || {
  echo 'Rust harness leaked the provider instance request ID' >&2
  exit 1
}

jq -s -e '
  length == 2 and .[0] == .[1]
  and .[0].path == "/v1/embeddings"
  and .[0].authorization_valid == true
  and .[0].caller_secret_present == false
  and .[0].content_type == "application/json"
  and .[0].body == {model:"gpt-test",input:"hello"}
' "$runtime/provider-hits.jsonl" >/dev/null

snapshot_database() {
  local database=$1 output=$2
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -At -v ON_ERROR_STOP=1 <<'SQL' \
    | jq -S . >"$output"
SELECT jsonb_build_object(
  'user', jsonb_build_object(
    'quota',u.quota,'used_quota',u.used_quota,'request_count',u.request_count,
    'last_api_activity_recorded',u.last_api_activity_at > 0
  ),
  'token', jsonb_build_object(
    'remain_quota',t.remain_quota,'used_quota',t.used_quota,
    'accessed_time_recorded',t.accessed_time > 1
  ),
  'channel', jsonb_build_object('used_quota',c.used_quota),
  'log_count',(SELECT COUNT(*) FROM logs),
  'logs',(SELECT COALESCE(jsonb_agg(jsonb_build_object(
    'user_id',l.user_id,'type',l.type,'content',l.content,'username',l.username,
    'token_name',l.token_name,'model_name',l.model_name,'quota',l.quota,
    'prompt_tokens',l.prompt_tokens,'completion_tokens',l.completion_tokens,
    'use_time_nonnegative',l.use_time >= 0,'is_stream',l.is_stream,
    'channel_id',l.channel_id,'channel_name',l.channel_name,'token_id',l.token_id,
    'group',l."group",'ip',l.ip,'request_id_recorded',l.request_id <> '',
    'upstream_request_id',l.upstream_request_id,
    'other',CASE WHEN COALESCE(l.other,'') = '' THEN NULL ELSE l.other::jsonb END
  ) ORDER BY l.type,l.quota,l.model_name),'[]'::jsonb) FROM logs l WHERE l.user_id=u.id)
)::text
FROM users u
JOIN tokens t ON t.user_id=u.id AND t.id=73
JOIN channels c ON c.id=7
WHERE u.id=42;
SQL
}

wait_for_minimum_log_count() {
  local database=$1 minimum=$2 count
  for _ in {1..200}; do
    count=$(psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -At \
      -v ON_ERROR_STOP=1 -c 'SELECT COUNT(*) FROM logs')
    [[ $count =~ ^[0-9]+$ && $count -ge $minimum ]] && return 0
    sleep .05
  done
  echo "$database did not persist at least $minimum relay logs before snapshot" >&2
  return 1
}

wait_for_minimum_log_count "$go_database" 1
wait_for_minimum_log_count "$rust_database" 1
snapshot_database "$go_database" "$runtime/go.snapshot.json"
snapshot_database "$rust_database" "$runtime/rust.snapshot.json"
if ! cmp -s "$runtime/go.snapshot.json" "$runtime/rust.snapshot.json"; then
  echo 'Go/Rust PostgreSQL side effects differ:' >&2
  diff -u "$runtime/go.snapshot.json" "$runtime/rust.snapshot.json" >&2 || true
  exit 1
fi

printf '%s' '{"model":"gpt-test","input":"compressed-gzip"}' >"$runtime/gzip.json"
printf '%s' '{"model":"gpt-test","input":"compressed-br"}' >"$runtime/br.json"
printf '%s' '{"model":"gpt-test","input":"compressed-zstd"}' >"$runtime/zstd.json"
gzip -n -c "$runtime/gzip.json" >"$runtime/gzip.body"
brotli -q 5 -c "$runtime/br.json" >"$runtime/br.body"
zstd -q -c "$runtime/zstd.json" >"$runtime/zstd.body"
printf '%s' 'not-a-valid-compressed-stream' >"$runtime/malformed-compressed.body"

compare_malformed_compression gzip "$runtime/malformed-compressed.body"
compare_malformed_compression br "$runtime/malformed-compressed.body"
compare_malformed_compression zstd "$runtime/malformed-compressed.body"
[[ $(wc -l <"$runtime/provider-hits.jsonl") == 2 ]] || {
  echo 'malformed compressed requests unexpectedly reached the provider' >&2
  exit 1
}

compare_compressed_success gzip "$runtime/gzip.body"
compare_compressed_success br "$runtime/br.body"
compare_compressed_success zstd "$runtime/zstd.body"

compare_upstream_error upstream-error-string fail
compare_upstream_error upstream-error-message fail-message
compare_upstream_error upstream-error-openai fail-openai
compare_upstream_error upstream-error-invalid-json fail-invalid-json

call_listener go invalid-token "$go_port" sk-not-a-token '{"model":"gpt-test","input":"hidden"}'
call_listener rust invalid-token "$rust_port" sk-not-a-token '{"model":"gpt-test","input":"hidden"}'
[[ $(<"$runtime/go-invalid-token.status") == 404 ]] || {
  echo "current Go invalid-token status was $(<"$runtime/go-invalid-token.status")" >&2
  exit 1
}
[[ $(<"$runtime/rust-invalid-token.status") == 404 ]] || {
  echo "Rust invalid-token status was $(<"$runtime/rust-invalid-token.status")" >&2
  exit 1
}
cmp -s "$runtime/go-invalid-token.body" "$runtime/rust-invalid-token.body" || {
  echo 'Go/Rust invalid-token bodies differ' >&2
  diff -u "$runtime/go-invalid-token.body" "$runtime/rust-invalid-token.body" >&2 || true
  exit 1
}

call_listener go model-rate-limit "$go_port" sk-relayprobe \
  '{"model":"gpt-test","input":"must-not-reach-provider"}'
call_listener rust model-rate-limit "$rust_port" sk-relayprobe \
  '{"model":"gpt-test","input":"must-not-reach-provider"}'
for engine in go rust; do
  [[ $(<"$runtime/$engine-model-rate-limit.status") == 429 ]] || {
    echo "$engine model-rate-limit status was $(<"$runtime/$engine-model-rate-limit.status")" >&2
    sed -n '1,80p' "$runtime/$engine-model-rate-limit.body" >&2
    exit 1
  }
  grep -Eiq '^content-type: application/json' \
    "$runtime/$engine-model-rate-limit.headers" || {
    echo "$engine model-rate-limit did not return JSON" >&2
    exit 1
  }
  jq -e '
    .error.code == "invalid_request"
    and .error.type == "new_api_error"
    and (.error.message | test("\\(request id: [^)]+\\)$"))
  ' "$runtime/$engine-model-rate-limit.body" >/dev/null || {
    echo "$engine model-rate-limit body has the wrong OpenAI error shape" >&2
    sed -n '1,80p' "$runtime/$engine-model-rate-limit.body" >&2
    exit 1
  }
done
if ! diff -u \
  <(normalize_request_id "$runtime/go-model-rate-limit.body") \
  <(normalize_request_id "$runtime/rust-model-rate-limit.body") >/dev/null; then
  echo 'Go/Rust model-rate-limit bodies differ' >&2
  diff -u \
    <(normalize_request_id "$runtime/go-model-rate-limit.body") \
    <(normalize_request_id "$runtime/rust-model-rate-limit.body") >&2 || true
  exit 1
fi

for valkey_database in 1 2; do
  valkey=(valkey-cli --raw -h 127.0.0.1 -p "$valkey_port" -n "$valkey_database")
  [[ $("${valkey[@]}" TYPE rateLimit:42) == hash ]] || {
    echo "Valkey DB $valkey_database is missing the total-attempt token bucket" >&2
    exit 1
  }
  [[ $("${valkey[@]}" HLEN rateLimit:42) == 2 ]] || {
    echo "Valkey DB $valkey_database has the wrong token-bucket shape" >&2
    exit 1
  }
  [[ $("${valkey[@]}" TTL rateLimit:42) == -1 ]] || {
    echo "Valkey DB $valkey_database unexpectedly expires the Go-compatible token bucket" >&2
    exit 1
  }
  bucket_tokens=$("${valkey[@]}" HGET rateLimit:42 tokens)
  [[ $bucket_tokens =~ ^[0-9]+$ && $bucket_tokens -lt 60 ]] || {
    echo "Valkey DB $valkey_database did not preserve the exhausted token count" >&2
    exit 1
  }
  [[ $("${valkey[@]}" HGET rateLimit:42 last_time) =~ ^[0-9]+$ ]] || {
    echo "Valkey DB $valkey_database has an invalid token-bucket timestamp" >&2
    exit 1
  }
  [[ $("${valkey[@]}" TYPE rateLimit:MRRLS:42) == list \
    && $("${valkey[@]}" LLEN rateLimit:MRRLS:42) == 4 ]] || {
    echo "Valkey DB $valkey_database did not record exactly four successful requests" >&2
    exit 1
  }
  success_timestamp=$("${valkey[@]}" LINDEX rateLimit:MRRLS:42 0)
  [[ $success_timestamp =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}\.[0-9]{3}Z$ ]] || {
    echo "Valkey DB $valkey_database has an invalid success timestamp" >&2
    exit 1
  }
  success_ttl=$("${valkey[@]}" TTL rateLimit:MRRLS:42)
  [[ $success_ttl -gt 0 && $success_ttl -le 60 ]] || {
    echo "Valkey DB $valkey_database has an invalid success-window TTL" >&2
    exit 1
  }
done

jq -s -e '
  length == 16
  and .[0] == .[1]
  and .[2] == .[3]
  and .[4] == .[5]
  and .[6] == .[7]
  and .[8] == .[9]
  and .[10] == .[11]
  and .[12] == .[13]
  and .[14] == .[15]
  and all(.[];
    .path == "/v1/embeddings"
    and .authorization_valid == true
    and .caller_secret_present == false
    and .content_encoding == "")
  and [.[2].body.input,.[4].body.input,.[6].body.input]
    == ["compressed-gzip","compressed-br","compressed-zstd"]
  and [.[8].body.input,.[10].body.input,.[12].body.input,.[14].body.input]
    == ["fail","fail-message","fail-openai","fail-invalid-json"]
' "$runtime/provider-hits.jsonl" >/dev/null

wait_for_minimum_log_count "$go_database" 4
wait_for_minimum_log_count "$rust_database" 4
snapshot_database "$go_database" "$runtime/go-after-errors.snapshot.json"
snapshot_database "$rust_database" "$runtime/rust-after-errors.snapshot.json"
if ! cmp -s "$runtime/go-after-errors.snapshot.json" "$runtime/rust-after-errors.snapshot.json"; then
  echo 'Go/Rust PostgreSQL side effects after upstream error, invalid token, and rate limit differ:' >&2
  diff -u "$runtime/go-after-errors.snapshot.json" "$runtime/rust-after-errors.snapshot.json" >&2 || true
  exit 1
fi

# The total-attempt limiter is deliberately exhausted above. Reset only its
# isolated oracle keys so the missing-usage billing probe exercises provider
# success rather than the already-proven rate-limit boundary.
for valkey_database in 1 2; do
  valkey-cli --raw -h 127.0.0.1 -p "$valkey_port" -n "$valkey_database" \
    DEL rateLimit:42 rateLimit:MRRLS:42 >/dev/null
 done

call_listener go missing-usage "$go_port" sk-relayprobe \
  '{"model":"gpt-test","input":"missing-usage"}'
call_listener rust missing-usage "$rust_port" sk-relayprobe \
  '{"model":"gpt-test","input":"missing-usage"}'
for engine in go rust; do
  [[ $(<"$runtime/$engine-missing-usage.status") == 200 ]] || {
    echo "$engine missing-usage status was $(<"$runtime/$engine-missing-usage.status")" >&2
    sed -n '1,80p' "$runtime/$engine-missing-usage.body" >&2
    exit 1
  }
  jq -e 'has("usage") | not' "$runtime/$engine-missing-usage.body" >/dev/null || {
    echo "$engine missing-usage fixture unexpectedly contained usage" >&2
    exit 1
  }
done
cmp -s "$runtime/go-missing-usage.body" "$runtime/rust-missing-usage.body" || {
  echo 'Go/Rust missing-usage response bodies differ' >&2
  diff -u "$runtime/go-missing-usage.body" "$runtime/rust-missing-usage.body" >&2 || true
  exit 1
}
jq -s -e '
  length == 18 and .[16] == .[17]
  and .[16].path == "/v1/embeddings"
  and .[16].authorization_valid == true
  and .[16].caller_secret_present == false
  and .[16].body == {model:"gpt-test",input:"missing-usage"}
' "$runtime/provider-hits.jsonl" >/dev/null

wait_for_minimum_log_count "$go_database" 5
wait_for_minimum_log_count "$rust_database" 5
snapshot_database "$go_database" "$runtime/go-after-missing-usage.snapshot.json"
snapshot_database "$rust_database" "$runtime/rust-after-missing-usage.snapshot.json"
if ! cmp -s "$runtime/go-after-missing-usage.snapshot.json" \
  "$runtime/rust-after-missing-usage.snapshot.json"; then
  echo 'Go/Rust PostgreSQL side effects for missing-usage fixed-price success differ:' >&2
  diff -u "$runtime/go-after-missing-usage.snapshot.json" \
    "$runtime/rust-after-missing-usage.snapshot.json" >&2 || true
  exit 1
fi
for engine in go rust; do
  jq -e '
    .user.quota == 995
    and .user.used_quota == 5
    and .user.request_count == 5
    and .token.remain_quota == 995
    and .token.used_quota == 5
    and .channel.used_quota == 5
    and .log_count == 5
    and ([.logs[] | select(.quota == 1 and .prompt_tokens == 0 and .completion_tokens == 0)] | length) == 1
  ' "$runtime/$engine-after-missing-usage.snapshot.json" >/dev/null || {
    echo "$engine missing-usage success did not retain the fixed-price charge" >&2
    jq . "$runtime/$engine-after-missing-usage.snapshot.json" >&2
    exit 1
  }
done

for database in "$go_database" "$rust_database"; do
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
INSERT INTO options (key,value) VALUES
  ('performance_setting.monitor_enabled','true'),
  ('performance_setting.monitor_cpu_threshold','0'),
  ('performance_setting.monitor_memory_threshold','1'),
  ('performance_setting.monitor_disk_threshold','0')
ON CONFLICT (key) DO UPDATE SET value=EXCLUDED.value;
SQL
done

# Current Go caches option state in-process. Restart both listeners so the
# enabled-monitor probe compares equivalent fresh processes and status caches.
stop_owned_process go_pid
stop_owned_process rust_pid
start_go
start_rust

for _ in {1..100}; do
  call_listener go performance-overload "$go_port" sk-relayprobe \
    '{"model":"gpt-test","input":"must-not-reach-provider"}'
  [[ $(<"$runtime/go-performance-overload.status") == 503 ]] && break
  sleep .05
done
call_listener rust performance-overload "$rust_port" sk-relayprobe \
  '{"model":"gpt-test","input":"must-not-reach-provider"}'
for engine in go rust; do
  [[ $(<"$runtime/$engine-performance-overload.status") == 503 ]] || {
    echo "$engine performance-overload status was $(<"$runtime/$engine-performance-overload.status")" >&2
    sed -n '1,80p' "$runtime/$engine-performance-overload.body" >&2
    exit 1
  }
  jq -e '
    .error.code == "system_memory_overloaded"
    and .error.type == "new_api_error"
    and .error.param == ""
    and (.error.message | test("^system memory overloaded \\(current: [0-9]+\\.[0-9]%, threshold: 1%\\)$"))
  ' "$runtime/$engine-performance-overload.body" >/dev/null || {
    echo "$engine performance-overload body has the wrong OpenAI error shape" >&2
    sed -n '1,80p' "$runtime/$engine-performance-overload.body" >&2
    exit 1
  }
done
normalize_performance_usage() {
  sed -E 's/current: [0-9]+\.[0-9]+%/current: CURRENT%/g' "$1"
}
if ! diff -u \
  <(normalize_performance_usage "$runtime/go-performance-overload.body") \
  <(normalize_performance_usage "$runtime/rust-performance-overload.body") >/dev/null; then
  echo 'Go/Rust performance-overload bodies differ' >&2
  diff -u \
    <(normalize_performance_usage "$runtime/go-performance-overload.body") \
    <(normalize_performance_usage "$runtime/rust-performance-overload.body") >&2 || true
  exit 1
fi
[[ $(wc -l <"$runtime/provider-hits.jsonl") == 18 ]] || {
  echo 'performance load shedding unexpectedly reached the provider' >&2
  exit 1
}
snapshot_database "$go_database" "$runtime/go-after-performance.snapshot.json"
snapshot_database "$rust_database" "$runtime/rust-after-performance.snapshot.json"
for engine in go rust; do
  cmp -s "$runtime/$engine-after-missing-usage.snapshot.json" \
    "$runtime/$engine-after-performance.snapshot.json" || {
    echo "$engine performance load shedding changed PostgreSQL accounting" >&2
    exit 1
  }
done

if [[ -n $result_dir ]]; then
  jq -cn \
    '{method:"POST",path:"/v1/embeddings",differential_verified:true,differential_scope:"relay-misc-pg",cases:18,provider_hits:18,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
    >"$result_dir/relay-misc-pg-embeddings.json"
fi

jq -cn \
  --arg result passed \
  --arg go_source "$go_root" \
  --argjson current_go_connection_named_header_leak "$go_connection_named_header_leak" \
  --argjson provider_hits 18 \
  '{test:"relay-misc-pg-listener-differential",result:$result,current_go_source:$go_source,provider_loopback_only:true,provider_hits:$provider_hits,http_status_body_and_safe_headers_equal:true,compressed_request_encodings_equal:["gzip","br","zstd"],malformed_compressed_request_equal:["gzip","br","zstd"],ordinary_paid_trust_level_2_discount_equal:true,status_code_mapping_429_to_503_equal:true,upstream_error_variants_equal:["string","message","openai","invalid-json"],upstream_error_header_boundary_equal:true,invalid_token_concealment_equal:true,model_request_rate_limit_equal:true,valkey_backed_model_request_rate_limit_equal:true,missing_usage_fixed_price_settlement_equal:true,system_performance_memory_overload_equal:true,current_go_connection_named_header_leak:$current_go_connection_named_header_leak,rust_connection_named_header_filtered:true,provider_requests_equal:true,postgres_side_effects_equal:true}'
