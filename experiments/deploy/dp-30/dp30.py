#!/usr/bin/env python3
"""DP-30 bounded evidence tools. No deployment, fault injection, or remote CI.

Python standard library only. Missing measurements are unknown, never zero.
This verifier checks supplied observations; it does not authenticate exporters.
"""
from __future__ import annotations

import argparse
import collections
import hashlib
import json
import math
import os
from pathlib import Path
import re
import shutil
import sys
from typing import Any

BASE_SHA = "72667564c0431754d4856dc2e0db55f360bd2745"
MAX_FILE_BYTES = 16 * 1024 * 1024
MAX_LINE_BYTES = 64 * 1024
MAX_ROWS = 20000
FAULTS = (
    "new_start_failure", "slow_old_exit", "bad_config", "key_rotation",
    "node_partition", "database_unavailable", "disk_full", "controller_exit",
)
COMPONENTS = ("go", "rust", "config", "frontend")
SHA = re.compile(r"[0-9a-f]{40}\Z")
DIGEST = re.compile(r"[0-9a-f]{64}\Z")


class EvidenceError(ValueError):
    """Malformed, unsafe, or contradictory evidence."""


def _object(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    out: dict[str, Any] = {}
    for key, value in pairs:
        if key in out:
            raise EvidenceError(f"duplicate JSON key: {key}")
        out[key] = value
    return out


def _constant(value: str) -> None:
    raise EvidenceError(f"non-finite JSON number: {value}")


def loads(text: str) -> Any:
    try:
        return json.loads(text, object_pairs_hook=_object, parse_constant=_constant)
    except (ValueError, RecursionError) as exc:
        raise EvidenceError(str(exc)) from exc


def read_json(path: Path) -> dict[str, Any]:
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_FILE_BYTES:
        raise EvidenceError(f"invalid or oversized file: {path.name}")
    result = loads(path.read_text(encoding="utf-8"))
    if not isinstance(result, dict):
        raise EvidenceError(f"object required: {path.name}")
    return result


def read_rows(path: Path) -> list[dict[str, Any]] | None:
    if not path.exists():
        return None
    if path.is_symlink() or not path.is_file() or path.stat().st_size > MAX_FILE_BYTES:
        raise EvidenceError(f"invalid or oversized file: {path.name}")
    rows = []
    with path.open("rb") as stream:
        while line := stream.readline(MAX_LINE_BYTES + 1):
            if len(line) > MAX_LINE_BYTES or not line.endswith(b"\n"):
                raise EvidenceError(f"oversized or unterminated row: {path.name}")
            row = loads(line.decode("utf-8"))
            if not isinstance(row, dict):
                raise EvidenceError(f"object required: {path.name}")
            rows.append(row)
            if len(rows) > MAX_ROWS:
                raise EvidenceError(f"too many rows: {path.name}")
    return rows


def integer(value: Any, name: str, minimum: int | None = None) -> int:
    if type(value) is not int or (minimum is not None and value < minimum):
        raise EvidenceError(f"{name} must be an integer" + (f" >= {minimum}" if minimum is not None else ""))
    return value


def finite(value: Any, name: str, minimum: float = 0) -> float:
    if type(value) not in (int, float) or not math.isfinite(value) or value < minimum:
        raise EvidenceError(f"invalid {name}")
    return float(value)


def percentile(values: list[float], fraction: float) -> float | None:
    """Nearest-rank; the sample count is always reported alongside tails."""
    return sorted(values)[max(0, math.ceil(len(values) * fraction) - 1)] if values else None


def write_json(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2, allow_nan=False) + "\n", encoding="utf-8")


def provisional_plan() -> dict[str, Any]:
    # Not task 21's common workload. No capacity claim may use this placeholder.
    return {
        "schema_version": 1, "audited_base_sha": BASE_SHA, "integration_sha": None,
        "status": "unverified", "common_workload_origin": "DP30_PROVISIONAL_NOT_DP21",
        "dp21_workload_sha256": None, "dp21_budget_sha256": None,
        "limits": {"concurrency": 8, "max_concurrency": 16, "max_requests_per_run": 256,
                   "max_seconds_per_run": 60, "max_total_input_bytes": 4194304,
                   "max_response_bytes_per_request": 262144, "max_evidence_bytes_per_file": MAX_FILE_BYTES,
                   "max_evidence_bytes_per_run": 67108864, "max_live_versions_per_component": 2,
                   "max_queued_releases": 1, "max_fault_seconds": 5},
        "offered_load": {"requests_per_second": 1, "stream_seconds": 5,
                         "request_deadline_seconds": 12, "retry_count": 0},
        "safety": {"isolated_only": True, "production_allowed": False,
                   "real_money_allowed": False, "notifications_allowed": False,
                   "remote_ci_allowed": False, "force_old_exit_to_improve_frequency": False},
        "groups": [
            {"name": "no_update", "repeats": 3, "schedule_seconds": 20,
             "post_schedule_observation_seconds": 15, "scheduled_rounds": 0},
            {"name": "normal_update", "repeats": 3, "schedule_seconds": 20,
             "post_schedule_observation_seconds": 15,
             "rounds": [{"round": i + 1, "requested_offset_s": i,
                         "component": COMPONENTS[i % 4]} for i in range(20)]},
            *[{"name": name, "repeats": 3, "separate_from_normal_update": True,
               "inject_once": True, "max_fault_seconds": 5, "max_run_seconds": 60}
              for name in FAULTS],
            {"name": "forced_death", "repeats": 3, "separate_from_normal_update": True,
             "allow_interrupted_streams": True, "retry_count": 0, "max_run_seconds": 60},
        ],
        "execution_notes": [
            "20 rounds are requested inputs, not a production frequency promise.",
            "Keep the same offered arrival schedule in baseline and update groups.",
            "Record queue wait; when versions or queue hit the cap, stop admitting updates.",
            "Do not kill draining instances to make the schedule appear faster.",
            "No dp22-dp29 adapter is supplied here. This file does not deploy anything.",
        ],
    }


def verify(directory: Path) -> dict[str, Any]:
    """Reconcile an immutable per-run evidence directory; no network or writes."""
    known_files = ("manifest.json", "requests.jsonl", "upstream.jsonl", "ledger.jsonl", "tasks.jsonl",
                   "auth.jsonl", "releases.jsonl", "resources.jsonl", "faults.jsonl")
    if sum((directory / name).stat().st_size for name in known_files if (directory / name).is_file()) > 64 * 1024 * 1024:
        raise EvidenceError("total run evidence exceeds 64 MiB")
    manifest = read_json(directory / "manifest.json")
    checks: list[dict[str, Any]] = []
    metrics: dict[str, Any] = {}

    def check(name: str, state: str, detail: str) -> None:
        if state not in ("verified", "failed", "unverified"):
            raise AssertionError(state)
        checks.append({"name": name, "status": state, "detail": detail})

    def condition(name: str, ok: bool, detail: str) -> None:
        check(name, "verified" if ok else "failed", detail)

    def table(name: str) -> list[dict[str, Any]] | None:
        rows = read_rows(directory / (name + ".jsonl"))
        if not rows:
            check(name + "_evidence", "unverified", "missing or empty evidence")
            return None
        for row in rows:
            if row.get("run_id") != manifest.get("run_id"):
                raise EvidenceError(f"cross-run row in {name}")
        return rows

    if manifest.get("schema_version") != 1 or not isinstance(manifest.get("run_id"), str):
        raise EvidenceError("invalid manifest version or run_id")
    if not SHA.fullmatch(str(manifest.get("source_sha", ""))):
        raise EvidenceError("source_sha must be a full immutable SHA")
    scope = manifest.get("scope", "unknown")
    integration = scope == "application_integration"
    check("application_scope", "verified" if integration else "unverified",
          "actual application observations" if integration else "fixture or unspecified scope; not application acceptance")
    required = ("rust_model_path", "postgres_ledger", "isolated_network", "no_real_money",
                "no_notifications", "immutable_artifacts", "node_limits_observed")
    for feature in required:
        ok = manifest.get("capabilities", {}).get(feature) is True
        check(feature, "verified" if integration and ok else "unverified", "declared with exporter evidence required" if ok else "not established")
    for name in ("dp21_workload_sha256", "dp21_budget_sha256"):
        check(name, "verified" if DIGEST.fullmatch(str(manifest.get(name, ""))) else "unverified",
              "must match task 21 and paired control; a hash alone is not provenance")
    environment = manifest.get("environment", {})
    kind = environment.get("kind")
    if kind not in ("process_fixture", "container_simulation", "virtual_machines", "physical_nodes"):
        raise EvidenceError("unknown environment kind")
    nodes = environment.get("nodes", [])
    check("1c1g_budget", "verified" if integration and nodes and all(
        n.get("cpu_quota_cores") == 1 and n.get("memory_limit_bytes") == 1073741824
        and n.get("limit_evidence") for n in nodes) else "unverified",
        "per-node observed quota, not idle RSS or summed service limits")
    check("independent_nodes", "verified" if integration and kind in ("virtual_machines", "physical_nodes")
          and len(nodes) in (3, 5) and len({n.get("node_id") for n in nodes}) == len(nodes)
          and all(n.get("independent_host_evidence") for n in nodes) else "unverified",
          "same-host containers and a single node do not establish independent-node behavior")

    requests = table("requests")
    upstream = table("upstream")
    ledger = table("ledger")
    tasks = table("tasks")
    auth = table("auth")
    scenario = manifest.get("scenario")
    if scenario not in ("no_update", "normal_update", "forced_death", *FAULTS):
        raise EvidenceError("unknown scenario")
    expected_requests = integer(manifest.get("expected_requests"), "expected_requests", 1)
    if expected_requests > 256:
        raise EvidenceError("request count exceeds safety cap")
    metrics["expected_requests"] = expected_requests
    request_map: dict[str, dict[str, Any]] = {}
    if requests:
        for row in requests:
            rid = row.get("request_id")
            if not isinstance(rid, str) or not rid:
                raise EvidenceError("invalid request_id")
            if rid in request_map:
                check("unique_request_id", "failed", rid)
            request_map[rid] = row
        condition("request_coverage", len(requests) == expected_requests and len(request_map) == expected_requests,
                  f"expected={expected_requests}, rows={len(requests)}, unique={len(request_map)}")
        condition("no_automatic_replay", all(row.get("attempt") == 1 for row in requests), "one client attempt per logical request")
        successful = [r for r in requests if r.get("outcome") == "complete"]
        interrupted = [r for r in requests if r.get("outcome") != "complete"]
        metrics.update({"observed_requests": len(requests), "completed_requests": len(successful),
                        "interrupted_or_rejected_requests": len(interrupted),
                        "client_shed_requests": sum(r.get("outcome") == "client_shed" for r in requests)})
        if scenario in ("no_update", "normal_update"):
            condition("normal_stream_continuity", not interrupted, f"interrupted/rejected={len(interrupted)}; compare all repeats, never hide errors")
        else:
            check("fault_stream_interruptions", "verified", f"reported separately: {len(interrupted)}; not normal-update success")
        content_fields = ("sequence", "expected_sequence", "content_sha256", "expected_content_sha256", "terminal_complete")
        complete_evidence = all(all(k in r for k in content_fields) for r in successful)
        if not successful or not complete_evidence:
            check("stream_content", "unverified", "no successful stream or missing sequence/content/terminal evidence")
        else:
            condition("stream_content", all(r["terminal_complete"] is True and r["sequence"] == r["expected_sequence"]
                      and r["content_sha256"] == r["expected_content_sha256"] for r in successful),
                      "full terminal frame, exact ordered chunks and content digest")
        values = [finite(r["latency_ms"], "latency_ms") for r in requests if r.get("latency_ms") is not None]
        metrics.update({"latency_samples": len(values), "latency_p50_ms": percentile(values, .50),
                        "latency_p95_ms": percentile(values, .95), "latency_p99_ms": percentile(values, .99)})
        condition("latency_coverage", len(values) == len(requests), "failure and rejection latencies included")
        first = [finite(r["first_byte_ms"], "first_byte_ms") for r in requests if r.get("first_byte_ms") is not None]
        metrics.update({"first_byte_samples": len(first), "first_byte_p99_ms": percentile(first, .99)})
    if upstream and requests:
        calls: dict[str, int] = collections.Counter()
        attempt_ids = set()
        duplicate = False
        for row in upstream:
            rid = row.get("request_id")
            if rid not in request_map:
                check("upstream_orphan", "failed", str(rid))
            attempt = row.get("upstream_attempt_id")
            if not isinstance(attempt, str) or attempt in attempt_ids:
                duplicate = True
            attempt_ids.add(attempt)
            calls[rid] += 1
        condition("upstream_receipt_ids", not duplicate, "upstream observations require unique attempt IDs")
        if all(type(r.get("expected_upstream_calls")) is int for r in requests):
            condition("upstream_call_count", all(calls[rid] == r["expected_upstream_calls"] for rid, r in request_map.items()),
                      "compares all calls including failed client requests; no silent retry")
        else:
            check("upstream_call_count", "unverified", "missing expected call counts")
        metrics["upstream_calls"] = len(upstream)

    if ledger and requests:
        start = manifest.get("balances_before")
        end = manifest.get("balances_after")
        expected = manifest.get("expected_request_wallet_delta")
        if not isinstance(start, dict) or not isinstance(end, dict) or not isinstance(expected, dict):
            check("money_reconciliation", "unverified", "missing real snapshots or independent per-request charge expectations")
        else:
            balances = {a: integer(v, "initial balance") for a, v in start.items()}
            transactions: dict[str, int] = collections.Counter()
            account_entries: dict[str, list[dict[str, Any]]] = collections.defaultdict(list)
            actual: dict[str, int] = collections.Counter()
            ids = set()
            keys_ok = True
            for row in ledger:
                eid = row.get("entry_id")
                if not isinstance(eid, str) or eid in ids:
                    keys_ok = False
                ids.add(eid)
                account = row.get("account_id")
                delta = integer(row.get("delta_units"), "delta_units")
                txn = row.get("transaction_id")
                rid = row.get("request_id")
                if not isinstance(txn, str) or not txn or account not in balances or rid not in request_map:
                    raise EvidenceError("unknown ledger account, transaction, or request")
                balances[account] += delta
                account_entries[account].append(row)
                transactions[txn] += delta
                if account == manifest.get("wallet_account_id"):
                    actual[rid] += delta
            revision_before = manifest.get("account_revisions_before")
            revision_after = manifest.get("account_revisions_after")
            if not isinstance(revision_before, dict) or not isinstance(revision_after, dict) or set(revision_before) != set(start) or set(revision_after) != set(end) or not all("account_revision" in r and "balance_after_units" in r for r in ledger):
                check("ledger_account_revisions", "unverified", "export native per-account revisions and resulting balances, not journal order")
            else:
                revisions_ok = True
                for account in start:
                    revision = integer(revision_before[account], "opening revision", 0)
                    amount = integer(start[account], "opening balance")
                    ordered = sorted(account_entries[account], key=lambda r: integer(r["account_revision"], "entry revision", 1))
                    for entry in ordered:
                        revision += 1
                        amount += integer(entry["delta_units"], "entry amount")
                        revisions_ok &= entry["account_revision"] == revision and integer(entry["balance_after_units"], "entry balance") == amount
                    revisions_ok &= revision == integer(revision_after[account], "closing revision", 0)
                condition("ledger_account_revisions", revisions_ok, "no missing/duplicate account revisions; each intermediate balance reconciles")
            condition("ledger_unique_entries", keys_ok, "duplicate rows are not silently deduplicated")
            condition("double_entry_conservation", all(v == 0 for v in transactions.values()), "every recorded transaction balances in integer units")
            condition("balance_snapshots", set(balances) == set(end) and balances == {a: integer(v, "final balance") for a, v in end.items()}, "opening + every entry = closing for every account")
            condition("charge_expectation_coverage", set(expected) == set(request_map), "including interrupted and rejected requests")
            condition("exact_request_charges", all(actual[rid] == integer(v, "expected delta") for rid, v in expected.items()), "expectations must come from the price/usage oracle, not copies of ledger output")
            check("persistent_settlement", "verified" if integration and manifest.get("persistence_recovery_evidence") else "unverified",
                  "requires actual database recovery, reservation/outbox state and unique-command checks")
            metrics["ledger_rows"] = len(ledger)
    if tasks:
        expected_effects = manifest.get("expected_effects")
        if not isinstance(expected_effects, dict) or not expected_effects:
            check("task_side_effects", "unverified", "missing independently expected operations")
        else:
            ids = [r.get("effect_receipt_id") for r in tasks if r.get("state") == "effect_committed"]
            counts = collections.Counter(r.get("operation_id") for r in tasks if r.get("state") == "effect_committed")
            counts_ok = counts == {k: integer(v, "effect count", 0) for k, v in expected_effects.items() if v != 0}
            condition("task_side_effects", counts_ok and all(isinstance(v, str) and v for v in ids) and len(ids) == len(set(ids)), "unique committed effects, including orphan effects")
            pending = [r for r in tasks if r.get("state") not in ("effect_committed", "done", "cancelled")]
            condition("task_terminal_state", not pending, "adapter exports final task states and effect receipts; no unfinished task hidden")
    if auth:
        labels = {r.get("credential_label") for r in auth}
        required_labels = {"session", "api_key", "revoked_key"}
        condition("auth_coverage", required_labels <= labels, "include valid session/key and a revoked-key denial")
        condition("auth_results", all(type(r.get("expected_allowed")) is bool and r.get("allowed") == r["expected_allowed"] for r in auth), "no credential secrets may appear in evidence")
        required_phases = {"before", "after"}
        condition("auth_before_after", all(required_phases <= {r.get("phase") for r in auth if r.get("credential_label") == label} for label in required_labels), "same test credentials across change; revoked credentials remain denied")

    releases = table("releases") if scenario != "no_update" else read_rows(directory / "releases.jsonl")
    if scenario == "no_update":
        condition("no_update_control", not releases, "baseline must not contain release actions")
    elif releases:
        rounds = [integer(r.get("round"), "round", 1) for r in releases]
        condition("release_round_ids", len(rounds) == len(set(rounds)), "no duplicated or overwritten rounds")
        if scenario == "normal_update":
            condition("twenty_rounds", sorted(rounds) == list(range(1, 21)), "20 completed observations required, not 20 requested starts")
            condition("component_coverage", set(COMPONENTS) <= {r.get("component") for r in releases}, "Go, Rust, configuration and frontend")
        durations = []
        for row in releases:
            if row.get("clock") != "controller_monotonic":
                raise EvidenceError("release times must share one controller clock; no cross-host clock subtraction")
            stages = ("requested_ms", "download_start_ms", "download_end_ms", "ready_ms", "cutover_ms",
                      "old_stop_accept_ms", "drained_ms", "cleaned_ms")
            if not all(row.get(s) is not None for s in stages):
                check(f"round_{row['round']}_stages", "unverified", "missing stages; rejected startup must retain null cutover, not zero")
                continue
            times = [finite(row[s], s) for s in stages]
            condition(f"round_{row['round']}_order", times == sorted(times), "ready before cutover, stop admission before drain, clean only after drain")
            if scenario == "normal_update":
                condition(f"round_{row['round']}_not_forced", row.get("forced_kill") is False, "waiting is a measured result; no forced kill in a normal update")
            for key in ("artifact_digest", "config_digest", "frontend_digest"):
                if not DIGEST.fullmatch(str(row.get(key, ""))):
                    check(f"round_{row['round']}_{key}", "unverified", "immutable digest missing")
            durations.append({"round": row["round"], "component": row.get("component"),
                              "queue_wait_ms": times[1] - times[0], "download_ms": times[2] - times[1],
                              "ready_wait_ms": times[3] - times[2], "cutover_ms": times[4] - times[3],
                              "stop_admission_ms": times[5] - times[4], "drain_ms": times[6] - times[5],
                              "cleanup_ms": times[7] - times[6], "total_ms": times[7] - times[0]})
        metrics["release_timings"] = durations
        metrics["observed_release_rows"] = len(releases)

    resources = table("resources")
    if resources:
        required_metrics = ("memory_current_bytes", "rss_bytes", "virtual_bytes", "shared_bytes", "page_cache_bytes",
                            "cpu_usage_usec", "cpu_throttled_usec", "connections", "old_processes", "image_bytes",
                            "log_bytes", "event_backlog", "free_disk_bytes", "sample_elapsed_ms")
        if not all(all(type(r.get(k)) in (int, float) and r[k] >= 0 for k in required_metrics) for r in resources):
            check("resource_coverage", "unverified", "missing metrics must stay null, not synthetic zero")
        else:
            check("resource_coverage", "verified", "sampled values; not continuous true peaks")
            for key in required_metrics:
                vals = [finite(r[key], key) for r in resources]
                metrics[key + ("_min" if key == "free_disk_bytes" else "_sampled_max")] = min(vals) if key == "free_disk_bytes" else max(vals)
            expected_nodes = {n["node_id"] for n in nodes}
            condition("resource_node_coverage", bool(expected_nodes) and expected_nodes == {r.get("node_id") for r in resources}, "every node including database and overlap versions")
            if scenario == "normal_update":
                condition("resource_round_coverage", all(set(range(21)) <= {r.get("round") for r in resources if r.get("node_id") == n} for n in expected_nodes), "baseline slot and every round on every node")
            tails = [r for r in resources if r.get("phase") == "after_cleanup"]
            if tails and {r.get("node_id") for r in tails} == expected_nodes:
                condition("drain_and_backlog_cleanup", all(r["old_processes"] == 0 and r["event_backlog"] == 0 for r in tails), "final per-node inventories")
            else:
                check("drain_and_backlog_cleanup", "unverified", "missing after-cleanup inventory")
        # A finite run cannot prove indefinite boundedness without a retention/quota mechanism.
        check("disk_boundedness", "verified" if integration and manifest.get("disk_quota_evidence") and manifest.get("retention_recovery_evidence") else "unverified", "requires enforced quota plus cleanup/recovery tests, not only 20 flat samples")
        check("resource_stop_rules", "verified" if integration and manifest.get("stop_rule_evidence") else "unverified", "must prove admission stops on memory, CPU, disk, connections or queue cap")
    if scenario in (*FAULTS, "forced_death"):
        faults = table("faults")
        if faults:
            matching = [r for r in faults if r.get("fault") == scenario]
            condition("fault_case", len(matching) == 1, "one bounded injection per fault run")
            for row in matching:
                if row.get("clock") != "controller_monotonic":
                    raise EvidenceError("fault clock mismatch")
                if row.get("injected_ms") is None or row.get("service_recovered_ms") is None:
                    check("fault_recovery", "unverified", "recovery was not observed; report censored, never zero seconds")
                else:
                    start = finite(row["injected_ms"], "injected_ms")
                    finish = finite(row["service_recovered_ms"], "service_recovered_ms")
                    condition("fault_recovery_order", finish >= start, "recovery requires successful model, auth and ledger probes")
                    metrics["recovery_ms"] = finish - start
                check("fault_restored", "verified" if row.get("restored") is True else "unverified", "injection cleanup must finish even when the controller exits")
    check("paired_three_repeats", "unverified", "single-run verification cannot establish paired baseline plus three repeats; use a reviewed suite inventory")
    failed = sum(c["status"] == "failed" for c in checks)
    unknown = sum(c["status"] == "unverified" for c in checks)
    return {"schema_version": 1, "run_id": manifest["run_id"], "source_sha": manifest["source_sha"],
            "scope": scope, "environment_kind": kind, "scenario": scenario,
            "status": "failed" if failed else "unverified" if unknown else "verified",
            "failed_checks": failed, "unverified_checks": unknown,
            "application_capacity_claim_allowed": False,
            "metrics": metrics, "checks": checks}


def environment() -> dict[str, Any]:
    def read(path: str) -> str | None:
        try:
            return Path(path).read_text().strip()
        except OSError:
            return None
    return {"schema_version": 1, "scope": "read_only_local_preflight", "source_sha": BASE_SHA,
            "tools": {name: shutil.which(name) for name in ("docker", "podman", "cargo", "rustc", "go", "psql")},
            "memory_max": read("/sys/fs/cgroup/memory.max"), "cpu_max": read("/sys/fs/cgroup/cpu.max"),
            "cgroup_writable": os.access("/sys/fs/cgroup", os.W_OK),
            "integration_status": "unverified", "deployments_started": 0}


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("plan")
    sub.add_parser("preflight")
    verify_parser = sub.add_parser("verify")
    verify_parser.add_argument("directory", type=Path)
    args = parser.parse_args()
    try:
        if args.command == "plan":
            result = provisional_plan()
        elif args.command == "preflight":
            result = environment()
        else:
            result = verify(args.directory)
        print(json.dumps(result, ensure_ascii=False, indent=2, allow_nan=False))
        if args.command == "verify":
            return {"verified": 0, "failed": 1, "unverified": 2}[result["status"]]
        return 0
    except (OSError, UnicodeError, EvidenceError, KeyError, TypeError) as exc:
        print(json.dumps({"status": "failed", "error": str(exc)}, ensure_ascii=False))
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
