"""Synthetic opening-contract tests. No database, source dump, or credentials."""

import copy
from decimal import Decimal
import random
import unittest

from legacy_financial_preflight import (
    BIGINT_MAX, BIGINT_MIN, EPOCH_MAX, EPOCH_MIN, JS_SAFE_MAX,
    FinancialPreflightError, preflight_opening_batch, validate_opening_counters,
)


def opening(account=1, user=None, **changes):
    row = {
        "account_id": account, "affiliate_units": 0, "available_units": 0,
        "mapping_sha256": "b" * 64, "migration_id": "synthetic-a03-v1",
        "observed_at": 1700000000, "reserved_units": 0,
        "source_archive_sha256": "a" * 64,
        "source_entity_key": f"legacy.users/{account if user is None else user}",
        "source_row_sha256": f"{account:064x}",
    }
    return {**row, **changes}


def counters(row, **changes):
    value = {key: row[key] for key in (
        "account_id", "mapping_sha256", "migration_id", "observed_at",
        "source_archive_sha256", "source_entity_key", "source_row_sha256",
    )}
    value.update(user_id=int(row["source_entity_key"].split("/")[1]),
                 used_quota=101, request_count=7, aff_history=13)
    return {**value, **changes}


class OpeningPreflightTests(unittest.TestCase):
    def test_negative_wallet_debt_and_affiliate_stay_separate(self):
        row = opening(available_units=-900, reserved_units=200, affiliate_units=300)
        position = preflight_opening_batch([row]).positions[0]
        self.assertEqual((position.wallet_units, position.debt_units,
                          position.reserved_units, position.affiliate_units), (0, -900, 200, 300))
        self.assertEqual(position.clearing_delta_units, 400)
        self.assertEqual(position.journal_amount_units, 1400)
        self.assertEqual(position.entry_count, 4)

    def test_negative_affiliate_does_not_become_wallet_debt(self):
        position = preflight_opening_batch([opening(available_units=50, affiliate_units=-80)]).positions[0]
        self.assertEqual((position.wallet_units, position.debt_units, position.affiliate_units), (50, 0, -80))
        self.assertEqual(position.clearing_delta_units, 30)

    def test_empty_and_zero_openings_do_not_claim_import_or_approval(self):
        for payloads in ([], [opening()]):
            summary = preflight_opening_batch(payloads).summary()
            self.assertEqual(summary["target_writes"], 0)
            self.assertEqual(summary["journal_count"], 0)
            self.assertEqual(summary["entry_count"], 0)
            self.assertFalse(summary["target_state_verified"])
            self.assertFalse(summary["source_unit_provenance_verified"])
            self.assertNotIn("status", summary)

    def test_zero_net_still_has_balanced_nonzero_journal(self):
        result = preflight_opening_batch([opening(available_units=-70, affiliate_units=70)])
        self.assertEqual(result.positions[0].entry_count, 2)
        self.assertEqual(result.summary()["journal_count"], 1)
        self.assertEqual(result.summary()["expected_clearing_after_units"], 0)

    def test_exact_input_and_epoch_boundaries(self):
        for sign in (-1, 1):
            for observed in (EPOCH_MIN, EPOCH_MAX):
                row = opening(available_units=sign * JS_SAFE_MAX,
                              affiliate_units=sign * JS_SAFE_MAX,
                              reserved_units=JS_SAFE_MAX, observed_at=observed)
                result = preflight_opening_batch([row])
                self.assertEqual(result.positions[0].journal_amount_units, 3 * JS_SAFE_MAX)
        row = opening(BIGINT_MAX, user=BIGINT_MAX)
        self.assertEqual(preflight_opening_batch([row]).positions[0].account_id, BIGINT_MAX)

    def test_non_integer_amounts_and_counters_are_rejected(self):
        for field in ("account_id", "available_units", "reserved_units", "affiliate_units", "observed_at"):
            for value in (True, False, 1.0, Decimal("1"), "1", None):
                with self.subTest(field=field, value=repr(value)):
                    with self.assertRaises(FinancialPreflightError):
                        preflight_opening_batch([opening(**{field: value})])
        row = opening()
        for field in ("account_id", "user_id", "used_quota", "request_count", "aff_history", "observed_at"):
            for value in (True, False, 1.0, Decimal("1"), "1", None):
                with self.subTest(field=field, value=repr(value)):
                    with self.assertRaises(FinancialPreflightError):
                        validate_opening_counters([row], [counters(row, **{field: value})])

    def test_out_of_range_inputs_are_rejected(self):
        bad = {
            "account_id": (0, -1, BIGINT_MAX + 1),
            "available_units": (-JS_SAFE_MAX - 1, JS_SAFE_MAX + 1),
            "affiliate_units": (-JS_SAFE_MAX - 1, JS_SAFE_MAX + 1),
            "reserved_units": (-1, JS_SAFE_MAX + 1),
            "observed_at": (EPOCH_MIN - 1, EPOCH_MAX + 1),
        }
        for field, values in bad.items():
            for value in values:
                with self.subTest(field=field, value=value), self.assertRaises(FinancialPreflightError):
                    preflight_opening_batch([opening(**{field: value})])

    def test_clearing_prefix_overflow_is_rejected_even_when_final_sum_is_zero(self):
        for sign in (-1, 1):
            rows = [opening(i + 1, available_units=sign * JS_SAFE_MAX) for i in range(1025)]
            rows += [opening(i + 1026, available_units=-sign * JS_SAFE_MAX) for i in range(1025)]
            self.assertEqual(sum(row["available_units"] for row in rows), 0)
            with self.assertRaisesRegex(FinancialPreflightError, "^opening_clearing_balance_overflow$"):
                preflight_opening_batch(rows)

    def test_clearing_prefix_near_bigint_limit_is_exact(self):
        for sign in (-1, 1):
            rows = [opening(i + 1, available_units=sign * JS_SAFE_MAX) for i in range(1024)]
            result = preflight_opening_batch(rows)
            self.assertEqual(result.positions[-1].clearing_after_units, -sign * 1024 * JS_SAFE_MAX)
            self.assertGreaterEqual(result.positions[-1].clearing_after_units, BIGINT_MIN)
            self.assertLessEqual(result.positions[-1].clearing_after_units, BIGINT_MAX)

    def test_batch_source_bindings_cannot_be_mixed(self):
        for field, value in (("migration_id", "other"), ("mapping_sha256", "c" * 64),
                             ("source_archive_sha256", "d" * 64), ("observed_at", 1700000001)):
            with self.subTest(field=field), self.assertRaisesRegex(FinancialPreflightError, "^opening_batch_source_mismatch$"):
                preflight_opening_batch([opening(), opening(2, **{field: value})])

    def test_malformed_hashes_source_ids_and_unexpected_fields_are_rejected(self):
        changes = [
            {"source_archive_sha256": "A" * 64}, {"mapping_sha256": "b" * 63},
            {"source_row_sha256": "x" * 64}, {"source_row_sha256": None},
            {"migration_id": "bad';SELECT secret"}, {"migration_id": []},
            {"source_entity_key": "legacy.users/01"}, {"source_entity_key": "legacy.users/0"},
            {"source_entity_key": f"legacy.users/{BIGINT_MAX + 1}"},
            {"source_entity_key": "other.users/1"}, {"source_entity_key": 1},
            {"extra": "not allowed"},
        ]
        for change in changes:
            with self.subTest(change=change), self.assertRaises(FinancialPreflightError):
                preflight_opening_batch([opening(**change)])
        for field in opening():
            row = opening()
            del row[field]
            with self.subTest(missing=field), self.assertRaises(FinancialPreflightError):
                preflight_opening_batch([row])

    def test_duplicate_accounts_users_and_noncanonical_order_are_rejected(self):
        batches = ([opening(), opening()], [opening(2), opening()],
                   [opening(), opening(2, user=1)])
        for rows in batches:
            with self.subTest(rows=rows), self.assertRaises(FinancialPreflightError):
                preflight_opening_batch(rows)

    def test_invalid_container_shapes_fail_with_fixed_codes(self):
        for rows in (None, {}, (), [None], [[]]):
            with self.subTest(rows=rows), self.assertRaises(FinancialPreflightError):
                preflight_opening_batch(rows)
        for rows in (None, {}, (), [None], [[]]):
            with self.subTest(counters=rows), self.assertRaises(FinancialPreflightError):
                validate_opening_counters([opening()], rows)

    def test_counter_coverage_binding_owner_and_row_hash_must_match(self):
        row = opening()
        wrong = [[], [counters(row), counters(row)], [counters(row, account_id=2)],
                 [counters(row, user_id=2)], [counters(row, source_entity_key="legacy.users/2")],
                 [counters(row, source_row_sha256="c" * 64)], [counters(row, migration_id="other")],
                 [counters(row, mapping_sha256="c" * 64)],
                 [counters(row, source_archive_sha256="d" * 64)],
                 [counters(row, observed_at=1700000001)], [counters(row, extra="forbidden")]]
        for rows in wrong:
            with self.subTest(rows=rows), self.assertRaises(FinancialPreflightError):
                validate_opening_counters([row], rows)

    def test_historical_counters_remain_exact_and_do_not_create_money(self):
        row = opening(available_units=-3, affiliate_units=2)
        records = [counters(row, used_quota=-JS_SAFE_MAX, request_count=JS_SAFE_MAX, aff_history=-1)]
        before = copy.deepcopy(([row], records))
        validate_opening_counters([row], records)
        self.assertEqual(([row], records), before)
        self.assertEqual(preflight_opening_batch([row]).positions[0].clearing_delta_units, 1)
        for field in ("used_quota", "request_count", "aff_history"):
            for value in (-JS_SAFE_MAX - 1, JS_SAFE_MAX + 1):
                with self.subTest(field=field), self.assertRaises(FinancialPreflightError):
                    validate_opening_counters([row], [counters(row, **{field: value})])

    def test_input_is_unchanged_on_success_and_failure(self):
        for rows in ([opening(available_units=7)], [opening(), opening()]):
            before = copy.deepcopy(rows)
            try:
                preflight_opening_batch(rows)
            except FinancialPreflightError:
                pass
            self.assertEqual(rows, before)

    def test_random_signed_positions_balance_per_journal_and_batch(self):
        rng = random.Random(103)
        rows = [opening(i + 1, available_units=rng.randint(-10**9, 10**9),
                        reserved_units=rng.randint(0, 10**9),
                        affiliate_units=rng.randint(-10**9, 10**9)) for i in range(1000)]
        result = preflight_opening_batch(rows)
        for row, position in zip(rows, result.positions):
            self.assertEqual(position.wallet_units + position.debt_units, row["available_units"])
            self.assertEqual(sum((position.wallet_units, position.debt_units, position.reserved_units,
                                  position.affiliate_units, position.clearing_delta_units)), 0)
            self.assertGreaterEqual(position.entry_count, 2)
        expected = -sum(row["available_units"] + row["reserved_units"] + row["affiliate_units"] for row in rows)
        self.assertEqual(result.summary()["expected_clearing_after_units"], expected)
        validate_opening_counters(rows, [counters(row) for row in rows])


if __name__ == "__main__":
    unittest.main()
