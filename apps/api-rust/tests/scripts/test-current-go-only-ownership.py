#!/usr/bin/env python3
"""Current money-route declarations cannot weaken frozen or ownership gates."""

import copy
import csv
import importlib.util
from pathlib import Path
import unittest


spec = importlib.util.spec_from_file_location(
    "current_go_ownership", Path(__file__).with_name("check-current-go-only-ownership.py"))
ownership = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ownership)


class CurrentGoOwnershipTests(unittest.TestCase):
    def setUp(self):
        directory = ownership.ROOT / "apps/api-rust/tests/fixtures/routes"
        with (directory / "current-go-only-ownership.tsv").open(newline="") as file:
            header, *self.rows = list(csv.reader(file, delimiter="\t"))
        self.assertEqual(header, ownership.HEADER)
        self.current = {tuple(row[:2]): row[4] for row in self.rows}
        self.frozen = {tuple(row[:2]) for row in ownership.route_rows(directory / "legacy-go-routes.tsv")}
        self.rust = {
            name: {tuple(row[:2]) for row in ownership.route_rows(directory / name)}
            for name in ("rust-implemented-routes.tsv", "rust-normal-mounted-routes.tsv",
                         "rust-mounted-fail-closed-shells.tsv")
        }
        self.source = (ownership.ROOT / ownership.SOURCE_PATH).read_text()

    def check(self, rows=None, current=None, frozen=None, rust=None):
        ownership.check_ledger(
            self.rows if rows is None else rows,
            self.current if current is None else current,
            self.frozen if frozen is None else frozen,
            self.rust if rust is None else rust)

    def test_all_twelve_current_declarations_and_real_group_context_pass(self):
        self.check()
        ownership.check_source(self.source)

    def test_missing_duplicate_and_wrong_method_routes_are_rejected(self):
        for broken in (self.rows[:-1], self.rows + [self.rows[0]],
                       [["GET", *self.rows[0][1:]], *self.rows[1:]]):
            with self.subTest(rows=len(broken)), self.assertRaises(ValueError):
                self.check(rows=broken)

    def test_owner_auth_handler_and_source_cannot_be_relabelled(self):
        for index, value in ((2, "rust"), (3, "public"), (4, "other.handler"),
                             (5, "apps/api-rust/src/routes/system_config.rs"), (6, "")):
            broken = copy.deepcopy(self.rows)
            broken[-1][index] = value
            with self.subTest(field=index), self.assertRaises(ValueError):
                self.check(rows=broken)

    def test_actual_gin_manifest_must_contain_exact_method_and_handler(self):
        identity = tuple(self.rows[0][:2])
        for changed in (None, "github.com/LIghtJUNction/api.lmm.best/controller.RequestEpay"):
            broken = self.current.copy()
            if changed is None:
                del broken[identity]
            else:
                broken[identity] = changed
            with self.subTest(handler=changed), self.assertRaises(ValueError):
                self.check(current=broken)

    def test_current_routes_cannot_be_added_to_frozen_evidence(self):
        with self.assertRaises(ValueError):
            self.check(frozen=self.frozen | {tuple(self.rows[0][:2])})

    def test_rust_implementation_mount_and_shell_cannot_claim_go_only_routes(self):
        for name in self.rust:
            broken = copy.deepcopy(self.rust)
            broken[name].add(tuple(self.rows[0][:2]))
            with self.subTest(ledger=name), self.assertRaises(ValueError):
                self.check(rust=broken)

    def test_auth_must_belong_to_the_registered_group_not_elsewhere_in_file(self):
        for original, replacement in (
            ("selfRoute.Use(middleware.UserAuth())", "otherRoute.Use(middleware.UserAuth())"),
            ("optionRoute.Use(middleware.RootAuth())", "otherRoute.Use(middleware.RootAuth())"),
            ("optionRoute.Use(middleware.RootAuth())", "optionRoute.Use(middleware.AdminAuth())"),
            ('userRoute.Group("/")', 'apiRouter.Group("/")'),
        ):
            self.assertIn(original, self.source)
            broken = self.source.replace(original, replacement, 1)
            with self.subTest(replacement=replacement), self.assertRaises(ValueError):
                ownership.check_source(broken)

    def test_raw_group_cannot_alias_legacy_routes_or_drop_its_credit_guard(self):
        for original, replacement in (
            ('selfRoute.Group("/topup/currency")', 'selfRoute.Group("/topup")'),
            ("controller.RequireCanonicalTopUpCredit, controller.RequestAmount", "controller.RequestAmount"),
            ('currencyTopUpRoute.POST("/amount"', 'currencyTopUpRoute.GET("/amount"'),
            ("controller.GetUSDPriceOptions)", "controller.GetOptions)"),
        ):
            self.assertIn(original, self.source)
            broken = self.source.replace(original, replacement, 1)
            with self.subTest(replacement=replacement), self.assertRaises(ValueError):
                ownership.check_source(broken)


if __name__ == "__main__":
    unittest.main()
