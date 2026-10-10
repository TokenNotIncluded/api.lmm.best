"""Behavioral tests for private, complete and bounded feedback audits."""
import contextlib
import io
import json
from pathlib import Path
import stat
import tempfile
import unittest

from assistant_feedback_audit import anonymous, parser, read_page, scan


def message(conversation, sequence, role, content, owner="owner", identifier=None):
    return {"id": sequence if identifier is None else identifier, "conversation_id": conversation,
            "owner_key": owner, "sequence": sequence, "role": role, "content": content, "created_at": 123}


class AuditTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        self.source = self.root / "history.jsonl"
        self.output = self.root / "private"

    def tearDown(self):
        self.temporary.cleanup()

    def audit(self, messages, inventory=None, extra=()):
        self.source.write_text("".join(json.dumps(value, ensure_ascii=False)+"\n" for value in messages), encoding="utf-8")
        options = ["scan", "--input", str(self.source), "--output", str(self.output), *extra]
        if inventory is not None:
            path = self.root / "inventory.json"
            path.write_text(json.dumps(inventory), encoding="utf-8")
            options += ["--inventory", str(path)]
        return scan(parser().parse_args(options))

    def records(self, filename):
        return [json.loads(line) for line in (self.output / filename).read_text(encoding="utf-8").splitlines()]

    def test_sort_multi_turns_and_keep_different_answers(self):
        rows = [message("first", 4, "assistant", "done"), message("second", 1, "user", "same"),
                message("first", 2, "assistant", "cannot change"), message("first", 1, "user", "same"),
                message("second", 2, "assistant", "already changed"), message("first", 3, "user", "retry")]
        summary = self.audit(rows)
        self.assertEqual(summary["statistics"]["turns_after_deduplication"], 3)
        turns = self.records("all_unique_turns.jsonl")
        self.assertEqual({row["assistant"] for row in turns}, {"cannot change", "already changed", "done"})
        first = [row for row in turns if row["conversation_id"] == anonymous("first")]
        self.assertEqual(sorted(row["sequence_start"] for row in first), [1, 3])

    def test_normalized_dedup_counts_preserve_sources_and_sample_all_buckets(self):
        summary = self.audit([message("a", 1, "user", "HELLO  there"), message("a", 2, "assistant", "OK"),
                              message("b", 1, "user", "hello there", "another"), message("b", 2, "assistant", "ok", "another")])
        self.assertEqual(summary["statistics"]["conversations_after_deduplication"], 1)
        self.assertEqual(summary["statistics"]["turns_before_deduplication"], 2)
        turns = self.records("all_unique_turns.jsonl")
        self.assertEqual(len(turns), 1)
        self.assertEqual(turns[0]["occurrences"], 2)
        self.assertEqual(turns[0]["source_conversations"], 2)
        self.assertEqual(turns[0]["source_users"], 2)
        self.assertEqual(self.records("candidates.jsonl")[0]["candidate_reasons"], ["hash_bucket_sample"])
        shards = sum((self.records(f"review-{index:02d}.jsonl") for index in range(6)), [])
        self.assertEqual(len(shards), 1)
        self.assertEqual(shards[0]["occurrences"], 2)

    def test_consecutive_users_human_response_empty_answer_and_secure_card(self):
        summary = self.audit([message("a", 1, "user", "one"), message("a", 2, "user", "two"),
                              message("a", 3, "human", "support answered"), message("a", 4, "secure_card", '{"key":"plain-secret"}'),
                              message("a", 5, "user", "last"), message("a", 6, "assistant", "   ")])
        turns = self.records("all_unique_turns.jsonl")
        supported = next(row for row in turns if row["user"] == "one\ntwo")
        self.assertTrue(supported["answered"])
        self.assertFalse(supported["assistant_answered"])
        self.assertEqual(supported["human"], "support answered")
        unanswered = next(row for row in turns if row["user"] == "last")
        self.assertIn("unanswered_user_turn", unanswered["categories"])
        self.assertEqual(summary["category_unique_turn_counts"]["unanswered_user_turn"], 1)
        self.assertNotIn("plain-secret", (self.output / "conversation_fragments.jsonl").read_text())

    def test_redacts_every_artifact_and_repeated_nickname_in_answer(self):
        secrets = ["alice@example.invalid", "sk-abc123456789secret", "password-value", "203.0.113.9",
                   "+886912345678", "alice-private", "NamedPerson", "querySecret", "private-path"]
        text = "昵称改成 NamedPerson。 email alice@example.invalid; password: password-value; sk-abc123456789secret; "
        text += "203.0.113.9 +886912345678 https://host.invalid/private-path?token=querySecret /home/alice-private/file"
        self.audit([message("original-conversation", 1, "user", text, "real-username"),
                    message("original-conversation", 2, "assistant", "NamedPerson 已设置。API 密钥：尚未开通。")])
        for path in self.output.iterdir():
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            exported = path.read_text(encoding="utf-8")
            for secret in secrets+ ["real-username", "original-conversation"]:
                self.assertNotIn(secret, exported, (path.name, secret))
        self.assertEqual(stat.S_IMODE(self.output.stat().st_mode), 0o700)
        self.assertFalse(any(path.name.startswith(".audit-") for path in self.output.iterdir()))
        self.assertIn("尚未开通", self.records("all_unique_turns.jsonl")[0]["assistant"])

    def test_full_text_classification_bounded_fragments_and_review_budget(self):
        text = "界" * 3500 + " cannot do this " + "界" * 3500
        summary = self.audit([message("a", 1, "user", "request"), message("a", 2, "assistant", text)],
                             extra=["--turn-side-chars", "100", "--fragment-message-chars", "100", "--fragment-messages", "1"])
        turn = self.records("all_unique_turns.jsonl")[0]
        self.assertLessEqual(len(turn["assistant"]), 100)
        self.assertNotIn("cannot", turn["assistant"])
        self.assertIn("refusal_or_missing_capability", turn["categories"])
        fragments = self.records("conversation_fragments.jsonl")
        parts = [part for row in fragments for part in row["messages"] if part["role"] == "assistant"]
        self.assertEqual("".join(part["content"] for part in parts), text)
        self.assertGreater(summary["statistics"]["messages_split_into_parts"], 0)
        self.assertTrue(all(len(part["content"]) <= 100 for part in parts))
        reviews = sum((self.records(f"review-{index:02d}.jsonl") for index in range(6)), [])
        self.assertLessEqual(sum(len(row["content"]) for row in reviews[0]["messages"]), 3000)
        self.assertTrue(all(len(row["content"]) <= 1000 for row in reviews[0]["messages"]))

    def test_unprefixed_tokens_in_toml_and_personal_github_urls_are_redacted(self):
        token = "A9zK4m7Qp2Rw8Nv5Xe3Lb6Cs1Td0"
        prose_token = "n7Fa2Pc9Qx4Lm6Bd8Kz1Vr5Hs3Wu0"
        personal_url = "https://github.com/private-person/private-project?token=unlisted-secret#private-file"
        text = f"[model_providers.{token}]\nname = 'custom'\n{personal_url}\n{prose_token}"
        self.audit([message("a", 1, "user", text), message("a", 2, "assistant", text)])
        for path in self.output.iterdir():
            contents = path.read_text()
            for sensitive in (token, prose_token, "private-person", "private-project", "unlisted-secret", "private-file"):
                self.assertNotIn(sensitive, contents, path.name)
        self.assertIn("model_providers", self.records("all_unique_turns.jsonl")[0]["user"])

    def test_empty_assistant_still_separates_next_user_turn(self):
        summary = self.audit([message("a", 1, "user", "first"), message("a", 2, "assistant", ""),
                              message("a", 3, "user", "second"), message("a", 4, "assistant", "done")])
        self.assertEqual(summary["statistics"]["turns_before_deduplication"], 2)
        turns = self.records("all_unique_turns.jsonl")
        self.assertEqual({row["user"] for row in turns}, {"first", "second"})
        self.assertEqual(summary["category_unique_turn_counts"]["unanswered_user_turn"], 1)

    def test_quoted_authorization_and_status_like_passwords_are_still_secret(self):
        text = '{"Authorization":"Bearer short-secret", "password":"not-a-secret", "secret":"未公开值"}'
        self.audit([message("a", 1, "user", text)])
        for path in self.output.iterdir():
            for secret in ("short-secret", "not-a-secret", "未公开值"):
                self.assertNotIn(secret, path.read_text(), path.name)

    def test_quoted_multiword_password_and_multicookie_values_are_entirely_redacted(self):
        text = '{"password":"private-word-one private-word-two private-word-three private-word-four", "Cookie":"sid=abc; auth=short-secret"}'
        text += "\nCookie: session=another-secret; csrf=final-secret\n"
        self.audit([message("a", 1, "user", text)])
        for path in self.output.iterdir():
            for secret in ("private-word-one", "private-word-two", "private-word-three", "private-word-four", "sid=abc", "short-secret", "another-secret", "final-secret"):
                self.assertNotIn(secret, path.read_text(), path.name)

    def test_empty_history_inventory_and_all_shards_exist(self):
        summary = self.audit([], {"conversations": 60, "users": 12, "empty_conversations": 60, "private_username": "do-not-export"})
        self.assertEqual(summary["statistics"]["conversations_scanned"], 0)
        self.assertEqual(summary["statistics"]["turns_after_deduplication"], 0)
        self.assertEqual(summary["inventory"]["empty_conversations"], 60)
        self.assertNotIn("private_username", summary["inventory"])
        self.assertTrue(all((self.output / f"review-{index:02d}.jsonl").exists() for index in range(6)))

    def test_duplicate_sequence_missing_id_and_unknown_roles_are_explicit(self):
        rows = [message("a", 1, "assistant", "answer", identifier=20), message("a", 1, "user", "question", identifier=10),
                {"conversation_id": "b", "role": "unexpected", "content": "metadata"}]
        summary = self.audit(rows)
        self.assertEqual(summary["statistics"]["duplicate_sequence_positions"], 1)
        self.assertEqual(summary["statistics"]["missing_message_ids"], 1)
        self.assertEqual(summary["statistics"]["missing_or_invalid_sequences"], 1)
        self.assertEqual(summary["statistics"]["unknown_role_messages"], 1)
        self.assertEqual(self.records("all_unique_turns.jsonl")[0]["assistant"], "answer")

    def test_pagination_and_bucket_filter_are_bounded(self):
        self.audit([message(str(index), 1, "user", f"request {index}") for index in range(12)])
        args = parser().parse_args(["read", "--input", str(self.output / "all_unique_turns.jsonl"), "--offset", "3", "--limit", "2"])
        destination = io.StringIO()
        with contextlib.redirect_stdout(destination):
            read_page(args)
        self.assertEqual(len(destination.getvalue().splitlines()), 2)
        expected = self.records("all_unique_turns.jsonl")[3:5]
        self.assertEqual([json.loads(row) for row in destination.getvalue().splitlines()], expected)

    def test_large_conversation_review_is_bounded_but_fragments_preserve_all_messages(self):
        rows = [message("a", index, "user" if index % 2 else "assistant", f"text {index}") for index in range(1, 251)]
        summary = self.audit(rows)
        reviews = sum((self.records(f"review-{index:02d}.jsonl") for index in range(6)), [])
        self.assertEqual(len(reviews[0]["messages"]), 200)
        self.assertEqual(reviews[0]["omitted_review_messages"], 50)
        self.assertEqual(summary["statistics"]["review_omitted_messages"], 50)
        fragments = self.records("conversation_fragments.jsonl")
        self.assertEqual(sum(len(row["messages"]) for row in fragments), 250)
        self.assertEqual(summary["statistics"]["fragment_omitted_characters"], 0)

    def test_invalid_json_fails_without_including_raw_content_and_removes_scratch(self):
        self.source.write_text('{"content":"never-print-this-secret",invalid}\n')
        args = parser().parse_args(["scan", "--input", str(self.source), "--output", str(self.output)])
        with self.assertRaisesRegex(ValueError, "invalid JSON on input line 1") as caught:
            scan(args)
        self.assertNotIn("never-print", str(caught.exception))
        self.assertFalse(any(self.output.iterdir()))

    def test_read_refuses_raw_history_before_printing_content(self):
        self.source.write_text(json.dumps(message("a", 1, "user", "never-print-raw"))+"\n")
        args = parser().parse_args(["read", "--input", str(self.source)])
        destination = io.StringIO()
        with contextlib.redirect_stdout(destination), self.assertRaisesRegex(ValueError, "not raw history"):
            read_page(args)
        self.assertEqual(destination.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
