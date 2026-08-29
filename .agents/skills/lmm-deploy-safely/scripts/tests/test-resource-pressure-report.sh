#!/usr/bin/env bash

set -euo pipefail
umask 077

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
readonly SCRIPT_DIR
readonly SCRIPT="$SCRIPT_DIR/resource-pressure-report.sh"
readonly TEST_STATE_ROOT=${XDG_STATE_HOME:-$HOME/.local/state}
mkdir -p -- "$TEST_STATE_ROOT"
TEST_ROOT=$(mktemp -d "$TEST_STATE_ROOT/lmm-resource-report-test.XXXXXX")
readonly TEST_ROOT
trap 'rm -rf -- "$TEST_ROOT"' EXIT

die() {
  printf 'test error: %s\n' "$1" >&2
  exit 1
}

assert_contains() {
  local needle=$1
  local file=$2
  grep -F -- "$needle" "$file" >/dev/null || die "missing '$needle' in $file"
}

assert_exit() {
  local expected=$1
  shift
  local actual=0
  "$@" >/dev/null 2>&1 || actual=$?
  [[ $actual == "$expected" ]] || die "expected exit $expected, got $actual: $*"
}

mkdir -p -- "$TEST_ROOT/bin" "$TEST_ROOT/proc"
cat > "$TEST_ROOT/proc/meminfo" <<'EOF'
MemTotal:        973824 kB
MemAvailable:    409600 kB
SwapTotal:      1048576 kB
SwapFree:        983040 kB
EOF
cat > "$TEST_ROOT/proc/vmstat" <<'EOF'
pswpin 12
pswpout 34
EOF
cat > "$TEST_ROOT/bin/hostnamectl" <<'EOF'
#!/usr/bin/env bash
printf 'arch-dmit\n'
EOF
cat > "$TEST_ROOT/bin/systemctl" <<'EOF'
#!/usr/bin/env bash
cat <<'OUT'
ActiveState=active
SubState=running
MainPID=1234
NRestarts=0
MemoryCurrent=104857600
MemoryHigh=335544320
MemoryMax=402653184
MemorySwapMax=268435456
OUT
EOF
cat > "$TEST_ROOT/bin/df" <<'EOF'
#!/usr/bin/env bash
printf 'Filesystem 1024-blocks Used Available Capacity Mounted on\n'
printf '/dev/test 21474836480 10737418240 10737418240 50%% /\n'
EOF
cat > "$TEST_ROOT/bin/curl" <<'EOF'
#!/usr/bin/env bash
printf '200 0.010000\n'
EOF
chmod 0755 "$TEST_ROOT/bin/hostnamectl" "$TEST_ROOT/bin/systemctl" "$TEST_ROOT/bin/df" "$TEST_ROOT/bin/curl"

bash -n "$SCRIPT"

output="$TEST_ROOT/report.kv"
PATH="$TEST_ROOT/bin:$PATH" "$SCRIPT" --expected-host arch-dmit --proc-root "$TEST_ROOT/proc" \
  --samples 2 --interval 0 >"$output"
assert_contains 'host_match=true' "$output"
assert_contains 'mem_pressure=green' "$output"
assert_contains 'swap_used_percent=6' "$output"
assert_contains 'swap_change=unchanged' "$output"
assert_contains 'root_headroom=green' "$output"
assert_contains 'service_state=green' "$output"
assert_contains 'status_http_status=200' "$output"
assert_contains 'livez_http_status=200' "$output"
assert_contains 'mutations_performed=none' "$output"

json_output="$TEST_ROOT/report.json"
PATH="$TEST_ROOT/bin:$PATH" "$SCRIPT" --expected-host arch-dmit --proc-root "$TEST_ROOT/proc" \
  --samples 1 --interval 0 --format json >"$json_output"
assert_contains '"overall_state": "green"' "$json_output"
if command -v jq >/dev/null 2>&1; then
  jq -e . "$json_output" >/dev/null || die 'JSON report is invalid'
fi

cat > "$TEST_ROOT/bin/hostnamectl" <<'EOF'
#!/usr/bin/env bash
printf 'not-production\n'
EOF
chmod 0755 "$TEST_ROOT/bin/hostnamectl"
assert_exit 3 env PATH="$TEST_ROOT/bin:$PATH" "$SCRIPT" --expected-host arch-dmit --proc-root "$TEST_ROOT/proc"

assert_exit 2 env PATH="$TEST_ROOT/bin:$PATH" "$SCRIPT" --expected-host arch-dmit --proc-root "$TEST_ROOT/proc" --samples 6
assert_exit 2 env PATH="$TEST_ROOT/bin:$PATH" "$SCRIPT" --expected-host arch-dmit --proc-root "$TEST_ROOT/proc" --base-url 'file:///etc/passwd'

if sed '/^[[:space:]]*#/d' "$SCRIPT" | rg -n \
  '(^|[[:space:]])(rm|reboot|shutdown|swapoff|swapon)([[:space:]]|$)|systemctl[[:space:]]+(start|stop|restart|enable|disable|reset-failed)' \
  >/dev/null; then
  die 'resource report contains a mutating command'
fi

printf 'resource-pressure-report tests: PASS\n'
