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
    "red_packet_schema": re.compile(r"red packet schema verification failed: missing (?:table )?(?:red_packets|red_packet_items|red_packet_claims)(?:\.|\s|\]|$)"),
    "resource_initialization": re.compile(r"failed to initialize resources:"),
    "route_configuration": re.compile(r"failed to configure routes:"),
    "route_oauth_configuration": re.compile(r"failed to configure routes: configure OAuth server:"),
    "route_oauth_initialization": re.compile(r"failed to configure routes: initialize OAuth server:"),
    "route_red_packet_configuration": re.compile(r"failed to configure routes: configure red packet routes:"),
    "red_packet_apply_failure": re.compile(r"configure red packet routes: migrate red packet schema:"),
    "red_packet_mode_invalid": re.compile(r"configure red packet routes: LMM_DB_MIGRATION_MODE must be exactly apply or verify"),
    "red_packet_mode_unsupported": re.compile(r"configure red packet routes: unsupported red packet migration mode"),
    "route_packaged_frontend": re.compile(r"failed to configure routes: configure packaged frontend:"),
    "route_frontend_exclusive_settings": re.compile(r"failed to configure routes: LMM_API_FRONTEND_DIR and FRONTEND_BASE_URL are mutually exclusive"),
    "oauth_enabled_invalid": re.compile(r"configure OAuth server: OAUTH_SERVER_ENABLED must be true or false"),
    "oauth_groups_invalid_json": re.compile(r"configure OAuth server: OAUTH_SERVER_GROUPS must be an explicit JSON string array"),
    "oauth_proxy_invalid": re.compile(r"configure OAuth server: invalid OAuth proxy trust configuration"),
    "oauth_groups_empty": re.compile(r"initialize OAuth server: OAuth needs an explicit enabled group allowlist"),
    "oauth_groups_duplicate_or_invalid": re.compile(r"initialize OAuth server: invalid or repeated OAuth group"),
    "oauth_issuer_invalid": re.compile(r"initialize OAuth server: oauth server: issuer must be a canonical HTTPS DNS origin"),
    "oauth_writer_invalid": re.compile(r"initialize OAuth server: oauth server: a (?:healthy writer database and policy|root, non-dry-run writer handle)"),
    "oauth_scopes_invalid": re.compile(r"initialize OAuth server: oauth server: invalid registered scopes"),
    "frontend_path_not_absolute": re.compile(r"configure packaged frontend: LMM_API_FRONTEND_DIR must be an absolute path"),
    "frontend_path_resolve_failure": re.compile(r"configure packaged frontend: resolve frontend directory:"),
    "frontend_current_link_invalid": re.compile(r"configure packaged frontend: LMM_API_FRONTEND_DIR symlink must be an atomic current link"),
    "frontend_nested_symlink": re.compile(r"configure packaged frontend: frontend directory contains a symlink:"),
    "frontend_unsupported_file": re.compile(r"configure packaged frontend: frontend directory contains an unsupported file:"),
    "frontend_index_unavailable": re.compile(r"configure packaged frontend: frontend index is unavailable:"),
    "frontend_index_not_regular": re.compile(r"configure packaged frontend: frontend index is not a regular file"),
    "route_filesystem_permission": re.compile(r"failed to configure routes:.*permission denied"),
    "route_filesystem_missing": re.compile(r"failed to configure routes:.*no such file or directory"),
    "route_sql_table_missing": re.compile(r"failed to configure routes:.*SQLSTATE 42P01"),
    "route_sql_column_missing": re.compile(r"failed to configure routes:.*SQLSTATE 42703"),
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
        text = message[:16384]
        for label, pattern in PATTERNS.items():
            if pattern.search(text):
                counts[label] += 1
        for match in re.finditer(r"red packet schema verification failed: missing table ([a-z_]+)(?:\s|\]|$)", text):
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


STATE_KEYS = ("MainPID", "ExecMainPID", "ExecMainCode", "ExecMainStatus", "ActiveState",
              "SubState", "Result", "ControlGroup", "Restart", "InvocationID")


def summarize_unit_state(text):
    """Only fixed property names, counters and whitelisted states can be published."""
    if len(text) > 16384:
        return {"oversized": True}
    fields = {}
    malformed = False
    for line in text.splitlines():
        key, separator, value = line.partition("=")
        if not separator or key in fields:
            malformed = True
            continue
        if key in STATE_KEYS:
            fields[key] = value
    result = {"missing_properties": [key for key in STATE_KEYS if key not in fields],
              "malformed": malformed,
              "control_group_empty": fields.get("ControlGroup") == "",
              "invocation_present": bool(fields.get("InvocationID"))}
    for key in ("MainPID", "ExecMainPID", "ExecMainCode", "ExecMainStatus"):
        value = fields.get(key, "")
        if re.fullmatch(r"[0-9]{1,10}", value):
            result[key] = int(value)
    choices = {"ActiveState": {"active", "activating", "deactivating", "inactive", "failed"},
               "SubState": {"running", "start", "start-pre", "start-post", "auto-restart",
                            "stop", "stop-sigterm", "stop-post", "dead", "failed"},
               "Result": {"success", "exit-code", "signal", "timeout", "core-dump", "resources"},
               "Restart": {"no", "always", "on-failure", "on-abnormal", "on-success", "on-abort", "on-watchdog"}}
    for key, allowed in choices.items():
        result[key] = fields.get(key) if fields.get(key) in allowed else "other"
    return result


def inspect_unit_property_presence():
    reports = {}
    for label, extra in (("default", []), ("all_properties", ["--all"])):
        try:
            completed = subprocess.run(
                ["systemctl", "show", "lmm-api.service", "--no-pager", *extra,
                 "--property=" + ",".join(STATE_KEYS)], stdout=subprocess.PIPE,
                stderr=subprocess.DEVNULL, text=True, timeout=8, check=False)
            reports[label] = {"exit_code": completed.returncode,
                              **summarize_unit_state(completed.stdout)}
        except (OSError, subprocess.TimeoutExpired):
            reports[label] = {"read_failed": True}
    # Sequential snapshots are not an atomic proof that a writer cannot start.
    return reports


def main(report_path):
    report = Path(report_path)
    if not report.is_file() or report.is_symlink():
        raise ValueError("report_file_required")
    raw = report.parent / "startup-journal.jsonl"
    descriptor = os.open(raw, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
    result = {"operation_kind": "read_only_diagnosis", "deployment_id": DEPLOYMENT,
              "at": datetime.datetime.now(datetime.timezone.utc).isoformat(),
              "sources": {}, "production_changed": False}
    result["unit_property_snapshots"] = inspect_unit_property_presence()
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
