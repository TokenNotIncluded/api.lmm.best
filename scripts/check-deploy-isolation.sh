#!/usr/bin/env bash
# Verify executable separation using local files only. Never contacts a server.
set -Eeuo pipefail
if [[ $# != 2 ]]; then
  echo 'Usage: check-deploy-isolation.sh BACKEND DEPLOY_ENGINE' >&2
  exit 2
fi
backend=$(realpath -e -- "$1")
engine=$(realpath -e -- "$2")
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
[[ -f "$backend" && -x "$backend" && -f "$engine" && -x "$engine" && "$backend" != "$engine" ]]
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
"$engine" capabilities | python3 -c 'import json,sys; c=json.load(sys.stdin); assert c == {"format":1,"workspace_create":"operator"} and type(c["format"]) is int'
expected=$(sha256sum "$root/contracts/api-route/VERSION" | cut -d' ' -f1)
[[ $(cd "$root" && "$engine" contract route print) == "$expected" ]]
for release in first second; do
  mkdir -p "$work/$release/static/js"
  printf '<script src="/static/js/%s.js"></script>\n' "$release" >"$work/$release/index.html"
  printf 'window.fixture="%s";\n' "$release" >"$work/$release/static/js/$release.js"
  "$engine" frontend publish --root "$work/public" --source "$work/$release" --release "$release" --keep 2
  cmp "$work/$release/index.html" "$work/public/current/index.html"
done
"$engine" frontend rollback --root "$work/public" --release first --keep 2
cmp "$work/first/index.html" "$work/public/current/index.html"
[[ -f "$work/public/assets/js/second.js" ]]
printf '%s\n' 'PASS: backend excludes deployment code, removed commands fail without startup, separate tool publishes and rolls back local frontend files.'
