#!/usr/bin/env bash
set -euo pipefail
# Local only. No package downloads, Docker, remote CI, or production addresses.
HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
OUT=${1:-"$HERE/results"}
umask 077
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
for tool in unshare nsenter ip tc python3 timeout go; do command -v "$tool" >/dev/null; done
python3 -c 'import grpc, cryptography, psutil'
POLICY="$HERE/../../../apps/lmm-extensions/internal/coreclient/transportpolicy"
mkdir -p "$OUT/.gocache"
(cd "$POLICY"; GOCACHE="$OUT/.gocache" GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GO111MODULE=off \
 timeout 90s go test -race -c -o "$OUT/policy.test")
export DP24_POLICY_TEST="$OUT/policy.test"
export DP24_OUTER_NETNS
DP24_OUTER_NETNS=$(readlink /proc/self/ns/net)
export TERM=dumb
# Exit 2 means an experiment was blocked; it is not an acceptance pass.
umask 077
exec env -i PATH="$PATH" TERM=dumb DP24_POLICY_TEST="$DP24_POLICY_TEST" \
  DP24_OUTER_NETNS="$DP24_OUTER_NETNS" \
  timeout --signal=TERM --kill-after=5s 180s unshare --user --map-root-user --net --mount \
  bash -c 'set -euo pipefail; mount --make-rprivate /; ip link set lo up; ulimit -n 256; "$2/policy.test" -test.v > "$2/go-tests.log"; exec python3 "$1/lab.py" orchestrate "$2"' _ "$HERE" "$OUT"
