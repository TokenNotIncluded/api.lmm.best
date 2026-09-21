#!/usr/bin/env bash
# Executes the fail-closed half of every external-gateway route against the
# real disposable Rust test-instance listener.  The listener is assembled by
# the binary itself; no production listener, provider credential, or network
# destination is accepted.  Positive provider execution stays deliberately
# blocked because that listener injects only Disabled*/Deny* provider adapters.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixture_check="$repo_root/apps/api-rust/tests/behavior-oracle/tests/test-route-compatibility-external-gateway-fixtures.sh"
listener_runner="$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-local-listeners.sh"

"$fixture_check" >/dev/null
output=$(ROUTE_COMPATIBILITY_INCLUDE_CLASSES=external-gateway bash "$listener_runner")
printf '%s\n' "$output"

# Cargo and the disposable listener bootstrap can write progress lines before
# the differential JSON. Keep only parseable JSON records before selecting the
# final listener summary.
summary=$(jq -Rrc 'fromjson? | select(.test == "route-compatibility-listener-boundary")' <<<"$output" \
  | jq -cs 'last')
executed=$(jq -r '.routes // empty' <<<"$summary")
matched=$(jq -r '.transport_matches // empty' <<<"$summary")
[[ $executed == 27 ]] || { echo "expected 27 executed external-gateway routes, found ${executed:-none}" >&2; exit 1; }
[[ $matched =~ ^[0-9]+$ ]] || { echo "missing matched-route count" >&2; exit 1; }

# Keep the blocked count tied to the compiled test-instance composition rather
# than presenting its intentional safe defaults as successful provider runs.
test_instance="$repo_root/apps/api-rust/src/test_instance.rs"
for adapter in \
  TestInstanceDisabledRatioSyncUpstream \
  DisabledEpayGateway \
  DisabledStripeCreemGateway \
  DenyTopUpGateway \
  DenyWebhookAvailability \
  DenyRelayVideo \
  DenyRelayMisc \
  TestInstanceRelayBackend; do
  rg -Fq "$adapter" "$test_instance" || {
    echo "test-instance provider block changed; re-audit positive fixture eligibility: $adapter" >&2
    exit 1
  }
done

jq -cn \
  --argjson executed "$executed" \
  --argjson matched "$matched" \
  --argjson blocked 27 \
  '{test:"route-compatibility-external-gateway-real-listener",executed:$executed,matched:$matched,blocked_positive_or_replay:$blocked,listener:"real disposable Rust test-instance",mock_executor:"in-process fail-closed adapters",network:"loopback-only",production_access:false,reason:"success/replay requires an injected loopback provider executor; the compiled test instance intentionally injects only disabled adapters"}'
