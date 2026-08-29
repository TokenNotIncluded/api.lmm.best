#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../../../.." && pwd -P)
checker="$repo_root/apps/api-rust/tests/scripts/check-complete-route-coverage.sh"
runtime=$(mktemp -d "${TMPDIR:-/tmp}/lmm-complete-route-coverage-test.XXXXXX")
trap 'rm -rf -- "$runtime"' EXIT
fail() { printf 'complete route coverage self-test: %s\n' "$*" >&2; exit 1; }
expect_fail() { "$@" >"$runtime/out" 2>"$runtime/err" && fail "expected failure: $*" || true; }

write_gate() {
  printf '%s\n' \
    $'method\tpath\tsource_state\tcompile_state\tmount_state\tdifferential_state\tapproval_state\tproduction_owner\tgate_state\tevidence' \
    "$@" >"$runtime/gate.tsv"
}

printf '%s\n' \
  $'GET\t/candidate\tgo.candidate' \
  $'GET\t/stub\tgo.stub' \
  $'POST\t/blocked\tgo.blocked' \
  $'POST\t/shell\tgo.shell' \
  $'GET\t/static\tgo.static' >"$runtime/manifest.tsv"
cp -- "$runtime/manifest.tsv" "$runtime/frozen.tsv"
printf '%s\n' $'GET\t/retired\tgo.retired' >>"$runtime/frozen.tsv"
write_gate $'GET\t/retired\tabsent\tnot-applicable\tunmounted\tnot-applicable\tnot-applicable\tgo\tlegacy-go\tretired=true;fixture=absent'
printf '%s\n' '# method	path	Rust handler' $'GET\t/candidate\trust.candidate' >"$runtime/implemented.tsv"
printf '%s\n' \
  $'method\tpath\trust_source\tlegacy_handler\tfrozen_ledger\tbehavior_test\trationale' \
  $'GET\t/stub\trust.stub\tgo.stub\tfrozen\ttest\texplicit 501' >"$runtime/stubs.tsv"
printf '%s\n' \
  $'method\tpath\tadapter/boundary\tcurrent fail-closed behavior\tfrozen Go capability\treusable real implementation if any\tpriority\trequired safe test configuration\tsource evidence\tnotes' \
  $'POST\t/blocked\tDenyProvider\tfixed fail-closed\tfrozen\tfixture\tP1\tisolation\tsource\tnotes' \
  $'POST\t/shell\tFailClosedProvider\tfixed fail-closed\tfrozen\tnone\tP1\tisolation\tsource\tnotes' >"$runtime/blockers.tsv"
printf '%s\n' '# method	path	source evidence' $'GET\t/candidate\trust.mount' >"$runtime/normal.tsv"
printf '%s\n' \
  '# method	path	Rust handler	ordinary-listener mount evidence	production blocker adapter	promotion gate' \
  $'POST\t/shell\tlmm_api_rs::shell_router\tapps/api-rust/src/main.rs:shell\tFailClosedProvider\trequires-real-adapter-and-capability-test' >"$runtime/shells.tsv"
: >"$runtime/mcp.tsv"
mkdir "$runtime/results"
printf '%s\n' '{"method":"GET","path":"/candidate","differential_verified":true,"differences":null,"mismatch_names":[]}' >"$runtime/results/candidate.jsonl"

run() {
  COMPLETE_GO_MANIFEST="$runtime/manifest.tsv" \
  COMPLETE_FROZEN_LEDGER="$runtime/frozen.tsv" \
  COMPLETE_RUST_IMPLEMENTED_LEDGER="$runtime/implemented.tsv" \
  COMPLETE_LEGACY_STUB_LEDGER="$runtime/stubs.tsv" \
  COMPLETE_RUNTIME_BLOCKERS_LEDGER="$runtime/blockers.tsv" \
  COMPLETE_NORMAL_MOUNTS_LEDGER="$runtime/normal.tsv" \
  COMPLETE_FAIL_CLOSED_SHELLS_LEDGER="$runtime/shells.tsv" \
  COMPLETE_MIGRATION_GATE="$runtime/gate.tsv" \
  COMPLETE_MCP_PATHS="$runtime/mcp.tsv" \
  COMPLETE_DIFFERENTIAL_RESULTS_DIR="$runtime/results" \
  bash "$checker"
}

bash -n "$checker" "$0"
# Draft completion may consume the retired set only inside CI jobs that have
# already made the live-manifest complete checker a required result. Stable
# Rust binary publication is retired, so no release workflow is an owner.
workflow="$repo_root/.github/workflows/ci.yml"
perl -0777 -e '
  my $text = <>;
  my $complete = index($text, "run_check complete-route-coverage ");
  my $completion = index($text, "run_check draft-route-completion ");
  exit !($complete >= 0 && $completion > $complete);
' "$workflow" || fail "draft completion is not ordered after complete route coverage in $workflow"
run >"$runtime/report.jsonl"
[[ $(wc -l <"$runtime/report.jsonl" | tr -d ' ') == 6 ]] || fail 'expected five route rows and one summary row'
for class in differential-candidate legacy-501 provider-blocked mounted-fail-closed-shell static-only; do
  grep -Fq "\"class\":\"$class\"" "$runtime/report.jsonl" || fail "missing $class"
done
grep -Fq '"differential_verified":true' "$runtime/report.jsonl" || fail 'differential result was not carried through'
grep -Fq '"coverage_complete":true' "$runtime/report.jsonl" || fail 'summary lacks coverage_complete'
grep -Fq '"parity_claimed":false' "$runtime/report.jsonl" || fail 'summary must not claim parity'
grep -Fq '"rust_normal":"fail-closed-compatibility-shell"' "$runtime/report.jsonl" || fail 'shell was reported as a normal runtime-capable mount'

printf '%s\n' $'POST\t/shell\trust.shell' >>"$runtime/implemented.tsv"
expect_fail run
grep -Fq 'mounted fail-closed shell is incorrectly counted as implemented: POST /shell' "$runtime/err" || fail 'implemented shell failed for the wrong reason'
sed -i '$d' "$runtime/implemented.tsv"

printf '%s\n' $'POST\t/shell\trust.mount' >>"$runtime/normal.tsv"
expect_fail run
grep -Fq 'mounted fail-closed shell is incorrectly counted as a normal mount: POST /shell' "$runtime/err" || fail 'normal-mounted shell failed for the wrong reason'
sed -i '$d' "$runtime/normal.tsv"

printf '%s\n' $'POST\t/blocked\tlmm_api_rs::DisabledProvider' >>"$runtime/implemented.tsv"
printf '%s\n' $'POST\t/blocked\trust.mount' >>"$runtime/normal.tsv"
expect_fail run
grep -Fq 'known blocker adapter cannot qualify as implemented: POST /blocked' "$runtime/err" || fail 'known blocker adapter gained implementation credit'
sed -i '$d' "$runtime/implemented.tsv"
sed -i '$d' "$runtime/normal.tsv"

printf '%s\n' '{"method":"GET","path":"/missing","differential_verified":false}' >"$runtime/results/missing.json"
expect_fail run
rm "$runtime/results/missing.json"

printf '%s\n' $'GET\t/candidate\tgo.candidate' >>"$runtime/manifest.tsv"
expect_fail run
grep -Fq 'duplicate Go inventory route: GET /candidate' "$runtime/err" || fail 'duplicate Go route failed for the wrong reason'
sed -i '$d' "$runtime/manifest.tsv"

# A valid retired entry is accepted only while it remains absent from the live
# Go manifest and from every Rust ownership/stub ledger.
printf '%s\n' $'GET\t/retired\tgo.retired' >>"$runtime/manifest.tsv"
expect_fail run
grep -Fq 'retired route was reintroduced in current Go manifest: GET /retired' "$runtime/err" || fail 'reintroduced retired Go route was not rejected'
sed -i '$d' "$runtime/manifest.tsv"

printf '%s\n' $'GET\t/retired\trust.retired' >>"$runtime/implemented.tsv"
expect_fail run
grep -Fq 'retired route remains in Rust implemented ledger: GET /retired' "$runtime/err" || fail 'implemented retired route was not rejected'
sed -i '$d' "$runtime/implemented.tsv"

printf '%s\n' $'GET\t/retired\trust.retired' >>"$runtime/normal.tsv"
expect_fail run
grep -Fq 'retired route remains in Rust normal mount ledger: GET /retired' "$runtime/err" || fail 'mounted retired route was not rejected'
sed -i '$d' "$runtime/normal.tsv"

printf '%s\n' $'GET\t/retired\trust.stub\tgo.retired\tfrozen\ttest\texplicit 501' >>"$runtime/stubs.tsv"
expect_fail run
grep -Fq 'retired route remains in legacy stub ledger: GET /retired' "$runtime/err" || fail 'stubbed retired route was not rejected'
sed -i '$d' "$runtime/stubs.tsv"

write_gate $'GET\t/fake-retired\tabsent\tnot-applicable\tunmounted\tnot-applicable\tnot-applicable\tgo\tlegacy-go\tretired=true;fixture=fake'
expect_fail run
grep -Fq 'retired route is not frozen: GET /fake-retired' "$runtime/err" || fail 'non-frozen retired route was not rejected'
write_gate $'GET\t/retired\tpresent\tnot-applicable\tunmounted\tnot-applicable\tnot-applicable\tgo\tlegacy-go\tretired=true;fixture=invalid'
expect_fail run
grep -Fq 'retired migration gate row has invalid state' "$runtime/err" || fail 'invalid retired state tuple was not rejected'
write_gate $'GET\t/retired\tabsent\tnot-applicable\tunmounted\tnot-applicable\tnot-applicable\tgo\tlegacy-go\tretired=true;fixture=absent'

printf '%s\n' $'GET\t/unclassified\tgo.unclassified' >>"$runtime/manifest.tsv"
expect_fail env COMPLETE_REQUIRE_EXPLICIT_CLASSIFICATION=1 \
  COMPLETE_GO_MANIFEST="$runtime/manifest.tsv" \
  COMPLETE_FROZEN_LEDGER="$runtime/frozen.tsv" \
  COMPLETE_RUST_IMPLEMENTED_LEDGER="$runtime/implemented.tsv" \
  COMPLETE_LEGACY_STUB_LEDGER="$runtime/stubs.tsv" \
  COMPLETE_RUNTIME_BLOCKERS_LEDGER="$runtime/blockers.tsv" \
  COMPLETE_NORMAL_MOUNTS_LEDGER="$runtime/normal.tsv" \
  COMPLETE_FAIL_CLOSED_SHELLS_LEDGER="$runtime/shells.tsv" \
  COMPLETE_MIGRATION_GATE="$runtime/gate.tsv" \
  COMPLETE_MCP_PATHS="$runtime/mcp.tsv" bash "$checker"
sed -i '$d' "$runtime/manifest.tsv"

# Representative payment, relay, video, and Responses WebSocket shells must
# not regain implementation credit until their blocker adapter is replaced and
# the classification gains real capability evidence.
representatives=(
  $'POST\t/api/subscription/stripe/pay'
  $'POST\t/api/user/creem/pay'
  $'POST\t/api/user/pay'
  $'POST\t/api/user/waffo/pay'
  $'POST\t/pg/chat/completions'
  $'POST\t/v1/video/generations'
  $'GET\t/v1/responses'
)
for route in "${representatives[@]}"; do
  awk -F '\t' -v route="$route" '$1 "\t" $2 == route { found++ } END { exit found != 1 }' \
    "$repo_root/apps/api-rust/tests/fixtures/routes/rust-mounted-fail-closed-shells.tsv" || fail "representative shell is not classified exactly once: $route"
  ! awk -F '\t' -v route="$route" '$1 "\t" $2 == route { found=1 } END { exit !found }' \
    "$repo_root/apps/api-rust/tests/fixtures/routes/rust-implemented-routes.tsv" || fail "representative shell re-entered implemented status: $route"
  ! awk -F '\t' -v route="$route" '$1 "\t" $2 == route { found=1 } END { exit !found }' \
    "$repo_root/apps/api-rust/tests/fixtures/routes/rust-normal-mounted-routes.tsv" || fail "representative shell re-entered normal-mounted status: $route"
done

# Current repository ledgers are consumable with a safe manifest override; no
# backend build or listener starts during this self-test. The retired route is
# removed and the three current-only shells are added to model the live set.
awk -F '\t' '!($1 == "GET" && $2 == "/api/option/waffo-pancake/catalog")' \
  "$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv" >"$runtime/current-manifest.tsv"
printf '%s\n' \
  $'POST\t/pg/images/edits\tgo.current-only' \
  $'POST\t/pg/images/generations\tgo.current-only' \
  $'GET\t/v1/responses\tgo.current-only' >>"$runtime/current-manifest.tsv"
COMPLETE_GO_MANIFEST="$runtime/current-manifest.tsv" \
COMPLETE_MCP_PATHS="$runtime/mcp.tsv" bash "$checker" >"$runtime/current-ledger-report.jsonl"
grep -Fq '"total":355' "$runtime/current-ledger-report.jsonl" || fail 'current repository ledgers could not be checked'
echo 'complete route coverage self-test: passed'
