#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
manifest="$repo_root/apps/api-rust/Cargo.toml"
[[ -f $manifest && ! -L $manifest ]] || {
  echo "Rust API Cargo manifest is unavailable: $manifest" >&2
  exit 1
}
suite=${1:-all}

usage() {
  echo "usage: $0 {auth|models|api-token|subscription-reset|migration|system-config|relay-timeouts|all}" >&2
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
  cargo test --locked --manifest-path "$manifest" -p lmm-db-migrate \
    --test "$target" "$test_name" -- --ignored --exact --test-threads=1
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
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test api_token -- --ignored --test-threads=1
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
  run_exact_migration_test full_copy full_copy_should_verify_all_tables_and_rollback_both_fault_phases
  run_exact_migration_test waffo_subscription_schema contract_eight_preserves_pending_evidence_and_rejects_broken_replay_guards
}

run_system_config() {
  require_loopback_url LMM_SYSTEM_CONFIG_TEST_DATABASE_URL
  require_loopback_url LMM_SYSTEM_CONFIG_TEST_VALKEY_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test system_config -- --ignored --test-threads=1
}

run_relay_timeouts() {
  require_loopback_url LMM_TEST_DATABASE_URL
  cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test relay_anthropic_gemini_postgres -- --ignored --test-threads=1
  [[ ${LMM_AUTH_TEST_ALLOW_SCHEMA_RESET:-} == 1 ]] || {
    echo "LMM_AUTH_TEST_ALLOW_SCHEMA_RESET=1 is required for the isolated relay-misc schema reset" >&2
    exit 1
  }
  LMM_RELAY_MISC_TEST_DATABASE_URL="$LMM_TEST_DATABASE_URL" \
    LMM_RELAY_MISC_TEST_ALLOW_SCHEMA_RESET=1 \
    cargo test --locked --manifest-path "$manifest" -p lmm-api-rs \
    --test relay_misc_pg -- --ignored --test-threads=1
}

case "$suite" in
  auth) run_auth ;;
  models) run_models ;;
  api-token) run_api_token ;;
  subscription-reset) run_subscription_reset ;;
  migration) run_migration ;;
  system-config) run_system_config ;;
  relay-timeouts) run_relay_timeouts ;;
  all) run_auth; run_models; run_api_token; run_subscription_reset; run_system_config; run_migration; run_relay_timeouts ;;
  *) usage ;;
esac
