"""Explicit future-rights allowlist for the offline credit rebase planner."""

# Each writable field is named here. Historical tip/fee/used totals are never writable.
SPECS = {
    "redemptions": {
        "write": ("quota",),
        "int": ("user_id", "used_user_id", "quota", "status", "created_time", "redeemed_time", "expired_time"),
        "text": ("reward_type",), "null": ("deleted_at",)},
    "open_source_bounty_projects": {
        "write": ("escrow_quota", "reward_quota", "net_reward_quota"),
        "int": ("owner_user_id", "escrow_quota", "reward_quota", "net_reward_quota", "reward_slots", "platform_fee_quota", "platform_fee_rate_bps", "created_at", "updated_at", "published_at", "closed_at", "archived_at"),
        "text": ("status",), "null": ()},
    "open_source_bounty_challenges": {
        "write": ("reward_quota",),
        "int": ("project_id", "participant_user_id", "reward_quota", "tip_quota", "accepted_at", "submitted_at", "reviewed_at", "rejected_at", "paid_at", "created_at", "updated_at"),
        "text": ("status",), "null": ()},
}


def make_entities(snapshot, selected, scale, *, include_redemptions=False, include_bounties=False):
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
                raise ValueError(f"entitlement {table}.{key} must be exact nonnegative integer")
            source[key] = value
        for key in spec["text"]:
            value = row.get(key)
            if not isinstance(value, str) or "\x00" in value:
                raise ValueError(f"entitlement {table}.{key} must be exact text")
            source[key] = value
        for key in spec["null"]:
            if key not in row or row[key] is not None:
                raise ValueError("deleted or ambiguous entitlement must not migrate")
            source[key] = None
        entry = {"table": table, "id": rid, "source": source,
                 "updates": {key: {"before_credit": source[key], "after_credit": scale(source[key])}
                             for key in spec["write"]}}
        result.append(entry)
        return entry

    if include_redemptions:
        rows = entities.get("redemptions")
        if not isinstance(rows, list):
            raise ValueError("explicit usable redemptions array is required")
        for row in rows:
            e = add("redemptions", row)
            s = e["source"]
            if s["status"] != 1 or s["used_user_id"] != 0 or s["reward_type"] not in ("", "quota") or s["user_id"] not in selected or (s["expired_time"] and s["expired_time"] < at):
                raise ValueError("redemption is not a usable selected issuer's credit right")

    if include_bounties:
        projects, challenges = entities.get("bounty_projects"), entities.get("bounty_challenges")
        if not isinstance(projects, list) or not isinstance(challenges, list):
            raise ValueError("explicit active bounty projects and unpaid challenges arrays are required")
        pmap, owed = {}, {}
        for row in projects:
            e = add("open_source_bounty_projects", row)
            s = e["source"]
            if s["status"] not in ("published", "paused") or s["owner_user_id"] not in selected:
                raise ValueError("bounty is not an active selected owner's future right")
            pmap[s["id"]] = e
            owed[s["id"]] = 0
        for row in challenges:
            e = add("open_source_bounty_challenges", row)
            s = e["source"]
            if s["project_id"] not in pmap or s["participant_user_id"] not in selected or s["paid_at"] != 0 or s["status"] not in ("accepted", "submitted", "rejected"):
                raise ValueError("bounty challenge must be an unpaid selected participant's future right")
            owed[s["project_id"]] += e["updates"]["reward_quota"]["after_credit"]
        for pid, project in pmap.items():
            if owed[pid] > project["updates"]["escrow_quota"]["after_credit"]:
                raise ValueError("rounded bounty escrow cannot cover unpaid commitments; explicitly resolve tail allocation")
    return sorted(result, key=lambda e: (e["table"], e["id"]))


def render_entities(plan, schema, literal):
    checks, updates = [], []
    for e in plan["entity_updates"]:
        spec = SPECS[e["table"]]
        clauses = []
        for key, value in e["source"].items():
            if key != "id" and key not in spec["int"] + spec["text"] + spec["null"]:
                raise ValueError("unapproved entitlement CAS column")
            if value is None:
                clauses.append(f'"{key}" IS NULL')
            else:
                clauses.append(f'"{key}" = ' + (literal(value) if isinstance(value, str) else str(value)))
        where = " AND ".join(clauses)
        checks.append(f'IF NOT EXISTS (SELECT 1 FROM {schema}."{e["table"]}" WHERE {where}) THEN RAISE EXCEPTION \'entitlement state, owner or timestamp changed\'; END IF;')
        for field, change in e["updates"].items():
            if field not in spec["write"]:
                raise ValueError("unapproved entitlement writable column")
            if type(change["after_credit"]) is not int or not 0 <= change["after_credit"] <= (1 << 53) - 1:
                raise ValueError("invalid entitlement target credit integer")
            updates.append(f'UPDATE {schema}."{e["table"]}" SET "{field}"={change["after_credit"]} WHERE id={e["id"]};')
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    at = plan["snapshot_at"]
    if plan["include_redemptions"]:
        count = sum(e["table"] == "redemptions" for e in plan["entity_updates"])
        checks.append(f"IF (SELECT count(*) FROM {schema}.redemptions WHERE user_id=ANY(ARRAY[{selected}]::bigint[]) AND status=1 AND deleted_at IS NULL AND COALESCE(reward_type,'quota') IN ('','quota') AND (expired_time=0 OR expired_time>={at})) <> {count} THEN RAISE EXCEPTION 'usable redemption snapshot incomplete'; END IF;")
    if plan["include_bounties"]:
        projects = [e for e in plan["entity_updates"] if e["table"] == "open_source_bounty_projects"]
        pids = ",".join(str(e["id"]) for e in projects)
        checks.append(f"IF (SELECT count(*) FROM {schema}.open_source_bounty_projects WHERE owner_user_id=ANY(ARRAY[{selected}]::bigint[]) AND status IN ('published','paused')) <> {len(projects)} THEN RAISE EXCEPTION 'active bounty snapshot incomplete'; END IF;")
        if projects:
            count = sum(e["table"] == "open_source_bounty_challenges" for e in plan["entity_updates"])
            checks.append(f"IF (SELECT count(*) FROM {schema}.open_source_bounty_challenges WHERE project_id=ANY(ARRAY[{pids}]::bigint[]) AND status IN ('accepted','submitted','rejected') AND paid_at=0) <> {count} THEN RAISE EXCEPTION 'unpaid bounty snapshot incomplete'; END IF;")
    locks = []
    if plan["include_redemptions"]:
        locks.append(f"{schema}.redemptions")
    if plan["include_bounties"]:
        locks += [f"{schema}.open_source_bounty_projects", f"{schema}.open_source_bounty_challenges"]
    return checks, updates, (", " + ", ".join(locks)) if locks else ""
