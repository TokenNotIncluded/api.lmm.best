#!/usr/bin/env bash
# Real loopback differential for GET /api/log/channel_affinity_usage_cache.
# Both listeners use disposable PostgreSQL/Valkey instances; production
# endpoints and credentials are never accepted by this runner.
set -euo pipefail
set +x

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || {
  echo 'LMM_GO_ORACLE_ROOT must be an external non-symlink directory' >&2
  exit 2
}
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in
  "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the repository' >&2; exit 2 ;;
esac

rust_binary=${LMM_OBSERVABILITY_RUST_BINARY:-"$repo_root/apps/api-rust/target/debug/lmm-api-rs"}
[[ $rust_binary == /* && -x $rust_binary && ! -L $rust_binary ]] || {
  echo 'LMM_OBSERVABILITY_RUST_BINARY must be an absolute executable non-symlink Rust binary' >&2
  exit 2
}

runtime=$(mktemp -d /tmp/lmm-observability-affinity.XXXXXX)
go_build=$(mktemp -d /dev/shm/lmm-observability-go.XXXXXX)
go_tmp=$(mktemp -d /dev/shm/lmm-observability-gotmp.XXXXXX)
pg_pid=''; go_pid=''; rust_pid=''; valkey_pid=''
cleanup() {
  local code=$?
  if (( code != 0 )); then
    echo "Valkey keys before (go): ${go_keys_before:-<unset>}" >&2
    echo "Valkey keys before (rust): ${rust_keys_before:-<unset>}" >&2
    if [[ -n ${valkey_pid:-} ]] && owned valkey_pid; then
      echo "Valkey keys after (go): $(valkey_keys "$valkey_port" 1 2>/dev/null || true)" >&2
      echo "Valkey keys after (rust): $(valkey_keys "$valkey_port" 2 2>/dev/null || true)" >&2
    fi
    for log_file in "$runtime"/*.log; do
      [[ -f $log_file ]] || continue
      echo "--- $log_file" >&2
      tail -80 "$log_file" >&2 || true
    done
    for response_file in "$runtime"/*.body; do
      [[ -f $response_file ]] || continue
      echo "--- $response_file" >&2
      jq -S . "$response_file" >&2 2>/dev/null || sed -n '1,120p' "$response_file" >&2
    done
  fi
  for pid in "$go_pid" "$rust_pid" "$valkey_pid"; do
    if [[ -n $pid ]] && kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$go_pid" "$rust_pid" "$valkey_pid" 2>/dev/null || true
  if [[ -n $pg_pid && -d $runtime/pg ]]; then
    pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  fi
  case "$runtime" in /tmp/lmm-observability-affinity.*) rm -rf -- "$runtime" ;; esac
  case "$go_build" in /dev/shm/lmm-observability-go.*) rm -rf -- "$go_build" ;; esac
  case "$go_tmp" in /dev/shm/lmm-observability-gotmp.*) rm -rf -- "$go_tmp" ;; esac
  exit "$code"
}
trap cleanup EXIT INT TERM

for command in createdb curl git go initdb jq pg_dump pg_ctl psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done

random_port() {
  local candidate
  while :; do
    candidate=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000))
    [[ -z $(ss -H -ltn "sport = :$candidate") ]] && { echo "$candidate"; return; }
  done
}
pg_port=$(random_port); go_port=$(random_port); rust_port=$(random_port); valkey_port=$(random_port)
go_database=observability_affinity_go; rust_database=observability_affinity_rust
go_database_url="postgresql://postgres@127.0.0.1:$pg_port/$go_database?sslmode=disable"
rust_database_url="postgresql://postgres@127.0.0.1:$pg_port/$rust_database"
go_valkey_url="redis://127.0.0.1:$valkey_port/1"
rust_valkey_url="redis://127.0.0.1:$valkey_port/2"
session_secret='ObservabilityAffinity-2026!SyntheticSessionSecret'
crypto_secret='ObservabilityAffinity-2026!SyntheticCryptoSecret'
dashboard_token='obs-root-pat-000000000000000001'
admin_token='obs-admin-pat-000000000000000002'

pid_start() { [[ -r /proc/$1/stat ]] && awk '{print $22}' "/proc/$1/stat"; }
record_pid() {
  local name=$1 pid=$2
  printf -v "$name" '%s' "$pid"
  printf -v "${name}_start" '%s' "$(pid_start "$pid")"
}
owned() {
  local name=$1
  local pid=${!name:-}
  local start="${name}_start"
  [[ -n $pid && -n ${!start:-} && $(pid_start "$pid" 2>/dev/null || true) == "${!start}" ]] && kill -0 "$pid" 2>/dev/null
}
listener_owned() {
  local port=$1 pid=$2 line
  line=$(ss -H -ltnp "sport = :$port" 2>/dev/null || true)
  [[ $line == *"pid=$pid,"* ]]
}
wait_listener() {
  local port=$1 pid_name=$2 path=$3 pid
  for _ in {1..300}; do
    pid=${!pid_name:-}
    if owned "$pid_name" && listener_owned "$port" "$pid" && curl --connect-timeout 2 --max-time 5 -fsS "http://127.0.0.1:$port$path" >/dev/null 2>&1; then return; fi
    sleep .05
  done
  return 1
}
valkey_keys() {
  valkey-cli -h 127.0.0.1 -p "$1" -n "$2" --scan | LC_ALL=C sort
}

initdb --username=postgres --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
record_pid pg_pid "$(head -n 1 "$runtime/pg/postmaster.pid")"
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" -U postgres "$rust_database"
valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no --dir "$runtime" >"$runtime/valkey.log" 2>&1 & record_pid valkey_pid "$!"
for _ in {1..200}; do
  if owned valkey_pid && listener_owned "$valkey_port" "$valkey_pid" && [[ $(valkey-cli -h 127.0.0.1 -p "$valkey_port" ping) == PONG ]]; then break; fi
  sleep .05
done

cp -a "$legacy_root/." "$go_build/source"
mkdir -p "$go_build/source/web/dist"
: >"$go_build/source/web/dist/index.html"
(cd "$go_build/source" && GOTOOLCHAIN=local CGO_ENABLED=1 GOTMPDIR="$go_tmp" go build -buildvcs=false -o "$go_build/legacy-go" .)

start_go() {
  env -i PATH="$PATH" HOME="$HOME" SQL_DSN="$go_database_url" PORT="$go_port" \
    REDIS_CONN_STRING="$go_valkey_url" SESSION_SECRET="$session_secret" CRYPTO_SECRET="$crypto_secret" \
    PASSWORD_LOGIN_ENABLED=false GLOBAL_API_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release \
    "$go_build/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
  wait_listener "$go_port" go_pid /api/status
}
start_go
for _ in {1..300}; do
  [[ $(psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$go_database" -qAt -c "SELECT to_regclass('public.users') IS NOT NULL") == t ]] && break
  sleep .05
done
kill "$go_pid" 2>/dev/null || true; wait "$go_pid" 2>/dev/null || true; go_pid=''

pg_dump -h 127.0.0.1 -p "$pg_port" -U postgres -d "$go_database" --schema-only --no-owner --no-privileges >"$runtime/go-schema.sql"
psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$rust_database" -v ON_ERROR_STOP=1 -f "$runtime/go-schema.sql" >/dev/null
sed 's/__LMM_APP_SCHEMA__/public/g' "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" >"$runtime/bounty.sql"
for database in "$go_database" "$rust_database"; do
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 -f "$runtime/bounty.sql" >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 -c 'CREATE TABLE IF NOT EXISTS lmm_schema_contract(singleton BOOLEAN PRIMARY KEY,min_reader_version BIGINT NOT NULL,max_reader_version BIGINT NOT NULL); INSERT INTO lmm_schema_contract VALUES(true,1,1) ON CONFLICT(singleton) DO NOTHING;' >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 -c 'ALTER TABLE users ADD COLUMN IF NOT EXISTS console_activated_at BIGINT NOT NULL DEFAULT 0; ALTER TABLE users ADD COLUMN IF NOT EXISTS access_token TEXT;' >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 -c "INSERT INTO users (id,username,password,role,status,access_token,auth_version,console_activated_at) VALUES (999,'observability-root','unused-password',100,1,'$dashboard_token',1,0),(1000,'observability-admin','unused-password',10,1,'$admin_token',1,0) ON CONFLICT(id) DO UPDATE SET username=EXCLUDED.username,role=EXCLUDED.role,status=EXCLUDED.status,access_token=EXCLUDED.access_token,auth_version=EXCLUDED.auth_version,console_activated_at=EXCLUDED.console_activated_at;" >/dev/null
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
INSERT INTO channels (id, name, key)
VALUES (1, 'east', 'observability-channel-east')
ON CONFLICT(id) DO UPDATE SET name = EXCLUDED.name, key = EXCLUDED.key;
INSERT INTO tokens (id, user_id, key, status, name)
VALUES
  (11, 999, 'observability', 1, 'primary'),
  (22, 1000, 'sk-observability-admin', 1, 'admin-token')
ON CONFLICT(id) DO UPDATE SET user_id = EXCLUDED.user_id, key = EXCLUDED.key, status = EXCLUDED.status, name = EXCLUDED.name, deleted_at = NULL;
INSERT INTO quota_data (id, user_id, username, node_name, token_id, use_group, channel_id, model_name, created_at, count, quota, token_used)
VALUES
  (9101, 999, 'observability-root', 'node-a', 11, 'default', 1, 'model-a', 1700000000, 2, 100, 40),
  (9102, 999, 'observability-root', 'node-a', 11, 'default', 1, 'model-a', 1700000000, 1, 50, 20),
  (9103, 1000, 'observability-admin', 'node-b', 22, 'vip', 1, 'model-b', 1700000000, 3, 70, 30),
  (9104, 999, 'observability-root', 'node-a', 0, '', 0, 'model-empty', 1700000000, 4, 5, 2),
  (9105, 999, 'observability-root', 'node-a', 11, 'default', 2, 'model-b', 1700000100, 1, 200, 80),
  (9106, 999, 'observability-root', 'node-a', 11, 'default', 1, 'model-c', 1700003600, 1, 300, 120)
ON CONFLICT(id) DO UPDATE SET user_id = EXCLUDED.user_id, username = EXCLUDED.username, node_name = EXCLUDED.node_name, token_id = EXCLUDED.token_id, use_group = EXCLUDED.use_group, channel_id = EXCLUDED.channel_id, model_name = EXCLUDED.model_name, created_at = EXCLUDED.created_at, count = EXCLUDED.count, quota = EXCLUDED.quota, token_used = EXCLUDED.token_used;
SQL
  psql -h 127.0.0.1 -p "$pg_port" -U postgres -d "$database" -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
INSERT INTO logs (id,user_id,created_at,type,content,username,token_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,channel_id,channel_name,token_id,"group",ip,request_id,upstream_request_id,other)
VALUES
  (9001,999,1700000100,2,'consume','observability-root','token-a','model-a',10,3,4,5,false,0,'',11,'default','127.0.0.1','req-a','up-a',$${"admin_info":"secret","stream_status":"active","keep":"yes"}$$),
  (9002,999,1700000000,4,'system','observability-root','','',0,0,0,0,false,0,'',0,'default','','req-b','up-b',$${"keep":"system"}$$),
  (9003,1000,1700000100,2,'other-user','other-user','token-b','model-b',20,5,6,7,false,0,'',12,'default','','req-c','up-c',$${"keep":"other"}$$),
  (9004,999,1700000200,2,'recent','observability-root','token-a','model-a',99,5,6,1,false,0,'',13,'default','','req-d','up-d',$${"keep":"recent"}$$),
  (9005,999,1700000050,4,'null-other','observability-root','','',0,0,0,0,false,0,'',0,'default','','req-e','up-e',NULL)
ON CONFLICT(id) DO UPDATE SET user_id=EXCLUDED.user_id,created_at=EXCLUDED.created_at,type=EXCLUDED.type,content=EXCLUDED.content,username=EXCLUDED.username,token_name=EXCLUDED.token_name,model_name=EXCLUDED.model_name,quota=EXCLUDED.quota,prompt_tokens=EXCLUDED.prompt_tokens,completion_tokens=EXCLUDED.completion_tokens,use_time=EXCLUDED.use_time,is_stream=EXCLUDED.is_stream,channel_id=EXCLUDED.channel_id,channel_name=EXCLUDED.channel_name,token_id=EXCLUDED.token_id,"group"=EXCLUDED."group",ip=EXCLUDED.ip,request_id=EXCLUDED.request_id,upstream_request_id=EXCLUDED.upstream_request_id,other=EXCLUDED.other;
SQL
done

cache_key=$'new-api:channel_affinity_usage_cache_stats:v1:rule-a\ndefault\nfp-a'
cache_value='{"cached_token_rate_mode":"cached_over_prompt","hit":3,"total":4,"window_seconds":3600,"prompt_tokens":100,"completion_tokens":40,"total_tokens":140,"cached_tokens":80,"prompt_cache_hit_tokens":70,"last_seen_at":1700000000}'
valkey-cli -h 127.0.0.1 -p "$valkey_port" -n 1 SET "$cache_key" "$cache_value" >/dev/null
valkey-cli -h 127.0.0.1 -p "$valkey_port" -n 2 SET "$cache_key" "$cache_value" >/dev/null

start_go
env -i PATH="$PATH" HOME="$HOME" LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=blue \
  DATABASE_URL="$rust_database_url" VALKEY_URL="$rust_valkey_url" LMM_SCHEMA_CONTRACT=1 \
  SESSION_SECRET="$session_secret" CRYPTO_SECRET="$crypto_secret" PASSWORD_LOGIN_ENABLED=false \
  GLOBAL_API_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none VERSION=v0.0.0 "$rust_binary" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"
wait_listener "$rust_port" rust_pid /readyz

request() {
  local engine=$1 port=$2 name=$3 path=$4 token=${5:-$dashboard_token} prefix="$runtime/$1-$3"
  curl --connect-timeout 2 --max-time 10 -sS -D "$prefix.headers" -o "$prefix.body" -w '%{http_code}' \
    -H "authorization: Bearer $token" -H 'accept: application/json' "http://127.0.0.1:$port$path" >"$prefix.status"
}
compare() {
  local name=$1 path=$2 expected=$3
  request go "$go_port" "$name" "$path"; request rust "$rust_port" "$name" "$path"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$name.status") == "$expected" ]] || return 1
    jq -S . "$runtime/$engine-$name.body" >"$runtime/$engine-$name.sorted"
    grep -qi '^content-type: application/json' "$runtime/$engine-$name.headers"
  done
  diff -u "$runtime/go-$name.sorted" "$runtime/rust-$name.sorted"
}
compare_as() {
  local name=$1 path=$2 expected=$3 token=$4
  request go "$go_port" "$name" "$path" "$token"; request rust "$rust_port" "$name" "$path" "$token"
  for engine in go rust; do
    [[ $(<"$runtime/$engine-$name.status") == "$expected" ]] || return 1
    jq -S . "$runtime/$engine-$name.body" >"$runtime/$engine-$name.sorted"
    grep -qi '^content-type: application/json' "$runtime/$engine-$name.headers"
  done
  diff -u "$runtime/go-$name.sorted" "$runtime/rust-$name.sorted"
}

# The Go auth middleware lazily creates per-user Valkey entries on the first
# authenticated request. Warm both listeners before taking the route-level
# side-effect snapshot so those auth entries are not attributed to this read.
compare warmup '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default&key_fp=fp-a' 200
compare_as warmup-admin '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default&key_fp=fp-a' 200 "$admin_token"
compare_as token-warmup '/api/log/token' 200 'sk-observability-primary'
compare warmup-self-logs '/api/log/self?p=1&page_size=10&start_timestamp=1700000000&end_timestamp=1700000100' 200
go_keys_before=$(valkey_keys "$valkey_port" 1); rust_keys_before=$(valkey_keys "$valkey_port" 2)
compare valid '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default&key_fp=fp-a' 200
compare missing-rule '/api/log/channel_affinity_usage_cache?using_group=default&key_fp=fp-a' 400
compare missing-key '/api/log/channel_affinity_usage_cache?rule_name=rule-a&using_group=default' 400
compare self-logs '/api/log/self?p=1&page_size=10&start_timestamp=1700000000&end_timestamp=1700000100' 200
compare self-log-stat '/api/log/self/stat?type=2&start_timestamp=1700000000&end_timestamp=1700000100' 200
compare data-all '/api/data/?start_timestamp=1700000000&end_timestamp=1700000100' 200
compare data-all-filter '/api/data/?start_timestamp=1700000000&end_timestamp=1700000100&username=observability-root' 200
compare data-users '/api/data/users?start_timestamp=1700000000&end_timestamp=1700000100' 200
compare data-self '/api/data/self?start_timestamp=1700000000&end_timestamp=1700000100' 200
compare data-flow-root '/api/data/flow?start_timestamp=1700000000&end_timestamp=1700000100&username=observability-root' 200
compare_as data-flow-admin '/api/data/flow?start_timestamp=1700000000&end_timestamp=1700000100' 200 "$admin_token"
compare data-flow-self '/api/data/flow/self?start_timestamp=1700000000&end_timestamp=1700000100' 200
compare data-flow-invalid '/api/data/flow?start_timestamp=bad&end_timestamp=1700000100' 200
compare data-flow-self-too-large '/api/data/flow/self?start_timestamp=1&end_timestamp=2592002' 200
compare all-logs-filter '/api/log/?p=1&page_size=10&type=2&start_timestamp=1700000000&end_timestamp=1700000100&username=observability-root&model_name=model-a&token_name=token-a&group=default&request_id=req-a&upstream_request_id=up-a' 200
compare all-logs-wildcard '/api/log/?p=1&page_size=10&username=observability-root&model_name=model-%&token_name=token-a&group=default' 200
compare self-logs-filter '/api/log/self?p=1&page_size=10&type=2&start_timestamp=1700000000&end_timestamp=1700000100&model_name=model-a&token_name=token-a&group=default&request_id=req-a&upstream_request_id=up-a' 200
compare log-stat-filter '/api/log/stat?type=4&start_timestamp=1700000000&end_timestamp=1700000100&username=observability-root&model_name=model-a&token_name=token-a&group=default' 200
compare self-log-stat-wildcard '/api/log/self/stat?type=4&start_timestamp=1700000000&end_timestamp=1700000100&model_name=model-%&token_name=token-a&group=default' 200
compare_as token-logs '/api/log/token?start_timestamp=1&p=99&page_size=1' 200 'sk-observability-primary'
jq -e '.success == true and .message == "" and .data.rule_name == "rule-a" and .data.using_group == "default" and .data.key_fp == "fp-a" and .data.hit == 3 and .data.total == 4 and .data.cached_token_rate_mode == "cached_over_prompt"' "$runtime/go-valid.body" >/dev/null
jq -e '.success == true and .data.quota == 10 and .data.rpm == 0 and .data.tpm == 0' "$runtime/go-self-log-stat.body" >/dev/null
[[ "$go_keys_before" == "$(valkey_keys "$valkey_port" 1)" && "$rust_keys_before" == "$(valkey_keys "$valkey_port" 2)" ]]

jq -cn --arg revision "$legacy_revision" '{test:"observability-affinity-listener-differential",real_tcp:true,production_access:false,legacy_go_revision:$revision,scenarios:24,routes:["GET /api/log/","GET /api/log/channel_affinity_usage_cache","GET /api/log/self","GET /api/log/self/stat","GET /api/log/stat","GET /api/log/token","GET /api/data/","GET /api/data/users","GET /api/data/self","GET /api/data/flow","GET /api/data/flow/self"],result:"passed"}'
