#!/usr/bin/env python3
"""Refuse publication while any issue or pull request remains open."""

import json
import os
import re
import subprocess
import sys


class ReleaseBlocked(Exception):
    """The repository cannot yet be declared ready for publication."""


def verify(repository, run=subprocess.run):
    if not isinstance(repository, str) or not re.fullmatch(
        r"[A-Za-z0-9][A-Za-z0-9-]*/[A-Za-z0-9_][A-Za-z0-9_.-]*", repository
    ):
        raise ReleaseBlocked("a valid GITHUB_REPOSITORY is required")
    # GitHub's repository issues endpoint also returns pull requests. One item
    # is sufficient to reject publication; an empty first page proves neither
    # type has an open item. No search index or pagination assumption is used.
    command = [
        "gh", "api", "--hostname", "github.com", "--method", "GET",
        "-H", "Accept: application/vnd.github+json",
        f"repos/{repository}/issues?state=open&per_page=1",
    ]
    try:
        result = run(command, capture_output=True, text=True, timeout=30, check=False)
    except (OSError, subprocess.SubprocessError) as error:
        raise ReleaseBlocked("could not verify live repository work items") from error
    # Do not echo API bodies or stderr: issue text and credentials do not belong
    # in a release log, and untrusted text may contain workflow commands.
    if result.returncode != 0:
        raise ReleaseBlocked("GitHub work-item query failed; publication is blocked")
    try:
        rows = json.loads(result.stdout)
    except (TypeError, ValueError) as error:
        raise ReleaseBlocked("GitHub returned an invalid work-item response") from error
    if not isinstance(rows, list) or len(rows) > 1:
        raise ReleaseBlocked("GitHub returned an invalid work-item collection")
    if not rows:
        return
    item = rows[0]
    if (
        not isinstance(item, dict)
        or type(item.get("number")) is not int
        or item["number"] <= 0
        or item.get("state") != "open"
        or ("pull_request" in item and not isinstance(item["pull_request"], dict))
    ):
        raise ReleaseBlocked("GitHub returned an invalid work item")
    kind = "pull request" if "pull_request" in item else "issue"
    raise ReleaseBlocked(
        f"open {kind} #{item['number']} blocks publication; "
        "finish verified remediation and review before releasing"
    )


def main():
    try:
        verify(os.environ.get("GITHUB_REPOSITORY"))
    except ReleaseBlocked as error:
        print(f"release-work-items: {error}", file=sys.stderr)
        return 1
    print("release-work-items: no open issues or pull requests")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
