#!/usr/bin/env python3
"""Offline connection-budget gate. It never connects to a database or Docker.

Numbers are configured upper bounds, not measured open connections or capacity.
Every physical pool appears exactly once. Cloned handles share a consumers list.
"""
from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any

BASE = "72667564c0431754d4856dc2e0db55f360bd2745"


def integer(value: Any, name: str, low: int = 0, high: int = 100_000) -> int:
    if type(value) is not int or not low <= value <= high:
        raise ValueError(f"{name} must be an integer in [{low}, {high}]")
    return value


def current_plan(nodes: int, surge: int, admin_processes: int = 0) -> dict:
    integer(nodes, "nodes", 1, 100)
    integer(surge, "surge", 0, 100)
    integer(admin_processes, "admin_processes", 0, 10)
    return {
        "base_commit": BASE,
        "scope": "enabled identity host only; NOT the completed business stack",
        "max_connections": 32,
        # These are proposed protected reserves, not claims about the old Compose.
        "reserves": {"superuser": 3, "recovery": 2, "operations": 1},
        "role_isolation_verified": False,
        "pools": [
            {"id": "core-http", "kind": "direct", "instances": nodes,
             "surge": surge, "max_connections": 8,
             "consumers": ["identity HTTP", "account administration through HTTP"]},
            {"id": "core-rpc", "kind": "direct", "instances": nodes,
             "surge": surge, "max_connections": 2,
             "consumers": ["identity RPC"]},
            {"id": "offline-admin", "kind": "direct", "instances": admin_processes,
             "surge": 0, "max_connections": 8,
             "consumers": ["init-db", "bootstrap-user (sequential, not two pools)"]},
        ],
        "unwired": [
            "ledger and charging: externally supplied PgPool, no production host allocation",
            "events: connector max=2 exists; main does not attach EventStore",
            "Go: current host registers identity RPC only; business database pools not assembled",
        ],
    }


def evaluate(plan: dict) -> dict:
    if not isinstance(plan, dict):
        raise ValueError("plan must be an object")
    limit = integer(plan.get("max_connections"), "max_connections", 1)
    reserves = plan.get("reserves")
    if not isinstance(reserves, dict) or set(reserves) != {"superuser", "recovery", "operations"}:
        raise ValueError("reserves must specify superuser, recovery and operations")
    reserve = sum(integer(v, f"reserves.{k}") for k, v in reserves.items())
    if reserve >= limit:
        raise ValueError("reserves leave no application connection slots")
    if type(plan.get("role_isolation_verified")) is not bool:
        raise ValueError("role_isolation_verified must be a boolean")
    pools = plan.get("pools")
    if not isinstance(pools, list):
        raise ValueError("pools must be a list")
    seen: set[str] = set()
    backend_keys: set[tuple] = set()
    rows, unknown = [], []
    normal_sum = peak_sum = 0
    for p in pools:
        if not isinstance(p, dict) or not isinstance(p.get("id"), str) or not p["id"]:
            raise ValueError("each physical pool needs a nonempty id")
        if p["id"] in seen:
            raise ValueError(f"duplicate physical pool: {p['id']}")
        seen.add(p["id"])
        kind = p.get("kind")
        if kind not in {"direct", "pgbouncer_backend"}:
            raise ValueError("count direct pools or explicit PgBouncer backend pools only")
        live = integer(p.get("instances"), p["id"] + ".instances")
        surge = integer(p.get("surge"), p["id"] + ".surge")
        extra = 0
        if kind == "pgbouncer_backend":
            key = tuple(p.get(k) for k in ("proxy", "database", "user"))
            if not all(isinstance(k, str) and k for k in key):
                raise ValueError("PgBouncer pools require proxy, database and user")
            if key in backend_keys:
                raise ValueError("duplicate proxy/database/user backend allocation")
            backend_keys.add(key)
            extra = integer(p.get("reserve_pool_size"), p["id"] + ".reserve_pool_size")
        maximum = p.get("max_connections")
        if maximum is None and live + surge:
            unknown.append(p["id"])
            rows.append({"id": p["id"], "steady": None, "peak": None})
            continue
        cap = 0 if maximum is None else integer(maximum, p["id"] + ".max_connections", 1)
        steady, peak = (cap + extra) * live, (cap + extra) * (live + surge)
        normal_sum += steady
        peak_sum += peak
        rows.append({"id": p["id"], "steady": steady, "peak": peak})
    within = not unknown and peak_sum + reserve <= limit
    reasons = []
    if unknown:
        reasons.append("unknown pool caps: " + ", ".join(unknown))
    if not unknown and not within:
        reasons.append("peak physical connections plus reserves exceed max_connections")
    if not plan["role_isolation_verified"]:
        reasons.append("runtime role separation and reserved-slot protection are not verified")
    return {
        "measurement_kind": "configured_upper_bound_not_observation",
        "scope": plan.get("scope", "explicit supplied plan"),
        "base_commit": plan.get("base_commit"),
        "max_connections": limit, "protected_reserves": reserve,
        "application_budget": limit - reserve,
        "steady_application_connections": None if unknown else normal_sum,
        "peak_application_connections": None if unknown else peak_sum,
        "peak_with_reserves": None if unknown else peak_sum + reserve,
        "peak_headroom": None if unknown else limit - reserve - peak_sum,
        "known_peak_lower_bound": peak_sum,
        "within_connection_budget": within,
        "gate": "pass" if not reasons else "blocked",
        "reasons": reasons, "pools": rows,
        "business_capacity_accepted": False,
    }


def operator_memory_mib(work_mem: int, sessions: int, sorts: int, hashes: int,
                        hash_multiplier: int, parallel_workers: int = 0) -> int:
    """Conservative operator allowance, NOT PostgreSQL's total-memory bound.

Each session has sorts + hashes; count the parallel leader and workers. Add
backend private memory, shared memory, vacuum, WAL, cache, and probes separately.
"""
    for name, value in locals().copy().items():
        integer(value, name, 0 if name in {"sorts", "hashes", "parallel_workers"} else 1)
    return work_mem * sessions * (sorts + hashes * hash_multiplier) * (parallel_workers + 1)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--plan", type=Path)
    parser.add_argument("--nodes", type=int, default=1)
    parser.add_argument("--surge", type=int, default=1,
                        help="additional old/new core processes alive at the same time")
    parser.add_argument("--admin-processes", type=int, default=0)
    args = parser.parse_args()
    try:
        plan = json.loads(args.plan.read_text()) if args.plan else current_plan(
            args.nodes, args.surge, args.admin_processes)
        result = evaluate(plan)
    except (ValueError, OSError) as exc:
        print(json.dumps({"gate": "invalid", "error": str(exc)}))
        return 2
    print(json.dumps(result, indent=2))
    return 0 if result["gate"] == "pass" else 2


if __name__ == "__main__":
    raise SystemExit(main())
