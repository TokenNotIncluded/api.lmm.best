#!/usr/bin/env bash

production_promote_with_transport_retry() {
  if (( $# < 2 )); then
    printf 'production promote retry requires a stderr log and command\n' >&2
    return 2
  fi

  local stderr_log=$1
  shift
  local status=0
  local marker='production activation became transport-ambiguous and reconciliation failed'
  local delay=${PRODUCTION_PROMOTE_RETRY_DELAY_SECONDS:-5}

  if [[ ! "$delay" =~ ^[0-9]+$ ]]; then
    printf 'PRODUCTION_PROMOTE_RETRY_DELAY_SECONDS must be a non-negative integer\n' >&2
    return 2
  fi

  : >"$stderr_log"
  if "$@" 2>"$stderr_log"; then
    cat "$stderr_log" >&2
    return 0
  else
    status=$?
  fi

  cat "$stderr_log" >&2
  if ! grep -Fq -- "$marker" "$stderr_log"; then
    return "$status"
  fi

  printf 'production promote reconciliation was transport-ambiguous; retrying the persisted controller state once\n' >&2
  sleep "$delay"
  "$@"
}
