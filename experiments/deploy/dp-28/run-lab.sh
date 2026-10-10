#!/usr/bin/env bash
# All destructive test work occurs on a private, capped tmpfs. No Docker,
# remote CI, production paths or inherited network namespace is used.
set -euo pipefail
HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
OUT=${1:?Usage: bash run-lab.sh /absolute/new-evidence-directory}
[[ "$OUT" = /* && ! -e "$OUT" ]] || { echo 'Use a new absolute evidence directory.' >&2; exit 2; }
mkdir -m 700 -- "$OUT"
OUT=$(cd -- "$OUT" && pwd -P)
export PYTHONDONTWRITEBYTECODE=1
cc -std=c11 -O2 -Wall -Wextra -Werror -Wl,--build-id=none "$HERE/fixture.c" -o "$OUT/fixture-probe"
# Some sandboxes allow user/mount/network namespaces but not a new proc mount.
# This suite needs no new PID namespace. It reports /proc visibility limits.
timeout 180s unshare -Urnm -- bash -s -- "$HERE" "$OUT" <<'INNER'
set -euo pipefail
HERE=$1
OUT=$2
LAB=$(mktemp -d /tmp/lmm-dp28-lab.XXXXXXXX)
cleanup() {
    umount "$LAB" || true
    rmdir "$LAB" || true
}
trap cleanup EXIT
mount --make-rprivate /
# This is a deliberately small test fixture, not a server-size recommendation.
mount -t tmpfs -o size=16m,nr_inodes=512,mode=0700 lmm-dp28-lab "$LAB"
export DP28_LAB="$LAB" DP28_OUT="$OUT" DP28_PROBE="$OUT/fixture-probe"
python3 -B "$HERE/run_experiment.py"
python3 -B -m unittest discover -s "$HERE" -p 'test_*.py' -v > "$OUT/tests.log" 2>&1 || {
    cat "$OUT/tests.log"
    exit 1
}
cat "$OUT/tests.log"
INNER
