#!/usr/bin/env bash
# Run only against a fresh CI database. Called through the private mise setup.
set -euo pipefail
cd "${GITHUB_WORKSPACE:?}/services/core"
: "${RUNNER_TEMP:?}" "${DATABASE_URL:?}"
case "$DATABASE_URL" in
  postgres://postgres:postgres@127.0.0.1:*/postgres) ;;
  *) printf '%s\n' 'A04 runner requires its dedicated synthetic CI database.' >&2; exit 2;;
esac
mkdir -p "$RUNNER_TEMP/a04-evidence"
evidence="$RUNNER_TEMP/a04-evidence"
export CARGO_TARGET_DIR="$RUNNER_TEMP/a04-target"
export CARGO_BUILD_JOBS=2 CARGO_INCREMENTAL=0 CARGO_PROFILE_TEST_DEBUG=0 CARGO_PROFILE_DEV_DEBUG=0
rustc --version | tee "$evidence/rustc.txt"
cargo --version | tee "$evidence/cargo.txt"
grep -q '^rustc 1\.99\.0 ' "$evidence/rustc.txt"
grep -q '^cargo 1\.99\.0 ' "$evidence/cargo.txt"
printf '%s\n' "$(git rev-parse HEAD)" > "$evidence/source-sha.txt"
# First integration pass resolves the new exact dependencies. The resulting
# lockfile and formatted source are retained for review; no CI write token.
cargo metadata --format-version 1 >/dev/null
files=(src/history_read.rs src/history_read/*.rs src/finance_export.rs src/finance_export/*.rs tests/history_read.rs tests/finance_export.rs tests/history_repository.rs)
rustfmt --edition 2024 --config skip_children=true "${files[@]}"
git diff --binary -- Cargo.lock "${files[@]}" > "$evidence/generated.patch"
cp Cargo.lock "$evidence/Cargo.lock"
tar -czf "$evidence/checked-source.tar.gz" Cargo.toml Cargo.lock src tests schema build.rs
set +e
cargo test --locked --test history_read --test finance_export --test history_repository --test financial_profile --no-fail-fast 2>&1 | tee "$evidence/tests.log"
status=${PIPESTATUS[0]}
printf '%s\n' "$status" > "$evidence/test-exit-code.txt"
exit "$status"
