#!/usr/bin/env bash
# This contract test intentionally matches shell/nginx snippets as literal text.
# shellcheck disable=SC2016
set -Eeuo pipefail

repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
config=$repo/deploy/nginx/lmm-api-locations.conf
server_config=$repo/deploy/nginx/new-api.conf
mime_types=$repo/deploy/nginx/mime.types
region_policy=$repo/deploy/nginx/lmm-api-region-policy.conf
release=$repo/deploy/frontend-release.sh
nginx_installer=$repo/deploy/nginx/install-nginx-split.sh
route_manifest=$repo/apps/api-rust/tests/fixtures/routes/legacy-go-routes.tsv

fail() { printf 'split-check: %s\n' "$*" >&2; exit 1; }
assert_literal() { grep -Fq -- "$1" "$2" || fail "$2 is missing: $1"; }

for route in '/api/' '/v1/' '/v1beta/' '/pg/' '/mj/' '/suno/' '/kling/v1/' '/jimeng' '/dashboard/'; do
  assert_literal "$route" "$config"
done
assert_literal '^/[^/]+/mj(?:/|$)' "$config"
assert_literal 'location ^~ /oauth/' "$config"
assert_literal 'include /etc/nginx/lmm-api-mime.types;' "$config"
assert_literal 'default_type application/octet-stream;' "$config"
if grep -Fq 'include /etc/nginx/mime.types;' "$config"; then
  fail 'site config must not depend on or manage the global nginx MIME map'
fi
assert_literal 'application/javascript               js mjs;' "$mime_types"
assert_literal 'text/css                              css;' "$mime_types"
assert_literal 'application/json                     json map;' "$mime_types"
assert_literal 'image/svg+xml                         svg svgz;' "$mime_types"
assert_literal 'font/woff2                            woff2;' "$mime_types"
assert_literal 'location = /terms' "$config"
assert_literal 'location = /privacy' "$config"
assert_literal 'alias /var/www/api.lmm.best/legal/terms.html;' "$config"
assert_literal 'alias /var/www/api.lmm.best/legal/privacy.html;' "$config"
assert_literal 'default_type text/html;' "$config"
assert_literal 'charset utf-8;' "$config"
assert_literal 'add_header X-Content-Type-Options nosniff always;' "$config"
[[ $(grep -Fc 'default_type text/html;' "$config") == 2 ]] ||
  fail 'each exact legal alias must set the HTML content type'
[[ $(grep -Fc 'charset utf-8;' "$config") == 2 ]] ||
  fail 'each exact legal alias must set UTF-8'
[[ $(grep -Fc 'add_header X-Content-Type-Options nosniff always;' "$config") == 2 ]] ||
  fail 'each exact legal alias must set nosniff'
if grep -Eq 'return 30[1278] /(user-agreement|privacy-policy)' "$config"; then
  fail 'legal aliases must not redirect into the SPA'
fi
assert_literal 'alias /srv/lmm-api-frontend/assets/' "$config"
assert_literal 'location = /dashboard/billing/subscription' "$config"
assert_literal 'location = /dashboard/billing/usage' "$config"
assert_literal 'location = /jimeng ' "$config"
assert_literal 'location ^~ /jimeng/' "$config"
assert_literal 'jpe?g|js|json|map|png|svg|webp|woff2?' "$config"
assert_literal 'max-age=31536000, immutable' "$config"
assert_literal 'no-cache, must-revalidate' "$config"
assert_literal 'proxy_buffering off' "$config"
assert_literal 'Upgrade $websocket_upgrade' "$config"
assert_literal 'Connection $connection_upgrade' "$config"
assert_literal 'error_page 418 = @lmm_api_cors_preflight;' "$config"
assert_literal 'location @lmm_api_cors_preflight {' "$config"
assert_literal 'add_header Access-Control-Allow-Origin "*" always;' "$config"
assert_literal 'add_header Access-Control-Allow-Methods $http_access_control_request_method always;' "$config"
assert_literal 'add_header Access-Control-Allow-Headers $http_access_control_request_headers always;' "$config"
assert_literal 'add_header Vary "Origin, Access-Control-Request-Method, Access-Control-Request-Headers" always;' "$config"
backend_route_count=$(grep -Fc 'try_files /.__lmm_backend__ @lmm_api_backend;' "$config")
preflight_guard_count=$(grep -Fc 'if ($request_method = OPTIONS) { return 418; }' "$config")
normalized_uri_capture_count=$(grep -Fc 'set $lmm_access_policy_original_uri $uri;' "$config")
[[ $backend_route_count -gt 0 && $preflight_guard_count == "$backend_route_count" && $normalized_uri_capture_count == "$backend_route_count" ]] ||
  fail "every backend API location must capture its normalized URI and have one OPTIONS guard (routes=$backend_route_count captures=$normalized_uri_capture_count guards=$preflight_guard_count)"
assert_literal 'geoip2 /var/lib/geoip2/DBIP-Country-Lite.mmdb {' "$repo/deploy/nginx/http-map.conf"
assert_literal 'map $lmm_geoip_country_code $lmm_cn_source {' "$repo/deploy/nginx/http-map.conf"

redirect_server=$(awk '
  /^server \{$/ { server_count++; capture = server_count == 1 }
  capture { print }
  capture && /^}$/ { exit }
' "$server_config")
canonical_server=$(awk '
  /^server \{$/ { server_count++; capture = server_count == 2 }
  capture { print }
' "$server_config")
[[ $(grep -Fc 'listen 9000 ssl;' <<<"$redirect_server") == 1 ]] ||
  fail 'the first server must exclusively listen on the legacy :9000 entry point'
[[ $(grep -Fc 'return 308 https://api.lmm.best$request_uri;' <<<"$redirect_server") == 1 ]] ||
  fail 'the :9000 entry point must redirect to the canonical HTTPS origin'
if grep -Fq 'lmm-api-locations.conf' <<<"$redirect_server"; then
  fail 'the :9000 redirect server must not serve application routes'
fi
[[ $(grep -Fc 'listen 443 ssl;' <<<"$canonical_server") == 1 ]] ||
  fail 'the second server must exclusively listen on the canonical HTTPS entry point'
[[ $(grep -Fc 'include /etc/nginx/lmm-api-locations.conf;' <<<"$canonical_server") == 1 ]] ||
  fail 'the canonical HTTPS server must serve application routes'
[[ $(grep -Fc 'include /etc/nginx/lmm-api-region-policy.conf;' <<<"$canonical_server") == 1 ]] ||
  fail 'the canonical HTTPS server must load the package-managed access policy'
if grep -Fq 'listen 9000 ssl;' <<<"$canonical_server"; then
  fail 'the canonical HTTPS server must not also serve the legacy :9000 origin'
fi

assert_literal 'mv -Tf -- "$temp" "$root/current"' "$release"
assert_literal 'flock -n 9' "$release"
assert_literal 'immutable asset collision with different content' "$release"
assert_literal 'preflight_assets "$stage/static"' "$release"
assert_literal 'flock -n 9' "$nginx_installer"
assert_literal 'mv -Tf -- "$temp" "$target"' "$nginx_installer"
assert_literal 'nginx -t' "$nginx_installer"
assert_literal 'systemctl reload nginx' "$nginx_installer"
assert_literal 'restore_backup "$backup"' "$nginx_installer"
assert_literal 'MIME_TARGET=$ROOT_PREFIX/etc/nginx/lmm-api-mime.types' "$nginx_installer"
assert_literal 'REGION_POLICY_TARGET=$ROOT_PREFIX/etc/nginx/lmm-api-region-policy.conf' "$nginx_installer"
assert_literal 'auth_request /internal/access-ip-policy;' "$region_policy"
assert_literal 'auth_request_set $lmm_access_policy_result $upstream_http_x_lmm_access_policy;' "$region_policy"
assert_literal 'X-LMM-CN-Source $lmm_cn_source;' "$region_policy"
assert_literal 'X-LMM-Access-Policy $lmm_access_policy_result;' "$region_policy"
assert_literal 'X-LMM-Internal-Error access-policy;' "$region_policy"
assert_literal 'X-LMM-Original-URI $lmm_access_policy_original_uri;' "$region_policy"
assert_literal 'X-LMM-Original-Accept $http_accept;' "$region_policy"
assert_literal 'proxy_set_header Authorization "";' "$region_policy"
assert_literal 'proxy_set_header Cookie "";' "$region_policy"
assert_literal 'deploy production edge-policy install|verify' "$repo/apps/api-go/internal/appcli/deploy.go"
if grep -Fq 'location ^~ /dashboard/' "$config"; then
  fail 'broad /dashboard/ proxy would swallow frontend dashboard routes'
fi
if grep -Fq 'location ^~ /jimeng {' "$config"; then
  fail 'broad /jimeng prefix would also proxy /jimengx'
fi

# Keep the nginx split synchronized with every route in the immutable legacy
# backend surface. This remains available after the Go source is archived.
while IFS=$'\t' read -r method route handler; do
  [[ -n $method && -n $route && -n $handler ]] ||
    fail "malformed frozen backend route: $method $route $handler"
  case $route in
    /|/api|/api/*|/v1|/v1/*|/v1beta|/v1beta/*|/pg|/pg/*|/mj|/mj/*|/suno|/suno/*|/kling/v1|/kling/v1/*|jimeng|/jimeng|/jimeng/*|/dashboard|/dashboard/*|/:mode/mj|/:mode/mj/*) ;;
    *) fail "unclassified backend router family: $route" ;;
  esac
done < "$route_manifest"

bash -n "$release"
bash -n "$nginx_installer"
bash -n "$repo/deploy/test-nginx-access-policy-preflight.sh"
if command -v nginx >/dev/null; then
  LMM_NGINX_MIME_TYPES="$mime_types" "$repo/deploy/test-nginx-mime.sh"
  "$repo/deploy/test-nginx-access-policy-preflight.sh"
else
  printf 'split-check: nginx not installed; MIME and access-policy integration tests skipped\n' >&2
fi
"$repo/deploy/test-nginx-installer.sh"

# The behavioral test must consume the tracked map, not an unrelated system
# file. Corrupting a copy of that map must make nginx validation fail.
mime_test_root=$(mktemp -d)
trap 'rm -rf -- "$mime_test_root"' EXIT
cp -- "$mime_types" "$mime_test_root/mime.types"
printf 'this is not nginx syntax\n' >>"$mime_test_root/mime.types"
if command -v nginx >/dev/null &&
   LMM_NGINX_MIME_TYPES="$mime_test_root/mime.types" "$repo/deploy/test-nginx-mime.sh" >/dev/null 2>&1; then
  fail 'corrupted tracked MIME map unexpectedly passed nginx validation'
fi
printf 'frontend/backend split checks passed\n'
