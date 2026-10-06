"""Independent future payouts from immutable, safe bounty dispute evidence."""

from credit_rebase_other_rights import exact_source

KIND = "bounty_dispute_reward"
SOURCE_INTS = (
    "id", "challenge_id", "project_id", "opened_by_user_id", "against_user_id",
    "project_escrow_quota_snapshot", "reward_quota_snapshot", "tip_quota_snapshot",
    "resolved_by_user_id", "created_at", "updated_at", "resolved_at",
)
SOURCE_TEXT = ("challenge_status_snapshot", "status")
FUTURE_CHALLENGE_STATES = ("accepted", "submitted", "rejected")


def prepare(snapshot, selected, scale, *, include=False):
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
            if not isinstance(row, dict) or type(row.get("id")) is not int or row["id"] <= 0 or row["id"] in maps[key]:
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
        if type(uid) is not int or type(owner) is not int or uid <= 0 or owner <= 0 or uid == owner or uid not in selected or owner not in selected:
            raise ValueError("bounty dispute parties must be distinct selected users")
        if source["opened_by_user_id"] == owner and source["against_user_id"] == uid:
            continue  # An owner-filed dispute cannot authorize contributor payment.
        if source["opened_by_user_id"] != uid or source["against_user_id"] != owner:
            raise ValueError("bounty dispute claimant or respondent changed")
        if challenge.get("status") not in FUTURE_CHALLENGE_STATES or challenge.get("paid_at") != 0:
            continue
        original = source["reward_quota_snapshot"]
        if (source["created_at"] <= 0 or source["created_at"] > at or source["resolved_at"] != 0 or source["resolved_by_user_id"] != 0
                or original <= 0 or original != challenge.get("reward_quota")
                or project.get("status") not in ("published", "paused")
                or type(project.get("escrow_quota")) is not int or project["escrow_quota"] < original):
            raise ValueError("bounty dispute is inconsistent with its unpaid escrow reward")
        target = scale(original)
        if type(target) is not int or not 0 <= target <= original:
            raise ValueError("invalid bounty dispute target credit integer")
        bases.append({"kind": KIND, "source_id": str(source["id"]), "user_id": uid,
                      "original_quota": original, "rebased_quota": target, "source": source})
    return sorted(bases, key=lambda e: e["source_id"])


def sql(plan, schema, literal):
    bases = [e for e in plan.get("other_credit_bases", []) if e.get("kind") == KIND]
    if not plan.get("include_bounties"):
        if bases:
            raise ValueError("bounty dispute bases require the bounty migration scope")
        return [], [], ""
    if not plan.get("include_other_rights"):
        raise ValueError("bounty dispute future payouts require the other rights audit scope")
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    eligible = (f"p.owner_user_id=ANY(ARRAY[{selected}]::bigint[]) AND p.status IN ('published','paused') "
                f"AND c.participant_user_id=ANY(ARRAY[{selected}]::bigint[]) AND c.paid_at=0 "
                "AND c.status IN ('accepted','submitted','rejected') AND d.status='open' "
                "AND d.project_id=p.id AND d.opened_by_user_id=c.participant_user_id AND d.against_user_id=p.owner_user_id")
    joins = (f"{schema}.open_source_bounty_disputes d JOIN {schema}.open_source_bounty_challenges c ON c.id=d.challenge_id "
             f"JOIN {schema}.open_source_bounty_projects p ON p.id=c.project_id")
    checks = [f"IF (SELECT count(*) FROM {joins} WHERE {eligible}) <> {len(bases)} THEN RAISE EXCEPTION 'bounty dispute reward snapshot incomplete'; END IF;"]
    seen = set()
    for base in bases:
        source = exact_source(base["source"], SOURCE_INTS, SOURCE_TEXT)
        rid = source["id"]
        if rid <= 0 or rid in seen or base["source_id"] != str(rid) or base["user_id"] not in plan["user_ids"] or base["user_id"] != source["opened_by_user_id"] or base["original_quota"] != source["reward_quota_snapshot"]:
            raise ValueError("invalid bounty dispute reward basis")
        seen.add(rid)
        clauses = [f'd."{key}"=' + (literal(value) if isinstance(value, str) else str(value)) for key, value in source.items()]
        clauses += [eligible, f"c.participant_user_id={base['user_id']}", f"c.reward_quota={base['original_quota']}", f"p.escrow_quota>={base['original_quota']}"]
        checks.append(f"IF NOT EXISTS (SELECT 1 FROM {joins} WHERE " + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'bounty dispute credit source facts changed'; END IF;")
    locks = ", " + ", ".join(f'{schema}."{table}"' for table in ("open_source_bounty_projects", "open_source_bounty_challenges", "open_source_bounty_disputes"))
    return [], checks, locks
