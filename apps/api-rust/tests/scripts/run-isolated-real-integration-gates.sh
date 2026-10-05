#!/usr/bin/env bash
# Strict local PostgreSQL 18 + Valkey N/N-1 compatibility harness.
#
# The harness never discovers a production endpoint and never creates scratch
# state below /tmp.  The caller supplies an existing, marker-owned workspace,
# two immutable executable artifacts, and one verifier executable.  The
# verifier owns route semantics; this script owns process identity, service
# isolation, cross-version sequencing, and redacted evidence binding.
set -Eeuo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
# shellcheck source=n-minus-one-harness-lib.sh
# shellcheck disable=SC1091 # Repository root is resolved at runtime.
source "$script_dir/n-minus-one-harness-lib.sh"

usage() {
  cat >&2 <<'EOF'
usage: run-isolated-real-integration-gates.sh \
  --workspace ABS --workspace-marker ABS \
  --n-binary ABS --n-revision HEX --n-artifact-sha256 SHA256 --n-kind rust|go \
  --n-minus-one-binary ABS --n-minus-one-revision HEX \
  --n-minus-one-artifact-sha256 SHA256 --n-minus-one-kind rust|go \
  [--valkey-port PORT] \
  --verifier ABS --output ABS [--duration-seconds 600] [--keep-evidence]

The verifier receives fixed LMM_N1_* environment variables and writes one
bounded JSON result to LMM_N1_RESULT_PATH per operation.  Its exact result
contract is checked by n-minus-one-harness-lib.sh.  Real runs require
prebuilt artifacts; this command never invokes a compiler or package manager.
EOF
  return 2
}

die() { printf 'run-isolated-real-integration-gates: %s\n' "$*" >&2; exit 1; }
require_command() { command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"; }
require_absolute_file() {
  local name=$1 file=$2 executable=$3
  [[ $file == /* && -f $file && ! -L $file ]] || die "$name must be an absolute regular non-symlink file"
  if ((executable)); then [[ -x $file ]] || die "$name must be executable"; fi
}

workspace=''; marker=''; n_binary=''; n_revision=''; n_sha=''; n_kind=''
n1_binary=''; n1_revision=''; n1_sha=''; n1_kind=''; verifier=''; output=''
duration=600
valkey_port=
while (($#)); do
  case $1 in
    --workspace) (($# >= 2)) || usage; workspace=$2; shift 2 ;;
    --workspace-marker|--marker) (($# >= 2)) || usage; marker=$2; shift 2 ;;
    --n-binary) (($# >= 2)) || usage; n_binary=$2; shift 2 ;;
    --n-revision) (($# >= 2)) || usage; n_revision=$2; shift 2 ;;
    --n-artifact-sha256) (($# >= 2)) || usage; n_sha=$2; shift 2 ;;
    --n-kind) (($# >= 2)) || usage; n_kind=$2; shift 2 ;;
    --n-minus-one-binary) (($# >= 2)) || usage; n1_binary=$2; shift 2 ;;
    --n-minus-one-revision) (($# >= 2)) || usage; n1_revision=$2; shift 2 ;;
    --n-minus-one-artifact-sha256) (($# >= 2)) || usage; n1_sha=$2; shift 2 ;;
    --n-minus-one-kind) (($# >= 2)) || usage; n1_kind=$2; shift 2 ;;
    --verifier) (($# >= 2)) || usage; verifier=$2; shift 2 ;;
    --output) (($# >= 2)) || usage; output=$2; shift 2 ;;
    --valkey-port) (($# >= 2)) || usage; valkey_port=$2; shift 2 ;;
    --duration-seconds) (($# >= 2)) || usage; duration=$2; shift 2 ;;
    --keep-evidence) shift ;;
    -h|--help) usage ;;
    *) die "unknown argument: $1" ;;
  esac
done

[[ $workspace == /* && -d $workspace && ! -L $workspace ]] || die 'workspace must be an existing absolute directory'
[[ $marker == /* && -f $marker && ! -L $marker ]] || die 'workspace marker must be an existing absolute regular file'
[[ $(dirname -- "$marker") == "$workspace" ]] || die 'workspace marker must be directly inside workspace'
[[ $(stat -c %u -- "$workspace") == "$EUID" && $(stat -c %u -- "$marker") == "$EUID" ]] || die 'workspace and marker must be owned by the invoking user'
[[ $(stat -c %a -- "$workspace") =~ ^(700|750|770|755)$ ]] || die 'workspace permissions are not an accepted private mode'
grep -Fqx 'lmm-n-minus-one-workspace-v1' "$marker" || die 'workspace marker has an unexpected format'

for value in "$n_revision" "$n1_revision"; do lmm_n1_valid_revision "$value" || die 'revisions must be 7..64 hexadecimal characters'; done
[[ $n_revision != "$n1_revision" ]] || die 'N and N-1 revisions must differ'
for value in "$n_sha" "$n1_sha"; do lmm_n1_valid_sha256 "$value" || die 'artifact SHA-256 must be non-zero lowercase hexadecimal'; done
[[ $n_sha != "$n1_sha" ]] || die 'N and N-1 artifact hashes must differ'
[[ $n_kind == rust || $n_kind == go ]] || die 'N kind must be rust or go'
[[ $n1_kind == rust || $n1_kind == go ]] || die 'N-1 kind must be rust or go'
[[ $duration =~ ^[0-9]+$ && $duration -ge 600 ]] || die 'duration must be an integer of at least 600 seconds'
require_absolute_file n-binary "$n_binary" 1
require_absolute_file n-minus-one-binary "$n1_binary" 1
require_absolute_file verifier "$verifier" 1
[[ $output == /* ]] || die 'output must be an absolute path'
require_command initdb; require_command postgres; require_command psql
require_command valkey-server; require_command valkey-cli; require_command curl
require_command jq; require_command sha256sum; require_command ss; require_command od
[[ $(postgres --version) =~ PostgreSQL[[:space:]]18([.[:space:]]|$) ]] || die 'PostgreSQL 18 is required'
[[ $(initdb --version) =~ PostgreSQL[[:space:]]18([.[:space:]]|$) ]] || die 'PostgreSQL 18 initdb is required'

[[ $(sha256sum -- "$n_binary" | awk '{print $1}') == "$n_sha" ]] || die 'N artifact checksum mismatch'
[[ $(sha256sum -- "$n1_binary" | awk '{print $1}') == "$n1_sha" ]] || die 'N-1 artifact checksum mismatch'
mkdir -p -- "$(dirname -- "$output")"
[[ ! -e $output || -f $output ]] || die 'output exists but is not a regular file'

umask 077
nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d '[:space:]')
runtime="$workspace/n-minus-one.$nonce"
mkdir -- "$runtime" || die 'could not create unique evidence directory'
chmod 0700 -- "$runtime"
evidence="$runtime/evidence"
mkdir -- "$evidence"
printf 'workspace=%s\nrun=%s\n' "$workspace" "$runtime" >"$runtime/run.marker"
export LMM_N1_LAST_MONOTONIC_FILE="$runtime/monotonic.last"

pg_pid=''; valkey_pid=''; n_pid=''; n1_pid=''; pg_port=''; api_n_port=''; api_n1_port=''
pg_user=''; pg_database=''; pg_schema=''; pg_password=''; valkey_password=''
pg_url=''; valkey_url=''; fixture_file=''; pg_app=''; pg_app_password=''; LAUNCHED_PID=''

port_is_unused() {
  local port=$1
  ! ss -H -ltn "sport = :$port" | grep -q . && ! ss -H -lun "sport = :$port" | grep -q .
}
random_port() {
  local candidate attempts=0
  while ((attempts < 128)); do
    candidate=$((20000 + (16#$(od -An -N2 -tx2 /dev/urandom | tr -d ' ') % 30000)))
    if port_is_unused "$candidate"; then printf '%s\n' "$candidate"; return; fi
    ((attempts += 1))
  done
  return 1
}
stop_pid() {
  local pid=$1
  [[ $pid =~ ^[0-9]+$ ]] || return 0
  kill -0 "$pid" 2>/dev/null || return 0
  kill -TERM "$pid" 2>/dev/null || return 0
  for _ in {1..100}; do kill -0 "$pid" 2>/dev/null || { wait "$pid" 2>/dev/null || true; return; }; sleep 0.05; done
  kill -KILL "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true
}
listener_belongs_to_pid() {
  local port=$1 pid=$2
  ss -H -ltnp "sport = :$port" 2>/dev/null | grep -Eq "pid=$pid([,)]|$)"
}
cleanup() {
  local status=$?
  stop_pid "$n_pid"; stop_pid "$n1_pid"; stop_pid "$valkey_pid"; stop_pid "$pg_pid"
  # Evidence is deliberately retained in the marker-owned workspace.  The
  # only removed state is process state, and each PID was launched by us.
  if ((status == 0)); then printf 'completed=1\n' >>"$runtime/run.marker"; else printf 'completed=0\n' >>"$runtime/run.marker"; fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT; trap 'exit 143' TERM

pg_port=$(random_port) || die 'could not reserve PostgreSQL port'
api_n_port=$(random_port) || die 'could not reserve N listener port'
api_n1_port=$(random_port) || die 'could not reserve N-1 listener port'
while [[ $api_n_port == "$pg_port" || $api_n1_port == "$pg_port" || $api_n_port == "$api_n1_port" ]]; do api_n1_port=$(random_port); done
if [[ -z $valkey_port ]]; then
  while :; do
    valkey_port=$(random_port) || die 'could not reserve Valkey port'
    [[ $valkey_port != "$pg_port" && $valkey_port != "$api_n_port" && $valkey_port != "$api_n1_port" ]] && break
  done
else
  [[ $valkey_port =~ ^[0-9]+$ && $valkey_port -ge 2000 && $valkey_port -le 65535 ]] || die 'valkey port must be an integer between 2000 and 65535'
fi
if ! port_is_unused "$valkey_port"; then
  die "Valkey $valkey_port is occupied; isolated harness requires a dedicated free Valkey port"
fi

pg_user="lmm_test_${nonce:0:16}"
pg_database="lmm_test_${nonce:0:16}"
pg_schema="lmm_test_${nonce:0:16}_v1"
pg_password=$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')
valkey_password=$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')
pgdata="$runtime/postgres"
pg_pwfile="$runtime/postgres-password"
printf '%s\n' "$pg_password" >"$pg_pwfile"; chmod 0600 "$pg_pwfile"
initdb --no-locale --encoding=UTF8 --auth-local=scram-sha-256 --auth-host=scram-sha-256 \
  --username="$pg_user" --pwfile="$pg_pwfile" --pgdata="$pgdata" >"$runtime/initdb.log" 2>&1
if rg -n '(^|[[:space:]])trust([[:space:]]|$)' "$pgdata/pg_hba.conf" >/dev/null; then die 'PostgreSQL authentication unexpectedly contains trust'; fi
postgres -D "$pgdata" -h 127.0.0.1 -p "$pg_port" -k "$runtime" >"$runtime/postgres.log" 2>&1 & pg_pid=$!
for _ in {1..200}; do
  PGPASSWORD="$pg_password" psql "postgresql://$pg_user:$pg_password@127.0.0.1:$pg_port/postgres" -Atqc 'SELECT 1' >/dev/null 2>&1 && break
  kill -0 "$pg_pid" 2>/dev/null || die 'PostgreSQL exited during startup'; sleep 0.05
done
if PGPASSWORD=incorrect PGCONNECT_TIMEOUT=2 psql "postgresql://$pg_user@127.0.0.1:$pg_port/postgres" -Atqc 'SELECT 1' >/dev/null 2>&1; then
  die 'PostgreSQL host authentication accepted an incorrect password'
fi
# initdb's bootstrap role is the database owner; create the application role
# and database separately using an escaped, deterministic SQL file.
pg_app="${pg_user}_app"
pg_app_password=$(od -An -N24 -tx1 /dev/urandom | tr -d '[:space:]')
PGPASSWORD="$pg_password" psql "postgresql://$pg_user:$pg_password@127.0.0.1:$pg_port/postgres" -v ON_ERROR_STOP=1 \
  -v app_role="$pg_app" -v app_password="$pg_app_password" -v app_db="$pg_database" -v schema_name="$pg_schema" \
  -f <(printf 'CREATE ROLE :"app_role" LOGIN PASSWORD '\''%s'\''; CREATE DATABASE :"app_db" OWNER :"app_role";\n' "$pg_app_password") >/dev/null
pg_url="postgresql://$pg_app:$pg_app_password@127.0.0.1:$pg_port/$pg_database?options=-csearch_path%3D$pg_schema"
PGPASSWORD="$pg_app_password" psql "postgresql://$pg_app:$pg_app_password@127.0.0.1:$pg_port/$pg_database" \
  -v ON_ERROR_STOP=1 -v schema_name="$pg_schema" -c 'CREATE SCHEMA :"schema_name" AUTHORIZATION CURRENT_USER' >/dev/null
PGPASSWORD="$pg_app_password" psql "$pg_url" -v ON_ERROR_STOP=1 -f <(sed "s/public\./\"$pg_schema\"./g" "$script_dir/../../crates/lmm-db-migrate/schema/postgresql-baseline.sql") >/dev/null
# Keep the frozen contract-1 rehearsal while supplying the nullable snapshots
# that current listener readiness checks.
PGPASSWORD="$pg_app_password" psql "$pg_url" -v ON_ERROR_STOP=1 -f <(sed "s/__LMM_APP_SCHEMA__/\"$pg_schema\"/g" "$script_dir/../../migrations/0018_subscription_amount_snapshots.sql") >/dev/null
PGPASSWORD="$pg_app_password" psql "$pg_url" -v ON_ERROR_STOP=1 -f <(sed "s/__LMM_APP_SCHEMA__/\"$pg_schema\"/g" "$script_dir/../../migrations/0001_schema_contract.sql") >/dev/null
PGPASSWORD="$pg_app_password" psql "$pg_url" -v ON_ERROR_STOP=1 -c \
  "INSERT INTO \"$pg_schema\".options(key,value) VALUES ('SystemName','N/N-1 compatibility fixture'),('RegisterEnabled','false'),('PasswordLoginEnabled','true') ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value;" >/dev/null
identity=$(PGPASSWORD="$pg_app_password" psql "$pg_url" -Atqc \
  "SELECT current_database() || '|' || current_user || '|' || current_schema() || '|' || COALESCE(inet_server_addr()::text,'') || '|' || (SELECT rolsuper::text FROM pg_roles WHERE rolname=current_user)")
IFS='|' read -r db role schema host superuser <<<"$identity"
[[ $db == "$pg_database" && $role == "$pg_app" && $schema == "$pg_schema" && $host == 127.0.0.1 && $superuser == f ]] || die 'PostgreSQL identity check failed'

valkey_config="$runtime/valkey.conf"
printf 'bind 127.0.0.1\nport %s\nprotected-mode yes\nsave ""\nappendonly no\nrequirepass %s\n' "$valkey_port" "$valkey_password" >"$valkey_config"
chmod 0600 "$valkey_config"
valkey-server "$valkey_config" >"$runtime/valkey.log" 2>&1 & valkey_pid=$!
for _ in {1..200}; do VALKEYCLI_AUTH="$valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$valkey_port" ping 2>/dev/null | grep -qx PONG && break; kill -0 "$valkey_pid" 2>/dev/null || die 'Valkey exited during startup'; sleep 0.05; done
if valkey-cli --no-auth-warning -h 127.0.0.1 -p "$valkey_port" ping >/dev/null 2>&1; then die 'Valkey accepted an unauthenticated request'; fi
VALKEYCLI_AUTH="$valkey_password" valkey-cli --no-auth-warning -h 127.0.0.1 -p "$valkey_port" SET lmm:n1:identity "$nonce" EX 600 >/dev/null
valkey_url="redis://:$valkey_password@127.0.0.1:$valkey_port/0"

fixture_file="$evidence/fixture.json"
jq -cn --arg id "$nonce" --arg db "$pg_database" --arg schema "$pg_schema" \
  '{schema_version:1,fixture_id:$id,database:$db,schema:$schema,user_id:71001,token_id:71002,model_id:71003,channel_id:71004,provider_id:71005}' >"$fixture_file"

launch() {
  local kind=$1 binary=$2 revision=$3 port=$4 label=$5
  local log="$runtime/$label.log"
  if [[ $kind == rust ]]; then
    # Omitting LMM_RS_TEST_INSTANCE keeps the normal listener on its explicit
    # CurrentTrustPolicy mode; frozen 5418ce6 is reserved for oracle fixtures.
    env LMM_RS_LISTEN_ADDR="127.0.0.1:$port" LMM_RS_SLOT=blue \
      DATABASE_URL="$pg_url" VALKEY_URL="$valkey_url" LMM_SCHEMA_CONTRACT=1 \
      SESSION_SECRET="n1-session-$nonce" CRYPTO_SECRET="n1-crypto-$nonce" \
      PASSWORD_LOGIN_ENABLED=1 LMM_LOCAL_ACCEPTANCE=true VERSION="$revision" \
      "$binary" >"$log" 2>&1 &
  else
    env LMM_API_BIND_ADDRESS=127.0.0.1 PORT="$port" SQL_DSN="$pg_url" REDIS_CONN_STRING="$valkey_url" \
      LMM_DB_MIGRATION_MODE=verify LMM_LOCAL_ACCEPTANCE=true VERSION="$revision" \
      "$binary" >"$log" 2>&1 &
  fi
  LAUNCHED_PID=$!
}
launch "$n_kind" "$n_binary" "$n_revision" "$api_n_port" n; n_pid=$LAUNCHED_PID
launch "$n1_kind" "$n1_binary" "$n1_revision" "$api_n1_port" n-minus-one; n1_pid=$LAUNCHED_PID
for spec in "$api_n_port:$n_pid" "$api_n1_port:$n1_pid"; do
  IFS=: read -r port pid <<<"$spec"
  ready=0
  for _ in {1..200}; do curl --silent --fail --max-time 1 "http://127.0.0.1:$port/readyz" >/dev/null 2>&1 && { ready=1; break; }; kill -0 "$pid" 2>/dev/null || die 'API binary exited before readiness'; sleep 0.05; done
  ((ready == 1)) || die 'API binary did not become ready'
  listener_belongs_to_pid "$port" "$pid" || die 'API listener ownership check failed'
done

run_verifier() {
  local label=$1 revision=$2 port=$3 operation=$4 iteration=$5
  local result="$evidence/$iteration-$label-$operation.json"
  rm -f -- "$result"
  env LMM_N1_PHASE="$label" LMM_N1_REVISION="$revision" LMM_N1_OPERATION="$operation" \
    LMM_N1_ITERATION="$iteration" LMM_N1_BASE_URL="http://127.0.0.1:$port" \
    LMM_N1_DATABASE_URL="$pg_url" LMM_N1_VALKEY_URL="$valkey_url" \
    LMM_N1_FIXTURE_PATH="$fixture_file" LMM_N1_RESULT_PATH="$result" \
    "$verifier" >/dev/null
  lmm_n1_validate_result "$result" "$revision" "$operation" "$iteration"
}

start_epoch=$(lmm_n1_monotonic_seconds)
iteration=0
while :; do
  now=$(lmm_n1_monotonic_seconds)
  elapsed=$((now - start_epoch))
  ((elapsed >= duration)) && break
  ((iteration += 1))
  run_verifier n "$n_revision" "$api_n_port" write "$iteration"
  run_verifier n-minus-one "$n1_revision" "$api_n1_port" read "$iteration"
  run_verifier n-minus-one "$n1_revision" "$api_n1_port" write "$iteration"
  run_verifier n "$n_revision" "$api_n_port" read "$iteration"
  if [[ -n ${LMM_N1_TEST_MONOTONIC_FILE:-} ]]; then :; else sleep 1; fi
done

stop_pid "$n_pid"; n_pid=
post_iteration=$((iteration + 1))
run_verifier n-minus-one "$n1_revision" "$api_n1_port" read "$post_iteration"
run_verifier n-minus-one "$n1_revision" "$api_n1_port" write "$post_iteration"

n1_duration=$((elapsed > duration ? elapsed : duration))
schema_sha=$(sha256sum -- "$script_dir/../../migrations/0001_schema_contract.sql" | awk '{print $1}')
jq -cn --arg revision "$n_revision" --arg n1_revision "$n1_revision" --arg nsha "$n_sha" --arg n1sha "$n1_sha" \
  --arg executor "lmm-n-minus-one-harness" --argjson duration "$n1_duration" \
  '{schema_version:1,kind:"postgres-n-minus-one",release_revision:$revision,status:"passed",passed:true,duration_seconds:$duration,database_restored:false,executor_identity:$executor,n_revision:$revision,n_minus_one_revision:$n1_revision,n_artifact_sha256:$nsha,n_minus_one_artifact_sha256:$n1sha}' >"$output"
jq -cn --arg revision "$n_revision" --arg n1_revision "$n1_revision" --arg nsha "$n_sha" --arg n1sha "$n1_sha" \
  --arg schema_sha "$schema_sha" --arg evidence_dir "$evidence" --argjson duration "$n1_duration" \
  --argjson iterations "$iteration" --argjson post_iteration "$post_iteration" \
  '{schema_version:1,kind:"postgres-n-minus-one-verifier",status:"passed",release_revision:$revision,n_minus_one_revision:$n1_revision,n_artifact_sha256:$nsha,n_minus_one_artifact_sha256:$n1sha,duration_seconds:$duration,iterations:$iterations,post_stop_iteration:$post_iteration,schema_contract_sha256:$schema_sha,evidence_dir:$evidence_dir}' >"$output.verifier.json"
chmod 0600 -- "$output" "$output.verifier.json"
printf '%s\n' "N/N-1 compatibility passed: duration=${n1_duration}s iterations=${iteration} evidence=$evidence"
