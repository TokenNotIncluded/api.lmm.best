#!/usr/bin/env python3
"""Select PR tests from a complete Git diff. Release evidence always stays full."""

import json
import os
from pathlib import Path, PurePosixPath
import re
import subprocess

from ci_quality_gate import REQUIRED_JOBS

PR_BASE = {"changes", "repository-contracts"}
COMPONENTS = {
    "apps/web/": {"web", "translations", "route-coverage-contract"},
    "apps/api-go/": {"go", "release-artifact-contract", "route-coverage-contract"},
    "apps/api-rust/": {
        "rust-preview", "rust-real-integration", "root-route-acceptance-lockfile",
        "route-coverage-contract", "rustsec", "web",
    },
    "packages/pi-lmm-provider/": {"pi-lmm-provider"},
}
DOC_FILES = {"README.md", "CHANGELOG.md", "CONTRIBUTING.md", "CODE_OF_CONDUCT.md", "SECURITY.md"}


def select_jobs(event: str, paths: list[str] | None = None) -> list[str]:
    """Unknown paths/diffs run everything; do not guess about shared dependencies."""
    if event == "schedule":
        selected = {"changes", "rustsec"}
    elif event != "pull_request" or paths is None:
        selected = set(REQUIRED_JOBS)
    else:
        selected = set(PR_BASE)
        for path in paths:
            if (not isinstance(path, str) or not path or path.startswith("/")
                    or "\\" in path or ".." in PurePosixPath(path).parts):
                return list(REQUIRED_JOBS)
            # Documentation below a component can affect its fixtures: classify
            # component roots before allowing repository documentation omissions.
            for prefix, jobs in COMPONENTS.items():
                if path.startswith(prefix):
                    selected.update(jobs)
                    break
            else:
                if path in DOC_FILES or (path.startswith("docs/") and path.endswith(".md")):
                    continue
                # CI/deploy scripts, packaging, workspace locks, submodules and
                # every newly introduced component require the full suite.
                return list(REQUIRED_JOBS)
    return [job for job in REQUIRED_JOBS if job in selected]


def changed_paths(base: str) -> list[str] | None:
    if not re.fullmatch(r"[0-9a-f]{40}", base) or base == "0" * 40:
        return None
    try:
        # --no-renames includes BOTH paths on a cross-component move. Git's
        # NUL output has no API pagination or 300-file path-filter truncation.
        data = subprocess.check_output(
            ["git", "diff", "--no-ext-diff", "--no-renames", "--name-only", "-z", base, "HEAD", "--"],
            stderr=subprocess.DEVNULL, timeout=30,
        )
        if len(data) > 8 * 1024 * 1024:
            return None
        return [part.decode("utf-8") for part in data.split(b"\0") if part]
    except (OSError, subprocess.SubprocessError, UnicodeError):
        return None


def main() -> None:
    event = os.environ.get("GITHUB_EVENT_NAME", "")
    paths = changed_paths(os.environ.get("CI_BASE_SHA", "")) if event == "pull_request" else None
    selected = select_jobs(event, paths)
    output = json.dumps(selected, separators=(",", ":"))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as stream:
        stream.write("required=" + output + "\n")
    print("Required CI jobs: " + ", ".join(selected))
    if event == "pull_request" and paths is None:
        print("No reliable complete diff; full CI selected.")


if __name__ == "__main__":
    main()
