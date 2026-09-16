#!/usr/bin/env bash
# Read-only incident diagnosis. Raw journal lines remain in the private ops audit.
# Does not migrate, restart, change admission, clear locks, or confirm a deploy.
set -euo pipefail
set +x
: "${LMM_OPS_REPORT:?Run through the owner-authorized server-ops workflow}"
python3 - "$LMM_OPS_REPORT" <<'PY'
import collections
import datetime
import json
import os
from pathlib import Path
import re
import stat
import subprocess
import sys

DEPLOYMENT = "release-go-v0.2.51-35116594330-attempt-1"
LIMIT = 1048576
TABLES = ("red_packets", "red_packet_items", "red_packet_claims")
PHASES = {"ROLLBACK_REQUIRED", "ROLLING_BACK", "ROLLED_BACK", "CONFIRMED",
          "AWAITING_CONFIRMATION", "OBSERVING", "DEPLOYING_GO", "DEPLOYING_WEB"}
# Report known diagnostic labels only. Never copy log text or SQL into Actions.
PATTERNS = {
    "red_packet_schema": re.compile(r"red packet schema verification failed: missing (?:table )?(?:red_packets|red_packet_items|red_packet_claims)(?:\.|\s|$)"),
    "resource_initialization": re.compile(r"failed to initialize resources:"),
    "route_configuration": re.compile(r"failed to configure routes:"),
    "log_file_open": re.compile(r"initialize logger: open log file:"),
    "http_bind": re.compile(r"failed to start HTTP server:.*(?:bind:|address already in use)"),
    "migration_verification": re.compile(r"database (?:schema|migration) verification failed"),
    "shutdown_failure": re.compile(r"shutdown failed:"),
    "unclassified_connection_message": re.compile(r"failed to connect", re.I),
}


def classify(messages):
    counts = collections.Counter()
    tables = set()
    scanned = 0
    for message in messages:
        if not isinstance(message, str):
            continue
        scanned += 1
        # A classifier is evidence, not authorization for any corrective action.
        text = message[:16384]
        for label, pattern in PATTERNS.items():
            if pattern.search(text):
                counts[label] += 1
        for match in re.finditer(r"red packet schema verification failed: missing table ([a-z_]+)(?:\s|$)", text):
            if match.group(1) in TABLES:
                tables.add(match.group(1))
    return {"messages_scanned": scanned, "categories": dict(sorted(counts.items())),
            "missing_red_packet_tables": sorted(tables)}


def bounded_tail(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, "rb") as source:
        info = os.fstat(source.fileno())
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("not_regular")
        start = max(0, info.st_size - LIMIT)
        source.seek(start)
        data = source.read(LIMIT)
        if start:
            data = data.partition(b"\n")[2]
        return data.decode("utf-8", errors="replace").splitlines(), start > 0


def main(report_path):
    report = Path(report_path)
    if not report.is_file() or report.is_symlink():
        raise ValueError("report_file_required")
    raw = report.parent / "startup-journal.jsonl"
    descriptor = os.open(raw, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    result = {"operation_kind": "read_only_diagnosis", "deployment_id": DEPLOYMENT,
              "at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "sources": {}, "production_changed": False}
    messages = []
    with os.fdopen(descriptor, "wb") as output:
        try:
            process = subprocess.run(
                ["journalctl", "--unit=lmm-api.service", "--since=2026-09-16 15:37:00 UTC",
                 "--lines=1000", "--output=json", "--no-pager"],
                stdout=output, stderr=subprocess.DEVNULL, timeout=20, check=False)
            result["sources"]["journal_exit_code"] = process.returncode
        except subprocess.TimeoutExpired:
            result["sources"]["journal_timed_out"] = True
        except OSError:
            result["sources"]["journal_unavailable"] = True
    lines, truncated = bounded_tail(raw)
    result["sources"]["journal_truncated"] = truncated
    for line in lines:
        try:
            entry = json.loads(line)
            if isinstance(entry, dict) and entry.get("_SYSTEMD_UNIT") == "lmm-api.service":
                messages.append(entry.get("MESSAGE"))
        except ValueError:
            pass
    # The packaged service's working directory and default logger location.
    # A custom log location is deliberately not followed or published.
    log_root = Path("/var/lib/lmm-api-go/logs")
    candidates = []
    try:
        for index, entry in enumerate(log_root.iterdir()):
            if index >= 2048:
                result["sources"]["log_directory_truncated"] = True
                break
            if re.fullmatch(r"oneapi-[0-9]{14}\.log", entry.name) and not entry.is_symlink():
                info = entry.stat()
                if stat.S_ISREG(info.st_mode):
                    candidates.append((info.st_mtime, entry))
        for _, path in sorted(candidates, reverse=True)[:6]:
            try:
                lines, _ = bounded_tail(path)
                messages.extend(lines)
            except (OSError, ValueError):
                result["sources"]["log_file_unreadable"] = True
    except OSError:
        result["sources"]["default_log_directory_unavailable"] = True
    result["sources"]["default_log_files_scanned"] = min(len(candidates), 6)
    result.update(classify(messages))
    state = Path("/var/lib/lmm-api-go-deploy/work") / DEPLOYMENT / "state/status.json"
    try:
        lines, truncated = bounded_tail(state)
        if truncated:
            raise ValueError("oversized_state")
        status = json.loads("\n".join(lines))
        phase = status.get("phase")
        result["transaction_phase"] = phase if phase in PHASES else "other"
        result["transaction_failure_categories"] = classify([status.get("failure")])["categories"]
    except (OSError, ValueError, AttributeError, TypeError):
        result["sources"]["transaction_state_unavailable"] = True
    result["interpretation"] = "Historical diagnostic evidence only; confirm current schema before any repair. Connection messages alone do not establish a database fault."
    report.write_text(json.dumps(result, sort_keys=True, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main(sys.argv[1])
PY
