#!/usr/bin/env bash
# Exercises the isolated identity-security candidate over TCP.  It is opt-in
# because it creates disposable PG18 clusters and local Valkey children.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac

if [[ ${LMM_RUN_IDENTITY_SECURITY_DIFFERENTIAL:-0} != 1 ]]; then
  cat >&2 <<'EOF'
identity-security listener differential is opt-in.
Run with LMM_RUN_IDENTITY_SECURITY_DIFFERENTIAL=1 after a Planner-approved
serialized heavy-test slot. This runner only binds loopback test ports and
creates temporary PostgreSQL/Valkey state under /tmp.
EOF
  exit 2
fi

pg_port=${LMM_IDENTITY_SECURITY_PG_PORT:-55459}
go_port=${LMM_IDENTITY_SECURITY_GO_PORT:-13019}
rust_port=${LMM_IDENTITY_SECURITY_RUST_PORT:-33049}
go_valkey_port=${LMM_IDENTITY_SECURITY_GO_VALKEY_PORT:-16419}
rust_valkey_port=${LMM_IDENTITY_SECURITY_RUST_VALKEY_PORT:-56419}
rust_database=lmm_test_identity_security_rust
rust_role=lmm_test_identity_security_runtime
rust_schema=lmm_test_identity_security_v1
runtime=$(mktemp -d /tmp/lmm-identity-security.XXXXXX)
go_valkey_pid=''
rust_valkey_pid=''
go_pid=''
rust_pid=''

cleanup() {
  local pid
  for pid in "$go_pid" "$rust_pid" "$go_valkey_pid" "$rust_valkey_pid"; do
    if [[ -n $pid ]]; then
      kill "$pid" 2>/dev/null || true
      wait "$pid" 2>/dev/null || true
    fi
  done
  if [[ -d $runtime/pg ]]; then
    pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  fi
  case "$runtime" in
    /tmp/lmm-identity-security.*)
      [[ ${LMM_KEEP_IDENTITY_SECURITY_RUNTIME:-0} == 1 ]] || rm -rf "$runtime"
      ;;
    *) printf 'refusing unexpected runtime cleanup: %s\n' "$runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM

require_command() {
  command -v "$1" >/dev/null || {
    printf 'required command unavailable: %s\n' "$1" >&2
    exit 1
  }
}

for command_name in cargo curl git go initdb jq pg_ctl postgres psql createdb ss stat valkey-cli valkey-server openssl; do
  require_command "$command_name"
done
[[ $(postgres --version) == *'PostgreSQL) 18.'* ]] || {
  printf 'PostgreSQL 18 is required\n' >&2
  exit 1
}

assert_unused_port() {
  local port=$1
  if ss -ltn "sport = :$port" | grep -q LISTEN; then
    printf 'refusing occupied test port: %s\n' "$port" >&2
    exit 1
  fi
}

for port in "$pg_port" "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port"; do
  assert_unused_port "$port"
done

go_valkey_password=$(openssl rand -hex 32)
rust_valkey_password=$(openssl rand -hex 32)
session_secret='IdentitySecurityListener-2026!SyntheticOnly'

start_valkey() {
  local name=$1 port=$2 password=$3 pid_name=$4
  local config="$runtime/$name-valkey.conf"
  (
    umask 077
    {
      printf 'bind 127.0.0.1\n'
      printf 'protected-mode yes\n'
      printf 'port %s\n' "$port"
      printf 'save ""\n'
      printf 'appendonly no\n'
      printf 'dir %s\n' "$runtime"
      printf 'logfile %s\n' "$runtime/$name-valkey.log"
      printf 'requirepass %s\n' "$password"
    } >"$config"
  )
  chmod 600 "$config"
  [[ $(stat -c '%a' "$config") == 600 ]]
  valkey-server "$config" &
  local child_pid=$!
  printf -v "$pid_name" '%s' "$child_pid"
  for _ in {1..100}; do
    if VALKEYCLI_AUTH="$password" valkey-cli -h 127.0.0.1 -p "$port" --no-auth-warning ping >/dev/null 2>&1; then
      return 0
    fi
    if ! kill -0 "$child_pid" 2>/dev/null; then
      cat "$runtime/$name-valkey.log" >&2 || true
      return 1
    fi
    sleep 0.05
  done
  printf 'Valkey did not become ready: %s\n' "$name" >&2
  return 1
}

start_valkey go "$go_valkey_port" "$go_valkey_password" go_valkey_pid
start_valkey rust "$rust_valkey_port" "$rust_valkey_password" rust_valkey_pid

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
  -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$rust_database"

# The Rust candidate is an explicitly isolated test instance.  Its schema and
# role must remain unique to this runner; production databases are never named
# here and no environment defaults are accepted for DATABASE_URL/VALKEY_URL.
psql -h 127.0.0.1 -p "$pg_port" -d "$rust_database" -v ON_ERROR_STOP=1 \
  -v rust_role="$rust_role" -v rust_schema="$rust_schema" <<'SQL' >/dev/null
CREATE ROLE :"rust_role" LOGIN;
CREATE SCHEMA :"rust_schema" AUTHORIZATION :"rust_role";
SET search_path TO :"rust_schema";
CREATE TABLE lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL);
INSERT INTO lmm_schema_contract VALUES (TRUE, 1, 1);
-- Minimal subscription shape for listener readiness only; snapshots remain nullable.
CREATE TABLE user_subscriptions (
  amount_total BIGINT, amount_used BIGINT, reset_amount BIGINT, renewal_amount BIGINT
);
CREATE TABLE users (
  id BIGINT PRIMARY KEY, username TEXT NOT NULL, password TEXT NOT NULL,
  display_name TEXT, role BIGINT NOT NULL, status BIGINT NOT NULL, email TEXT,
  github_id TEXT, discord_id TEXT, oidc_id TEXT, wechat_id TEXT,
  telegram_id TEXT, access_token TEXT, quota BIGINT DEFAULT 0,
  used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0,
  "group" TEXT DEFAULT 'default', setting TEXT DEFAULT '{}',
  aff_code TEXT, aff_count BIGINT DEFAULT 0, aff_quota BIGINT DEFAULT 0,
  aff_history BIGINT DEFAULT 0, inviter_id BIGINT, linux_do_id TEXT,
  stripe_customer TEXT, console_activated_at BIGINT NOT NULL DEFAULT 0,
  auth_version BIGINT NOT NULL DEFAULT 1, deleted_at TIMESTAMPTZ
);
CREATE TABLE user_sessions (sid TEXT PRIMARY KEY, user_id BIGINT NOT NULL, version BIGINT NOT NULL, user_auth_version BIGINT NOT NULL, status TEXT NOT NULL, refresh_hash CHAR(64) NOT NULL, previous_refresh_hash TEXT, previous_valid_until BIGINT NOT NULL DEFAULT 0, login_method TEXT NOT NULL, ip TEXT, user_agent TEXT, created_at BIGINT NOT NULL, last_active_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revoked_at BIGINT NOT NULL DEFAULT 0, revoked_reason TEXT);
CREATE TABLE passkey_credentials (user_id BIGINT NOT NULL, credential_id BYTEA, deleted_at TIMESTAMPTZ, last_used_at TIMESTAMPTZ, backup_eligible BOOLEAN, backup_state BOOLEAN);
CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE custom_oauth_providers (id BIGINT PRIMARY KEY, name TEXT, slug TEXT, icon TEXT, enabled BOOLEAN, client_id TEXT, authorization_endpoint TEXT, scopes TEXT);
CREATE TABLE setups (id BIGINT PRIMARY KEY);
CREATE TABLE two_fas (id BIGINT PRIMARY KEY, user_id BIGINT, secret TEXT, is_enabled BOOLEAN, deleted_at TIMESTAMPTZ);
CREATE TABLE casbin_rule (id BIGINT PRIMARY KEY, ptype TEXT, v0 TEXT, v1 TEXT, v2 TEXT, v3 TEXT, v4 TEXT, v5 TEXT);
CREATE TABLE auth_flows (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, token_hash CHAR(64) NOT NULL UNIQUE, purpose TEXT NOT NULL, provider TEXT, intent TEXT, user_id BIGINT, session_id TEXT, payload TEXT, created_at TIMESTAMPTZ, expires_at TIMESTAMPTZ NOT NULL, consumed_at TIMESTAMPTZ);
CREATE TABLE two_fa_backup_codes (id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, user_id BIGINT NOT NULL, code_hash TEXT NOT NULL, is_used BOOLEAN DEFAULT FALSE, used_at TIMESTAMPTZ, created_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ);
CREATE TABLE tokens (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, key VARCHAR(128) UNIQUE, status INTEGER DEFAULT 1, name TEXT DEFAULT '', created_time BIGINT DEFAULT 0, accessed_time BIGINT DEFAULT 0, expired_time BIGINT DEFAULT -1, remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN DEFAULT FALSE, model_limits_enabled BOOLEAN DEFAULT FALSE, model_limits TEXT, allow_ips TEXT DEFAULT '', used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN DEFAULT FALSE, deleted_at TIMESTAMPTZ);
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
GRANT USAGE ON SCHEMA public TO :"rust_role";
GRANT USAGE ON SCHEMA :"rust_schema" TO :"rust_role";
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA :"rust_schema" TO :"rust_role";
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA :"rust_schema" TO :"rust_role";
SQL

cargo build --offline --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
cp -a "$legacy_root/." "$runtime/go-source"
mkdir -p "$runtime/go-source/web/dist"
: >"$runtime/go-source/web/dist/index.html"
(
  cd "$runtime/go-source"
  GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GONOSUMDB='*' CGO_ENABLED=1 \
    go build -mod=readonly -o "$runtime/legacy-go" .
)

dead_proxy='http://127.0.0.1:9'
local_only='127.0.0.1,localhost'
# These identity probes are all anonymous/auth-fail boundaries and do not
# require Go's cache client. Keep the disposable Valkey child for the Rust
# listener, but leave REDIS_CONN_STRING unset so Go cannot turn a cache
# transport hiccup into a readiness failure before the HTTP matrix.
env SQL_DSN=local SQLITE_PATH="$runtime/legacy.db?_busy_timeout=30000" PORT="$go_port" \
  SESSION_SECRET="$session_secret" GIN_MODE=release \
  "$runtime/legacy-go" >"$runtime/go.log" 2>&1 &
go_pid=$!

# The current config accepts the serialized test slot `single`; the old
# `blue` value makes the Rust listener exit before any route is exercised.
env DATABASE_URL="postgresql://$rust_role@127.0.0.1:$pg_port/$rust_database?options=-csearch_path%3D$rust_schema" \
  VALKEY_URL="redis://:$rust_valkey_password@127.0.0.1:$rust_valkey_port" \
  LMM_RS_TEST_INSTANCE=1 LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" LMM_RS_SLOT=single \
  LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" LMM_SCHEMA_CONTRACT=1 SESSION_SECRET="$session_secret" \
  HTTP_PROXY="$dead_proxy" HTTPS_PROXY="$dead_proxy" ALL_PROXY="$dead_proxy" \
  http_proxy="$dead_proxy" https_proxy="$dead_proxy" all_proxy="$dead_proxy" \
  NO_PROXY="$local_only" no_proxy="$local_only" \
  "$repo_root/apps/api-rust/target/debug/lmm-api-rs" >"$runtime/rust.log" 2>&1 &
rust_pid=$!

wait_for_http() {
  local name=$1 url=$2 pid=$3 logfile=$4
  for _ in {1..300}; do
    curl -fsS "$url" >/dev/null 2>&1 && return 0
    if ! kill -0 "$pid" 2>/dev/null; then
      cat "$logfile" >&2 || true
      return 1
    fi
    sleep 0.05
  done
  printf '%s listener did not become ready\n' "$name" >&2
  return 1
}

wait_for_http go "http://127.0.0.1:$go_port/api/status" "$go_pid" "$runtime/go.log"
wait_for_http rust "http://127.0.0.1:$rust_port/readyz" "$rust_pid" "$runtime/rust.log"

capture_response() {
  local prefix=$1 method=$2 base=$3 path=$4 body=$5 authorization=$6
  local -a curl_args=(
    --noproxy '*'
    -sS
    -X "$method"
    -H 'accept: application/json'
    -H 'accept-language: zh-CN'
    -H 'content-type: application/json'
  )
  if [[ $authorization != __ABSENT__ ]]; then
    curl_args+=(-H "authorization: $authorization")
  fi
  if [[ $body != __NO_BODY__ ]]; then
    curl_args+=(--data-binary "$body")
  fi
  curl "${curl_args[@]}" \
    -D "$runtime/$prefix.headers" -o "$runtime/$prefix.json" -w '%{http_code}' \
    "$base$path" >"$runtime/$prefix.status"
}

fail_assertion() {
  local message=$1 prefix=${2:-}
  printf 'assertion failed: %s\n' "$message" >&2
  if [[ -n $prefix ]]; then
    printf '%s status: ' "$prefix" >&2
    cat "$runtime/$prefix.status" >&2 || true
    printf '\n%s headers:\n' "$prefix" >&2
    cat "$runtime/$prefix.headers" >&2 || true
    printf '\n%s body:\n' "$prefix" >&2
    cat "$runtime/$prefix.json" >&2 || true
    printf '\n' >&2
  fi
  exit 1
}

header_value() {
  local file=$1 wanted=$2
  awk -v wanted="$wanted" '
    {
      line = $0
      sub(/\r$/, "", line)
      colon = index(line, ":")
      if (colon > 0 && tolower(substr(line, 1, colon - 1)) == tolower(wanted)) {
        value = substr(line, colon + 1)
        sub(/^[[:space:]]+/, "", value)
        print value
        exit
      }
    }
  ' "$file"
}

assert_status() {
  local prefix=$1 expected=$2 actual
  actual=$(<"$runtime/$prefix.status")
  [[ $actual == "$expected" ]] || fail_assertion "$prefix status $actual != $expected" "$prefix"
}

assert_json_exact() {
  local prefix=$1 expected=$2
  jq -e --argjson expected "$expected" '. == $expected' "$runtime/$prefix.json" >/dev/null \
    || fail_assertion "$prefix JSON differs from the frozen envelope" "$prefix"
}

assert_header_equals() {
  local prefix=$1 name=$2 expected=$3 actual
  actual=$(header_value "$runtime/$prefix.headers" "$name")
  [[ $actual == "$expected" ]] \
    || fail_assertion "$prefix header $name '$actual' != '$expected'" "$prefix"
}

assert_header_absent() {
  local prefix=$1 name=$2 actual
  actual=$(header_value "$runtime/$prefix.headers" "$name")
  [[ -z $actual ]] || fail_assertion "$prefix unexpectedly emitted $name" "$prefix"
}

assert_common_failure_headers() {
  local prefix=$1 implementation=$2
  case "$implementation" in
    go|rust) assert_header_equals "$prefix" content-type 'application/json; charset=utf-8' ;;
    *) fail_assertion "unknown implementation: $implementation" "$prefix" ;;
  esac
  assert_header_absent "$prefix" auth-version
  assert_header_absent "$prefix" set-cookie
}

assert_no_store_headers() {
  local prefix=$1
  assert_header_equals "$prefix" cache-control 'no-store, no-cache, must-revalidate, private, max-age=0'
  assert_header_equals "$prefix" pragma 'no-cache'
  assert_header_equals "$prefix" expires '0'
}

assert_cache_headers_absent() {
  local prefix=$1
  assert_header_absent "$prefix" cache-control
  assert_header_absent "$prefix" pragma
  assert_header_absent "$prefix" expires
}

readonly unauthorized_json='{"success":false,"message":"无权进行此操作，access token 无效","code":"AUTH_UNAUTHORIZED"}'
readonly unavailable_json='{"success":false,"message":"安全服务暂不可用"}'
routes_checked=0
authorization_shapes_checked=0

# Every protected route receives the same locale and malformed body on both
# implementations. Authentication must run first and match the frozen Go
# status, envelope, content type, and absence of auth/cookie side effects.
protected_routes=(
  'DELETE|/api/user/1/bindings/github|__NO_BODY__'
  'DELETE|/api/user/1/reset_passkey|__NO_BODY__'
  'GET|/api/user/passkey|__NO_BODY__'
  'DELETE|/api/user/passkey|__NO_BODY__'
  'DELETE|/api/user/sessions/not-a-session|__NO_BODY__'
  'GET|/api/authz/catalog|__NO_BODY__'
  'GET|/api/user/sessions|__NO_BODY__'
  'POST|/api/user/passkey/register/begin|{not-json'
  'POST|/api/user/passkey/register/finish|{not-json'
  'POST|/api/user/passkey/verify/begin|{not-json'
  'POST|/api/user/passkey/verify/finish|{not-json'
  'POST|/api/user/sessions/revoke-others|{not-json'
  'POST|/api/verify|{not-json'
)
for route in "${protected_routes[@]}"; do
  IFS='|' read -r method path body <<<"$route"
  label=$(printf '%s_%s' "$method" "$path" | tr -c '[:alnum:]' '_')
  for implementation in go rust; do
    if [[ $implementation == go ]]; then
      base="http://127.0.0.1:$go_port"
    else
      base="http://127.0.0.1:$rust_port"
    fi
    prefix="$implementation-$label"
    capture_response "$prefix" "$method" "$base" "$path" "$body" __ABSENT__
    assert_status "$prefix" 401
    assert_json_exact "$prefix" "$unauthorized_json"
    assert_common_failure_headers "$prefix" "$implementation"
    assert_cache_headers_absent "$prefix"
  done
  routes_checked=$((routes_checked + 1))
done

# Exercise the frozen Go credential grammar on both listeners: one bare token,
# a case-insensitive Bearer pair, and the two malformed field counts. Tokens
# remain deliberately invalid, so neither listener needs seeded user data.
authorization_shapes=(
  'bare-test-token'
  'bEaReR bearer-test-token'
  'Bearer'
  'Bearer token extra'
)
for authorization in "${authorization_shapes[@]}"; do
  label=$(printf '%s' "$authorization" | tr -c '[:alnum:]' '_')
  for implementation in go rust; do
    if [[ $implementation == go ]]; then
      base="http://127.0.0.1:$go_port"
    else
      base="http://127.0.0.1:$rust_port"
    fi
    prefix="$implementation-auth-shape-$label"
    capture_response "$prefix" GET "$base" /api/user/passkey __NO_BODY__ "$authorization"
    assert_status "$prefix" 401
    assert_json_exact "$prefix" "$unauthorized_json"
    assert_common_failure_headers "$prefix" "$implementation"
    assert_cache_headers_absent "$prefix"
  done
  authorization_shapes_checked=$((authorization_shapes_checked + 1))
done

# These requests deliberately fail before outbound delivery or proof work.
# Legacy Go business rejections and Rust's explicit 503 capability boundary
# are asserted independently; 404/405/500 and generic non-success are never
# accepted as evidence.
anonymous_routes=(
  'GET|/api/reset_password?email=not-an-email|__NO_BODY__|200|{"success":false,"message":"无效的参数"}|plain'
  'GET|/api/verification?email=not-an-email|__NO_BODY__|200|{"success":false,"message":"无效的参数"}|plain'
  'POST|/api/user/login/2fa|{}|200|{"success":false,"message":"参数错误"}|no-store'
  'POST|/api/user/passkey/login/begin|{}|200|{"success":false,"message":"管理员未启用 Passkey 登录"}|no-store'
  'POST|/api/user/passkey/login/finish|{}|200|{"success":false,"message":"管理员未启用 Passkey 登录"}|no-store'
  'POST|/api/user/register|{}|200|{"success":false,"message":"无效的参数"}|plain'
  'POST|/api/user/reset|{}|200|{"success":false,"message":"无效的参数"}|plain'
)
for route in "${anonymous_routes[@]}"; do
  IFS='|' read -r method path body go_status go_json cache_policy <<<"$route"
  label=$(printf '%s_%s' "$method" "$path" | tr -c '[:alnum:]' '_')

  capture_response "go-$label" "$method" "http://127.0.0.1:$go_port" "$path" "$body" __ABSENT__
  assert_status "go-$label" "$go_status"
  assert_json_exact "go-$label" "$go_json"
  assert_common_failure_headers "go-$label" go

  capture_response "rust-$label" "$method" "http://127.0.0.1:$rust_port" "$path" "$body" __ABSENT__
  # Verify2FALogin binds the required `code` field in Go before touching its
  # unavailable provider.  The Rust candidate mirrors that observable
  # malformed-body boundary with the same HTTP-200 parameter-error envelope;
  # only well-shaped requests continue to the explicit 503 capability gate.
  if [[ $path == /api/user/login/2fa ||
    $path == /api/user/passkey/login/begin ||
    $path == /api/user/passkey/login/finish ||
    $path == /api/user/reset ]]; then
    assert_status "rust-$label" "$go_status"
    assert_json_exact "rust-$label" "$go_json"
  else
    assert_status "rust-$label" 503
    assert_json_exact "rust-$label" "$unavailable_json"
  fi
  assert_common_failure_headers "rust-$label" rust

  if [[ $cache_policy == no-store ]]; then
    assert_no_store_headers "go-$label"
    assert_no_store_headers "rust-$label"
  else
    assert_cache_headers_absent "go-$label"
    assert_cache_headers_absent "rust-$label"
  fi
  routes_checked=$((routes_checked + 1))
done

jq -cn \
  --argjson routes_checked "$routes_checked" \
  --argjson authorization_shapes_checked "$authorization_shapes_checked" \
  '{test:"identity-security-listener-differential", postgres_major:18, listener:"loopback-only", runtime:"isolated", routes_checked:$routes_checked, authorization_shapes_checked:$authorization_shapes_checked, authenticated_before_parse:true, external_security_boundaries:"fail-closed", approved_functionality:0, result:"passed"}'
