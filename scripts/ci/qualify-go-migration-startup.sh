#!/usr/bin/env bash
# A disposable database schema proves migrate -> verify -> serve(verify), without
# letting an apply-mode HTTP startup silently repair missing migration models.
set -euo pipefail
set +x

[[ ${GITHUB_ACTIONS:-} == true ]] || { echo 'This destructive fixture is restricted to Actions.' >&2; exit 2; }
pg_host=${LMM_QUALIFICATION_PG_HOST:-127.0.0.1}
pg_port=${LMM_QUALIFICATION_PG_PORT:-5432}
pg_user=${LMM_QUALIFICATION_PG_USER:-lmm_test_release}
pg_database=${LMM_QUALIFICATION_PG_DATABASE:-lmm_test_release}
[[ $pg_host == 127.0.0.1 && $pg_database == lmm_test_* && $pg_user == lmm_test_* ]] || {
  echo 'Migration startup qualification requires a loopback lmm_test_ database and role.' >&2; exit 2;
}
[[ $pg_port =~ ^[0-9]+$ ]] || exit 2
export PGPASSWORD=${LMM_QUALIFICATION_PG_PASSWORD:-lmm-release-postgres-password}
binary=${LMM_QUALIFICATION_GO_BINARY:?LMM_QUALIFICATION_GO_BINARY is required}
[[ $binary == /* && -x $binary ]] || exit 2
repo_root=$(git rev-parse --show-toplevel)
runtime=$(mktemp -d "${RUNNER_TEMP:-/tmp}/lmm-migration-startup.XXXXXX")
schema="lmm_test_cli_startup_$$"
evidence="$repo_root/qualification-artifacts/go/migration-startup"
mkdir -p "$evidence"
server_pid=
psql_args=(-X -h "$pg_host" -p "$pg_port" -U "$pg_user" -d "$pg_database" -v ON_ERROR_STOP=1)
cleanup() {
  local result=$?
  trap - EXIT
  if [[ -n ${server_pid:-} ]]; then
    kill -TERM "$server_pid" 2>/dev/null || true
    for _ in {1..40}; do kill -0 "$server_pid" 2>/dev/null || break; sleep .1; done
    kill -KILL "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  cp -f "$runtime"/*.log "$evidence/" 2>/dev/null || true
  cp -f "$runtime"/*.json "$evidence/" 2>/dev/null || true
  if ! psql "${psql_args[@]}" -c "DROP SCHEMA IF EXISTS $schema CASCADE" >"$evidence/cleanup.log" 2>&1; then
    result=1
  fi
  rm -rf -- "$runtime"
  exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
psql "${psql_args[@]}" -c "CREATE SCHEMA $schema" >"$runtime/fixture.log" 2>&1
export SQL_DSN="postgres://$pg_user:$PGPASSWORD@$pg_host:$pg_port/$pg_database?sslmode=disable&search_path=$schema"
export SESSION_SECRET=synthetic-migration-startup-session-secret
export CRYPTO_SECRET=synthetic-migration-startup-crypto-secret
export LMM_API_BIND_ADDRESS=127.0.0.1 LMM_API_PORT=33019 PORT=33019
export LMM_LOCAL_ACCEPTANCE=true TRUSTED_PROXIES=none GIN_MODE=release
export REDIS_CONN_STRING=

# A synthetic root record is required by the existing read-only setup checks.
# It is never a production credential or used for any authenticated request.
timeout 90 "$binary" migrate --apply >"$runtime/apply.log" 2>&1
psql "${psql_args[@]}" -c "INSERT INTO $schema.users (username,password,role,status,aff_code) VALUES ('startup-root','unusable-synthetic-test-password',100,1,'startup-fixture')" >>"$runtime/fixture.log" 2>&1
timeout 90 "$binary" migrate --apply >"$runtime/setup-apply.log" 2>&1

verify_and_serve() {
  local label=$1 ready=false
  timeout 60 "$binary" migrate --verify >"$runtime/$label-verify.log" 2>&1
  env LMM_DB_MIGRATION_MODE=verify "$binary" serve >"$runtime/$label-serve.log" 2>&1 &
  server_pid=$!
  for _ in {1..160}; do
    kill -0 "$server_pid" 2>/dev/null || { echo 'verify-mode server exited before readiness' >&2; return 1; }
    if curl --fail --silent --show-error --max-time 2 http://127.0.0.1:33019/api/status >"$runtime/$label-status.json" 2>/dev/null &&
      jq -e '.success == true' "$runtime/$label-status.json" >/dev/null; then
      ready=true; break
    fi
    sleep .25
  done
  [[ $ready == true ]] || { echo 'verify-mode server never became ready' >&2; return 1; }
  kill -TERM "$server_pid"
  for _ in {1..120}; do
    if ! kill -0 "$server_pid" 2>/dev/null; then
      wait "$server_pid"
      server_pid=
      return 0
    fi
    sleep .1
  done
  echo 'verify-mode server failed graceful shutdown' >&2
  return 1
}

verify_and_serve initial
# Standalone verify must also notice damage before registering HTTP routes.
psql "${psql_args[@]}" -c "DROP TABLE $schema.red_packet_claims" >>"$runtime/fixture.log" 2>&1
if timeout 60 "$binary" migrate --verify >"$runtime/missing-table-verify.log" 2>&1; then
  echo 'migrate --verify incorrectly accepted a missing red packet table' >&2; exit 1
fi
grep -F 'red_packet_claims' "$runtime/missing-table-verify.log" >/dev/null
timeout 90 "$binary" migrate --apply >"$runtime/repair-apply.log" 2>&1
verify_and_serve repaired
printf '{"migration_apply":true,"migration_verify":true,"verify_mode_http_startup":true,"missing_table_blocks_cli_verify":true,"repaired_verify_mode_restart":true,"scope":"disposable loopback CI schema"}\n' >"$runtime/result.json"
