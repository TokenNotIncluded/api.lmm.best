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
  [system_config.rs]='option_write_invalidates_valkey_then_recovers_from_authoritative_postgres,pricing_locks_filter_before_commit_and_dry_run_has_no_side_effects|LMM_SYSTEM_CONFIG_TEST_DATABASE_URL|LMM_SYSTEM_CONFIG_TEST_VALKEY_URL'
  [auth_pg_valkey.rs]='auth_routes_preserve_postgres_and_valkey_control_plane,dashboard_resolver_preserves_session_pat_and_userauth_boundaries,assistant_l1_confirmation_should_be_session_bound_and_single_use,weekly_session_age_revokes_access_and_refresh_but_honors_opt_out,session_management_preserves_preferences_inventory_and_shared_revocation_fences|LMM_AUTH_TEST_DATABASE_URL|LMM_AUTH_TEST_VALKEY_URL'
  [models_pg_valkey.rs]='models_route_uses_authoritative_postgres_and_tolerates_valkey_failure|LMM_MODELS_TEST_DATABASE_URL|LMM_MODELS_TEST_VALKEY_URL'
  [api_token.rs]='mixed_case_deleted_at_create_update_preserves_active_rows_and_rejects_invalid_inputs,create_missing_and_explicit_zero_fields_use_go_model_defaults,create_token_activation_is_one_time_and_transactional,api_token_mutations_invalidate_cached_credentials_and_keep_listings_masked,api_token_status_only_writes_the_legacy_update_column_set,api_token_delete_is_idempotent_under_replay_and_competing_requests,api_token_batch_delete_is_owner_scoped_and_preserves_foreign_cache_on_replay,api_token_token_limit_and_owner_scope_use_postgres_authority,concurrent_create_keeps_the_legacy_count_then_insert_race_contract,api_token_options_refresh_is_best_effort_and_retains_last_good_snapshot,api_token_update_returns_loaded_mutation_without_a_post_write_select,api_token_row_decode_faults_keep_raw_generic_detail_but_search_maps_the_error,api_token_listener_preserves_field_specific_query_overflows_and_repeated_keys,api_token_batch_key_database_fault_is_not_silently_downgraded_to_an_empty_map,account_balance_access_is_owner_scoped_and_exposed_in_token_response|LMM_API_TOKEN_TEST_DATABASE_URL|LMM_API_TOKEN_TEST_VALKEY_URL'
  [epay_runtime_postgres.rs]='amount_provider_method_and_missing_snapshots_reject_without_credit,checkout_enforces_duplicate_policies_audience_unlock_and_limits,concurrent_replays_credit_coupon_and_referral_exactly_once,current_go_checkout_notification_and_rejection_fixtures_match_rust,fractional_checkout_snapshots_pricing_and_ignores_stale_builtin_rates,http_checkout_and_signed_callback_use_live_credentials_and_ack_only_committed_credit,logs_and_cache_are_post_commit_effects_and_failure_cannot_unpay_an_order,one_provider_transaction_cannot_pay_two_concurrent_orders,referral_ledger_failure_rolls_back_wallet_order_and_coupon_then_retries,stripe_wallet::stripe_checkout_is_durable_before_provider_and_signed_promoted_payment_credits_once,stripe_wallet::stripe_current_go_checkout_settlement_and_refund_reference_matches,stripe_wallet::stripe_current_go_subscription_reference_matches,stripe_wallet::stripe_expiration_releases_coupon_and_subscription_dispatch_precedes_wallet_validation,stripe_wallet::stripe_invoice_before_checkout_retries_receipt_without_granting_twice,stripe_wallet::stripe_partial_and_full_refunds_replay_safely_and_claw_back_only_the_first_reward,stripe_wallet::stripe_provider_failure_retains_order_and_callback_storage_failure_retries_atomically,stripe_wallet::stripe_refund_insufficient_wallet_and_ledger_failure_are_atomic_retryable_failures,stripe_wallet::stripe_reused_payment_intent_and_wrong_currency_cannot_credit_another_order,stripe_wallet::subscription_pay::stripe_current_go_subscription_checkout_reference_matches,stripe_wallet::subscription_pay::stripe_subscription_checkout_gates_then_completes_persisted_plan_once,stripe_wallet::stripe_subscription_checkout_renewal_replay_and_cancellation_preserve_purchased_snapshot,tokens_group_discount_and_private_coupon_preserve_go_order_of_rounding,two_real_payments_share_one_first_topup_reward_and_ldc_never_grants_it,wallet_ceiling_and_coupon_failure_roll_back_the_entire_payment|LMM_EPAY_TEST_DATABASE_URL|LMM_EPAY_TEST_VALKEY_URL'
  [relay_openai_settlement_pg.rs]='billing_preferences_select_wallet_subscription_and_allowed_fallbacks,cancellation_preserves_measured_partial_usage_and_ignores_zero_placeholders,cancellation_while_waiting_for_upstream_body_refunds_without_waiting_for_timeout,cancelled_stream_keeps_completed_tool_cost_without_inventing_token_usage,changed_overflow_policy_keeps_exact_settlement_intent_for_later_reconciliation,chat_usage_after_finish_reason_is_charged_before_done,committed_reservation_refund_and_recovery_invalidate_go_quota_caches,current_go_free_preconsume_policy_and_empty_failure_refunds_match_over_http,current_go_retry_freezes_model_rates_and_reads_tools_and_units_at_settlement,downstream_drop_before_consumption_refunds_exactly_once,durable_settlement_recovers_after_storage_failure_without_another_provider_call,failed_image_response_discards_completed_item_observations,failed_terminal_without_usage_refunds_without_a_success_log,in_flight_and_completed_request_id_replays_never_reach_provider,json_error_after_http_200_refunds_the_reservation,managed_internal_keys_cannot_authenticate_or_contact_the_provider,nonstream_chat_missing_usage_counts_output_and_returns_go_usage_shape,nonstream_chat_zero_prompt_keeps_provider_output_and_fills_input_usage,nonstream_malformed_json_matches_go_500_and_refunds,old_subscription_refund_after_request_reset_cannot_erase_new_turn_usage,partial_subscription_reserves_shared_wallet_budget_until_actual_settlement,relay_trust_discount_uses_credited_quota_and_excludes_linuxdo_credit,response_tool_only_usage_charges_real_calls_and_records_their_prices,settlement_storage_failure_preserves_provider_result_and_durable_replay_fence,shutdown_tracker_waits_for_real_cancelled_refund_blocked_on_a_user_lock,simultaneous_duplicate_requests_create_only_one_reservation_and_upstream_call,startup_and_periodic_workers_recover_once_and_preserve_unknown_reservations,stopped_recovery_worker_leaves_owned_financial_task_in_shutdown_drain,streaming_usage_settles_after_terminal_with_frozen_price_and_real_counts,strict_subscription_and_subscription_only_never_fall_back_to_wallet,tiny_paid_price_with_empty_wallet_never_reaches_the_provider,unavailable_cache_never_retries_or_rolls_back_committed_funds,unlimited_and_soft_deleted_tokens_keep_subscription_usage_accounting|LMM_TEST_DATABASE_URL|LMM_API_TOKEN_TEST_VALKEY_URL'
  [scripts.rs]='repository_options_commit_refresh_runtime_invalidate_cache_and_redact_audit|LMM_TEST_DATABASE_URL|LMM_AUTH_TEST_VALKEY_URL'
  [ai_directory.rs]='postgres_cache_and_audit_failures_do_not_reverse_committed_wallet_changes,postgres_create_replay_quote_changes_and_concurrency_charge_once,postgres_hide_refunds_once_and_wallet_failure_rolls_back_visibility,postgres_public_private_pagination_expiry_and_http_contract|LMM_TEST_DATABASE_URL|LMM_AUTH_TEST_VALKEY_URL'
  [token_queries.rs]='configured_token_prices_match_current_go_reference_live_maps_and_limits,persisted_usage_is_exact_token_scoped_utc_and_never_changes_credentials,quota_query_auth_checks_exact_key_expiry_owner_oauth_and_ip_without_status_writes,quota_query_limiter_is_shared_per_owner_across_keys_and_instances,token_pricing_checks_permissions_before_query_validation_and_never_mutates_key,token_pricing_uses_shared_credited_trust_facts_and_excludes_internal_credits|LMM_TEST_DATABASE_URL|LMM_AUTH_TEST_VALKEY_URL'
)

total_ignored=0
for file in "${!requirements[@]}"; do
  test_file="$tests_dir/$file"
  [[ -f $test_file ]] || { echo "missing integration test: $test_file" >&2; exit 1; }
  IFS='|' read -r test_names database_env valkey_env <<<"${requirements[$file]}"
  IFS=',' read -r -a test_name_list <<<"$test_names"
  actual_listing=$(python3 "$script_dir/ignored-test-inventory.py" "$test_file")
  mapfile -t actual_ignored_tests <<<"$actual_listing"
  (( ${#actual_ignored_tests[@]} == ${#test_name_list[@]} )) || {
    echo "$file ignored-test set changed: expected ${#test_name_list[@]}, found ${#actual_ignored_tests[@]}" >&2
    exit 1
  }
  total_ignored=$((total_ignored + ${#test_name_list[@]}))
  for test_name in "${test_name_list[@]}"; do
    grep -Fxq "$test_name" <<<"$actual_listing" || {
      echo "$file must mark $test_name as an explicit ignored real integration test" >&2
      exit 1
    }
  done
  source_listing=$(python3 "$script_dir/ignored-test-inventory.py" "$test_file" --files)
  mapfile -t source_files <<<"$source_listing"
  grep -Fq "env::var(\"$database_env\")" "${source_files[@]}" || {
    echo "$file does not read required PostgreSQL environment variable $database_env" >&2
    exit 1
  }
  if [[ -n $valkey_env ]] && ! grep -Fq "env::var(\"$valkey_env\")" "${source_files[@]}"; then
    echo "$file does not read required Valkey environment variable $valkey_env" >&2
    exit 1
  fi

done

# These tests are library tests, so validate the owning file and exact
# fully-qualified CI selector rather than assuming they live in tests/*.rs.
for entry in \
  'auth::postgres::trust_pg_tests::current_go_credit_history_drives_dashboard_trust_access_and_refund_transitions|src/auth/trust_pg_tests.rs|LMM_TEST_DATABASE_URL' \
  'models::cache_pg_tests::current_token_cache_fences_old_readers_and_preserves_reserved_quota|src/models_cache_pg_tests.rs|LMM_AUTH_TEST_VALKEY_URL' \
  'routes::relay_openai::funding::go_oracle_tests::current_go_funding_vectors_match_real_postgres_reserve_settle_refund_and_grow|src/routes/relay_openai/funding/go_oracle_tests.rs|LMM_TEST_DATABASE_URL'; do
  IFS='|' read -r qualified source environment <<<"$entry"
  names=$(python3 "$script_dir/ignored-test-inventory.py" "$repo_root/apps/api-rust/$source")
  grep -Fxq "${qualified##*::}" <<<"$names" || { echo "missing ignored library contract $qualified" >&2; exit 1; }
  grep -Fq "$qualified" "$runner" || { echo "CI does not execute $qualified" >&2; exit 1; }
  grep -Fq "env::var(\"$environment\")" "$repo_root/apps/api-rust/$source" || { echo "missing library dependency $environment" >&2; exit 1; }
done
schema_tests=$(python3 "$script_dir/ignored-test-inventory.py" "$repo_root/apps/api-rust/crates/lmm-db-migrate/tests/current_catalog_schema.rs")
[[ $(wc -l <<<"$schema_tests") == 4 ]] || { echo "current schema contract test inventory must contain four tests" >&2; exit 1; }
while IFS= read -r name; do
  grep -Fq "run_exact_migration_test current_catalog_schema $name" "$runner" || { echo "CI does not execute current schema test $name" >&2; exit 1; }
done <<<"$schema_tests"
token_schema_tests=$(python3 "$script_dir/ignored-test-inventory.py" "$repo_root/apps/api-rust/crates/lmm-db-migrate/tests/token_management_schema.rs")
[[ $token_schema_tests == 'token_management_schema_preserves_rows_and_rejects_weakened_guards' ]] || {
  echo "token management schema contract test inventory changed" >&2; exit 1;
}
grep -Fq "run_exact_migration_test token_management_schema $token_schema_tests" "$runner" || {
  echo "CI does not execute token management schema contract" >&2; exit 1;
}

balance_source="$repo_root/apps/api-rust/src/channel_balance_store.rs"
go_balance_tests="$repo_root/apps/api-go/controller/channel_billing_currency_test.go"
go_refresh_tests="$repo_root/apps/api-go/controller/channel_balance_refresh_test.go"
[[ -f $balance_source ]] || { echo "missing channel balance persistence source: $balance_source" >&2; exit 1; }
[[ -f $go_balance_tests ]] || { echo "missing Go channel balance oracle tests: $go_balance_tests" >&2; exit 1; }
[[ -f $go_refresh_tests ]] || { echo "missing Go channel refresh oracle tests: $go_refresh_tests" >&2; exit 1; }
grep -Fq 'async fn persisted_balance_updates_value_and_timestamp_together()' "$balance_source" || {
  echo "channel balance persistence regression test is missing" >&2
  exit 1
}
grep -Fq 'channel_balance_store::tests::persisted_balance_updates_value_and_timestamp_together' "$runner" || {
  echo "real-integration runner does not execute the channel balance persistence regression" >&2
  exit 1
}
for oracle_test in \
  TestGetDeepSeekBalanceUSD \
  TestRefreshChannelBalancesCapturesAndSanitizesProviderFailure \
  TestRefreshChannelBalancesCapturesAndSanitizesDatabaseFailure \
  TestRefreshChannelBalancesReportsMixedOutcome \
  TestRefreshChannelBalancesReportsAllSuccess \
  TestRefreshChannelBalancesBoundsFailureDetailsWithoutDroppingCounts \
  TestWriteChannelBalanceRefreshResponseUsesCompatiblePartialAndFullFailureEnvelopes; do
  grep -Fq "func $oracle_test" "$go_balance_tests" "$go_refresh_tests" || {
    echo "Go channel balance oracle test is missing: $oracle_test" >&2
    exit 1
  }
  grep -Fq "$oracle_test" "$runner" || {
    echo "real-integration runner does not execute Go oracle test: $oracle_test" >&2
    exit 1
  }
done
grep -Fq -- "--lib 'channel_balance::tests::'" "$runner" || {
  echo "real-integration runner does not execute Rust DeepSeek balance contracts" >&2
  exit 1
}
grep -Fq -- "--lib 'channel_balance_provider::tests::'" "$runner" || {
  echo "real-integration runner does not execute Rust channel balance route contracts" >&2
  exit 1
}

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

for suite in auth models api-token system-config migration announcements epay stripe catalog token-queries shared-trust token-cache relay-settlement scripts channel-balance; do
  if env -u LMM_TEST_DATABASE_URL -u LMM_AUTH_TEST_ALLOW_SCHEMA_RESET -u LMM_AUTH_TEST_DATABASE_URL -u LMM_AUTH_TEST_VALKEY_URL \
    -u LMM_MODELS_TEST_DATABASE_URL -u LMM_MODELS_TEST_VALKEY_URL \
    -u LMM_API_TOKEN_TEST_DATABASE_URL -u LMM_API_TOKEN_TEST_VALKEY_URL \
    -u LMM_SYSTEM_CONFIG_TEST_DATABASE_URL -u LMM_SYSTEM_CONFIG_TEST_VALKEY_URL \
    -u LMM_EPAY_TEST_DATABASE_URL -u LMM_EPAY_TEST_VALKEY_URL \
    bash "$runner" "$suite" >/dev/null 2>&1; then
    echo "$suite real-integration runner unexpectedly accepted missing environment" >&2
    exit 1
  fi
done

# Reproduce libtest's successful zero-match exit without touching dependencies.
# The runner must reject it before treating a migration gate as passed.
empty_test_bin=$(mktemp -d /tmp/lmm-empty-integration-test.XXXXXX)
trap 'rm -rf -- "$empty_test_bin"' EXIT
cat >"$empty_test_bin/cargo" <<'STUB'
#!/usr/bin/env bash
exit 0
STUB
chmod +x "$empty_test_bin/cargo"
if PATH="$empty_test_bin:$PATH" LMM_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/isolated' \
  bash "$runner" migration >"$empty_test_bin/output" 2>&1; then
  echo 'migration gate accepted a successful zero-test selection' >&2
  exit 1
fi
rg -Fq 'required integration test is missing:' "$empty_test_bin/output" || {
  echo 'migration gate did not reject the missing compiled test' >&2
  exit 1
}

if PATH="$empty_test_bin:$PATH" LMM_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/isolated' \
  bash "$runner" announcements >"$empty_test_bin/announcements-output" 2>&1; then
  echo 'announcement gate accepted a successful zero-test selection' >&2
  exit 1
fi
rg -Fq 'required integration test is missing:' "$empty_test_bin/announcements-output" || {
  echo 'announcement gate did not reject the missing compiled test' >&2
  exit 1
}

# These suites must reject an empty COMPILED inventory before running Go,
# opening the database, or treating libtest's zero-test success as a pass.
for suite in epay stripe catalog token-queries relay-settlement scripts; do
  if PATH="$empty_test_bin:$PATH" \
    LMM_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/isolated' \
    LMM_AUTH_TEST_VALKEY_URL='redis://:fixture@127.0.0.1:6379/0' \
    LMM_EPAY_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/isolated' \
    LMM_EPAY_TEST_VALKEY_URL='redis://:fixture@127.0.0.1:6379/0' \
    bash "$runner" "$suite" >"$empty_test_bin/$suite-output" 2>&1; then
    echo "$suite gate accepted a successful zero-test selection" >&2
    exit 1
  fi
  rg -Fq 'required integration test count mismatch:' "$empty_test_bin/$suite-output" || {
    echo "$suite gate did not reject its missing compiled inventory" >&2
    exit 1
  }
done

for suite in shared-trust token-cache; do
  if PATH="$empty_test_bin:$PATH" LMM_TEST_DATABASE_URL='postgresql://127.0.0.1:5432/isolated' \
    LMM_AUTH_TEST_VALKEY_URL='redis://:fixture@127.0.0.1:6379/0' \
    bash "$runner" "$suite" >"$empty_test_bin/$suite-output" 2>&1; then
    echo "$suite accepted an empty compiled library test selection" >&2; exit 1;
  fi
  rg -Fq 'required integration test is missing:' "$empty_test_bin/$suite-output" || {
    echo "$suite did not reject the missing library test" >&2; exit 1;
  }
done
python3 "$script_dir/test-new-integration-suites.py"
python3 "$script_dir/test-ignored-test-inventory.py"

echo "real integration gates valid: $total_ignored ignored tests across ${#requirements[@]} modules plus exact announcement/schema gates and current Go oracles; missing environment and zero-test execution hard-fail"
