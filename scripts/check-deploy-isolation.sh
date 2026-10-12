#!/usr/bin/env bash
# Verify executable separation using local files only. Never contacts a server.
set -Eeuo pipefail
if [[ $# != 1 ]]; then
  echo 'Usage: check-deploy-isolation.sh BACKEND' >&2
  exit 2
fi
backend=$(realpath -e -- "$1")
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
[[ -f "$backend" && -x "$backend" ]]
work=$(mktemp -d)
trap 'rm -rf -- "$work"' EXIT
(cd "$root/apps/api-go" && go list -deps .) >"$work/dependencies"
if grep -Fxq 'github.com/LIghtJUNction/api.lmm.best/internal/deploycli' "$work/dependencies"; then
  echo 'API executable still links deployment implementation' >&2
  exit 1
fi
for removed in operator deploy production frontend build; do
  code=0
  SQL_DSN='invalid://must-not-open' timeout 10 "$backend" "$removed" help >"$work/stdout" 2>"$work/stderr" || code=$?
  [[ "$code" == 64 && ! -s "$work/stdout" ]]
  grep -Fq 'unknown command' "$work/stderr"
done
printf '%s\n' 'PASS: backend excludes deployment code and rejects removed commands without startup.'
