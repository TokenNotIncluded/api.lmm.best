#!/usr/bin/env python3
"""Compare the live Go inventory with Rust source and listener ledgers.

This is a source/mount audit, not a behavioral-parity certification. The frozen
353-route contract remains immutable. New Go routes must not disappear merely
because the frozen contract's completion gate is green.
"""

from __future__ import annotations

import argparse
from collections import Counter
import csv
from datetime import datetime, timezone
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile


ROOT = Path(__file__).resolve().parents[4]
SCRIPTS = ROOT / "apps/api-rust/tests/scripts"


def route_map(rows: list[dict], label: str) -> dict[tuple[str, str], dict]:
    result = {}
    for row in rows:
        key = (row.get("method"), row.get("path"))
        if not all(isinstance(value, str) and value for value in key):
            raise ValueError(f"{label}: invalid method/path")
        if key in result:
            raise ValueError(f"{label}: duplicate route {key[0]} {key[1]}")
        result[key] = row
    return result


def read_jsonl(path: Path) -> list[dict]:
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def read_go_manifest(path: Path) -> list[dict]:
    rows = []
    with path.open(newline="") as handle:
        for fields in csv.reader(handle, delimiter="\t"):
            if len(fields) != 3 or not all(fields):
                raise ValueError("Go manifest must contain three nonempty TSV fields per row")
            rows.append(dict(zip(("method", "path", "go_handler"), fields)))
    if not rows:
        raise ValueError("Go manifest is empty")
    return rows


def group(path: str) -> str:
    if path.startswith(("/api/acquisition", "/api/admin/acquisition")):
        return "acquisition"
    if path.startswith(("/api/scripts", "/scripts/")):
        return "scripts"
    if path.startswith(("/api/red-packet", "/red-packet-cover/")):
        return "red-packet"
    if path.startswith(("/api/hero-sms", "/api/option/hero-sms")):
        return "hero-sms"
    if path.startswith("/api/"):
        return path.split("/")[2]
    return path.split("/")[1]


def analyze(go_rows: list[dict], coverage_rows: list[dict], source_rows: list[dict]) -> dict:
    go = route_map(go_rows, "Go inventory")
    coverage = route_map(
        [row for row in coverage_rows if row.get("record_type") == "route"],
        "coverage inventory",
    )
    source = route_map(source_rows, "Rust source inventory")
    if go.keys() != coverage.keys():
        raise ValueError("coverage inventory does not match this Go manifest")

    routes = []
    for key, go_row in sorted(go.items(), key=lambda item: (item[0][1], item[0][0])):
        row = coverage[key]
        if go_row["go_handler"] != row.get("go_handler"):
            raise ValueError(f"Go handler drift for {key[0]} {key[1]}")
        declaration = source.get(key)
        # Test-only declarations cannot satisfy the current application surface.
        if declaration and declaration.get("category") in ("test", "fixture"):
            declaration = None
        legacy_stub = row["class"] == "legacy-501"
        if legacy_stub and not go_row["go_handler"].endswith(".RelayNotImplemented"):
            raise ValueError(f"current Go no longer has a legacy 501 for {key[0]} {key[1]}")
        normal_mounted = row["rust_normal"] == "mounted"
        shell = row["class"] == "mounted-fail-closed-shell"
        source_placeholder = bool(
            declaration and declaration.get("placeholder") and not legacy_stub
        )
        source_missing = declaration is None
        if source_missing:
            state = "source-absent"
        elif source_placeholder:
            state = "source-placeholder"
        elif shell:
            state = "mounted-fail-closed-shell"
        elif not normal_mounted:
            state = "normal-mount-absent"
        elif legacy_stub:
            state = "go-legacy-501"
        else:
            state = "mounted-candidate-unverified"
        routes.append({
            **row,
            "group": group(key[1]),
            "source": declaration.get("source") if declaration else None,
            "source_handler": declaration.get("handler") if declaration else None,
            "source_missing": source_missing,
            "source_placeholder": source_placeholder,
            "normal_mounted": normal_mounted,
            "audit_state": state,
            "ledger_missing_with_source": row["class"] == "static-only" and not source_missing,
            "ledger_claim_without_source": source_missing and (
                row["class"] == "differential-candidate" or normal_mounted
            ),
        })

    count = lambda predicate: sum(bool(predicate(row)) for row in routes)
    missing_by_group = Counter(row["group"] for row in routes if row["source_missing"])
    unmounted_by_group = Counter(row["group"] for row in routes if not row["normal_mounted"])
    summary = {
        "go_routes": len(routes),
        "outside_frozen_baseline": count(lambda r: not r["evidence"]["frozen_go"]),
        "source_present": count(lambda r: not r["source_missing"]),
        "source_missing": count(lambda r: r["source_missing"]),
        "source_placeholders": count(lambda r: r["source_placeholder"]),
        "normal_mounted": count(lambda r: r["normal_mounted"]),
        "normal_mount_missing": count(lambda r: not r["normal_mounted"]),
        "mounted_fail_closed_shells": count(lambda r: r["class"] == "mounted-fail-closed-shell"),
        "ledger_missing_with_source": count(lambda r: r["ledger_missing_with_source"]),
        "ledger_claim_without_source": count(lambda r: r["ledger_claim_without_source"]),
        "class_counts": dict(sorted(Counter(row["class"] for row in routes).items())),
        "missing_source_by_group": dict(sorted(missing_by_group.items())),
        "missing_normal_mount_by_group": dict(sorted(unmounted_by_group.items())),
        "behavioral_parity_verified": False,
        "note": "Source declarations and mount ledgers do not prove listener reachability, adapter capability, side effects, or Go/Rust behavior.",
    }
    return {"summary": summary, "routes": routes}


def requirement_met(report: dict, requirement: str) -> bool:
    summary = report["summary"]
    source_ok = not any(summary[key] for key in (
        "source_missing", "source_placeholders", "ledger_claim_without_source",
    ))
    if requirement == "source":
        return source_ok
    if requirement == "mounted":
        return source_ok and summary["normal_mount_missing"] == 0
    return True


def run_checked(command: list[str], log: Path, env: dict | None = None, cwd: Path = ROOT) -> None:
    with log.open("w") as handle:
        result = subprocess.run(command, cwd=cwd, env=env, stdout=handle, stderr=subprocess.STDOUT)
    if result.returncode:
        raise ValueError(f"command failed ({result.returncode}); see {log}: {' '.join(command)}")


def render_markdown(report: dict) -> str:
    summary = report["summary"]
    lines = [
        "# Current Go / Rust route audit", "",
        f"Go revision: `{report['go_revision']}`; generated `{report['generated_at']}`.", "",
        "This report checks source declarations and checked-in mount ledgers. It does not certify behavioral parity.", "",
        "| Measure | Count |", "| --- | ---: |",
    ]
    for key in ("go_routes", "outside_frozen_baseline", "source_present", "source_missing", "normal_mounted", "normal_mount_missing", "mounted_fail_closed_shells", "ledger_missing_with_source", "ledger_claim_without_source"):
        lines.append(f"| {key} | {summary[key]} |")
    lines.extend(["", "## Missing source declarations", "", "| Group | Routes |", "| --- | ---: |"])
    lines.extend(f"| {name} | {value} |" for name, value in summary["missing_source_by_group"].items())
    lines.extend(["", "## Routes needing source, mount, or ledger work", "", "| Method | Path | State | Source |", "| --- | --- | --- | --- |"])
    for row in report["routes"]:
        if row["normal_mounted"] and not row["ledger_missing_with_source"] and not row["ledger_claim_without_source"]:
            continue
        lines.append(f"| {row['method']} | `{row['path']}` | {row['audit_state']} | {row['source'] or 'absent'} |")
    return "\n".join(lines) + "\n"


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, help="current Go cmd/route-manifest output; generated when omitted")
    parser.add_argument("--report-dir", type=Path, help="persist JSON, Markdown, manifests and checker logs here")
    parser.add_argument("--require", choices=("report", "source", "mounted"), default="report", help="fail when the selected static requirement is incomplete; never claims behavioral parity")
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix="lmm-current-route-audit-") as temporary:
        output = args.report_dir.resolve() if args.report_dir else Path(temporary)
        output.mkdir(parents=True, exist_ok=True)
        manifest = output / "current-go-routes.tsv"
        if args.manifest:
            contents = args.manifest.read_bytes()
            manifest.write_bytes(contents)
        else:
            env = {**os.environ, "GIN_MODE": "release"}
            with manifest.open("w") as handle:
                result = subprocess.run(["go", "run", "./cmd/route-manifest"], cwd=ROOT / "apps/api-go", env=env, stdout=handle)
            if result.returncode:
                raise ValueError("Go route manifest command failed")
        source_inventory = output / "rust-source-routes.jsonl"
        run_checked(
            ["bash", str(SCRIPTS / "check-draft-route-coverage.sh")],
            output / "draft-route-coverage.log",
            {**os.environ, "DRAFT_SOURCE_INVENTORY_PATH": str(source_inventory)},
        )
        coverage = output / "current-route-coverage.jsonl"
        run_checked(
            ["bash", str(SCRIPTS / "check-complete-route-coverage.sh"), "--manifest", str(manifest)],
            coverage,
        )
        report = analyze(read_go_manifest(manifest), read_jsonl(coverage), read_jsonl(source_inventory))
        report["generated_at"] = datetime.now(timezone.utc).isoformat()
        report["go_revision"] = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip()
        report["requirement"] = args.require
        report["requirement_met"] = requirement_met(report, args.require)
        (output / "current-route-audit.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n")
        (output / "current-route-audit.md").write_text(render_markdown(report))
        print(json.dumps({"summary": report["summary"], "requirement": args.require, "requirement_met": report["requirement_met"]}, ensure_ascii=False, indent=2))
        if args.report_dir:
            print(f"audit artifacts: {output}", file=sys.stderr)
        if not report["requirement_met"]:
            print(f"current route {args.require} requirement is incomplete", file=sys.stderr)
            return 1
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, json.JSONDecodeError, subprocess.CalledProcessError) as error:
        print(f"current route audit: {error}", file=sys.stderr)
        sys.exit(2)
