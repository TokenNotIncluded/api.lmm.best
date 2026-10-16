"""Offline bounty rebase lineage, never an escrow release or import authority.

Rules below reuse this project's AGPL-3.0 historical source at revision
3948a0a1ee1cefdd016a7aa45a11851545bb4b85:
 scripts/credit_rebase_entitlements.py (SPECS, make_entities),
 scripts/credit_rebase_other_rights.py (exact_source),
 scripts/credit_rebase_bounty_disputes.py (prepare, renamed prepare_disputes).
The original rules are preserved; only their offline adapter is new. See LICENSE.
Historical parent hashes use that revision's JSON domain, not the financial
planner's newer typed digest. Source facts matching an after image do not prove
approval, dump-era executable identity, monetary units or a successful COMMIT.
"""

import hashlib
import json
import re
from collections import Counter
from fractions import Fraction

RULE_REVISION = "3948a0a1ee1cefdd016a7aa45a11851545bb4b85"
BOUNTY_TABLES = (
    "open_source_bounty_projects",
    "open_source_bounty_challenges",
    "open_source_bounty_disputes",
)
MAX_INTEGER = (1 << 53) - 1

# Each writable field is named here. Historical tip/fee/used totals are never writable.
SPECS = {
    "redemptions": {
        "write": ("quota",),
        "int": (
            "user_id",
            "used_user_id",
            "quota",
            "status",
            "created_time",
            "redeemed_time",
            "expired_time",
        ),
        "text": ("reward_type",),
        "nullable_text": ("reward_type",),
        "null": ("deleted_at",),
    },
    "open_source_bounty_projects": {
        "write": ("escrow_quota", "reward_quota", "net_reward_quota"),
        "int": (
            "owner_user_id",
            "escrow_quota",
            "reward_quota",
            "net_reward_quota",
            "reward_slots",
            "platform_fee_quota",
            "platform_fee_rate_bps",
            "created_at",
            "updated_at",
            "published_at",
            "closed_at",
            "archived_at",
        ),
        "text": ("status",),
        "null": (),
    },
    "open_source_bounty_challenges": {
        "write": ("reward_quota",),
        "int": (
            "project_id",
            "participant_user_id",
            "reward_quota",
            "tip_quota",
            "accepted_at",
            "submitted_at",
            "reviewed_at",
            "rejected_at",
            "paid_at",
            "created_at",
            "updated_at",
        ),
        "text": ("status",),
        "null": (),
    },
    "open_source_bounty_disputes": {
        "write": (),
        "int": (
            "challenge_id",
            "project_id",
            "opened_by_user_id",
            "against_user_id",
            "project_escrow_quota_snapshot",
            "reward_quota_snapshot",
            "tip_quota_snapshot",
            "resolved_by_user_id",
            "created_at",
            "updated_at",
            "resolved_at",
        ),
        "text": ("challenge_status_snapshot", "status"),
        "null": (),
    },
}


def make_entities(
    snapshot, selected, scale, *, include_redemptions=False, include_bounties=False
):
    if not include_redemptions and not include_bounties:
        return []
    at = snapshot.get("snapshot_at")
    if type(at) is not int or at <= 0:
        raise ValueError("entitlement plan requires frozen snapshot_at Unix timestamp")
    entities = snapshot.get("entities")
    if not isinstance(entities, dict):
        raise ValueError("explicit entities snapshot is required")
    result, seen = [], set()

    def add(table, row):
        spec = SPECS[table]
        rid = row.get("id")
        if type(rid) is not int or rid <= 0 or (table, rid) in seen:
            raise ValueError("invalid or duplicate entitlement id")
        seen.add((table, rid))
        source = {"id": rid}
        for key in spec["int"]:
            value = row.get(key)
            if type(value) is not int or not 0 <= value <= (1 << 53) - 1:
                raise ValueError(
                    f"entitlement {table}.{key} must be exact nonnegative integer"
                )
            source[key] = value
        for key in spec["text"]:
            value = row.get(key)
            if key in spec.get("nullable_text", ()) and value is None and key in row:
                source[key] = None
                continue
            if not isinstance(value, str) or "\x00" in value:
                raise ValueError(f"entitlement {table}.{key} must be exact text")
            source[key] = value
        for key in spec["null"]:
            if key not in row or row[key] is not None:
                raise ValueError("deleted or ambiguous entitlement must not migrate")
            source[key] = None
        entry = {
            "table": table,
            "id": rid,
            "source": source,
            "updates": {
                key: {"before_credit": source[key], "after_credit": scale(source[key])}
                for key in spec["write"]
            },
        }
        result.append(entry)
        return entry

    if include_redemptions:
        rows = entities.get("redemptions")
        if not isinstance(rows, list):
            raise ValueError("explicit usable redemptions array is required")
        for row in rows:
            e = add("redemptions", row)
            s = e["source"]
            if (
                s["status"] != 1
                or s["used_user_id"] != 0
                or s["reward_type"] not in (None, "", "quota")
                or s["user_id"] not in selected
                or (s["expired_time"] and s["expired_time"] < at)
            ):
                raise ValueError(
                    "redemption is not a usable selected issuer's credit right"
                )

    if include_bounties:
        projects, challenges, disputes = (
            entities.get("bounty_projects"),
            entities.get("bounty_challenges"),
            entities.get("bounty_disputes"),
        )
        if (
            not isinstance(projects, list)
            or not isinstance(challenges, list)
            or not isinstance(disputes, list)
        ):
            raise ValueError(
                "explicit active bounty projects, unpaid challenges and all related disputes are required"
            )
        pmap, owed = {}, {}
        for row in projects:
            e = add("open_source_bounty_projects", row)
            s = e["source"]
            if (
                s["status"] not in ("published", "paused")
                or s["owner_user_id"] not in selected
            ):
                raise ValueError(
                    "bounty is not an active selected owner's future right"
                )
            pmap[s["id"]] = e
            owed[s["id"]] = 0
        claims = {}
        for row in disputes:
            e = add("open_source_bounty_disputes", row)
            s = e["source"]
            if (
                s["project_id"] not in pmap
                or s["opened_by_user_id"] not in selected
                or s["against_user_id"] not in selected
            ):
                raise ValueError(
                    "bounty dispute ownership or project snapshot incomplete"
                )
            claims.setdefault(s["challenge_id"], []).append(s)
        for row in challenges:
            e = add("open_source_bounty_challenges", row)
            s = e["source"]
            cases = claims.get(s["id"], [])
            open_claim = any(case["status"] == "open" for case in cases)
            resolved = any(
                case["status"] in ("resolved_paid", "resolved_denied") for case in cases
            )
            active = (
                s["status"] in ("accepted", "submitted")
                or open_claim
                or (
                    s["status"] == "rejected"
                    and s["rejected_at"] > at - 7 * 24 * 60 * 60
                    and not resolved
                )
            )
            if (
                s["project_id"] not in pmap
                or s["participant_user_id"] not in selected
                or s["paid_at"] != 0
                or (
                    s["status"] not in ("accepted", "submitted", "rejected")
                    and not open_claim
                )
            ):
                raise ValueError(
                    "bounty challenge must be an unpaid selected participant's future right"
                )
            if any(case["project_id"] != s["project_id"] for case in cases):
                raise ValueError("bounty dispute/challenge project identity mismatch")
            if active:
                e["rights_status"] = "active_future_reward"
                owed[s["project_id"]] += e["updates"]["reward_quota"]["after_credit"]
            else:
                e["rights_status"] = "historical_rejection_guard_only"
                e["updates"] = {}
        for pid, project in pmap.items():
            if owed[pid] > project["updates"]["escrow_quota"]["after_credit"]:
                raise ValueError(
                    "rounded bounty escrow cannot cover unpaid commitments; explicitly resolve tail allocation"
                )
    return sorted(result, key=lambda e: (e["table"], e["id"]))


def exact_source(row, ints, texts, bools=()):
    if not isinstance(row, dict) or set(row) != set(ints + texts + bools):
        raise ValueError(
            "future right source must contain the complete approved financial projection"
        )
    result = dict(row)
    for key in ints:
        if type(row[key]) is not int or not 0 <= row[key] <= (1 << 53) - 1:
            raise ValueError("future right requires exact nonnegative integer " + key)
    for key in texts:
        if not isinstance(row[key], str) or "\x00" in row[key]:
            raise ValueError("future right requires exact text " + key)
    for key in bools:
        if type(row[key]) is not bool:
            raise ValueError("future right requires exact boolean " + key)
    return result


KIND = "bounty_dispute_reward"
SOURCE_INTS = (
    "id",
    "challenge_id",
    "project_id",
    "opened_by_user_id",
    "against_user_id",
    "project_escrow_quota_snapshot",
    "reward_quota_snapshot",
    "tip_quota_snapshot",
    "resolved_by_user_id",
    "created_at",
    "updated_at",
    "resolved_at",
)
SOURCE_TEXT = ("challenge_status_snapshot", "status")
FUTURE_CHALLENGE_STATES = ("accepted", "submitted", "rejected")


def prepare_disputes(snapshot, selected, scale, *, include=False):
    if not include:
        return []
    at, entities = snapshot.get("snapshot_at"), snapshot.get("entities")
    if type(at) is not int or at <= 0 or not isinstance(entities, dict):
        raise ValueError("bounty dispute rights require a frozen entities snapshot")
    maps = {}
    for key in ("bounty_projects", "bounty_challenges", "bounty_disputes"):
        rows = entities.get(key)
        if not isinstance(rows, list):
            raise ValueError("complete explicit entities." + key + " array is required")
        maps[key] = {}
        for row in rows:
            if (
                not isinstance(row, dict)
                or type(row.get("id")) is not int
                or row["id"] <= 0
                or row["id"] in maps[key]
            ):
                raise ValueError("invalid or duplicate bounty source id")
            maps[key][row["id"]] = row
    bases = []
    for row in maps["bounty_disputes"].values():
        source = exact_source(row, SOURCE_INTS, SOURCE_TEXT)
        project = maps["bounty_projects"].get(source["project_id"])
        if not project:
            raise ValueError("bounty dispute source must bind its captured project")
        if source["status"] != "open":
            # A resolved paid case can refer to a historical challenge omitted
            # from the unpaid-right snapshot. Its raw dispute remains a guard.
            continue
        challenge = maps["bounty_challenges"].get(source["challenge_id"])
        if not challenge or challenge.get("project_id") != source["project_id"]:
            raise ValueError("open bounty dispute must bind its captured challenge")
        uid, owner = challenge.get("participant_user_id"), project.get("owner_user_id")
        if (
            type(uid) is not int
            or type(owner) is not int
            or uid <= 0
            or owner <= 0
            or uid == owner
            or uid not in selected
            or owner not in selected
        ):
            raise ValueError("bounty dispute parties must be distinct selected users")
        if source["opened_by_user_id"] == owner and source["against_user_id"] == uid:
            continue  # An owner-filed dispute cannot authorize contributor payment.
        if source["opened_by_user_id"] != uid or source["against_user_id"] != owner:
            raise ValueError("bounty dispute claimant or respondent changed")
        if (
            challenge.get("status") not in FUTURE_CHALLENGE_STATES
            or challenge.get("paid_at") != 0
        ):
            continue
        original = source["reward_quota_snapshot"]
        if (
            source["created_at"] <= 0
            or source["created_at"] > at
            or source["resolved_at"] != 0
            or source["resolved_by_user_id"] != 0
            or original <= 0
            or original != challenge.get("reward_quota")
            or project.get("status") not in ("published", "paused")
            or type(project.get("escrow_quota")) is not int
            or project["escrow_quota"] < original
        ):
            raise ValueError(
                "bounty dispute is inconsistent with its unpaid escrow reward"
            )
        target = scale(original)
        if type(target) is not int or not 0 <= target <= original:
            raise ValueError("invalid bounty dispute target credit integer")
        bases.append(
            {
                "kind": KIND,
                "source_id": str(source["id"]),
                "user_id": uid,
                "original_quota": original,
                "rebased_quota": target,
                "source": source,
            }
        )
    return sorted(bases, key=lambda e: e["source_id"])


def legacy_digest(value):
    return hashlib.sha256(
        json.dumps(
            value, sort_keys=True, separators=(",", ":"), allow_nan=False
        ).encode()
    ).hexdigest()


def typed_equal(F, left, right):
    return F.digest(left) == F.digest(right)


def safe_int(F, value, *, positive=False):
    F.require(
        type(value) is int
        and abs(value) <= MAX_INTEGER
        and (not positive or value > 0),
        "bounty_rebase_integer_invalid",
    )
    return value


def parent_contract(F, audit, identities):
    parent = audit["full_plan_unknown_fields_preserved"]
    F.require(
        type(parent) is dict
        and type(parent.get("version")) is int
        and parent["version"] == 1
        and parent.get("kind") == "offline_credit_balance_rebase_preview",
        "bounty_rebase_parent_kind_invalid",
    )

    def legacy_json_types(value):
        if type(value) in (str, int, bool) or value is None:
            return True
        if type(value) is list:
            return all(legacy_json_types(child) for child in value)
        if type(value) is dict:
            return all(
                type(key) is str and legacy_json_types(child)
                for key, child in value.items()
            )
        return False

    F.require(
        legacy_json_types(parent), "bounty_rebase_legacy_hash_json_type_unsupported"
    )
    F.require(
        parent.get("plan_sha256")
        == audit["declared_plan_sha256"]
        == legacy_digest({k: v for k, v in parent.items() if k != "plan_sha256"}),
        "bounty_rebase_parent_hash_mismatch",
    )
    F.require(
        parent.get("business_plan_sha256")
        == legacy_digest(
            {
                k: v
                for k, v in parent.items()
                if k
                not in (
                    "target",
                    "source_sha256",
                    "plan_sha256",
                    "business_plan_sha256",
                )
            }
        ),
        "bounty_rebase_business_hash_mismatch",
    )
    F.require(
        type(parent.get("migration_id")) is str
        and re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,99}", parent["migration_id"])
        is not None
        and parent.get("migration_id") == audit["source_migration_id"]
        and parent.get("include_bounties") is True
        and parent.get("include_other_rights") is True,
        "bounty_rebase_parent_scope_invalid",
    )
    ids = parent.get("user_ids")
    F.require(type(ids) is list and bool(ids), "bounty_rebase_selected_users_invalid")
    selected = {safe_int(F, value, positive=True) for value in ids}
    F.require(
        ids == sorted(selected) and selected <= set(identities),
        "bounty_rebase_selected_users_invalid",
    )
    divisor_text = parent.get("divisor")
    F.require(
        type(divisor_text) is str
        and re.fullmatch(r"[0-9]+(?:\.[0-9]{1,18})?", divisor_text) is not None,
        "bounty_rebase_divisor_invalid",
    )
    divisor = Fraction(divisor_text)
    F.require(
        divisor > 1
        and parent.get("rounding") in ("half-away-from-zero", "toward-zero"),
        "bounty_rebase_scaling_policy_invalid",
    )
    factor = parent.get("exact_factor")
    F.require(
        type(factor) is dict and set(factor) == {"numerator", "denominator"},
        "bounty_rebase_factor_invalid",
    )
    # Rational factors are not monetary balances: a legal 18-digit decimal
    # divisor can have a numerator/denominator beyond JavaScript's safe integer.
    F.require(
        all(
            type(factor[key]) is int and factor[key] > 0
            for key in ("numerator", "denominator")
        ),
        "bounty_rebase_factor_invalid",
    )
    numerator, denominator = factor["numerator"], factor["denominator"]
    F.require(
        Fraction(numerator, denominator) == 1 / divisor
        and type(parent.get("usd_credit_conversion")) is int
        and parent["usd_credit_conversion"] == 500000,
        "bounty_rebase_factor_or_credit_basis_invalid",
    )
    at = safe_int(F, parent.get("snapshot_at"), positive=True)

    def scale(value):
        safe_int(F, value)
        amount = abs(value) * divisor.denominator
        result, remainder = divmod(amount, divisor.numerator)
        if (
            parent["rounding"] == "half-away-from-zero"
            and remainder * 2 >= divisor.numerator
        ):
            result += 1
        return -result if value < 0 else result

    entries = parent.get("entity_updates")
    F.require(
        type(entries) is list and all(type(e) is dict for e in entries),
        "bounty_rebase_entities_invalid",
    )
    entries = [e for e in entries if e.get("table") in BOUNTY_TABLES]
    seen = set()
    entity_rows = {
        "bounty_projects": [],
        "bounty_challenges": [],
        "bounty_disputes": [],
    }
    for e in entries:
        key = (e["table"], safe_int(F, e.get("id"), positive=True))
        F.require(
            key not in seen
            and type(e.get("source")) is dict
            and type(e.get("updates")) is dict,
            "bounty_rebase_entity_duplicate_or_invalid",
        )
        seen.add(key)
        spec = SPECS[e["table"]]
        fields = {"id", *spec["int"], *spec["text"], *spec["null"]}
        F.require(
            set(e["source"]) == fields
            and type(e["source"].get("id")) is int
            and e["id"] == e["source"]["id"],
            "bounty_rebase_source_projection_invalid",
        )
        entity_rows[
            {
                BOUNTY_TABLES[0]: "bounty_projects",
                BOUNTY_TABLES[1]: "bounty_challenges",
                BOUNTY_TABLES[2]: "bounty_disputes",
            }[e["table"]]
        ].append(e["source"])
    snapshot = {"snapshot_at": at, "entities": entity_rows}
    try:
        regenerated = make_entities(snapshot, selected, scale, include_bounties=True)
        expected_bases = prepare_disputes(snapshot, selected, scale, include=True)
    except ValueError:
        raise F.MappingError("bounty_rebase_pinned_rule_rejected") from None
    F.require(
        typed_equal(
            F, sorted(entries, key=lambda e: (e["table"], e["id"])), regenerated
        ),
        "bounty_rebase_regenerated_entities_mismatch",
    )
    bases = parent.get("other_credit_bases")
    F.require(
        type(bases) is list and all(type(e) is dict for e in bases),
        "bounty_rebase_future_bases_invalid",
    )
    actual_bases = [e for e in bases if e.get("kind") == KIND]
    F.require(
        typed_equal(
            F,
            sorted(actual_bases, key=lambda e: e.get("source_id", "")),
            expected_bases,
        ),
        "bounty_rebase_future_dispute_basis_mismatch",
    )
    return parent, regenerated


# @mark migration.financial-rights role=implementation
# @mark migration.bounty-basis role=implementation
def build(F, *, tables, identities, plan):
    F.verify_plan_mapping(plan)
    blockers, records, bindings = [], [], []
    counts = Counter()
    regenerated_amount_field_count = 0
    base = {
        "format_version": 1,
        "scope": "bounty-rebase-lineage-only",
        "rule_source_revision": RULE_REVISION,
        "economic_approval_verified": False,
        "import_payload_created": False,
        "target_queries": 0,
        "target_writes": 0,
        "source_queries": 0,
        "full_database_rights_cleared": False,
        "current_amounts_rescaled": False,
        "dispute_snapshots_rescaled": False,
    }
    if not set(BOUNTY_TABLES) <= set(tables):
        blockers.append("bounty_source_table_coverage_incomplete")
    else:
        indexes = {}
        for table in BOUNTY_TABLES:
            evidence = plan["source_evidence"][table]
            rows = tables[table]
            F.require(
                type(evidence["row_count"]) is int
                and len(rows) == evidence["row_count"]
                and evidence["primary_key_columns"] == ["id"]
                and [F.digest(row) for row in rows] == evidence["row_hashes"],
                "bounty_rebase_current_evidence_mismatch",
            )
            F.require(
                type(evidence["source_records"]) is list
                and len(evidence["source_records"]) == len(rows),
                "bounty_rebase_current_record_coverage_mismatch",
            )
            index = {}
            for ordinal, row in enumerate(rows):
                rid = safe_int(F, row.get("id"), positive=True)
                F.require(rid not in index, "bounty_rebase_current_id_duplicate")
                expected = {
                    "source_primary_key": {"id": rid},
                    "source_export_ordinal": ordinal,
                    "source_row_sha256": F.digest(row),
                }
                F.require(
                    typed_equal(F, evidence["source_records"][ordinal], expected),
                    "bounty_rebase_current_record_mismatch",
                )
                index[rid] = row
            indexes[table] = index
        audits = plan["refund_lineage"]["wallet_rebase_audits"]
        parents = [
            audit
            for audit in audits
            if audit["full_plan_unknown_fields_preserved"].get("include_bounties")
            is True
        ]
        if not parents:
            blockers.append("bounty_rebase_parent_missing")
        elif len(parents) != 1:
            blockers.append("bounty_rebase_multiple_parents_require_ordered_lineage")
        else:
            audit = parents[0]
            raw_audits = tables.get("wallet_credit_rebases", [])
            raw_evidence = plan["source_evidence"].get("wallet_credit_rebases")
            F.require(
                type(raw_evidence) is dict
                and type(raw_evidence.get("row_count")) is int
                and raw_evidence["row_count"] == len(raw_audits)
                and [F.digest(row) for row in raw_audits] == raw_evidence["row_hashes"],
                "bounty_rebase_raw_audit_evidence_mismatch",
            )
            raw_matches = [
                row for row in raw_audits if F.digest(row) == audit["source_row_sha256"]
            ]
            F.require(
                len(raw_matches) == 1
                and raw_matches[0].get("migration_id") == audit["source_migration_id"]
                and raw_matches[0].get("plan_sha256") == audit["declared_plan_sha256"]
                and typed_equal(
                    F,
                    F.exact(
                        F.parse_json(raw_matches[0]["plan"])
                        if type(raw_matches[0]["plan"]) is str
                        else raw_matches[0]["plan"]
                    ),
                    audit["full_plan_unknown_fields_preserved"],
                ),
                "bounty_rebase_raw_parent_binding_mismatch",
            )
            parent, entries = parent_contract(F, audit, identities)
            regenerated_amount_field_count = sum(
                len(entry["updates"]) for entry in entries
            )
            bindings.append(
                {
                    "source_migration_id": audit["source_migration_id"],
                    "source_audit_row_sha256": audit["source_row_sha256"],
                    "plan_sha256": parent["plan_sha256"],
                    "business_plan_sha256": parent["business_plan_sha256"],
                    "parent_typed_sha256": F.digest(parent),
                    "historical_hash_domain": "python-json-sort-keys-compact-ensure-ascii",
                    "original_jsonb_serialization_bytes_reverified": False,
                    "reconstructed_compact_parent_hash_matches": True,
                    "exact_factor": parent["exact_factor"],
                    "rounding": parent["rounding"],
                    "usd_credit_conversion": parent["usd_credit_conversion"],
                    "snapshot_at": parent["snapshot_at"],
                    "regenerated_entity_rules_match": True,
                }
            )
            covered = {table: set() for table in BOUNTY_TABLES}
            for entry in entries:
                table, rid = entry["table"], entry["id"]
                covered[table].add(rid)
                current = indexes[table].get(rid)
                before = entry["source"]
                after = {
                    **before,
                    **{
                        field: change["after_credit"]
                        for field, change in entry["updates"].items()
                    },
                }
                if current is None:
                    state = "missing"
                else:
                    projected = {field: current.get(field) for field in before}
                    is_before, is_after = (
                        typed_equal(F, projected, before),
                        typed_equal(F, projected, after),
                    )
                    if is_before and is_after:
                        state = "before_and_after"
                    elif is_after:
                        state = "after"
                    elif is_before:
                        state = "before"
                    elif all(
                        typed_equal(F, current.get(field), value)
                        for field, value in before.items()
                        if field not in entry["updates"]
                    ):
                        state = "mixed_or_unmatched_amounts"
                    else:
                        state = "changed_since_snapshot"
                counts[state] += 1
                records.append(
                    {
                        "table": table,
                        "source_primary_key": {"id": rid},
                        "state": state,
                        "parent_source_sha256": F.digest(before),
                        "parent_after_sha256": F.digest(after),
                        "current_source_row_sha256": F.digest(current)
                        if current is not None
                        else None,
                        "rights_status": entry.get("rights_status", "project-escrow"),
                        "economic_authority": "not-established",
                    }
                )
            # Complete current scope is checked independently of the parent's list.
            # Extra/new rows remain protected, never silently treated as migrated.
            selected = set(parent["user_ids"])
            project_ids = covered[BOUNTY_TABLES[0]]
            open_dispute_challenges = {
                row.get("challenge_id")
                for row in indexes[BOUNTY_TABLES[2]].values()
                if row.get("project_id") in project_ids and row.get("status") == "open"
            }
            for table in BOUNTY_TABLES:
                for rid, row in indexes[table].items():
                    relevant = (
                        (
                            table == BOUNTY_TABLES[0]
                            and (
                                row.get("escrow_quota") != 0
                                or (
                                    row.get("owner_user_id") in selected
                                    and row.get("status") in ("published", "paused")
                                )
                            )
                        )
                        or (
                            table == BOUNTY_TABLES[1]
                            and row.get("project_id") in project_ids
                            and row.get("paid_at") == 0
                            and (
                                row.get("status")
                                in ("accepted", "submitted", "rejected")
                                or rid in open_dispute_challenges
                            )
                        )
                        or (
                            table == BOUNTY_TABLES[2]
                            and row.get("project_id") in project_ids
                        )
                    )
                    if relevant and rid not in covered[table]:
                        counts["outside_parent_scope"] += 1
                        records.append(
                            {
                                "table": table,
                                "source_primary_key": {"id": rid},
                                "state": "outside_parent_scope",
                                "current_source_row_sha256": F.digest(row),
                                "economic_authority": "not-established",
                            }
                        )
            unsafe = set(counts) - {"after", "before_and_after"}
            if unsafe:
                blockers.append("bounty_current_rows_not_exact_parent_after")
            if counts["after"] == 0:
                blockers.append("bounty_no_after_only_conversion_witness")
    blockers.append("bounty_economic_approval_and_dump_epoch_unit_authority_missing")
    lineage = (
        "matches-parent-after-with-unverified-authority"
        if bindings
        and counts["after"] > 0
        and not (set(counts) - {"after", "before_and_after"})
        else "not-proven-exact-parent-after"
    )
    public = {
        **base,
        "lineage_status": lineage,
        "row_state_counts": dict(sorted(counts.items())),
        "parent_count": len(bindings),
        "record_count": len(records),
        "regenerated_amount_field_count": regenerated_amount_field_count,
        "blockers": sorted(set(blockers)),
    }
    return {
        **base,
        "lineage_status": lineage,
        "parent_plan_bindings": bindings,
        "records": records,
        "blockers": sorted(set(blockers)),
        "public_summary": public,
    }
