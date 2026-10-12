"""Check a legacy opening batch without SQL, credentials, or target writes.

The input is the existing ``core_billing.import_opening_balance(jsonb)``
contract. Results describe an empty target with a zero clearing balance. They
are not target observations, source approvals, or permission to execute an
import. The owner must repeat target checks inside the import transaction.
"""

from dataclasses import dataclass
import re

BIGINT_MIN, BIGINT_MAX = -(2**63), 2**63 - 1
JS_SAFE_MAX = 2**53 - 1
EPOCH_MIN, EPOCH_MAX = -62135596800, 253402300799
_HASH = re.compile(r"[0-9a-f]{64}")
_MIGRATION = re.compile(r"[A-Za-z0-9._:/-]{1,128}")
_USER = re.compile(r"legacy\.users/([1-9][0-9]{0,18})")
_OPENING_FIELDS = frozenset((
    "account_id", "affiliate_units", "available_units", "mapping_sha256",
    "migration_id", "observed_at", "reserved_units", "source_archive_sha256",
    "source_entity_key", "source_row_sha256",
))
_COUNTER_FIELDS = frozenset((
    "account_id", "aff_history", "mapping_sha256", "migration_id", "observed_at",
    "request_count", "source_archive_sha256", "source_entity_key",
    "source_row_sha256", "used_quota", "user_id",
))
_BINDING_FIELDS = (
    "migration_id", "source_archive_sha256", "mapping_sha256", "observed_at",
)


class FinancialPreflightError(ValueError):
    """A fixed error code; never includes source rows or credentials."""


def _require(condition: bool, code: str) -> None:
    if not condition:
        raise FinancialPreflightError(code)


def _integer(value: object, code: str, minimum: int, maximum: int) -> int:
    _require(type(value) is int and minimum <= value <= maximum, code)
    return value


def _shape(row: object, fields: frozenset[str], code: str) -> None:
    _require(type(row) is dict and row.keys() == fields, code)


def _binding(row: dict) -> tuple[str, str, str, int]:
    migration = row["migration_id"]
    _require(type(migration) is str and _MIGRATION.fullmatch(migration) is not None,
             "opening_migration_id_invalid")
    for field in ("source_archive_sha256", "mapping_sha256", "source_row_sha256"):
        value = row[field]
        _require(type(value) is str and _HASH.fullmatch(value) is not None,
                 "opening_source_hash_invalid")
    _integer(row["observed_at"], "opening_observation_invalid", EPOCH_MIN, EPOCH_MAX)
    return tuple(row[field] for field in _BINDING_FIELDS)


@dataclass(frozen=True, slots=True)
class OpeningPosition:
    account_id: int
    user_id: int
    source_entity_key: str
    source_row_sha256: str
    wallet_units: int
    debt_units: int
    reserved_units: int
    affiliate_units: int
    clearing_delta_units: int
    clearing_after_units: int
    journal_amount_units: int

    @property
    def entry_count(self) -> int:
        return sum(value != 0 for value in (
            self.wallet_units, self.debt_units, self.reserved_units,
            self.affiliate_units, self.clearing_delta_units,
        ))


@dataclass(frozen=True, slots=True)
class OpeningBatch:
    binding: tuple[str, str, str, int] | None
    positions: tuple[OpeningPosition, ...]

    def summary(self) -> dict:
        return {
            "contract": "legacy-opening-preflight-v1",
            "scope": "wallet-debt-reserved-affiliate-opening-only",
            "unit": "credit_500k_usd",
            "credits_per_usd": 500000,
            "account_count": len(self.positions),
            "journal_count": sum(p.journal_amount_units != 0 for p in self.positions),
            "entry_count": sum(p.entry_count for p in self.positions),
            "expected_clearing_before_units": 0,
            "expected_clearing_after_units": (
                self.positions[-1].clearing_after_units if self.positions else 0
            ),
            "target_state_verified": False,
            "source_unit_provenance_verified": False,
            "target_writes": 0,
        }


def preflight_opening_batch(payloads: list[dict]) -> OpeningBatch:
    """Check every intermediate clearing balance in canonical account order.

    Each amount may fit the input limit while a batch exceeds BIGINT. Checking
    only the final sum is insufficient: earlier statements can overflow even
    when later rows cancel them. Do not reorder rows to hide that failure.
    """
    _require(type(payloads) is list, "opening_batch_shape_invalid")
    positions, users = [], set()
    binding, previous_account, clearing = None, 0, 0
    for row in payloads:
        _shape(row, _OPENING_FIELDS, "opening_payload_shape_invalid")
        row_binding = _binding(row)
        if binding is None:
            binding = row_binding
        _require(row_binding == binding, "opening_batch_source_mismatch")
        account = _integer(row["account_id"], "opening_account_invalid", 1, BIGINT_MAX)
        _require(account > previous_account, "opening_account_order_or_duplicate")
        previous_account = account
        entity = row["source_entity_key"]
        match = _USER.fullmatch(entity) if type(entity) is str else None
        _require(match is not None, "opening_source_entity_invalid")
        user = _integer(int(match.group(1)), "opening_source_user_invalid", 1, BIGINT_MAX)
        _require(user not in users, "opening_source_user_duplicate")
        users.add(user)
        available = _integer(row["available_units"], "opening_wallet_out_of_range",
                             -JS_SAFE_MAX, JS_SAFE_MAX)
        reserved = _integer(row["reserved_units"], "opening_reserved_out_of_range",
                            0, JS_SAFE_MAX)
        affiliate = _integer(row["affiliate_units"], "opening_affiliate_out_of_range",
                             -JS_SAFE_MAX, JS_SAFE_MAX)
        delta = _integer(-(available + reserved + affiliate),
                         "opening_clearing_delta_overflow", BIGINT_MIN, BIGINT_MAX)
        clearing = _integer(clearing + delta, "opening_clearing_balance_overflow",
                            BIGINT_MIN, BIGINT_MAX)
        amount = _integer(abs(available) + reserved + abs(affiliate),
                          "opening_journal_amount_overflow", 0, BIGINT_MAX)
        positions.append(OpeningPosition(
            account, user, entity, row["source_row_sha256"], max(available, 0),
            min(available, 0), reserved, affiliate, delta, clearing, amount,
        ))
    return OpeningBatch(binding, tuple(positions))


def validate_opening_counters(opening_payloads: list[dict], counter_payloads: list[dict]) -> None:
    """Check complete counter ownership without turning old counters into money.

    Negative historical integers remain unchanged, as in the current SQL
    contract. This function does not price them or infer a historical USD basis.
    """
    batch = preflight_opening_batch(opening_payloads)
    _require(type(counter_payloads) is list, "opening_counter_batch_shape_invalid")
    _require(len(counter_payloads) == len(batch.positions), "opening_counter_coverage_mismatch")
    for position, row in zip(batch.positions, counter_payloads):
        _shape(row, _COUNTER_FIELDS, "opening_counter_payload_shape_invalid")
        _require(_binding(row) == batch.binding, "opening_counter_source_mismatch")
        account = _integer(row["account_id"], "opening_counter_account_invalid", 1, BIGINT_MAX)
        user = _integer(row["user_id"], "opening_counter_user_invalid", 1, BIGINT_MAX)
        _require((account, user, row["source_entity_key"], row["source_row_sha256"]) == (
            position.account_id, position.user_id, position.source_entity_key,
            position.source_row_sha256,
        ), "opening_counter_owner_mismatch")
        for field in ("used_quota", "request_count", "aff_history"):
            _integer(row[field], "opening_counter_out_of_range", -JS_SAFE_MAX, JS_SAFE_MAX)
