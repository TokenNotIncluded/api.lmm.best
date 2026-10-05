"""Locked future payment/referral bases; never executes SQL or reads a database."""

TOPUP_NUMBERS = ("credited_quota", "amount", "platform_amount_micros", "settled_amount_micros", "expected_amount_micros", "refunded_quota", "refunded_amount_micros")
TOPUP_TEXT = ("payment_provider", "payment_method", "settlement_currency")
REFERRAL_NUMBERS = ("inviter_id", "invitee_id", "top_up_id", "quota", "revoked_quota", "penalty_quota", "penalty_percent", "max_penalty_quota", "revision", "created_at", "updated_at")
INTERNAL_METHODS = ("ldc", "linuxdo", "linux_do", "linuxdo_credit")
ASCII_SPACE = " \t\n\r\v\f"
RECOVERABLE_TOPUP_SQL = "(status='pending' OR (payment_provider='waffo_pancake' AND status='failed' AND failure_reason_code='checkout_timeout'))"


def legacy_noncash(source):
    # The authoritative boolean is exported by Go; this independent check and
    # its SQL equivalent fail closed on a wrongly classified cash payment.
    for key in TOPUP_TEXT:
        value = source.get(key)
        if not isinstance(value, str) or not value.isascii() or "\x00" in value:
            raise ValueError("topup classification requires exact ASCII " + key)
    provider, method, currency = (source[key].strip(ASCII_SPACE).lower() for key in TOPUP_TEXT)
    expected = provider == "epay" and (method in INTERNAL_METHODS or
        (method == "epay" and (currency != "cny" or
         (source["expected_amount_micros"] <= 0 and source["settled_amount_micros"] <= 0))))
    if type(source.get("is_legacy_linuxdo_credit_topup")) is not bool or source["is_legacy_linuxdo_credit_topup"] != expected:
        raise ValueError("authoritative noncash classification disagrees with immutable raw facts")
    return expected


def legacy_noncash_sql(literal):
    def normalized(column):
        return "lower(btrim(" + column + "," + literal(ASCII_SPACE) + "))"
    provider, method, currency = (normalized(key) for key in TOPUP_TEXT)
    return "(" + provider + "='epay' AND (" + method + " IN (" + ",".join(literal(v) for v in INTERNAL_METHODS) + ") OR (" + method + "='epay' AND (" + currency + "<>'cny' OR (expected_amount_micros<=0 AND settled_amount_micros<=0)))))"


def safe_int(value, label):
    if type(value) is not int or not 0 <= value <= (1 << 53) - 1:
        raise ValueError(label + " must be an exact nonnegative integer")
    return value


def make_auxiliary(snapshot, selected, scale, *, include_pending=False, include_affiliate=False):
    pending, referrals, blocked = [], [], []
    if include_pending:
        rows = snapshot.get("pending_topups")
        if not isinstance(rows, list):
            raise ValueError("explicit complete pending_topups snapshot is required")
        seen = set()
        for row in rows:
            source = dict(row)
            tid, uid = safe_int(source.get("id"), "pending topup id"), safe_int(source.get("user_id"), "pending topup user")
            recoverable = source.get("status") == "pending" or (source.get("status") == "failed" and source.get("payment_provider") == "waffo_pancake" and source.get("failure_reason_code") == "checkout_timeout")
            if tid <= 0 or tid in seen or uid not in selected or not recoverable:
                raise ValueError("duplicate, unselected or nonpending topup")
            seen.add(tid)
            for key in TOPUP_NUMBERS:
                safe_int(source.get(key), "pending topup " + key)
            for key in TOPUP_TEXT + ("money", "failure_reason_code"):
                if not isinstance(source.get(key), str) or "\x00" in source[key]:
                    raise ValueError("pending source requires exact " + key)
            from decimal import Decimal, InvalidOperation
            try:
                money = Decimal(source["money"])
            except InvalidOperation as error:
                raise ValueError("invalid pending money string") from error
            if not money.is_finite() or money < 0:
                raise ValueError("invalid pending money value")
            old = safe_int(source.get("effective_credited_quota"), "normalized pending quote")
            noncash = legacy_noncash(source)
            for key, expected in (("pending_credit_rebase_key", ""), ("pending_credit_rebase_original_quota", 0), ("pending_credit_rebase_effective_quota", 0)):
                if source.get(key) != expected:
                    raise ValueError("pending quote already rebased or metadata snapshot incomplete")
            if old == 0:
                blocked.append({"source":source,"top_up_id":tid,"user_id":uid,
                                "original_credited_quota":0,"effective_credited_quota":0,
                                "reason":"legacy_noncash_without_immutable_grant" if noncash else "authority_zero_not_settleable",
                                "future_settlement":"blocked_until_separate_audited_payment_reconciliation"})
                continue
            if scale(old) <= 0:
                raise ValueError("pending grant rounds to zero; resolve this payment quote explicitly")
            pending.append({"source": source, "top_up_id": tid, "user_id": uid,
                            "original_credited_quota": old, "effective_credited_quota": scale(old)})
    if include_affiliate:
        rows = snapshot.get("referrals")
        if not isinstance(rows, list):
            raise ValueError("affiliate plan requires complete referrals snapshot, including revoked rewards")
        seen = set()
        for row in rows:
            source = dict(row)
            rid = safe_int(source.get("id"), "referral reward id")
            if rid <= 0 or rid in seen:
                raise ValueError("invalid or duplicate referral reward")
            seen.add(rid)
            for key in REFERRAL_NUMBERS:
                safe_int(source.get(key), "referral " + key)
            for key in ("status", "reason"):
                if not isinstance(source.get(key), str) or "\x00" in source[key]:
                    raise ValueError("referral requires exact state and reason")
            if source["inviter_id"] not in selected or source["status"] not in ("earned", "revoked"):
                raise ValueError("unselected inviter or unsupported referral lifecycle state")
            referrals.append({"source": source, "reward_id": rid, "user_id": source["inviter_id"],
                              "original_quota": source["quota"], "rebased_quota": scale(source["quota"]),
                              "rebased_revoked_quota": scale(source["revoked_quota"]),
                              "rebased_penalty_quota": scale(source["penalty_quota"])})
    return sorted(pending, key=lambda e: e["top_up_id"]), sorted(referrals, key=lambda e: e["reward_id"]), sorted(blocked,key=lambda e:e["top_up_id"])


def render_auxiliary(plan, schema, literal):
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    mid = literal(plan["migration_id"])
    ddl, checks, updates, inserts = [], [], [], []
    locks = []
    if plan["include_pending_topups"]:
        for key, kind, default in (("pending_credit_rebase_key", "varchar(128)", "''"), ("pending_credit_rebase_original_quota", "bigint", "0"), ("pending_credit_rebase_effective_quota", "bigint", "0")):
            ddl.append(f"ALTER TABLE {schema}.top_ups ADD COLUMN IF NOT EXISTS {key} {kind} NOT NULL DEFAULT {default};")
        count = len(plan['pending_bases']) + len(plan['blocked_pending_bases'])
        checks.append(f"IF (SELECT count(*) FROM {schema}.top_ups WHERE {RECOVERABLE_TOPUP_SQL} AND user_id=ANY(ARRAY[{selected}]::bigint[])) <> {count} THEN RAISE EXCEPTION 'pending or recoverable failed topup snapshot incomplete'; END IF;")
        for b in plan["pending_bases"] + plan["blocked_pending_bases"]:
            s = b["source"]
            clauses = [f"id={b['top_up_id']}", f"user_id={b['user_id']}", f"status={literal(s['status'])}", f"failure_reason_code={literal(s['failure_reason_code'])}"]
            clauses += [f"{key}={s[key]}" for key in TOPUP_NUMBERS]
            clauses += [f"{key}={literal(s[key])}" for key in TOPUP_TEXT]
            clauses += [f"money={literal(s['money'])}::double precision", "pending_credit_rebase_key=''", "pending_credit_rebase_original_quota=0", "pending_credit_rebase_effective_quota=0"]
            clauses.append(legacy_noncash_sql(literal) + ("" if s["is_legacy_linuxdo_credit_topup"] else " IS FALSE"))
            checks.append(f"IF NOT EXISTS (SELECT 1 FROM {schema}.top_ups WHERE " + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'pending quote fact or rebase metadata changed'; END IF;")
            updates.append(f"UPDATE {schema}.top_ups SET pending_credit_rebase_key={mid}, pending_credit_rebase_original_quota={b['original_credited_quota']}, pending_credit_rebase_effective_quota={b['effective_credited_quota']} WHERE id={b['top_up_id']};")
    if plan["include_affiliate"]:
        locks.append(f"{schema}.referral_rewards")
        ddl.append(f"""CREATE TABLE IF NOT EXISTS {schema}.wallet_referral_credit_rebases (
    reward_id bigint PRIMARY KEY, user_id bigint NOT NULL,
    migration_id text NOT NULL REFERENCES {schema}.wallet_credit_rebases(migration_id),
    original_quota bigint NOT NULL, divisor text NOT NULL, rounding text NOT NULL,
    rebased_quota bigint NOT NULL, rebased_revoked_quota bigint NOT NULL,
    rebased_penalty_quota bigint NOT NULL
);""")
        checks.append(f"IF (SELECT count(*) FROM {schema}.referral_rewards WHERE inviter_id=ANY(ARRAY[{selected}]::bigint[])) <> {len(plan['referral_bases'])} THEN RAISE EXCEPTION 'referral snapshot incomplete'; END IF;")
        for b in plan["referral_bases"]:
            s = b["source"]
            clauses = [f"id={b['reward_id']}"] + [f"{key}={s[key]}" for key in REFERRAL_NUMBERS] + [f"{key}={literal(s[key])}" for key in ("status", "reason")]
            checks.append(f"IF NOT EXISTS (SELECT 1 FROM {schema}.referral_rewards WHERE " + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'referral historical state or ownership changed'; END IF;")
            fields = ("reward_id", "user_id", "original_quota", "rebased_quota", "rebased_revoked_quota", "rebased_penalty_quota")
            inserts.append(f"INSERT INTO {schema}.wallet_referral_credit_rebases (migration_id,divisor,rounding," + ",".join(fields) + ") VALUES (" + mid + "," + literal(plan["divisor"]) + "," + literal(plan["rounding"]) + "," + ",".join(str(b[k]) for k in fields) + ");")
    return ddl, checks, updates, inserts, (", " + ", ".join(locks)) if locks else ""
