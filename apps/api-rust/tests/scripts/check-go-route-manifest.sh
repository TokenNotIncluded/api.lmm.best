#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../../" && pwd)"
routes_dir="${repo_root}/apps/api-rust/tests/fixtures/routes"
golden_hash_file="${repo_root}/apps/api-rust/tests/fixtures/routes/go-routes.sha256"
legacy_manifest="${routes_dir}/legacy-go-routes.tsv"
rust_manifest="${routes_dir}/rust-implemented-routes.tsv"
current_manifest_source="${repo_root}/apps/api-go/cmd/route-manifest/main.go"

for required_file in "${golden_hash_file}" "${legacy_manifest}" "${rust_manifest}" \
  "${routes_dir}/ownership.tsv"; do
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

echo "verified immutable legacy Go route baseline: ${route_count} routes (${actual_hash})"
echo "current Go route inventory: ${current_route_count} identities"
echo "Rust implementation coverage: ${rust_count} routes (${frozen_implementation_count}/${route_count} frozen + ${current_only_count} current-only); implementation does not imply production ownership"
