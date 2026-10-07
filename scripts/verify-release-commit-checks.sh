#!/usr/bin/env bash
set -Eeuo pipefail

ROOT=$(git rev-parse --show-toplevel)
readonly ROOT
readonly REPOSITORY=${GITHUB_REPOSITORY:-TokenNotIncluded/api.lmm.best}
readonly API_ROOT=${GITHUB_API_URL:-https://api.github.com}
readonly MAX_ATTEMPTS=${LMM_CHECK_MAX_ATTEMPTS:-30}
readonly WAIT_SECONDS=${LMM_CHECK_WAIT_SECONDS:-20}

fail() {
  printf 'verify-release-commit-checks: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 1 || $# -eq 3 ]] || fail 'usage: verify-release-commit-checks.sh COMMIT_SHA [--component go|web|rust]'
[[ $1 =~ ^[0-9a-f]{40}$ ]] || fail 'commit must be a full lowercase SHA'
readonly REVISION=$1
component=full
if [[ $# -eq 3 ]]; then
  [[ $2 == --component ]] || fail 'expected --component'
  case "$3" in go|web|rust) component=$3 ;; *) fail 'unsupported release component' ;; esac
fi
readonly COMPONENT=$component
if [[ $COMPONENT == go || $COMPONENT == web ]]; then
  readonly REQUIRED_FILE="$ROOT/.github/required-go-web-release-checks.txt"
else
  readonly REQUIRED_FILE="$ROOT/.github/required-release-checks.txt"
fi
[[ -f $REQUIRED_FILE ]] || fail 'required check inventory is missing'

required_names=()
required_workflows=()
required_events=()
required_branches=()
while IFS='|' read -r name workflow event branch extra; do
  [[ -n $name ]] || continue
  [[ -n $workflow && -n $event && -n $branch && -z $extra ]] ||
    fail "invalid required check inventory entry: $name"
  required_names+=("$name")
  required_workflows+=("$workflow")
  required_events+=("$event")
  required_branches+=("$branch")
done <"$REQUIRED_FILE"
[[ ${#required_names[@]} -gt 0 ]] || fail 'required check inventory is empty'
for command in curl jq; do command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"; done

api_get() {
  local url=$1
  curl --fail --location --silent --show-error \
    --retry 4 --retry-all-errors --retry-delay 2 --connect-timeout 15 --max-time 60 \
    --header "Authorization: Bearer $GITHUB_TOKEN" \
    --header 'Accept: application/vnd.github+json' \
    "$url"
}

fetch_checks() {
  if [[ -n ${LMM_CHECK_RUNS_FILE:-} ]]; then
    cat -- "$LMM_CHECK_RUNS_FILE"
    return
  fi
  api_get "$API_ROOT/repos/$REPOSITORY/commits/$REVISION/check-runs?per_page=100"
}

fetch_workflow_runs() {
  if [[ -n ${LMM_WORKFLOW_RUNS_FILE:-} ]]; then
    cat -- "$LMM_WORKFLOW_RUNS_FILE"
    return
  fi
  api_get "$API_ROOT/repos/$REPOSITORY/actions/runs?head_sha=$REVISION&per_page=100"
}

if [[ -z ${LMM_CHECK_RUNS_FILE:-} || -z ${LMM_WORKFLOW_RUNS_FILE:-} ]]; then
  [[ -n ${GITHUB_TOKEN:-} ]] || fail 'GITHUB_TOKEN is required for live check verification'
fi

for ((attempt = 1; attempt <= MAX_ATTEMPTS; attempt++)); do
  checks=$(fetch_checks) || fail 'could not read commit check runs'
  workflow_runs=$(fetch_workflow_runs) || fail 'could not read workflow runs'
  invalid=()
  pending=()

  for index in "${!required_names[@]}"; do
    name=${required_names[$index]}
    workflow=${required_workflows[$index]}
    event=${required_events[$index]}
    branch=${required_branches[$index]}

    workflow_run=$(jq -c \
      --arg workflow "$workflow" \
      --arg event "$event" \
      --arg branch "$branch" \
      --arg revision "$REVISION" '
        [.workflow_runs[] |
          select(
            .path == $workflow and
            .event == $event and
            .head_branch == $branch and
            .head_sha == $revision
          )] |
        if length == 0 then null
        elif any(.status == "completed") then
          [.[] | select(.status == "completed")] |
          max_by([(.completed_at // .updated_at // .created_at // ""), .id])
        else
          max_by([(.updated_at // .created_at // ""), .id])
        end
      ' <<<"$workflow_runs")
    if [[ $workflow_run == null ]]; then
      pending+=("$name ($workflow $event/$branch missing)")
      continue
    fi

    workflow_status=$(jq -r '.status' <<<"$workflow_run")
    workflow_conclusion=$(jq -r '.conclusion // ""' <<<"$workflow_run")
    allow_component_parent=false
    if [[ $COMPONENT == go || $COMPONENT == web ]]; then
      case "$workflow" in
        .github/workflows/ci.yml|dynamic/github-code-scanning/codeql)
          allow_component_parent=true ;;
      esac
    fi
    if [[ $workflow_status != completed ]]; then
      if [[ $allow_component_parent != true || $workflow_status != in_progress || -n $workflow_conclusion ]]; then
        pending+=("$name ($workflow $workflow_status)")
        continue
      fi
    elif [[ $workflow_conclusion != success ]]; then
      if [[ $allow_component_parent != true || $workflow_conclusion != failure ]]; then
        invalid+=("$name ($workflow $workflow_conclusion)")
        continue
      fi
    fi

    workflow_run_id=$(jq -r '.id' <<<"$workflow_run")
    record=$(jq -c --arg name "$name" --arg revision "$REVISION" \
      --arg parent_status "$workflow_status" --argjson run_id "$workflow_run_id" '
      [.check_runs[] |
        select(
          .name == $name and
          .head_sha == $revision and
          .app.slug == "github-actions" and
          ((.details_url // "") | contains("/actions/runs/" + ($run_id | tostring) + "/"))
        )] |
      if length == 0 then null
      # A running parent can be rerunning a formerly successful job. Its newest
      # check-run ID must win even when a queued job has no started_at yet.
      elif $parent_status == "in_progress" then max_by(.id)
      elif any(.status == "completed") then
        [.[] | select(.status == "completed")] |
        max_by([(.completed_at // .started_at // ""), .id])
      else
        max_by([(.started_at // ""), .id])
      end
    ' <<<"$checks")
    if [[ $record == null ]]; then
      pending+=("$name (selected workflow run $workflow_run_id missing)")
      continue
    fi

    status=$(jq -r '.status' <<<"$record")
    conclusion=$(jq -r '.conclusion // ""' <<<"$record")
    if [[ $status != completed ]]; then
      pending+=("$name ($status)")
    elif [[ $conclusion != success ]]; then
      invalid+=("$name ($conclusion)")
    fi
  done

  if [[ ${#invalid[@]} -gt 0 ]]; then
    printf 'verify-release-commit-checks: failed required checks for %s:\n' "$REVISION" >&2
    printf '  - %s\n' "${invalid[@]}" >&2
    exit 1
  fi
  if [[ ${#pending[@]} -eq 0 ]]; then
    if [[ $COMPONENT == go || $COMPONENT == web ]]; then
      printf 'all required %s component CI, CodeQL, and release qualification checks passed for %s\n' "$COMPONENT" "$REVISION"
    else
      printf 'all required main-push CI, CodeQL, and release artifact checks passed for %s\n' "$REVISION"
    fi
    exit 0
  fi
  if ((attempt == MAX_ATTEMPTS)); then
    printf 'verify-release-commit-checks: required checks did not complete for %s:\n' "$REVISION" >&2
    printf '  - %s\n' "${pending[@]}" >&2
    exit 1
  fi
  sleep "$WAIT_SECONDS"
done
