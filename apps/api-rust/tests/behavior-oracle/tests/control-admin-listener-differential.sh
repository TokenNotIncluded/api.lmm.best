#!/usr/bin/env bash
# Isolated TCP boundary check for the fourteen control-admin candidate routes.
# Heavy mode requires two explicitly provisioned local PostgreSQL test
# databases and unique, previously absent schemas. It never accepts production
# endpoints and never builds binaries itself.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../.." && pwd)

go_port=${LMM_CONTROL_ADMIN_GO_PORT:-13016}
rust_port=${LMM_CONTROL_ADMIN_RUST_PORT:-33046}
go_valkey_port=${LMM_CONTROL_ADMIN_GO_VALKEY_PORT:-16396}
rust_valkey_port=${LMM_CONTROL_ADMIN_RUST_VALKEY_PORT:-56396}
runtime=$(mktemp -d /tmp/lmm-control-admin-listener.XXXXXX)
result_dir=${LMM_CONTROL_ADMIN_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    printf 'LMM_CONTROL_ADMIN_RESULT_DIR must be an absolute path without ..\n' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
go_valkey_secret=$(openssl rand -hex 32)
rust_valkey_secret=$(openssl rand -hex 32)
rust_session_secret="ControlAdmin-Session-${rust_valkey_secret}!"

# METHOD|PATH|BODY. These are the fourteen frozen Go registrations, excluding
# the GET /api/task -> /api/task/ trailing-slash redirect alias.
readonly route_matrix=(
  'GET|/api/custom-oauth-provider/|'
  'GET|/api/custom-oauth-provider/not-an-id|'
  'POST|/api/custom-oauth-provider/|{'
  'PUT|/api/custom-oauth-provider/not-an-id|{'
  'DELETE|/api/custom-oauth-provider/not-an-id|'
  'POST|/api/custom-oauth-provider/discovery|{'
  'POST|/api/system-task/log-cleanup?target_timestamp=not-a-number|'
  'GET|/api/system-task/list?limit=0|'
  'GET|/api/system-task/current|'
  'GET|/api/system-task/missing-task|'
  'GET|/api/system-info/instances|'
  'DELETE|/api/system-info/stale-instances|'
  'DELETE|/api/system-info/instances/missing-node|'
  'GET|/api/task/|'
)

cleanup() {
  for pid in ${go_pid:-} ${rust_pid:-} ${go_valkey_pid:-} ${rust_valkey_pid:-}; do
    kill "$pid" 2>/dev/null || true
  done
  wait ${go_pid:-} ${rust_pid:-} ${go_valkey_pid:-} ${rust_valkey_pid:-} \
    2>/dev/null || true
  case "$runtime" in
    /tmp/lmm-control-admin-listener.*)
      if [[ ${LMM_KEEP_CONTROL_ADMIN_RUNTIME:-0} == 1 ]]; then
        printf 'preserved control-admin runtime: %s\n' "$runtime" >&2
      else
        rm -rf "$runtime"
      fi
      ;;
    *) printf 'refusing unexpected runtime: %s\n' "$runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'printf "control-admin listener differential failed at line %s\n" "$LINENO" >&2' ERR

require_command() {
  command -v "$1" >/dev/null || {
    printf 'required command is unavailable: %s\n' "$1" >&2
    exit 1
  }
}

require_free_port() {
  local port=$1
  if ss -ltnH "sport = :$port" | grep -q .; then
    printf 'refusing occupied listener port: %s\n' "$port" >&2
    exit 1
  fi
}

parse_test_dsn() {
  local name=$1 dsn=$2 output_name=$3
  local rest authority_path authority host_port database query
  case "$dsn" in
    postgresql://*) rest=${dsn#postgresql://} ;;
    postgres://*) rest=${dsn#postgres://} ;;
    *)
      printf '%s must be a postgres:// or postgresql:// URI\n' "$name" >&2
      exit 2
      ;;
  esac
  [[ $rest != *'#'* ]] || {
    printf '%s must not contain a URI fragment\n' "$name" >&2
    exit 2
  }
  authority_path=${rest%%\?*}
  query=
  [[ $rest == "$authority_path" ]] || query=${rest#"$authority_path"}
  authority=${authority_path%%/*}
  database=${authority_path#*/}
  [[ $database != "$authority_path" && $database != */* && $database =~ ^[A-Za-z0-9_]+$ ]] || {
    printf '%s must contain one plain database name\n' "$name" >&2
    exit 2
  }
  host_port=${authority##*@}
  [[ $host_port =~ ^(127\.0\.0\.1|localhost)(:([0-9]{1,5}))?$ ]] || {
    printf '%s must target an explicit localhost PostgreSQL listener\n' "$name" >&2
    exit 2
  }
  [[ $database == lmm_test_control_admin_* ]] || {
    printf '%s database name must begin lmm_test_control_admin_\n' "$name" >&2
    exit 2
  }
  [[ $query != *search_path* && $query != *options=* ]] || {
    printf '%s must not predefine search_path/options; the harness owns it\n' "$name" >&2
    exit 2
  }
  printf -v "$output_name" '%s' "$database"
}

require_unique_schema() {
  local name=$1 schema=$2
  [[ $schema =~ ^lmm_test_control_admin_[A-Za-z0-9_]+$ ]] || {
    printf '%s must match lmm_test_control_admin_[A-Za-z0-9_]+\n' "$name" >&2
    exit 2
  }
}

dsn_with_schema() {
  local dsn=$1 schema=$2 separator='?'
  [[ $dsn == *'?'* ]] && separator='&'
  printf '%s%soptions=-csearch_path%%3D%s' "$dsn" "$separator" "$schema"
}

write_valkey_config() {
  local path=$1 port=$2 secret=$3 logfile=$4
  (
    umask 077
    {
      printf 'bind 127.0.0.1\n'
      printf 'protected-mode yes\n'
      printf 'port %s\n' "$port"
      printf 'requirepass %s\n' "$secret"
      printf 'save ""\n'
      printf 'appendonly no\n'
      printf 'daemonize no\n'
      printf 'dir %s\n' "$runtime"
      printf 'logfile %s\n' "$logfile"
    } >"$path"
  )
  [[ $(stat -c '%a' "$path") == 600 ]] || {
    printf 'Valkey config permissions are not 0600: %s\n' "$path" >&2
    exit 2
  }
}

wait_for_valkey() {
  local pid=$1 port=$2 secret=$3 label=$4
  for _ in {1..200}; do
    kill -0 "$pid" 2>/dev/null || {
      printf '%s exited before readiness\n' "$label" >&2
      return 1
    }
    if VALKEYCLI_AUTH=$secret valkey-cli -h 127.0.0.1 -p "$port" ping \
      >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.05
  done
  printf '%s did not become ready\n' "$label" >&2
  return 1
}

wait_for_owned_http() {
  local pid=$1 url=$2 label=$3
  local status=
  for _ in {1..300}; do
    kill -0 "$pid" 2>/dev/null || {
      printf '%s exited before readiness\n' "$label" >&2
      return 1
    }
    status=$(curl -sS -o /dev/null -w '%{http_code}' "$url" || true)
    [[ $status == 200 ]] && return 0
    sleep 0.05
  done
  printf '%s did not become ready (last HTTP %s)\n' "$label" "${status:-none}" >&2
  return 1
}

capture() {
  local prefix=$1
  shift
  curl -sS -D "$runtime/$prefix.headers" -o "$runtime/$prefix.json" \
    -w '%{http_code}' "$@" >"$runtime/$prefix.status"
}

assert_route_matrix_unauthorized() {
  local label=$1 base=$2 index=0 entry method path body prefix
  for entry in "${route_matrix[@]}"; do
    IFS='|' read -r method path body <<<"$entry"
    prefix="$label-matrix-$index"
    if [[ -n $body ]]; then
      capture "$prefix" -X "$method" -H 'content-type: application/json' \
        --data-binary "$body" "$base$path"
    else
      capture "$prefix" -X "$method" "$base$path"
    fi
    grep -qx 401 "$runtime/$prefix.status"
    jq -e '.success == false and .code == "AUTH_UNAUTHORIZED"' \
      "$runtime/$prefix.json" >/dev/null
    jq -S '{success,code}' "$runtime/$prefix.json" >"$runtime/$prefix.auth.json"
    index=$((index + 1))
  done
}

assert_matrix_pair() {
  local index
  for ((index = 0; index < ${#route_matrix[@]}; index++)); do
    diff -u "$runtime/go-matrix-$index.status" "$runtime/rust-matrix-$index.status"
    diff -u "$runtime/go-matrix-$index.auth.json" "$runtime/rust-matrix-$index.auth.json"
  done
}

control_route_identity() {
  local method=$1 path=$2
  path=${path%%\?*}
  case "$method $path" in
    'GET /api/custom-oauth-provider/not-an-id') printf '%s\n' 'GET /api/custom-oauth-provider/:id' ;;
    'PUT /api/custom-oauth-provider/not-an-id') printf '%s\n' 'PUT /api/custom-oauth-provider/:id' ;;
    'DELETE /api/custom-oauth-provider/not-an-id') printf '%s\n' 'DELETE /api/custom-oauth-provider/:id' ;;
    'GET /api/system-task/missing-task') printf '%s\n' 'GET /api/system-task/:task_id' ;;
    'DELETE /api/system-info/instances/missing-node') printf '%s\n' 'DELETE /api/system-info/instances/:node_name' ;;
    *) printf '%s %s\n' "$method" "$path" ;;
  esac
}

seed_root_user() {
  local dsn=$1 schema=$2 sql_file=$3
  # Synthetic bcrypt fixture for the test-only password "password".
  # shellcheck disable=SC2016
  local root_hash='$2a$10$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2'
  (
    umask 077
    {
      printf 'SET search_path TO "%s";\n' "$schema"
      printf '%s\n' 'INSERT INTO users (username,password,display_name,role,status,"group",setting,auth_version,quota)'
      printf "VALUES ('control-admin-root','%s','Control Admin Root',100,1,'default','{}',1,100000000)\n" "$root_hash"
      printf '%s\n' 'ON CONFLICT (username) DO UPDATE SET password=EXCLUDED.password, role=100, status=1, auth_version=1;'
    } >"$sql_file"
  )
  psql "$dsn" -v ON_ERROR_STOP=1 -f "$sql_file" >/dev/null
}

login_root() {
  local label=$1 base=$2
  capture "$label-login" -X POST -H 'content-type: application/json' \
    --data-binary '{"username":"control-admin-root","password":"password"}' \
    "$base/api/user/login"
  grep -qx 200 "$runtime/$label-login.status"
  jq -er 'select(.success == true and (.data.access_token | type == "string")) | .data.access_token' \
    "$runtime/$label-login.json"
}

assert_root_read_surface() {
  local label=$1 base=$2 token=$3 prefix
  prefix="$label-root-list"
  capture "$prefix" -H "authorization: Bearer $token" \
    "$base/api/custom-oauth-provider/"
  grep -qx 200 "$runtime/$prefix.status"
  jq -e '.success == true and (.data | type == "array")' "$runtime/$prefix.json" >/dev/null
  grep -Eqi "^auth-version:[[:space:]]*$(cat "$repo_root/apps/api-go/common/auth_version.txt")[[:space:]]*$" \
    "$runtime/$prefix.headers"
}

for required in curl jq openssl ss stat; do
  require_command "$required"
done
[[ ${#route_matrix[@]} == 14 ]] || {
  printf 'route matrix must contain exactly 14 entries\n' >&2
  exit 2
}
for port in "$go_port" "$rust_port" "$go_valkey_port" "$rust_valkey_port"; do
  require_free_port "$port"
done

if [[ ${LMM_CONTROL_ADMIN_ALLOW_HEAVY:-0} != 1 ]]; then
  jq -cn \
    --arg test control-admin-listener-differential \
    --arg mode preflight \
    --argjson routes "${#route_matrix[@]}" \
    '{test:$test,mode:$mode,routes:$routes,checks:["exclusive-ports","pid-only-cleanup","0600-valkey-config","VALKEYCLI_AUTH","distinct-test-databases","unique-test-schemas","root-auth-seed","fourteen-route-auth-matrix","auth-before-body-query"]}'
  exit 0
fi

for required in psql valkey-cli valkey-server; do
  require_command "$required"
done
: "${LMM_CONTROL_ADMIN_GO_BIN:?set an isolated legacy Go test binary}"
: "${LMM_CONTROL_ADMIN_RUST_BIN:?set an isolated Rust test binary}"
: "${LMM_CONTROL_ADMIN_GO_DATABASE_URL:?set an isolated Go PostgreSQL DSN}"
: "${LMM_CONTROL_ADMIN_RUST_DATABASE_URL:?set an isolated Rust PostgreSQL DSN}"
: "${LMM_CONTROL_ADMIN_GO_SCHEMA:?set a unique Go test schema}"
: "${LMM_CONTROL_ADMIN_RUST_SCHEMA:?set a unique Rust test schema}"

go_database=
rust_database=
parse_test_dsn LMM_CONTROL_ADMIN_GO_DATABASE_URL \
  "$LMM_CONTROL_ADMIN_GO_DATABASE_URL" go_database
parse_test_dsn LMM_CONTROL_ADMIN_RUST_DATABASE_URL \
  "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" rust_database
[[ $go_database != "$rust_database" ]] || {
  printf 'Go and Rust must use distinct PostgreSQL databases\n' >&2
  exit 2
}
require_unique_schema LMM_CONTROL_ADMIN_GO_SCHEMA "$LMM_CONTROL_ADMIN_GO_SCHEMA"
require_unique_schema LMM_CONTROL_ADMIN_RUST_SCHEMA "$LMM_CONTROL_ADMIN_RUST_SCHEMA"
[[ $LMM_CONTROL_ADMIN_GO_SCHEMA != "$LMM_CONTROL_ADMIN_RUST_SCHEMA" ]] || {
  printf 'Go and Rust must use distinct PostgreSQL schemas\n' >&2
  exit 2
}
[[ -x $LMM_CONTROL_ADMIN_GO_BIN && -x $LMM_CONTROL_ADMIN_RUST_BIN ]] || {
  printf 'listener binaries must be executable\n' >&2
  exit 2
}

go_schema_exists=$(psql "$LMM_CONTROL_ADMIN_GO_DATABASE_URL" -qAt -v ON_ERROR_STOP=1 \
  -c "SELECT 1 FROM pg_namespace WHERE nspname='$LMM_CONTROL_ADMIN_GO_SCHEMA'")
rust_schema_exists=$(psql "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" -qAt -v ON_ERROR_STOP=1 \
  -c "SELECT 1 FROM pg_namespace WHERE nspname='$LMM_CONTROL_ADMIN_RUST_SCHEMA'")
[[ -z $go_schema_exists && -z $rust_schema_exists ]] || {
  printf 'test schemas must be unique and previously absent\n' >&2
  exit 2
}
psql "$LMM_CONTROL_ADMIN_GO_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -c "CREATE SCHEMA \"$LMM_CONTROL_ADMIN_GO_SCHEMA\"" >/dev/null
psql "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -c "CREATE SCHEMA \"$LMM_CONTROL_ADMIN_RUST_SCHEMA\"" >/dev/null

# The Go listener migrates its isolated schema on first boot. Rust is tested
# against the immutable contract-1 PostgreSQL baseline plus the forward bounty
# migration, so provision that same contract before starting the listener.
sed "s/public\\./$LMM_CONTROL_ADMIN_RUST_SCHEMA./g" \
  "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" \
  >"$runtime/rust-baseline.sql"
psql "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f "$runtime/rust-baseline.sql" >/dev/null
sed "s/__LMM_APP_SCHEMA__/$LMM_CONTROL_ADMIN_RUST_SCHEMA/g" \
  "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
  >"$runtime/rust-bounty-forward.sql"
psql "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" -v ON_ERROR_STOP=1 \
  -f "$runtime/rust-bounty-forward.sql" >/dev/null
psql "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" -v ON_ERROR_STOP=1 <<SQL >/dev/null
CREATE TABLE "$LMM_CONTROL_ADMIN_RUST_SCHEMA".lmm_schema_contract (
  singleton BOOLEAN PRIMARY KEY,
  min_reader_version BIGINT NOT NULL,
  max_reader_version BIGINT NOT NULL
);
INSERT INTO "$LMM_CONTROL_ADMIN_RUST_SCHEMA".lmm_schema_contract
  VALUES (TRUE, 1, 1);
GRANT USAGE, CREATE ON SCHEMA "$LMM_CONTROL_ADMIN_RUST_SCHEMA"
  TO CURRENT_USER;
GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA "$LMM_CONTROL_ADMIN_RUST_SCHEMA"
  TO CURRENT_USER;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA "$LMM_CONTROL_ADMIN_RUST_SCHEMA"
  TO CURRENT_USER;
SQL
go_database_url=$(dsn_with_schema "$LMM_CONTROL_ADMIN_GO_DATABASE_URL" \
  "$LMM_CONTROL_ADMIN_GO_SCHEMA")
rust_database_url=$(dsn_with_schema "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" \
  "$LMM_CONTROL_ADMIN_RUST_SCHEMA")

go_valkey_config="$runtime/go-valkey.conf"
rust_valkey_config="$runtime/rust-valkey.conf"
write_valkey_config "$go_valkey_config" "$go_valkey_port" "$go_valkey_secret" \
  "$runtime/go-valkey.log"
write_valkey_config "$rust_valkey_config" "$rust_valkey_port" "$rust_valkey_secret" \
  "$runtime/rust-valkey.log"
valkey-server "$go_valkey_config" >"$runtime/go-valkey.stdout" 2>&1 &
go_valkey_pid=$!
valkey-server "$rust_valkey_config" >"$runtime/rust-valkey.stdout" 2>&1 &
rust_valkey_pid=$!
wait_for_valkey "$go_valkey_pid" "$go_valkey_port" "$go_valkey_secret" go-valkey
wait_for_valkey "$rust_valkey_pid" "$rust_valkey_port" "$rust_valkey_secret" rust-valkey

SQL_DSN="$go_database_url" PORT="$go_port" \
  REDIS_CONN_STRING="redis://:$go_valkey_secret@127.0.0.1:$go_valkey_port" \
  SESSION_SECRET="$go_valkey_secret" PASSWORD_LOGIN_ENABLED=true GIN_MODE=release \
  "$LMM_CONTROL_ADMIN_GO_BIN" >"$runtime/go.log" 2>&1 &
go_pid=$!
DATABASE_URL="$rust_database_url" \
  VALKEY_URL="redis://:$rust_valkey_secret@127.0.0.1:$rust_valkey_port" \
  SESSION_SECRET="$rust_session_secret" PASSWORD_LOGIN_ENABLED=true \
  LMM_RS_TEST_INSTANCE=1 LMM_RS_SLOT=single LMM_SCHEMA_CONTRACT=1 \
  LMM_RS_TEST_VALKEY_PORT="$rust_valkey_port" \
  LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port" \
  "$LMM_CONTROL_ADMIN_RUST_BIN" >"$runtime/rust.log" 2>&1 &
rust_pid=$!

wait_for_owned_http "$go_pid" "http://127.0.0.1:$go_port/api/status" legacy-go
wait_for_owned_http "$rust_pid" "http://127.0.0.1:$rust_port/readyz" rust

# Authentication must win over malformed JSON/path/query input on every route.
assert_route_matrix_unauthorized go "http://127.0.0.1:$go_port"
assert_route_matrix_unauthorized rust "http://127.0.0.1:$rust_port"
assert_matrix_pair

if [[ -n $result_dir ]]; then
  index=0
  for entry in "${route_matrix[@]}"; do
    IFS='|' read -r method concrete_path body <<<"$entry"
    route=$(control_route_identity "$method" "$concrete_path")
    route_method=${route%% *}
    route_path=${route#* }
    index=$((index + 1))
    jq -cn --arg method "$route_method" --arg path "$route_path" \
      --argjson cases 1 --arg scope auth-matrix \
      '{method:$method,path:$path,differential_verified:false,transport_boundary_verified:true,differential_scope:$scope,cases:$cases,approval_credit:false,differences:null,mismatch_names:[]}' \
      >"$result_dir/control-admin-$index.json"
  done
fi

# Seed equivalent root identities only after each isolated listener has created
# its own schema. The seed and captured tokens remain inside the 0700 runtime.
seed_root_user "$LMM_CONTROL_ADMIN_GO_DATABASE_URL" "$LMM_CONTROL_ADMIN_GO_SCHEMA" \
  "$runtime/go-root-seed.sql"
seed_root_user "$LMM_CONTROL_ADMIN_RUST_DATABASE_URL" "$LMM_CONTROL_ADMIN_RUST_SCHEMA" \
  "$runtime/rust-root-seed.sql"
go_token=$(login_root go "http://127.0.0.1:$go_port")
rust_token=$(login_root rust "http://127.0.0.1:$rust_port")
assert_root_read_surface go "http://127.0.0.1:$go_port" "$go_token"
assert_root_read_surface rust "http://127.0.0.1:$rust_port" "$rust_token"

jq -cn \
  '{test:"control-admin-listener-differential",mode:"isolated-tcp",routes:14,checks:["pid-aware-readiness","fourteen-route-auth-matrix","root-auth-seed","auth-version","no-external-targets"],result:"passed"}'
