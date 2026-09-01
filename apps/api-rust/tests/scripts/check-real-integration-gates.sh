#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
tests_dir="$repo_root/apps/api-rust/tests"
runner="$repo_root/apps/api-rust/tests/scripts/run-real-integration-gates.sh"
isolated_runner="$repo_root/apps/api-rust/tests/scripts/run-isolated-real-integration-gates.sh"

[[ -x $runner || -f $runner ]] || { echo "missing real-integration runner: $runner" >&2; exit 1; }
[[ -x $isolated_runner || -f $isolated_runner ]] || { echo "missing isolated real-integration runner: $isolated_runner" >&2; exit 1; }

declare -A requirements=(
  [auth_pg_valkey.rs]='auth_routes_preserve_postgres_and_valkey_control_plane,dashboard_resolver_preserves_session_pat_and_userauth_boundaries,assistant_l1_confirmation_should_be_session_bound_and_single_use|LMM_AUTH_TEST_DATABASE_URL|LMM_AUTH_TEST_VALKEY_URL'
  [models_pg_valkey.rs]='models_route_uses_authoritative_postgres_and_tolerates_valkey_failure|LMM_MODELS_TEST_DATABASE_URL|LMM_MODELS_TEST_VALKEY_URL'
  [api_token.rs]='mixed_case_deleted_at_create_update_preserves_active_rows_and_rejects_invalid_inputs,create_missing_and_explicit_zero_fields_use_go_model_defaults,create_token_activation_is_one_time_and_transactional,api_token_mutations_invalidate_cached_credentials_and_keep_listings_masked,api_token_status_only_writes_the_legacy_update_column_set,api_token_delete_is_idempotent_under_replay_and_competing_requests,api_token_batch_delete_is_owner_scoped_and_preserves_foreign_cache_on_replay,api_token_token_limit_and_owner_scope_use_postgres_authority,concurrent_create_keeps_the_legacy_count_then_insert_race_contract,api_token_options_refresh_is_best_effort_and_retains_last_good_snapshot,api_token_update_returns_loaded_mutation_without_a_post_write_select,api_token_row_decode_faults_keep_raw_generic_detail_but_search_maps_the_error,api_token_listener_preserves_field_specific_query_overflows_and_repeated_keys,api_token_batch_key_database_fault_is_not_silently_downgraded_to_an_empty_map|LMM_API_TOKEN_TEST_DATABASE_URL|LMM_API_TOKEN_TEST_VALKEY_URL'
)

total_ignored=0
for file in "${!requirements[@]}"; do
  test_file="$tests_dir/$file"
  [[ -f $test_file ]] || { echo "missing integration test: $test_file" >&2; exit 1; }
  IFS='|' read -r test_names database_env valkey_env <<<"${requirements[$file]}"
  IFS=',' read -r -a test_name_list <<<"$test_names"
  mapfile -t actual_ignored_tests < <(awk '
    $0 == "#[tokio::test]" { test_attr=1; ignore_attr=0; next }
    test_attr && $0 ~ /^#\[ignore = / { ignore_attr=1; next }
    test_attr && $0 ~ /^async fn [A-Za-z0-9_]+\(/ {
      if (ignore_attr) { name=$3; sub(/\(.*/, "", name); print name }
      test_attr=0; ignore_attr=0
    }
  ' "$test_file")
  (( ${#actual_ignored_tests[@]} == ${#test_name_list[@]} )) || {
    echo "$file ignored-test set changed: expected ${#test_name_list[@]}, found ${#actual_ignored_tests[@]}" >&2
    exit 1
  }
  total_ignored=$((total_ignored + ${#test_name_list[@]}))
  for test_name in "${test_name_list[@]}"; do
    awk -v name="$test_name" '
      $0 == "#[tokio::test]" { test_attr=1; next }
      test_attr && $0 ~ /^#\[ignore = / { ignore_attr=1; next }
      test_attr && ignore_attr && $0 ~ ("^async fn " name "\\(") { found=1 }
      END { exit found ? 0 : 1 }
    ' "$test_file" || {
      echo "$file must mark $test_name as an explicit ignored real integration test" >&2
      exit 1
    }
  done
  grep -Fq "env::var(\"$database_env\")" "$test_file" || {
    echo "$file does not read required PostgreSQL environment variable $database_env" >&2
    exit 1
  }
  grep -Fq "env::var(\"$valkey_env\")" "$test_file" || {
    echo "$file does not read required Valkey environment variable $valkey_env" >&2
    exit 1
  }
done

for hostile_url in \
  'redis://:secret@10.0.0.1:6379' \
  'redis://:secret@example.com:6379'; do
  if LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1 \
    LMM_AUTH_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/lmm_auth' \
    LMM_AUTH_TEST_VALKEY_URL="$hostile_url" \
    bash "$runner" auth >/dev/null 2>&1; then
    echo "real-integration runner unexpectedly accepted non-loopback Valkey URL" >&2
    exit 1
  fi
done

if ! rg -Fq 'redis://:*@127.0.0.1:*' "$runner"; then
  echo "real-integration runner must accept password-authenticated loopback Valkey URLs" >&2
  exit 1
fi

if rg -U -n 'else\s*\{\s*return;\s*\}' \
  "$tests_dir/auth_pg_valkey.rs" "$tests_dir/models_pg_valkey.rs" "$tests_dir/api_token.rs"; then
  echo "real integration tests must not silently return when environment is missing" >&2
  exit 1
fi

for suite in auth models api-token; do
  if env -u LMM_AUTH_TEST_ALLOW_SCHEMA_RESET -u LMM_AUTH_TEST_DATABASE_URL -u LMM_AUTH_TEST_VALKEY_URL \
    -u LMM_MODELS_TEST_DATABASE_URL -u LMM_MODELS_TEST_VALKEY_URL \
    -u LMM_API_TOKEN_TEST_DATABASE_URL -u LMM_API_TOKEN_TEST_VALKEY_URL \
    bash "$runner" "$suite" >/dev/null 2>&1; then
    echo "$suite real-integration runner unexpectedly accepted missing environment" >&2
    exit 1
  fi
done

echo "real integration gates valid: $total_ignored ignored tests across ${#requirements[@]} modules; missing environment hard-fails"
