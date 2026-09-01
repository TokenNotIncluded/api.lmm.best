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
  echo "usage: $0 {auth|models|api-token|subscription-reset|all}" >&2
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
  cargo test --locked --manifest-path "$manifest" -p lmm-db-migrate \
    --test full_copy subscription_reset_manifest_should_copy_representative_rows -- \
    --ignored --exact --test-threads=1
}

case "$suite" in
  auth) run_auth ;;
  models) run_models ;;
  api-token) run_api_token ;;
  subscription-reset) run_subscription_reset ;;
  all) run_auth; run_models; run_api_token; run_subscription_reset ;;
  *) usage ;;
esac
