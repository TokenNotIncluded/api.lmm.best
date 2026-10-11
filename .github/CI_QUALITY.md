# Quality checks

Current local rules, executable checks and acceptance limits are documented in
[the quality guide](../docs/quality.md).

Use `just quality` for the local entry point and `just quality-self-tests` for its
regression tests. An unexecuted, blocked or skipped check is not a passing check.
The former `scripts/ci_quality_gate.py` and `test_ci_quality_gate.py` commands do
not exist in this microservice WIP and are not valid instructions for this branch.

This update does not change any workflow trigger, branch protection or repository
permission. Local reports are evidence for review, not authenticated approval or
permission to merge, publish or deploy.
