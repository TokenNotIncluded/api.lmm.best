#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
fixture_dir="$repo_root/apps/api-rust/tests/behavior-oracle/fixtures"
legacy_routes="$repo_root/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv"
expected=(
  api-status
  api-notice
  api-about
  api-home-page-content
  auth-login
  auth-logout
  auth-refresh
  auth-self
  relay-chat-completions
  relay-responses
  v1-models
)
# Current-Go billing/token fixtures use a vector-array contract, not the
# frozen route request/response fixture contract checked above.
declare -A current_go_vectors=(
  [relay-funding]=28
  [relay-price-lifecycle]=32
  [relay-token-counts]=208
  [relay-tool-billing]=11
)
declare -A expected_routes=(
  [api-status]='GET /api/status'
  [api-notice]='GET /api/notice'
  [api-about]='GET /api/about'
  [api-home-page-content]='GET /api/home_page_content'
  [auth-login]='POST /api/user/login'
  [auth-logout]='POST /api/user/auth/logout'
  [auth-refresh]='POST /api/user/auth/refresh'
  [auth-self]='GET /api/user/self'
  [relay-chat-completions]='POST /v1/chat/completions'
  [relay-responses]='POST /v1/responses'
  [v1-models]='GET /v1/models'
)

for name in "${expected[@]}"; do
  fixture="$fixture_dir/$name.json"
  [[ -f "$fixture" ]] || { echo "missing fixture: $fixture" >&2; exit 1; }
  jq -e --arg expected_route "${expected_routes[$name]}" '
    .schema_version == 1 and
    .source.revision == "5418ce6b6d45ed69167b0aad53f2f595e5bc8de9" and
    .source.synthetic == true and
    .route.id == $expected_route and
    ((.request.method + " " + .request.path) == $expected_route) and
    (.route.middleware | type == "array" and length > 0) and
    (.route.side_effects | type == "array") and
    (.request.method | IN("GET", "POST")) and
    (.request.path | startswith("/")) and
    (.request.headers | type == "object") and
    (.request | has("body")) and
    (.response.status | type == "number") and
    (.response.selected_headers | type == "object") and
    (.response.selected_headers | has("content-type") and has("x-oneapi-request-id")) and
    (.response | has("body")) and
    (.response.sse_frames | type == "array") and
    (.effects.database | keys == ["deleted", "inserted", "updated"]) and
    (.effects.valkey | keys == ["added", "changed", "removed"]) and
    (.normalization.rules | type == "array") and
    all(.normalization.rules[];
      (.path | type == "string" and startswith("$.")) and
      (.path | IN(
        "$.response.selected_headers.x-oneapi-request-id",
        "$.response.body.data.start_time",
        "$.response.body.error.message"
      )) and
      (.operation | IN("replace", "regex_replace")) and
      (.replacement | type == "string") and
      (.reason | type == "string" and length > 0)
    )
  ' "$fixture" >/dev/null || { echo "invalid fixture contract: $fixture" >&2; exit 1; }
  route=$(jq -r '.request.method + "\t" + .request.path' "$fixture")
  grep -Fqx -- "$route" <(cut -f1-2 "$legacy_routes") || {
    echo "fixture route is absent from the frozen legacy manifest: $route" >&2
    exit 1
  }
done

for name in "${!current_go_vectors[@]}"; do
  fixture="$fixture_dir/$name.json"
  jq -e --argjson count "${current_go_vectors[$name]}" \
    'type == "array" and length == $count and all(.[]; type == "object")' \
    "$fixture" >/dev/null || { echo "invalid current Go vector fixture: $fixture" >&2; exit 1; }
done

actual=$(find "$fixture_dir" -maxdepth 1 -name '*.json' -printf '%f\n' | sort)
expected_files=$(printf '%s.json\n' "${expected[@]}" "${!current_go_vectors[@]}" | sort)
[[ "$actual" == "$expected_files" ]] || {
  echo "fixture set differs from explicit required route manifest" >&2
  diff -u <(printf '%s\n' "$expected_files") <(printf '%s\n' "$actual") >&2 || true
  exit 1
}

echo "behavior oracle fixtures valid: ${#expected[@]} routes"
