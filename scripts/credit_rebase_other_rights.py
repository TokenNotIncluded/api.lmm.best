"""Immutable future-credit child bases; no database connections or source writes."""

SPECS = {
    "public_relay_tip_pool": ("public_relay_tip_pools", "public_relay_contributions",
        ("id", "user_id", "tip_quota", "withdrawn_quota", "created_at", "updated_at"), ("status",), ()),
    "assistant_gift": ("assistant_gifts", "assistant_new_user_gifts",
        ("id", "user_id", "quota", "amount_cents", "created_at", "claimed_at"), ("status",), ()),
    "grant_gift": ("grant_gifts", "gifts",
        ("id", "quota", "start_at", "end_at", "min_used_quota", "min_account_age_days", "created_at"), (), ("enabled",)),
    "ai_directory_ad_refund": ("ai_directory_ad_refunds", "ai_directory_ads",
        ("id", "owner_user_id", "charged_quota", "bid_cents", "paid_at", "expires_at", "hidden_at", "refunded_at"), ("status",), ()),
    "violation_fee_refund": ("violation_fee_refunds", "violation_fee_records",
        ("id", "user_id", "charged_quota", "requested_quota", "created_at", "reversed_at", "reversed_by"), ("status", "error_code"), ()),
    "email_refund_pool": ("hero_sms_email_refunds", "hero_sms_email_orders",
        ("user_id", "charge_quota", "refunded_quota", "quantity", "created_at", "updated_at", "last_refund_ledger_id"), ("id", "status", "operation"), ()),
}
ACTIVATION_INTS = ("user_id", "charge_quota", "refund_quota", "created_at", "updated_at", "refunded_at", "cancelled_at")
ACTIVATION_TEXT = ("id", "order_id", "status", "cancel_reason")


def exact_source(row, ints, texts, bools=()):
    if not isinstance(row, dict) or set(row) != set(ints + texts + bools):
        raise ValueError("future right source must contain the complete approved financial projection")
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


def prepare(snapshot, selected, scale, *, include=False):
    if not include:
        return []
    at = snapshot.get("snapshot_at")
    rows = snapshot.get("other_rights")
    if type(at) is not int or at <= 0 or not isinstance(rows, dict):
        raise ValueError("other rights require a frozen timestamp and complete private supplement")
    entries, seen, emails = [], set(), {}
    for kind, (key, table, ints, texts, bools) in SPECS.items():
        if not isinstance(rows.get(key), list):
            raise ValueError("complete explicit other_rights." + key + " array is required")
        for row in rows[key]:
            source = exact_source(row, ints, texts, bools)
            rid = source["id"]
            if (type(rid) is int and rid <= 0) or (isinstance(rid, str) and (not rid or len(rid) > 64)) or (kind, str(rid)) in seen:
                raise ValueError("invalid or duplicate future credit source id")
            seen.add((kind, str(rid)))
            uid = 0 if kind == "grant_gift" else source.get("user_id", source.get("owner_user_id"))
            if uid != 0 and uid not in selected:
                raise ValueError("future credit source owner was not selected")
            if kind != "grant_gift" and uid <= 0:
                raise ValueError("future credit source requires an owner")
            if kind == "public_relay_tip_pool":
                original = source["tip_quota"] - source["withdrawn_quota"]
                if original < 0:
                    raise ValueError("tip withdrawal exceeds the historical tip pool")
            elif kind == "assistant_gift":
                original = source["quota"]
                if source["status"] != "offered" or source["claimed_at"] != 0 or source["amount_cents"] <= 0:
                    raise ValueError("assistant gift must be an unclaimed positive offer")
            elif kind == "grant_gift":
                original = source["quota"]
                if not source["enabled"] or source["end_at"] <= at or source["start_at"] >= source["end_at"]:
                    raise ValueError("gift must retain a future claim window")
            elif kind == "ai_directory_ad_refund":
                original = source["charged_quota"]
                if source["status"] != "active" or source["expires_at"] <= at or source["refunded_at"] != 0:
                    raise ValueError("advertisement must retain its unfulfilled refund right")
            elif kind == "violation_fee_refund":
                original = source["charged_quota"]
                if source["status"] != "charged" or source["reversed_at"] != 0:
                    raise ValueError("violation fee must retain its unfulfilled appeal right")
            else:
                original = source["charge_quota"] - source["refunded_quota"]
                if original <= 0 or source["quantity"] <= 0:
                    raise ValueError("email order must retain a possible future refund")
                emails[str(rid)] = uid
            if original < 0 or (kind != "public_relay_tip_pool" and original <= 0):
                raise ValueError("invalid future credit principal")
            entries.append({"kind": kind, "source_id": str(rid), "user_id": uid,
                            "original_quota": original, "rebased_quota": scale(original), "source": source})
    activations = rows.get("hero_sms_email_activations")
    if not isinstance(activations, list):
        raise ValueError("complete explicit email activation source array is required")
    activation_ids = set()
    for row in activations:
        s = exact_source(row, ACTIVATION_INTS, ACTIVATION_TEXT)
        if not s["id"] or len(s["id"]) > 64 or s["id"] in activation_ids or emails.get(s["order_id"]) != s["user_id"]:
            raise ValueError("invalid, duplicate or orphan email activation")
        activation_ids.add(s["id"])
    # Email activation rows are guards, not separately added credit pools.
    for entry in entries:
        if entry["kind"] == "email_refund_pool":
            entry["activation_sources"] = [dict(a) for a in activations if a["order_id"] == entry["source_id"]]
            if len(entry["activation_sources"]) != entry["source"]["quantity"]:
                raise ValueError("email activation snapshot is incomplete")
    return sorted(entries, key=lambda e: (e["kind"], e["source_id"]))


def sql(plan, schema, literal):
    if not plan.get("include_other_rights"):
        return [], ""
    entries = plan["other_credit_bases"]
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    at = plan["snapshot_at"]
    conditions = {
        "public_relay_tip_pool": f"user_id=ANY(ARRAY[{selected}]::bigint[])",
        "assistant_gift": f"user_id=ANY(ARRAY[{selected}]::bigint[]) AND status='offered' AND quota>0 AND amount_cents>0",
        "grant_gift": f"enabled=true AND end_at>{at}",
        "ai_directory_ad_refund": f"owner_user_id=ANY(ARRAY[{selected}]::bigint[]) AND status='active' AND expires_at>{at}",
        "violation_fee_refund": f"user_id=ANY(ARRAY[{selected}]::bigint[]) AND status='charged' AND charged_quota>0",
        "email_refund_pool": f"user_id=ANY(ARRAY[{selected}]::bigint[]) AND charge_quota>refunded_quota",
    }
    checks, locks = [], []

    def guard(table, source):
        clauses = []
        for key, value in source.items():
            if type(value) is bool:
                encoded = "true" if value else "false"
            elif type(value) is int:
                encoded = str(value)
            else:
                encoded = literal(value)
            if key == "last_refund_ledger_id":
                clauses.append(f"(SELECT COALESCE(MAX(id),0) FROM {schema}.hero_sms_email_quota_ledgers WHERE order_id={literal(source['id'])} AND entry_type='refund')={encoded}")
            else:
                clauses.append(f'"{key}"={encoded}')
        return f'IF NOT EXISTS (SELECT 1 FROM {schema}."{table}" WHERE ' + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'future credit source facts changed'; END IF;"

    for kind, (_, table, ints, texts, bools) in SPECS.items():
        subset = [e for e in entries if e["kind"] == kind]
        locks.append(f'{schema}."{table}"')
        checks.append(f'IF (SELECT count(*) FROM {schema}."{table}" WHERE {conditions[kind]}) <> {len(subset)} THEN RAISE EXCEPTION \'future credit source snapshot incomplete\'; END IF;')
        for e in subset:
            exact_source(e["source"], ints, texts, bools)
            checks.append(guard(table, e["source"]))
    emails = [e for e in entries if e["kind"] == "email_refund_pool"]
    locks.append(f'{schema}."hero_sms_email_activations"')
    locks.append(f'{schema}."hero_sms_email_quota_ledgers"')
    for e in emails:
        sources = e["activation_sources"]
        checks.append(f'IF (SELECT count(*) FROM {schema}.hero_sms_email_activations WHERE order_id={literal(e["source_id"])}) <> {len(sources)} THEN RAISE EXCEPTION \'email activation snapshot incomplete\'; END IF;')
        for source in sources:
            exact_source(source, ACTIVATION_INTS, ACTIVATION_TEXT)
            checks.append(guard("hero_sms_email_activations", source))
    return checks, ", " + ", ".join(locks)
