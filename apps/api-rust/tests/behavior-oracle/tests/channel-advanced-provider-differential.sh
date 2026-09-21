#!/usr/bin/env bash
# Static and future isolated-fixture contract gate for the nineteen advanced
# channel operations.  It never addresses production, and its only permitted
# provider target is a disposable literal-loopback fixture.
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
legacy_revision=5418ce6b6d45ed69167b0aad53f2f595e5bc8de9
legacy_root=${LMM_GO_ORACLE_ROOT:-}
[[ -n $legacy_root ]] || { echo "LMM_GO_ORACLE_ROOT is required; set it to an absolute external immutable Go oracle tree ($legacy_revision)" >&2; exit 2; }
[[ $legacy_root == /* && -d $legacy_root && ! -L $legacy_root ]] || { echo 'LMM_GO_ORACLE_ROOT must be an absolute, non-symlink directory' >&2; exit 2; }
legacy_root=$(realpath -e -- "$legacy_root")
case "$legacy_root" in "$repo_root"|"$repo_root"/*) echo 'LMM_GO_ORACLE_ROOT must be external to the current repository' >&2; exit 2 ;; esac
route_source="$repo_root/apps/api-rust/src/routes/channel_advanced.rs"
pg_port=${LMM_CHANNEL_ADVANCED_PG_PORT:-55496}
go_port=${LMM_CHANNEL_ADVANCED_GO_PORT:-13096}
rust_port=${LMM_CHANNEL_ADVANCED_RUST_PORT:-33096}
fixture_port=${LMM_CHANNEL_ADVANCED_FIXTURE_PORT:-19096}
valkey_port=${LMM_CHANNEL_ADVANCED_VALKEY_PORT:-16396}
runtime=$(mktemp -d /tmp/lmm-channel-advanced.XXXXXX)
fixture_secret=$(od -An -N32 -tx1 /dev/urandom | tr -d ' \n')
fixture_secret_file="$runtime/provider.secret"
printf '%s' "$fixture_secret" >"$fixture_secret_file"
chmod 600 "$fixture_secret_file"

cleanup() {
  for pid in "${go_pid:-}" "${rust_pid:-}" "${fixture_pid:-}" "${valkey_pid:-}"; do
    [[ -z $pid ]] || kill "$pid" 2>/dev/null || true
  done
  for pid in "${go_pid:-}" "${rust_pid:-}" "${fixture_pid:-}" "${valkey_pid:-}"; do
    [[ -z $pid ]] || wait "$pid" 2>/dev/null || true
  done
  [[ ! -d $runtime/pg ]] || pg_ctl -D "$runtime/pg" -m fast -w stop >/dev/null 2>&1 || true
  case "$runtime" in
    /tmp/lmm-channel-advanced.*) rm -rf "$runtime" ;;
    *) echo "refusing unexpected runtime cleanup target: $runtime" >&2 ;;
  esac
}
trap cleanup EXIT INT TERM

for command in awk git initdb jq od pg_ctl postgres python3 rg valkey-cli valkey-server; do
  command -v "$command" >/dev/null || {
    echo "required command unavailable: $command" >&2
    exit 1
  }
done
[[ -f $route_source ]] || { echo "missing Rust route source: $route_source" >&2; exit 1; }
[[ -f $legacy_root/router/channel-router.go ]] || {
  echo "missing frozen channel router" >&2
  exit 1
}

preflight_port() {
  local name=$1 port=$2
  # The probe runs in a subshell, so the descriptor closes automatically. An
  # explicit close would mask a refused connection with a zero exit status.
  if (exec 3<>"/dev/tcp/127.0.0.1/$port") 2>/dev/null; then
    echo "$name port is already occupied: 127.0.0.1:$port" >&2
    exit 1
  fi
}
preflight_port PostgreSQL "$pg_port"
preflight_port Go_HTTP "$go_port"
preflight_port Rust_HTTP "$rust_port"
preflight_port Provider_Fixture "$fixture_port"
preflight_port Valkey "$valkey_port"

require_source_contract() {
  local expected_routes actual_routes production_source
  expected_routes=19
  actual_routes=$(rg -o '\.route\(' "$route_source" | wc -l | tr -d ' ')
  [[ $actual_routes == "$expected_routes" ]] || {
    echo "expected $expected_routes advanced routes, found $actual_routes" >&2
    return 1
  }
  production_source=$(sed '/^#\[cfg(test)\]/,$d' "$route_source")
  for route in \
    '/api/channel/test' \
    '/api/channel/test/{id}' \
    '/api/channel/update_balance' \
    '/api/channel/update_balance/{id}' \
    '/api/channel/{id}/codex/refresh' \
    '/api/channel/{id}/codex/usage' \
    '/api/channel/{id}/codex/usage/reset-credits' \
    '/api/channel/{id}/codex/usage/reset' \
    '/api/channel/fetch_models' \
    '/api/channel/fetch_models/{id}' \
    '/api/channel/ollama/pull' \
    '/api/channel/ollama/pull/stream' \
    '/api/channel/ollama/delete' \
    '/api/channel/ollama/version/{id}' \
    '/api/channel/upstream_updates/apply' \
    '/api/channel/upstream_updates/apply_all' \
    '/api/channel/upstream_updates/detect' \
    '/api/channel/upstream_updates/detect_all' \
    '/api/channel/{id}/key'; do
    rg -Fq "$route" "$route_source"
  done
  rg -Fq 'validate_loopback_target' "$route_source"
  rg -Fq 'host.parse::<IpAddr>()' "$route_source"
  rg -Fq 'request.into_parts()' "$route_source"
  rg -Fq 'to_bytes(body, DEFAULT_ADVANCED_REQUEST_LIMIT)' "$route_source"
  rg -Fq 'refresh_codex_credential' "$route_source"
  rg -Fq 'x-accel-buffering' "$route_source"
  rg -Fq 'SecureVerificationRequired' "$legacy_root/router/channel-router.go"
  rg -Fq 'return Err(ChannelAdvancedError::Forbidden);' "$route_source"
  # `https://api.openai.com/auth` is a JWT claim namespace, not an outbound
  # target. Exclude that data key while still rejecting any actual provider
  # URL embedded in the production portion of the route.
  if grep -E 'https://auth\.openai\.com|http://[^127]|https://[^127]' <<<"$production_source" \
      | grep -vF 'get("https://api.openai.com/auth")' >/dev/null; then
    echo "candidate source contains a non-loopback provider target" >&2
    return 1
  fi
}

start_disposable_dependencies() {
  initdb --no-locale --encoding=UTF8 --auth=trust -D "$runtime/pg" >/dev/null
  pg_ctl -D "$runtime/pg" -l "$runtime/postgres.log" \
    -o "-h 127.0.0.1 -p $pg_port -k $runtime" -w start >/dev/null
  valkey-server --bind 127.0.0.1 --port "$valkey_port" --save '' --appendonly no \
    --requirepass "$fixture_secret" --dir "$runtime" --logfile "$runtime/valkey.log" >/dev/null 2>&1 &
  valkey_pid=$!
  for _ in {1..100}; do
    if valkey-cli --no-auth-warning -a "$fixture_secret" -h 127.0.0.1 -p "$valkey_port" ping >/dev/null 2>&1; then
      return
    fi
    sleep .05
  done
  echo "disposable Valkey did not become ready" >&2
  return 1
}

require_source_contract

if [[ ${LMM_CHANNEL_ADVANCED_EXECUTE:-0} != 1 ]]; then
  jq -cn \
    --arg source "$route_source" \
    --argjson ports "[$pg_port,$go_port,$rust_port,$fixture_port,$valkey_port]" \
    '{test:"channel-advanced-provider-differential",mode:"static-contract",source:$source,ports:$ports,provider:"literal-loopback only",credentials:"per-run synthetic secret; never logged",approval_credit:false,reason:"No isolated test listener injects the advanced-channel store, cache invalidator, and loopback provider fixture yet."}'
  exit 0
fi

# This mode validates only disposable dependency ownership.  It deliberately
# does not launch Go or Rust listeners until a future adapter-injection harness
# can prove response, PostgreSQL, and cache parity for all nineteen routes.
start_disposable_dependencies
jq -cn \
  --arg runtime "$runtime" \
  '{test:"channel-advanced-provider-differential",mode:"isolated-dependency-safety",runtime:$runtime,provider:"127.0.0.1 only",approval_credit:false,reason:"Listener injection and cache side-effect comparison remain required before route approval."}'
