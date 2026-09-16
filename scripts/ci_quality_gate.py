#!/usr/bin/env python3
"""Require successful evidence from every mandatory job in CI's needs context."""

import json
import os
import sys
from pathlib import Path

# Keep this list and quality-gate.needs in .github/workflows/ci.yml in sync.
REQUIRED_JOBS = (
    "repository-contracts",
    "release-artifact-contract",
    "pi-lmm-provider",
    "web",
    "go",
    "rust-preview",
    "route-coverage-contract",
    "rust-real-integration",
    "aur-package-matrix",
    "translations",
)
KNOWN_RESULTS = frozenset(("success", "failure", "cancelled", "skipped"))


def unique_object(pairs: list[tuple[str, object]]) -> dict[str, object]:
    """Reject ambiguous duplicate keys instead of silently accepting the last one."""
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("Duplicate JSON object key")
        result[key] = value
    return result


def reject_constant(value: str) -> None:
    """GitHub emits standard JSON; NaN and Infinity are not valid job evidence."""
    raise ValueError("Non-standard JSON constant")


def check_needs(needs: object) -> tuple[dict[str, str], list[str]]:
    """Fail closed on absent, malformed, skipped, or non-success job results."""
    results = dict.fromkeys(REQUIRED_JOBS, "missing")
    if not isinstance(needs, dict):
        return results, ["CI_NEEDS must be a JSON object containing job results."]

    errors = []
    if set(needs) - set(REQUIRED_JOBS):
        errors.append("Unexpected jobs in CI_NEEDS; review the required-job inventory.")
    for job in REQUIRED_JOBS:
        if job not in needs:
            errors.append(f"{job}: required job is missing.")
            continue
        entry = needs[job]
        result = entry.get("result") if isinstance(entry, dict) else None
        # Never render arbitrary outputs or untrusted strings in workflow commands.
        status = result if isinstance(result, str) and result in KNOWN_RESULTS else "invalid"
        results[job] = status
        if status != "success":
            errors.append(f"{job}: expected success, got {status}.")
    return results, errors


def summary_text(results: dict[str, str], errors: list[str]) -> str:
    lines = ["## CI Quality Gate", "", "| Job | Result |", "| --- | --- |"]
    lines.extend(f"| {job} | {results[job]} |" for job in REQUIRED_JOBS)
    lines.extend(("", "**FAIL**" if errors else "**PASS**"))
    if errors:
        lines.extend(("", *(f"- {error}" for error in errors)))
    return "\n".join(lines) + "\n"


def main() -> int:
    try:
        needs = json.loads(
            os.environ.get("CI_NEEDS", ""),
            object_pairs_hook=unique_object,
            parse_constant=reject_constant,
        )
    except (ValueError, RecursionError):
        results = dict.fromkeys(REQUIRED_JOBS, "missing")
        errors = ["CI_NEEDS is missing or is not valid unambiguous JSON."]
    else:
        results, errors = check_needs(needs)

    summary = summary_text(results, errors)
    print(summary, end="")
    for error in errors:
        print(f"::error::{error}")
    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        try:
            with Path(summary_path).open("a", encoding="utf-8") as stream:
                stream.write(summary)
        except OSError:
            print("::error::Unable to write the CI quality summary.", file=sys.stderr)
            return 1
    return int(bool(errors))


if __name__ == "__main__":
    sys.exit(main())
