#!/usr/bin/env bash
# Validates the fixture contract, then executes it through disposable real
# Go/Rust listeners unless --contract-only is explicitly selected.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
contract_only=false
if [[ ${1:-} == --contract-only ]]; then contract_only=true; shift; fi
fixtures=${1:-"$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-transaction-fixtures.json"}
matrix=${ROUTE_COMPATIBILITY_MATRIX:-"$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-matrix.tsv"}

for command in awk jq; do
  command -v "$command" >/dev/null || { echo "required command unavailable: $command" >&2; exit 1; }
done
[[ -f $fixtures ]] || { echo "missing transaction fixtures: $fixtures" >&2; exit 1; }
[[ -f $matrix ]] || { echo "missing route matrix: $matrix" >&2; exit 1; }

jq -e '
  .schema_version == 1 and
  (.isolation.production == "forbidden") and
  (.isolation.network | contains("loopback")) and
  (.fixtures | type == "array" and length == 7) and
  ([.fixtures[].id] | unique | length == 7) and
  all(.fixtures[];
    (.route.method | IN("GET", "POST")) and
    (.route.path | type == "string") and
    (.tables | type == "array" and length > 0) and
    (.positive.expect | type == "string" and length > 0) and
    (.failure.expect | type == "string" and length > 0) and
    (.rollback.inject | type == "string" and length > 0) and
    (.rollback.expect | contains("unchanged")) and
    (.replay.expect | type == "string" and length > 0)
  )
' "$fixtures" >/dev/null || { echo "malformed transaction fixture contract" >&2; exit 1; }

while IFS=$'\t' read -r method path; do
  lookup=${path%%\?*}
  awk -F '\t' -v method="$method" -v path="$lookup" '
    /^#/ || NF == 0 { next }
    $1 == "database-transaction" && $2 == method && $4 == path { found=1 }
    END { exit !found }
  ' "$matrix" || { echo "fixture route is absent from database-transaction matrix: $method $lookup" >&2; exit 1; }
done < <(jq -r '.fixtures[] | [.route.method, .route.path] | @tsv' "$fixtures")

matrix_count=$(awk -F '\t' '!/^#/ && $1 == "database-transaction" { count++ } END { print count + 0 }' "$matrix")
[[ $matrix_count == 7 ]] || { echo "expected 7 database-transaction matrix routes, found $matrix_count" >&2; exit 1; }

jq -cn \
  --arg fixtures "$fixtures" \
  --argjson routes "$(jq '.fixtures | length' "$fixtures")" \
  --argjson phases "$(jq '[.fixtures[] | [.positive, .failure, .rollback, .replay]] | flatten | length' "$fixtures")" \
  '{test:"route-compatibility-transaction-fixtures",fixtures:$routes,phases:$phases,isolated:true,production_access:false,result:"contract-passed",approval_credit:false}'

if ! $contract_only; then
  exec "$repo_root/apps/api-rust/tests/behavior-oracle/tests/route-compatibility-transaction-differential.sh"
fi
