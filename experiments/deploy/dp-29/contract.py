"""DP-29 control contract. No service or business database imports."""
from __future__ import annotations

import hashlib
import itertools
import json
import re
from typing import Any

API = "dp29/v1"
COMPONENTS = {"rust-core", "go-extensions", "web"}
SHA = re.compile(r"sha256:[0-9a-f]{64}\Z")
IDENT = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_.-]{0,79}\Z")
MAX_JSON = 65536


class Rejected(Exception):
    """No permission to advance this release."""


class Unavailable(Exception):
    """The result may be unknown. Keep the operation ID for reconciliation."""


def canonical(value: Any) -> str:
    return json.dumps(value, sort_keys=True, separators=(",", ":"), allow_nan=False)


def digest(value: Any) -> str:
    return "sha256:" + hashlib.sha256(canonical(value).encode()).hexdigest()


def require(condition: bool, message: str) -> None:
    if not condition:
        raise Rejected(message)


def integer(value: Any, minimum: int = 0, maximum: int = 2**53) -> bool:
    return type(value) is int and minimum <= value <= maximum


def in_range(value: int, bounds: list[int]) -> bool:
    return bounds[0] <= value <= bounds[1]


def range_ok(bounds: Any) -> bool:
    return (isinstance(bounds, list) and len(bounds) == 2
            and all(integer(x, 1) for x in bounds) and bounds[0] <= bounds[1])


def strings(value: Any) -> bool:
    return (isinstance(value, list) and len(value) <= 64
            and all(isinstance(x, str) and IDENT.fullmatch(x) for x in value)
            and len(value) == len(set(value)))


def manifest(value: dict) -> dict:
    """Unknown optional top-level fields are ignored, critical fields are not."""
    require(isinstance(value, dict), "manifest must be an object")
    try:
        require(len(canonical(value).encode()) <= MAX_JSON, "manifest is too large")
        require(value["api"] == API, "unsupported manifest API")
        known = {"api", "component", "version", "artifact", "config", "protocol", "schema",
                 "provides", "requires", "understood_states", "web", "critical_fields"}
        require(strings(value.get("critical_fields", [])), "invalid critical_fields")
        require(set(value.get("critical_fields", [])) <= known, "unknown critical field")
        require(value["component"] in COMPONENTS, "unknown component")
        require(isinstance(value["version"], str) and IDENT.fullmatch(value["version"]),
                "invalid release version")
        a = value["artifact"]
        require(a["platform"] in {"linux/amd64", "linux/arm64"}, "unsupported architecture")
        require(isinstance(a["digest"], str) and bool(SHA.fullmatch(a["digest"])), "invalid digest")
        require(isinstance(a["image"], str) and len(a["image"]) <= 512
                and a["image"].endswith("@" + a["digest"])
                and re.fullmatch(r"[a-zA-Z0-9./:_-]+@sha256:[0-9a-f]{64}", a["image"]),
                "image must be pinned by SHA-256, not only by a mutable tag")
        for key in ("download_bytes", "unpacked_bytes", "peak_memory_bytes"):
            require(integer(a[key], 1), "invalid artifact budget: " + key)
        c, p, s = value["config"], value["protocol"], value["schema"]
        require(integer(c["version"], 1) and range_ok(c["accepts"])
                and in_range(c["version"], c["accepts"]), "unsupported configuration version")
        require(bool(SHA.fullmatch(c["digest"])), "configuration must be pinned")
        require(strings(c["required_fields"]) and strings(c["understood_fields"])
                and set(c["required_fields"]) <= set(c["understood_fields"]),
                "unknown required configuration field")
        require(integer(p["major"], 1) and integer(p["emit"], 1) and range_ok(p["accepts"])
                and in_range(p["emit"], p["accepts"]), "invalid protocol range")
        require(range_ok(s["read"]) and range_ok(s["write"]), "invalid new-schema range")
        require(strings(value["provides"]) and strings(value["understood_states"]),
                "invalid capabilities or data states")
        require(isinstance(value["requires"], dict), "invalid requirements")
        for component, needs in value["requires"].items():
            require(component in COMPONENTS and strings(needs), "invalid dependency")
        w = value["web"]
        require(range_ok(w["api_accepts"]) and integer(w["api_emit"], 1), "invalid web API")
        require(strings(w["assets"]) and integer(w["retain_seconds"], 1), "invalid asset policy")
    except (KeyError, TypeError, ValueError) as exc:
        raise Rejected("malformed manifest: " + str(exc)) from exc
    return value


def compatible(versions: dict[str, list[dict]], schema: int, data_states: list[str]) -> None:
    """Check all mixed old/new peers, not only matching N/N and N+1/N+1."""
    require(integer(schema, 1) and strings(data_states), "invalid live schema/data observation")
    for component, candidates in versions.items():
        require(component in COMPONENTS and candidates, "missing component versions")
        for m in candidates:
            manifest(m)
            require(m["component"] == component, "component identity mismatch")
            require(in_range(schema, m["schema"]["read"])
                    and in_range(schema, m["schema"]["write"]), "unsafe live schema for " + component)
            require(set(data_states) <= set(m["understood_states"]),
                    "unknown business state for " + component + "; forward repair required")
            for peer, needs in m["requires"].items():
                require(peer in versions and versions[peer], "dependency is missing: " + peer)
                for other in versions[peer]:
                    require(set(needs) <= set(other["provides"]), "peer capability is missing")
                    a, b = m["protocol"], other["protocol"]
                    require(a["major"] == b["major"] and in_range(a["emit"], b["accepts"])
                            and in_range(b["emit"], a["accepts"]), "mixed protocol is incompatible")
    for page in versions.get("web", []):
        for backend in versions.get("rust-core", []) + versions.get("go-extensions", []):
            require(in_range(page["web"]["api_emit"], backend["web"]["api_accepts"]),
                    "old page API would be broken")


def compatibility_matrix(before: dict, after: dict, schema: int, states: list[str]) -> list[dict]:
    names = sorted(COMPONENTS)
    results = []
    for choices in itertools.product((0, 1), repeat=len(names)):
        selected = {n: [(before if i == 0 else after)[n]] for n, i in zip(names, choices)}
        error = None
        try:
            compatible(selected, schema, states)
        except Rejected as exc:
            error = str(exc)
        results.append({"versions": dict(zip(names, choices)), "compatible": error is None,
                        "reason": error})
    # Also test coexistence of every old and new version at once.
    try:
        compatible({n: [before[n], after[n]] for n in names}, schema, states)
        error = None
    except Rejected as exc:
        error = str(exc)
    results.append({"versions": "all-coexisting", "compatible": error is None, "reason": error})
    return results


def configuration_compatible(application: dict, configuration: dict) -> None:
    """Check a separately delivered N/N+1 configuration against the application reader."""
    require(in_range(configuration["version"], application["config"]["accepts"]),
            "application cannot read this configuration version")
    require(set(configuration["required_fields"]) <= set(application["config"]["understood_fields"]),
            "application does not understand a required configuration field")
