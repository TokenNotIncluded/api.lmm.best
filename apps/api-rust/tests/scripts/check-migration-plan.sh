#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
legacy="${MIGRATION_LEGACY_PATH:-$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv}"
plan="${MIGRATION_PLAN_PATH:-$repo_root/apps/api-rust/tests/fixtures/routes/migration-plan.tsv}"
gate="${MIGRATION_GATE_PATH:-$repo_root/apps/api-rust/tests/fixtures/routes/migration-gate.tsv}"
review="${MIGRATION_INTEGRATION_REVIEW_PATH:-$repo_root/apps/api-rust/tests/fixtures/routes/integration-review.tsv}"
frozen_contract="${MIGRATION_FROZEN_ROUTE_AUTH_PATH:-$repo_root/apps/api-rust/tests/fixtures/routes/frozen-route-auth.tsv}"
route_stub_checker="$repo_root/apps/api-rust/tests/scripts/route-local-stub-state.pl"

# Normalize command input without rewriting tracked route ledgers. This keeps
# the checker stable when a checkout preserves CRLF TSV endings.
tsv_without_crlf() {
  sed 's/\r$//' -- "$1"
}
tsv_first_line() {
  sed -n '1{s/\r$//;p;q;}' -- "$1"
}
expected_header=$'method\tpath\tlegacy_handler\tdomain\tauth_scope\tdata_access\tstreaming\tpriority\tplanned_rust_module\tjob_dependency'
expected_review_header=$'method\tpath\trust_handler\tlistener_differential\tpostgres_evidence\tvalkey_evidence\tdecision\tnotes'
gate_validator="${MIGRATION_GATE_VALIDATOR_PATH:-$repo_root/packaging/common/lmm-api/validate-route-gate}"
release_revision="${MIGRATION_RELEASE_REVISION:-$(git -C "$repo_root" rev-parse HEAD 2>/dev/null || true)}"
evidence_root="${MIGRATION_EVIDENCE_ROOT:-$repo_root}"
snapshot_dir=$(mktemp -d "$repo_root/.migration-gate-snapshot.XXXXXXXX")
asset_manifest=$(mktemp "$repo_root/.migration-gate-assets.XXXXXXXX")
cleanup() {
  rm -rf -- "$snapshot_dir"
  rm -f -- "$asset_manifest"
}
trap cleanup EXIT

[[ -f "$plan" ]] || {
  echo "missing migration plan: $plan" >&2
  exit 1
}
[[ -f "$legacy" ]] || {
  echo "missing frozen legacy route ledger: $legacy" >&2
  exit 1
}
[[ $(tsv_first_line "$plan") == "$expected_header" ]] || {
  echo "invalid migration-plan header" >&2
  exit 1
}

awk -F '\t' '
  function frozen_auth_scope(method, path) {
    if (method == "GET" && (path == "/api/mj/self" || path == "/api/models" || path == "/api/task/self")) return "user"
    if (method == "GET" && (path == "/api/ratio_config" || path == "/api/user/groups")) return "public"
    if (method == "GET" && (path == "/api/usage/token/" || path == "/dashboard/billing/subscription" || path == "/dashboard/billing/usage")) return "token"
    if (method == "POST" && path == "/api/user/topup/complete") return "admin"
    if (method == "POST" && path == "/pg/chat/completions") return "user"
    if ((method == "GET" && path == "/api/ratio_sync/channels") || (method == "POST" && path == "/api/ratio_sync/fetch")) return "root"
    if (method == "GET" && (path == "/api/data/flow/self" || path == "/api/data/self" || path == "/api/log/self" || path == "/api/log/self/search" || path == "/api/log/self/stat" || path == "/api/subscription/plans")) return "user"
    if (method == "GET" && path == "/api/log/token") return "token"
    if (method == "POST" && (path == "/api/subscription/balance/pay" || path == "/api/subscription/creem/pay" || path == "/api/subscription/epay/pay" || path == "/api/subscription/stripe/pay" || path == "/api/subscription/waffo-pancake/pay")) return "user"
    if ((method == "GET" || method == "POST") && path == "/api/subscription/epay/return") return "public"
    return ""
  }
  function is_api_token_route(method, path) {
    return (method == "GET" && (path == "/api/token/" || path == "/api/token/:id" || path == "/api/token/search")) ||
      (method == "POST" && (path == "/api/token/" || path == "/api/token/:id/key" || path == "/api/token/batch" || path == "/api/token/batch/keys")) ||
      (method == "PUT" && path == "/api/token/") ||
      (method == "DELETE" && path == "/api/token/:id")
  }
  NR == 1 { next }
  NF != 10 { printf "line %d: expected 10 tab-separated fields, got %d\n", NR, NF > "/dev/stderr"; failed=1; next }
  $1 !~ /^(GET|POST|PUT|DELETE|PATCH)$/ { printf "line %d: invalid method %s\n", NR, $1 > "/dev/stderr"; failed=1 }
  $4 !~ /^(relay|media-task|identity-user|identity-auth|api-token|channel|billing|deployment|usage-audit|system-config|control-plane)$/ { printf "line %d: invalid domain %s\n", NR, $4 > "/dev/stderr"; failed=1 }
  $5 !~ /^(public|webhook|token|public-or-user|admin|root|user|user-or-token)$/ { printf "line %d: invalid auth scope %s\n", NR, $5 > "/dev/stderr"; failed=1 }
  $6 !~ /^(read|write|read-write|read-write-stream)$/ { printf "line %d: invalid access mode %s\n", NR, $6 > "/dev/stderr"; failed=1 }
  $7 !~ /^(none|websocket|sse-optional)$/ { printf "line %d: invalid streaming mode %s\n", NR, $7 > "/dev/stderr"; failed=1 }
  $8 !~ /^P[0-3]$/ { printf "line %d: invalid priority %s\n", NR, $8 > "/dev/stderr"; failed=1 }
  $9 !~ /^lmm_api_rs::routes::[a-z_]+$/ { printf "line %d: invalid planned module %s\n", NR, $9 > "/dev/stderr"; failed=1 }
  $10 !~ /^(none|deployment-runner|media-worker|relay-upstream)$/ { printf "line %d: invalid job dependency %s\n", NR, $10 > "/dev/stderr"; failed=1 }
  {
    key=$1 "\t" $2
    if (seen[key]++) { printf "line %d: duplicate route %s\n", NR, key > "/dev/stderr"; failed=1 }
    if (is_api_token_route($1, $2)) {
      api_token_routes++
      if ($5 != "user") { printf "line %d: API-token route %s must use frozen UserAuth scope user, got %s\n", NR, key, $5 > "/dev/stderr"; failed=1 }
    }
    frozen_scope = frozen_auth_scope($1, $2)
    if (frozen_scope != "" && $5 != frozen_scope) {
      printf "line %d: frozen route %s must use auth scope %s, got %s\n", NR, key, frozen_scope, $5 > "/dev/stderr"
      failed=1
    }
  }
  END {
    if (NR != 354) { printf "expected 353 routes, got %d\n", NR - 1 > "/dev/stderr"; failed=1 }
    if (api_token_routes != 9) { printf "expected 9 exact API-token authorization rows, got %d\n", api_token_routes > "/dev/stderr"; failed=1 }
    exit failed
  }
' <(tsv_without_crlf "$plan")

cut -f1-3 <(tsv_without_crlf "$plan") | tail -n +2 | diff -u <(tsv_without_crlf "$legacy") -

[[ -f $frozen_contract && ! -L $frozen_contract ]] || {
  echo "missing frozen route/auth contract: $frozen_contract" >&2
  exit 1
}
diff -u <(tsv_without_crlf "$frozen_contract") <(
  awk -F '\t' 'BEGIN { OFS=FS; print "method", "path", "auth_scope" } NR > 1 { print $1, $2, $5 }' \
    <(tsv_without_crlf "$plan")
) || {
  echo "frozen route/auth contract differs from migration-plan membership or auth scope" >&2
  exit 1
}

[[ -f "$gate" ]] || {
  echo "missing migration evidence gate: $gate" >&2
  exit 1
}
[[ -x $gate_validator && ! -L $gate_validator ]] || {
  echo "missing canonical route-gate validator: $gate_validator" >&2
  exit 1
}
{
  printf '%s  migration-gate.tsv\n' "$(sha256sum "$gate" | awk '{print $1}')"
  printf '%s  validate-route-gate\n' "$(sha256sum "$gate_validator" | awk '{print $1}')"
  printf '%s  migration-compatibility.env\n' \
    "$(sha256sum "$repo_root/packaging/common/lmm-api/migration-compatibility.env" | awk '{print $1}')"
  printf '%s  frozen-route-auth.tsv\n' "$(sha256sum "$frozen_contract" | awk '{print $1}')"
} >"$asset_manifest"
asset_manifest_sha=$(sha256sum "$asset_manifest" | awk '{print $1}')
"$gate_validator" --mode source --snapshot-dir "$snapshot_dir" --gate "$gate" --frozen-contract "$frozen_contract" \
  --assets-manifest "$asset_manifest" --assets-manifest-sha256 "$asset_manifest_sha" \
  --evidence-root "$evidence_root" --revision "$release_revision"
[[ -f "$review" ]] || {
  echo "missing integration review: $review" >&2
  exit 1
}
[[ $(tsv_first_line "$review") == "$expected_review_header" ]] || {
  echo "invalid integration-review header" >&2
  exit 1
}
if rg -Fq $'/api/user/logout' <(tsv_without_crlf "$gate") <(tsv_without_crlf "$review"); then
  echo "obsolete /api/user/logout must not appear in migration evidence" >&2
  exit 1
fi

cut -f1-2 <(tsv_without_crlf "$gate") | tail -n +2 | diff -u <(tsv_without_crlf "$legacy" | cut -f1-2) -

evidence_value() {
  local key=$1
  local evidence=$2
  local entry
  local -a entries
  local matches=0
  local value=

  IFS=';' read -r -a entries <<<"$evidence"
  for entry in "${entries[@]}"; do
    if [[ $entry == "$key="* ]]; then
      ((matches += 1))
      value=${entry#"$key="}
    fi
  done
  [[ $matches -eq 1 ]] || return 1
  printf '%s\n' "$value"
}

validate_evidence_keys() {
  local method=$1 path=$2 evidence=$3 entry evidence_key
  local -a entries
  declare -A evidence_keys=()

  [[ -n $evidence ]] || return 0
  IFS=';' read -r -a entries <<<"$evidence"
  for entry in "${entries[@]}"; do
    [[ $entry == *=* ]] || {
      [[ ${#entries[@]} -eq 1 ]] && return 0
      echo "gate $method $path has malformed legacy evidence entry: $entry" >&2
      return 1
    }
    evidence_key=${entry%%=*}
    [[ $evidence_key =~ ^[a-z][a-z0-9_-]*$ ]] || {
      echo "gate $method $path has malformed evidence key: $evidence_key" >&2
      return 1
    }
    if [[ ${evidence_keys[$evidence_key]+x} ]]; then
      echo "gate $method $path has duplicate evidence key: $evidence_key" >&2
      return 1
    fi
    evidence_keys[$evidence_key]=1
  done
}

normalize_ledger_path() {
  case $1 in
  # The legacy ledger uses :name parameters while Axum source uses
  # {name}; normalize every path parameter before checking source mounts.
  *:*) sed -E 's/:([A-Za-z_][A-Za-z0-9_]*)/{\1}/g' <<<"$1" ;;
  *) printf '%s\n' "$1" ;;
  esac
}

is_frozen_model_delete_501() {
  [[ $1 == DELETE && $2 == /v1/models/:model ]]
}

is_uncredited_frozen_legacy_501_candidate() {
  local method=$1 path=$2 source_state=$3 compile_state=$4 mount_state=$5
  local differential_state=$6 approval_state=$7 owner=$8 gate_state=$9
  local expected_handler='github.com/QuantumNous/new-api/controller.RelayNotImplemented'
  local handler_count

  [[ $source_state == present && $compile_state == unverified && $mount_state == mounted &&
    $differential_state == unverified && $approval_state == pending-independent-approval &&
    $owner == go && $gate_state == candidate-pending-independent-approval ]] || return 1
  handler_count=$(awk -F '\t' -v method="$method" -v path="$path" -v handler="$expected_handler" '
    $1 == method && $2 == path && $3 == handler { count++ }
    END { print count + 0 }
  ' <(tsv_without_crlf "$legacy"))
  [[ $handler_count == 1 ]]
}

require_frozen_model_delete_501_evidence() {
  local method=$1 path=$2 source_state=$3 compile_state=$4 mount_state=$5
  local differential_state=$6 approval_state=$7 owner=$8 gate_state=$9 evidence=${10}
  local expected_handler='github.com/QuantumNous/new-api/controller.RelayNotImplemented'
  local handler_count differential_reference differential_path validator

  is_frozen_model_delete_501 "$method" "$path" || return 1
  [[ $source_state == present && $compile_state == verified && $mount_state == mounted &&
    $differential_state == verified && $approval_state == approved && $owner == go &&
    $gate_state == verified-approved ]] || {
    echo "frozen DELETE /v1/models/:model 501 requires fully verified-approved gate state" >&2
    return 1
  }
  handler_count=$(awk -F '\t' -v method="$method" -v path="$path" -v handler="$expected_handler" '
    $1 == method && $2 == path && $3 == handler { count++ }
    END { print count + 0 }
  ' <(tsv_without_crlf "$legacy"))
  [[ $handler_count == 1 ]] || {
    echo "frozen DELETE /v1/models/:model 501 must match exactly one RelayNotImplemented Go owner" >&2
    return 1
  }
  differential_reference=$(evidence_value differential "$evidence") || {
    echo "frozen DELETE /v1/models/:model 501 has no differential evidence" >&2
    return 1
  }
  [[ $differential_reference == *@sha256:* ]] || {
    echo "frozen DELETE /v1/models/:model 501 differential evidence requires SHA-256 pinning" >&2
    return 1
  }
  differential_path=${differential_reference%@sha256:*}
  validator="$repo_root/apps/api-rust/tests/behavior-oracle/tests/validate-frozen-model-delete-501-evidence.sh"
  [[ -x $validator ]] || {
    echo "missing frozen DELETE 501 evidence validator" >&2
    return 1
  }
  "$validator" "$repo_root/$differential_path"
}

while IFS=$'\t' read -r method path source_state compile_state mount_state differential_state approval_state owner gate_state evidence; do
  validate_evidence_keys "$method" "$path" "$evidence" || exit 1
done < <(awk -F '\t' 'NR > 1 { print }' <(tsv_without_crlf "$gate"))

awk -F '\t' '
  NR == 1 { next }
  NF != 8 { printf "review line %d: expected 8 tab-separated fields, got %d\\n", NR, NF > "/dev/stderr"; failed=1; next }
  $1 !~ /^(GET|POST|PUT|DELETE|PATCH)$/ { printf "review line %d: invalid method %s\\n", NR, $1 > "/dev/stderr"; failed=1 }
  $2 == "" { printf "review line %d: empty path\\n", NR > "/dev/stderr"; failed=1 }
  {
    key=$1 "\\t" $2
    if (seen[key]++) { printf "review line %d: duplicate method/path %s\\n", NR, key > "/dev/stderr"; failed=1 }
  }
  END { exit failed }
' <(tsv_without_crlf "$review")

while IFS=$'\t' read -r method path approval_state evidence; do
  [[ $approval_state == approved ]] || continue
  evidence_value approval "$evidence" >/dev/null || {
    echo "approved $method $path has no unique approval evidence reference" >&2
    exit 1
  }
  review_approval_count=$(awk -F '\t' -v method="$method" -v path="$path" '
    NR > 1 && $1 == method && $2 == path && $7 == "approved" { count++ }
    END { print count + 0 }
  ' <(tsv_without_crlf "$review"))
  [[ $review_approval_count -eq 1 ]] || {
    echo "approved $method $path lacks one exact approved integration-review record" >&2
    exit 1
  }
done < <(awk -F '\t' 'NR > 1 { print $1 "\t" $2 "\t" $7 "\t" $10 }' <(tsv_without_crlf "$gate"))

while IFS=$'\t' read -r method path source_state compile_state mount_state differential_state approval_state owner gate_state evidence; do
  router_evidence=$evidence
  if [[ $evidence == *"mount="* ]]; then
    router_evidence=$(evidence_value mount "$evidence") || {
      echo "mounted $method $path has no unique mount reference in evidence" >&2
      exit 1
    }
    router_evidence=${router_evidence%@sha256:*}
    if [[ $router_evidence == *.json ]]; then
      mount_json="$evidence_root/$router_evidence"
      [[ -f $mount_json && ! -L $mount_json ]] || {
        echo "mounted $method $path names missing mount evidence JSON: $router_evidence" >&2
        exit 1
      }
      router_evidence=$(jq -er '.router_path' "$mount_json") || {
        echo "mounted $method $path has no router_path in $mount_json" >&2
        exit 1
      }
    fi
  fi
  source_file="$repo_root/$router_evidence"
  [[ -f $source_file ]] || {
    echo "mounted $method $path names missing router source: $router_evidence" >&2
    exit 1
  }
  router_path=$(normalize_ledger_path "$path") || {
    echo "mounted $method $path uses an unsupported ledger/router parameter syntax" >&2
    exit 1
  }
  # Candidate routers commonly format a route over several lines. Remove
  # Rust line comments before whitespace so a comment cannot satisfy the
  # exact `.route("/path"` source-mount check. Axum represents a Gin
  # `/*path` registration as two non-overlapping routes when the single
  # segment has a distinct handler, so accept that exact pair as one frozen
  # wildcard contract.
  compact_router=$(sed 's#//.*$##' "$source_file" | tr -d '[:space:]')
  route_found=0
  if grep -Fq -- ".route(\"$router_path\"" <<<"$compact_router"; then
    route_found=1
  elif [[ $path == */\** ]]; then
    wildcard_prefix=${path%/*}
    single_path=$(normalize_ledger_path "$wildcard_prefix/{model}")
    tail_path=$(normalize_ledger_path "$wildcard_prefix/{model}/{*tail}")
    if grep -Fq -- ".route(\"$single_path\"" <<<"$compact_router" &&
      grep -Fq -- ".route(\"$tail_path\"" <<<"$compact_router"; then
      route_found=1
    fi
  fi
  if ((route_found == 0)); then
    echo "mounted $method $path lacks exact router mount $router_path in $router_evidence" >&2
    exit 1
  fi
  route_stub_state=complete
  if rg -q -i 'StatusCode::NOT_IMPLEMENTED|not[_ -]?implemented|(^|[^0-9])501([^0-9]|$)' "$source_file"; then
    [[ -f $route_stub_checker && ! -L $route_stub_checker ]] || {
      echo "missing route-local stub checker: $route_stub_checker" >&2
      exit 1
    }
    if [[ $path == */\** ]]; then
      single_stub_state=$(perl "$route_stub_checker" "$source_file" "$method" "$single_path") || {
        echo "could not inspect route-local stub state for $method $path ($single_path)" >&2
        exit 1
      }
      tail_stub_state=$(perl "$route_stub_checker" "$source_file" "$method" "$tail_path") || {
        echo "could not inspect route-local stub state for $method $path ($tail_path)" >&2
        exit 1
      }
      if [[ $single_stub_state == stub || $tail_stub_state == stub ]]; then
        route_stub_state=stub
      elif [[ $single_stub_state != complete || $tail_stub_state != complete ]]; then
        echo "invalid route-local stub state for $method $path" >&2
        exit 1
      fi
    else
      route_stub_state=$(perl "$route_stub_checker" "$source_file" "$method" "$router_path") || {
        echo "could not inspect route-local stub state for $method $path" >&2
        exit 1
      }
      [[ $route_stub_state == complete || $route_stub_state == stub ]] || {
        echo "invalid route-local stub state for $method $path: $route_stub_state" >&2
        exit 1
      }
    fi
  fi
  if [[ $route_stub_state == stub ]]; then
    if is_frozen_model_delete_501 "$method" "$path"; then
      require_frozen_model_delete_501_evidence "$method" "$path" "$source_state" "$compile_state" "$mount_state" "$differential_state" "$approval_state" "$owner" "$gate_state" "$evidence" || exit 1
    elif is_uncredited_frozen_legacy_501_candidate "$method" "$path" "$source_state" "$compile_state" "$mount_state" "$differential_state" "$approval_state" "$owner" "$gate_state"; then
      : # Static legacy-equivalent 501 candidate; it earns no Rust migration credit.
    else
      echo "mounted $method $path has a route-local stub/501 handler in $router_evidence" >&2
      exit 1
    fi
  fi
done < <(awk -F '\t' 'NR > 1 && $5 == "mounted" { print }' <(tsv_without_crlf "$gate"))

# Registration is a normal-listener security route. Its gate row proves the
# route factory contains the exact Axum route; verify the non-comment
# production wiring separately so a comment in main.rs cannot satisfy the
# direct-route mount check. The normal listener now composes the complete
# identity-security router (registration is one route in that router), so do
# not require the old registration-only factory/merge shape here.
registration_mount_state=$(awk -F '\t' '
  NR > 1 && $1 == "POST" && $2 == "/api/user/register" { print $5; found=1 }
  END { if (!found) exit 1 }
' <(tsv_without_crlf "$gate")) || {
  echo "migration gate is missing POST /api/user/register" >&2
  exit 1
}
if [[ $registration_mount_state == mounted ]]; then
  normal_main="${MIGRATION_NORMAL_MAIN_PATH:-$repo_root/apps/api-rust/src/main.rs}"
  [[ -f $normal_main && ! -L $normal_main ]] || {
    echo "mounted registration route requires a regular normal-listener main.rs" >&2
    exit 1
  }
  registration_wiring=$(sed 's#//.*$##' "$normal_main")
  registration_wiring_compact=$(tr -d '[:space:]' <<<"$registration_wiring")
  [[ $registration_wiring_compact == *"letidentity_security=lmm_api_rs::migration_routes::identity_security::router("* ]] || {
    echo "mounted registration route lacks a non-comment full identity-security router construction" >&2
    exit 1
  }
  registration_merge_count=$(grep -oF '.merge(identity_security)' <<<"$registration_wiring_compact" | wc -l)
  [[ $registration_merge_count -eq 1 ]] || {
    echo "mounted registration route requires exactly one non-comment identity-security merge" >&2
    exit 1
  }
fi

expected_root_mounts=$'GET\t/api/about\nGET\t/api/home_page_content\nGET\t/api/notice\nGET\t/api/status\nGET\t/api/token/\nPOST\t/api/token/\nPUT\t/api/token/\nDELETE\t/api/token/:id\nGET\t/api/token/:id\nPOST\t/api/token/:id/key\nPOST\t/api/token/batch\nPOST\t/api/token/batch/keys\nGET\t/api/token/search\nGET\t/api/user/self\nPOST\t/api/user/auth/logout\nPOST\t/api/user/auth/refresh\nPOST\t/api/user/login\nGET\t/v1/models\nGET\t/v1beta/models\nGET\t/v1beta/openai/models'
actual_root_mounts=$(awk -F '\t' -v expected="$expected_root_mounts" '
  BEGIN {
    count = split(expected, routes, "\n")
    for (i = 1; i <= count; i++) authorized[routes[i]] = 1
  }
  NR > 1 && $5 == "mounted" && (($1 "\t" $2) in authorized) {
    print $1 "\t" $2
  }
' <(tsv_without_crlf "$gate") | LC_ALL=C sort)
diff -u <(printf '%s\n' "$expected_root_mounts" | LC_ALL=C sort) <(printf '%s\n' "$actual_root_mounts") || {
  echo "migration gate core root mount inventory must contain exactly 20 authorized local Rust routes" >&2
  exit 1
}

expected_blocked_routes=''
actual_blocked_routes=$(awk -F '\t' 'NR > 1 && $9 == "blocked-sol-stop" { print $1 "\t" $2 }' <(tsv_without_crlf "$gate") | LC_ALL=C sort)
diff -u <(printf '%s\n' "$expected_blocked_routes" | sed '/^$/d' | LC_ALL=C sort) <(printf '%s\n' "$actual_blocked_routes" | sed '/^$/d' | LC_ALL=C sort) || {
  echo "migration gate must block exactly the zero routes stopped by the latest independent Sol review" >&2
  exit 1
}
actual_review_blocks=$(awk -F '\t' 'NR > 1 && $7 == "blocked-sol-stop" { print $1 "\t" $2 }' <(tsv_without_crlf "$review") | LC_ALL=C sort)
diff -u <(printf '%s\n' "$expected_blocked_routes" | sed '/^$/d' | LC_ALL=C sort) <(printf '%s\n' "$actual_review_blocks" | sed '/^$/d' | LC_ALL=C sort) || {
  echo "integration review must record exactly the zero current Sol STOP routes" >&2
  exit 1
}

candidate_dir="$repo_root/apps/api-rust/src/migration_routes"
candidate_mod="$repo_root/apps/api-rust/src/migration_routes.rs"

# Some migration modules are deliberately reusable executors for isolated
# listener fixtures rather than route factories.  They must remain declared
# and compile-checked, but requiring a public `router` from them would either
# invent a route or force a non-production mount.  Keep this list explicit so
# a newly added helper cannot silently evade the module inventory.
is_candidate_helper() {
  case $1 in
  legacy_http | relay_anthropic_gemini_postgres | relay_misc_postgres | sse | test_support) return 0 ;;
  *) return 1 ;;
  esac
}

for helper in legacy_http relay_anthropic_gemini_postgres relay_misc_postgres sse test_support; do
  [[ -f "$candidate_dir/$helper.rs" ]] || {
    echo "declared migration helper is missing: $helper.rs" >&2
    exit 1
  }
  grep -Eq "^pub(\(crate\))? mod $helper;$" "$candidate_mod" || {
    echo "migration helper is not declared: $helper" >&2
    exit 1
  }
done

mapfile -t candidate_files < <(
  while IFS= read -r candidate_file; do
    candidate_name=${candidate_file##*/}
    candidate_name=${candidate_name%.rs}
    is_candidate_helper "$candidate_name" || printf '%s\n' "$candidate_file"
  done < <(find "$candidate_dir" -maxdepth 1 -type f -name '*.rs' -print)
)
candidate_count=${#candidate_files[@]}
mapfile -t declared_candidates < <(
  awk '/^pub mod [a-z0-9_]+;$/ { name=$3; sub(/;$/, "", name); print name }' "$candidate_mod" |
    while IFS= read -r candidate_name; do
      is_candidate_helper "$candidate_name" || printf '%s\n' "$candidate_name"
    done | LC_ALL=C sort
)
declared_candidate_count=${#declared_candidates[@]}
# shellcheck disable=SC1003 # tr receives the quoted backslash character set.
mapfile -t candidate_names < <(
  printf '%s\n' "${candidate_files[@]}" |
    tr '\\' '/' |
    sed -E 's#^.*/##; s#\.rs$##' |
    LC_ALL=C sort
)
diff -u <(printf '%s\n' "${declared_candidates[@]}") <(printf '%s\n' "${candidate_names[@]}") || {
  echo "candidate module declarations and source files differ" >&2
  exit 1
}
candidate_router_count=$(rg -l 'pub fn [a-z_]+\([^)]*\) -> Router' "${candidate_files[@]}" | wc -l)
[[ $candidate_count -eq $declared_candidate_count && $candidate_router_count -eq $candidate_count ]] || {
  echo "expected every declared candidate module to expose one router, found $candidate_router_count/$declared_candidate_count" >&2
  exit 1
}
root_router="${MIGRATION_ROOT_ROUTER_PATH:-$repo_root/apps/api-rust/src/http.rs}"
[[ -f $root_router ]] || {
  echo "missing production root router: $root_router" >&2
  exit 1
}
production_root=$(awk '
  /^[[:space:]]*mod[[:space:]]+tests[[:space:]]*\{/ { boundary=1; exit }
  { print }
  END { if (!boundary) exit 1 }
' "$root_router") || {
  echo "production root router is missing its mod tests boundary" >&2
  exit 1
}
api_import_block=$(printf '%s\n' "$production_root" | awk '
  /^use lmm_api_rs::\{/ {
    if (found++) exit 1
    capture=1
  }
  capture { print }
  capture && /^};$/ { capture=0; closed=1 }
  END { if (found != 1 || !closed || capture) exit 1 }
') || {
  echo "production root router must contain one closed lmm_api_rs grouped import" >&2
  exit 1
}
api_import_compact=$(printf '%s\n' "$api_import_block" | tr -d '[:space:]')
production_root_compact=$(printf '%s\n' "$production_root" | tr -d '[:space:]')
allowed_candidate_import='migration_routes::api_token::{ApiTokenHttpState,ApiTokenPrincipal,api_token_router},'
[[ $api_import_compact == *"$allowed_candidate_import"* ]] || {
  echo "production root router must import exactly the audited api-token symbols" >&2
  exit 1
}
production_without_allowed_import=${production_root_compact/"$allowed_candidate_import"/}
if [[ $production_without_allowed_import == *"migration_routes::"* ]]; then
  echo "only the audited api-token candidate may be imported or referenced before mod tests" >&2
  exit 1
fi
api_token_match_count=$(printf '%s\n' "$production_root_compact" | rg -o 'matchapi_token\{' | wc -l)
[[ $api_token_match_count -eq 1 ]] || {
  echo "production root router must contain exactly one api-token match" >&2
  exit 1
}
api_token_match_suffix=${production_root_compact#*matchapi_token\{}
api_token_match_body="matchapi_token{${api_token_match_suffix%%\}*}}"
expected_api_token_match='matchapi_token{Some(api_token)=>router.merge(mounted_api_token_router(api_token,api_token_legacy_headers,api_token_global_rate_limiter,)),None=>router,}'
[[ $api_token_match_body == "$expected_api_token_match" ]] || {
  echo "production root router must contain the exact audited api-token conditional merge" >&2
  exit 1
}
mounted_api_token_references=$(printf '%s\n' "$production_root" | rg -o 'mounted_api_token_router' | wc -l)
[[ $mounted_api_token_references -eq 2 ]] || {
  echo "mounted_api_token_router must have exactly one production definition and one conditional merge" >&2
  exit 1
}
api_token_router_mounts=$(printf '%s\n' "$production_root" | rg -o 'api_token_router\(mount\.state\.clone\(\)\)' | wc -l)
[[ $api_token_router_mounts -eq 1 ]] || {
  echo "production root router must contain exactly one audited api_token_router mount" >&2
  exit 1
}
root_merge_count=$(printf '%s\n' "$production_root" | rg -n '^[[:space:]]*\.merge\(' | wc -l)
[[ $root_merge_count -eq 2 ]] ||
  {
    echo "production root router must have exactly auth and models merges, found $root_merge_count" >&2
    exit 1
  }
if ! grep -Fq '.merge(auth)' <<<"$production_root" ||
  ! grep -Fq '.merge(models_router(models))' <<<"$production_root"; then
  echo "production root router merge topology is not the audited auth-plus-models shape" >&2
  exit 1
fi
echo "production root topology: 20 authorized core Rust mounts plus C1 extra surfaces; api-token is the sole root-mounted candidate; $((candidate_count - 1)) candidates remain unmounted from the production root"

awk -F '\t' '
  NR == 1 { next }
  {
    state[$9]++
    source += $3 == "present"
    compiled += $4 == "verified"
    mounted += $5 == "mounted"
    unmounted += $5 == "unmounted"
    differential += $6 == "verified"
    approved += $7 == "approved"
    production += $8 == "rs"
  }
  END {
    printf "migration gate evidence: source-present=%d compiled=%d mounted=%d unmounted=%d differential-verified=%d approved=%d production-owned-rust=%d migration-credit=%d\n", source, compiled, mounted, unmounted, differential, approved, production, production
    printf "migration gate states: legacy-go=%d mounted-unverified=%d candidate-pending-independent-approval=%d blocked-sol-stop=%d verified-approved=%d\n", state["legacy-go"], state["mounted-unverified"], state["candidate-pending-independent-approval"], state["blocked-sol-stop"], state["verified-approved"]
  }
' <(tsv_without_crlf "$gate")

echo "migration plan valid: 353 frozen legacy routes covered exactly; route ownership policy satisfied"
