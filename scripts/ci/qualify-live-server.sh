#!/usr/bin/env bash
set -euo pipefail
set +x

backend=${LMM_QUALIFICATION_BACKEND:?LMM_QUALIFICATION_BACKEND is required}
base=${LMM_QUALIFICATION_BASE_URL:?LMM_QUALIFICATION_BASE_URL is required}
mode=${1:-full}
root_user=${LMM_QUALIFICATION_ROOT_USERNAME:-releaseci}
root_password=${LMM_QUALIFICATION_ROOT_PASSWORD:-ReleaseCI-2026-Local-Only}
model=${LMM_QUALIFICATION_MODEL:-gpt-4o-mini}
work=${LMM_QUALIFICATION_WORK_DIR:?LMM_QUALIFICATION_WORK_DIR is required}
mkdir -p "$work"
body_file="$work/http-body.json"

status=
request() {
  local method=$1 path=$2 bearer=${3:-} data=${4:-} timeout=${5:-12}
  local -a args=(--silent --show-error --max-time "$timeout" --output "$body_file" --write-out '%{http_code}' -X "$method" -H 'accept: application/json')
  [[ -z $bearer ]] || args+=(-H "Authorization: Bearer $bearer")
  if [[ $method != GET ]]; then
    args+=(-H 'content-type: application/json' -H "Origin: $base")
  fi
  [[ -z $data ]] || args+=(--data-binary "$data")
  status=$(curl "${args[@]}" "$base$path" || true)
}

json_success() {
  [[ $status =~ ^2[0-9][0-9]$ ]] || {
    echo "$1 returned HTTP $status" >&2
    cat "$body_file" >&2 || true
    return 1
  }
  jq -e '.success == true' "$body_file" >/dev/null || {
    echo "$1 returned success != true" >&2
    cat "$body_file" >&2 || true
    return 1
  }
}

wait_ready() {
  local i
  for i in {1..240}; do
    request GET /api/status '' '' 2
    [[ $status == 200 ]] && return 0
    sleep .25
  done
  echo "server did not become ready at $base" >&2
  cat "$body_file" >&2 || true
  return 1
}

login() {
  request POST '/api/user/login?turnstile=' '' "$(jq -cn --arg u "$root_user" --arg p "$root_password" '{username:$u,password:$p}')"
  json_success 'root login'
  dashboard_token=$(jq -er '.data.access_token | select(type == "string" and length > 20)' "$body_file")
}

wait_ready

if [[ $mode == restart ]]; then
  login
  request GET '/api/channel/?p=1&page_size=100' "$dashboard_token"
  json_success 'channel list after restart'
  jq -e --arg name "release-ci-$backend-ok" '.data.items | any(.name == $name)' "$body_file" >/dev/null || {
    echo 'mock channel did not persist across backend restart' >&2
    exit 1
  }
  request GET /api/user/self "$dashboard_token"
  json_success 'authenticated self after restart'
  echo "[$backend] restart persistence qualification passed"
  exit 0
fi

[[ $mode == full ]] || { echo "unknown mode: $mode" >&2; exit 2; }

printf '{' >"$work/malformed.json"
status=$(curl --silent --show-error --max-time 5 --output "$body_file" --write-out '%{http_code}' \
  -H 'content-type: application/json' --data-binary @"$work/malformed.json" "$base/api/user/login" || true)
[[ $status != 5?? ]] || { echo "malformed login crashed server: HTTP $status" >&2; exit 1; }

# Production TokenAuth deliberately conceals missing/invalid relay credentials
# as a generic 404 instead of revealing whether an API route or token exists.
request POST /v1/chat/completions '' "$(jq -cn --arg m "$model" '{model:$m,messages:[{role:"user",content:"anonymous"}]}')"
[[ $status == 404 ]] || {
  echo "anonymous relay expected concealed 404, got $status" >&2
  cat "$body_file" >&2 || true
  exit 1
}

request GET /api/setup
[[ $status == 200 ]] || { echo "GET /api/setup returned $status" >&2; exit 1; }
if ! jq -e '.data.status == true' "$body_file" >/dev/null; then
  setup_payload=$(jq -cn --arg u "$root_user" --arg p "$root_password" \
    '{username:$u,password:$p,confirmPassword:$p,SelfUseModeEnabled:true,DemoSiteEnabled:false}')
  request POST /api/setup '' "$setup_payload"
  json_success 'initial setup'
fi

login
request GET /api/user/self "$dashboard_token"
json_success 'authenticated self'
jq -e '(.data.role // .data.user.role) == 100' "$body_file" >/dev/null || {
  echo 'authenticated root role mismatch' >&2
  cat "$body_file" >&2
  exit 1
}

create_channel() {
  local suffix=$1 channel_status=$2
  local name="release-ci-$backend-$suffix"
  request GET "/api/channel/search?keyword=$name&p=1&page_size=20" "$dashboard_token"
  if [[ $status == 200 ]] && jq -e --arg name "$name" '.data.items // [] | any(.name == $name)' "$body_file" >/dev/null 2>&1; then
    return 0
  fi
  local payload
  payload=$(jq -cn \
    --arg name "$name" --arg key 'release-ci-upstream-key' --arg base_url "http://127.0.0.1:18080/$suffix" --arg model "$model" \
    --argjson status "$channel_status" \
    '{mode:"single",channel:{name:$name,type:1,key:$key,status:$status,base_url:$base_url,models:$model,group:"default",test_model:$model,weight:1,priority:0,auto_ban:0}}')
  request POST /api/channel/ "$dashboard_token" "$payload"
  json_success "create $suffix channel"
}

create_channel ok 1
create_channel rate 2
create_channel fail 2
create_channel slow 2
create_channel fault 2

request GET '/api/channel/?p=1&page_size=100' "$dashboard_token"
json_success 'list channels'
cp "$body_file" "$work/channels.json"

channel_id() {
  jq -er --arg name "release-ci-$backend-$1" '.data.items[] | select(.name == $name) | .id' "$work/channels.json" | head -1
}

ok_id=$(channel_id ok)
rate_id=$(channel_id rate)
fail_id=$(channel_id fail)
slow_id=$(channel_id slow)
fault_id=$(channel_id fault)

request GET "/api/channel/test/$ok_id?model=$model" "$dashboard_token" '' 8
json_success 'mock OpenAI success channel'

expected_channel_failure() {
  local id=$1 label=$2 timeout=${3:-8}
  request GET "/api/channel/test/$id?model=$model" "$dashboard_token" '' "$timeout"
  [[ $status != 5?? ]] || {
    echo "$label crashed the server with HTTP $status" >&2
    cat "$body_file" >&2 || true
    return 1
  }
  if jq -e '.success == true' "$body_file" >/dev/null 2>&1; then
    echo "$label unexpectedly succeeded" >&2
    cat "$body_file" >&2 || true
    return 1
  fi
}

expected_channel_failure "$rate_id" 'mock upstream 429'
expected_channel_failure "$fail_id" 'mock upstream 500'
start_ns=$(date +%s%N)
expected_channel_failure "$slow_id" 'mock upstream response-header timeout' 7
elapsed_ms=$(( ($(date +%s%N) - start_ns) / 1000000 ))
(( elapsed_ms < 6500 )) || { echo "upstream timeout was not bounded: ${elapsed_ms}ms" >&2; exit 1; }
expected_channel_failure "$fault_id" 'mock upstream connection reset' 7

token_name="release-ci-$backend-token"
request POST /api/token/ "$dashboard_token" "$(jq -cn --arg name "$token_name" '{name:$name,remain_quota:100000000,expired_time:-1,unlimited_quota:true,model_limits_enabled:false,model_limits:"",allow_ips:"",group:"default",auto_groups:[],cross_group_retry:false}')"
if jq -e '.success == true and (.data.key | type == "string")' "$body_file" >/dev/null 2>&1; then
  relay_key=$(jq -r '.data.key' "$body_file")
else
  request GET "/api/token/search?keyword=$token_name&p=1&size=20" "$dashboard_token"
  json_success 'find synthetic relay token'
  token_id=$(jq -er --arg name "$token_name" '.data.items[] | select(.name == $name) | .id' "$body_file" | head -1)
  request POST "/api/token/$token_id/key" "$dashboard_token" '{}'
  json_success 'read synthetic relay token'
  relay_key=$(jq -er '.data.key' "$body_file")
fi
[[ $relay_key == sk-* ]] || relay_key="sk-$relay_key"

request POST /v1/chat/completions "$relay_key" "$(jq -cn --arg m "$model" '{model:$m,messages:[{role:"user",content:"release qualification"}]}')" 8
[[ $status == 200 ]] || { echo "non-streaming relay returned HTTP $status" >&2; cat "$body_file" >&2; exit 1; }
jq -e '.choices[0].message.content == "wiremock-ok"' "$body_file" >/dev/null || {
  echo 'non-streaming relay changed mocked upstream payload' >&2
  cat "$body_file" >&2
  exit 1
}

stream_file="$work/stream.txt"
status=$(curl --silent --show-error --max-time 8 --output "$stream_file" --write-out '%{http_code}' \
  -H "Authorization: Bearer $relay_key" -H 'content-type: application/json' \
  --data-binary "$(jq -cn --arg m "$model" '{model:$m,stream:true,stream_options:{include_usage:true},messages:[{role:"user",content:"stream release qualification"}]}')" \
  "$base/v1/chat/completions" || true)
[[ $status == 200 ]] || { echo "streaming relay returned HTTP $status" >&2; cat "$stream_file" >&2; exit 1; }
grep -Fq 'wiremock-stream' "$stream_file"
grep -Fq '[DONE]' "$stream_file"

parallel_dir="$work/parallel"
mkdir -p "$parallel_dir"
for i in {1..12}; do
  (
    code=$(curl --silent --show-error --max-time 8 --output "$parallel_dir/$i.json" --write-out '%{http_code}' \
      -H "Authorization: Bearer $relay_key" -H 'content-type: application/json' \
      --data-binary "$(jq -cn --arg m "$model" --arg n "$i" '{model:$m,messages:[{role:"user",content:("parallel-"+$n)}]}')" \
      "$base/v1/chat/completions" || true)
    [[ $code == 200 ]] && jq -e '.choices[0].message.content == "wiremock-ok"' "$parallel_dir/$i.json" >/dev/null
  ) &
done
wait

request GET /api/status
[[ $status == 200 ]] || { echo "server unhealthy after qualification matrix: $status" >&2; exit 1; }

echo "[$backend] live server qualification passed"
