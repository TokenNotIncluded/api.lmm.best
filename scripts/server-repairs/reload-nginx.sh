#!/usr/bin/env bash
# No config mutation, service restart, access-policy bypass, or automatic rollback.
set -Eeuo pipefail
: "${LMM_OPS_REPORT:?Run through the manual server-ops workflow}"
nginx -t
systemctl reload nginx.service
systemctl is-active --quiet nginx.service
printf 'Nginx configuration validated; graceful reload completed.\n' >"$LMM_OPS_REPORT"
