#!/usr/bin/env python3
"""Regression tests for current-route audit false completion cases."""

import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location(
    "audit_current_routes", Path(__file__).with_name("audit-current-routes.py")
)
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


def go(path="/api/new-feature", handler="example/controller.NewFeature"):
    return {"method": "GET", "path": path, "go_handler": handler}


def coverage(route, kind="static-only", mounted=False):
    return {
        **route,
        "record_type": "route",
        "class": kind,
        "rust_normal": "mounted" if mounted else "unmounted",
        "evidence": {"frozen_go": False},
    }


def source(route, **overrides):
    return {
        "method": route["method"], "path": route["path"],
        "source": "apps/api-rust/src/routes/example.rs:10", "handler": "example",
        "category": "compatibility", "placeholder": False,
        **overrides,
    }


class CurrentRouteAuditTests(unittest.TestCase):
    def test_current_go_addition_cannot_hide_behind_frozen_completion(self):
        route = go()
        report = audit.analyze([route], [coverage(route)], [])
        self.assertEqual(report["summary"]["source_missing"], 1)
        self.assertFalse(audit.requirement_met(report, "source"))
        self.assertFalse(audit.requirement_met(report, "mounted"))
        self.assertFalse(report["summary"]["behavioral_parity_verified"])

    def test_real_source_without_ledger_is_distinguished_from_missing_source(self):
        route = go()
        report = audit.analyze([route], [coverage(route)], [source(route)])
        self.assertEqual(report["summary"]["source_missing"], 0)
        self.assertEqual(report["summary"]["ledger_missing_with_source"], 1)
        self.assertTrue(audit.requirement_met(report, "source"))
        self.assertFalse(audit.requirement_met(report, "mounted"))

    def test_disabled_shell_never_satisfies_normal_mount(self):
        route = go()
        report = audit.analyze([route], [coverage(route, "mounted-fail-closed-shell")], [source(route)])
        self.assertEqual(report["summary"]["mounted_fail_closed_shells"], 1)
        self.assertFalse(audit.requirement_met(report, "mounted"))

    def test_stale_implemented_ledger_cannot_supply_missing_source(self):
        route = go()
        report = audit.analyze([route], [coverage(route, "differential-candidate", True)], [])
        self.assertEqual(report["summary"]["ledger_claim_without_source"], 1)
        self.assertFalse(audit.requirement_met(report, "mounted"))

    def test_test_only_declaration_and_todo_do_not_satisfy_source_requirement(self):
        route = go()
        for declaration in (source(route, category="test"), source(route, placeholder=True)):
            with self.subTest(declaration=declaration):
                report = audit.analyze([route], [coverage(route)], [declaration])
                self.assertFalse(audit.requirement_met(report, "source"))

    def test_legacy_501_requires_current_go_to_remain_a_stub(self):
        route = go("/v1/files")
        with self.assertRaisesRegex(ValueError, "no longer has a legacy 501"):
            audit.analyze([route], [coverage(route, "legacy-501", True)], [source(route, placeholder=True)])
        route = go("/v1/files", "example/controller.RelayNotImplemented")
        report = audit.analyze([route], [coverage(route, "legacy-501", True)], [source(route, placeholder=True)])
        self.assertTrue(audit.requirement_met(report, "mounted"))
        self.assertFalse(report["summary"]["behavioral_parity_verified"])

    def test_mismatched_or_duplicate_evidence_is_rejected(self):
        route = go()
        with self.assertRaisesRegex(ValueError, "does not match"):
            audit.analyze([route], [], [])
        with self.assertRaisesRegex(ValueError, "duplicate"):
            audit.analyze([route, route], [coverage(route)], [])
        with self.assertRaisesRegex(ValueError, "handler drift"):
            audit.analyze([route], [coverage(go(handler="stale.Handler"))], [])


if __name__ == "__main__":
    unittest.main()
