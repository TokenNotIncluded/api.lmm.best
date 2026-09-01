#!/usr/bin/env bash
# Validates the sole explicit-501 exception which may receive route ownership credit.
# This is deliberately a narrow evidence parser, not a generic 501 allow-list.
set -euo pipefail

usage() {
  echo "usage: $0 <evidence.json>" >&2
  exit 2
}

[[ $# -eq 1 ]] || usage
evidence=$1
[[ -f $evidence ]] || { echo "missing frozen DELETE 501 evidence: $evidence" >&2; exit 1; }
command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

expected_handler='github.com/QuantumNous/new-api/controller.RelayNotImplemented'
expected_path='/v1/models/:model'
expected_router_path='/v1/models/{model}'

check=$(jq -r --arg handler "$expected_handler" --arg path "$expected_path" --arg router_path "$expected_router_path" '
  def loopback_listener:
    type == "string" and test("^http://(127\\.0\\.0\\.1|localhost|\\[::1\\]):[0-9]+$");
  def response:
    (.status == 501)
    and (.content_type == "application/json")
    and (.body_sha256 | type == "string" and test("^[0-9a-f]{64}$"));

  if .test != "frozen-model-delete-501-tcp-differential" then "wrong test identity"
  elif .approval_credit != true then "approval credit was not explicitly granted"
  elif .transport != "tcp" then "evidence is not a TCP differential"
  elif .auth_case != "valid-token" then "evidence must use a valid-token fixture"
  elif .route != {
    method:"DELETE",
    frozen_path:$path,
    normalized_rust_path:$router_path,
    legacy_handler:$handler
  } then "route or exact Go owner does not match the frozen exception"
  elif (.go_listener | loopback_listener | not) or (.rust_listener | loopback_listener | not) then "listeners must be isolated loopback endpoints"
  elif .go_listener == .rust_listener then "Go and Rust listeners must differ"
  elif (.go | response | not) or (.rust | response | not) then "both responses must be JSON 501 with body SHA-256"
  elif .status_body_parity != true then "status/body parity is not asserted"
  elif .go.status != .rust.status or .go.content_type != .rust.content_type or .go.body_sha256 != .rust.body_sha256 then "Go/Rust status, content type, or body hash differs"
  else "ok"
  end
' "$evidence")

[[ $check == ok ]] || { echo "invalid frozen DELETE 501 evidence: $check" >&2; exit 1; }
echo "frozen DELETE /v1/models/:model valid-token TCP 501 evidence is valid"
