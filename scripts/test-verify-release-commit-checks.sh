#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(git rev-parse --show-toplevel)
SCRIPT="$ROOT/scripts/verify-release-commit-checks.sh"
REQUIRED="$ROOT/.github/required-release-checks.txt"
REVISION=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
readonly ROOT SCRIPT REQUIRED REVISION

for workflow in release-go release-web; do
  # Both component publishers must invoke the same immutable-commit gate.
  # shellcheck disable=SC2016
  grep -Fq 'run: bash scripts/verify-release-commit-checks.sh "${GITHUB_SHA}"' \
    "$ROOT/.github/workflows/$workflow.yml" || {
    printf '%s does not enforce the required commit checks\n' "$workflow" >&2
    exit 1
  }
done

tmp=$(mktemp -d)
cleanup() { rm -rf -- "$tmp"; }
trap cleanup EXIT

write_success_fixtures() {
  local checks='[]' runs='[]' id=100 run_id=1000
  local name workflow event branch extra key
  declare -A workflow_run_ids=()

  while IFS='|' read -r name workflow event branch extra; do
    [[ -n $name ]] || continue
    [[ -n $workflow && -n $event && -n $branch && -z $extra ]]
    key="$workflow|$event|$branch"
    if [[ -z ${workflow_run_ids[$key]:-} ]]; then
      workflow_run_ids[$key]=$run_id
      runs=$(jq -c \
        --argjson id "$run_id" \
        --arg path "$workflow" \
        --arg event "$event" \
        --arg branch "$branch" \
        --arg revision "$REVISION" \
        '. + [{
          id: $id,
          path: $path,
          event: $event,
          head_branch: $branch,
          head_sha: $revision,
          status: "completed",
          conclusion: "success",
          created_at: "2026-08-24T00:00:00Z",
          updated_at: "2026-08-24T00:01:00Z",
          completed_at: "2026-08-24T00:01:00Z"
        }]' <<<"$runs")
      ((run_id += 1))
    fi
    selected_run_id=${workflow_run_ids[$key]}
    checks=$(jq -c \
      --argjson id "$id" \
      --argjson run_id "$selected_run_id" \
      --arg name "$name" \
      '. + [{
        id: $id,
        name: $name,
        status: "completed",
        conclusion: "success",
        started_at: "2026-08-24T00:00:00Z",
        completed_at: "2026-08-24T00:01:00Z",
        details_url: ("https://github.example/actions/runs/" + ($run_id | tostring) + "/job/" + ($id | tostring)),
        app: {slug: "github-actions"}
      }]' <<<"$checks")
    ((id += 1))
  done <"$REQUIRED"

  jq -cn --argjson check_runs "$checks" '{check_runs:$check_runs}' >"$tmp/checks.json"
  jq -cn --argjson workflow_runs "$runs" '{workflow_runs:$workflow_runs}' >"$tmp/runs.json"
}

verify_fixture() {
  local checks_file=${1:-$tmp/checks.json} runs_file=${2:-$tmp/runs.json}
  LMM_CHECK_RUNS_FILE="$checks_file" \
    LMM_WORKFLOW_RUNS_FILE="$runs_file" \
    LMM_CHECK_MAX_ATTEMPTS=1 \
    bash "$SCRIPT" "$REVISION"
}

expect_rejected() {
  local label=$1 expected=$2 checks_file=${3:-$tmp/checks.json} runs_file=${4:-$tmp/runs.json}
  if verify_fixture "$checks_file" "$runs_file" >"$tmp/rejected.out" 2>&1; then
    printf 'negative governance fixture unexpectedly accepted %s\n' "$label" >&2
    exit 1
  fi
  grep -Fq "$expected" "$tmp/rejected.out" || {
    printf 'negative governance fixture for %s did not report %s\n' "$label" "$expected" >&2
    cat "$tmp/rejected.out" >&2
    exit 1
  }
}

write_success_fixtures
verify_fixture >/dev/null
ci_run_id=$(jq -r '.workflow_runs[] | select(.path == ".github/workflows/ci.yml" and .event == "push" and .head_branch == "main") | .id' "$tmp/runs.json")
[[ $ci_run_id =~ ^[0-9]+$ ]]

for conclusion in failure cancelled timed_out; do
  write_success_fixtures
  jq \
    --argjson run_id "$ci_run_id" \
    --arg conclusion "$conclusion" \
    '.check_runs += [{
      id: 9999,
      name: "Release artifact contract",
      status: "completed",
      conclusion: $conclusion,
      started_at: "2026-08-24T00:02:00Z",
      completed_at: "2026-08-24T00:03:00Z",
      details_url: ("https://github.example/actions/runs/" + ($run_id | tostring) + "/job/9999"),
      app: {slug: "github-actions"}
    }]' "$tmp/checks.json" >"$tmp/new-$conclusion-check.json"
  expect_rejected "newer completed $conclusion check" "Release artifact contract ($conclusion)" \
    "$tmp/new-$conclusion-check.json" "$tmp/runs.json"
done

for conclusion in failure cancelled timed_out; do
  write_success_fixtures
  jq \
    --arg conclusion "$conclusion" \
    --arg revision "$REVISION" \
    '.workflow_runs += [{
      id: 9000,
      path: ".github/workflows/ci.yml",
      event: "push",
      head_branch: "main",
      head_sha: $revision,
      status: "completed",
      conclusion: $conclusion,
      created_at: "2026-08-24T00:04:00Z",
      updated_at: "2026-08-24T00:05:00Z",
      completed_at: "2026-08-24T00:05:00Z"
    }]' "$tmp/runs.json" >"$tmp/new-$conclusion-run.json"
  expect_rejected "newer completed $conclusion workflow" ".github/workflows/ci.yml $conclusion" \
    "$tmp/checks.json" "$tmp/new-$conclusion-run.json"
done

write_success_fixtures
jq --arg revision "$REVISION" '.workflow_runs += [
  {
    id: 9100,
    path: ".github/workflows/ci.yml",
    event: "pull_request",
    head_branch: "feature/review",
    head_sha: $revision,
    status: "completed",
    conclusion: "failure",
    completed_at: "2026-08-24T00:10:00Z"
  },
  {
    id: 9200,
    path: ".github/workflows/ci.yml",
    event: "push",
    head_branch: "go-v0.1.59",
    head_sha: $revision,
    status: "completed",
    conclusion: "failure",
    completed_at: "2026-08-24T00:11:00Z"
  },
  {
    id: 9300,
    path: ".github/workflows/ci.yml",
    event: "workflow_dispatch",
    head_branch: "main",
    head_sha: $revision,
    status: "completed",
    conclusion: "failure",
    completed_at: "2026-08-24T00:12:00Z"
  },
  {
    id: 9400,
    path: ".github/workflows/ci.yml",
    event: "push",
    head_branch: "main",
    head_sha: $revision,
    status: "in_progress",
    conclusion: null,
    updated_at: "2026-08-24T00:13:00Z"
  }
]' "$tmp/runs.json" >"$tmp/unrelated-runs.json"
verify_fixture "$tmp/checks.json" "$tmp/unrelated-runs.json" >/dev/null

write_success_fixtures
jq 'del(.check_runs[] | select(.name == "Release artifact contract"))' \
  "$tmp/checks.json" >"$tmp/missing.json"
expect_rejected 'missing selected-run check' 'Release artifact contract (selected workflow run' \
  "$tmp/missing.json" "$tmp/runs.json"

printf 'release governance workflow/event and latest-completed fixtures verified\n'
