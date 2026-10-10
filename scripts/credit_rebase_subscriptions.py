"""Explicit sold-contract, reset, renewal and future subscription grants."""
import json
import math

from credit_rebase_auxiliary import safe_int

SUB_INT = ("user_id", "plan_id", "amount_total", "amount_used", "quota_version", "start_time", "end_time", "last_reset_time", "next_reset_time", "created_at", "updated_at")
SUB_TEXT = ("status", "source")
ORDER_INT = ("user_id", "plan_id", "user_subscription_id", "charged_quota", "refunded_quota", "refunded_amount_micros", "create_time", "complete_time", "expected_amount_micros", "current_period_start", "current_period_end", "provider_event_time_millis")
ORDER_TEXT = ("status", "money", "plan_currency", "plan_snapshot", "settlement_currency", "payment_method", "payment_provider", "provider_subscription_state")
PLAN_INT = ("total_amount", "created_at", "updated_at", "archived_at", "quota_reset_custom_seconds")
PLAN_TEXT = ("price_amount", "currency", "quota_reset_period")
PAYMENT_INT = ("subscription_order_id", "settlement_amount_micros", "created_time")
PAYMENT_TEXT = ("payment_provider", "provider_event_id", "provider_transaction_id", "settlement_currency")
REFUND_INT = ("subscription_order_id", "subscription_payment_event_id", "amount_micros", "quota_revoked", "finance_ledger_entry_id", "created_time")
REFUND_TEXT = ("payment_provider", "provider_event_id", "currency")


def rows(snapshot, key, ints, texts, *, nullable=(), booleans=(),nullable_text=()):
    sources = snapshot.get(key)
    if not isinstance(sources, list):
        raise ValueError("subscription plan requires complete " + key + " array")
    result, seen = [], set()
    for row in sources:
        source = {"id": safe_int(row.get("id"), key + " id")}
        if source["id"] <= 0 or source["id"] in seen:
            raise ValueError("invalid or duplicate " + key + " id")
        seen.add(source["id"])
        for field in ints:
            if field in nullable:
                continue
            source[field] = safe_int(row.get(field), key + " " + field)
        for field in texts:
            value = row.get(field)
            if field in nullable_text and value is None and field in row:
                source[field] = None
                continue
            if not isinstance(value, str) or "\x00" in value:
                raise ValueError(key + " requires exact text " + field)
            source[field] = value
        for field in nullable:
            if field not in row:
                raise ValueError(key + " requires explicit nullable " + field)
            source[field] = None if row[field] is None else safe_int(row[field], key + " " + field)
        for field in booleans:
            if type(row.get(field)) is not bool:
                raise ValueError(key + " requires boolean " + field)
            source[field] = row[field]
        result.append(source)
    return sorted(result, key=lambda s: s["id"])


def plan_snapshot(source):
    try:
        data = json.loads(source["plan_snapshot"], parse_constant=lambda s: (_ for _ in ()).throw(ValueError(s)))
    except (ValueError, TypeError) as error:
        raise ValueError("subscription order needs exact valid sold plan snapshot") from error
    if not isinstance(data, dict):
        raise ValueError("subscription plan snapshot must be an object")
    safe_int(data.get("total_amount"), "full sold subscription grant")
    if data.get("id") != source["plan_id"]:
        raise ValueError("sold subscription snapshot plan identity mismatch")
    return data


def paid_micros(source):
    if source["expected_amount_micros"] > 0:
        return source["expected_amount_micros"]
    # Matches existing Go moneyToMicros(float64), using the raw DB float text.
    try:
        value = float(source["money"])
    except ValueError as error:
        raise ValueError("invalid legacy subscription payment amount") from error
    if not math.isfinite(value) or value < 0:
        raise ValueError("invalid subscription payment amount")
    return safe_int(math.floor(value * 1_000_000 + 0.5), "legacy subscription paid micros")


def make_subscriptions(snapshot, selected, scale, *, include=False):
    if not include:
        return {"subscriptions": [], "subscription_order_updates": [], "subscription_plan_updates": [], "subscription_refund_bases": [], "subscription_order_sources": [], "subscription_payment_events": [], "subscription_payment_refunds": []}
    at = safe_int(snapshot.get("snapshot_at"), "subscription frozen timestamp")
    if at <= 0:
        raise ValueError("subscription plan requires frozen snapshot_at")
    subs = rows(snapshot, "subscriptions", SUB_INT, SUB_TEXT, nullable=("reset_amount", "renewal_amount"))
    orders = rows(snapshot, "subscription_orders", ORDER_INT, ORDER_TEXT,nullable=("current_period_start","current_period_end"),nullable_text=("plan_snapshot","plan_currency","settlement_currency","provider_subscription_state"))
    plans = rows(snapshot, "subscription_plans", PLAN_INT, PLAN_TEXT, booleans=("enabled",))
    payments = rows(snapshot, "subscription_payment_events", PAYMENT_INT, PAYMENT_TEXT, nullable=("period_start", "period_end"))
    refunds = rows(snapshot, "subscription_payment_refunds", REFUND_INT, REFUND_TEXT)
    by_sub = {}
    pending = []
    by_plan = {p["id"]: p for p in plans}
    for order in orders:
        if order["user_id"] not in selected or order["status"] not in ("pending", "success", "failed"):
            raise ValueError("unselected or unsupported subscription order")
        if order["status"] == "pending" and not (order["plan_snapshot"] or "").strip():
            catalog = by_plan.get(order["plan_id"])
            if catalog is None:
                raise ValueError("legacy pending subscription fallback has no complete current catalog")
            pending.append({"id":order["id"],"source":order,"plan_snapshot":order["plan_snapshot"],
                            "grant_source_kind":"runtime_current_catalog_fallback","catalog_plan_id":catalog["id"],
                            "catalog_source":catalog,"original_grant":catalog["total_amount"],
                            "effective_grant":scale(catalog["total_amount"])})
            continue
        sold = plan_snapshot(order)
        if order["status"] == "success":
            by_sub.setdefault(order["user_subscription_id"], []).append(order)
        if order["status"] == "pending":
            if order["user_subscription_id"] != 0:
                raise ValueError("pending subscription order already owns a contract")
            grant = scale(sold["total_amount"])
            if sold["total_amount"] > 0 and grant == 0:
                raise ValueError("finite pending subscription grant rounds to unlimited sentinel")
            sold["total_amount"] = grant
            pending.append({"id": order["id"], "source": order,
                            "grant_source_kind":"frozen_order_plan_snapshot",
                            "original_grant":plan_snapshot(order)["total_amount"],"effective_grant":grant,
                            "plan_snapshot": json.dumps(sold, separators=(",", ":"), ensure_ascii=False)})
    result, bases = [], []
    for sub in subs:
        if sub["user_id"] not in selected or sub["status"] not in ("active", "expired", "cancelled"):
            raise ValueError("unselected or unsupported sold subscription")
        if sub["reset_amount"] is not None or sub["renewal_amount"] is not None:
            raise ValueError("sold subscription already has corrected grants; refuse repeat or partial migration")
        linked = by_sub.get(sub["id"], [])
        if len(linked) > 1:
            raise ValueError("multiple paid orders bind one sold contract")
        order = linked[0] if linked else None
        if order and (order["user_id"] != sub["user_id"] or order["plan_id"] != sub["plan_id"]):
            raise ValueError("sold subscription order ownership changed")
        if not order and sub["source"] not in ("admin", "balance"):
            raise ValueError("sold subscription has no complete original paid order")
        full = plan_snapshot(order)["total_amount"] if order else sub["amount_total"]
        if (full == 0) != (sub["amount_total"] == 0):
            raise ValueError("ambiguous finite versus unlimited sold subscription")
        finite = full > 0
        remaining = max(sub["amount_total"] - sub["amount_used"], 0)
        entry = {"id": sub["id"], "source": sub, "finite": finite,
                 "amount_total": sub["amount_used"] + scale(remaining) if finite else sub["amount_total"],
                 "reset_amount": scale(sub["amount_total"]) if finite else None,
                 "renewal_amount": scale(full) if finite else None,
                 "quota_version": sub["quota_version"] + 1 if finite else sub["quota_version"],
                 "updated_at": at if finite else sub["updated_at"]}
        safe_int(entry["amount_total"], "corrected subscription cap")
        safe_int(entry["quota_version"], "corrected subscription version")
        result.append(entry)
        if order and finite:
            if sub["amount_total"] + order["refunded_quota"] != full:
                raise ValueError("current subscription plus historical refund does not match full sold grant")
            paid = paid_micros(order)
            refunded = order["refunded_amount_micros"]
            if paid <= 0 or refunded > paid:
                raise ValueError("invalid original sold subscription payment/refund facts")
            if paid == refunded:
                if remaining > 0:
                    raise ValueError("fully refunded sold subscription retains spendable credits")
                continue
            current_events = [p for p in payments if p["subscription_order_id"] == order["id"] and
                (p["period_start"] or 0) == (order["current_period_start"] or 0) and
                (p["period_end"] or 0) == (order["current_period_end"] or 0)]
            if len(current_events) > 1 or any(p["settlement_amount_micros"] != paid for p in current_events):
                raise ValueError("subscription current receipt payment basis is ambiguous")
            bases.append({"subscription_order_id": order["id"], "user_subscription_id": sub["id"],
                "user_id": sub["user_id"], "period_start": order["current_period_start"] or 0,
                "period_end": order["current_period_end"] or 0, "subscription_end_time": sub["end_time"],
                "original_quota_version": entry["quota_version"],
                "original_credit_quota": safe_int(sub["amount_total"] + order["refunded_quota"], "original subscription refund grant"),
                "original_refunded_quota": order["refunded_quota"],
                "original_refunded_amount_micros": refunded, "original_paid_amount_micros": paid,
                "refundable_quota": scale(remaining), "reset_quota": entry["reset_amount"],
                "reset_reduced_quota": 0, "rebased_debited_quota": 0})
    pupdates = []
    for source in plans:
        target = scale(source["total_amount"])
        if source["total_amount"] > 0 and target == 0:
            raise ValueError("finite subscription catalog grant rounds to unlimited sentinel")
        pupdates.append({"id": source["id"], "source": source, "total_amount": target, "updated_at": at})
    order_ids, payment_ids = {o["id"] for o in orders}, {p["id"] for p in payments}
    if any(p["subscription_order_id"] not in order_ids for p in payments) or any(r["subscription_order_id"] not in order_ids or r["subscription_payment_event_id"] not in payment_ids for r in refunds):
        raise ValueError("subscription receipt or refund ownership is incomplete")
    if set(by_sub) - {s["id"] for s in subs}:
        raise ValueError("paid subscription contract missing from snapshot")
    return {"subscriptions": result, "subscription_order_updates": pending,
            "subscription_plan_updates": pupdates, "subscription_refund_bases": bases,
            "subscription_order_sources": orders, "subscription_payment_events": payments,
            "subscription_payment_refunds": refunds}


def render_subscriptions(plan, schema, literal):
    if not plan["include_subscriptions"]:
        return [], [], [], [], ""
    mid = literal(plan["migration_id"])
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    ddl = [f"ALTER TABLE {schema}.user_subscriptions ADD COLUMN IF NOT EXISTS reset_amount bigint;",
           f"ALTER TABLE {schema}.user_subscriptions ADD COLUMN IF NOT EXISTS renewal_amount bigint;"]
    fields = ("subscription_order_id", "user_subscription_id", "user_id", "period_start", "period_end", "subscription_end_time", "original_quota_version", "original_credit_quota", "original_refunded_quota", "original_refunded_amount_micros", "original_paid_amount_micros", "refundable_quota", "reset_quota", "reset_reduced_quota", "rebased_debited_quota")
    ddl.append(f"CREATE TABLE IF NOT EXISTS {schema}.subscription_order_credit_rebases (" + "subscription_order_id bigint PRIMARY KEY," + ",".join(f + " bigint NOT NULL" for f in fields[1:]) + f",migration_id text NOT NULL REFERENCES {schema}.wallet_credit_rebases(migration_id));")
    checks, updates, inserts = [], [], []

    def cas(table, source):
        def value(v):
            if type(v) is bool:
                return "true" if v else "false"
            return literal(v) if isinstance(v, str) else str(v)
        clauses = []
        for key, v in source.items():
            rhs = value(v) + ("::double precision" if key == "money" else "::numeric" if key == "price_amount" else "")
            clauses.append('"' + key + '" IS NULL' if v is None else '"' + key + '"=' + rhs)
        checks.append(f"IF NOT EXISTS (SELECT 1 FROM {schema}.{table} WHERE " + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'subscription ownership, state or immutable financial facts changed'; END IF;")

    for entry in plan["subscriptions"]:
        cas("user_subscriptions", entry["source"])
        if entry["finite"]:
            updates.append(f"UPDATE {schema}.user_subscriptions SET amount_total={entry['amount_total']},reset_amount={entry['reset_amount']},renewal_amount={entry['renewal_amount']},quota_version={entry['quota_version']},updated_at={entry['updated_at']} WHERE id={entry['id']};")
    for source in plan["subscription_order_sources"]:
        cas("subscription_orders", source)
    for entry in plan["subscription_order_updates"]:
        if entry["grant_source_kind"] == "frozen_order_plan_snapshot":
            updates.append(f"UPDATE {schema}.subscription_orders SET plan_snapshot={literal(entry['plan_snapshot'])} WHERE id={entry['id']};")
    for entry in plan["subscription_plan_updates"]:
        cas("subscription_plans", entry["source"])
        updates.append(f"UPDATE {schema}.subscription_plans SET total_amount={entry['total_amount']},updated_at={entry['updated_at']} WHERE id={entry['id']};")
    for key in ("subscription_payment_events", "subscription_payment_refunds"):
        for source in plan[key]:
            cas(key, source)
        checks.append(f"IF (SELECT count(*) FROM {schema}.{key} WHERE subscription_order_id IN (SELECT id FROM {schema}.subscription_orders WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND (status='pending' OR user_subscription_id IN (SELECT id FROM {schema}.user_subscriptions WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND (status='active' OR id IN (SELECT user_subscription_id FROM {schema}.subscription_orders WHERE status='success' AND user_subscription_id>0)))))) <> {len(plan[key])} THEN RAISE EXCEPTION 'subscription payment/refund snapshot incomplete'; END IF;")
    checks.append(f"IF (SELECT count(*) FROM {schema}.user_subscriptions WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND (status='active' OR id IN (SELECT user_subscription_id FROM {schema}.subscription_orders WHERE status='success' AND user_subscription_id>0))) <> {len(plan['subscriptions'])} THEN RAISE EXCEPTION 'sold subscription snapshot incomplete'; END IF;")
    checks.append(f"IF (SELECT count(*) FROM {schema}.subscription_orders WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND (status='pending' OR user_subscription_id IN (SELECT id FROM {schema}.user_subscriptions WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND (status='active' OR id IN (SELECT user_subscription_id FROM {schema}.subscription_orders WHERE status='success' AND user_subscription_id>0))))) <> {len(plan['subscription_order_sources'])} THEN RAISE EXCEPTION 'subscription order snapshot incomplete'; END IF;")
    checks.append(f"IF (SELECT count(*) FROM {schema}.subscription_plans) <> {len(plan['subscription_plan_updates'])} THEN RAISE EXCEPTION 'subscription catalog snapshot incomplete'; END IF;")
    for base in plan["subscription_refund_bases"]:
        inserts.append(f"INSERT INTO {schema}.subscription_order_credit_rebases (migration_id," + ",".join(fields) + ") VALUES (" + mid + "," + ",".join(str(base[f]) for f in fields) + ");")
    locks = ("user_subscriptions", "subscription_orders", "subscription_plans", "subscription_payment_events", "subscription_payment_refunds")
    return ddl, checks, updates, inserts, ", " + ", ".join(schema + "." + table for table in locks)
