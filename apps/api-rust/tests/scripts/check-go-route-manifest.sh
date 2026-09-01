#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../" && pwd)"
routes_dir="${repo_root}/apps/api-rust/tests/fixtures/routes"
golden_hash_file="${repo_root}/apps/api-rust/tests/fixtures/routes/go-routes.sha256"
legacy_manifest="${routes_dir}/legacy-go-routes.tsv"
rust_manifest="${routes_dir}/rust-implemented-routes.tsv"
normal_manifest="${routes_dir}/rust-normal-mounted-routes.tsv"
fail_closed_shells="${routes_dir}/rust-mounted-fail-closed-shells.tsv"
current_manifest_source="${repo_root}/apps/api-go/cmd/route-manifest/main.go"

for required_file in "${golden_hash_file}" "${legacy_manifest}" "${rust_manifest}" \
  "${normal_manifest}" "${fail_closed_shells}" "${routes_dir}/ownership.tsv"; do
  if [[ ! -f "${required_file}" ]]; then
    echo "missing route contract file: ${required_file}" >&2
    exit 1
  fi
done

[[ -f "${current_manifest_source}" ]] || {
  echo "missing current Go route manifest command: ${current_manifest_source}" >&2
  exit 1
}
current_runtime="$(mktemp -d "${TMPDIR:-/tmp}/lmm-current-go-route.XXXXXX")"
trap 'rm -rf -- "$current_runtime"' EXIT
current_manifest="${current_runtime}/route-manifest.tsv"
(
  cd "${repo_root}/apps/api-go"
  GIN_MODE=release go run ./cmd/route-manifest >"${current_manifest}"
)
[[ -s "${current_manifest}" ]] || {
  echo "current Go route manifest is empty" >&2
  exit 1
}
LC_ALL=C awk -F '\t' '
  NF != 3 || $1 == "" || $2 == "" || $3 == "" {
    print "malformed current Go route at line " FNR > "/dev/stderr"
    failures++
    next
  }
  {
    identity = $1 SUBSEP $2
    if (seen[identity]++) {
      print "duplicate current Go route: " $1 " " $2 > "/dev/stderr"
      failures++
    }
  }
  END { exit failures != 0 }
' "${current_manifest}"
cut -f1-2 "${current_manifest}" | LC_ALL=C sort -u >"${current_runtime}/identities.tsv"
current_route_count="$(wc -l <"${current_runtime}/identities.tsv" | tr -d ' ')"

expected_hash="$(awk 'NR == 1 { print $1 }' "${golden_hash_file}")"
actual_hash="$(sed 's/\r$//' "${legacy_manifest}" | sha256sum | awk '{ print $1 }')"
if [[ "${actual_hash}" != "${expected_hash}" ]]; then
  echo "expected frozen legacy route hash ${expected_hash}, got ${actual_hash}" >&2
  echo "legacy-go-routes.tsv is immutable evidence; regenerate it only from the pinned Go revision" >&2
  exit 1
fi

route_count="$(wc -l <"${legacy_manifest}")"
if [[ "${route_count}" -ne 353 ]]; then
  echo "frozen legacy route manifest contains ${route_count} routes; expected 353" >&2
  exit 1
fi

LC_ALL=C awk -F '\t' '
  NF != 3 || $1 == "" || $2 == "" || $3 == "" {
    print "malformed frozen route at line " FNR > "/dev/stderr"
    failures++
    next
  }
  {
    identity = $1 SUBSEP $2
    if (seen[identity]++) {
      print "duplicate frozen route: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    ordering = $2 "\t" $1
    if (FNR > 1 && previous > ordering) {
      print "frozen route manifest is not sorted at line " FNR > "/dev/stderr"
      failures++
    }
    previous = ordering
  }
  END { exit failures != 0 }
' "${legacy_manifest}"

for required in $'GET\t/v1/realtime\t' $'POST\t/:mode/mj/submit/imagine\t' $'GET\t/api/oauth/:provider\t'; do
  if ! grep -Fq "${required}" "${legacy_manifest}"; then
    echo "route manifest is missing critical dynamic route: ${required}" >&2
    exit 1
  fi
done

awk -F '\t' '
  FNR == NR {
    if ($0 ~ /^#/ || NF == 0) next
    if (NF != 8 || $1 !~ /^[0-9]+$/ || ($2 != "exact" && $2 != "prefix") ||
        ($4 != "go" && $4 != "rust")) {
      print "invalid ownership rule at line " FNR > "/dev/stderr"
      failures++
      next
    }
    priority[++rules] = $1 + 0
    kind[rules] = $2
    pattern[rules] = $3
    owner[rules] = $4
    matched[rules] = 0
    next
  }
  {
    path = $2
    best = -1
    winners = 0
    for (rule = 1; rule <= rules; rule++) {
      applies = (kind[rule] == "exact" && path == pattern[rule]) ||
                (kind[rule] == "prefix" && index(path, pattern[rule]) == 1)
      if (!applies) continue
      if (priority[rule] > best) {
        best = priority[rule]
        winners = 1
        winner = rule
      } else if (priority[rule] == best) {
        winners++
      }
    }
    if (winners == 0) {
      print "unmatched route ownership: " $1 " " path > "/dev/stderr"
      failures++
    } else if (winners > 1) {
      print "ambiguous route ownership at priority " best ": " $1 " " path > "/dev/stderr"
      failures++
    } else {
      matched[winner]++
      owned[owner[winner]]++
      assigned++
    }
  }
  END {
    for (rule = 1; rule <= rules; rule++) {
      if (matched[rule] == 0) {
        print "ownership rule matches no routes: " kind[rule] " " pattern[rule] > "/dev/stderr"
        failures++
      }
    }
    if (assigned != expected) {
      print "ownership assigned " assigned " routes; expected " expected > "/dev/stderr"
      failures++
    }
    if (!failures) {
      print "active production ownership: " (owned["rust"] + 0) " Rust / " (owned["go"] + 0) " Go"
    }
    exit failures != 0
  }
' expected="${route_count}" "${routes_dir}/ownership.tsv" "${legacy_manifest}"

rust_report="$(awk -F '\t' -v current_routes="${current_runtime}/identities.tsv" '
  BEGIN {
    while ((getline line < current_routes) > 0) {
      split(line, fields, "\t")
      current[fields[1] SUBSEP fields[2]] = 1
    }
    close(current_routes)
  }
  FNR == NR {
    legacy[$1 SUBSEP $2] = 1
    next
  }
  $0 ~ /^#/ || NF == 0 { next }
  NF != 3 || $1 == "" || $2 == "" || $3 == "" {
    print "malformed Rust implementation route at line " FNR > "/dev/stderr"
    failures++
    next
  }
  {
    identity = $1 SUBSEP $2
    if (implemented[identity]++) {
      print "duplicate Rust implementation route: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if ($3 ~ /(^|::)(Disabled|FailClosed|Unconfigured)[A-Za-z0-9_]*/) {
      print "known blocker adapter cannot qualify as a Rust implementation: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (!(identity in legacy) && !(identity in current)) {
      print "Rust implementation is absent from frozen and current Go route inventories: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (identity in legacy) frozen_count++
    else current_only_count++
    count++
  }
  END {
    if (failures) exit 1
    printf "%d\t%d\t%d\n", count, frozen_count, current_only_count
  }
' "${legacy_manifest}" "${rust_manifest}")"
IFS=$'\t' read -r rust_count frozen_implementation_count current_only_count <<<"${rust_report}"

shell_report="$(awk -F '\t' \
  -v current_routes="${current_runtime}/identities.tsv" \
  -v legacy_routes="${legacy_manifest}" \
  -v implemented_routes="${rust_manifest}" \
  -v normal_routes="${normal_manifest}" '
  function load_routes(file, target, line, fields, identity) {
    while ((getline line < file) > 0) {
      if (line ~ /^#/ || line == "") continue
      split(line, fields, "\t")
      identity = fields[1] SUBSEP fields[2]
      if (target == "current") current[identity] = 1
      else if (target == "legacy") legacy[identity] = 1
      else if (target == "implemented") implemented[identity] = 1
      else if (target == "normal") normal[identity] = 1
    }
    close(file)
  }
  BEGIN {
    load_routes(current_routes, "current")
    load_routes(legacy_routes, "legacy")
    load_routes(implemented_routes, "implemented")
    load_routes(normal_routes, "normal")
    representative["POST" SUBSEP "/api/subscription/stripe/pay"] = "DisabledCheckoutProvider"
    representative["POST" SUBSEP "/api/user/creem/pay"] = "DisabledStripeCreemGateway"
    representative["POST" SUBSEP "/api/user/pay"] = "DisabledTopupRepository+DisabledEpayGateway"
    representative["POST" SUBSEP "/api/user/waffo/pay"] = "DisabledTopUpGateway"
    representative["POST" SUBSEP "/pg/chat/completions"] = "FailClosedRelayCompatService"
    representative["POST" SUBSEP "/v1/video/generations"] = "FailClosedRelayVideoService"
    representative["GET" SUBSEP "/v1/responses"] = "UnconfiguredResponsesWebSocketService"
  }
  $0 ~ /^#/ || NF == 0 { next }
  NF != 6 || $1 == "" || $2 == "" || $3 == "" || $4 == "" || $5 == "" || $6 == "" {
    print "malformed mounted fail-closed shell at line " FNR > "/dev/stderr"
    failures++
    next
  }
  {
    identity = $1 SUBSEP $2
    if (shell[identity]++) {
      print "duplicate mounted fail-closed shell: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (identity in implemented) {
      print "mounted fail-closed shell is incorrectly counted as implemented: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (identity in normal) {
      print "mounted fail-closed shell is incorrectly counted as a normal mount: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (!(identity in current)) {
      print "mounted fail-closed shell is absent from current Go inventory: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if ($3 !~ /^lmm_api_rs::/ || $4 !~ /^apps\/api-rust\/src\/main\.rs:/ ||
        $5 !~ /(^|\+)(Disabled|FailClosed|Unconfigured)[A-Za-z0-9_]*(\+|$)/ ||
        $6 != "requires-real-adapter-and-capability-test") {
      print "invalid mounted fail-closed shell evidence: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (identity in representative && $5 != representative[identity]) {
      print "representative shell has unexpected blocker adapter: " $1 " " $2 > "/dev/stderr"
      failures++
    }
    if (identity in legacy) frozen_count++
    else current_only_count++
    count++
  }
  END {
    for (identity in representative) {
      if (!(identity in shell)) {
        split(identity, fields, SUBSEP)
        print "missing representative mounted fail-closed shell: " fields[1] " " fields[2] > "/dev/stderr"
        failures++
      }
    }
    if (count != 31) {
      print "mounted fail-closed shell ledger contains " count " routes; expected 31" > "/dev/stderr"
      failures++
    }
    if (failures) exit 1
    printf "%d\t%d\t%d\n", count, frozen_count, current_only_count
  }
' "${fail_closed_shells}")"
IFS=$'\t' read -r shell_count frozen_shell_count current_only_shell_count <<<"${shell_report}"

echo "verified immutable legacy Go route baseline: ${route_count} routes (${actual_hash})"
echo "current Go route inventory: ${current_route_count} identities"
echo "Rust implementation coverage: ${rust_count} routes (${frozen_implementation_count}/${route_count} frozen + ${current_only_count} current-only); implementation does not imply production ownership"
echo "Mounted fail-closed compatibility shells: ${shell_count} routes (${frozen_shell_count} frozen + ${current_only_shell_count} current-only); no implementation or ownership credit"
