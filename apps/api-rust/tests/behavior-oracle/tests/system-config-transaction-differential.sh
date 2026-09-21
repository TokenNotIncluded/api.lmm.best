#!/usr/bin/env bash
# Isolated real-TCP differential for the mounted control/config routes.
# Frozen-contract routes use the legacy revision by default. Current-only
# route groups may opt into an explicitly pinned, externally materialized Go
# oracle whose revision marker is verified before the listeners start.
#
# This runner intentionally has no remote gateway inputs.  By default it
# starts the normal Rust listener, whose project-update/Pancake adapters are
# concrete but are not exercised by the write-boundary case below.  Set
# LMM_SYSTEM_CONFIG_RUST_TEST_INSTANCE=1 only when you explicitly want the
# historical deny-only candidate; that mode is expected to expose a mismatch.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
oracle_revision=${LMM_GO_ORACLE_REVISION:-$legacy_revision}
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ $oracle_revision =~ ^[0-9a-f]{40}$ ]] || { echo 'LMM_GO_ORACLE_REVISION must be a full lowercase Git commit' >&2; exit 2; }
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($oracle_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
if [[ $oracle_revision != "$legacy_revision" ]]; then
  [[ ${LMM_SYSTEM_CONFIG_ALLOW_CURRENT_ORACLE:-0} == 1 || ${LMM_SYSTEM_CONFIG_ROUTE_GROUP:-all} == open-source ]] || {
    echo 'a non-legacy Go oracle requires LMM_SYSTEM_CONFIG_ALLOW_CURRENT_ORACLE=1 (or the current-only open-source route group)' >&2
    exit 2
  }
  revision_marker="$legacy_root/.lmm-go-oracle-revision"
  [[ -f $revision_marker && ! -L $revision_marker ]] || {
    echo "non-legacy Go oracle is missing its revision marker: $revision_marker" >&2
    exit 2
  }
  [[ $(<"$revision_marker") == "$oracle_revision" ]] || {
    echo "Go oracle revision marker does not match $oracle_revision" >&2
    exit 2
  }
fi
runtime_base=${LMM_SYSTEM_CONFIG_RUNTIME_BASE:-/tmp}
pg_port=${LMM_SYSTEM_CONFIG_PG_PORT:-55468}
go_port=${LMM_SYSTEM_CONFIG_GO_PORT:-13038}
rust_port=${LMM_SYSTEM_CONFIG_RUST_PORT:-33068}
valkey_requested_port=${LMM_SYSTEM_CONFIG_VALKEY_PORT:-6381}
rust_test_instance=${LMM_SYSTEM_CONFIG_RUST_TEST_INSTANCE:-0}
relay_decode_differential=${LMM_SYSTEM_CONFIG_RELAY_DECODE_DIFFERENTIAL:-0}
runtime=$(mktemp -d "$runtime_base/lmm-system-config-differential.XXXXXX")
cargo_target=${LMM_SYSTEM_CONFIG_CARGO_TARGET_DIR:-"$runtime/cargo-target"}
rust_binary=${LMM_SYSTEM_CONFIG_RUST_BINARY:-"$cargo_target/debug/lmm-api-rs"}
result_dir=${LMM_SYSTEM_CONFIG_RESULT_DIR:-}
if [[ -n $result_dir ]]; then
  [[ $result_dir == /* && $result_dir != *..* ]] || {
    echo 'LMM_SYSTEM_CONFIG_RESULT_DIR must be an absolute path without ..' >&2
    exit 2
  }
  mkdir -p "$result_dir"
fi
go_schema=lmm_test_system_config_go
rust_schema=lmm_test_system_config_rust
go_role=lmm_test_system_config_go
rust_role=lmm_test_system_config_rust
database=lmm_test_system_config
valkey_password=$(openssl rand -hex 32)
# shellcheck disable=SC2034 # Read indirectly through the PID variable names.
go_pid='' rust_pid='' valkey_pid='' go_pid_start='' rust_pid_start='' valkey_pid_start=''
declare -A route_case_count=()
declare -A route_result_file=()
declare -A existing_route_result=()
next_system_config_result_index=1
if [[ -n $result_dir ]]; then
  shopt -s nullglob
  for existing_file in "$result_dir"/*.json; do
    existing_basename=${existing_file##*/}
    if [[ $existing_basename =~ ^system-config-([0-9]+)\.json$ ]]; then
      existing_index=${BASH_REMATCH[1]}
      if (( existing_index >= next_system_config_result_index )); then
        next_system_config_result_index=$((existing_index + 1))
      fi
    fi
    existing_method=$(jq -r '.method // empty' "$existing_file" 2>/dev/null || true)
    existing_path=$(jq -r '.path // empty' "$existing_file" 2>/dev/null || true)
    if [[ -n $existing_method && -n $existing_path ]]; then
      existing_route_result["$existing_method\t$existing_path"]=$existing_file
    fi
  done
  shopt -u nullglob
fi

cleanup() {
  for pid_name in go_pid rust_pid valkey_pid; do
    stop_owned_process "$pid_name" || true
  done
  if [[ -d $runtime/pg ]]; then
    pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  fi
  if [[ ${LMM_KEEP_SYSTEM_CONFIG_RUNTIME:-0} == 1 ]]; then
    echo "retaining stopped system-config runtime: $runtime" >&2
    return
  fi
  case "$runtime" in
    "$runtime_base"/lmm-system-config-differential.*)
      chmod -R u+w "$runtime/go-build" 2>/dev/null || true
      rm -rf "$runtime"
      ;;
    *) echo "refusing unexpected runtime cleanup target: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM

for command in cargo createdb curl git go initdb jq openssl pg_ctl postgres psql ss valkey-cli valkey-server; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 127; }
done
[[ $(postgres --version) == *"PostgreSQL) 18."* ]] || { echo "requires PostgreSQL 18" >&2; exit 1; }
[[ -d $legacy_root ]] || { echo "missing frozen Go source: $legacy_root" >&2; exit 1; }
[[ -d $runtime_base && -w $runtime_base ]] || { echo "runtime base is not writable: $runtime_base" >&2; exit 1; }
pid_start_time() { [[ -r /proc/$1/stat ]] || return 1; awk '{print $22}' "/proc/$1/stat"; }
record_pid() { local pid_name=$1 pid=$2 start; printf -v "$pid_name" '%s' "$pid"; start=$(pid_start_time "$pid") || { echo "failed to record pid $pid" >&2; wait "$pid" 2>/dev/null || true; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; return 1; }; printf -v "${pid_name}_start" '%s' "$start"; }
owned_pid_is_live() { local pid_name=$1 pid start_name expected; pid=${!pid_name:-}; start_name="${pid_name}_start"; expected=${!start_name:-}; [[ -n $pid && -n $expected ]] && kill -0 "$pid" 2>/dev/null && [[ $(pid_start_time "$pid" 2>/dev/null || true) == "$expected" ]]; }
stop_owned_process() { local pid_name=$1 pid; pid=${!pid_name:-}; if [[ -n $pid ]]; then if owned_pid_is_live "$pid_name"; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; else echo "refusing to signal unowned or recycled PID $pid ($pid_name)" >&2; fi; fi; printf -v "$pid_name" ''; printf -v "${pid_name}_start" ''; }
port_free() { [[ -z $(ss -H -ltn "sport = :$1" 2>/dev/null) ]]; }
random_free_port() { local p; while :; do p=$((20000 + 0x$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 35000)); [[ -z $(ss -H -ltn "sport = :$p" 2>/dev/null) ]] && { echo "$p"; return; }; done; }
select_unused_port() {
  local requested=$1 label=$2 candidate
  if port_free "$requested"; then echo "$requested"; return 0; fi
  echo "requested $label port $requested is occupied, selecting a free port" >&2
  for _ in {1..200}; do
    candidate=$(random_free_port)
    if port_free "$candidate"; then echo "$candidate"; return 0; fi
  done
  return 1
}
for entry in \
  "postgres:$pg_port" "go:$go_port" "rust:$rust_port"; do
  name=${entry%%:*}; port=${entry##*:}
  if ss -ltn "sport = :$port" | grep -q LISTEN; then
    echo "refusing occupied $name port: $port" >&2
    exit 1
  fi
done
valkey_port=$(select_unused_port "$valkey_requested_port" system-config-valkey) || { echo "unable to allocate an unused valkey port" >&2; exit 1; }
[[ $valkey_port == "$valkey_requested_port" ]] || echo "using fallback valkey port: $valkey_port" >&2

wait_for_pid_http() {
  local pid=$1 port=$2 path=$3
  # Cold Go migration on the full PostgreSQL baseline can exceed 30s. Keep
  # polling the owned PID and loopback endpoint for a bounded 120s instead of
  # classifying startup latency as route parity failure.
  for _ in {1..2400}; do
    kill -0 "$pid" 2>/dev/null || return 1
    curl --silent --output /dev/null "http://127.0.0.1:$port$path" && return 0
    sleep 0.05
  done
  return 1
}

admin_sql() {
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$1"
}

schema_sql() {
  local engine=$1 statement=$2 schema role
  case "$engine" in
    go) schema=$go_schema; role=$go_role ;;
    rust) schema=$rust_schema; role=$rust_role ;;
    *) return 2 ;;
  esac
  PGOPTIONS="-c search_path=$schema" \
    psql -h 127.0.0.1 -p "$pg_port" -U "$role" -d "$database" -qAt -v ON_ERROR_STOP=1 -c "$statement"
}

start_listeners() {
  local go_dsn rust_dsn
  go_dsn="postgresql://$go_role@127.0.0.1:$pg_port/$database?sslmode=disable&options=-csearch_path=$go_schema"
  rust_dsn="postgresql://$rust_role@127.0.0.1:$pg_port/$database?options=-csearch_path=$rust_schema"
  SQL_DSN="$go_dsn" PORT="$go_port" REDIS_CONN_STRING="redis://:$valkey_password@127.0.0.1:$valkey_port/5" \
    SESSION_SECRET='SystemConfigOracle-2026-SyntheticOnly' CRYPTO_SECRET='SystemConfigOracle-Crypto-SyntheticOnly' \
    PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false GIN_MODE=release \
    "$runtime/go-build/legacy-go" >"$runtime/go.log" 2>&1 & record_pid go_pid "$!" || exit 1
  wait_for_pid_http "$go_pid" "$go_port" /api/status || { sed -n '1,220p' "$runtime/go.log" >&2; return 1; }
  rust_env=(
    LMM_RS_SLOT=blue LMM_RS_LISTEN_ADDR="127.0.0.1:$rust_port"
    DATABASE_URL="$rust_dsn" VALKEY_URL="redis://:$valkey_password@127.0.0.1:$valkey_port/6" LMM_SCHEMA_CONTRACT=1
    SESSION_SECRET='SystemConfigOracle-2026-SyntheticOnly' CRYPTO_SECRET='SystemConfigOracle-Crypto-SyntheticOnly'
    PASSWORD_LOGIN_ENABLED=true GLOBAL_API_RATE_LIMIT_ENABLE=false CRITICAL_RATE_LIMIT_ENABLE=false TRUSTED_PROXIES=none VERSION=v0.0.0
  )
  if [[ $rust_test_instance == 1 ]]; then
    rust_env+=(LMM_RS_TEST_INSTANCE=1 LMM_RS_SLOT=single LMM_RS_TEST_VALKEY_PORT="$valkey_port")
  fi
  env -u LMM_RS_TEST_INSTANCE "${rust_env[@]}" \
    "$rust_binary" >"$runtime/rust.log" 2>&1 & record_pid rust_pid "$!" || exit 1
  wait_for_pid_http "$rust_pid" "$rust_port" /readyz || { sed -n '1,220p' "$runtime/rust.log" >&2; return 1; }
}

call() {
  local engine=$1 name=$2 method=$3 path=$4 body=${5:-} token=${6:-} port prefix
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  prefix="$runtime/$engine.$name"
  local args=(--silent --show-error --dump-header "$prefix.headers" --output "$prefix.body" --write-out '%{http_code}' --request "$method")
  [[ -z $token ]] || args+=(--header "authorization: Bearer $token")
  [[ -z $body ]] || args+=(--header 'content-type: application/json' --data-binary "$body")
  curl "${args[@]}" "http://127.0.0.1:$port$path" >"$prefix.status"
  jq -e . <"$prefix.body" >/dev/null
}

call_tip() {
  local engine=$1 name=$2 body=$3 token=$4 key=$5 port prefix
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  prefix="$runtime/$engine.$name"
  curl --silent --show-error --dump-header "$prefix.headers" --output "$prefix.body" \
    --write-out '%{http_code}' --request POST \
    --header "authorization: Bearer $token" \
    --header 'content-type: application/json' \
    --header "Idempotency-Key: $key" \
    --data-binary "$body" "http://127.0.0.1:$port/api/open-source-bounties/challenges/99/tip" >"$prefix.status"
  jq -e . <"$prefix.body" >/dev/null
}

normalize_tip_body() {
  jq -S 'walk(if type == "object" then del(.created_at, .updated_at, .accepted_at, .submitted_at, .reviewed_at, .rejected_at, .paid_at, .owner_rated_at, .contributor_rated_at) else . end)' "$1"
}

canonical_from_route_tsv() {
  local route_tsv=$1 method=$2 request_path=$3 clean_path=${request_path%%\?*}
  awk -F '\t' -v method="$method" -v path="$clean_path" '
    function parameter_count(value, copy, count) {
      copy = value
      count = gsub(/:[A-Za-z_][A-Za-z0-9_]*/, "", copy)
      count += gsub(/\{[A-Za-z_][A-Za-z0-9_]*\}/, "", copy)
      return count
    }
    NR == 1 { next }
    $1 != method { next }
    $2 == path { print $2; found=1; exit }
    {
      pattern=$2
      gsub(/:[A-Za-z_][A-Za-z0-9_]*/, "[^/]+", pattern)
      gsub(/\{[A-Za-z_][A-Za-z0-9_]*\}/, "[^/]+", pattern)
      if (path ~ ("^" pattern "$")) {
        params=parameter_count($2)
        if (!best || params < best_params || (params == best_params && length($2) > length(best))) {
          best=$2
          best_params=params
        }
      }
    }
    END {
      if (found) exit 0
      if (best) { print best; exit 0 }
      exit 1
    }
  ' "$route_tsv"
}

canonical_legacy_path() {
  local method=$1 request_path=$2 canonical
  canonical=$(canonical_from_route_tsv \
    "$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv" \
    "$method" "$request_path" 2>/dev/null) && {
    printf '%s\n' "$canonical"
    return 0
  }
  # Current-only routes (for example the open-source bounty domain) are not
  # present in the frozen contract-1 ledger. Use the audited normal-mount
  # ledger as their canonical route inventory so live evidence still joins
  # the current Go route manifest.
  canonical=$(canonical_from_route_tsv \
    "$repo_root/apps/api-rust/tests/fixtures/routes/rust-normal-mounted-routes.tsv" \
    "$method" "$request_path" 2>/dev/null) && {
    printf '%s\n' "$canonical"
    return 0
  }
  return 1
}

record_pair_result() {
  local name=$1 method=$2 request_path=$3 canonical route_key file_index file_path cases
  [[ -n $result_dir ]] || return 0
  canonical=$(canonical_legacy_path "$method" "$request_path" 2>/dev/null || true)
  [[ -n $canonical ]] || return 0
  route_key="$method\t$canonical"
  [[ -z ${existing_route_result[$route_key]+x} ]] || return 0
  route_case_count[$route_key]=$(( ${route_case_count[$route_key]:-0} + 1 ))
  if [[ -z ${route_result_file[$route_key]+x} ]]; then
    file_index=$next_system_config_result_index
    next_system_config_result_index=$((next_system_config_result_index + 1))
    file_path="$result_dir/system-config-$file_index.json"
    route_result_file[$route_key]=$file_path
  fi
  file_path=${route_result_file[$route_key]}
  cases=${route_case_count[$route_key]}
  jq -cn --arg method "$method" --arg path "$canonical" --arg route "$name" --argjson cases "$cases" \
    '{method:$method,path:$path,differential_verified:true,differential_scope:"system-config",cases:$cases,route_fixture:$route,postgres_valkey_isolated:true,approval_credit:false,differences:null,mismatch_names:[]}' \
    >"$file_path"
}

pair_tip() {
  local name=$1 body=$2 token_go=$3 token_rust=$4 key=$5
  case_count=$((case_count + 1))
  call_tip go "$name" "$body" "$token_go" "$key"
  call_tip rust "$name" "$body" "$token_rust" "$key"
  diff -u "$runtime/go.$name.status" "$runtime/rust.$name.status"
  normalize_tip_body "$runtime/go.$name.body" >"$runtime/go.$name.normalized"
  normalize_tip_body "$runtime/rust.$name.body" >"$runtime/rust.$name.normalized"
  diff -u "$runtime/go.$name.normalized" "$runtime/rust.$name.normalized"
  record_pair_result "$name" POST /api/open-source-bounties/challenges/99/tip
}

pair_open_source_read() {
  local name=$1 method=$2 path=$3 body=${4:-} token_go=${5:-} token_rust=${6:-}
  case_count=$((case_count + 1))
  call go "$name" "$method" "$path" "$body" "$token_go"
  call rust "$name" "$method" "$path" "$body" "$token_rust"
  diff -u "$runtime/go.$name.status" "$runtime/rust.$name.status"
  normalize_tip_body "$runtime/go.$name.body" >"$runtime/go.$name.normalized"
  normalize_tip_body "$runtime/rust.$name.body" >"$runtime/rust.$name.normalized"
  diff -u "$runtime/go.$name.normalized" "$runtime/rust.$name.normalized"
  record_pair_result "$name" "$method" "$path"
}

pair() {
  local name=$1 method=$2 path=$3 body=${4:-} token_go=${5:-} token_rust=${6:-}
  if [[ ${LMM_SYSTEM_CONFIG_ROUTE_GROUP:-all} == open-source && $name != open-source-* ]]; then
    return 0
  fi
  case_count=$((case_count + 1))
  call go "$name" "$method" "$path" "$body" "$token_go"
  call rust "$name" "$method" "$path" "$body" "$token_rust"
  diff -u "$runtime/go.$name.status" "$runtime/rust.$name.status"
  # Each listener owns its request-id generator. Normalize only the dynamic
  # suffix embedded in legacy relay errors; all envelope fields remain exact.
  diff -u \
    <(sed -E 's/\(request id: [^)]+\)/(request id: <REQUEST_ID>)/g' "$runtime/go.$name.body" | jq -S .) \
    <(sed -E 's/\(request id: [^)]+\)/(request id: <REQUEST_ID>)/g' "$runtime/rust.$name.body" | jq -S .)
  record_pair_result "$name" "$method" "$path"
}

rust_current_relay_mount() {
  local name=$1 method=$2 path=$3 body=${4:-}
  rust_mount_count=$((rust_mount_count + 1))
  local prefix="$runtime/rust.$name"
  local args=(--silent --show-error --dump-header "$prefix.headers" --output "$prefix.body" \
    --write-out '%{http_code}' --request "$method" \
    --header 'content-type: application/json' --header 'content-encoding: gzip' \
    --data-binary 'not-a-gzip-body')
  curl "${args[@]}" "http://127.0.0.1:$rust_port$path" >"$prefix.status"
  [[ $(<"$runtime/rust.$name.status") == 400 ]] || {
    echo "Rust current relay mount $name returned $(<"$runtime/rust.$name.status")" >&2
    sed -n '1,80p' "$prefix.body" >&2
    return 1
  }
  [[ ! -s "$prefix.body" ]] || {
    echo "Rust current relay mount $name did not reach the relay decoding boundary (expected empty 400)" >&2
    return 1
  }
  if [[ $relay_decode_differential == 1 && $method == POST ]]; then
    local go_prefix="$runtime/go.$name"
    curl --silent --show-error --dump-header "$go_prefix.headers" --output "$go_prefix.body" \
      --write-out '%{http_code}' --request "$method" \
      --header 'content-type: application/json' --header 'content-encoding: gzip' \
      --data-binary 'not-a-gzip-body' "http://127.0.0.1:$go_port$path" >"$go_prefix.status"
    [[ $(<"$go_prefix.status") == 400 ]] || {
      echo "Go invalid-gzip oracle $name returned $(<"$go_prefix.status")" >&2
      sed -n '1,80p' "$go_prefix.body" >&2
      return 1
    }
    if [[ $(<"$prefix.status") != $(<"$go_prefix.status") ]] || ! cmp -s "$prefix.body" "$go_prefix.body"; then
      relay_decode_mismatch_count=$((relay_decode_mismatch_count + 1))
    fi
  fi
}

login() {
  local engine=$1 port
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  curl --silent --show-error --header 'content-type: application/json' \
    --data '{"username":"root","password":"password"}' "http://127.0.0.1:$port/api/user/login" |
    jq -er 'select(.success == true) | .data.access_token | strings'
}

login_as() {
  local engine=$1 username=$2 port
  [[ $engine == go ]] && port=$go_port || port=$rust_port
  curl --silent --show-error --header 'content-type: application/json' \
    --data "{\"username\":\"$username\",\"password\":\"password\"}" "http://127.0.0.1:$port/api/user/login" |
    jq -er 'select(.success == true) | .data.access_token | strings'
}

mkdir -p "$runtime/go-build/go-source"
cp -a "$legacy_root/." "$runtime/go-build/go-source"
chmod -R u+w "$runtime/go-build/go-source"
mkdir -p "$runtime/go-build/go-source/web/dist"
: >"$runtime/go-build/go-source/web/dist/index.html"
if [[ -n ${LMM_SYSTEM_CONFIG_RUST_BINARY:-} ]]; then
  [[ -x $rust_binary ]] || {
    echo "LMM_SYSTEM_CONFIG_RUST_BINARY is not executable: $rust_binary" >&2
    exit 2
  }
else
  TMPDIR="$runtime" CARGO_TARGET_DIR="$cargo_target" cargo build --manifest-path "$repo_root/apps/api-rust/Cargo.toml" -p lmm-api-rs --locked
fi
[[ -x $rust_binary ]] || { echo "Rust test-instance binary unavailable: $rust_binary" >&2; exit 1; }
(cd "$runtime/go-build/go-source" && GOTOOLCHAIN=local CGO_ENABLED=1 go build -buildvcs=false -o "$runtime/go-build/legacy-go" .)

initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
createdb -h 127.0.0.1 -p "$pg_port" "$database"
admin_sql "CREATE ROLE $go_role LOGIN; CREATE ROLE $rust_role LOGIN; CREATE SCHEMA $go_schema; CREATE SCHEMA $rust_schema;"
for schema in "$go_schema" "$rust_schema"; do
  sed "s/public\\./$schema./g" "$repo_root/apps/api-rust/crates/lmm-db-migrate/schema/postgresql-baseline.sql" >"$runtime/$schema.sql"
  PGOPTIONS="-c search_path=$schema" psql -h 127.0.0.1 -p "$pg_port" -d "$database" -q -v ON_ERROR_STOP=1 -f "$runtime/$schema.sql" >/dev/null
  admin_sql "CREATE TABLE $schema.lmm_schema_contract (singleton BOOLEAN PRIMARY KEY, min_reader_version BIGINT NOT NULL, max_reader_version BIGINT NOT NULL); INSERT INTO $schema.lmm_schema_contract VALUES(TRUE,1,1);"
done
# Contract 1 is deliberately frozen. Provision the same contract-2 bounty
# schema in both disposable listener schemas before either process starts.
# Relying on Go AutoMigrate here races the fixture seeding phase and can leave
# the Go schema without the tables even though the listener has started.
for schema in "$go_schema" "$rust_schema"; do
  sed "s/__LMM_APP_SCHEMA__/$schema/g" \
    "$repo_root/apps/api-rust/migrations/0002_open_source_bounty_schema.sql" \
    >"$runtime/$schema-open-source-bounty-schema.sql"
  psql -h 127.0.0.1 -p "$pg_port" -d "$database" -q -v ON_ERROR_STOP=1 \
    -f "$runtime/$schema-open-source-bounty-schema.sql" >/dev/null
done
# Explicit ownership is required because each listener runs an isolated schema.
for pair_value in "$go_schema:$go_role" "$rust_schema:$rust_role"; do
  schema=${pair_value%%:*}; role=${pair_value##*:}
  # ALTER TABLE also transfers ownership of sequences declared OWNED BY that
  # table.  Altering a linked sequence first is rejected by PostgreSQL 18, so
  # keep the ownership pass table-only and let the dependency do the rest.
  admin_sql "DO \$\$ DECLARE r record; BEGIN FOR r IN SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='$schema' AND c.relkind = 'r' LOOP EXECUTE format('ALTER TABLE %I.%I OWNER TO $role', '$schema', r.relname); END LOOP; END \$\$; ALTER SCHEMA $schema OWNER TO $role;"
done
valkey-server --bind 127.0.0.1 --port "$valkey_port" --requirepass "$valkey_password" --save '' --appendonly no --dir "$runtime" --logfile "$runtime/valkey.log" > /dev/null 2>&1 & record_pid valkey_pid "$!" || exit 1
for _ in {1..100}; do
  kill -0 "$valkey_pid" 2>/dev/null || { sed -n '1,220p' "$runtime/valkey.log" >&2; exit 1; }
  valkey-cli -h 127.0.0.1 -p "$valkey_port" --pass "$valkey_password" ping >/dev/null 2>&1 && break
  sleep 0.05
done
valkey-cli -h 127.0.0.1 -p "$valkey_port" --pass "$valkey_password" ping >/dev/null

for engine in go rust; do
  schema_sql "$engine" "INSERT INTO users (id,username,password,display_name,role,status,\"group\",setting,auth_version,quota,aff_quota) VALUES (1,'root','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','Root User',100,1,'default','{}',1,100000000,0);"
  schema_sql "$engine" "INSERT INTO users (id,username,password,display_name,role,status,\"group\",setting,auth_version,quota,aff_quota) VALUES (2,'bounty-contributor','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','Bounty Contributor',1,1,'default','{}',1,0,0);"
  schema_sql "$engine" "INSERT INTO users (id,username,password,display_name,role,status,\"group\",setting,auth_version,quota,aff_quota) VALUES (3,'bounty-admin','\$2a\$10\$5Rm09lSOGBsP.6RiFTuleun103cKGxh/grNS/rcy7HPxJDvY9EEt2','Bounty Admin',10,1,'default','{}',1,0,0);"
done
start_listeners
case_count=0
rust_mount_count=0
relay_decode_mismatch_count=0
pair setup-public GET /api/setup
pair setup-post-initialized POST /api/setup '{"username":"setup-probe","password":"password","confirmPassword":"password","SelfUseModeEnabled":false,"DemoSiteEnabled":false}'
go_token=$(login go)
rust_token=$(login rust)
pair runtime-write-boundary PUT /api/option/ '{"key":"SystemConfigOracleProbe","value":"after"}' "$go_token" "$rust_token"
pair control-task-all GET /api/mj/ '' "$go_token" "$rust_token"
pair control-task-self GET /api/mj/self '' "$go_token" "$rust_token"
pair control-task-generic GET /api/task/self '' "$go_token" "$rust_token"
pair control-status-test GET /api/status/test '' "$go_token" "$rust_token"
pair ratio-sync-channels GET /api/ratio_sync/channels '' "$go_token" "$rust_token"
pair billing-dashboard-unauth GET /dashboard/billing/subscription
pair billing-dashboard-v1-unauth GET /v1/dashboard/billing/usage
pair channel-advanced-unauth GET /api/channel/test
pair channel-advanced-id-unauth GET /api/channel/test/1
pair channel-advanced-fetch-unauth POST /api/channel/fetch_models '{}'
pair deployment-unauth GET /api/deployments/settings
pair deployment-list-unauth GET /api/deployments/
pair deployment-create-unauth POST /api/deployments/ '{}'
pair topup-list-unauth GET /api/user/topup
pair topup-redeem-unauth POST /api/user/topup '{"key":""}'
pair topup-complete-unauth POST /api/user/topup/complete '{"trade_no":""}'
pair twofa-backup-codes-unauth POST /api/user/2fa/backup_codes '{}'
pair twofa-disable-unauth POST /api/user/2fa/disable '{}'
pair twofa-enable-unauth POST /api/user/2fa/enable '{}'
pair twofa-setup-unauth POST /api/user/2fa/setup '{}'
pair twofa-stats-unauth GET /api/user/2fa/stats
pair twofa-status-unauth GET /api/user/2fa/status
pair twofa-admin-disable-unauth DELETE /api/user/1/2fa
pair control-public-groups-unauth GET /api/group/
pair control-public-models-unauth GET /api/models
pair control-public-pricing GET /api/pricing
pair control-public-rankings GET '/api/rankings?period=week'
pair control-public-ratio GET /api/ratio_config
pair control-public-token-usage-unauth GET /api/usage/token/
pair channel-core-list-unauth GET /api/channel/
pair channel-core-create-unauth POST /api/channel/ '{}'
pair channel-core-update-unauth PUT /api/channel/ '{}'
pair channel-core-get-unauth GET /api/channel/not-an-id
pair channel-core-delete-unauth DELETE /api/channel/not-an-id
pair channel-core-status-unauth POST /api/channel/not-an-id/status '{}'
pair channel-core-batch-unauth POST /api/channel/batch '{}'
pair channel-core-batch-tag-unauth POST /api/channel/batch/tag '{}'
pair channel-core-copy-unauth POST /api/channel/copy/not-an-id '{}'
pair channel-core-disabled-unauth DELETE /api/channel/disabled
pair channel-core-fix-unauth POST /api/channel/fix '{}'
pair channel-core-models-unauth GET /api/channel/models
pair channel-core-models-enabled-unauth GET /api/channel/models_enabled
pair channel-core-multi-key-unauth POST /api/channel/multi_key/manage '{}'
pair channel-core-ops-unauth GET /api/channel/ops
pair channel-core-search-unauth GET /api/channel/search
pair channel-core-status-batch-unauth POST /api/channel/status/batch '{}'
pair channel-ops-tag-unauth PUT /api/channel/tag '{}'
pair channel-ops-tag-disabled-unauth POST /api/channel/tag/disabled '{}'
pair channel-ops-tag-enabled-unauth POST /api/channel/tag/enabled '{}'
pair channel-ops-tag-models-unauth GET /api/channel/tag/models
# Relay-misc follows the current Go concealment contract, while this broad
# frozen-route runner intentionally boots the older 5418 Go oracle. These
# Rust-only probes prove that the normal listener reaches the current token
# boundary (an unmatched Axum route cannot produce this JSON). Current-Go
# status/body/provider/accounting parity is covered by the dedicated
# relay-misc PostgreSQL/Valkey listener differential.
rust_current_relay_mount relay-misc-alpha-unauth POST /v1/alpha/search '{}'
rust_current_relay_mount relay-misc-embeddings-unauth POST /v1/embeddings '{}'
rust_current_relay_mount relay-misc-rerank-unauth POST /v1/rerank '{}'
rust_current_relay_mount relay-misc-moderations-unauth POST /v1/moderations '{}'
rust_current_relay_mount relay-misc-files-unauth GET /v1/files
rust_current_relay_mount relay-misc-files-post-unauth POST /v1/files '{}'
rust_current_relay_mount relay-misc-file-id-unauth GET /v1/files/not-an-id
rust_current_relay_mount relay-misc-file-id-delete-unauth DELETE /v1/files/not-an-id
rust_current_relay_mount relay-misc-file-content-unauth GET /v1/files/not-an-id/content
rust_current_relay_mount relay-misc-fine-tunes-unauth GET /v1/fine-tunes
rust_current_relay_mount relay-misc-fine-tunes-post-unauth POST /v1/fine-tunes '{}'
rust_current_relay_mount relay-misc-fine-tune-id-unauth GET /v1/fine-tunes/not-an-id
rust_current_relay_mount relay-misc-fine-tune-cancel-unauth POST /v1/fine-tunes/not-an-id/cancel '{}'
rust_current_relay_mount relay-misc-fine-tune-events-unauth GET /v1/fine-tunes/not-an-id/events
rust_current_relay_mount relay-misc-images-variations-unauth POST /v1/images/variations '{}'
pair control-admin-oauth-list-unauth GET /api/custom-oauth-provider/
pair control-admin-oauth-create-unauth POST /api/custom-oauth-provider/ '{}'
pair control-admin-oauth-get-unauth GET /api/custom-oauth-provider/not-an-id
pair control-admin-oauth-update-unauth PUT /api/custom-oauth-provider/not-an-id '{}'
pair control-admin-oauth-delete-unauth DELETE /api/custom-oauth-provider/not-an-id
pair control-admin-oauth-discovery-unauth POST /api/custom-oauth-provider/discovery '{}'
pair control-admin-system-info-unauth GET /api/system-info/instances
pair control-admin-system-info-node-unauth DELETE /api/system-info/instances/not-a-node
pair control-admin-system-info-stale-unauth DELETE /api/system-info/stale-instances
pair control-admin-system-task-unauth GET /api/system-task/not-a-task
pair control-admin-system-task-current-unauth GET /api/system-task/current
pair control-admin-system-task-list-unauth GET /api/system-task/list
pair control-admin-system-task-cleanup-unauth POST /api/system-task/log-cleanup '{}'
pair control-admin-task-unauth GET /api/task/
pair identity-admin-list-unauth GET /api/user/
pair identity-admin-create-unauth POST /api/user/ '{}'
pair identity-admin-update-unauth PUT /api/user/ '{}'
pair identity-admin-get-unauth GET /api/user/not-an-id
pair identity-admin-delete-unauth DELETE /api/user/not-an-id
pair identity-admin-search-unauth GET /api/user/search
pair identity-admin-manage-unauth POST /api/user/manage '{}'
pair identity-catalog-token-unauth GET /api/user/token
pair identity-stripe-amount-unauth POST /api/user/stripe/amount '{}'
pair identity-stripe-amount-auth POST /api/user/stripe/amount '{"amount":1}' "$go_token" "$rust_token"
pair identity-security-authz-unauth GET /api/authz/catalog
pair identity-security-reset-password-unauth GET /api/reset_password
pair identity-security-verification-unauth GET /api/verification
pair identity-security-verify-unauth POST /api/verify '{}'
pair identity-security-login-2fa-unauth POST /api/user/login/2fa '{}'
pair identity-security-binding-unauth DELETE /api/user/1/bindings/email
pair identity-security-reset-passkey-unauth DELETE /api/user/1/reset_passkey
pair identity-security-passkey-status-unauth GET /api/user/passkey
pair identity-security-passkey-delete-unauth DELETE /api/user/passkey
pair identity-security-passkey-login-begin-unauth POST /api/user/passkey/login/begin '{}'
pair identity-security-passkey-login-finish-unauth POST /api/user/passkey/login/finish '{}'
pair identity-security-passkey-register-begin-unauth POST /api/user/passkey/register/begin '{}'
pair identity-security-passkey-register-finish-unauth POST /api/user/passkey/register/finish '{}'
pair identity-security-passkey-verify-begin-unauth POST /api/user/passkey/verify/begin '{}'
pair identity-security-passkey-verify-finish-unauth POST /api/user/passkey/verify/finish '{}'
pair identity-security-reset-unauth POST /api/user/reset '{}'
pair identity-security-sessions-unauth GET /api/user/sessions
pair identity-security-session-delete-unauth DELETE /api/user/sessions/not-a-session
pair identity-security-revoke-others-unauth POST /api/user/sessions/revoke-others '{}'
pair catalog-vendor-list-unauth GET /api/vendors/
pair catalog-vendor-create-unauth POST /api/vendors/ '{}'
pair catalog-vendor-update-unauth PUT /api/vendors/ '{}'
pair catalog-vendor-get-unauth GET /api/vendors/not-an-id
pair catalog-vendor-delete-unauth DELETE /api/vendors/not-an-id
pair catalog-vendor-search-unauth GET /api/vendors/search
# The same API-wide ConsoleAccessGate boundary also fronts the mounted
# provider-backed candidates.  These probes deliberately stop before any
# PostgreSQL/provider work, making the discovery/auth envelope independently
# comparable on the normal listeners.
pair channel-advanced-codex-refresh-unauth POST /api/channel/1/codex/refresh '{}'
pair channel-advanced-codex-usage-unauth GET /api/channel/1/codex/usage
pair channel-advanced-codex-reset-unauth POST /api/channel/1/codex/usage/reset '{}'
pair channel-advanced-codex-reset-credits-unauth GET /api/channel/1/codex/usage/reset-credits
pair channel-advanced-key-unauth POST /api/channel/1/key '{}'
pair channel-advanced-fetch-models-id-unauth GET /api/channel/fetch_models/1
pair channel-advanced-ollama-delete-unauth DELETE /api/channel/ollama/delete
pair channel-advanced-ollama-pull-unauth POST /api/channel/ollama/pull '{}'
pair channel-advanced-ollama-pull-stream-unauth POST /api/channel/ollama/pull/stream '{}'
pair channel-advanced-ollama-version-unauth GET /api/channel/ollama/version/1
pair channel-advanced-update-balance-unauth GET /api/channel/update_balance
pair channel-advanced-update-balance-id-unauth GET /api/channel/update_balance/1
pair channel-advanced-upstream-apply-unauth POST /api/channel/upstream_updates/apply '{}'
pair channel-advanced-upstream-apply-all-unauth POST /api/channel/upstream_updates/apply_all '{}'
pair channel-advanced-upstream-detect-unauth POST /api/channel/upstream_updates/detect '{}'
pair channel-advanced-upstream-detect-all-unauth POST /api/channel/upstream_updates/detect_all '{}'
pair deployment-get-id-unauth GET /api/deployments/1
pair deployment-delete-id-unauth DELETE /api/deployments/1
pair deployment-update-id-unauth PUT /api/deployments/1 '{}'
pair deployment-containers-unauth GET /api/deployments/1/containers
pair deployment-container-details-unauth GET /api/deployments/1/containers/fixture
pair deployment-extend-unauth POST /api/deployments/1/extend '{}'
pair deployment-logs-unauth GET /api/deployments/1/logs
pair deployment-name-unauth PUT /api/deployments/1/name '{}'
pair deployment-available-replicas-unauth GET /api/deployments/available-replicas
pair deployment-check-name-unauth GET /api/deployments/check-name
pair deployment-hardware-types-unauth GET /api/deployments/hardware-types
pair deployment-locations-unauth GET /api/deployments/locations
pair deployment-price-estimation-unauth POST /api/deployments/price-estimation '{}'
pair deployment-search-unauth GET /api/deployments/search
pair deployment-settings-test-connection-unauth POST /api/deployments/settings/test-connection '{}'
pair deployment-test-connection-unauth POST /api/deployments/test-connection '{}'
pair catalog-model-sync-unauth POST /api/models/sync_upstream '{}'
pair catalog-model-sync-preview-unauth GET /api/models/sync_upstream/preview
pair system-option-list-unauth GET /api/option/
pair system-option-affinity-list-unauth GET /api/option/channel_affinity_cache
pair system-option-affinity-clear-unauth DELETE /api/option/channel_affinity_cache
pair system-option-payment-compliance-unauth POST /api/option/payment_compliance '{}'
pair system-option-project-update-unauth GET /api/option/project-update
pair system-option-reset-ratio-unauth POST /api/option/rest_model_ratio '{}'
pair system-option-waffo-catalog-unauth POST /api/option/waffo-pancake/catalog '{}'
pair system-option-waffo-pair-unauth POST /api/option/waffo-pancake/pair '{}'
pair system-option-waffo-save-unauth POST /api/option/waffo-pancake/save '{}'
pair system-option-waffo-subscription-product-unauth POST /api/option/waffo-pancake/subscription-product '{}'
pair system-option-waffo-subscription-options-unauth GET /api/option/waffo-pancake/subscription-product-options
pair ratio-sync-fetch-unauth POST /api/ratio_sync/fetch '{}'
pair subscription-admin-bind-unauth POST /api/subscription/admin/bind '{}'
pair subscription-admin-plans-list-unauth GET /api/subscription/admin/plans
pair subscription-admin-plans-create-unauth POST /api/subscription/admin/plans '{}'
pair subscription-admin-plans-update-unauth PUT /api/subscription/admin/plans/1 '{}'
pair subscription-admin-plans-status-unauth PATCH /api/subscription/admin/plans/1 '{}'
pair subscription-admin-plan-reset-unauth POST /api/subscription/admin/plans/1/subscriptions/reset '{}'
pair subscription-admin-user-reset-unauth POST /api/subscription/admin/users/1/subscriptions/reset '{}'
pair subscription-admin-user-create-unauth POST /api/subscription/admin/users/1/subscriptions '{}'
pair subscription-admin-user-subscription-list-unauth GET /api/subscription/admin/users/1/subscriptions
pair subscription-admin-user-invalidate-unauth POST /api/subscription/admin/user_subscriptions/1/invalidate '{}'
pair subscription-admin-user-delete-unauth DELETE /api/subscription/admin/user_subscriptions/1
pair identity-profile-binding-clear-unauth DELETE /api/user/bindings/email
pair identity-security-register-unauth POST /api/user/register '{}'
pair billing-dashboard-usage-unauth GET /dashboard/billing/usage
pair billing-dashboard-subscription-unauth GET /v1/dashboard/billing/subscription
pair relay-active-alpha-unauth POST /v1/alpha/search '{}'
pair relay-active-moderations-unauth POST /v1/moderations '{}'
pair relay-active-rerank-unauth POST /v1/rerank '{}'
pair relay-model-lookup-unauth GET /v1/models/fixture
if [[ ${LMM_SYSTEM_CONFIG_ROUTE_GROUP:-all} == open-source ]]; then
  # Seed one identical published bounty/challenge in each isolated schema so
  # the positive tip and REST-idempotency cases exercise real quota and ledger
  # side effects rather than only anonymous/error boundaries.
  for engine in go rust; do
    schema_sql "$engine" "INSERT INTO open_source_bounty_projects (id,owner_user_id,repository_url,title,description,rules,reward_quota,net_reward_quota,reward_slots,escrow_quota,platform_fee_rate_bps,platform_fee_quota,status,created_at,updated_at,published_at,closed_at) VALUES (99,1,'https://github.com/example/parity-tip','Parity tip bounty','A synthetic published bounty used for listener parity testing.','The synthetic rules require a matching issue and pull request for completion.',1000,900,1,900,100,100,'published',1700000000,1700000000,1700000000,0);"
    schema_sql "$engine" "INSERT INTO open_source_bounty_challenges (id,project_id,participant_user_id,github_handle,status,issue_url,pull_request_url,submission_note,review_note,reward_quota,tip_quota,owner_rating_score,owner_rating_comment,owner_rated_at,contributor_rating_score,contributor_rating_comment,contributor_rated_at,owner_rating_overturned,accepted_at,submitted_at,reviewed_at,rejected_at,paid_at,created_at,updated_at) VALUES (99,99,2,'bounty-contributor','accepted','','','','',900,0,0,'',0,0,'',0,FALSE,1700000000,0,0,0,0,1700000000,1700000000);"
    schema_sql "$engine" "INSERT INTO open_source_bounty_disputes (id,challenge_id,project_id,opened_by_user_id,against_user_id,case_key,open_key,reason,statement,project_title_snapshot,repository_url_snapshot,project_rules_snapshot,project_escrow_quota_snapshot,challenge_status_snapshot,issue_url_snapshot,pull_request_url_snapshot,submission_note_snapshot,review_note_snapshot,reward_quota_snapshot,tip_quota_snapshot,owner_rating_score_snapshot,owner_rating_comment_snapshot,contributor_rating_score_snapshot,contributor_rating_comment_snapshot,status,resolution,resolved_by_user_id,created_at,updated_at,resolved_at) VALUES (100,99,99,2,1,'challenge:99:user:2','challenge:99','other','A synthetic dispute used for deny-resolution listener parity testing.','Parity tip bounty','https://github.com/example/parity-tip','The synthetic rules require a matching issue and pull request for completion.',900,'accepted','','','', '',900,0,0,'',0,'','open','',0,1700000000,1700000000,0);"
  done
  admin_go_token=$(login_as go bounty-admin)
  admin_rust_token=$(login_as rust bounty-admin)
  pair_open_source_read open-source-list GET /api/open-source-bounties '' "$go_token" "$rust_token"
  pair_open_source_read open-source-detail GET /api/open-source-bounties/projects/99 '' "$go_token" "$rust_token"
  pair_open_source_read open-source-config GET /api/open-source-bounties/config '' "$go_token" "$rust_token"
  pair_open_source_read open-source-mine GET /api/open-source-bounties/mine '' "$go_token" "$rust_token"
  pair_open_source_read open-source-accepted GET /api/open-source-bounties/accepted '' "$go_token" "$rust_token"
  pair_open_source_read open-source-disputes-mine GET /api/open-source-bounties/disputes/mine '' "$go_token" "$rust_token"
  pair_open_source_read open-source-notifications GET /api/open-source-bounties/notifications '' "$go_token" "$rust_token"
  pair_open_source_read open-source-tip-notifications GET /api/open-source-bounties/tips/received '' "$go_token" "$rust_token"
  pair_open_source_read open-source-resolve-deny POST /api/open-source-bounties/disputes/100/resolve '{"action":"deny","resolution":"The synthetic dispute does not establish a payable claim."}' "$admin_go_token" "$admin_rust_token"
  # Cover every current-only route that is not exercised by the positive
  # bounty fixture below. These auth-boundary probes still compare the exact
  # status and envelope on both real listeners and produce route evidence.
  pair open-source-mcp-get-unauth GET /api/open-source-bounties/mcp-token
  pair open-source-mcp-post-unauth POST /api/open-source-bounties/mcp-token
  pair open-source-mcp-delete-unauth DELETE /api/open-source-bounties/mcp-token
  pair open-source-notifications-read-unauth POST /api/open-source-bounties/notifications/read
  pair open-source-tip-notifications-read-unauth POST /api/open-source-bounties/tips/received/read
  pair open-source-tip-thank-unauth POST /api/open-source-bounties/tips/1/thank
  pair open-source-disputes-admin-unauth GET /api/open-source-bounties/disputes/admin
  pair open-source-bounty-create-unauth POST /api/open-source-bounties '{}'
  pair open-source-bounty-update-unauth PUT /api/open-source-bounties/projects/not-an-id '{}'
  pair open-source-bounty-delete-unauth DELETE /api/open-source-bounties/projects/not-an-id
  pair open-source-bounty-pause-unauth POST /api/open-source-bounties/projects/not-an-id/pause
  pair open-source-bounty-resume-unauth POST /api/open-source-bounties/projects/not-an-id/resume
  pair open-source-bounty-create-invalid-auth POST /api/open-source-bounties '{}' "$go_token" "$rust_token"
  pair open-source-bounty-update-invalid-auth PUT /api/open-source-bounties/projects/1 '{}' "$go_token" "$rust_token"
  pair open-source-bounty-delete-missing-auth DELETE /api/open-source-bounties/projects/1 '' "$go_token" "$rust_token"
  pair open-source-bounty-pause-missing-auth POST /api/open-source-bounties/projects/1/pause '' "$go_token" "$rust_token"
  pair open-source-bounty-resume-missing-auth POST /api/open-source-bounties/projects/1/resume '' "$go_token" "$rust_token"
  pair open-source-bounty-publish-unauth POST /api/open-source-bounties/projects/1/publish
  pair open-source-bounty-close-unauth POST /api/open-source-bounties/projects/1/close
  pair open-source-bounty-accept-unauth POST /api/open-source-bounties/projects/1/accept '{}'
  pair open-source-bounty-submit-unauth POST /api/open-source-bounties/projects/1/submit '{}'
  pair open-source-bounty-withdraw-unauth POST /api/open-source-bounties/challenges/1/withdraw
  pair open-source-bounty-cancel-unauth POST /api/open-source-bounties/challenges/1/cancel
  pair open-source-bounty-approve-unauth POST /api/open-source-bounties/challenges/1/approve '{}'
  pair open-source-bounty-reject-unauth POST /api/open-source-bounties/challenges/1/reject '{}'
  pair open-source-bounty-rate-owner-unauth POST /api/open-source-bounties/challenges/1/rate-owner '{}'
  pair open-source-bounty-tip-unauth POST /api/open-source-bounties/challenges/1/tip '{}'
  pair open-source-bounty-dispute-unauth POST /api/open-source-bounties/challenges/1/disputes '{}'
  pair open-source-bounty-resolve-unauth POST /api/open-source-bounties/disputes/1/resolve '{}'
  pair open-source-bounty-publish-invalid-auth POST /api/open-source-bounties/projects/not-an-id/publish '' "$go_token" "$rust_token"
  pair open-source-bounty-close-invalid-auth POST /api/open-source-bounties/projects/not-an-id/close '' "$go_token" "$rust_token"
  pair open-source-bounty-accept-invalid-auth POST /api/open-source-bounties/projects/not-an-id/accept '{}' "$go_token" "$rust_token"
  pair open-source-bounty-submit-invalid-auth POST /api/open-source-bounties/projects/not-an-id/submit '{}' "$go_token" "$rust_token"
  pair open-source-bounty-withdraw-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/withdraw '' "$go_token" "$rust_token"
  pair open-source-bounty-cancel-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/cancel '' "$go_token" "$rust_token"
  pair open-source-bounty-approve-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/approve '{}' "$go_token" "$rust_token"
  pair open-source-bounty-reject-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/reject '{}' "$go_token" "$rust_token"
  pair open-source-bounty-rate-owner-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/rate-owner '{}' "$go_token" "$rust_token"
  pair open-source-bounty-tip-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/tip '{}' "$go_token" "$rust_token"
  pair open-source-bounty-dispute-invalid-auth POST /api/open-source-bounties/challenges/not-an-id/disputes '{}' "$go_token" "$rust_token"
  pair open-source-bounty-resolve-invalid-auth POST /api/open-source-bounties/disputes/not-an-id/resolve '{}' "$go_token" "$rust_token"
  pair open-source-bounty-publish-missing-project POST /api/open-source-bounties/projects/1/publish '' "$go_token" "$rust_token"
  pair open-source-bounty-close-missing-project POST /api/open-source-bounties/projects/1/close '' "$go_token" "$rust_token"
  pair open-source-bounty-accept-missing-project POST /api/open-source-bounties/projects/1/accept '{}' "$go_token" "$rust_token"
  pair open-source-bounty-submit-missing-project POST /api/open-source-bounties/projects/1/submit '{}' "$go_token" "$rust_token"
  pair open-source-bounty-withdraw-missing-challenge POST /api/open-source-bounties/challenges/1/withdraw '' "$go_token" "$rust_token"
  pair open-source-bounty-cancel-missing-challenge POST /api/open-source-bounties/challenges/1/cancel '' "$go_token" "$rust_token"
  pair open-source-bounty-approve-missing-challenge POST /api/open-source-bounties/challenges/1/approve '{}' "$go_token" "$rust_token"
  pair open-source-bounty-reject-missing-challenge POST /api/open-source-bounties/challenges/1/reject '{}' "$go_token" "$rust_token"
  pair open-source-bounty-rate-owner-missing-challenge POST /api/open-source-bounties/challenges/1/rate-owner '{}' "$go_token" "$rust_token"
  pair open-source-bounty-dispute-missing-challenge POST /api/open-source-bounties/challenges/1/disputes '{"reason":"other","statement":"This is a sufficiently long synthetic dispute statement."}' "$go_token" "$rust_token"
  pair open-source-bounty-resolve-missing-dispute POST /api/open-source-bounties/disputes/1/resolve '{"action":"deny","resolution":"A sufficiently long synthetic resolution."}' "$go_token" "$rust_token"
  tip_body='{"quota":250,"note":"Thanks for the focused parity test."}'
  tip_key='tip-parity-20260810-001'
  pair_tip open-source-bounty-tip-success "$tip_body" "$go_token" "$rust_token" "$tip_key"
  pair_tip open-source-bounty-tip-replay "$tip_body" "$go_token" "$rust_token" "$tip_key"
  pair_tip open-source-bounty-tip-idempotency-mismatch '{"quota":251,"note":"Thanks for the focused parity test."}' "$go_token" "$rust_token" "$tip_key"
  for engine in go rust; do
    tip_owner_quota=$(schema_sql "$engine" "SELECT quota FROM users WHERE id=1" | tr -d '[:space:]')
    tip_participant_quota=$(schema_sql "$engine" "SELECT quota FROM users WHERE id=2" | tr -d '[:space:]')
    tip_ledger_count=$(schema_sql "$engine" "SELECT COUNT(*) FROM open_source_bounty_ledgers WHERE project_id=99 AND challenge_id=99 AND kind='tip_transfer'" | tr -d '[:space:]')
    tip_operation_count=$(schema_sql "$engine" "SELECT COUNT(*) FROM open_source_bounty_rest_operations WHERE user_id=1 AND operation='tip'" | tr -d '[:space:]')
    [[ $tip_owner_quota == 99999750 ]] || { echo "$engine tip owner quota mismatch: $tip_owner_quota" >&2; exit 1; }
    [[ $tip_participant_quota == 250 ]] || { echo "$engine tip participant quota mismatch: $tip_participant_quota" >&2; exit 1; }
    [[ $tip_ledger_count == 1 ]] || { echo "$engine tip ledger count mismatch: $tip_ledger_count" >&2; exit 1; }
    [[ $tip_operation_count == 1 ]] || { echo "$engine tip operation count mismatch: $tip_operation_count" >&2; exit 1; }
  done
fi
printf '%s\n' "{\"test\":\"normal-listener-mounted-surfaces-differential\",\"mode\":\"$([[ $rust_test_instance == 1 ]] && echo test-instance || echo normal-listener)\",\"go_oracle_revision\":\"$oracle_revision\",\"cases\":$case_count,\"rust_current_relay_mount_cases\":$rust_mount_count,\"relay_decode_mismatch_cases\":$relay_decode_mismatch_count,\"result\":\"passed\"}"
