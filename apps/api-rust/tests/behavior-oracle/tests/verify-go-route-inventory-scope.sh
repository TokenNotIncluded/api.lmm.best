#!/usr/bin/env bash
set -euo pipefail

# This gate is intentionally registration-only.  It does not start an API
# process, connect to storage, or claim that a registered handler is safe to
# migrate.  The frozen ledger remains immutable evidence; the live inventory
# comes from the authoritative Go router registration.

repo_root="$(git rev-parse --show-toplevel)"
go_root="${repo_root}/apps/api-go"
router_dir="${go_root}/router"
manifest_source="${go_root}/cmd/route-manifest/main.go"
legacy_manifest="${repo_root}/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv"

fail() {
  echo "inventory gate: $*" >&2
  exit 1
}

[[ -d "${go_root}" ]] || fail "missing Go module: ${go_root}"
[[ -f "${manifest_source}" ]] || fail "missing authoritative route manifest command: ${manifest_source}"
[[ -f "${legacy_manifest}" ]] || fail "missing frozen route ledger: ${legacy_manifest}"

# route-manifest is the existing side-effect-free registration command.  Keep
# this guard aligned with router.SetRouter: any dropped registration must stop
# the gate instead of silently shrinking the observed inventory.
for registration in SetApiRouter SetDashboardRouter SetRelayRouter SetVideoRouter; do
  grep -Fq "router.${registration}(engine)" "${manifest_source}" ||
    fail "route-manifest no longer registers ${registration}"
done

mcp_source="${router_dir}/open_source_bounty_mcp_router.go"
[[ -f "${mcp_source}" ]] || fail "missing MCP router registration: ${mcp_source}"
grep -Fq 'router.Group("/mcp")' "${mcp_source}" ||
  fail "MCP router group is no longer rooted at /mcp"

mapfile -t mcp_relative_paths < <(
  sed -nE 's/.*mcpRoute\.Any\("([^"]*)".*/\1/p' "${mcp_source}"
)
[[ "${#mcp_relative_paths[@]}" -gt 0 ]] ||
  fail "MCP router exposes no Any registration"

# Gin's RouterGroup.Any expands exactly these nine methods (including HEAD and
# OPTIONS).  Keep the list explicit so an omitted method cannot look like a
# path-only match.  The source comment is checked as a second guard against a
# future Gin API change that invalidates this expansion.
any_methods=(GET POST PUT PATCH HEAD OPTIONS DELETE CONNECT TRACE)
gin_routergroup="$(cd "${go_root}" && go list -m -f '{{.Dir}}' github.com/gin-gonic/gin)/routergroup.go"
grep -Fq 'GET, POST, PUT, PATCH, HEAD, OPTIONS, CONNECT, TRACE, DELETE' \
  "${gin_routergroup}" 2>/dev/null ||
  grep -Fq 'GET, POST, PUT, PATCH, HEAD, OPTIONS, DELETE, CONNECT, TRACE' \
    "${gin_routergroup}" 2>/dev/null ||
  fail "unable to verify Gin Any method expansion"

runtime_dir="$(mktemp -d "${TMPDIR:-/tmp}/lmm-go-route-inventory.XXXXXX")"
trap 'rm -rf "${runtime_dir}"' EXIT

# route-manifest builds only the route-registration command.  It does not call
# InitResources, listen, or touch DB/Valkey.  Keep stderr visible on failure so
# a build/import problem cannot be mistaken for an empty inventory.
(
  cd "${go_root}"
  GIN_MODE=release go run ./cmd/route-manifest >"${runtime_dir}/manifest.tsv"
)

[[ -s "${runtime_dir}/manifest.tsv" ]] || fail "authoritative route manifest is empty"

while IFS= read -r relative_path; do
  case "${relative_path}" in
  "") mcp_path="/mcp" ;;
  "/") mcp_path="/mcp/" ;;
  *) fail "unexpected MCP Any relative path: ${relative_path}" ;;
  esac
  for method in "${any_methods[@]}"; do
    printf '%s\t%s\t%s\n' "${method}" "${mcp_path}" 'gin.RouterGroup.Any' \
      >>"${runtime_dir}/manifest-with-mcp.tsv"
  done
done < <(printf '%s\n' "${mcp_relative_paths[@]}")
cat "${runtime_dir}/manifest.tsv" >>"${runtime_dir}/manifest-with-mcp.tsv"

# Validate the command output as a route inventory, not as an arbitrary text
# file.  A duplicate identity would make count comparisons unsound.
LC_ALL=C awk -F '\t' '
  NF != 3 || $1 == "" || $2 == "" || $3 == "" {
    print "malformed Go route registration at line " FNR > "/dev/stderr"
    failed = 1
    next
  }
  {
    key = $1 SUBSEP $2
    if (seen[key]++) {
      print "duplicate Go route registration: " $1 " " $2 > "/dev/stderr"
      failed = 1
    }
    count++
  }
  END {
    if (count == 0) {
      print "Go route registration produced no rows" > "/dev/stderr"
      failed = 1
    }
    if (failed) exit 1
    print count + 0
  }
' "${runtime_dir}/manifest-with-mcp.tsv" >"${runtime_dir}/inventory-count"

inventory_count="$(<"${runtime_dir}/inventory-count")"
ledger_count="$(wc -l <"${legacy_manifest}" | tr -d ' ')"
[[ "${ledger_count}" -eq 353 ]] ||
  fail "frozen ledger contains ${ledger_count} rows; expected 353"

cut -f1-2 "${runtime_dir}/manifest-with-mcp.tsv" | LC_ALL=C sort -u >"${runtime_dir}/inventory-identities"
cut -f1-2 "${legacy_manifest}" | LC_ALL=C sort -u >"${runtime_dir}/ledger-identities"
cut -f2 "${runtime_dir}/manifest-with-mcp.tsv" | LC_ALL=C sort -u >"${runtime_dir}/inventory-paths"
cut -f2 "${legacy_manifest}" | LC_ALL=C sort -u >"${runtime_dir}/ledger-paths"

extra_identities="${runtime_dir}/extra-identities"
missing_identities="${runtime_dir}/missing-identities"
extra_paths="${runtime_dir}/extra-paths"
missing_paths="${runtime_dir}/missing-paths"
comm -23 "${runtime_dir}/inventory-identities" "${runtime_dir}/ledger-identities" >"${extra_identities}"
comm -13 "${runtime_dir}/inventory-identities" "${runtime_dir}/ledger-identities" >"${missing_identities}"
comm -23 "${runtime_dir}/inventory-paths" "${runtime_dir}/ledger-paths" >"${extra_paths}"
comm -13 "${runtime_dir}/inventory-paths" "${runtime_dir}/ledger-paths" >"${missing_paths}"

# These routes are intentionally called out because a path-only ledger check
# can hide the new MCP methods and the open-source-bounty registration family.
for required_identity in \
  $'GET\t/mcp' $'HEAD\t/mcp' $'OPTIONS\t/mcp' \
  $'GET\t/mcp/' $'HEAD\t/mcp/' $'OPTIONS\t/mcp/' \
  $'GET\t/api/open-source-bounties' \
  $'GET\t/api/open-source-bounties/mcp-token' \
  $'POST\t/api/open-source-bounties/mcp-token' \
  $'DELETE\t/api/open-source-bounties/mcp-token'; do
  grep -Fqx -- "${required_identity}" "${runtime_dir}/inventory-identities" ||
    fail "authoritative inventory is missing required registration: ${required_identity//$'\t'/ }"
done

# Explicit HEAD/OPTIONS registrations are part of the normal route inventory.
# Any is already expanded above, so this check makes their presence visible in
# the result and fails closed if a source change drops them.
for method in HEAD OPTIONS; do
  count="$(awk -v method="${method}" '$1 == method { count++ } END { print count + 0 }' \
    "${runtime_dir}/manifest-with-mcp.tsv")"
  [[ "${count}" -eq "${#mcp_relative_paths[@]}" ]] ||
    fail "${method} inventory count ${count} does not match MCP Any registrations ${#mcp_relative_paths[@]}"
done

# Gin's production router currently has NoRoute (404) but does not enable
# HandleMethodNotAllowed or register NoMethod.  A future 405 surface changes
# the externally visible method inventory and must be reviewed explicitly.
if rg -n --glob '*.go' --glob '!*_test.go' \
  'HandleMethodNotAllowed[[:space:]]*=[[:space:]]*true|\.NoMethod\(' "${router_dir}" >/dev/null; then
  fail "405 method-not-allowed behavior is exposed; inventory requires explicit review"
fi

echo "Go registration inventory: ${inventory_count} method/path identities"
echo "Frozen route ledger: ${ledger_count} method/path identities"
echo "MCP Any registrations: ${#mcp_relative_paths[@]} paths x ${#any_methods[@]} methods (HEAD/OPTIONS included)"
echo "405 behavior: no production HandleMethodNotAllowed=true or NoMethod registration; Gin fallback remains outside the route inventory"
echo "Inventory derivation: route-manifest (SetApiRouter, SetDashboardRouter, SetRelayRouter, SetVideoRouter) plus source-registered SetOpenSourceBountyMCPRouter"

if [[ -s "${extra_identities}" || -s "${missing_identities}" || -s "${extra_paths}" || -s "${missing_paths}" ]]; then
  echo "inventory exceeds route scope: authoritative Go registration is not exactly the frozen 353-route ledger" >&2
  echo "extra method/path identities: $(wc -l <"${extra_identities}" | tr -d ' ')" >&2
  sed 's/\t/ /' "${extra_identities}" >&2
  echo "missing method/path identities: $(wc -l <"${missing_identities}" | tr -d ' ')" >&2
  sed 's/\t/ /' "${missing_identities}" >&2
  echo "extra paths: $(wc -l <"${extra_paths}" | tr -d ' ')" >&2
  sed 's/^/  /' "${extra_paths}" >&2
  echo "missing paths: $(wc -l <"${missing_paths}" | tr -d ' ')" >&2
  sed 's/^/  /' "${missing_paths}" >&2
  exit 1
fi

echo "inventory matches route scope: ${ledger_count} method/path identities"
