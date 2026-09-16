# CI quality gate

`CI Quality Gate` aggregates the nine mandatory jobs in `workflows/ci.yml`.
It runs with `always()` and accepts only `success`. Failed, cancelled, skipped,
missing and malformed results fail. Duplicate JSON keys and non-standard JSON
constants are rejected. The summary contains fixed job names and normalized
results only, not job outputs or credentials.

Web formatting/copyright and Rust Clippy are blocking checks. Existing lint or
formatting failures must be corrected, not hidden behind `continue-on-error`.
Explicit Bash enables `pipefail`. The Go formatting guard preserves gofmt parse
failures; the static-binary guard rejects empty, unexpected and failed ldd
results, while allowing Linux ldd's normal static-executable exit status.

## Local regression tests

```sh
shellcheck scripts/check-go-format.sh scripts/check-static-go-binary.sh
python3 -B -m unittest discover -s scripts -p test_ci_quality_gate.py -v
```

The tests use Python's standard library, Bash and gofmt. Shell-guard tests execute
real processes. Static-check tests use an ldd fixture; these do not replace the
production-binary build check. No project dependencies are needed for the tests.

When changing mandatory jobs, update both `quality-gate.needs` in the workflow
and `REQUIRED_JOBS` in `scripts/ci_quality_gate.py`. The inventory regression
rejects additions or omissions until both lists agree.

## Scope and activation

The gate covers this workflow only, not independent release workflows or all
open issues. It does not prove the project builds, change branch protection,
merge PRs, or deploy anything. After the workflow passes, an administrator may
add `CI Quality Gate` as a required check while preserving existing protections.

This patch overlaps PR #335's CI changes. Apply it to the stated upstream base
instead of applying both versions blindly. Resolve overlap explicitly if #335
has already landed. Billing, assistant, homepage and other business changes are
not included. Full Web/Go/Rust builds, actionlint and database integration still
need to run on the complete repository; local gate tests are not that evidence.
