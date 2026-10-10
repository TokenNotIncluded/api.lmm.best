#!/usr/bin/env bash
set -euo pipefail
package_directory="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
profile_directory="$(mktemp -d "${TMPDIR:-/tmp}/lmm-vscode-host.XXXXXX")"
trap 'rm -rf -- "$profile_directory"' EXIT
cd "$package_directory"
npm run compile
"${LMM_VSCODE_BIN:-code}" --user-data-dir "$profile_directory/user" --extensions-dir "$profile_directory/extensions" --extensionDevelopmentPath "$package_directory" --extensionTestsPath "$package_directory/test/host.cjs" --disable-extensions --disable-workspace-trust --skip-welcome --skip-release-notes --no-sandbox --new-window --wait
if ! rg -q 'LMM_REAL_HOST_SMOKE_PASS' "$profile_directory/user/logs"; then
  printf '%s\n' 'LMM real extension host smoke failed; VS Code logs:' >&2
  rg -n 'error|Error|LMM_|lmm-copilot' "$profile_directory/user/logs" >&2 || true
  exit 1
fi
printf '%s\n' 'LMM_REAL_HOST_SMOKE_PASS: actual VS Code extension activation, bundled logo, provider registration, command registration, silent unauthenticated model discovery.'
