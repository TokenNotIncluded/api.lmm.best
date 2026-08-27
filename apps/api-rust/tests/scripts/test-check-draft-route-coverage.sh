#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
checker="$repo_root/apps/api-rust/tests/scripts/check-draft-route-coverage.sh"
completion_checker="$repo_root/apps/api-rust/tests/scripts/check-draft-route-completion.sh"
runtime=$(mktemp -d "$repo_root/apps/api-rust/.lmm-draft-route-coverage.XXXXXX")

cleanup() {
  rm -rf "$runtime"
}
trap cleanup EXIT

write_gate() {
  local target=$1
  shift
  {
    printf 'method\tpath\tsource_state\tcompile_state\tmount_state\tdifferential_state\tapproval_state\tproduction_owner\tgate_state\tevidence\n'
    local row
    for row in "$@"; do
      printf '%b\n' "$row"
    done
  } >"$target"
}

run_fixture() {
  local root=$1
  local baseline=$2
  local gate=$3
  local expected=$4
  local plan=${5:-"$root/../plan.tsv"}
  DRAFT_ROUTER_ROOT="$root" \
    DRAFT_BASELINE_PATH="$baseline" \
    DRAFT_GATE_PATH="$gate" \
    DRAFT_PLAN_PATH="$plan" \
    DRAFT_EXPECT_BASELINE_COUNT="$expected" \
    DRAFT_OUTSIDE_BASELINE_ALLOWLIST="${DRAFT_OUTSIDE_BASELINE_ALLOWLIST:-}" \
    bash "$checker"
}

write_plan() {
  local target=$1
  shift
  {
    printf 'method\tpath\tplanned_rust_module\n'
    local row
    for row in "$@"; do
      printf '%b\n' "$row"
    done
  } >"$target"
}

write_legacy_stub_ledger() {
  local target=$1
  shift
  {
    printf 'method\tpath\trust_source\tlegacy_handler\tfrozen_ledger\tbehavior_test\trationale\n'
    local row
    for row in "$@"; do
      printf '%b\n' "$row"
    done
  } >"$target"
}

valid="$runtime/valid"
mkdir -p "$valid/src"
cat >"$valid/src/routes.rs" <<'RS'
use axum::{Router, routing::{get, post}};

fn router() -> Router {
    Router::new()
        .route("/api/widgets/{id}", get(list_todos).post(update))
        .route(
            "/v1/models/{*request}",
            axum::routing::post(proxy),
        )
        .route("/healthz", get(health))
        .route("/api/draft", post(not_implemented))
        .route("/api/todo", get(todo_handler))
        .route("/api/panic", get(panic_handler))
        .route("/api/placeholder", get(temporary_placeholder))
        .route("/api/legacy-stub", get(relay_not_implemented))
}

async fn list_todos() {}
async fn update() {}
async fn proxy() {}
async fn health() {}
async fn not_implemented() { unimplemented!() }
async fn todo_handler() { todo!() }
async fn panic_handler() { panic!("fixture") }
async fn temporary_placeholder() {}
async fn relay_not_implemented() -> axum::http::StatusCode {
    axum::http::StatusCode::NOT_IMPLEMENTED
}
RS
printf 'GET\t/api/widgets/:id\thandler.list_todos\nPOST\t/api/widgets/:id\thandler.update\nPOST\t/v1/models/*request\thandler.proxy\nPOST\t/api/draft\thandler.draft\nGET\t/api/todo\thandler.todo\nGET\t/api/panic\thandler.panic\nGET\t/api/placeholder\thandler.placeholder\nGET\t/api/legacy-stub\tcontroller.RelayNotImplemented\n' >"$valid/baseline.tsv"
write_plan "$valid/plan.tsv" \
  'GET\t/api/widgets/:id\tlmm_api_rs::routes::widgets' \
  'POST\t/api/widgets/:id\tlmm_api_rs::routes::widgets' \
  'POST\t/v1/models/*request\tlmm_api_rs::routes::relay' \
  'POST\t/api/draft\tlmm_api_rs::routes::draft' \
  'GET\t/api/todo\tlmm_api_rs::routes::draft' \
  'GET\t/api/panic\tlmm_api_rs::routes::draft' \
  'GET\t/api/placeholder\tlmm_api_rs::routes::draft' \
  'GET\t/api/legacy-stub\tlmm_api_rs::routes::relay'
write_gate "$valid/gate.tsv" \
  'GET\t/api/widgets/:id\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'POST\t/api/widgets/:id\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'POST\t/v1/models/*request\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'POST\t/api/draft\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'GET\t/api/todo\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'GET\t/api/panic\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'GET\t/api/placeholder\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'GET\t/api/legacy-stub\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture'

valid_output=$(run_fixture "$valid/src" "$valid/baseline.tsv" "$valid/gate.tsv" 8)
printf '%s\n' "$valid_output"
expected_summary='draft route coverage: candidate-method-paths=9 frozen-matches=8 frozen-total=8 missing=0 mounted=2 placeholders=4 legacy-stubs=1 outside-baseline=1'
[[ $valid_output == *"$expected_summary"* ]] || {
  echo "valid fixture produced unexpected summary" >&2
  exit 1
}
[[ $valid_output == *'draft route outside frozen baseline: GET /healthz'* ]] || {
  echo "unknown route was not reported" >&2
  exit 1
}
[[ $valid_output == *'draft route placeholder: POST /api/draft'* ]] || {
  echo "placeholder route was not reported" >&2
  exit 1
}
[[ $valid_output == *'draft route frozen legacy stub: GET /api/legacy-stub'* ]] || {
  echo "frozen legacy stub was not reported separately" >&2
  exit 1
}
for expected_placeholder in \
  'draft route placeholder: GET /api/panic' \
  'draft route placeholder: GET /api/placeholder' \
  'draft route placeholder: GET /api/todo'; do
  [[ $valid_output == *"$expected_placeholder"* ]] || {
    echo "$expected_placeholder was not reported" >&2
    exit 1
  }
done
[[ $valid_output == *'not differential verification, migration credit, or production ownership'* ]] || {
  echo "draft-only disclaimer is missing" >&2
  exit 1
}

assert_rejected() {
  local name=$1
  local expected_error=$2
  local fixture="$runtime/$name"
  local output
  if output=$(run_fixture "$fixture/src" "$fixture/baseline.tsv" "$fixture/gate.tsv" 1 2>&1); then
    echo "$name fixture was unexpectedly accepted" >&2
    exit 1
  fi
  [[ $output == *"$expected_error"* ]] || {
    echo "$name fixture failed for the wrong reason: $output" >&2
    exit 1
  }
}

write_outside_allowlist() {
  local target=$1
  shift
  {
    printf 'method\tpath\tcategory\tsource_path\trationale\n'
    local row
    for row in "$@"; do
      printf '%b\n' "$row"
    done
  } >"$target"
}

missing="$runtime/missing"
mkdir -p "$missing/src"
cat >"$missing/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new().route("/api/present", get(present))
}
async fn present() {}
RS
printf 'GET\t/api/present\tpresent.handler\nPOST\t/api/missing\tmissing.handler\n' >"$missing/baseline.tsv"
write_plan "$missing/plan.tsv" \
  'GET\t/api/present\tlmm_api_rs::routes::present' \
  'POST\t/api/missing\tlmm_api_rs::routes::missing_group'
write_gate "$missing/gate.tsv" \
  'GET\t/api/present\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'POST\t/api/missing\tabsent\tnot-applicable\tunmounted\tnot-applicable\tnot-applicable\tgo\tlegacy-go\tfixture'
missing_output=$(DRAFT_REPORT_MISSING=1 run_fixture "$missing/src" "$missing/baseline.tsv" "$missing/gate.tsv" 2)
[[ $missing_output == *'draft route missing: POST /api/missing module=lmm_api_rs::routes::missing_group legacy=missing.handler'* ]] || {
  echo "missing route detail was not reported with its planned module" >&2
  exit 1
}
[[ $missing_output == *'draft route missing group: module=lmm_api_rs::routes::missing_group count=1'* ]] || {
  echo "missing route module grouping was not reported" >&2
  exit 1
}

models_overlap="$runtime/models-overlap"
mkdir -p "$models_overlap/src"
cat >"$models_overlap/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new().route(
        "/v1/models/{*path}",
        post(proxy).delete(relay_not_implemented),
    )
}
async fn proxy() {}
async fn relay_not_implemented() -> axum::http::StatusCode {
    axum::http::StatusCode::NOT_IMPLEMENTED
}
RS
printf 'POST\t/v1/models/*path\tproxy.handler\nDELETE\t/v1/models/:model\tcontroller.RelayNotImplemented\n' >"$models_overlap/baseline.tsv"
write_plan "$models_overlap/plan.tsv" \
  'POST\t/v1/models/*path\tlmm_api_rs::routes::relay' \
  'DELETE\t/v1/models/:model\tlmm_api_rs::routes::relay'
write_gate "$models_overlap/gate.tsv" \
  'POST\t/v1/models/*path\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture' \
  'DELETE\t/v1/models/:model\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture'
models_overlap_output=$(run_fixture "$models_overlap/src" "$models_overlap/baseline.tsv" "$models_overlap/gate.tsv" 2)
[[ $models_overlap_output == *'frozen-matches=2 frozen-total=2 missing=0'* ]] || {
  echo "Axum models wildcard ownership was not normalized to the frozen method paths" >&2
  exit 1
}
[[ $models_overlap_output == *'draft route frozen legacy stub: DELETE /v1/models/:model'* ]] || {
  echo "frozen DELETE model stub was not attributed to its exact legacy path" >&2
  exit 1
}

models_exact="$runtime/models-exact"
mkdir -p "$models_exact/src"
cat >"$models_exact/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new()
        .route("/v1/models/{model}", get(show).delete(relay_not_implemented))
        .route("/v1/models/{model}/{*tail}", post(proxy_tail))
        .route("/v1beta/models/{model}/{*tail}", post(proxy_tail))
}
async fn show() {}
async fn proxy_tail() {}
async fn relay_not_implemented() -> axum::http::StatusCode {
    axum::http::StatusCode::NOT_IMPLEMENTED
}
RS
printf 'GET\t/v1/models/:model\tlookup.handler\nPOST\t/v1/models/*path\tproxy.handler\nDELETE\t/v1/models/:model\tcontroller.RelayNotImplemented\nPOST\t/v1beta/models/*path\tproxy.handler\n' >"$models_exact/baseline.tsv"
write_plan "$models_exact/plan.tsv" \
  'GET\t/v1/models/:model\tlmm_api_rs::routes::relay' \
  'POST\t/v1/models/*path\tlmm_api_rs::routes::relay' \
  'DELETE\t/v1/models/:model\tlmm_api_rs::routes::relay' \
  'POST\t/v1beta/models/*path\tlmm_api_rs::routes::relay'
write_gate "$models_exact/gate.tsv" \
  'GET\t/v1/models/:model\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'POST\t/v1/models/*path\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'DELETE\t/v1/models/:model\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'POST\t/v1beta/models/*path\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
models_exact_output=$(run_fixture "$models_exact/src" "$models_exact/baseline.tsv" "$models_exact/gate.tsv" 4)
[[ $models_exact_output == *'frozen-matches=4 frozen-total=4 missing=0 mounted=4'* ]] || {
  echo "exact and tail Gemini forms did not normalize to their frozen wildcard inventory" >&2
  exit 1
}
[[ $models_exact_output != *'/v1/models/:model/*tail'* ]] || {
  echo "GET/DELETE model-tail guards were incorrectly emitted as route candidates" >&2
  exit 1
}

if DRAFT_ROUTER_ROOT="$valid/src" \
  DRAFT_BASELINE_PATH="$valid/baseline.tsv" \
  DRAFT_GATE_PATH="$valid/gate.tsv" \
  DRAFT_PLAN_PATH="$valid/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=8 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  bash "$completion_checker" >/dev/null 2>&1; then
  echo "completion gate accepted placeholders, a legacy stub, and incomplete mounts" >&2
  exit 1
fi

complete="$runtime/complete"
mkdir -p "$complete/src"
cat >"$complete/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new().route("/v1/models/{model}", delete(relay_not_implemented))
}
async fn relay_not_implemented() -> axum::http::StatusCode {
    axum::http::StatusCode::NOT_IMPLEMENTED
}
RS
printf 'DELETE\t/v1/models/:model\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\n' >"$complete/baseline.tsv"
write_plan "$complete/plan.tsv" \
  'DELETE\t/v1/models/:model\tlmm_api_rs::routes::relay'
runtime_rel=${runtime#"$repo_root/"}
write_gate "$complete/gate.tsv" \
  'DELETE\t/v1/models/:model\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
complete_baseline_sha=$(sha256sum "$complete/baseline.tsv")
complete_baseline_sha=${complete_baseline_sha%% *}
complete_source="$runtime_rel/complete/src/routes.rs"
complete_baseline="$runtime_rel/complete/baseline.tsv@sha256:$complete_baseline_sha"
write_legacy_stub_ledger "$complete/legacy-equivalent-stubs.tsv" \
  "DELETE\\t/v1/models/:model\\t$complete_source\\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\\t$complete_baseline\\t$complete_source\\tfixture legacy-equivalent explicit 501"
complete_output=$(DRAFT_ROUTER_ROOT="$complete/src" \
  DRAFT_BASELINE_PATH="$complete/baseline.tsv" \
  DRAFT_GATE_PATH="$complete/gate.tsv" \
  DRAFT_PLAN_PATH="$complete/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=1 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER="$complete/legacy-equivalent-stubs.tsv" \
  DRAFT_EXPECT_APPROVED_LEGACY_STUBS=1 \
  bash "$completion_checker")
[[ $complete_output == *'draft completion gate passed: missing=0 placeholders=0 approved-legacy-stubs=1'* ]] || {
  echo "completion gate did not accept the audited legacy-equivalent stub fixture" >&2
  exit 1
}

write_legacy_stub_ledger "$complete/source-drift-legacy-equivalent-stubs.tsv" \
  "DELETE\\t/v1/models/:model\\tapps/api-rust/src/http.rs\\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\\t$complete_baseline\\t$complete_source\\tfixture legacy-equivalent explicit 501"
source_drift_output=$(DRAFT_ROUTER_ROOT="$complete/src" \
  DRAFT_BASELINE_PATH="$complete/baseline.tsv" \
  DRAFT_GATE_PATH="$complete/gate.tsv" \
  DRAFT_PLAN_PATH="$complete/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=1 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER="$complete/source-drift-legacy-equivalent-stubs.tsv" \
  DRAFT_EXPECT_APPROVED_LEGACY_STUBS=1 \
  bash "$completion_checker" 2>&1 || true)
[[ $source_drift_output == *'legacy-equivalent stub source drift: DELETE /v1/models/:model'* ]] || {
  echo "completion gate accepted a legacy-equivalent stub from the wrong Rust source" >&2
  exit 1
}

write_legacy_stub_ledger "$complete/stale-legacy-equivalent-stubs.tsv" \
  "DELETE\\t/v1/models/:model\\t$complete_source\\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\\t$runtime_rel/complete/baseline.tsv@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\\t$complete_source\\tfixture legacy-equivalent explicit 501"
if DRAFT_ROUTER_ROOT="$complete/src" \
  DRAFT_BASELINE_PATH="$complete/baseline.tsv" \
  DRAFT_GATE_PATH="$complete/gate.tsv" \
  DRAFT_PLAN_PATH="$complete/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=1 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER="$complete/stale-legacy-equivalent-stubs.tsv" \
  DRAFT_EXPECT_APPROVED_LEGACY_STUBS=1 \
  bash "$completion_checker" >/dev/null 2>&1; then
  echo "completion gate accepted stale legacy-equivalent stub evidence" >&2
  exit 1
fi

unapproved_stub="$runtime/unapproved-stub"
mkdir -p "$unapproved_stub/src"
cat >"$unapproved_stub/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new()
        .route("/v1/models/{model}", delete(relay_not_implemented))
        .route("/v1/another", get(present))
}
async fn relay_not_implemented() -> axum::http::StatusCode {
    axum::http::StatusCode::NOT_IMPLEMENTED
}
async fn present() {}
RS
printf 'DELETE\t/v1/models/:model\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\nGET\t/v1/another\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\n' >"$unapproved_stub/baseline.tsv"
write_plan "$unapproved_stub/plan.tsv" \
  'DELETE\t/v1/models/:model\tlmm_api_rs::routes::relay' \
  'GET\t/v1/another\tlmm_api_rs::routes::relay'
write_gate "$unapproved_stub/gate.tsv" \
  'DELETE\t/v1/models/:model\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture' \
  'GET\t/v1/another\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
unapproved_rel=${unapproved_stub#"$repo_root/"}
unapproved_sha=$(sha256sum "$unapproved_stub/baseline.tsv")
unapproved_sha=${unapproved_sha%% *}
write_legacy_stub_ledger "$unapproved_stub/legacy-equivalent-stubs.tsv" \
  "GET\\t/v1/another\\t$unapproved_rel/src/routes.rs\\tgithub.com/QuantumNous/new-api/controller.RelayNotImplemented\\t$unapproved_rel/baseline.tsv@sha256:$unapproved_sha\\t$unapproved_rel/src/routes.rs\\tfixture stale approved route"
unapproved_output=$(DRAFT_ROUTER_ROOT="$unapproved_stub/src" \
  DRAFT_BASELINE_PATH="$unapproved_stub/baseline.tsv" \
  DRAFT_GATE_PATH="$unapproved_stub/gate.tsv" \
  DRAFT_PLAN_PATH="$unapproved_stub/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=2 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER="$unapproved_stub/legacy-equivalent-stubs.tsv" \
  DRAFT_EXPECT_APPROVED_LEGACY_STUBS=1 \
  bash "$completion_checker" 2>&1 || true)
[[ $unapproved_output == *'unapproved frozen legacy stub: DELETE /v1/models/:model'* ]] || {
  echo "completion gate did not reject an unapproved frozen legacy stub" >&2
  exit 1
}

cat >>"$complete/src/routes.rs" <<'RS'

fn test_helper_router() -> Router {
    Router::new().route("/_test/unknown-helper", get(unknown_helper))
}
async fn unknown_helper() {}
RS
completion_outside_output=$(DRAFT_ROUTER_ROOT="$complete/src" \
  DRAFT_BASELINE_PATH="$complete/baseline.tsv" \
  DRAFT_GATE_PATH="$complete/gate.tsv" \
  DRAFT_PLAN_PATH="$complete/plan.tsv" \
  DRAFT_EXPECT_BASELINE_COUNT=1 \
  DRAFT_OUTSIDE_BASELINE_ALLOWLIST='' \
  DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER="$complete/legacy-equivalent-stubs.tsv" \
  DRAFT_EXPECT_APPROVED_LEGACY_STUBS=1 \
  bash "$completion_checker" 2>&1 || true)
[[ $completion_outside_output == *'outside-baseline=1'* \
  && $completion_outside_output == *'draft completion gate failed: static coverage has missing, placeholder, or outside-baseline routes'* ]] || {
  echo "completion gate did not reject an unknown outside-baseline route" >&2
  exit 1
}

allowlisted="$runtime/allowlisted"
mkdir -p "$allowlisted/src"
cat >"$allowlisted/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new()
        .route("/api/frozen", get(frozen))
        .route("/_internal/probe", get(internal_probe))
}
async fn frozen() {}
async fn internal_probe() {}
RS
printf 'GET\t/api/frozen\thandler.frozen\n' >"$allowlisted/baseline.tsv"
write_plan "$allowlisted/plan.tsv" \
  'GET\t/api/frozen\tlmm_api_rs::routes::frozen'
write_gate "$allowlisted/gate.tsv" \
  'GET\t/api/frozen\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
write_outside_allowlist "$allowlisted/allowlist.tsv" \
  'GET\t/_internal/probe\tinternal\tapps/api-rust/src/http.rs\tfixture internal probe'
allowlisted_output=$(DRAFT_REQUIRE_COMPLETE=1 DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$allowlisted/allowlist.tsv" run_fixture "$allowlisted/src" "$allowlisted/baseline.tsv" "$allowlisted/gate.tsv" 1 2>&1 || true)
[[ $allowlisted_output == *'allowlist source mismatch'* ]] || {
  echo "allowlist source rules did not reject a route from the wrong file" >&2
  exit 1
}

write_outside_allowlist "$allowlisted/allowlist.tsv" \
  'GET\t/_internal/probe\tinternal\t.lmm-draft-route-coverage-placeholder.rs\tfixture internal probe'
if DRAFT_REQUIRE_COMPLETE=1 DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$allowlisted/allowlist.tsv" \
  run_fixture "$allowlisted/src" "$allowlisted/baseline.tsv" "$allowlisted/gate.tsv" 1 >/dev/null 2>&1; then
  echo "allowlist accepted an invalid source path" >&2
  exit 1
fi

relative_allowlisted_source=${allowlisted#"$repo_root/"}/src/routes.rs
write_outside_allowlist "$allowlisted/allowlist.tsv" \
  "GET\\t/_internal/probe\\tinternal\\t$relative_allowlisted_source\\tfixture internal probe"
allowlisted_output=$(DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$allowlisted/allowlist.tsv" run_fixture "$allowlisted/src" "$allowlisted/baseline.tsv" "$allowlisted/gate.tsv" 1)
[[ $allowlisted_output == *'outside-baseline=0 approved-outside-baseline=1'* ]] || {
  echo "approved internal helper was not separately counted" >&2
  exit 1
}
if ! DRAFT_REQUIRE_COMPLETE=1 DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$allowlisted/allowlist.tsv" \
  run_fixture "$allowlisted/src" "$allowlisted/baseline.tsv" "$allowlisted/gate.tsv" 1 >/dev/null; then
  echo "approved internal helper did not remain eligible for complete static coverage" >&2
  exit 1
fi
[[ $allowlisted_output == *'draft route approved outside frozen baseline: GET /_internal/probe category=internal'* ]] || {
  echo "approved internal helper was not reported with its classification" >&2
  exit 1
}

unknown="$runtime/unknown-outside"
mkdir -p "$unknown/src"
cat >"$unknown/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new().route("/api/unknown", get(unknown))
}
async fn unknown() {}
RS
printf 'GET\t/api/frozen\thandler.frozen\n' >"$unknown/baseline.tsv"
write_plan "$unknown/plan.tsv" \
  'GET\t/api/frozen\tlmm_api_rs::routes::frozen'
write_gate "$unknown/gate.tsv" \
  'GET\t/api/frozen\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
write_outside_allowlist "$unknown/allowlist.tsv"
if DRAFT_REQUIRE_COMPLETE=1 DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$unknown/allowlist.tsv" \
  run_fixture "$unknown/src" "$unknown/baseline.tsv" "$unknown/gate.tsv" 1 >/dev/null 2>&1; then
  echo "unknown outside-baseline route passed complete static coverage" >&2
  exit 1
fi
unknown_output=$(DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$unknown/allowlist.tsv" run_fixture "$unknown/src" "$unknown/baseline.tsv" "$unknown/gate.tsv" 1)
[[ $unknown_output == *'outside-baseline=1 approved-outside-baseline=0'* ]] || {
  echo "unknown outside-baseline route was not counted as unresolved" >&2
  exit 1
}

path_alias="$runtime/path-alias"
mkdir -p "$path_alias/src"
cat >"$path_alias/src/routes.rs" <<'RS'
const ITEM_PATH: &str = "/api/items/{id}";
fn owner() -> Router {
    Router::new().route("/api/items/{id}", get(show))
}
fn non_owner() -> Router {
    Router::new().route(ITEM_PATH, get(show))
}
async fn show() {}
RS
printf 'GET\t/api/items/:id\thandler\n' >"$path_alias/baseline.tsv"
write_plan "$path_alias/plan.tsv" \
  'GET\t/api/items/:id\tlmm_api_rs::routes::items'
write_gate "$path_alias/gate.tsv" \
  'GET\t/api/items/:id\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
path_alias_output=$(run_fixture "$path_alias/src" "$path_alias/baseline.tsv" "$path_alias/gate.tsv" 1)
[[ $path_alias_output == *'frozen-matches=1 frozen-total=1 missing=0'* ]] || {
  echo "*_PATH non-owner alias was not deduplicated against its literal owner" >&2
  exit 1
}

alias_only="$runtime/alias-only"
mkdir -p "$alias_only/src"
cat >"$alias_only/src/routes.rs" <<'RS'
const ITEM_PATH: &str = "/api/items/{id}";
fn router() -> Router {
    Router::new().route(ITEM_PATH, get(show))
}
async fn show() {}
RS
printf 'GET\t/api/items/:id\thandler\n' >"$alias_only/baseline.tsv"
write_plan "$alias_only/plan.tsv" \
  'GET\t/api/items/:id\tlmm_api_rs::routes::items'
write_gate "$alias_only/gate.tsv" \
  'GET\t/api/items/:id\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
assert_rejected alias-only 'non-owning *_PATH alias has no literal owner for GET /api/items/:id'

models_forged_duplicate="$runtime/models-forged-duplicate"
mkdir -p "$models_forged_duplicate/src"
cat >"$models_forged_duplicate/src/routes.rs" <<'RS'
fn first_router() -> Router {
    Router::new().route("/v1/models/{model}", get(first))
}
fn forged_router() -> Router {
    Router::new().route("/v1/models/:model", get(forged))
}
async fn first() {}
async fn forged() {}
RS
printf 'GET\t/v1/models/:model\thandler\n' >"$models_forged_duplicate/baseline.tsv"
write_plan "$models_forged_duplicate/plan.tsv" \
  'GET\t/v1/models/:model\tlmm_api_rs::routes::relay'
write_gate "$models_forged_duplicate/gate.tsv" \
  'GET\t/v1/models/:model\tpresent\tunverified\tmounted\tunverified\tnot-applicable\tgo\tmounted-unverified\tfixture'
assert_rejected models-forged-duplicate 'duplicate normalized route GET /v1/models/:model'

duplicate="$runtime/duplicate"
mkdir -p "$duplicate/src"
cat >"$duplicate/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new()
        .route("/api/items/{id}", get(first))
        .route("/api/items/:id", get(second))
}
RS
printf 'GET\t/api/items/:id\thandler\n' >"$duplicate/baseline.tsv"
write_plan "$duplicate/plan.tsv" \
  'GET\t/api/items/:id\tlmm_api_rs::routes::items'
write_gate "$duplicate/gate.tsv" \
  'GET\t/api/items/:id\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture'
assert_rejected duplicate 'duplicate normalized route GET /api/items/:id'

ambiguous="$runtime/ambiguous"
mkdir -p "$ambiguous/src"
cat >"$ambiguous/src/routes.rs" <<'RS'
fn router() -> Router {
    Router::new().route("/api/items", get(first).get(second))
}
RS
printf 'GET\t/api/items\thandler\n' >"$ambiguous/baseline.tsv"
write_plan "$ambiguous/plan.tsv" \
  'GET\t/api/items\tlmm_api_rs::routes::items'
write_gate "$ambiguous/gate.tsv" \
  'GET\t/api/items\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture'
assert_rejected ambiguous 'ambiguous repeated GET method for /api/items'

unparseable="$runtime/unparseable"
mkdir -p "$unparseable/src"
cat >"$unparseable/src/routes.rs" <<'RS'
fn router(path: &str) -> Router {
    Router::new().route(path, get(handler))
}
RS
printf 'GET\t/api/items\thandler\n' >"$unparseable/baseline.tsv"
write_plan "$unparseable/plan.tsv" \
  'GET\t/api/items\tlmm_api_rs::routes::items'
write_gate "$unparseable/gate.tsv" \
  'GET\t/api/items\tpresent\tunverified\tunmounted\tunverified\tnot-applicable\tgo\tlegacy-go\tfixture'
assert_rejected unparseable 'route path identifier must use a *_PATH constant'

echo "draft route coverage checker tests passed"
