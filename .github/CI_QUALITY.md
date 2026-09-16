# CI quality gate

`CI Quality Gate` aggregates every mandatory job in `workflows/ci.yml`. It runs
with `always()` and accepts only `success`: failed, cancelled, skipped, missing,
and malformed results fail the gate. Job results and failure reasons are written
to the Actions job summary. No job outputs or credentials are included.

The web formatting and copyright checks and Rust Clippy are blocking checks,
not advisory steps. Existing formatting or lint debt must be corrected before
merging; do not restore green checks with `continue-on-error`, warning wrappers,
or skipped mandatory jobs. No repository-wide formatting changes are included
in the gate implementation.

Explicit Bash is the workflow default, enabling `pipefail` for run steps. Keep
intentional error handling local to the command that requires it. The Go format
check propagates gofmt parse errors and prints the files needing formatting. Actions and
toolchain pins, read-only permissions, job timeouts, and release-sensitive
concurrency behavior are preserved.

CI also runs on merge queue `checks_requested` events. It still runs for PRs,
main pushes, tags, and manual dispatches.

## Local regression tests

```sh
python3 -B -m unittest discover -s scripts -p test_ci_quality_gate.py -v
```

The tests exercise failure propagation, malformed input, missing jobs, skipped
and cancelled results, safe summaries, and the actual process exit status. They
require only the Python standard library. Repository Contracts runs them in CI.
When adding or removing mandatory jobs, update both `quality-gate.needs` and
`REQUIRED_JOBS` in `scripts/ci_quality_gate.py`.

## Branch protection

After this workflow has run successfully, a repository administrator should add
`CI Quality Gate` to the required status checks for `main`. Keep existing required
checks until the new gate is verified. Changing this workflow does not configure
branch protection, remove bypass permissions, or alter release approval rules.
