#!/usr/bin/env bash
# Honest A7 identity-federation verification entrypoint.
#
# Only the provider exchange currently has a compile-time loopback seam. The
# shared listener issuer, cache publisher, and Go-equivalent session fixture
# are not wired in this route module, so this script reports those rows as
# BLOCKED and exits non-zero instead of inventing listener environment knobs.
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
repo_root=$(cd -- "$script_dir/../../../../../" && pwd -P)
runtime=$(mktemp -d /tmp/lmm-identity-federation.XXXXXX)
test_pid=''
test_pgid=''

# shellcheck disable=SC2329 # Invoked indirectly by the EXIT/INT/TERM trap.
cleanup() {
  if [[ -n $test_pgid ]]; then
    kill -TERM -- "-$test_pgid" 2>/dev/null || true
    for _ in {1..20}; do
      kill -0 -- "-$test_pgid" 2>/dev/null || break
      sleep 0.05
    done
    kill -KILL -- "-$test_pgid" 2>/dev/null || true
  fi
  [[ -n $test_pid ]] && wait "$test_pid" 2>/dev/null || true
  case $runtime in
    /tmp/lmm-identity-federation.*) rm -rf -- "$runtime" ;;
    *) printf 'refusing unexpected runtime path: %s\n' "$runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM
trap 'printf "identity federation check failed at line %s\\n" "$LINENO" >&2' ERR

for command in cargo setsid ps; do
  command -v "$command" >/dev/null || {
    printf 'required command is unavailable: %s\n' "$command" >&2
    exit 1
  }
done

cd -- "$repo_root/apps/api-rust"
# The Rust test owns one ephemeral 127.0.0.1:0 socket from bind through
# shutdown. No port is preselected, no secret is passed in argv/environment,
# and the production-only URL policy cannot construct the fixture variant.
setsid cargo test -p lmm-api-rs --lib \
  routes::identity_federation::adapter_tests::configured_github_provider_uses_compile_time_loopback_fixture \
  -- --exact >"$runtime/provider-loopback.log" 2>&1 &
test_pid=$!
test_pgid=$(ps -o pgid= -p "$test_pid" | tr -d '[:space:]')
if [[ -z $test_pgid || $test_pgid != "$test_pid" ]]; then
  printf 'fixture test process does not own its process group\n' >&2
  exit 1
fi
set +e
wait "$test_pid"
test_status=$?
set -e
test_pid=''
test_pgid=''
if ((test_status != 0)); then
  cat "$runtime/provider-loopback.log" >&2
  exit "$test_status"
fi

cat <<'MATRIX'
identity federation verification matrix:
  PASS    provider exchange: compile-time isolated loopback HTTP fixture
  BLOCKED PostgreSQL state/claim differential: shared Go/Rust fixture not wired
  BLOCKED Valkey publication: listener has not installed FederationMutationPublisher
  BLOCKED session/JWT parity: listener has not installed the shared login issuer
  BLOCKED Set-Cookie parity: requires the same shared login issuer

No full Go/Rust differential was claimed. Wire the shared listener fixtures,
issuer, and publisher in their owning modules before this gate can pass.
MATRIX
exit 2
