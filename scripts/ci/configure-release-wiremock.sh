#!/usr/bin/env bash
set -euo pipefail

wiremock=${WIREMOCK_ADMIN_URL:-http://127.0.0.1:18080}

for _ in {1..120}; do
  if curl --fail --silent --show-error --max-time 2 "$wiremock/__admin/mappings" >/dev/null 2>&1; then
    break
  fi
  sleep .25
done
curl --fail --silent --show-error --max-time 2 "$wiremock/__admin/mappings" >/dev/null
curl --fail --silent --show-error -X DELETE "$wiremock/__admin/mappings" >/dev/null
curl --fail --silent --show-error -X DELETE "$wiremock/__admin/requests" >/dev/null

mapping() {
  curl --fail --silent --show-error \
    -H 'content-type: application/json' \
    -X POST "$wiremock/__admin/mappings" \
    --data-binary @- >/dev/null
}

# Buffered fallback for the production OpenAI-compatible channel. The lower
# priority SSE mapping below wins whenever stream=true is present.
mapping <<'JSON'
{
  "priority": 10,
  "request": {
    "method": "POST",
    "urlPath": "/ok/v1/chat/completions"
  },
  "response": {
    "status": 200,
    "headers": {"Content-Type": "application/json"},
    "jsonBody": {
      "id": "chatcmpl-release-ci",
      "object": "chat.completion",
      "created": 1789430400,
      "model": "gpt-4o-mini",
      "choices": [
        {"index": 0, "message": {"role": "assistant", "content": "wiremock-ok"}, "finish_reason": "stop"}
      ],
      "usage": {"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}
    }
  }
}
JSON

mapping <<'JSON'
{
  "priority": 1,
  "request": {
    "method": "POST",
    "urlPath": "/ok/v1/chat/completions",
    "bodyPatterns": [
      {"matchesJsonPath": "$[?(@.stream == true)]"}
    ]
  },
  "response": {
    "status": 200,
    "headers": {"Content-Type": "text/event-stream", "Cache-Control": "no-cache"},
    "body": "data: {\"id\":\"chatcmpl-release-ci\",\"object\":\"chat.completion.chunk\",\"created\":1789430400,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"wiremock-stream\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chatcmpl-release-ci\",\"object\":\"chat.completion.chunk\",\"created\":1789430400,\"model\":\"gpt-4o-mini\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\ndata: [DONE]\n\n"
  }
}
JSON

mapping <<'JSON'
{
  "request": {"method": "GET", "urlPathPattern": "/[^/]+/v1/models"},
  "response": {
    "status": 200,
    "headers": {"Content-Type": "application/json"},
    "jsonBody": {
      "object": "list",
      "data": [
        {"id": "gpt-4o-mini", "object": "model", "created": 1789430400, "owned_by": "release-ci"}
      ]
    }
  }
}
JSON

mapping <<'JSON'
{
  "request": {"method": "POST", "urlPath": "/rate/v1/chat/completions"},
  "response": {
    "status": 429,
    "headers": {"Content-Type": "application/json", "Retry-After": "1"},
    "jsonBody": {"error": {"message": "wiremock rate limit", "type": "rate_limit_error", "code": "rate_limit_exceeded"}}
  }
}
JSON

mapping <<'JSON'
{
  "request": {"method": "POST", "urlPath": "/fail/v1/chat/completions"},
  "response": {
    "status": 500,
    "headers": {"Content-Type": "application/json"},
    "jsonBody": {"error": {"message": "wiremock upstream failure", "type": "server_error", "code": "upstream_failure"}}
  }
}
JSON

mapping <<'JSON'
{
  "request": {"method": "POST", "urlPath": "/slow/v1/chat/completions"},
  "response": {
    "status": 200,
    "fixedDelayMilliseconds": 3000,
    "headers": {"Content-Type": "application/json"},
    "jsonBody": {
      "id": "chatcmpl-release-ci-slow",
      "object": "chat.completion",
      "created": 1789430400,
      "model": "gpt-4o-mini",
      "choices": [{"index": 0, "message": {"role": "assistant", "content": "too-slow"}, "finish_reason": "stop"}],
      "usage": {"prompt_tokens": 4, "completion_tokens": 2, "total_tokens": 6}
    }
  }
}
JSON

mapping <<'JSON'
{
  "request": {"method": "POST", "urlPath": "/fault/v1/chat/completions"},
  "response": {"fault": "CONNECTION_RESET_BY_PEER"}
}
JSON

# Responses API keeps a second OpenAI protocol surface under the same local
# upstream. The listener tests can expand to this route without another mock.
mapping <<'JSON'
{
  "request": {"method": "POST", "urlPath": "/ok/v1/responses"},
  "response": {
    "status": 200,
    "headers": {"Content-Type": "application/json"},
    "jsonBody": {
      "id": "resp_release_ci",
      "object": "response",
      "created_at": 1789430400,
      "status": "completed",
      "model": "gpt-4o-mini",
      "output": [{"id": "msg_release_ci", "type": "message", "role": "assistant", "status": "completed", "content": [{"type": "output_text", "text": "wiremock-response", "annotations": []}]}],
      "usage": {"input_tokens": 4, "output_tokens": 2, "total_tokens": 6}
    }
  }
}
JSON

printf 'WireMock release qualification mappings loaded: '
curl --fail --silent --show-error "$wiremock/__admin/mappings" | jq '.meta.total'
