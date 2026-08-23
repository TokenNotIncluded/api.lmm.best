#!/usr/bin/env bash
set -Eeuo pipefail
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
printf 'A=one\nREDIS_CONN_STRING=old\nSQL_DSN=old\nB=two\n' >"$TMP/current.env"
printf 'LMM_MIGRATE_DATABASE_URL=postgresql://app:db-secret@127.0.0.1/lmm_api\n' >"$TMP/migration.env" # gitleaks:allow -- synthetic migration credential
printf 'user default off\nuser lmm-api on >valkey-secret-123456789 ~* +@all\n' >"$TMP/valkey.acl"      # gitleaks:allow -- synthetic Valkey credential
output=$(LMM_CUTOVER_TEST_MODE=1 "$HERE/prepare-candidate-env.sh" --schema lmm_prod_test \
  --output "$TMP/candidate.env" --current-env "$TMP/current.env" \
  --migration-env "$TMP/migration.env" --valkey-acl "$TMP/valkey.acl")
[[ $output == 'candidate environment created atomically' ]]
[[ $output != *secret* ]]
grep -Fxq 'A=one' "$TMP/candidate.env"
grep -Fxq 'B=two' "$TMP/candidate.env"
grep -Fxq 'SQL_DSN=postgresql://app:db-secret@127.0.0.1/lmm_api?options=-csearch_path%3Dlmm_prod_test' "$TMP/candidate.env" # gitleaks:allow
grep -Fxq 'REDIS_CONN_STRING=redis://lmm-api:valkey-secret-123456789@127.0.0.1:6380/0' "$TMP/candidate.env"                 # gitleaks:allow
[[ $(stat -c %a "$TMP/candidate.env") == 600 ]]
[[ $(grep -c '^SQL_DSN=' "$TMP/candidate.env") == 1 ]]
[[ $(grep -c '^REDIS_CONN_STRING=' "$TMP/candidate.env") == 1 ]]
echo 'candidate environment preparation tests passed'
