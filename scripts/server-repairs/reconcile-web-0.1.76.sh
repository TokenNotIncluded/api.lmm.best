#!/usr/bin/env bash
# One recorded drift recovery, with signed packages and native frontend hooks.
set -Eeuo pipefail
set +x
: "${LMM_OPS_REPORT:?Run through the authorized server-ops workflow}"
work=/var/lib/lmm-api-web-reconcile/web-v0.1.76
helper="$work/reconcile-frontend-package.py"
[[ -f "$helper" && ! -L "$helper" ]]
[[ $(sha256sum "$helper" | cut -d' ' -f1) == f927ab22000928e2af236f8cf5635e2fc548e2877e5f7b08c8634637bfc6e452 ]]
python3 "$helper" --action apply \
  --plan-sha256 83ad0f32dee25dc6b5dbdb3a34a14f477576dba57fc9adf268a63b363ba917a4 \
  >"$LMM_OPS_REPORT"
