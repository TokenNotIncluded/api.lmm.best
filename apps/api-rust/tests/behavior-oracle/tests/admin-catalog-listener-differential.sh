#!/usr/bin/env bash
# Real loopback differential for the administrator catalog read surface.
# The runner owns every process, database, schema, and port it creates.
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
  "$repo_root"|"$repo_root"/*)
    echo 'LMM_GO_ORACLE_ROOT must be external to the repository' >&2
    exit 2
    ;;
esac

runtime=$(mktemp -d /tmp/lmm-admin-catalog-listener.XXXXXX)
go_pid='' rust_pid='' go_valkey_pid='' rust_valkey_pid=''
pg_port=$(shuf -i 20000-60000 -n 1)
go_port=$(shuf -i 20000-60000 -n 1)
rust_port=$(shuf -i 20000-60000 -n 1)
go_valkey_port=$(shuf -i 20000-60000 -n 1)
rust_valkey_port=$(shuf -i 20000-60000 -n 1)
go_valkey_password=$(openssl rand -hex 32)
rust_valkey_password=$(openssl rand -hex 32)
crypto_secret=$(openssl rand -hex 32)
session_secret='AdminCatalogListener-2026!SyntheticSecret'
go_db="$runtime/legacy.db"
rust_db=lmm_test_admin_catalog_rust
rust_role=lmm_test_admin_catalog_runtime
rust_schema=lmm_test_admin_catalog_schema
listener_mode=${LMM_ADMIN_CATALOG_LISTENER_MODE:-candidate}
case "$listener_mode" in
  candidate|normal) ;;
  *) echo "LMM_ADMIN_CATALOG_LISTENER_MODE must be candidate or normal" >&2; exit 2 ;;
esac
result_dir=${LMM_ADMIN_CATALOG_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo "LMM_ADMIN_CATALOG_RESULT_DIR must be an absolute path without '..'" >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi

pid_live() { [[ -n ${1:-} ]] && kill -0 "$1" 2>/dev/null; }
stop_pid() {
  local pid=${1:-}
  pid_live "$pid" && kill "$pid" 2>/dev/null || true
  [[ -n $pid ]] && wait "$pid" 2>/dev/null || true
}
cleanup() {
  local code=$?
  stop_pid "$go_pid"
  stop_pid "$rust_pid"
  stop_pid "$go_valkey_pid"
  stop_pid "$rust_valkey_pid"
  [[ -f "$runtime/pg/postmaster.pid" ]] && pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    /tmp/lmm-admin-catalog-listener.*)
      if [[ ${LMM_KEEP_ADMIN_CATALOG_RUNTIME:-0} == 1 ]]; then
        echo "retained admin-catalog runtime: $runtime" >&2
      else
        rm -rf -- "$runtime"
      fi
      ;;
  esac
  exit "$code"
}
trap cleanup EXIT INT TERM

for command in cargo createdb curl go initdb jq pg_ctl psql sqlite3 valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command is unavailable: $command" >&2; exit 1; }
done
for port in "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port"; do
  [[ -z $(ss -H -ltn "sport = :$port") ]] || { echo "occupied random port: $port" >&2; exit 1; }
done

cp -a "$legacy_root/." "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: >"$runtime/go-source/web/dist/index.html"
(cd "$runtime/go-source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .)

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$rust_db"

start_valkey() {
  local name=$1 port=$2 password=$3
  local config="$runtime/$name-valkey.conf"
  umask 077
  printf 'bind 127.0.0.1\nport %s\nprotected-mode yes\nrequirepass %s\nsave ""\nappendonly no\ndaemonize no\ndir %s\nlogfile %s\n' \
    "$port" "$password" "$runtime" "$runtime/$name-valkey.log" >"$config"
  valkey-server "$config" >"$runtime/$name-valkey.stdout" 2>&1 &
  local pid=$!
  if [[ $name == go ]]; then go_valkey_pid=$pid; else rust_valkey_pid=$pid; fi
  for _ in {1..200}; do
    if VALKEYCLI_AUTH="$password" valkey-cli -h 127.0.0.1 -p "$port" ping >/dev/null 2>&1; then return; fi
    sleep .05
  done
  echo "$name Valkey did not become ready" >&2
  exit 1
}
start_valkey go "$go_valkey_port" "$go_valkey_password"
start_valkey rust "$rust_valkey_port" "$rust_valkey_password"

go_env=(SQL_DSN=local SQLITE_PATH="$go_db?_busy_timeout=30000" PORT="$go_port"
  REDIS_CONN_STRING="redis://:$go_valkey_password@127.0.0.1:$go_valkey_port"
  CRYPTO_SECRET="$crypto_secret" SESSION_SECRET="$session_secret"
  PASSWORD_LOGIN_ENABLED=true SYNC_FREQUENCY=60 GLOBAL_API_RATE_LIMIT_ENABLE=false GIN_MODE=release)
env "${go_env[@]}" "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
go_pid=$!
for _ in {1..300}; do
  [[ -n $(ss -H -ltn "sport = :$go_port") ]] && break
  pid_live "$go_pid" || { tail -n 80 "$runtime/go.log" >&2; exit 1; }
  sleep .05
done
[[ -n $(ss -H -ltn "sport = :$go_port") ]] || { tail -n 80 "$runtime/go.log" >&2; exit 1; }
# A listening socket can precede completion of the frozen Go SQLite migration.
# Wait for the actual readiness response instead of turning a transient 503
# into a false admin-catalog parity failure.
for _ in {1..600}; do
  if curl -fsS --max-time 3 "http://127.0.0.1:$go_port/api/status" >/dev/null; then
    break
  fi
  pid_live "$go_pid" || { tail -n 80 "$runtime/go.log" >&2; exit 1; }
  sleep .05
done
curl -fsS --max-time 3 "http://127.0.0.1:$go_port/api/status" >/dev/null
kill "$go_pid"
wait "$go_pid" 2>/dev/null || true
go_pid=

# Seed the Go ORM-created schema. All timestamps are fixed so read responses
# can be compared after JSON key sorting.
sqlite3 "$go_db" <<'SQL'
INSERT OR REPLACE INTO options (key,value) VALUES
 ('SelfUseModeEnabled','false'),
 ('ModelRatio','{"fixture-model":1}'),
 ('UserUsableGroups','{"default":"default"}'),
 ('GroupRatio','{"default":1}'),
 ('payment_setting.compliance_confirmed','true'),
 ('payment_setting.compliance_terms_version','v1');
INSERT OR REPLACE INTO users (id,username,password,display_name,role,status,email,quota,"group",setting,auth_version)
 VALUES (42,'admin-catalog-root','$2a$10$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','Admin Catalog Root',100,1,'',0,'default','{}',1);
INSERT OR REPLACE INTO custom_oauth_providers (id,name,slug,icon,enabled)
 VALUES (51,'Fixture OAuth One','fixture-oauth-one','fixture-icon-one',1),
        (52,'Fixture OAuth Two','fixture-oauth-two','fixture-icon-two',1);
INSERT OR REPLACE INTO user_oauth_bindings (id,user_id,provider_id,provider_user_id,created_at)
 VALUES (61,42,51,'fixture-provider-user-one','2023-11-14 22:13:20'),
        (62,42,52,'fixture-provider-user-two','2023-11-14 22:13:21');
INSERT OR REPLACE INTO vendors (id,name,description,icon,status,created_time,updated_time)
 VALUES (11,'FixtureVendor','','',1,1700000000,1700000001);
INSERT OR REPLACE INTO models (id,model_name,description,icon,tags,vendor_id,endpoints,status,sync_official,created_time,updated_time,name_rule)
 VALUES (21,'fixture-model','','','',11,'',1,1,1700000000,1700000001,0),
        (22,'disabled-model','hidden','','',11,'',0,1,1700000002,1700000003,0);
INSERT OR REPLACE INTO channels (id,type,key,name,status,models,"group")
 VALUES (101,1,'fixture-key','Fixture Channel',1,'fixture-model','default');
INSERT OR REPLACE INTO abilities ("group",model,channel_id,enabled,priority,weight)
 VALUES ('default','fixture-model',101,1,10,10);
INSERT OR REPLACE INTO prefill_groups (id,name,type,items,description,created_time,updated_time)
 VALUES (31,'Fixture Group','model','["fixture-model"]','',1700000004,1700000005);
INSERT OR REPLACE INTO redemptions (id,user_id,key,status,name,quota,created_time,redeemed_time,used_user_id,expired_time)
 VALUES (41,42,'fixture-redemption-0000000000000',1,'Fixture',100,1700000006,0,0,0);
SQL

# Use the frozen PostgreSQL baseline, then isolate it behind a test-only role,
# schema contract, and search_path. The names satisfy the Rust validator.
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE ROLE $rust_role LOGIN;
CREATE SCHEMA $rust_schema AUTHORIZATION $rust_role;
SQL
sed "s/public\\./$rust_schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/baseline.sql"
# Expand only the disposable Rust schema after the frozen baseline.
sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" \
  "$repo_root/apps/api-rust/migrations/0018_subscription_amount_snapshots.sql" \
  >>"$runtime/baseline.sql"
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_db" -v ON_ERROR_STOP=1 -f "$runtime/baseline.sql" >/dev/null
rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_db?options=-csearch_path%3D$rust_schema"
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_db" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE TABLE $rust_schema.lmm_schema_contract (singleton BOOLEAN PRIMARY KEY,min_reader_version BIGINT NOT NULL,max_reader_version BIGINT NOT NULL);
INSERT INTO $rust_schema.lmm_schema_contract VALUES (TRUE,1,1);
GRANT USAGE,CREATE ON SCHEMA $rust_schema TO $rust_role;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA $rust_schema TO $rust_role;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA $rust_schema TO $rust_role;
SQL
# The normal Rust readiness contract includes the reviewed forward-only
# open-source-bounty relations even when this matrix exercises only catalog
# reads. Apply that contract inside the disposable schema so `/readyz` is a
# real dependency check rather than an unrelated fixture failure.
sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/open-source-bounty.sql"
PGOPTIONS="-c search_path=$rust_schema" psql -h 127.0.0.1 -p "$pg_port" -d "$rust_db" \
  -q -v ON_ERROR_STOP=1 -f "$runtime/open-source-bounty.sql" >/dev/null
PGOPTIONS="-c search_path=$rust_schema" psql -h 127.0.0.1 -p "$pg_port" -d "$rust_db" -v ON_ERROR_STOP=1 -c \
  "GRANT SELECT ON open_source_bounty_projects, open_source_bounty_challenges, open_source_bounty_ledgers, open_source_bounty_disputes, open_source_bounty_mcp_tokens, open_source_bounty_mcp_confirmations, open_source_bounty_mcp_operations, open_source_bounty_rest_operations TO $rust_role" \
  >/dev/null
psql "$rust_dsn" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
INSERT INTO options (key,value) VALUES
 ('SelfUseModeEnabled','false'),
 ('ModelRatio','{"fixture-model":1}'),
 ('UserUsableGroups','{"default":"default"}'),
 ('GroupRatio','{"default":1}'),
 ('payment_setting.compliance_confirmed','true'),
 ('payment_setting.compliance_terms_version','v1');
INSERT INTO users (id,username,password,display_name,role,status,email,quota,"group",setting,auth_version)
 VALUES (42,'admin-catalog-root','$2a$10$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','Admin Catalog Root',100,1,'',0,'default','{}',1);
INSERT INTO custom_oauth_providers (id,name,slug,icon,enabled)
 VALUES (51,'Fixture OAuth One','fixture-oauth-one','fixture-icon-one',TRUE),
        (52,'Fixture OAuth Two','fixture-oauth-two','fixture-icon-two',TRUE);
INSERT INTO user_oauth_bindings (id,user_id,provider_id,provider_user_id,created_at)
 VALUES (61,42,51,'fixture-provider-user-one',to_timestamp(1700000000)),
        (62,42,52,'fixture-provider-user-two',to_timestamp(1700000001));
INSERT INTO vendors (id,name,description,icon,status,created_time,updated_time)
 VALUES (11,'FixtureVendor','','',1,1700000000,1700000001);
INSERT INTO models (id,model_name,description,icon,tags,vendor_id,endpoints,status,sync_official,created_time,updated_time,name_rule)
 VALUES (21,'fixture-model','','','',11,'',1,1,1700000000,1700000001,0),
        (22,'disabled-model','hidden','','',11,'',0,1,1700000002,1700000003,0);
INSERT INTO channels (id,type,key,name,status,models,"group")
 VALUES (101,1,'fixture-key','Fixture Channel',1,'fixture-model','default');
INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight)
 VALUES ('default','fixture-model',101,TRUE,10,10);
INSERT INTO prefill_groups (id,name,type,items,description,created_time,updated_time)
 VALUES (31,'Fixture Group','model','["fixture-model"]','',1700000004,1700000005);
INSERT INTO redemptions (id,user_id,key,status,name,quota,created_time,redeemed_time,used_user_id,expired_time)
 VALUES (41,42,'fixture-redemption-0000000000000',1,'Fixture',100,1700000006,0,0,0);
SQL

rust_env=(DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port"
  SESSION_SECRET="$session_secret" CRYPTO_SECRET="$crypto_secret" PASSWORD_LOGIN_ENABLED=true
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_SCHEMA_CONTRACT=1 VERSION=v0.0.0
  GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false AUTH_COOKIE_SECURE=false)
if [[ $listener_mode == candidate ]]; then
  rust_env+=(LMM_RS_TEST_INSTANCE=1 LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" LMM_RS_SLOT=single)
else
  # Exercise the normal blue/green assembly.  Local acceptance is loopback-only
  # and only replaces paid-activation/provider adapters; it does not use the
  # isolated candidate router.
  rust_env+=(LMM_RS_SLOT=blue LMM_LOCAL_ACCEPTANCE=true)
fi
env "${rust_env[@]}" "$repo_root/apps/api-rust/target/debug/lmm-api-rs" >"$runtime/rust.log" 2>&1 &
rust_pid=$!
for _ in {1..300}; do
  [[ -n $(ss -H -ltn "sport = :$rust_port") ]] && break
  pid_live "$rust_pid" || { tail -n 120 "$runtime/rust.log" >&2; exit 1; }
  sleep .05
done
[[ -n $(ss -H -ltn "sport = :$rust_port") ]] || { tail -n 120 "$runtime/rust.log" >&2; exit 1; }
curl -fsS --max-time 3 "http://127.0.0.1:$rust_port/readyz" >/dev/null

env "${go_env[@]}" "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
go_pid=$!
for _ in {1..300}; do
  [[ -n $(ss -H -ltn "sport = :$go_port") ]] && break
  pid_live "$go_pid" || { tail -n 120 "$runtime/go.log" >&2; exit 1; }
  sleep .05
done
[[ -n $(ss -H -ltn "sport = :$go_port") ]] || { tail -n 120 "$runtime/go.log" >&2; exit 1; }

capture() {
  local name=$1 base=$2 method=$3 path=$4 token=${5:-}
  local auth=()
  [[ -n $token ]] && auth=(-H "authorization: Bearer $token")
  curl -sS --max-time 5 -X "$method" -H 'accept: application/json' "${auth[@]}" \
    -D "$runtime/$name.headers" -o "$runtime/$name.body" -w '%{http_code}' \
    "$base$path" >"$runtime/$name.status"
}
capture_body() {
  local name=$1 base=$2 method=$3 path=$4 body=$5 token=${6:-}
  local auth=()
  [[ -n $token ]] && auth=(-H "authorization: Bearer $token")
  curl -sS --max-time 5 -X "$method" -H 'accept: application/json' -H 'content-type: application/json' "${auth[@]}" \
    --data-binary "$body" -D "$runtime/$name.headers" -o "$runtime/$name.body" -w '%{http_code}' \
    "$base$path" >"$runtime/$name.status"
}
login() {
  local name=$1 base=$2
  curl -sS --max-time 5 -X POST -H 'content-type: application/json' \
    --data-binary '{"username":"admin-catalog-root","password":"password"}' \
    -o "$runtime/$name.login.json" -w '%{http_code}' "$base/api/user/login" >"$runtime/$name.login.status"
  [[ $(<"$runtime/$name.login.status") == 200 ]]
  jq -er '.data.access_token' "$runtime/$name.login.json"
}
canonical() { jq -S . "$1"; }
canonical_write() {
  # Timestamps and generated redemption keys are intentionally dynamic.  The
  # durable fields, envelope, status, and generated-item cardinality remain
  # strict differential assertions.
  jq -S 'walk(if type == "object" then del(.created_time, .updated_time, .redeemed_time, .key) else . end) | if (.data|type) == "array" then .data |= map(if type == "string" and length == 32 then "<generated-key>" else . end) else . end' "$1"
}
pair() {
  local name=$1 method=$2 path=$3
  capture "go-$name" "http://127.0.0.1:$go_port" "$method" "$path" "$go_token"
  capture "rust-$name" "http://127.0.0.1:$rust_port" "$method" "$path" "$rust_token"
  diff -u "$runtime/go-$name.status" "$runtime/rust-$name.status"
  canonical "$runtime/go-$name.body" >"$runtime/go-$name.json"
  canonical "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  cases=$((cases + 1))
  record_route_case "$method" "$path"
}
pair_body() {
  local name=$1 method=$2 path=$3 body=$4
  capture_body "go-$name" "http://127.0.0.1:$go_port" "$method" "$path" "$body" "$go_token"
  capture_body "rust-$name" "http://127.0.0.1:$rust_port" "$method" "$path" "$body" "$rust_token"
  diff -u "$runtime/go-$name.status" "$runtime/rust-$name.status"
  canonical_write "$runtime/go-$name.body" >"$runtime/go-$name.json"
  canonical_write "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  cases=$((cases + 1))
  record_route_case "$method" "$path"
}
pair_write() {
  local name=$1 method=$2 path=$3
  capture "go-$name" "http://127.0.0.1:$go_port" "$method" "$path" "$go_token"
  capture "rust-$name" "http://127.0.0.1:$rust_port" "$method" "$path" "$rust_token"
  diff -u "$runtime/go-$name.status" "$runtime/rust-$name.status"
  canonical_write "$runtime/go-$name.body" >"$runtime/go-$name.json"
  canonical_write "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  cases=$((cases + 1))
  record_route_case "$method" "$path"
}

audit_differential=0
binding_differential=0
audit_actions() {
  local side=$1
  if [[ $side == go ]]; then
    sqlite3 "$go_db" "SELECT json_extract(other, '\$.op.action') || ':' || COUNT(*) FROM logs WHERE type=3 GROUP BY json_extract(other, '\$.op.action') ORDER BY json_extract(other, '\$.op.action');"
  else
    psql "$rust_dsn" -At -c "SELECT other::json->'op'->>'action' || ':' || COUNT(*) FROM logs WHERE type=3 GROUP BY other::json->'op'->>'action' ORDER BY other::json->'op'->>'action';"
  fi
}

go_token=$(login go "http://127.0.0.1:$go_port")
rust_token=$(login rust "http://127.0.0.1:$rust_port")
cases=0
declare -A route_case_counts=()
admin_route_identity() {
  local method=$1 path=$2
  path=${path%%\?*}
  case "$method $path" in
    'GET /api/models/'|'POST /api/models/'|'PUT /api/models/') printf '%s %s\n' "$method" "$path" ;;
    'GET /api/vendors/'|'POST /api/vendors/'|'PUT /api/vendors/') printf '%s %s\n' "$method" "$path" ;;
    'GET /api/prefill_group/'|'POST /api/prefill_group/'|'PUT /api/prefill_group/') printf '%s %s\n' "$method" "$path" ;;
    'GET /api/redemption/'|'POST /api/redemption/'|'PUT /api/redemption/') printf '%s %s\n' "$method" "$path" ;;
    'GET /api/models/'[0-9]*) printf '%s /api/models/:id\n' "$method" ;;
    'GET /api/vendors/'[0-9]*) printf '%s /api/vendors/:id\n' "$method" ;;
    'GET /api/redemption/'[0-9]*) printf '%s /api/redemption/:id\n' "$method" ;;
    'DELETE /api/models/'[0-9]*) printf '%s /api/models/:id\n' "$method" ;;
    'DELETE /api/vendors/'[0-9]*) printf '%s /api/vendors/:id\n' "$method" ;;
    'DELETE /api/prefill_group/'[0-9]*) printf '%s /api/prefill_group/:id\n' "$method" ;;
    'DELETE /api/redemption/'[0-9]*) printf '%s /api/redemption/:id\n' "$method" ;;
    'GET /api/user/'[0-9]*'/oauth/bindings') printf '%s /api/user/:id/oauth/bindings\n' "$method" ;;
    'DELETE /api/user/'[0-9]*'/oauth/bindings/'*) printf '%s /api/user/:id/oauth/bindings/:provider_id\n' "$method" ;;
    'DELETE /api/user/oauth/bindings/'*) printf '%s /api/user/oauth/bindings/:provider_id\n' "$method" ;;
    *) printf '%s %s\n' "$method" "$path" ;;
  esac
}
record_route_case() {
  local method=$1 path=$2 key
  local route
  route=$(admin_route_identity "$method" "$path")
  method=${route%% *}
  path=${route#* }
  key="${method}"$'\t'"${path}"
  route_case_counts["$key"]=$(( ${route_case_counts["$key"]:-0} + 1 ))
}
# Authentication must precede body/query parsing on the complete admin read family.
for entry in \
  'GET|/api/models/' 'GET|/api/models/search?keyword=fixture' 'GET|/api/models/21' 'GET|/api/models/missing' \
  'GET|/api/vendors/' 'GET|/api/vendors/search?keyword=Fixture' 'GET|/api/vendors/11' \
  'GET|/api/prefill_group/' 'GET|/api/prefill_group/?type=model' \
  'GET|/api/redemption/' 'GET|/api/redemption/search?status=1' 'GET|/api/redemption/41'; do
  IFS='|' read -r method path <<<"$entry"
  name="${method,,}-$(echo "$path" | tr '/?=&' '____' | tr -cd '[:alnum:]_-')"
  pair "$name" "$method" "$path"
done

if [[ ${LMM_ADMIN_CATALOG_BINDING_DIFFERENTIAL:-0} == 1 ]]; then
  # Custom-OAuth binding list/unbind is a local PostgreSQL/session boundary;
  # it must not be held behind the external provider adapter used by the
  # callback/login routes.
  pair binding-self GET /api/user/oauth/bindings
  pair binding-admin GET /api/user/42/oauth/bindings
  pair binding-self-delete DELETE /api/user/oauth/bindings/51
  pair binding-admin-delete DELETE /api/user/42/oauth/bindings/52
  pair binding-self-after-delete GET /api/user/oauth/bindings
  pair binding-admin-after-delete GET /api/user/42/oauth/bindings
  pair binding-self-invalid DELETE /api/user/oauth/bindings/not-an-id
  pair binding-admin-invalid DELETE /api/user/42/oauth/bindings/not-an-id
  binding_differential=1
fi

if [[ ${LMM_ADMIN_CATALOG_NEGATIVE_DIFFERENTIAL:-0} == 1 ]]; then
  # Duplicate-name checks are part of the Go controller contract, not merely
  # database uniqueness failures; compare the exact localized business
  # envelopes before any write-side sequence is advanced.
  pair_body duplicate-vendor POST /api/vendors/ '{"name":"FixtureVendor","description":"","icon":"","status":1}'
  pair_body duplicate-model POST /api/models/ '{"model_name":"fixture-model","description":"","icon":"","tags":"","vendor_id":11,"endpoints":"","status":1,"sync_official":1,"name_rule":0}'
  pair_body duplicate-prefill POST /api/prefill_group/ '{"name":"Fixture Group","type":"model","items":["fixture-model"],"description":""}'
fi

if [[ ${LMM_ADMIN_CATALOG_WRITE_DIFFERENTIAL:-0} == 1 ]]; then
  # Keep the PostgreSQL and SQLite sequences aligned with the explicit fixture
  # ids so subsequent resource paths exercise the same id on both listeners.
  psql "$rust_dsn" -v ON_ERROR_STOP=1 <<'SQL' >/dev/null
SELECT setval(pg_get_serial_sequence('models','id'), COALESCE((SELECT MAX(id) FROM models), 1), true);
SELECT setval(pg_get_serial_sequence('vendors','id'), COALESCE((SELECT MAX(id) FROM vendors), 1), true);
SELECT setval(pg_get_serial_sequence('prefill_groups','id'), COALESCE((SELECT MAX(id) FROM prefill_groups), 1), true);
SELECT setval(pg_get_serial_sequence('redemptions','id'), COALESCE((SELECT MAX(id) FROM redemptions), 1), true);
SQL
  pair_body create-vendor POST /api/vendors/ '{"name":"CreatedVendor","description":"created","icon":"icon","status":1}'
  pair_body update-vendor PUT /api/vendors/ '{"id":12,"name":"UpdatedVendor","description":"updated","icon":"icon-2","status":0}'
  pair_body create-model POST /api/models/ '{"model_name":"created-model","description":"created","icon":"model-icon","tags":"tag","vendor_id":11,"endpoints":"[]","status":1,"sync_official":1,"name_rule":0}'
  pair_body update-model PUT /api/models/ '{"id":23,"model_name":"updated-model","description":"updated","icon":"model-icon-2","tags":"tag-2","vendor_id":11,"endpoints":"[]","status":0,"sync_official":0,"name_rule":0}'
  pair_body create-prefill POST /api/prefill_group/ '{"name":"CreatedGroup","type":"model","items":["created-model"],"description":"created"}'
  pair_body update-prefill PUT /api/prefill_group/ '{"id":32,"name":"UpdatedGroup","type":"model","items":["updated-model"],"description":"updated"}'
  pair_body create-redemption POST /api/redemption/ '{"name":"Created Redemption","count":2,"quota":123,"expired_time":0}'
  pair_body update-redemption PUT '/api/redemption/?status_only=true' '{"id":42,"status":0}'
  pair_body delete-model DELETE /api/models/23 ''
  pair_body delete-vendor DELETE /api/vendors/12 ''
  pair_body delete-prefill DELETE /api/prefill_group/32 ''
  pair_body delete-redemption DELETE /api/redemption/42 ''
  pair_body delete-invalid DELETE /api/redemption/invalid ''
  # Verify durable state through the same list endpoints after the writes and
  # assert that each soft-delete is reflected identically.
  pair_write post-write-models GET '/api/models/?status=0'
  pair_write post-write-vendors GET '/api/vendors/?p=1&page_size=20'
  pair_write post-write-prefill GET '/api/prefill_group/?type=model'
  pair_write post-write-redemptions GET '/api/redemption/?p=1&page_size=20'
fi

if [[ ${LMM_ADMIN_CATALOG_AUDIT_DIFFERENTIAL:-0} == 1 ]]; then
  # Go writes operation audits from a goroutine. Wait for the expected count
  # before comparing action identities, then compare the authoritative rows on
  # each isolated database.  Audit failures are deliberately not allowed to
  # alter the HTTP response, but they are still part of takeover parity.
  expected_audits=0
  [[ ${LMM_ADMIN_CATALOG_NEGATIVE_DIFFERENTIAL:-0} == 1 ]] && expected_audits=$((expected_audits + 3))
  [[ ${LMM_ADMIN_CATALOG_WRITE_DIFFERENTIAL:-0} == 1 ]] && expected_audits=$((expected_audits + 13))
  # Go's AdminAuth middleware audits both administrator binding deletes (the
  # valid fixture delete and the invalid-provider negative case); the
  # self-delete route is not in the middleware action map and contributes no
  # row.
  [[ ${LMM_ADMIN_CATALOG_BINDING_DIFFERENTIAL:-0} == 1 ]] && expected_audits=$((expected_audits + 2))
  if (( expected_audits > 0 )); then
    for _ in {1..200}; do
      go_audit_count=$(sqlite3 "$go_db" 'SELECT COUNT(*) FROM logs WHERE type=3;')
      rust_audit_count=$(psql "$rust_dsn" -At -c 'SELECT COUNT(*) FROM logs WHERE type=3;')
      [[ $go_audit_count == "$expected_audits" && $rust_audit_count == "$expected_audits" ]] && break
      sleep .05
    done
    [[ $go_audit_count == "$expected_audits" ]] || {
      echo "Go audit count mismatch: expected $expected_audits, got $go_audit_count" >&2
      exit 1
    }
    [[ $rust_audit_count == "$expected_audits" ]] || {
      echo "Rust audit count mismatch: expected $expected_audits, got $rust_audit_count" >&2
      exit 1
    }
    audit_actions go >"$runtime/go-audit-actions"
    audit_actions rust >"$runtime/rust-audit-actions"
    diff -u "$runtime/go-audit-actions" "$runtime/rust-audit-actions"
    audit_differential=1
  fi
fi

if [[ -n $result_dir ]]; then
  route_index=0
  for key in "${!route_case_counts[@]}"; do
    method=${key%%$'\t'*}
    path=${key#*$'\t'}
    route_cases=${route_case_counts["$key"]}
    route_index=$((route_index + 1))
    jq -cn --arg method "$method" --arg path "$path" --arg mode "$listener_mode" \
      --argjson cases "$route_cases" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"admin-catalog",cases:$cases,listener_mode:$mode,production_access:($mode == "normal"),approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$result_dir/admin-catalog-$route_index.json"
  done
fi

jq -cn --argjson cases "$cases" --arg legacy_revision "$legacy_revision" --arg mode "$listener_mode" --arg writes "${LMM_ADMIN_CATALOG_WRITE_DIFFERENTIAL:-0}" --arg negative "${LMM_ADMIN_CATALOG_NEGATIVE_DIFFERENTIAL:-0}" \
  --argjson audit "$audit_differential" --argjson bindings "$binding_differential" \
  '{test:"admin-catalog-listener-differential",mode:"isolated-tcp",listener_mode:$mode,real_tcp:true,production_access:($mode == "normal"),write_differential:($writes == "1"),negative_differential:($negative == "1"),audit_differential:($audit == 1),binding_differential:($bindings == 1),cases:$cases,legacy_go_revision:$legacy_revision,routes:["GET /api/models/","POST /api/models/","PUT /api/models/","DELETE /api/models/:id","GET /api/models/:id","GET /api/models/missing","GET /api/models/search","GET /api/vendors/","POST /api/vendors/","PUT /api/vendors/","DELETE /api/vendors/:id","GET /api/vendors/:id","GET /api/vendors/search","GET /api/prefill_group/","POST /api/prefill_group/","PUT /api/prefill_group/","DELETE /api/prefill_group/:id","GET /api/redemption/","POST /api/redemption/","PUT /api/redemption/","DELETE /api/redemption/:id","GET /api/redemption/:id","DELETE /api/redemption/invalid","GET /api/redemption/search","GET /api/user/oauth/bindings","GET /api/user/:id/oauth/bindings","DELETE /api/user/oauth/bindings/:provider_id","DELETE /api/user/:id/oauth/bindings/:provider_id"],result:"passed"}'
