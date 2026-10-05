#!/usr/bin/env bash
# Real loopback Go/Rust differential for the subscription/profile setting
# surfaces. The runner owns two disposable PostgreSQL databases, two Valkey
# instances, and both listeners. It never accepts production endpoints or
# credentials.
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

rust_binary=${LMM_SUBSCRIPTION_RUST_BINARY:-"$repo_root/apps/api-rust/target/debug/lmm-api-rs"}
[[ $rust_binary == /* && -x $rust_binary && ! -L $rust_binary ]] || {
  echo 'LMM_SUBSCRIPTION_RUST_BINARY must be an absolute executable non-symlink Rust binary' >&2
  exit 2
}

runtime=$(mktemp -d /tmp/lmm-subscription-read.XXXXXX)
go_build=$(mktemp -d /dev/shm/lmm-subscription-go.XXXXXX)
go_tmp=$(mktemp -d /dev/shm/lmm-subscription-gotmp.XXXXXX)
pg_pid=''; go_pid=''; rust_pid=''; go_valkey_pid=''; rust_valkey_pid=''
cleanup() {
  local code=$?
  if (( code != 0 )); then
    for log_file in "$runtime"/*.log; do
      [[ -f $log_file ]] || continue
      echo "--- $log_file" >&2
      tail -80 "$log_file" >&2 || true
    done
  fi
  for pid in "$go_pid" "$rust_pid" "$go_valkey_pid" "$rust_valkey_pid"; do
    if [[ -n $pid ]] && kill -0 "$pid" 2>/dev/null; then kill "$pid" 2>/dev/null || true; fi
  done
  wait "$go_pid" "$rust_pid" "$go_valkey_pid" "$rust_valkey_pid" 2>/dev/null || true
  if [[ -n $pg_pid && -d $runtime/pg ]]; then pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true; fi
  case "$runtime" in /tmp/lmm-subscription-read.*) rm -rf -- "$runtime" ;; esac
  case "$go_build" in /dev/shm/lmm-subscription-go.*) rm -rf -- "$go_build" ;; esac
  case "$go_tmp" in /dev/shm/lmm-subscription-gotmp.*) rm -rf -- "$go_tmp" ;; esac
  exit "$code"
}
trap cleanup EXIT INT TERM

for command in cargo createdb curl git go initdb jq pg_ctl postgres psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done
[[ $(postgres --version) == *'PostgreSQL) 18.'* ]] || { echo 'requires PostgreSQL 18' >&2; exit 1; }

random_port() {
  local candidate
  while :; do
    candidate=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000))
    [[ -z $(ss -H -ltn "sport = :$candidate") ]] && { echo "$candidate"; return; }
  done
}
pg_port=$(random_port); go_port=$(random_port); rust_port=$(random_port)
go_valkey_port=$(random_port); rust_valkey_port=$(random_port)
pg_role=lmm_test_subscription_runtime
go_database=lmm_test_subscription_go; rust_database=lmm_test_subscription_rust
rust_schema=lmm_test_subscription_rust
go_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
rust_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
session_secret='SubscriptionRead-2026!SyntheticOnly'
crypto_secret='SubscriptionRead-Crypto-2026!SyntheticOnly'

pid_start() { [[ -r /proc/$1/stat ]] && awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local name=$1 pid=$2; printf -v "$name" '%s' "$pid"; printf -v "${name}_start" '%s' "$(pid_start "$pid")"; }
owned() {
  local name=$1
  local pid=${!name:-}
  local start="${name}_start"
  [[ -n $pid && -n ${!start:-} && $(pid_start "$pid" 2>/dev/null || true) == "${!start}" ]] && kill -0 "$pid" 2>/dev/null
}
listener_owned() { local port=$1 pid=$2 line; line=$(ss -H -ltnp "sport = :$port" 2>/dev/null || true); [[ $line == *"pid=$pid,"* ]]; }
wait_listener() {
  local port=$1 pid_name=$2 path=$3 pid
  for _ in {1..300}; do
    pid=${!pid_name:-}
    if owned "$pid_name" && listener_owned "$port" "$pid" && curl --connect-timeout 2 --max-time 5 -fsS "http://127.0.0.1:$port$path" >/dev/null 2>&1; then return; fi
    sleep .05
  done
  return 1
}
start_valkey() {
  local name=$1 port=$2 secret=$3 pid_name=$4 config="$runtime/$1-valkey.conf"
  (umask 077; printf 'bind 127.0.0.1\nport %s\nprotected-mode yes\nrequirepass %s\nsave ""\nappendonly no\ndaemonize no\ndir %s\n' "$port" "$secret" "$runtime" >"$config")
  valkey-server "$config" >"$runtime/$name-valkey.log" 2>&1 & record_pid "$pid_name" "$!"
  for _ in {1..200}; do
    if owned "$pid_name" && listener_owned "$port" "${!pid_name}" && VALKEYCLI_AUTH="$secret" valkey-cli -h 127.0.0.1 -p "$port" ping >/dev/null 2>&1; then return; fi
    sleep .05
  done
  return 1
}
sql() {
  if [[ $1 == "$rust_database" ]]; then
    PGOPTIONS="-c search_path=$rust_schema" psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$1" -qAt -v ON_ERROR_STOP=1 -c "$2"
  else
    psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$1" -qAt -v ON_ERROR_STOP=1 -c "$2"
  fi
}
seed() {
  local database=$1
  sql "$database" "INSERT INTO users (id,username,password,display_name,role,status,\"group\",setting,auth_version,quota) VALUES (1,'root','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','root',10,1,'default','{\"billing_preference\":\"wallet_first\"}',1,100000000),(2,'user','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','user',1,1,'default','{}',1,100000000) ON CONFLICT (id) DO UPDATE SET setting=EXCLUDED.setting"
  sql "$database" "INSERT INTO options(key,value) VALUES ('payment_setting.compliance_confirmed','true'),('payment_setting.compliance_terms_version','v1') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value"
  sql "$database" "INSERT INTO subscription_plans (id,title,subtitle,price_amount,currency,duration_unit,duration_value,custom_seconds,enabled,sort_order,allow_balance_pay,allow_wallet_overflow,stripe_price_id,creem_product_id,waffo_pancake_product_id,max_purchase_per_user,total_amount,upgrade_group,downgrade_group,quota_reset_period,quota_reset_custom_seconds,created_at,updated_at) VALUES (1,'Monthly','one month',12.5,'USD','month',1,0,true,20,true,true,'stripe-1','','',0,1000,'paid','default','monthly',0,1700000000,1700000000),(2,'Hidden','disabled',99,'USD','month',1,0,false,10,true,true,'','','',0,0,'','','never',0,1700000001,1700000001) ON CONFLICT(id) DO NOTHING"
  sql "$database" "INSERT INTO user_subscriptions (id,user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES (1,1,1,1000,125,1700000000,4102444800,'active','order',1700000000,0,'paid','default','default',true,1700000000,1700000000),(2,1,1,1000,1000,1600000000,1600003600,'expired','order',1600000000,0,'paid','default','default',true,1600000000,1600000000) ON CONFLICT(id) DO NOTHING"
}
snapshot() {
  local database=$1
  sql "$database" "SELECT jsonb_build_object('options',COALESCE((SELECT jsonb_agg(to_jsonb(x) ORDER BY key) FROM (SELECT key,value FROM options WHERE key LIKE 'payment_setting.%') x),'[]'::jsonb),'plans',COALESCE((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM (SELECT id,title,enabled,sort_order FROM subscription_plans) x),'[]'::jsonb),'subscriptions',COALESCE((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM (SELECT id,user_id,plan_id,status,amount_used FROM user_subscriptions) x),'[]'::jsonb),'users',COALESCE((SELECT jsonb_agg(to_jsonb(x) ORDER BY id) FROM (SELECT id,setting FROM users WHERE id IN (1,2)) x),'[]'::jsonb))" | jq -S .
}
valkey_keys() { VALKEYCLI_AUTH="$2" valkey-cli -h 127.0.0.1 -p "$1" --scan | LC_ALL=C sort; }
valkey_user_hash() {
  VALKEYCLI_AUTH="$2" valkey-cli -h 127.0.0.1 -p "$1" --raw HGETALL user:2 |
    paste - - | LC_ALL=C sort
}
seed_user_cache() {
  local port=$1 secret=$2
  VALKEYCLI_AUTH="$secret" valkey-cli -h 127.0.0.1 -p "$port" HSET user:2 \
    Id 2 Group default Email '' Quota 100000000 Status 1 Role 1 Username user \
    Setting '{}' AuthVersion 1 CacheSchema 2 >/dev/null
  VALKEYCLI_AUTH="$secret" valkey-cli -h 127.0.0.1 -p "$port" EXPIRE user:2 60 >/dev/null
}

cp -a "$legacy_root/." "$go_build/source"
mkdir -p "$go_build/source/web/dist"
: >"$go_build/source/web/dist/index.html"
(cd "$go_build/source" && GOTOOLCHAIN=local CGO_ENABLED=1 GOTMPDIR="$go_tmp" go build -buildvcs=false -o "$go_build/legacy-go" .)
initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
record_pid pg_pid "$(head -n 1 "$runtime/pg/postmaster.pid")"
psql -h 127.0.0.1 -p "$pg_port" -d postgres -v ON_ERROR_STOP=1 -c "CREATE ROLE $pg_role LOGIN SUPERUSER" >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" -U "$pg_role" "$go_database"
createdb -h 127.0.0.1 -p "$pg_port" -U "$pg_role" "$rust_database"
start_valkey go "$go_valkey_port" "$go_secret" go_valkey_pid
start_valkey rust "$rust_valkey_port" "$rust_secret" rust_valkey_pid

go_dsn="postgresql://$pg_role@127.0.0.1:$pg_port/$go_database?sslmode=disable"
rust_dsn="postgresql://$pg_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path%3D$rust_schema"
start_go() {
  SQL_DSN="$go_dsn" REDIS_CONN_STRING="redis://:$go_secret@127.0.0.1:$go_valkey_port" SESSION_SECRET="$session_secret" CRYPTO_SECRET="$crypto_secret" PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release PORT="$go_port" "$go_build/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!"
  wait_listener "$go_port" go_pid /api/status
}
start_go
for _ in {1..300}; do [[ $(sql "$go_database" "SELECT to_regclass('public.users') IS NOT NULL") == t ]] && break; sleep .05; done

psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$rust_database" -v ON_ERROR_STOP=1 -c "CREATE SCHEMA $rust_schema" >/dev/null
sed "s/public\\./$rust_schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/rust-baseline.sql"
# Expand only the disposable Rust schema after the frozen baseline.
sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" \
  "$repo_root/apps/api-rust/migrations/0018_subscription_amount_snapshots.sql" \
  >>"$runtime/rust-baseline.sql"
psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$rust_database" -v ON_ERROR_STOP=1 -f "$runtime/rust-baseline.sql" >/dev/null
psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$rust_database" -v ON_ERROR_STOP=1 -c "CREATE TABLE $rust_schema.lmm_schema_contract(singleton BOOLEAN PRIMARY KEY,min_reader_version BIGINT NOT NULL,max_reader_version BIGINT NOT NULL); INSERT INTO $rust_schema.lmm_schema_contract VALUES(true,1,1);" >/dev/null
sed "s/__LMM_APP_SCHEMA__/$rust_schema/g" "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" >"$runtime/bounty.sql"
psql -h 127.0.0.1 -p "$pg_port" -U "$pg_role" -d "$rust_database" -v ON_ERROR_STOP=1 -f "$runtime/bounty.sql" >/dev/null
seed "$go_database"; seed "$rust_database"

kill "$go_pid" 2>/dev/null || true
wait "$go_pid" 2>/dev/null || true
go_pid=''
start_go

start_rust() {
  DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$rust_secret@127.0.0.1:$rust_valkey_port" LMM_RS_TEST_INSTANCE=1 LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=single LMM_SCHEMA_CONTRACT=1 VERSION=v0.0.0 SESSION_SECRET="$session_secret" CRYPTO_SECRET="$crypto_secret" PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false "$rust_binary" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"
  wait_listener "$rust_port" rust_pid /readyz
}
start_rust

login() { curl --connect-timeout 2 --max-time 10 -fsS -H 'content-type: application/json' -d "{\"username\":\"$2\",\"password\":\"password\"}" "http://127.0.0.1:$1/api/user/login" | jq -er '.data.access_token | strings'; }
go_root=$(login "$go_port" root); rust_root=$(login "$rust_port" root); go_user=$(login "$go_port" user); rust_user=$(login "$rust_port" user)
seed_user_cache "$go_valkey_port" "$go_secret"
seed_user_cache "$rust_valkey_port" "$rust_secret"
request_method() {
  local method=$1 base=$2 token=$3 path=$4 body=$5 out=$6
  curl --connect-timeout 2 --max-time 10 -sS -X "$method" -D "$out.headers" -o "$out.body" -w '%{http_code}' \
    -H 'accept: application/json' -H 'content-type: application/json' -H "authorization: Bearer $token" \
    --data-binary "$body" "http://127.0.0.1:$base$path"
}
request() { request_method GET "$1" "$2" "$3" '' "$4"; }
compare() {
  local name=$1 path=$2 go_token=$3 rust_token=$4 go_code rust_code
  go_code=$(request "$go_port" "$go_token" "$path" "$runtime/go-$name")
  rust_code=$(request "$rust_port" "$rust_token" "$path" "$runtime/rust-$name")
  [[ $go_code == "$rust_code" ]] || { echo "$name status mismatch: $go_code/$rust_code" >&2; return 1; }
  jq -S . "$runtime/go-$name.body" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  grep -qi '^content-type: application/json' "$runtime/go-$name.headers"
  grep -qi '^content-type: application/json' "$runtime/rust-$name.headers"
}
compare_write() {
  local name=$1 body=$2 go_token=$3 rust_token=$4 go_code rust_code
  go_code=$(request_method PUT "$go_port" "$go_token" /api/subscription/self/preference "$body" "$runtime/go-$name")
  rust_code=$(request_method PUT "$rust_port" "$rust_token" /api/subscription/self/preference "$body" "$runtime/rust-$name")
  [[ $go_code == "$rust_code" ]] || { echo "$name status mismatch: $go_code/$rust_code" >&2; return 1; }
  jq -S . "$runtime/go-$name.body" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  grep -qi '^content-type: application/json' "$runtime/go-$name.headers"
  grep -qi '^content-type: application/json' "$runtime/rust-$name.headers"
}

compare_profile_write() {
  local name=$1 body=$2 go_token=$3 rust_token=$4 go_code rust_code
  go_code=$(request_method PUT "$go_port" "$go_token" /api/user/setting "$body" "$runtime/go-$name")
  rust_code=$(request_method PUT "$rust_port" "$rust_token" /api/user/setting "$body" "$runtime/rust-$name")
  [[ $go_code == "$rust_code" ]] || { echo "$name status mismatch: $go_code/$rust_code" >&2; return 1; }
  jq -S . "$runtime/go-$name.body" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  grep -qi '^content-type: application/json' "$runtime/go-$name.headers"
  grep -qi '^content-type: application/json' "$runtime/rust-$name.headers"
}

compare_self_write() {
  local name=$1 body=$2 go_token=$3 rust_token=$4 go_code rust_code
  go_code=$(request_method PUT "$go_port" "$go_token" /api/user/self "$body" "$runtime/go-$name")
  rust_code=$(request_method PUT "$rust_port" "$rust_token" /api/user/self "$body" "$runtime/rust-$name")
  [[ $go_code == "$rust_code" ]] || { echo "$name status mismatch: $go_code/$rust_code" >&2; return 1; }
  jq -S . "$runtime/go-$name.body" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  grep -qi '^content-type: application/json' "$runtime/go-$name.headers"
  grep -qi '^content-type: application/json' "$runtime/rust-$name.headers"
}

compare_self_delete() {
  local name=$1 go_token=$2 rust_token=$3 go_code rust_code
  go_code=$(request_method DELETE "$go_port" "$go_token" /api/user/self '' "$runtime/go-$name")
  rust_code=$(request_method DELETE "$rust_port" "$rust_token" /api/user/self '' "$runtime/rust-$name")
  [[ $go_code == "$rust_code" ]] || { echo "$name status mismatch: $go_code/$rust_code" >&2; return 1; }
  jq -S . "$runtime/go-$name.body" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.body" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  grep -qi '^content-type: application/json' "$runtime/go-$name.headers"
  grep -qi '^content-type: application/json' "$runtime/rust-$name.headers"
}

compare_profile_write profile-invalid-type '{"notify_type":"sms","quota_warning_threshold":1}' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "profile invalid-type DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
compare_profile_write profile-null 'null' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "profile null DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
compare_profile_write profile-email '{"notify_type":"email","quota_warning_threshold":2,"notification_email":"ada@example.test","accept_unset_model_ratio_model":true}' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "profile email DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
compare_profile_write profile-gotify-priority-fallback '{"notify_type":"gotify","quota_warning_threshold":3,"gotify_url":"https://gotify.example","gotify_token":"token","gotify_priority":99}' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "profile setting DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
if [[ "$(valkey_user_hash "$go_valkey_port" "$go_secret")" != "$(valkey_user_hash "$rust_valkey_port" "$rust_secret")" ]]; then
  echo "profile setting Valkey user hash mismatch" >&2
  printf '%s\n' "--- go" "$(valkey_user_hash "$go_valkey_port" "$go_secret")" "--- rust" "$(valkey_user_hash "$rust_valkey_port" "$rust_secret")" >&2
  exit 1
fi
compare_self_write self-language '{"language":"en"}' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "self language DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
if [[ "$(valkey_user_hash "$go_valkey_port" "$go_secret")" != "$(valkey_user_hash "$rust_valkey_port" "$rust_secret")" ]]; then
  echo "self language Valkey user hash mismatch" >&2
  printf '%s\n' "--- go" "$(valkey_user_hash "$go_valkey_port" "$go_secret")" "--- rust" "$(valkey_user_hash "$rust_valkey_port" "$rust_secret")" >&2
  exit 1
fi
compare_self_write self-sidebar '{"language":"zh-CN","sidebar_modules":"{\"chat\":true}"}' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "self sidebar DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
compare_self_write self-null 'null' "$go_user" "$rust_user"
if [[ "$(snapshot "$go_database")" != "$(snapshot "$rust_database")" ]]; then
  echo "self null DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$(snapshot "$go_database")" "--- rust" "$(snapshot "$rust_database")" >&2
  exit 1
fi
go_before=$(snapshot "$go_database"); rust_before=$(snapshot "$rust_database")
go_valkey_before=$(valkey_keys "$go_valkey_port" "$go_secret")
rust_valkey_before=$(valkey_keys "$rust_valkey_port" "$rust_secret")
compare user-plans /api/subscription/plans "$go_user" "$rust_user"
compare root-plans /api/subscription/admin/plans "$go_root" "$rust_root"
compare self /api/subscription/self "$go_user" "$rust_user"
compare admin-user-subscriptions /api/subscription/admin/users/1/subscriptions "$go_root" "$rust_root"
compare admin-invalid-user /api/subscription/admin/users/0/subscriptions "$go_root" "$rust_root"
[[ "$go_before" == "$(snapshot "$go_database")" && "$rust_before" == "$(snapshot "$rust_database")" ]]
[[ "$go_valkey_before" == "$(valkey_keys "$go_valkey_port" "$go_secret")" && "$rust_valkey_before" == "$(valkey_keys "$rust_valkey_port" "$rust_secret")" ]]
compare_write preference-valid '{"billing_preference":" wallet_only "}' "$go_user" "$rust_user"
go_after_preference=$(snapshot "$go_database"); rust_after_preference=$(snapshot "$rust_database")
if [[ "$go_after_preference" != "$rust_after_preference" ]]; then
  echo "preference DB snapshot mismatch" >&2
  printf '%s\n' "--- go" "$go_after_preference" "--- rust" "$rust_after_preference" >&2
  exit 1
fi
go_cache_after_preference=$(valkey_user_hash "$go_valkey_port" "$go_secret"); rust_cache_after_preference=$(valkey_user_hash "$rust_valkey_port" "$rust_secret")
if [[ "$go_cache_after_preference" != "$rust_cache_after_preference" ]]; then
  echo "preference Valkey user hash mismatch" >&2
  printf '%s\n' "--- go" "$go_cache_after_preference" "--- rust" "$rust_cache_after_preference" >&2
  exit 1
fi
compare_write preference-invalid '{"billing_preference":7}' "$go_user" "$rust_user"
compare_write preference-empty '{}' "$go_user" "$rust_user"
[[ "$(snapshot "$go_database")" == "$(snapshot "$rust_database")" ]]
[[ "$(valkey_user_hash "$go_valkey_port" "$go_secret")" == "$(valkey_user_hash "$rust_valkey_port" "$rust_secret")" ]]
compare_write preference-null 'null' "$go_user" "$rust_user"
compare_write preference-array '[]' "$go_user" "$rust_user"
sql "$go_database" "UPDATE options SET value='false' WHERE key='payment_setting.compliance_confirmed'"
sql "$rust_database" "UPDATE options SET value='false' WHERE key='payment_setting.compliance_confirmed'"
kill "$go_pid" "$rust_pid" 2>/dev/null || true
wait "$go_pid" "$rust_pid" 2>/dev/null || true
go_pid=''; rust_pid=''
start_go
start_rust
go_root=$(login "$go_port" root); rust_root=$(login "$rust_port" root); go_user=$(login "$go_port" user); rust_user=$(login "$rust_port" user)
compare compliance-off /api/subscription/plans "$go_user" "$rust_user"
compare non-admin /api/subscription/admin/plans "$go_user" "$rust_user"
compare_self_delete self-delete "$go_user" "$rust_user"
go_deleted=$(sql "$go_database" "SELECT (deleted_at IS NOT NULL) FROM users WHERE id = 2")
rust_deleted=$(sql "$rust_database" "SELECT (deleted_at IS NOT NULL) FROM users WHERE id = 2")
[[ $go_deleted == t && $rust_deleted == t ]]
[[ "$(valkey_keys "$go_valkey_port" "$go_secret")" != *$'user:2'* ]]
[[ "$(valkey_keys "$rust_valkey_port" "$rust_secret")" != *$'user:2'* ]]

jq -cn --arg revision "$legacy_revision" --argjson scenarios 20 '{test:"subscription-read-listener-differential",real_tcp:true,production_access:false,legacy_go_revision:$revision,scenarios:$scenarios,routes:["GET /api/subscription/plans","GET /api/subscription/admin/plans","GET /api/subscription/admin/users/:id/subscriptions","GET /api/subscription/self","PUT /api/subscription/self/preference","PUT /api/user/setting","PUT /api/user/self","DELETE /api/user/self"],result:"passed"}'
