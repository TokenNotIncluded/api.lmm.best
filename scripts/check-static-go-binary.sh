#!/usr/bin/env bash
# Used only for the just-built Go production binary, not arbitrary executables.
set -euo pipefail

if (( $# != 1 )) || [[ ! -f "$1" || ! -x "$1" ]]; then
  echo 'Usage: check-static-go-binary.sh BUILT_EXECUTABLE' >&2
  exit 1
fi

# Linux ldd returns 1 for a normal static Go executable. Capture the status
# separately: with pipefail, "ldd | grep" inside an if can hide ldd failures.
ldd_status=0
ldd_output=$(LC_ALL=C ldd "$1" 2>&1) || ldd_status=$?
reject() {
  printf 'Cannot confirm static Go production binary (ldd exit %s):\n%s\n' \
    "$ldd_status" "$ldd_output" >&2
  exit 1
}
if (( ldd_status > 1 )) || [[ -z "$ldd_output" ]]; then
  reject
fi
static_line_pattern='^[[:space:]]*(not a dynamic executable|statically linked)[[:space:]]*$'
while IFS= read -r line; do
  if [[ ! $line =~ $static_line_pattern ]]; then
    reject
  fi
done <<< "$ldd_output"
printf '%s\n' "$ldd_output"
