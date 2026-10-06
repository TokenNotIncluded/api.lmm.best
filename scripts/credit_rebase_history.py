"""Complete wallet/token financial sources, including preserved usage history."""
USER_INTS = ("id", "quota", "aff_quota", "used_quota", "request_count", "aff_history", "aff_count", "status")
TOKEN_INTS = ("id", "user_id", "remain_quota", "used_quota", "status", "created_time", "accessed_time", "expired_time")


def prepare(snapshot, selected, integer):
    if "user_sources" not in snapshot and "token_sources" not in snapshot:
        return [], [], False
    result = {}
    for key, ints, bools in (("user_sources", USER_INTS, ()), ("token_sources", TOKEN_INTS, ("unlimited_quota",))):
        rows = snapshot.get(key)
        if not isinstance(rows, list):
            raise ValueError("complete explicit " + key + " is required")
        seen, sources = set(), []
        for row in rows:
            if not isinstance(row, dict) or set(row) != set(ints + bools + ("deleted_at",)):
                raise ValueError("historical source requires exactly the approved financial projection")
            source = {field: integer(row[field], key + " " + field) for field in ints}
            rid = source["id"]
            if rid <= 0 or rid in seen:
                raise ValueError("invalid or duplicate historical financial source")
            seen.add(rid)
            owner = rid if key == "user_sources" else source["user_id"]
            if owner not in selected:
                raise ValueError("historical source owner was not selected")
            for field in bools:
                if type(row[field]) is not bool:
                    raise ValueError("historical token flags require booleans")
                source[field] = row[field]
            deleted = row["deleted_at"]
            if deleted is not None and (not isinstance(deleted, str) or "\x00" in deleted):
                raise ValueError("deleted_at must be exact database timestamp text or null")
            source["deleted_at"] = deleted
            sources.append(source)
        result[key] = sorted(sources, key=lambda r: r["id"])
    users = {r["id"]: r for r in result["user_sources"]}
    if set(users) != selected:
        raise ValueError("selected user history snapshot incomplete")
    for user in snapshot["users"]:
        if user["id"] in selected and any(users[user["id"]][key] != user[key] for key in ("quota", "aff_quota")):
            raise ValueError("wallet balances and historical sources came from different facts")
    tokens = {r["id"]: r for r in result["token_sources"]}
    selected_tokens = {r["id"]: r for r in snapshot["tokens"] if r["user_id"] in selected}
    if set(tokens) != set(selected_tokens):
        raise ValueError("all selected token history snapshot incomplete")
    for tid, row in selected_tokens.items():
        if any(tokens[tid][key] != row[key] for key in ("user_id", "remain_quota", "unlimited_quota")):
            raise ValueError("token limits and history sources came from different facts")
    return result["user_sources"], result["token_sources"], True


def sql(plan, schema, literal):
    checks = []
    selected = ",".join(str(uid) for uid in plan["user_ids"])
    for table, key, owner in (("users", "user_sources", "id"), ("tokens", "token_sources", "user_id")):
        if plan.get("snapshot_all_users") is True:
            checks.append(f"IF (SELECT count(*) FROM {schema}.{table}) <> {len(plan[key])} THEN RAISE EXCEPTION 'complete wallet/token inventory changed'; END IF;")
        checks.append(f"IF (SELECT count(*) FROM {schema}.{table} WHERE {owner}=ANY(ARRAY[{selected}]::bigint[])) <> {len(plan[key])} THEN RAISE EXCEPTION 'wallet/token historical snapshot incomplete'; END IF;")
        for source in plan[key]:
            clauses = []
            for field, value in source.items():
                if value is None:
                    clauses.append('"' + field + '" IS NULL')
                else:
                    rhs = "true" if value is True else "false" if value is False else literal(value) if isinstance(value, str) else str(value)
                    clauses.append('"' + field + '"=' + rhs)
            checks.append(f"IF NOT EXISTS (SELECT 1 FROM {schema}.{table} WHERE " + " AND ".join(clauses) + ") THEN RAISE EXCEPTION 'wallet/token historical facts, flags or ownership changed'; END IF;")
    return checks
