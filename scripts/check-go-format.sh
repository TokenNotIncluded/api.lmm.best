#!/usr/bin/env bash
# A gofmt parse failure is not an empty successful formatting result.
set -euo pipefail

if (( $# == 0 )); then
  set -- .
fi
unformatted=$(gofmt -l "$@")
if [[ -n "$unformatted" ]]; then
  printf 'Go files need formatting:\n%s\n' "$unformatted" >&2
  exit 1
fi
