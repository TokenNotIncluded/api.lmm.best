#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
manifest="$repo_root/apps/api-rust/Cargo.toml"
[[ -f $manifest && ! -L $manifest ]] || {
  echo "Rust API Cargo manifest is unavailable: $manifest" >&2
  exit 1
}
# Cargo selects the pinned rust-toolchain.toml from its working directory.
cd "$repo_root/apps/api-rust"
suite=${1:-all}

usage() {
  echo "usage: $0 {auth|models|api-token|subscription-reset|migration|announcements|epay|stripe|catalog|token-queries|acquisition|shared-trust|token-cache|relay-settlement|scripts|system-config|relay-timeouts|channel-balance|all}" >&2
  exit 2
}

require_loopback_url() {
  local name=$1 value=${!1:-}
  [[ -n $value ]] || { echo "$name is required for the isolated real-integration harness" >&2; exit 1; }
  case "$value" in
    postgresql://localhost:*/* | postgresql://127.0.0.1:*/* | postgresql://\[::1\]:*/* | \
    postgresql://*:*@localhost:*/* | postgresql://*:*@127.0.0.1:*/* | postgresql://*:*@\[::1\]:*/* | \
    redis://localhost:* | redis://127.0.0.1:* | redis://\[::1\]:* | \
    redis://:*@localhost:* | redis://:*@127.0.0.1:* | redis://:*@\[::1\]:*) ;;
    *) echo "$name must use a loopback-only isolated service" >&2; exit 1 ;;
  esac
}

# libtest returns success when a stale exact filter selects zero tests.
# Require the compiled ignored test to exist before executing the gate.
run_exact_migration_test() {
  local target=$1 test_name=$2 listing
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-db-migrate \
    --test "$target" "$test_name" -- --ignored --exact --list)
  if ! grep -Fxq "$test_name: test" <<<"$listing"; then
    echo "required integration test is missing: $target::$test_name" >&2
    exit 1
  fi
  run_counted_exact cargo test --locked --manifest-path "$manifest" -p lmm-db-migrate \
    --test "$target" "$test_name" -- --ignored --exact --test-threads=1
}

run_exact_api_lib_test() {
  local test_name=$1 listing
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --lib "$test_name" -- --ignored --exact --list)
  if ! grep -Fxq "$test_name: test" <<<"$listing"; then
    echo "required integration test is missing: lmm-api-rs::$test_name" >&2
    exit 1
  fi
  run_counted_exact cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --lib "$test_name" -- --ignored --exact --test-threads=1
}

run_exact_api_integration_test() {
  local target=$1 test_name=$2 listing
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$test_name" -- --ignored --exact --list)
  if ! grep -Fxq "$test_name: test" <<<"$listing"; then
    echo "required integration test is missing: $target::$test_name" >&2
    exit 1
  fi
  run_counted_exact cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$test_name" -- --ignored --exact --test-threads=1
}

require_api_ignored_test_count() {
  local target=$1 expected=$2 filter=${3:-} skip=${4:-} listing actual source_names compiled_names
  local -a selection=()
  [[ -z $skip ]] || selection+=(--skip "$skip")
  [[ $expected =~ ^[1-9][0-9]*$ ]] || { echo "expected integration count must be positive" >&2; exit 1; }
  source_names=$(python3 "$script_dir/ignored-test-inventory.py" "$repo_root/apps/api-rust/tests/$target.rs" --filter "$filter" --skip "$skip")
  actual=$(awk 'NF { count++ } END { print count + 0 }' <<<"$source_names")
  [[ $actual == "$expected" ]] || {
    echo "source integration test count mismatch: $target expected $expected, found $actual" >&2; exit 1;
  }
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$filter" -- --ignored "${selection[@]}" --list)
  compiled_names=$(awk '/^[A-Za-z0-9_:]+: test$/ { sub(/: test$/, ""); print }' <<<"$listing" | LC_ALL=C sort)
  actual=$(awk 'NF { count++ } END { print count + 0 }' <<<"$compiled_names")
  if [[ $actual != "$expected" ]]; then
    echo "required integration test count mismatch: $target expected $expected, found $actual" >&2
    exit 1
  fi
  [[ $compiled_names == "$source_names" ]] || {
    echo "required integration test name mismatch: $target compiled/source selections differ" >&2; exit 1;
  }
}

run_counted_api_integration_tests() (
  target=$1 expected=$2 filter=${3:-} skip=${4:-}
  selection=()
  [[ -z $skip ]] || selection+=(--skip "$skip")
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-counted-integration.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$filter" -- --ignored "${selection[@]}" --test-threads=1 | tee "$runtime/result.log"
  grep -Fq "test result: ok. $expected passed; 0 failed; 0 ignored;" "$runtime/result.log" || {
    echo "integration execution did not run all $expected required tests: $target" >&2
    exit 1
  }
)

run_counted_exact() (
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-exact-integration.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  "$@" | tee "$runtime/result.log"
  grep -Fq 'test result: ok. 1 passed; 0 failed; 0 ignored;' "$runtime/result.log" || {
    echo 'integration execution did not run all 1 required tests: exact selection' >&2; exit 1;
  }
)

run_exact_api_unit_test() {
  local target=$1 test_name=$2 listing
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$test_name" -- --exact --list)
  grep -Fxq "$test_name: test" <<<"$listing" || {
    echo "required integration test is missing: $target::$test_name" >&2; exit 1;
  }
  run_counted_exact cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test "$target" "$test_name" -- --exact --test-threads=1
}

payment_environment() {
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
  export LMM_EPAY_TEST_DATABASE_URL="${LMM_EPAY_TEST_DATABASE_URL:-$LMM_TEST_DATABASE_URL}"
  export LMM_EPAY_TEST_VALKEY_URL="${LMM_EPAY_TEST_VALKEY_URL:-$LMM_AUTH_TEST_VALKEY_URL}"
  for variable in LMM_EPAY_TEST_DATABASE_URL LMM_EPAY_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
}

run_epay() (
  payment_environment
  require_api_ignored_test_count epay_runtime_postgres 12 "" "stripe_wallet::"
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-epay.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  # Always overwrite inherited fixture paths with an export from CURRENT Go.
  # A new temporary directory prevents a stale golden file from making a
  # skipped or zero-match Go invocation appear successful.
  export LMM_EPAY_GO_ORACLE_OUTPUT="$runtime/current-go-epay.json"
  export LMM_EPAY_PARITY_FIXTURES="$repo_root/apps/api-rust/tests/fixtures/epay-current-go-input.json"
  (
    cd "$repo_root/apps/api-go"
    go test ./controller -run '^TestRustEpayCurrentGoOracle$' -count=1
  )
  python3 "$script_dir/verify-current-go-export.py" epay "$LMM_EPAY_GO_ORACLE_OUTPUT" --shared-input "$LMM_EPAY_PARITY_FIXTURES"
  run_counted_api_integration_tests epay_runtime_postgres 12 "" "stripe_wallet::"
)

run_stripe() (
  payment_environment
  require_api_ignored_test_count epay_runtime_postgres 12 "stripe_wallet::"
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-stripe.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  export LMM_STRIPE_GO_ORACLE_OUTPUT="$runtime/wallet.json"
  export LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT="$runtime/subscription.json"
  export LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT="$runtime/subscription-checkout.json"
  export LMM_STRIPE_SUBSCRIPTION_CHECKOUT_FIXTURES="$repo_root/apps/api-rust/tests/fixtures/stripe-subscription-checkout-current-go-input.json"
  (
    cd "$repo_root/apps/api-go"
    go test ./controller -run '^(TestRustStripeCurrentGoOracle|TestRustStripeSubscriptionCurrentGoOracle|TestRustStripeSubscriptionCheckoutCurrentGoOracle)$' -count=1
  )
  python3 "$script_dir/verify-current-go-export.py" stripe "$LMM_STRIPE_GO_ORACLE_OUTPUT"
  python3 "$script_dir/verify-current-go-export.py" stripe-subscription "$LMM_STRIPE_SUBSCRIPTION_GO_ORACLE_OUTPUT"
  python3 "$script_dir/verify-current-go-export.py" stripe-subscription-checkout "$LMM_STRIPE_SUBSCRIPTION_CHECKOUT_GO_ORACLE_OUTPUT" --shared-input "$LMM_STRIPE_SUBSCRIPTION_CHECKOUT_FIXTURES"
  run_counted_api_integration_tests epay_runtime_postgres 12 "stripe_wallet::"
)

run_catalog() (
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
  require_api_ignored_test_count ai_directory 4
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-catalog.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  export LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT="$runtime/catalog.json"
  (cd "$repo_root/apps/api-go"; go test ./model -run '^TestRustAIDirectoryCurrentGoOracle$' -count=1)
  python3 "$script_dir/verify-current-go-export.py" catalog "$LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT"
  run_exact_api_unit_test ai_directory quote_and_url_normalization_match_current_go_oracle
  run_counted_api_integration_tests ai_directory 4
)

run_token_queries() (
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
  require_api_ignored_test_count token_queries 6
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-token-pricing.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  export LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT="$runtime/token-pricing.json"
  (cd "$repo_root/apps/api-go"; go test ./controller -run '^TestRustTokenPricingCurrentGoOracle$' -count=1)
  python3 "$script_dir/verify-current-go-export.py" token-pricing "$LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT"
  run_counted_api_integration_tests token_queries 6
)

run_acquisition() {
  require_loopback_url LMM_TEST_DATABASE_URL
  require_api_ignored_test_count acquisition 7
  run_counted_api_integration_tests acquisition 7
}

run_shared_trust() {
  require_loopback_url LMM_TEST_DATABASE_URL
  run_exact_api_lib_test auth::postgres::trust_pg_tests::current_go_credit_history_drives_dashboard_trust_access_and_refund_transitions
}

run_token_cache() {
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
  run_exact_api_lib_test models::cache_pg_tests::current_token_cache_fences_old_readers_and_preserves_reserved_quota
}

run_relay_settlement() (
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do require_loopback_url "$variable"; done
  export LMM_API_TOKEN_TEST_VALKEY_URL="${LMM_API_TOKEN_TEST_VALKEY_URL:-$LMM_AUTH_TEST_VALKEY_URL}"
  require_loopback_url LMM_API_TOKEN_TEST_VALKEY_URL
  require_api_ignored_test_count relay_openai_settlement_pg 35
  runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-funding.XXXXXX")
  trap 'rm -rf -- "$runtime"' EXIT
  export LMM_RELAY_FUNDING_GO_VECTORS="$runtime/funding.json"
  export LMM_RELAY_PRICE_GO_VECTORS="$runtime/price-lifecycle.json"
  (
    cd "$repo_root/apps/api-go"
    GOMAXPROCS=2 go run -p=2 ../api-rust/tests/behavior-oracle/fixtures/relay_funding.go "$LMM_RELAY_FUNDING_GO_VECTORS"
    GOMAXPROCS=2 go run -p=2 ../api-rust/tests/behavior-oracle/fixtures/relay_price_lifecycle.go "$LMM_RELAY_PRICE_GO_VECTORS"
  )
  python3 "$script_dir/verify-current-go-export.py" relay-funding "$LMM_RELAY_FUNDING_GO_VECTORS"
  python3 "$script_dir/verify-current-go-export.py" relay-price "$LMM_RELAY_PRICE_GO_VECTORS"
  run_counted_api_integration_tests relay_openai_settlement_pg 35
  run_exact_api_lib_test routes::relay_openai::funding::go_oracle_tests::current_go_funding_vectors_match_real_postgres_reserve_settle_refund_and_grow
)

run_scripts() {
  for variable in LMM_TEST_DATABASE_URL LMM_AUTH_TEST_VALKEY_URL; do
    require_loopback_url "$variable"
  done
  require_api_ignored_test_count scripts 1
  run_counted_api_integration_tests scripts 1
}

run_auth() {
  [[ ${LMM_AUTH_TEST_ALLOW_SCHEMA_RESET:-} == 1 ]] || {
    echo "LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1 is required for the isolated auth schema reset" >&2
    exit 1
  }
  require_loopback_url LMM_AUTH_TEST_DATABASE_URL
  require_loopback_url LMM_AUTH_TEST_VALKEY_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test auth_pg_valkey -- --ignored --test-threads=1
}

run_models() {
  require_loopback_url LMM_MODELS_TEST_DATABASE_URL
  require_loopback_url LMM_MODELS_TEST_VALKEY_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test models_pg_valkey -- --ignored --test-threads=1
}

run_api_token() {
  require_loopback_url LMM_API_TOKEN_TEST_DATABASE_URL
  require_loopback_url LMM_API_TOKEN_TEST_VALKEY_URL
  (
    cd "$repo_root/apps/api-go"
    go test ./controller -run '^TestAccountBalance' -count=1
  )
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test api_token -- --ignored --test-threads=1
  local test_name=test_instance::account_balance_pg_tests::durable_balance_defaults_revocation_and_read_only_contract listing
  listing=$(cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --bin lmm-api-rs "$test_name" -- --ignored --exact --list)
  grep -Fxq "$test_name: test" <<<"$listing" || {
    echo "required durable account balance test is missing" >&2; exit 1;
  }
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --bin lmm-api-rs "$test_name" -- --ignored --exact --test-threads=1
}

run_subscription_reset() {
  require_loopback_url LMM_BILLING_SUBSCRIPTIONS_TEST_DATABASE_URL
  require_loopback_url LMM_BILLING_SUBSCRIPTIONS_TEST_VALKEY_URL
  require_loopback_url LMM_TEST_DATABASE_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test billing_subscriptions -- --ignored --test-threads=1
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test billing_subscription_reset_postgres -- --ignored --test-threads=1
  run_exact_migration_test schema_contract contract_six_verifier_rejects_wrong_default_and_index_columns
}

run_migration() {
  require_loopback_url LMM_TEST_DATABASE_URL
  run_exact_migration_test account_balance_access_schema account_balance_access_migration_is_additive_idempotent_and_default_denied
  run_exact_migration_test full_copy full_copy_should_verify_all_tables_and_rollback_both_fault_phases
  run_exact_migration_test waffo_subscription_schema contract_eight_preserves_pending_evidence_and_rejects_broken_replay_guards
  run_exact_migration_test current_parity_schema announcement_schema_preserves_history_and_rejects_weakened_unique_keys
  run_exact_migration_test current_parity_schema payment_runtime_schema_preserves_orders_and_rejects_weakened_replay_guards
  run_exact_migration_test current_catalog_schema current_catalog_schema_preserves_ads_and_rejects_weak_replay_or_money_columns
  run_exact_migration_test current_catalog_schema payment_extensions_schema_keeps_nullable_history_and_rejects_weak_idempotency
  run_exact_migration_test current_catalog_schema payment_extensions_preserve_legacy_subscription_currency_width_and_optional_periods
  run_exact_migration_test current_catalog_schema relay_settlement_schema_pins_phase_money_json_and_replay_constraints
  run_exact_migration_test token_management_schema token_management_schema_preserves_rows_and_rejects_weakened_guards
}

run_announcements() {
  require_loopback_url LMM_TEST_DATABASE_URL
  LMM_MANDATORY_ANNOUNCEMENTS_TEST_DATABASE_URL="$LMM_TEST_DATABASE_URL" \
    run_exact_api_integration_test mandatory_announcements postgres_orders_replays_and_revisions_are_account_scoped_and_atomic
}

run_system_config() {
  require_loopback_url LMM_SYSTEM_CONFIG_TEST_DATABASE_URL
  require_loopback_url LMM_SYSTEM_CONFIG_TEST_VALKEY_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test system_config -- --ignored --test-threads=1
}

run_relay_timeouts() {
  require_loopback_url LMM_TEST_DATABASE_URL
  # Both implementations consume the same native xAI SSE fixture. Retain the
  # Go provider and retry oracle beside the real PostgreSQL Rust boundary test.
  (
    cd "$repo_root/apps/api-go"
    go test ./relay/channel/xai -run '^TestClaudeMessages' -count=1
    go test ./controller -run '^TestGetChannelRetrySkipsUnsupportedEndpointCandidates$' -count=1
  )
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test relay_anthropic_gemini_postgres -- --ignored --test-threads=1
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test relay_openai_specific_channel_pg -- --ignored --test-threads=1
  [[ ${LMM_AUTH_TEST_ALLOW_SCHEMA_RESET:-} == 1 ]] || {
    echo "LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1 is required for the isolated relay-misc schema reset" >&2
    exit 1
  }
  LMM_RELAY_MISC_TEST_DATABASE_URL="$LMM_TEST_DATABASE_URL" \
    LMM_RELAY_MISC_TEST_ALLOW_SCHEMA_RESET=1 \
    cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test relay_misc_pg -- --ignored --test-threads=1
}

run_channel_balance_go_oracle() {
  local go_root="$repo_root/apps/api-go"
  [[ -f $go_root/go.mod && ! -L $go_root/go.mod ]] || {
    echo "Go production oracle module is unavailable: $go_root" >&2
    exit 1
  }
  (
    cd "$go_root"
    go test ./controller \
      -run '^(TestConvertCNYBalanceToUSDUsesSynchronizedRate|TestConvertCNYBalanceToUSDRejectsInvalidRate|TestConvertCNYBalanceToUSDRejectsNonFiniteRate|TestGetDeepSeekBalanceUSD|TestRefreshChannelBalancesCapturesAndSanitizesProviderFailure|TestRefreshChannelBalancesCapturesAndSanitizesDatabaseFailure|TestRefreshChannelBalancesReportsMixedOutcome|TestRefreshChannelBalancesReportsAllSuccess|TestRefreshChannelBalancesBoundsFailureDetailsWithoutDroppingCounts|TestWriteChannelBalanceRefreshResponseUsesCompatiblePartialAndFullFailureEnvelopes)$' \
      -count=1
  )
}

run_channel_balance_rust_contracts() {
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --lib 'channel_balance::tests::' -- --test-threads=1
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --lib 'channel_balance_provider::tests::' -- --test-threads=1
}

run_channel_balance() {
  require_loopback_url LMM_TEST_DATABASE_URL
  # Execute the current Go production-oracle vectors and the Rust parser/route
  # contracts in the same gate before checking the durable PostgreSQL side
  # effect. This keeps the balance evidence isolated from unrelated Go failures.
  run_channel_balance_go_oracle
  run_channel_balance_rust_contracts
  TEST_DATABASE_URL="$LMM_TEST_DATABASE_URL" \
    run_exact_api_lib_test channel_balance_store::tests::persisted_balance_updates_value_and_timestamp_together
}

case "$suite" in
  auth) run_auth ;;
  models) run_models ;;
  api-token) run_api_token ;;
  subscription-reset) run_subscription_reset ;;
  migration) run_migration ;;
  announcements) run_announcements ;;
  epay) run_epay ;;
  stripe) run_stripe ;;
  catalog) run_catalog ;;
  token-queries) run_token_queries ;;
  acquisition) run_acquisition ;;
  shared-trust) run_shared_trust ;;
  token-cache) run_token_cache ;;
  relay-settlement) run_relay_settlement ;;
  scripts) run_scripts ;;
  system-config) run_system_config ;;
  relay-timeouts) run_relay_timeouts ;;
  channel-balance) run_channel_balance ;;
  all) run_auth; run_models; run_api_token; run_subscription_reset; run_system_config; run_migration; run_announcements; run_epay; run_stripe; run_catalog; run_token_queries; run_acquisition; run_shared_trust; run_token_cache; run_relay_settlement; run_scripts; run_relay_timeouts; run_channel_balance ;;
  *) usage ;;
esac
