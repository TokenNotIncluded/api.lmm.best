#!/usr/bin/env python3
"""Private, streaming conversation audit. No model or network requests are made."""
from __future__ import annotations

import argparse
from collections import Counter
import hashlib
import json
import math
import os
from pathlib import Path
import re
import sqlite3
import tempfile
import unicodedata


RULES = {
    "refusal_or_missing_capability": r"无法|不能|做不到|不支持|没有(?:权限|能力|功能|工具)|无法直接|不能直接|can't|cannot|unable to|not supported|don't have access",
    "failure_or_error": r"失败|出错|报错|错误|超时|异常|没反应|没有反应|未成功|error|failed|timeout|timed out",
    "permission_or_application": r"权限|申请|授权|审批|确认|permission|authoriz|approval",
    "pricing_or_models": r"价格|定价|费用|计费|收费|多少钱|模型|model|pricing|price|cost",
    "payment_or_balance": r"支付|充值|退款|余额|到账|订单|账单|payment|refund|balance|top.?up",
    "nickname_or_profile": r"昵称|用户名|个人资料|头像|改名|更名|nickname|display.?name|profile|username",
    "experience_complaint": r"难用|不好用|不方便|太慢|很慢|卡住|(?:这|你|真|太|就是).{0,8}垃圾|垃圾(?:助手|平台|网站|回答|服务)|没用|敷衍|答非所问|骗人|胡说|不靠谱|为什么.*(?:不能|不行)|useless|frustrat|annoy|too slow",
    "support_handoff": r"人工|客服|联系.*(?:管理员|管理者)|support|contact.*admin",
    "assistant_self_limitation": r"我(?:目前|现在|暂时)?(?:无法|不能|没有)|我只能|无法直接(?:帮|为|替|修改|创建|删除|操作|执行)|不能直接(?:帮|为|替|修改|创建|删除|操作|执行)",
}
COMPILED_RULES = {name: re.compile(pattern, re.I) for name, pattern in RULES.items()}
NAME_PATTERNS = [
    re.compile(r"(?:昵称|用户名|姓名|名字)(?:\s*(?:改(?:成|为)|设(?:置)?(?:成|为)|换(?:成|为)|叫|是|为|[:：]))\s*[\"'“]?([^\s，。！!？?；;\"'”\n]{1,40})"),
    re.compile(r"(?:把|将)(?:我的|我)?(?:昵称|用户名|名字)\s*(?:改(?:成|为)|设(?:置)?(?:成|为)|换(?:成|为))\s*[\"'“]?([^\s，。！!？?；;\"'”\n]{1,40})"),
    re.compile(r"(?:my (?:name|nickname|username) is|(?:nickname|username|display name) (?:to|as))\s*[\"']?([\w.-]{1,40})", re.I),
]
REDACTIONS = [
    (re.compile(r"-----BEGIN [^-\n]*PRIVATE KEY-----.*?-----END [^-\n]*PRIVATE KEY-----", re.S), "[PRIVATE_KEY]"),
    (re.compile(r'''(?i)(?:authorization|api[_ -]?key|access[_ -]?token|refresh[_ -]?token|secret|password|passwd|cookie|session[_ -]?(?:id|key)|密码|密钥|令牌|验证码)\s*["']?\s*[:=：]\s*(?:"(?:\\[\s\S]|[^"\\])*"|'(?:\\[\s\S]|[^'\\])*')'''), "[QUOTED_CREDENTIAL_FIELD]"),
    (re.compile(r"(?im)\bcookie\s*[\"']?\s*[:=]\s*[^\r\n]+"), "cookie: [CREDENTIAL]"),
    (re.compile(r"\b(?:sk[-_]|sess-|gh[pousr]_|github_pat_|AKIA|AIza)[A-Za-z0-9_-]{8,}\b"), "[CREDENTIAL]"),
    (re.compile(r"\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b"), "[CREDENTIAL]"),
    (re.compile(r"(\[\s*model_providers\.)[A-Za-z0-9_-]{24,}(\s*\])", re.I), r"\1[CREDENTIAL]\2"),
    (re.compile(r"(?i)(?:authorization\s*[\"']?\s*[:=]\s*[\"']?\s*(?:bearer\s+|basic\s+)?)[^\s,;\"']+"), "authorization: [CREDENTIAL]"),
    (re.compile(r"(?i)(?:api[_ -]?key|access[_ -]?token|refresh[_ -]?token|secret|password|passwd|cookie|session[_ -]?(?:id|key)|密码|密钥|令牌|验证码)\s*[\"']?\s*[:=：]\s*[\"']?[^\s,;，；\"']+"), "[CREDENTIAL_FIELD]"),
    (re.compile(r"\b(?:https?|wss?|ftp)://[^\s<>\"'`）)]+", re.I), "[URL]"),
    (re.compile(r"\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b", re.I), "[EMAIL]"),
    (re.compile(r"(?<![\w])(?:\d{1,3}\.){3}\d{1,3}(?![\w])"), "[IP]"),
    (re.compile(r"(?<![\w])(?:[0-9a-f]{0,4}:){2,}[0-9a-f:]{0,39}(?![\w])", re.I), "[IP]"),
    (re.compile(r"(?:/home/|/Users/|[A-Z]:\\Users\\)[^\s\"'<>]+", re.I), "[PERSONAL_PATH]"),
    (re.compile(r"(?<!\w)\+?\d(?:[ ()-]*\d){6,}(?!\w)"), "[NUMBER]"),
    (re.compile(r"\b[A-Za-z0-9+/=_-]{40,}\b"), "[OPAQUE_VALUE]"),
]


def dumps(value):
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def fingerprint(text):
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


def normalize(text):
    return " ".join(unicodedata.normalize("NFKC", text).casefold().split())


def anonymous(value):
    # Input owner_key can itself be a weak hash. This adds a task namespace,
    # not a claim of cryptographic anonymity against guessing/auxiliary data.
    return fingerprint("assistant-feedback-v1:" + str(value))[:16]


def redact(text):
    for pattern in NAME_PATTERNS:
        text = pattern.sub(lambda match: match.group(0)[:match.start(1)-match.start(0)] + "[NAME]", text)
    for pattern, replacement in REDACTIONS:
        if replacement == "[CREDENTIAL_FIELD]":
            # Ordinary status descriptions such as "API 密钥：尚未开通"
            # are useful evidence, not credentials.
            def field(match):
                prefix, value = re.split(r"[:=：]", match.group(0), maxsplit=1)
                value = value.strip(" \"'。.*`：:")
                statuses = {"未开通", "尚未开通", "已开通", "未开启", "已开启", "已关闭", "未创建", "暂不可用"}
                return match.group(0) if re.search(r"密钥|api[_ -]?key", prefix, re.I) and value in statuses else replacement
            text = pattern.sub(field, text)
        else:
            text = pattern.sub(replacement, text)
    # Users sometimes paste an unlabeled API token in a TOML section name or
    # ordinary prose. Preserve normal long identifiers, mask high-entropy
    # mixed alphanumeric strings even when they lack a vendor key prefix.
    def opaque(match):
        value = match.group(0)
        entropy = -sum((count/len(value))*math.log2(count/len(value)) for count in Counter(value).values())
        classes = sum((bool(re.search(r"[a-z]", value)), bool(re.search(r"[A-Z]", value)), bool(re.search(r"[0-9]", value))))
        return "[OPAQUE_VALUE]" if classes >= 2 and entropy >= 3.5 else value
    return re.sub(r"\b[A-Za-z0-9_-]{24,}\b", opaque, text)


def identity_values(text):
    return {match.group(1) for pattern in NAME_PATTERNS for match in pattern.finditer(text)
            if match.group(1) not in ("[NAME]", "什么", "什么？", "如何", "我的", "你的", "自己")}


def redact_identities(text, names):
    for name in names:
        if re.fullmatch(r"[\w.-]+", name, flags=re.ASCII):
            text = re.sub(r"(?<![\w.-])"+re.escape(name)+r"(?![\w.-])", "[NAME]", text, flags=re.I)
        else:
            text = text.replace(name, "[NAME]")
    return text


def clipped(text, limit):
    if len(text) <= limit:
        return text, 0
    if limit <= 0:
        return "", len(text)
    # The head and tail retain both the request and the eventual result.
    marker = " […] "
    if limit <= len(marker):
        return text[:limit], len(text)-limit
    head = (limit-len(marker))*2//3
    tail = limit-len(marker)-head
    return text[:head]+marker+text[-tail:] if tail else text[:head]+marker, len(text)-(head+tail)


def private_file(path):
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC | os.O_NOFOLLOW, 0o600)
    os.fchmod(fd, 0o600)
    return os.fdopen(fd, "w", encoding="utf-8")


def signal_categories(user, assistant, answered):
    categories = [name for name, pattern in COMPILED_RULES.items()
                  if pattern.search(assistant if name == "assistant_self_limitation" else user+"\n"+assistant)]
    if not answered:
        categories.append("unanswered_user_turn")
    return categories


def create_database(connection):
    connection.executescript("""
      CREATE TABLE messages (conversation TEXT, sequence INTEGER, identifier TEXT,
        ordinal INTEGER, owner TEXT, role TEXT, content TEXT, chars INTEGER);
      CREATE INDEX message_order ON messages(conversation, sequence, identifier, ordinal);
      CREATE TABLE identities (conversation TEXT, value TEXT, UNIQUE(conversation,value));
      CREATE TABLE conversations (hash TEXT PRIMARY KEY, payload TEXT, occurrences INTEGER);
      CREATE TABLE turns (hash TEXT PRIMARY KEY, bucket INTEGER, payload TEXT, occurrences INTEGER);
      CREATE TABLE sources (kind TEXT, hash TEXT, conversation TEXT, owner TEXT,
        UNIQUE(kind,hash,conversation,owner));
    """)


def ingest(connection, source):
    stats = Counter()
    with source.open(encoding="utf-8") as stream:
        for ordinal, line in enumerate(stream, 1):
            if not line.strip():
                stats["blank_input_lines"] += 1
                continue
            try:
                row = json.loads(line)
            except (ValueError, UnicodeError):
                # Never include source lines or JSON exception text in errors.
                raise ValueError(f"invalid JSON on input line {ordinal}") from None
            if not isinstance(row, dict) or "conversation_id" not in row:
                raise ValueError(f"missing conversation_id on input line {ordinal}")
            content = row.get("content", "")
            if not isinstance(content, str):
                raise ValueError(f"content must be a string on input line {ordinal}")
            role = row.get("role", "")
            if role not in ("user", "assistant", "system", "tool", "human", "secure_card"):
                role = "other"
                stats["unknown_role_messages"] += 1
            sequence = row.get("sequence")
            if isinstance(sequence, bool) or not isinstance(sequence, int):
                sequence = ordinal
                stats["missing_or_invalid_sequences"] += 1
            identifier = row.get("id")
            if identifier is None:
                identifier = ordinal
                stats["missing_message_ids"] += 1
            # Numeric ids sort naturally even for same sequence; other ids are
            # deterministic strings and never appear in exported records.
            identifier = f"n:{identifier:024d}" if isinstance(identifier, int) else "s:"+str(identifier)
            conversation_id = anonymous(row["conversation_id"])
            if role in ("user", "human"):
                for value in identity_values(content):
                    connection.execute("INSERT OR IGNORE INTO identities VALUES (?,?)", (conversation_id, value))
            sanitized = "[SECURE_CARD_OMITTED]" if role == "secure_card" else redact(content)
            connection.execute("INSERT INTO messages VALUES (?,?,?,?,?,?,?,?)", (
                conversation_id, sequence, identifier, ordinal,
                anonymous(row.get("owner_key", "unknown")), role, sanitized, len(content)))
            stats["messages_scanned"] += 1
            stats[f"role_{role}"] += 1
            stats["input_characters"] += len(content)
            stats["redacted_messages"] += sanitized != content
    connection.commit()
    stats["conversations_scanned"] = connection.execute("SELECT count(DISTINCT conversation) FROM messages").fetchone()[0]
    stats["users_scanned"] = connection.execute("SELECT count(DISTINCT owner) FROM messages").fetchone()[0]
    stats["duplicate_sequence_positions"] = connection.execute("SELECT coalesce(sum(n-1),0) FROM (SELECT count(*) n FROM messages GROUP BY conversation,sequence)").fetchone()[0]
    stats["duplicate_message_ids"] = connection.execute("SELECT coalesce(sum(n-1),0) FROM (SELECT count(*) n FROM messages GROUP BY conversation,identifier)").fetchone()[0]
    return stats


def add_unique(connection, table, hash_value, payload, bucket=None):
    for owner in payload["user_id"]:
        connection.execute("INSERT OR IGNORE INTO sources VALUES (?,?,?,?)", (table, hash_value, payload["conversation_id"], owner))
    existing = connection.execute(f"SELECT occurrences FROM {table} WHERE hash=?", (hash_value,)).fetchone()
    if existing:
        connection.execute(f"UPDATE {table} SET occurrences=occurrences+1 WHERE hash=?", (hash_value,))
    elif table == "turns":
        connection.execute("INSERT INTO turns VALUES (?,?,?,1)", (hash_value, bucket, dumps(payload)))
    else:
        connection.execute("INSERT INTO conversations VALUES (?,?,1)", (hash_value, dumps(payload)))


def process_conversation(connection, conversation, owners, args, stats, fragments):
    rows = connection.execute("SELECT sequence,role,content,chars FROM messages WHERE conversation=? ORDER BY sequence,identifier,ordinal", (conversation,))
    conversation_hash = hashlib.sha256()
    user_hash = hashlib.sha256()
    assistant_hash = hashlib.sha256()
    human_hash = hashlib.sha256()
    pending = None
    fragment = []
    fragment_index = 0
    review = []
    count = connection.execute("SELECT count(*) FROM messages WHERE conversation=? AND role IN ('user','assistant','human')", (conversation,)).fetchone()[0]
    names = [row[0] for row in connection.execute("SELECT value FROM identities WHERE conversation=? ORDER BY length(value) DESC LIMIT 128", (conversation,))]
    # Fair allocation gives every message a visible slot in a bounded review
    # conversation. Complete bounded fragments remain available separately.
    review_slots = min(count, args.review_max_messages)
    allowance = min(args.review_message_chars, args.review_conversation_chars//max(review_slots, 1))
    review_head = (args.review_max_messages+1)//2
    review_tail = args.review_max_messages//2
    review_index = 0
    omitted_review_messages = 0

    def flush_turn():
        nonlocal pending
        if pending is None:
            return
        turn_hash = fingerprint(dumps([user_hash.hexdigest(), assistant_hash.hexdigest(), human_hash.hexdigest()]))
        payload = {"turn_id": turn_hash, "conversation_id": conversation,
                   "user_id": owners, "sequence_start": pending["start"],
                   "sequence_end": pending["end"], "user": pending["user"],
                   "assistant": pending["assistant"], "answered": pending["answered"],
                   "human": pending["human"], "assistant_answered": pending["assistant_answered"],
                   "omitted_characters": pending["omitted"],
                   "signal_sources": {key: sorted(value) for key, value in pending["sources"].items()},
                   "categories": signal_categories(pending["user"], pending["assistant"], pending["answered"])}
        # Classification inspects all text while building the turn, not merely
        # the clipped exported snippet.
        payload["categories"] = sorted(pending["categories"] | set(payload["categories"]))
        add_unique(connection, "turns", turn_hash, payload, int(turn_hash[:8], 16) % args.buckets)
        stats["turns_before_deduplication"] += 1
        stats["turn_export_omitted_characters"] += pending["omitted"]
        pending = None

    def append_turn(role, content, sequence):
        nonlocal pending, user_hash, assistant_hash, human_hash
        if role == "user":
            # Consecutive user messages are one unresolved request. A user
            # after any assistant message starts the next request.
            if pending and pending["has_response"]:
                flush_turn()
            if pending is None:
                user_hash, assistant_hash, human_hash = hashlib.sha256(), hashlib.sha256(), hashlib.sha256()
                pending = {"start": sequence, "end": sequence, "user": "", "assistant": "", "human": "", "answered": False, "assistant_answered": False, "has_response": False, "omitted": 0, "categories": set(), "sources": {}}
        elif pending is None:
            stats[f"{role}_messages_without_preceding_user"] += 1
            return
        target_hash = {"user": user_hash, "assistant": assistant_hash, "human": human_hash}[role]
        target_hash.update(dumps(normalize(content)).encode("utf-8"))
        pending["end"] = sequence
        pending["answered"] |= role in ("assistant", "human") and bool(content.strip())
        pending["has_response"] |= role in ("assistant", "human")
        pending["assistant_answered"] |= role == "assistant" and bool(content.strip())
        pending["categories"].update(signal_categories(content if role == "user" else "", content if role == "assistant" else "", True))
        if role in ("user", "assistant"):
            for name, pattern in COMPILED_RULES.items():
                if (name != "assistant_self_limitation" or role == "assistant") and pattern.search(content):
                    pending["sources"].setdefault(name, set()).add(role)
        previous = pending[role]
        budget = max(0, args.turn_side_chars-len(previous)-(1 if previous else 0))
        value, omitted = clipped(content, budget)
        pending[role] = previous+("\n" if previous and value else "")+value
        pending["omitted"] += omitted

    for sequence, role, content, original_chars in rows:
        before_identity_redaction = content
        content = redact_identities(content, names)
        stats["identity_redacted_messages"] += content != before_identity_redaction
        conversation_hash.update(dumps([role, normalize(content)]).encode("utf-8"))
        parts = max(1, (len(content)+args.fragment_message_chars-1)//args.fragment_message_chars)
        stats["fragment_message_parts"] += parts
        stats["messages_split_into_parts"] += parts > 1
        for part in range(parts):
            value = content[part*args.fragment_message_chars:(part+1)*args.fragment_message_chars]
            fragment.append({"role": role, "sequence": sequence, "content": value,
                             "message_part": part, "message_parts": parts,
                             "original_characters": original_chars, "omitted_characters": 0})
            if len(fragment) == args.fragment_messages:
                fragments.write(dumps({"conversation_id": conversation, "fragment": fragment_index, "messages": fragment})+"\n")
                fragment, fragment_index = [], fragment_index+1
        if role in ("user", "assistant", "human"):
            if count <= args.review_max_messages or review_index < review_head or review_index >= count-review_tail:
                value, omitted = clipped(content, allowance)
                review.append({"role": role, "sequence": sequence, "content": value,
                               "omitted_characters": omitted})
                stats["review_omitted_characters"] += omitted
                stats["review_truncated_messages"] += omitted > 0
            else:
                omitted_review_messages += 1
                stats["review_omitted_messages"] += 1
                stats["review_omitted_characters"] += len(content)
            review_index += 1
            append_turn(role, content, sequence)
    flush_turn()
    if fragment:
        fragments.write(dumps({"conversation_id": conversation, "fragment": fragment_index, "messages": fragment})+"\n")
    add_unique(connection, "conversations", conversation_hash.hexdigest(), {
        "conversation_id": conversation, "user_id": owners, "messages": review,
        "original_review_message_count": count, "omitted_review_messages": omitted_review_messages,
        "review_character_budget": args.review_conversation_chars,
    })


def export(connection, args, stats, inventory):
    output = args.output
    categories = Counter()
    raw_categories = Counter()
    source_categories = Counter()
    examples = {}
    buckets = Counter()

    def source_counts(kind, hash_value):
        conversations, users = connection.execute("SELECT count(DISTINCT conversation),count(DISTINCT owner) FROM sources WHERE kind=? AND hash=?", (kind, hash_value)).fetchone()
        return {"source_conversations": conversations, "source_users": users}

    with private_file(output / "all_unique_turns.jsonl") as all_turns, private_file(output / "candidates.jsonl") as candidates:
        for hash_value, bucket, serialized, occurrences in connection.execute("SELECT hash,bucket,payload,occurrences FROM turns ORDER BY hash"):
            payload = json.loads(serialized)
            payload.update(occurrences=occurrences, bucket=bucket)
            payload.update(source_counts("turns", hash_value))
            all_turns.write(dumps(payload)+"\n")
            tags = payload["categories"]
            reasons = []
            if tags:
                reasons.append("rule_signal")
            if buckets[bucket] < args.samples_per_bucket:
                reasons.append("hash_bucket_sample")
                buckets[bucket] += 1
            if reasons:
                candidates.write(dumps(dict(payload, candidate_reasons=reasons))+"\n")
                stats["candidate_turns"] += 1
            for category in tags:
                categories[category] += 1
                raw_categories[category] += occurrences
                for source in payload["signal_sources"].get(category, []):
                    source_categories[f"{category}:{source}"] += 1
                destination = examples.setdefault(category, [])
                if len(destination) < args.examples_per_category:
                    user, _ = clipped(payload["user"], args.example_chars)
                    assistant, _ = clipped(payload["assistant"], args.example_chars)
                    destination.append({"turn_id": hash_value, "conversation_id": payload["conversation_id"],
                                        "occurrences": occurrences, "user": user, "assistant": assistant,
                                        "answered": payload["answered"]})
    shard_files = [private_file(output / f"review-{index:02d}.jsonl") for index in range(args.review_shards)]
    shard_counts = Counter()
    try:
        for index, (hash_value, serialized, occurrences) in enumerate(connection.execute("SELECT hash,payload,occurrences FROM conversations ORDER BY hash")):
            shard = index % args.review_shards
            payload = json.loads(serialized)
            payload.update(conversation_hash=hash_value, occurrences=occurrences)
            payload.update(source_counts("conversations", hash_value))
            shard_files[shard].write(dumps(payload)+"\n")
            shard_counts[shard] += 1
    finally:
        for stream in shard_files:
            stream.close()
    stats["turns_after_deduplication"] = connection.execute("SELECT count(*) FROM turns").fetchone()[0]
    stats["duplicate_turns_removed"] = stats["turns_before_deduplication"]-stats["turns_after_deduplication"]
    stats["conversations_after_deduplication"] = connection.execute("SELECT count(*) FROM conversations").fetchone()[0]
    stats["duplicate_conversations_removed"] = stats["conversations_scanned"]-stats["conversations_after_deduplication"]
    summary = {"schema_version": 1, "scope": "all supplied retained messages; rules are signals, not verified failures",
               "statistics": dict(stats), "inventory": inventory,
               "category_unique_turn_counts": dict(categories), "category_original_turn_counts": dict(raw_categories),
               "category_signal_source_counts": dict(source_categories),
               "category_examples": examples, "hash_bucket_sample_counts": dict(buckets),
               "review_shard_conversation_counts": dict(shard_counts),
               "limits": {"turn_side_characters": args.turn_side_chars, "review_message_characters": args.review_message_chars,
                          "review_conversation_characters": args.review_conversation_chars, "fragment_message_characters": args.fragment_message_chars,
                          "fragment_messages": args.fragment_messages, "examples_per_category": args.examples_per_category,
                          "review_max_messages": args.review_max_messages,
                          "example_characters": args.example_chars, "hash_buckets": args.buckets},
               "limitations": ["Retained history is not deleted/expired or never-saved history.",
                               "Heuristic redaction cannot identify every name or personal detail; outputs remain private.",
                               "Review shards clip text; use bounded fragments/turn pages for full context.",
                               "An unanswered retained turn does not prove the assistant failed (cancellation, streaming or retention may explain it).",
                               "Rule hits do not establish whether a limitation/permission boundary was correct.",
                               "Deduplication uses normalized redacted complete content; metadata and personal values are excluded."]}
    with private_file(output / "summary.json") as stream:
        json.dump(summary, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    return summary


def scan(args):
    args.output.mkdir(mode=0o700, parents=True, exist_ok=True)
    if args.output.is_symlink():
        raise ValueError("output directory must not be a symlink")
    os.chmod(args.output, 0o700)
    inventory = {}
    if args.inventory:
        raw = json.loads(args.inventory.read_text(encoding="utf-8"))
        inventory = {key: value for key, value in raw.items()
                     if key in ("conversations", "users", "empty_conversations", "first_created_at", "last_updated_at", "snapshot_at") and isinstance(value, int)}
    with tempfile.TemporaryDirectory(prefix=".audit-", dir=args.output) as temporary:
        database = Path(temporary) / "audit.sqlite"
        descriptor = os.open(database, os.O_CREAT | os.O_EXCL | os.O_RDWR, 0o600)
        os.close(descriptor)
        connection = sqlite3.connect(database)
        try:
            create_database(connection)
            stats = ingest(connection, args.input)
            stats["fragment_omitted_characters"] = 0
            with private_file(args.output / "conversation_fragments.jsonl") as fragments:
                for conversation, owners in connection.execute("SELECT conversation,group_concat(DISTINCT owner) FROM messages GROUP BY conversation ORDER BY conversation"):
                    process_conversation(connection, conversation, owners.split(","), args, stats, fragments)
            connection.commit()
            return export(connection, args, stats, inventory)
        finally:
            connection.close()


def read_page(args):
    selected = emitted = 0
    with args.input.open(encoding="utf-8") as stream:
        for line in stream:
            row = json.loads(line)
            if not isinstance(row, dict) or not ("turn_id" in row or "fragment" in row or "conversation_hash" in row):
                raise ValueError("read accepts audit output files, not raw history")
            if args.bucket is not None and row.get("bucket") != args.bucket:
                continue
            if args.conversation is not None and row.get("conversation_id") != args.conversation:
                continue
            if selected >= args.offset:
                if emitted == args.limit:
                    break
                print(dumps(row))
                emitted += 1
            selected += 1


def parser():
    result = argparse.ArgumentParser(description=__doc__)
    subcommands = result.add_subparsers(dest="command", required=True)
    audit = subcommands.add_parser("scan", help="scan all supplied JSONL; output private redacted files")
    audit.add_argument("--input", type=Path, required=True)
    audit.add_argument("--inventory", type=Path)
    audit.add_argument("--output", type=Path, required=True)
    for name, default in (("turn-side-chars", 2400), ("fragment-message-chars", 2000), ("fragment-messages", 8),
                          ("review-message-chars", 1000), ("review-conversation-chars", 3000), ("review-max-messages", 200), ("review-shards", 6),
                          ("buckets", 16), ("samples-per-bucket", 8), ("examples-per-category", 8), ("example-chars", 400)):
        audit.add_argument("--"+name, type=int, default=default)
    page = subcommands.add_parser("read", help="read a bounded page of unique turns or conversation fragments")
    page.add_argument("--input", type=Path, required=True)
    page.add_argument("--bucket", type=int)
    page.add_argument("--conversation")
    page.add_argument("--offset", type=int, default=0)
    page.add_argument("--limit", type=int, default=8)
    return result


def main():
    args = parser().parse_args()
    try:
        if args.command == "read":
            if args.limit < 1 or args.limit > 50 or args.offset < 0:
                raise ValueError("read limit must be 1..50 and offset non-negative")
            read_page(args)
        else:
            if any(value < 1 for key, value in vars(args).items() if isinstance(value, int)):
                raise ValueError("all output limits must be positive")
            if args.examples_per_category > 8 or args.review_message_chars > 1000 or args.review_conversation_chars > 3000:
                raise ValueError("example/review limits exceed the privacy defaults")
            summary = scan(args)
            print(dumps({"statistics": summary["statistics"], "category_unique_turn_counts": summary["category_unique_turn_counts"],
                         "review_shard_conversation_counts": summary["review_shard_conversation_counts"]}))
    except (ValueError, OSError, sqlite3.Error) as error:
        # Error messages from external libraries can contain input/path data.
        message = str(error) if type(error) is ValueError else type(error).__name__
        raise SystemExit("audit failed: "+message) from None


if __name__ == "__main__":
    main()
