#!/usr/bin/env bash
# Keep every workflow rerun in its own auditable workspace. Never recycle or
# remove a prior transaction's directory to make a retry appear successful.
production_deployment_id() {
  local tag=${1:-} run_id=${2:-} attempt=${3:-}
  if [[ ! $tag =~ ^(go|web)-v[0-9]+\.[0-9]+\.[0-9]+$ ||
        ! $run_id =~ ^[1-9][0-9]*$ || ! $attempt =~ ^[1-9][0-9]*$ ]]; then
    printf 'invalid release tag, workflow run ID, or attempt\n' >&2
    return 2
  fi
  printf 'release-%s-%s-attempt-%s\n' "$tag" "$run_id" "$attempt"
}
