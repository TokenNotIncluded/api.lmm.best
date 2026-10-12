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
# Shared lock integration is owned by S11. Test its frozen patch in this
# disposable checkout, not a freshly resolved dependency graph on every run.
# Both accepted hashes are exact Cargo-generated inputs retained by run
# 38210812027. Unexpected lock changes require a new reviewed patch.
case "$(git hash-object Cargo.lock)" in
  e571b17a29f201e321f005eba20c2b4fa0bf3bf0)
    patch="$PWD/tests/history_read/cargo-lock.patch"
    git -C "$GITHUB_WORKSPACE" apply --check "$patch"
    git -C "$GITHUB_WORKSPACE" apply "$patch"
    ;;
  b54ce5875919b916743171f6ce9b72a1d08449a7) ;;
  *) printf '%s\n' 'A04 lock input changed; reconcile the integration patch.' >&2; exit 2;;
esac
test "$(git hash-object Cargo.lock)" = b54ce5875919b916743171f6ce9b72a1d08449a7
cargo metadata --locked --format-version 1 >/dev/null
printf '%s\n' 'isolated: frozen S11 lock patch and scoped rustfmt applied' > "$evidence/tested-source-mode.txt"
files=(src/history_read.rs src/history_read/*.rs src/finance_export.rs src/finance_export/*.rs tests/history_read.rs tests/finance_export.rs tests/history_repository.rs)
rustfmt --edition 2024 --config skip_children=true "${files[@]}"
git diff --binary -- Cargo.lock "${files[@]}" > "$evidence/generated.patch"
cp Cargo.lock "$evidence/Cargo.lock"
tar -czf "$evidence/checked-source.tar.gz" Cargo.toml Cargo.lock src tests schema build.rs
sha256sum "$evidence/checked-source.tar.gz" > "$evidence/checked-source.sha256"
set +e
cargo test --locked --test history_read --test finance_export --test history_repository --test financial_profile --no-fail-fast 2>&1 | tee "$evidence/tests.log"
status=${PIPESTATUS[0]}
printf '%s\n' "$status" > "$evidence/test-exit-code.txt"
exit "$status"
