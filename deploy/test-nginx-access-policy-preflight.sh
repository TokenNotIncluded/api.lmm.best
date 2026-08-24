#!/usr/bin/env bash
set -Eeuo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
locations_source=$repo/deploy/nginx/lmm-api-locations.conf
region_policy_source=$repo/deploy/nginx/lmm-api-region-policy.conf
mime_source=$repo/deploy/nginx/mime.types

fail() { printf 'nginx-preflight-test: %s\n' "$*" >&2; exit 1; }
for command in curl nginx python3 sed; do
  command -v "$command" >/dev/null 2>&1 || fail "required command is unavailable: $command"
done
for source in "$locations_source" "$region_policy_source" "$mime_source"; do
  [[ -f $source && ! -L $source ]] || fail "tracked nginx asset is missing or unsafe: $source"
done

work=$(mktemp -d)
backend_pid=
nginx_pid=
cleanup() {
  local status=$?
  trap - EXIT INT TERM
  if [[ -n $nginx_pid ]] && kill -0 "$nginx_pid" 2>/dev/null; then
    kill "$nginx_pid" 2>/dev/null || true
    wait "$nginx_pid" 2>/dev/null || true
  fi
  if [[ -n $backend_pid ]] && kill -0 "$backend_pid" 2>/dev/null; then
    kill "$backend_pid" 2>/dev/null || true
    wait "$backend_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work"
  exit "$status"
}
trap cleanup EXIT INT TERM

upstream_port_file=$work/upstream.port
upstream_log=$work/upstream.log
: >"$upstream_log"
python3 - "$upstream_port_file" "$upstream_log" <<'PY' &
import http.server
import pathlib
import sys

port_file = pathlib.Path(sys.argv[1])
request_log = pathlib.Path(sys.argv[2])


class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"

    def log_message(self, _format, *_args):
        return

    def record(self):
        with request_log.open("a", encoding="utf-8") as stream:
            stream.write(
                f"{self.command}\t{self.path}\t"
                f"{self.headers.get('X-LMM-Internal-Error', '')}\t"
                f"{self.headers.get('X-LMM-Original-URI', '')}\n"
            )

    def respond(self):
        self.record()
        if self.path == "/internal/access-ip-policy":
            self.send_response(403)
            self.send_header("X-LMM-Access-Policy", "denied")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        if self.path == "/internal/errors/access-policy":
            body = b'{"error":{"code":"IP_ACCESS_ROUTE_REJECTED"}}\n'
            self.send_response(451)
            self.send_header("Content-Type", "application/json")
            self.send_header("Access-Control-Allow-Origin", "*")
            self.send_header("Content-Length", str(len(body)))
            self.end_headers()
            self.wfile.write(body)
            return
        body = b'{"unexpected":"backend reached"}\n'
        self.send_response(500)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    do_GET = respond
    do_POST = respond
    do_OPTIONS = respond


server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
port_file.write_text(str(server.server_address[1]), encoding="ascii")
server.serve_forever()
PY
backend_pid=$!

for _ in $(seq 1 100); do
  [[ -s $upstream_port_file ]] && break
  kill -0 "$backend_pid" 2>/dev/null || fail 'mock policy backend exited before publishing its port'
  sleep 0.05
done
[[ -s $upstream_port_file ]] || fail 'mock policy backend did not become ready'
upstream_port=$(<"$upstream_port_file")
[[ $upstream_port =~ ^[0-9]+$ ]] || fail 'mock policy backend published an invalid port'

sed "s#127\\.0\\.0\\.1:3000#127.0.0.1:$upstream_port#g" \
  "$region_policy_source" >"$work/region-policy.conf"
sed "s#/etc/nginx/lmm-api-mime.types#$mime_source#g" \
  "$locations_source" >"$work/locations.conf"

socket=$work/nginx.sock
mkdir -p "$work/client_temp" "$work/proxy_temp" "$work/fastcgi_temp" \
  "$work/uwsgi_temp" "$work/scgi_temp"
cat >"$work/nginx.conf" <<EOF
worker_processes 1;
pid $work/nginx.pid;
error_log $work/error.log notice;
events { worker_connections 128; }
http {
    access_log $work/access.log;
    client_body_temp_path $work/client_temp;
    proxy_temp_path $work/proxy_temp;
    fastcgi_temp_path $work/fastcgi_temp;
    uwsgi_temp_path $work/uwsgi_temp;
    scgi_temp_path $work/scgi_temp;
    map \$http_upgrade \$connection_upgrade {
        default upgrade;
        '' close;
    }
    server {
        listen unix:$socket;
        server_name localhost;
        set \$lmm_geoip_country_code US;
        set \$lmm_cn_source 0;
        include $work/region-policy.conf;
        include $work/locations.conf;
    }
}
EOF
nginx -p "$work/" -c "$work/nginx.conf" -t >/dev/null
nginx -p "$work/" -c "$work/nginx.conf" -g 'daemon off;' &
nginx_pid=$!
for _ in $(seq 1 100); do
  [[ -S $socket ]] && break
  kill -0 "$nginx_pid" 2>/dev/null || {
    cat "$work/error.log" >&2 || true
    fail 'nginx exited before its test socket became ready'
  }
  sleep 0.05
done
[[ -S $socket ]] || fail 'nginx test socket did not become ready'

assert_header() {
  local headers=$1 expected=$2
  grep -Eiq -- "$expected" "$headers" || {
    cat "$headers" >&2
    fail "response is missing header pattern: $expected"
  }
}

preflight() {
  local path=$1 headers=$work/headers body=$work/body status
  : >"$headers"
  : >"$body"
  status=$(curl --silent --show-error --unix-socket "$socket" \
    --request OPTIONS --output "$body" --dump-header "$headers" \
    --header 'Origin: https://browser.example' \
    --header 'Access-Control-Request-Method: POST' \
    --header 'Access-Control-Request-Headers: authorization, content-type' \
    --write-out '%{http_code}' "http://localhost$path")
  [[ $status == 204 ]] || fail "OPTIONS $path returned HTTP $status instead of 204"
  tr -d '\r' <"$headers" >"$headers.normalized"
  assert_header "$headers.normalized" '^Access-Control-Allow-Origin: \*$'
  assert_header "$headers.normalized" '^Access-Control-Allow-Methods: POST$'
  assert_header "$headers.normalized" '^Access-Control-Allow-Headers: authorization, content-type$'
  assert_header "$headers.normalized" '^Vary: Origin, Access-Control-Request-Method, Access-Control-Request-Headers$'
  if grep -Eiq '^Access-Control-Allow-Credentials:' "$headers.normalized"; then
    fail "OPTIONS $path unexpectedly permits credentialed cross-origin requests"
  fi
  [[ ! -s $body ]] || fail "OPTIONS $path returned a non-empty body"
}

api_paths=(
  /api/status
  /api
  /mcp
  /mcp/tools
  /v1
  /v1/chat/completions
  /v1beta
  /v1beta/models
  /pg/task
  /mj/task
  /suno/task
  /kling/v1/task
  /jimeng
  /jimeng/task
  /dashboard/billing/subscription
  /dashboard/billing/usage
  /tenant/mj/task
)
for path in "${api_paths[@]}"; do
  preflight "$path"
done
[[ ! -s $upstream_log ]] || {
  cat "$upstream_log" >&2
  fail 'an API preflight reached the access-policy or application upstream'
}

# A non-OPTIONS API request must still run the inherited access policy and
# surface its structured denial rather than reaching the application route.
# The duplicate slash also proves that the normalized location-matching URI,
# rather than raw $request_uri, is preserved across the error-page redirect.
status=$(curl --silent --show-error --path-as-is --unix-socket "$socket" \
  --request GET --output "$work/get.body" --dump-header "$work/get.headers" \
  --header 'Origin: https://browser.example' --header 'Accept: application/json' \
  --write-out '%{http_code}' 'http://localhost//v1/models')
[[ $status == 451 ]] || fail "GET //v1/models returned HTTP $status instead of the policy's 451"
grep -Fq 'IP_ACCESS_ROUTE_REJECTED' "$work/get.body" || fail 'GET policy denial lost its JSON body'
grep -Fq $'GET\t/internal/access-ip-policy\t' "$upstream_log" || fail 'GET bypassed the access-policy subrequest'
grep -Fq $'GET\t/internal/errors/access-policy\taccess-policy\t/v1/models' "$upstream_log" || {
  cat "$upstream_log" >&2
  fail 'GET denial did not preserve the normalized trusted policy-error URI'
}
if grep -Eq $'GET\t/+v1/models\t' "$upstream_log"; then
  fail 'denied GET reached the application upstream'
fi

# Frontend routes and lookalike prefixes are not API preflights and therefore
# must not gain the OPTIONS exception.
for path in /oauth/mj /static/mj/asset.js /apiary; do
  status=$(curl --silent --show-error --unix-socket "$socket" \
    --request OPTIONS --output /dev/null \
    --header 'Origin: https://browser.example' \
    --header 'Access-Control-Request-Method: POST' \
    --write-out '%{http_code}' "http://localhost$path")
  [[ $status == 451 ]] || fail "non-API OPTIONS $path unexpectedly returned HTTP $status"
done

printf 'nginx access-policy preflight tests passed\n'
