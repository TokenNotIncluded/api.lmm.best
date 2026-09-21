#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../../.." && pwd)"
ledger="${TEST_INSTANCE_RUNTIME_BLOCKERS_LEDGER:-$repo_root/apps/api-rust/tests/fixtures/routes/test-instance-runtime-blockers.tsv}"
legacy="$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv"

fail() {
  echo "test-instance runtime blocker ledger: $*" >&2
  exit 1
}
[[ -f "$ledger" ]] || fail "missing ledger: $ledger"
[[ -f "$legacy" ]] || fail "missing frozen Go route manifest: $legacy"

expected_header=$'method\tpath\tadapter/boundary\tcurrent fail-closed behavior\tfrozen Go capability\treusable real implementation if any\tpriority\trequired safe test configuration\tsource evidence\tnotes'
actual_header="$(head -n 1 "$ledger")"
[[ "$actual_header" == "$expected_header" ]] || fail "wrong header"

command -v awk >/dev/null || fail "awk is required"
command -v sort >/dev/null || fail "sort is required"
command -v sha256sum >/dev/null || fail "sha256sum is required"

# This digest is the deterministic source-derived inventory of mounted
# method/path/adapter triples.  It is regenerated from safe_candidate_surface
# and each selected router definition when the adapter wiring changes.  The
# ledger must cover the inventory exactly: a valid-but-omitted row is a failure.
source_inventory_rows=159
source_inventory_paths=145
source_inventory_adapters=32
source_inventory_key_digest=2be730980c4f59f4a81134905a8db99a6baec526cc7a08b35918b102253869f4 # gitleaks:allow -- source inventory digest

tmp_dir="$(mktemp -d "${TMPDIR:-/tmp}/test-instance-runtime-blockers.XXXXXX")"
trap 'rm -rf "$tmp_dir"' EXIT

awk -F '\t' -v legacy="$legacy" '
  BEGIN {
    while ((getline line < legacy) > 0) {
      split(line, c, "\t")
      if (c[1] != "" && c[2] != "") frozen[c[1] SUBSEP c[2]] = 1
    }
    close(legacy)
    valid["GET"] = valid["POST"] = valid["PUT"] = valid["DELETE"] = 1
    valid["PATCH"] = valid["HEAD"] = valid["OPTIONS"] = valid["TRACE"] = valid["CONNECT"] = 1
  }
  NR == 1 { next }
  {
    if (NF != 10) { print "line " NR ": expected 10 TSV fields" > "/dev/stderr"; bad = 1; next }
    for (i = 1; i <= 10; i++) if ($i == "") { print "line " NR ": empty field " i > "/dev/stderr"; bad = 1 }
    if (!valid[$1]) { print "line " NR ": invalid method " $1 > "/dev/stderr"; bad = 1 }
    if ($7 != "P1" && $7 != "P2" && $7 != "P3") { print "line " NR ": invalid priority " $7 > "/dev/stderr"; bad = 1 }
    key = $1 SUBSEP $2 SUBSEP $3
    if (seen[key]++) { print "line " NR ": duplicate METHOD/path+adapter key" > "/dev/stderr"; bad = 1 }
    route = $1 SUBSEP $2
    if (!(route in frozen)) { print "line " NR ": path is not frozen in legacy Go manifest: " $1 " " $2 > "/dev/stderr"; bad = 1 }
    order = $1 "\t" $2 "\t" $3
    if (NR > 2 && previous > order) { print "line " NR ": unstable sort order" > "/dev/stderr"; bad = 1 }
    previous = order
    exact[route] = 1
    adapters[$3] = 1
    rows++
  }
  END {
    if (rows == 0) { print "ledger has zero rows" > "/dev/stderr"; bad = 1 }
    for (x in exact) paths++
    for (x in adapters) count++
    if (!bad) {
      print "total exact paths: " rows
      print "unique frozen paths: " paths
      print "adapter count: " count
    }
    exit bad
  }
' "$ledger"

actual_inventory_rows="$(tail -n +2 "$ledger" | cut -f1-3 | sort | wc -l | tr -d ' ')"
actual_inventory_paths="$(tail -n +2 "$ledger" | cut -f1-2 | sort -u | wc -l | tr -d ' ')"
actual_inventory_adapters="$(tail -n +2 "$ledger" | cut -f3 | sort -u | wc -l | tr -d ' ')"
actual_inventory_key_digest="$(tail -n +2 "$ledger" | cut -f1-3 | sort | sha256sum | awk '{print $1}')"
[[ "$actual_inventory_rows" == "$source_inventory_rows" ]] || fail "source-derived inventory row coverage mismatch: expected $source_inventory_rows, got $actual_inventory_rows"
[[ "$actual_inventory_paths" == "$source_inventory_paths" ]] || fail "source-derived inventory path coverage mismatch: expected $source_inventory_paths, got $actual_inventory_paths"
[[ "$actual_inventory_adapters" == "$source_inventory_adapters" ]] || fail "source-derived inventory adapter coverage mismatch: expected $source_inventory_adapters, got $actual_inventory_adapters"
[[ "$actual_inventory_key_digest" == "$source_inventory_key_digest" ]] || fail "source-derived adapter-to-router inventory digest mismatch"
echo "source-derived inventory: $actual_inventory_rows exact rows, $actual_inventory_paths unique frozen paths, $actual_inventory_adapters adapters"

while IFS= read -r evidence; do
  IFS=';' read -ra references <<<"$evidence"
  for reference in "${references[@]}"; do
    source_path="${reference%:*}"
    source_line="${reference##*:}"
    [[ -f "$repo_root/$source_path" ]] || fail "unknown source path: $source_path"
    [[ "$source_line" =~ ^[0-9]+$ ]] || fail "invalid source line: $reference"
    [[ "$source_line" -le "$(wc -l <"$repo_root/$source_path")" ]] || fail "source line out of range: $reference"
  done
done < <(tail -n +2 "$ledger" | awk -F '\t' '{print $9}')

if [[ "${1:-}" == "--self-test" ]]; then
  cp "$ledger" "$tmp_dir/base.tsv"
  TEST_INSTANCE_RUNTIME_BLOCKERS_LEDGER="$tmp_dir/base.tsv" "$0" >/dev/null
  sed '1s/^method/wrong/' "$tmp_dir/base.tsv" >"$tmp_dir/header.tsv"
  TEST_INSTANCE_RUNTIME_BLOCKERS_LEDGER="$tmp_dir/header.tsv" "$0" >/dev/null 2>&1 && fail "wrong-header self-test passed" || true
  awk 'NR == 2 {$4=""} {print}' OFS='\t' "$tmp_dir/base.tsv" >"$tmp_dir/empty.tsv"
  TEST_INSTANCE_RUNTIME_BLOCKERS_LEDGER="$tmp_dir/empty.tsv" "$0" >/dev/null 2>&1 && fail "empty-field self-test passed" || true
  awk 'NR == 3 {$1="BOGUS"} {print}' OFS='\t' "$tmp_dir/base.tsv" >"$tmp_dir/method.tsv"
  TEST_INSTANCE_RUNTIME_BLOCKERS_LEDGER="$tmp_dir/method.tsv" "$0" >/dev/null 2>&1 && fail "invalid-method self-test passed" || true
  echo "negative self-tests: passed"
fi
