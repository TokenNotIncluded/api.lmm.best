#!/usr/bin/env bash
set -Eeuo pipefail

command -v nginx >/dev/null || { printf 'test-nginx-mime: nginx is required\n' >&2; exit 1; }
command -v curl >/dev/null || { printf 'test-nginx-mime: curl is required\n' >&2; exit 1; }

work=$(mktemp -d)
port=${LMM_NGINX_TEST_PORT:-49184}
mime_types=${LMM_NGINX_MIME_TYPES:-/etc/nginx/lmm-api-mime.types}
[[ -r $mime_types ]] || { printf 'test-nginx-mime: unreadable MIME map: %s\n' "$mime_types" >&2; exit 1; }
nginx_pid=
cleanup() {
  if [[ -n $nginx_pid ]] && kill -0 "$nginx_pid" 2>/dev/null; then
    kill "$nginx_pid" 2>/dev/null || true
    wait "$nginx_pid" 2>/dev/null || true
  fi
  rm -rf -- "$work"
}
trap cleanup EXIT

mkdir -p -- "$work/assets" "$work/client_temp" "$work/proxy_temp" \
  "$work/fastcgi_temp" "$work/uwsgi_temp" "$work/scgi_temp"
printf 'export const ready = true\n' >"$work/assets/app.js"
printf 'body { color: black; }\n' >"$work/assets/app.css"
printf '{"ready":true}\n' >"$work/assets/app.json"
printf '<svg xmlns="http://www.w3.org/2000/svg"/>\n' >"$work/assets/app.svg"
printf 'synthetic-font\n' >"$work/assets/app.woff2"
chmod a+rx "$work" "$work/assets"
chmod a+r "$work/assets"/*

cat >"$work/nginx.conf" <<EOF
pid $work/nginx.pid;
error_log $work/error.log notice;
events { worker_connections 16; }
http {
    access_log $work/access.log;
    client_body_temp_path $work/client_temp;
    proxy_temp_path $work/proxy_temp;
    fastcgi_temp_path $work/fastcgi_temp;
    uwsgi_temp_path $work/uwsgi_temp;
    scgi_temp_path $work/scgi_temp;
    # Match production: no global mime.types include.
    default_type application/octet-stream;
    server {
        listen 127.0.0.1:$port;
        include $mime_types;
        default_type application/octet-stream;
        location ^~ /static/ {
            alias $work/assets/;
        }
    }
}
EOF

nginx -t -c "$work/nginx.conf"
nginx -c "$work/nginx.conf" -g 'daemon off;' &
nginx_pid=$!

for _ in {1..50}; do
  curl -fsS "http://127.0.0.1:$port/static/app.js" >/dev/null 2>&1 && break
  sleep 0.02
done

content_type() {
  curl -fsSI "http://127.0.0.1:$port/static/$1" |
    awk -F ': *' 'tolower($1) == "content-type" { sub("\\r$", "", $2); print tolower($2) }'
}

[[ $(content_type app.js) == application/javascript ]]
[[ $(content_type app.css) == text/css ]]
[[ $(content_type app.json) == application/json ]]
[[ $(content_type app.svg) == image/svg+xml ]]
[[ $(content_type app.woff2) == font/woff2 ]]
[[ $(curl -sS -o /dev/null -w '%{http_code}' "http://127.0.0.1:$port/static/missing.hash.js") == 404 ]]

printf 'nginx MIME integration tests passed\n'
