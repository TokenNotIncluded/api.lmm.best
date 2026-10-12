#!/usr/bin/env bash
# Basic isolated image smoke only; no database, identity or business acceptance.
set -euo pipefail
if [[ $# != 3 || ( "$1" != core && "$1" != extensions ) ]]; then
  echo "Usage: $0 core|extensions IMAGE EVIDENCE_DIRECTORY" >&2
  exit 2
fi
component=$1
image=$2
evidence=$3
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
mkdir -p -- "$evidence"
evidence=$(cd -- "$evidence" && pwd)
[[ -z $(find "$evidence" -mindepth 1 -print -quit) ]] || {
  echo 'Evidence directory must be empty; refusing stale smoke results' >&2
  exit 2
}
# Image inspection contains no runtime credential: synthetic files exist only in tmpfs.
docker image inspect "$image" > "$evidence/image-inspect.json"
python3 - "$component" "$evidence/image-inspect.json" <<'PY'
import json, sys
component, path = sys.argv[1:]
image, = json.load(open(path))
assert image['Os'] == 'linux' and image['Architecture'] == 'amd64'
assert image['Config']['User'] == '65532:65532', 'image must default to nonroot'
assert image['Config']['Entrypoint'] == ['/usr/bin/lmm-' + component]
assert image['Config']['Cmd'] == ['--config', '/etc/lmm.conf.d/' + component + '.toml']
assert '/health/live' in ' '.join(image['Config']['Healthcheck']['Test'])
PY
cid=''
cleanup() {
  rc=$?
  trap - EXIT INT TERM
  if [[ -n "$cid" ]]; then
    docker logs "$cid" > "$evidence/runtime.log" 2>&1 || true
    docker rm -f "$cid" > /dev/null || true
  fi
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
# The same unprivileged container writes private fixtures then execs the real binary.
# No bind mounts, owner URL, external database or host ports are involved.
# Variables intentionally expand inside the container, not on the host.
# shellcheck disable=SC2016
startup='umask 077
mkdir /tmp/image-smoke
case "$SMOKE_COMPONENT" in
  core)
    printf "%s\n" "version = 1" "[server]" "listen = \"127.0.0.1:8080\"" "worker_threads = 1" "max_inflight = 16" "drain_timeout_seconds = 5" > /tmp/image-smoke/runtime.toml
    ;;
  extensions)
    printf "%s" "image-smoke-synthetic-private-credential-only" > /tmp/image-smoke/host-token
    chmod 0400 /tmp/image-smoke/host-token
    printf "%s\n" "version = 1" "[host]" "listen = \"127.0.0.1:8081\"" "token_file = \"/tmp/image-smoke/host-token\"" "modules = [\"none\"]" "web_compat = false" "[legal]" "config_file = \"/tmp/image-smoke/legal.toml\"" > /tmp/image-smoke/runtime.toml
    cp /usr/share/lmm/legal/site.example.toml /tmp/image-smoke/legal.toml
    chmod 0400 /tmp/image-smoke/legal.toml
    test "$(stat -c "%u:%a" /tmp/image-smoke/host-token)" = "65532:400"
    ;;
esac
chmod 0400 /tmp/image-smoke/runtime.toml
test "$(stat -c "%u:%a" /tmp/image-smoke/runtime.toml)" = "65532:400"
exec "/usr/bin/lmm-$SMOKE_COMPONENT" --config /tmp/image-smoke/runtime.toml'
# Capture ownership before start, so even a failed start is cleaned up precisely.
cid=$(docker create --network none --user 65532:65532 --read-only \
  --cap-drop ALL --security-opt no-new-privileges --pids-limit 64 \
  --memory 256m --cpus 1 \
  --tmpfs /tmp:rw,noexec,nosuid,nodev,size=8m,uid=65532,gid=65532,mode=0700 \
  --env "SMOKE_COMPONENT=$component" --entrypoint /bin/sh "$image" -eu -c "$startup")
docker start "$cid" > /dev/null
docker inspect "$cid" | python3 -c '
import json, sys
c, = json.load(sys.stdin)
h = c["HostConfig"]
r = {"network": h["NetworkMode"], "user": c["Config"]["User"],
     "readonly_rootfs": h["ReadonlyRootfs"], "cap_drop": h["CapDrop"],
     "security_opt": h["SecurityOpt"], "mounts": c["Mounts"]}
assert r["network"] == "none" and r["user"] == "65532:65532"
assert r["readonly_rootfs"] and r["cap_drop"] == ["ALL"]
assert any(s.startswith("no-new-privileges") for s in r["security_opt"])
assert all(m["Type"] == "tmpfs" for m in r["mounts"])
json.dump(r, sys.stdout, indent=2)
' > "$evidence/runtime-security.json"
[[ $(docker exec "$cid" id -u) == 65532 ]]
docker exec "$cid" /bin/sh -ec 'test "$(awk "/^Uid:/ {print \$2}" /proc/1/status)" = 65532'
# Verify exact license/attribution bytes from this source, not merely existence.
for file in LICENSE NOTICE THIRD-PARTY-LICENSES.md; do
  expected=$(sha256sum "$root/$file" | awk '{print $1}')
  actual=$(docker exec "$cid" sha256sum "/usr/share/licenses/lmm/$file" | awk '{print $1}')
  [[ "$actual" == "$expected" ]]
done
if [[ "$component" == extensions ]]; then
  expected=$(sha256sum "$root/config/legal/site.example.toml" | awk '{print $1}')
  actual=$(docker exec "$cid" sha256sum /usr/share/lmm/legal/site.example.toml | awk '{print $1}')
  [[ "$actual" == "$expected" ]]
  while IFS= read -r -d '' file; do
    relative=${file#"$root/config/legal/"}
    expected=$(sha256sum "$file" | awk '{print $1}')
    actual=$(docker exec "$cid" sha256sum "/usr/share/lmm/legal/$relative" | awk '{print $1}')
    [[ "$actual" == "$expected" ]]
  done < <(find "$root/config/legal/templates" -type f -name '*.md' -print0)
  docker exec "$cid" /bin/sh -ec '
    test "$(find /usr/share/lmm/legal/templates -type f -name "*.md" | wc -l)" -eq 6
    test ! -e /usr/share/lmm/legal/site.json
    test ! -e /usr/share/lmm/legal/site.toml
    test ! -e /usr/share/lmm/legal/site.example.json
  '
fi
docker exec "$cid" /bin/sh -ec 'test ! -e /run/secrets; test ! -e /etc/lmm.conf.d'
port=8080
if [[ "$component" == extensions ]]; then port=8081; fi
healthy=false
for (( attempt=0; attempt<60; attempt++ )); do
  if docker exec "$cid" curl --fail --silent --show-error --max-time 2 --write-out '\n%{http_code}\n' \
      "http://127.0.0.1:$port/health/live" > "$evidence/health-live.txt" 2>/dev/null \
      && [[ $(tail -n 1 "$evidence/health-live.txt") == 200 ]]; then
    healthy=true
    break
  fi
  [[ $(docker inspect --format '{{.State.Running}}' "$cid") == true ]] || break
  sleep 1
done
[[ "$healthy" == true ]] || { echo 'Image liveness smoke failed' >&2; exit 1; }
docker stop --time 10 "$cid" > /dev/null
[[ $(docker inspect --format '{{.State.ExitCode}}' "$cid") == 0 ]]
python3 - "$component" "$evidence" <<'PY'
import json, pathlib, sys
component, directory = sys.argv[1:]
directory = pathlib.Path(directory)
image, = json.loads((directory / 'image-inspect.json').read_text())
result = {'component': component, 'image_id': image['Id'], 'platform': 'linux/amd64',
          'smoke': 'passed', 'scope': 'basic image startup and health/live only',
          'business_acceptance': False, 'database_used': False,
          'licenses_match_source': True, 'graceful_exit_code': 0}
(directory / 'smoke-result.json').write_text(json.dumps(result, indent=2) + '\n')
PY
printf '%s\n' "$component: basic image smoke passed (not full business acceptance)"
