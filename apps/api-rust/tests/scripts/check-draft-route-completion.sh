#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../.." && pwd -P)
checker="$repo_root/apps/api-rust/tests/scripts/check-draft-route-coverage.sh"
legacy=${DRAFT_BASELINE_PATH:-"$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv"}
outside_allowlist=${DRAFT_OUTSIDE_BASELINE_ALLOWLIST-"$repo_root/apps/api-rust/tests/fixtures/routes/draft-route-completion-allowlist.tsv"}
legacy_stub_ledger=${DRAFT_LEGACY_EQUIVALENT_STUB_LEDGER-"$repo_root/apps/api-rust/tests/fixtures/routes/legacy-equivalent-stubs.tsv"}
expected_legacy_stub_count=${DRAFT_EXPECT_APPROVED_LEGACY_STUBS:-12}

require_approved_legacy_stubs() {
  local coverage_output=$1
  local expected_handler='github.com/QuantumNous/new-api/controller.RelayNotImplemented'
  local legacy_relative legacy_sha expected_frozen_ledger
  local header line line_number=1 method path rust_source handler frozen_ledger behavior_test rationale
  local key source_file source_line baseline_count
  local -a stub_lines
  local -A ledger_sources=() ledger_seen=()

  [[ $expected_legacy_stub_count =~ ^[1-9][0-9]*$ ]] || {
    echo "DRAFT_EXPECT_APPROVED_LEGACY_STUBS must be a positive integer" >&2
    return 1
  }
  [[ -f $legacy_stub_ledger ]] || {
    echo "missing legacy-equivalent stub ledger: $legacy_stub_ledger" >&2
    return 1
  }
  legacy_relative=${legacy#"$repo_root/"}
  [[ $legacy_relative != "$legacy" ]] || {
    echo "frozen legacy baseline must live below the repository root" >&2
    return 1
  }
  legacy_sha=$(sha256sum -- "$legacy")
  legacy_sha=${legacy_sha%% *}
  expected_frozen_ledger="$legacy_relative@sha256:$legacy_sha"

  IFS= read -r header <"$legacy_stub_ledger" || {
    echo "legacy-equivalent stub ledger is empty" >&2
    return 1
  }
  [[ $header == $'method\tpath\trust_source\tlegacy_handler\tfrozen_ledger\tbehavior_test\trationale' ]] || {
    echo "legacy-equivalent stub ledger has an invalid header" >&2
    return 1
  }
  while IFS=$'\t' read -r method path rust_source handler frozen_ledger behavior_test rationale extra; do
    line_number=$((line_number + 1))
    [[ -z ${extra:-} && -n $method && -n $path && -n $rust_source && -n $handler && -n $frozen_ledger && -n $behavior_test && -n $rationale ]] || {
      echo "legacy-equivalent stub ledger line $line_number must contain seven non-empty tab-separated fields" >&2
      return 1
    }
    [[ $method =~ ^(GET|POST|PUT|DELETE|PATCH|HEAD|OPTIONS|TRACE|CONNECT)$ && $path == /* ]] || {
      echo "legacy-equivalent stub ledger line $line_number has an invalid method or path" >&2
      return 1
    }
    [[ $handler == "$expected_handler" && $frozen_ledger == "$expected_frozen_ledger" ]] || {
      echo "legacy-equivalent stub ledger line $line_number has unpinned or non-RelayNotImplemented Go evidence" >&2
      return 1
    }
    [[ $rust_source == apps/api-rust/*.rs && -f "$repo_root/$rust_source" && $behavior_test == apps/api-rust/*.rs && -f "$repo_root/$behavior_test" ]] || {
      echo "legacy-equivalent stub ledger line $line_number names a missing or non-Rust source/test file" >&2
      return 1
    }
    key="$method"$'\t'"$path"
    [[ -z ${ledger_sources[$key]+x} ]] || {
      echo "legacy-equivalent stub ledger line $line_number duplicates $method $path" >&2
      return 1
    }
    baseline_count=$(awk -F '\t' -v method="$method" -v path="$path" -v handler="$handler" '$1 == method && $2 == path && $3 == handler { count++ } END { print count + 0 }' "$legacy")
    [[ $baseline_count == 1 ]] || {
      echo "legacy-equivalent stub ledger line $line_number no longer matches the frozen Go route ledger: $method $path" >&2
      return 1
    }
    ledger_sources[$key]=$rust_source
  done < <(tail -n +2 "$legacy_stub_ledger")
  [[ ${#ledger_sources[@]} == "$expected_legacy_stub_count" ]] || {
    echo "legacy-equivalent stub ledger must approve exactly $expected_legacy_stub_count routes, found ${#ledger_sources[@]}" >&2
    return 1
  }

  mapfile -t stub_lines < <(grep '^draft route frozen legacy stub: ' <<<"$coverage_output" || true)
  [[ ${#stub_lines[@]} == "$expected_legacy_stub_count" ]] || {
    echo "completion requires exactly $expected_legacy_stub_count frozen legacy stubs, found ${#stub_lines[@]}" >&2
    return 1
  }
  for line in "${stub_lines[@]}"; do
    if [[ $line =~ ^draft\ route\ frozen\ legacy\ stub:\ ([A-Z]+)\ ([^[:space:]]+)\ \((.+):([0-9]+)\)$ ]]; then
      method=${BASH_REMATCH[1]}
      path=${BASH_REMATCH[2]}
      source_file=${BASH_REMATCH[3]}
      source_line=${BASH_REMATCH[4]}
    else
      echo "completion could not parse frozen legacy stub report: $line" >&2
      return 1
    fi
    key="$method"$'\t'"$path"
    [[ -n ${ledger_sources[$key]+x} ]] || {
      echo "unapproved frozen legacy stub: $method $path ($source_file:$source_line)" >&2
      return 1
    }
    [[ ${ledger_sources[$key]} == "$source_file" ]] || {
      echo "legacy-equivalent stub source drift: $method $path expected ${ledger_sources[$key]}, found $source_file:$source_line" >&2
      return 1
    }
    [[ -z ${ledger_seen[$key]+x} ]] || {
      echo "duplicate frozen legacy stub report: $method $path" >&2
      return 1
    }
    ledger_seen[$key]=1
  done
  for key in "${!ledger_sources[@]}"; do
    [[ -n ${ledger_seen[$key]+x} ]] || {
      echo "legacy-equivalent stub ledger entry is stale: ${key/$'\t'/ }" >&2
      return 1
    }
  done
}

export DRAFT_REPORT_MISSING=${DRAFT_REPORT_MISSING:-1}
# The static scanner intentionally identifies every explicit 501. Completion
# permits only the entries pinned in the audited ledger after the gate proves
# each frozen owner and valid-token Go/Rust TCP status/body differential; no
# generic 501 escape is available here.
coverage_output=$(DRAFT_REQUIRE_COMPLETE=0 DRAFT_OUTSIDE_BASELINE_ALLOWLIST="$outside_allowlist" bash "$checker")
printf '%s\n' "$coverage_output"

summary=$(grep '^draft route coverage: ' <<<"$coverage_output")
[[ $summary == *' missing=0 '* && $summary == *' placeholders=0 '* && $summary == *' outside-baseline=0'* ]] || {
  echo "draft completion gate failed: static coverage has missing, placeholder, or outside-baseline routes" >&2
  exit 1
}
legacy_stub_count=$(sed -n 's/.* legacy-stubs=\([0-9][0-9]*\) .*/\1/p' <<<"$summary")
[[ $legacy_stub_count == "$expected_legacy_stub_count" ]] || {
  echo "draft completion gate failed: expected ${expected_legacy_stub_count} frozen legacy stubs, found ${legacy_stub_count:-unknown}" >&2
  exit 1
}
require_approved_legacy_stubs "$coverage_output" || {
  echo "draft completion gate failed: legacy-equivalent stub approval is incomplete or stale" >&2
  exit 1
}

echo "draft completion gate passed: missing=0 placeholders=0 approved-legacy-stubs=$expected_legacy_stub_count (static legacy-equivalent 501s; no production Rust ownership credit)"
