#!/usr/bin/env bash
# One recorded drift recovery, with signed packages and native frontend hooks.
set -Eeuo pipefail
set +x
: "${LMM_OPS_REPORT:?Run through the authorized server-ops workflow}"
work=/var/lib/lmm-api-web-reconcile/web-v0.1.76
helper="$work/reconcile-frontend-package.py"
[[ -f "$helper" && ! -L "$helper" ]]
[[ $(sha256sum "$helper" | cut -d' ' -f1) == c0870847fb00600efff581ad41ea71da7a7ee9ca416b687c5154f8f18dd3b706 ]]
python3 "$helper" --action apply \
  --plan-sha256 29324f34ed3e02be22bdf7fd53a4f69a91991669dfa2cba7c89158a3f7f1c6e2 \
  >"$LMM_OPS_REPORT"
