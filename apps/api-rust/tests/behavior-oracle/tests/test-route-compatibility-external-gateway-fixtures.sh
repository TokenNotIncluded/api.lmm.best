#!/usr/bin/env bash
# Checks the deterministic, loopback-only provider fixture contract.  This is
# intentionally offline: the Rust tests inject the adapters and start each
# mock HTTP server; this oracle guarantees that every matrix row has a bounded
# success, failure, replay, request-body, and side-effect contract.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixtures=${1:-"$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-external-gateway-fixtures.json"}
matrix=${ROUTE_COMPATIBILITY_MATRIX:-"$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-matrix.tsv"}

for command in awk jq; do
  command -v "$command" >/dev/null || { echo "required command is unavailable: $command" >&2; exit 1; }
done
[[ -f $fixtures ]] || { echo "missing external-gateway fixtures: $fixtures" >&2; exit 1; }
[[ -f $matrix ]] || { echo "missing route matrix: $matrix" >&2; exit 1; }

jq -e '
  .schema_version == 1 and
  .isolation.network == "each mock binds only 127.0.0.1 on an ephemeral port" and
  .isolation.production == "forbidden" and
  (.fixtures | type == "array" and length == 25) and
  ([.fixtures[].id] | unique | length == 25) and
  all(.fixtures[];
    (.route.method | IN("GET", "POST", "DELETE")) and
    (.route.path | type == "string" and startswith("/")) and
    (.adapter | type == "string" and length > 0) and
    (.success.status | type == "number") and
    (.success.body | type == "string" and length > 0) and
    (.success.effect | type == "string" and length > 0) and
    (.failure.status | type == "number") and
    (.failure.body | type == "string") and
    (.failure.effect | type == "string" and length > 0) and
    (.replay.expect | type == "string" and length > 0) and
    (.mock.request.method | IN("GET", "POST", "DELETE")) and
    (.mock.request.path | type == "string" and startswith("/")) and
    (.mock.response.status | type == "number") and
    (.mock.response.body | type == "string")
  ) and
  ([.fixtures[] | select(has("signature"))] | length >= 8)
' "$fixtures" >/dev/null || { echo "malformed external-gateway fixture contract" >&2; exit 1; }

declare -A declared
while IFS=$'\t' read -r method path; do
  key="$method $path"
  [[ -z ${declared[$key]+x} ]] || { echo "duplicate external fixture route: $key" >&2; exit 1; }
  declared[$key]=1
done < <(jq -r '.fixtures[] | [.route.method, .route.path] | @tsv' "$fixtures")

matrix_count=0
while IFS=$'\t' read -r method path; do
  ((matrix_count += 1))
  key="$method $path"
  [[ -n ${declared[$key]+x} ]] || { echo "missing external fixture for matrix route: $key" >&2; exit 1; }
  unset 'declared[$key]'
done < <(awk -F '\t' '!/^#/ && $1 == "external-gateway" { print $2 "\t" $4 }' "$matrix")

[[ $matrix_count == 25 ]] || { echo "expected 25 external-gateway matrix routes, found $matrix_count" >&2; exit 1; }
[[ ${#declared[@]} == 0 ]] || { printf 'external fixture is absent from matrix: %s\n' "${!declared[@]}" >&2; exit 1; }

jq -cn --arg fixtures "$fixtures" --argjson routes "$matrix_count" \
  '{test:"route-compatibility-external-gateway-fixtures",fixtures:$routes,isolated:true,loopback_http:true,production_access:false,result:"contract-passed",approval_credit:false,reason:"Contract validation is not a Go/Rust execution; run the injected local mock adapters for success, failure, signature, replay, and durable-effect evidence."}'
