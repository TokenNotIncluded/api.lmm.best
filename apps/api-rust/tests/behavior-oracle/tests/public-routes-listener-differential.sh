#!/usr/bin/env bash
# Isolated real-TCP Go/Rust differential for the public-content and legal routes.
# This file deliberately owns no shared helper: its lifecycle boundary is part
# of the evidence.  `/api/status` is covered by status-listener-differential.
set -euo pipefail
if [[ ${LMM_PUBLIC_ROUTES_TRACE:-0} != 1 ]]; then
  set +x
fi

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
approval_mode=${LMM_PUBLIC_ROUTES_APPROVAL:-0}
probe_only=${LMM_PUBLIC_ROUTES_PROBE_ONLY:-0}
hold_on_fail=${LMM_PUBLIC_HOLD_ON_FAIL:-0}
result_dir=${LMM_PUBLIC_ROUTES_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_PUBLIC_ROUTES_RESULT_DIR must be an absolute path without ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
curl_connect_timeout=2
curl_max_time=12
go_pid=''; rust_pid=''; go_valkey_pid=''; rust_valkey_pid=''; pg_pid=''; cleanup_started=false

case "$approval_mode:$probe_only" in 0:0|1:0|0:1) ;; *) echo 'approval mode refuses probe-only' >&2; exit 2;; esac
for command in cargo curl flock git go initdb jq pg_ctl postgres psql sqlite3 ss valkey-cli valkey-server od sha256sum; do command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }; done
[[ $(postgres --version) == *'PostgreSQL) 18.'* ]] || { echo 'requires PostgreSQL 18' >&2; exit 1; }
[[ -f "$legacy_root/SHA256SUMS" && -f "$legacy_root/GIT-LS-FILES-S.tsv" ]] || { echo 'pinned Go archive manifest missing' >&2; exit 1; }
(cd "$legacy_root" && sha256sum --check --status SHA256SUMS) || { echo 'pinned Go archive hash mismatch' >&2; exit 1; }
frozen_go_manifest_sha256=$(sha256sum "$legacy_root/SHA256SUMS" "$legacy_root/GIT-LS-FILES-S.tsv" | sha256sum | awk '{print $1}')
build_input_hash() {
  (cd "$repo_root" && {
    printf '%s\0' apps/api-rust/Cargo.toml apps/api-rust/Cargo.lock apps/api-rust/Cargo.toml
    [[ ! -f apps/api-rust/build.rs ]] || printf '%s\0' apps/api-rust/build.rs
    git ls-files -co --exclude-standard -z -- apps/api-rust/src apps/api-rust/assets apps/api-rust/crates
  } | while IFS= read -r -d '' path; do [[ -f $path ]] && sha256sum "$path"; done | LC_ALL=C sort | sha256sum | awk '{print $1}')
}
rust_source_sha256=$(build_input_hash)
[[ $frozen_go_manifest_sha256 =~ ^[[:xdigit:]]{64}$ && $rust_source_sha256 =~ ^[[:xdigit:]]{64}$ ]] || exit 1
runtime_root=${LMM_PUBLIC_RUNTIME_ROOT:-/tmp}
[[ $runtime_root == /* && $runtime_root != / && $runtime_root != *..* ]] || {
  echo 'LMM_PUBLIC_RUNTIME_ROOT must be an absolute non-root path without ..' >&2
  exit 2
}
mkdir -p -- "$runtime_root"
stop_pg() { if [[ -n ${pg_pid:-} ]] && owned pg_pid && listener_owned "$pg_port" "$pg_pid"; then pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true; elif [[ -n ${pg_pid:-} ]]; then echo "cleanup: PostgreSQL: refusing to signal state=$(process_state pg_pid "$pg_port") pid=$pg_pid" >&2; fi; pg_pid=''; }
if [[ $probe_only == 1 ]]; then
  jq -cn --arg go "$frozen_go_manifest_sha256" --arg rust "$rust_source_sha256" '{test:"public-routes-listener-differential",mode:"probe",approval_eligible:false,frozen_go_manifest_sha256:$go,rust_source_sha256:$rust,result:"passed"}'
  exit 0
fi
runtime=$(mktemp -d "$runtime_root/lmm-public-listener.XXXXXX")
go_build=$(mktemp -d "${TMPDIR:-/tmp}/lmm-public-go.XXXXXX")
exec 9>/tmp/lmm-listener-differential-heavy.lock
flock -n 9 || { echo 'another listener differential owns the heavy lock' >&2; exit 1; }

pid_start() { awk '{print $22}' "/proc/$1/stat" 2>/dev/null; }
# shellcheck disable=SC2034 # start times are accessed through deliberate indirection.
record_pid() { local n=$1 p=$2 s; s=$(pid_start "$p") || return 1; printf -v "$n" %s "$p"; printf -v "${n}_start" %s "$s"; }
owned() { local n=$1 p s want; p=${!n:-}; s=${n}_start; want=${!s:-}; [[ -n $p && -n $want && $(pid_start "$p") == "$want" ]] && kill -0 "$p" 2>/dev/null; }
process_state() {
  local n=$1 port=${2:-} p actual want line
  p=${!n:-}; want=${n}_start; want=${!want:-}
  actual=$(pid_start "$p" 2>/dev/null || true)
  [[ -z $actual ]] && { echo child-exited; return; }
  [[ $actual != "$want" ]] && { echo pid-start-time-mismatch; return; }
  if [[ -n $port ]]; then
    line=$(ss -H -ltnp "sport = :$port" 2>/dev/null || true)
    if [[ -n $line ]] && ! listener_owned "$port" "$p"; then echo port-owned-by-other; return; fi
  fi
  [[ -n $port && -z $line ]] && { echo child-not-listening; return; }
  echo child-not-owned
}
stop_owned() {
  local n=$1 port=${2:-} p
  p=${!n:-}
  if [[ -n $p ]] && owned "$n"; then kill "$p" 2>/dev/null || true; wait "$p" 2>/dev/null || true
  elif [[ -n $p ]]; then echo "cleanup: $n: refusing to signal state=$(process_state "$n" "$port") pid=$p" >&2; fi
  printf -v "$n" ''; printf -v "${n}_start" ''
}
go_redis_startup_diagnostics() {
  local ping=failed
  if VALKEYCLI_AUTH="$go_password" valkey-cli --no-auth-warning --command-timeout 1 -h 127.0.0.1 -p "$go_valkey_port" ping >/dev/null 2>&1; then ping=ok; fi
  {
    echo "Go listener startup diagnostics (credentials redacted):"
    echo "go_http_endpoint=127.0.0.1:$go_port"
    echo "go_redis_endpoint=127.0.0.1:$go_valkey_port"
    echo "go_redis_authenticated_ping=$ping"
    echo 'go_redis_listener_state:'
    ss -H -ltnp "sport = :$go_valkey_port" 2>/dev/null || true
    echo 'go_redis_connection_state:'
    ss -H -tnp "sport = :$go_valkey_port or dport = :$go_valkey_port" 2>/dev/null || true
    echo 'go.log tail:'
    tail -n 120 "$runtime/go.log" 2>/dev/null || true
    echo 'go-valkey.log tail:'
    tail -n 80 "$runtime/go-valkey.log" 2>/dev/null || true
  } >&2
}
cleanup() {
  local exit_code=$? preserve=0
  [[ $cleanup_started == false ]] || return "$exit_code"
  cleanup_started=true
  [[ $exit_code -ne 0 || $hold_on_fail == 1 ]] && preserve=1
  stop_owned go_pid "$go_port"; stop_owned rust_pid "$rust_port"; stop_owned go_valkey_pid "$go_valkey_port"; stop_owned rust_valkey_pid "$rust_valkey_port"; stop_pg
  case "$runtime" in
    "$runtime_root"/lmm-public-listener.*)
      if [[ $preserve == 1 ]]; then echo "preserved public listener runtime: $runtime" >&2; else rm -rf "$runtime"; fi
      ;;
    *) echo "refusing unexpected public runtime: $runtime" >&2;;
  esac
  case "$go_build" in "${TMPDIR:-/tmp}"/lmm-public-go.*) rm -rf "$go_build";; esac
  return "$exit_code"
}
trap cleanup EXIT INT TERM
random_port() { local p; while :; do p=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000)); [[ -z $(ss -H -ltn "sport = :$p") ]] && { echo "$p"; return; }; done; }
pg_port=$(random_port); go_port=$(random_port); rust_port=$(random_port); go_valkey_port=$(random_port); rust_valkey_port=$(random_port)
[[ $(printf '%s\n' "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port" | sort -u | wc -l) == 5 ]] || exit 1
port_free() { [[ -z $(ss -H -ltn "sport = :$1" 2>/dev/null) ]]; }
for port in "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port"; do port_free "$port" || { echo "random port became occupied: $port" >&2; exit 1; }; done
listener_owned() { local port=$1 expected=$2 line; line=$(ss -H -ltnp "sport = :$port" 2>/dev/null || true); [[ $(wc -l <<<"$line") == 1 && $line == *"pid=$expected,"* ]]; }
wait_http() { local port=$1 path=$2 pid=$3; for _ in {1..240}; do listener_owned "$port" "$pid" && curl --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" -fsS "http://127.0.0.1:$port$path" >/dev/null 2>&1 && return; sleep .05; done; return 1; }
password() { od -An -N32 -tx1 /dev/urandom | tr -d ' \n'; }
go_password=$(password); rust_password=$(password); rust_session_secret="Aa!$(password)"; rust_crypto_secret="Bb!$(password)"; go_valkey_config="$runtime/go-valkey.conf"; rust_valkey_config="$runtime/rust-valkey.conf"; umask 077
printf 'bind 127.0.0.1\nport %s\nunixsocket %s\nunixsocketperm 600\nrequirepass %s\nsave \nappendonly no\ndir %s\n' "$go_valkey_port" "$runtime/go-valkey.sock" "$go_password" "$runtime" >"$go_valkey_config"
printf 'bind 127.0.0.1\nport %s\nunixsocket %s\nunixsocketperm 600\nrequirepass %s\nsave \nappendonly no\ndir %s\n' "$rust_valkey_port" "$runtime/rust-valkey.sock" "$rust_password" "$runtime" >"$rust_valkey_config"
start_valkey_go() { port_free "$go_valkey_port" || return 1; valkey-server "$go_valkey_config" >"$runtime/go-valkey.log" 2>&1 & record_pid go_valkey_pid "$!"; for _ in {1..120}; do listener_owned "$go_valkey_port" "$go_valkey_pid" && valkey-cli --no-auth-warning -h 127.0.0.1 -p "$go_valkey_port" -a "$go_password" ping >/dev/null && return; sleep .05; done; return 1; }
start_valkey_rust() { port_free "$rust_valkey_port" || return 1; valkey-server "$rust_valkey_config" >"$runtime/rust-valkey.log" 2>&1 & record_pid rust_valkey_pid "$!"; for _ in {1..120}; do listener_owned "$rust_valkey_port" "$rust_valkey_pid" && valkey-cli --no-auth-warning -h 127.0.0.1 -p "$rust_valkey_port" -a "$rust_password" ping >/dev/null && return; sleep .05; done; return 1; }
start_go_listener() {
  port_free "$go_port" || return 1
  SQLITE_PATH="$runtime/go.db?_busy_timeout=30000" PORT="$go_port" REDIS_CONN_STRING="unix://:$(printf %s "$go_password")@$runtime/go-valkey.sock" SESSION_SECRET='public-differential-synthetic' GLOBAL_API_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none GIN_MODE=release "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
  local child=$!
  if ! record_pid go_pid "$child" || ! wait_http "$go_port" /api/status "$go_pid"; then
    go_redis_startup_diagnostics
    return 1
  fi
}

cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
[[ $(build_input_hash) == "$rust_source_sha256" ]] || { echo 'Rust build inputs changed during differential' >&2; exit 1; }
rust_binary_sha256=$(sha256sum "$repo_root/apps/api-rust/target/debug/lmm-api-rs" | awk '{print $1}')
cp -r "$legacy_root/." "$go_build/source"
chmod -R u+rwX "$go_build/source"
mkdir -p "$go_build/source/web/dist"
: >"$go_build/source/web/dist/index.html"
(cd "$go_build/source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/legacy-go" .)
start_valkey_go; start_valkey_rust
start_go_listener
sqlite3 -cmd '.timeout 30000' "$runtime/go.db" "INSERT OR REPLACE INTO options(key,value) VALUES ('Notice','notice value'),('About','about value'),('HomePageContent','home value'),('legal.user_agreement','agreement value'),('legal.privacy_policy','privacy value');"
stop_owned go_pid
start_go_listener
port_free "$pg_port" || exit 1; initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null; pg_ctl -D "$runtime/pg" -l "$runtime/pg.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null; record_pid pg_pid "$(head -n 1 "$runtime/pg/postmaster.pid")"; listener_owned "$pg_port" "$pg_pid" || { echo 'PostgreSQL listener ownership failed' >&2; exit 1; }; createdb -h 127.0.0.1 -p "$pg_port" public_diff
psql -h 127.0.0.1 -p "$pg_port" -d public_diff -v ON_ERROR_STOP=1 >/dev/null <<'SQL'
CREATE ROLE lmm_public_runtime LOGIN; CREATE TABLE lmm_schema_contract(singleton BOOLEAN PRIMARY KEY,min_reader_version BIGINT NOT NULL,max_reader_version BIGINT NOT NULL); INSERT INTO lmm_schema_contract VALUES(true,1,1); CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT); INSERT INTO options VALUES('Notice','notice value'),('About','about value'),('HomePageContent','home value'); CREATE TABLE custom_oauth_providers(id BIGINT PRIMARY KEY,name TEXT NOT NULL,slug TEXT NOT NULL,icon TEXT,enabled BOOLEAN,client_id TEXT,authorization_endpoint TEXT,scopes TEXT); CREATE TABLE setups(id BIGINT PRIMARY KEY); CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT,password TEXT NOT NULL,display_name TEXT,role BIGINT,status BIGINT,email TEXT,github_id TEXT,discord_id TEXT,oidc_id TEXT,wechat_id TEXT,telegram_id TEXT,"group" TEXT,quota BIGINT,used_quota BIGINT,request_count BIGINT,aff_code TEXT,aff_count BIGINT,aff_quota BIGINT,aff_history BIGINT,inviter_id BIGINT,linux_do_id TEXT,setting TEXT,stripe_customer TEXT,auth_version BIGINT,console_activated_at BIGINT,access_token TEXT,deleted_at TIMESTAMPTZ); CREATE TABLE user_sessions(sid TEXT PRIMARY KEY,user_id BIGINT,version BIGINT,user_auth_version BIGINT,status TEXT,refresh_hash CHAR(64),previous_refresh_hash TEXT,previous_valid_until BIGINT,login_method TEXT,ip TEXT,user_agent TEXT,created_at BIGINT,last_active_at BIGINT,expires_at BIGINT,revoked_at BIGINT,revoked_reason TEXT); CREATE TABLE two_fas(id BIGINT PRIMARY KEY,user_id BIGINT,is_enabled BOOLEAN,deleted_at TIMESTAMPTZ); CREATE TABLE casbin_rule(id BIGINT PRIMARY KEY,ptype TEXT,v0 TEXT,v1 TEXT,v2 TEXT,v3 TEXT); CREATE TABLE auth_flows(token_hash CHAR(64),purpose TEXT,user_id BIGINT,payload TEXT,created_at TIMESTAMPTZ,expires_at TIMESTAMPTZ,consumed_at TIMESTAMPTZ); CREATE SEQUENCE tokens_id_seq; CREATE TABLE tokens(id BIGINT PRIMARY KEY DEFAULT nextval('tokens_id_seq'),user_id BIGINT NOT NULL,key VARCHAR(128) UNIQUE,status INTEGER DEFAULT 1,name TEXT DEFAULT '',created_time BIGINT DEFAULT 0,accessed_time BIGINT DEFAULT 0,expired_time BIGINT DEFAULT -1,remain_quota BIGINT DEFAULT 0,unlimited_quota BOOLEAN DEFAULT false,model_limits_enabled BOOLEAN DEFAULT false,model_limits TEXT,allow_ips TEXT DEFAULT '',used_quota BIGINT DEFAULT 0,"group" TEXT DEFAULT '',cross_group_retry BOOLEAN DEFAULT false,deleted_at TIMESTAMPTZ); GRANT USAGE ON SCHEMA public TO lmm_public_runtime; GRANT SELECT ON ALL TABLES IN SCHEMA public TO lmm_public_runtime; GRANT INSERT,UPDATE ON auth_flows TO lmm_public_runtime; GRANT SELECT,INSERT,UPDATE,DELETE ON tokens TO lmm_public_runtime; GRANT USAGE ON SEQUENCE tokens_id_seq TO lmm_public_runtime;
-- Minimal subscription shape for listener readiness only; snapshots remain nullable.
CREATE TABLE user_subscriptions (
  amount_total BIGINT, amount_used BIGINT, reset_amount BIGINT, renewal_amount BIGINT
);
GRANT SELECT, UPDATE ON user_subscriptions TO lmm_public_runtime;
CREATE TABLE open_source_bounty_projects(id BIGINT,owner_user_id BIGINT,repository_url TEXT,title TEXT,description TEXT,rules TEXT,reward_quota BIGINT,net_reward_quota BIGINT,reward_slots BIGINT,escrow_quota BIGINT,platform_fee_rate_bps BIGINT,platform_fee_quota BIGINT,status TEXT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,published_at TIMESTAMPTZ,closed_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_challenges(id BIGINT,project_id BIGINT,participant_user_id BIGINT,github_handle TEXT,status TEXT,issue_url TEXT,pull_request_url TEXT,submission_note TEXT,review_note TEXT,reward_quota BIGINT,tip_quota BIGINT,owner_rating_score BIGINT,owner_rating_comment TEXT,owner_rated_at TIMESTAMPTZ,contributor_rating_score BIGINT,contributor_rating_comment TEXT,contributor_rated_at TIMESTAMPTZ,owner_rating_overturned BOOLEAN,accepted_at TIMESTAMPTZ,submitted_at TIMESTAMPTZ,reviewed_at TIMESTAMPTZ,rejected_at TIMESTAMPTZ,paid_at TIMESTAMPTZ,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_ledgers(id BIGINT,project_id BIGINT,challenge_id BIGINT,user_id BIGINT,counterparty_user_id BIGINT,kind TEXT,quota BIGINT,note TEXT,recipient_read_at TIMESTAMPTZ,thanked_at TIMESTAMPTZ,created_at TIMESTAMPTZ);
CREATE TABLE open_source_bounty_disputes(id BIGINT,challenge_id BIGINT,project_id BIGINT,opened_by_user_id BIGINT,against_user_id BIGINT,reason TEXT,statement TEXT,project_title_snapshot TEXT,repository_url_snapshot TEXT,project_rules_snapshot TEXT,project_escrow_quota_snapshot BIGINT,challenge_status_snapshot TEXT,issue_url_snapshot TEXT,pull_request_url_snapshot TEXT,submission_note_snapshot TEXT,review_note_snapshot TEXT,reward_quota_snapshot BIGINT,tip_quota_snapshot BIGINT,owner_rating_score_snapshot BIGINT,owner_rating_comment_snapshot TEXT,contributor_rating_score_snapshot BIGINT,contributor_rating_comment_snapshot TEXT,status TEXT,resolution TEXT,resolved_by_user_id BIGINT,created_at TIMESTAMPTZ,updated_at TIMESTAMPTZ,resolved_at TIMESTAMPTZ);
GRANT SELECT ON lmm_schema_contract, options, custom_oauth_providers, setups, users, user_sessions, two_fas, casbin_rule, auth_flows, tokens, open_source_bounty_projects, open_source_bounty_challenges, open_source_bounty_ledgers, open_source_bounty_disputes TO lmm_public_runtime; GRANT UPDATE (console_activated_at) ON users TO lmm_public_runtime;
SQL
psql -h 127.0.0.1 -p "$pg_port" -d public_diff -v ON_ERROR_STOP=1 \
  -c "INSERT INTO options(key,value) VALUES ('legal.user_agreement','agreement value'),('legal.privacy_policy','privacy value');" >/dev/null
port_free "$rust_port" || exit 1; DATABASE_URL="postgresql://lmm_public_runtime@127.0.0.1:$pg_port/public_diff" VALKEY_URL="redis://:$(printf %s "$rust_password")@127.0.0.1:$rust_valkey_port" LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=blue LMM_SCHEMA_CONTRACT=1 SESSION_SECRET='PublicDifferential-Session-2026-Synthetic' CRYPTO_SECRET='PublicDifferential-Crypto-2026-Synthetic' GLOBAL_API_RATE_LIMIT_ENABLE=false VERSION=v0.0.0 "$repo_root/apps/api-rust/target/debug/lmm-api-rs" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!"; wait_http "$rust_port" /readyz "$rust_pid"
request() { local base=$1 path=$2 out=$3; curl --connect-timeout "$curl_connect_timeout" --max-time "$curl_max_time" -sS -D "$out.h" -o "$out.b" -w '%{http_code}' -H 'accept: application/json' "$base$path"; }
assert_headers() { awk 'BEGIN{IGNORECASE=1} /^content-type:[[:space:]]*application\/json; charset=utf-8\r?$/ {ct=1} /^x-new-api-version:[[:space:]]*v0\.0\.0\r?$/ {v=1} /^x-oneapi-request-id:[[:space:]]*[^[:space:]]+/ {r=1} END{exit !(ct&&v&&r)}' "$1"; }
scenario_total=0
compare() {
  local path=$1 name index
  name=${path##*/}
  [[ $(request "http://127.0.0.1:$go_port" "$path" "$runtime/go-$name") == 200 ]]
  [[ $(request "http://127.0.0.1:$rust_port" "$path" "$runtime/rust-$name") == 200 ]]
  assert_headers "$runtime/go-$name.h"
  assert_headers "$runtime/rust-$name.h"
  jq -S . "$runtime/go-$name.b" >"$runtime/go-$name.json"
  jq -S . "$runtime/rust-$name.b" >"$runtime/rust-$name.json"
  diff -u "$runtime/go-$name.json" "$runtime/rust-$name.json"
  scenario_total=$((scenario_total+1))
  if [[ -n $result_dir ]]; then
    case "$path" in
      /api/notice) index=1 ;;
      /api/about) index=2 ;;
      /api/home_page_content) index=3 ;;
      /api/user-agreement) index=4 ;;
      /api/privacy-policy) index=5 ;;
      /api/uptime/status) index=6 ;;
      *) return 0 ;;
    esac
    jq -cn --arg method GET --arg path "$path" --argjson cases 1 \
      --arg go_manifest "$frozen_go_manifest_sha256" \
      --arg rust_source "$rust_source_sha256" \
      --arg rust_binary "$rust_binary_sha256" \
      --argjson approval_eligible "$([[ $approval_mode == 1 ]] && echo true || echo false)" \
      '{method:$method,path:$path,differential_verified:true,differential_scope:"public-routes",cases:$cases,real_tcp:true,postgres_valkey_isolated:true,approval_eligible:$approval_eligible,approval_credit:false,frozen_go_manifest_sha256:$go_manifest,rust_source_sha256:$rust_source,rust_binary_sha256:$rust_binary,differences:null,mismatch_names:[]}' \
      >"$result_dir/public-routes-$index.json"
  fi
}
options_snapshot() { local engine=$1; if [[ $engine == go ]]; then sqlite3 -json "$runtime/go.db" 'SELECT key,value FROM options ORDER BY key'|jq -S .; else psql -h 127.0.0.1 -p "$pg_port" -d public_diff -qAt -c "SELECT COALESCE(json_agg(to_jsonb(x) ORDER BY x.key),'[]'::json) FROM (SELECT key,value FROM options)x"|jq -S .; fi; }
valkey_snapshot() { local port=$1 secret=$2; valkey-cli --no-auth-warning -h 127.0.0.1 -p "$port" -a "$secret" --scan | LC_ALL=C sort; }
options_snapshot go >"$runtime/go.options.before"; options_snapshot rust >"$runtime/rust.options.before"; valkey_snapshot "$go_valkey_port" "$go_password" >"$runtime/go.valkey.before"; valkey_snapshot "$rust_valkey_port" "$rust_password" >"$runtime/rust.valkey.before"
for route in /api/notice /api/about /api/home_page_content /api/user-agreement /api/privacy-policy /api/uptime/status; do compare "$route"; done
[[ $scenario_total == 6 ]] || { echo "scenario count mismatch: $scenario_total" >&2; exit 1; }
options_snapshot go >"$runtime/go.options.after"; options_snapshot rust >"$runtime/rust.options.after"; diff -u "$runtime/go.options.before" "$runtime/go.options.after"; diff -u "$runtime/rust.options.before" "$runtime/rust.options.after"; valkey_snapshot "$go_valkey_port" "$go_password" >"$runtime/go.valkey.after"; valkey_snapshot "$rust_valkey_port" "$rust_password" >"$runtime/rust.valkey.after"; [[ ! -s $runtime/go.valkey.after ]]; diff -u /dev/null "$runtime/rust.valkey.before"; diff -u /dev/null "$runtime/rust.valkey.after"
summary=$(jq -cn --arg go "$frozen_go_manifest_sha256" --arg rust "$rust_source_sha256" --arg binary "$rust_binary_sha256" --argjson approval "$approval_mode" --argjson scenarios "$scenario_total" '{test:"public-routes-listener-differential",mode:"full",approval_eligible:($approval==1),approval_credit:false,real_tcp:true,scenario_count:$scenarios,routes:["GET /api/notice","GET /api/about","GET /api/home_page_content","GET /api/user-agreement","GET /api/privacy-policy","GET /api/uptime/status"],frozen_go_manifest_sha256:$go,rust_source_sha256:$rust,rust_binary_sha256:$binary,result:"passed"}')
if [[ -n $result_dir ]]; then
  printf '%s\n' "$summary" >"$result_dir/public-routes-summary.json"
fi
printf '%s\n' "$summary"
