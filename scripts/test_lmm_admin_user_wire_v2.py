"""Author regressions against synthetic stdlib HTTP, never actual Core/PG."""
from __future__ import annotations

import copy
import hashlib
import io
import json
import os
import stat
import sys
import tempfile
import unittest
import urllib.parse
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from test_lmm_admin import A, CAPABILITIES, CONFIG, OP, RESULT, TOKEN, USER, missing, preview, preview_request, server

BIG = 9007199254740993
IDS = (BIG, BIG + 1, A.MAX_I64)


def user(uid, *, revision=3, marked=False):
    return {**USER, "user_id": str(uid), "revision": revision, "marked": marked,
            "policy_id": "test" if marked else None}


def users_receipt(ids=IDS):
    return {**RESULT, "users": [user(uid, revision=4, marked=True) for uid in ids]}


def user_payload(ids=IDS, *, version=2):
    request = preview_request()
    request["targets"] = [{"user_id": str(uid) if version == 2 else uid, "expected_revision": 3} for uid in ids]
    value = preview()
    value["version"] = version
    value["targets"] = [{**copy.deepcopy(value["targets"][0]), "user_id": row["user_id"]}
                        for row in request["targets"]]
    return {"request": request, "preview": value}


def policy_receipt():
    return {"version": 1, "status": "applied", "operation_id": OP, "kind": "policies", "policy_revision": 5,
            "users": [], "applied_config_hash": A.policy_config_hash(CONFIG)}


class UserWireV2Tests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix="lmm-admin-user-v2-")
        self.root = Path(self.temp.name)
        self.sequence = 0

    def tearDown(self):
        self.temp.cleanup()

    def args(self, url, *values):
        return A.parser().parse_args(["--server", url, "--allow-insecure-loopback", *values])

    def invoke(self, url, *values):
        with patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}):
            return A.run(self.args(url, *values))

    def artifact(self, url, *, version=2, payload=None):
        self.sequence += 1
        path = self.root / f"preview-{self.sequence}.json"
        value = {"artifact_version": version, "server": url, "kind": "users", "operation_id": OP,
                 "payload": user_payload(version=version) if payload is None else payload}
        digest = A.private_write(str(path), value)
        return path, SimpleNamespace(preview=str(path), confirm=digest, apply=True)

    def test_canonical_positive_i64_strings_and_strict_numeric_rejection(self):
        for uid in (1, 7, BIG - 1, *IDS):
            self.assertEqual(A.user_id_string(str(uid)), str(uid))
        for invalid in (7, True, 7.0, None, "", "0", "01", "+7", "-7", " 7", "7 ", "7\n", "7.0", "7e0", "７", "٧",
                        str(A.MAX_I64 + 1), "1" * 20, [], {}):
            with self.subTest(value=invalid), self.assertRaises(A.ClientError):
                A.user_id_string(invalid)

    def test_cli_i64_bounds_are_checked_before_credentials_or_network(self):
        cases = [("users", "inspect", "0"), ("users", "inspect", str(A.MAX_I64 + 1)),
                 ("users", "list", "--after", "-1"), ("users", "list", "--after", str(A.MAX_I64 + 1)),
                 ("honeypot", "preview", "--user-id", str(A.MAX_I64 + 1), "--action", "mark", "--policy-id", "test",
                  "--reason-stdin", "--out", str(self.root / "forbidden.json"))]
        for values in cases:
            with self.subTest(values=values), patch.object(A, "session_token", side_effect=AssertionError("credential read")), \
                    patch.object(A, "Client", side_effect=AssertionError("network client")), self.assertRaises(A.ClientError):
                A.run(self.args("http://127.0.0.1:1", *values))

    def test_large_ids_and_cursor_are_exact_strings_and_first_page_omits_after(self):
        response = {"version": 2, "users": [user(uid) for uid in IDS], "next_after": str(A.MAX_I64)}
        with server(lambda *_: (200, response, {})) as (url, seen):
            result = self.invoke(url, "users", "list", "--after", "0")
        self.assertEqual([row["user_id"] for row in result["users"]], list(map(str, IDS)))
        self.assertEqual(result["next_after"], str(A.MAX_I64))
        self.assertNotIn("after", urllib.parse.parse_qs(urllib.parse.urlsplit(seen[0][1]).query))
        self.assertNotIn(USER["email"], json.dumps(result))
        self.assertTrue(seen[0][1].startswith(A.USERS_API + "/users?"))

    def test_user_page_order_is_numeric_not_lexicographic(self):
        ids = (2, 10, BIG)
        with server(lambda *_: (200, {"version": 2, "users": [user(uid) for uid in ids], "next_after": None}, {})) as (url, _):
            self.assertEqual([row["user_id"] for row in self.invoke(url, "users", "list")["users"]], ["2", "10", str(BIG)])
        for invalid in ((10, 2), (BIG, BIG), (BIG + 1, BIG)):
            with self.subTest(ids=invalid), server(lambda *_: (200, {"version": 2, "users": [user(uid) for uid in invalid], "next_after": None}, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "users", "list")
                self.assertEqual(len(seen), 1)

    def test_cursor_is_required_nullable_strict_and_bound_to_last_user(self):
        base = {"version": 2, "users": [user(BIG + 1)], "next_after": str(BIG + 1)}
        variants = [{key: value for key, value in base.items() if key != "next_after"},
                    {**base, "next_after": BIG + 1}, {**base, "next_after": "0"},
                    {**base, "next_after": str(BIG)}, {**base, "users": []}]
        for value in variants:
            with self.subTest(response=value), server(lambda *_: (200, value, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "users", "list", "--after", str(BIG))
                self.assertEqual(len(seen), 1)
        with server(lambda *_: (200, base, {})) as (url, seen):
            self.invoke(url, "users", "list", "--after", str(BIG))
        self.assertEqual(urllib.parse.parse_qs(urllib.parse.urlsplit(seen[0][1]).query)["after"], [str(BIG)])

    def test_v2_list_detail_preview_and_receipt_reject_even_small_numeric_ids(self):
        for invalid in (7, True, 7.0):
            with self.subTest(id=invalid):
                with server(lambda *_: (200, {"version": 2, "users": [{**USER, "user_id": invalid}], "next_after": None}, {})) as (url, seen):
                    with self.assertRaises(A.ClientError):
                        self.invoke(url, "users", "list")
                    self.assertEqual(len(seen), 1)
                with server(lambda *_: (200, {"version": 2, **USER, "user_id": invalid}, {})) as (url, seen):
                    with self.assertRaises(A.ClientError):
                        self.invoke(url, "users", "inspect", "7")
                    self.assertEqual(len(seen), 1)
                value = preview(); value["targets"][0]["user_id"] = invalid
                with self.assertRaises(A.ClientError):
                    A.check_preview(value, preview_request())
                with self.assertRaises(A.ClientError):
                    A.safe_operation({**RESULT, "users": [{**RESULT["users"][0], "user_id": invalid}]}, OP)

    def test_detail_requires_flat_v2_marker_and_closed_nullable_fields(self):
        valid = {"version": 2, **user(BIG)}
        variants = [user(BIG), {**valid, "version": 1}, {**valid, "version": True},
                    {**valid, "hidden": "synthetic secret"}, {key: value for key, value in valid.items() if key != "policy_id"},
                    {**valid, "policy_id": 7}, {**valid, "email": None}, {**valid, "revision": "3"}]
        for value in variants:
            with self.subTest(response=value), server(lambda *_: (200, value, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "users", "inspect", str(BIG))
                self.assertEqual(len(seen), 1)
        with server(lambda *_: (200, valid, {})) as (url, seen):
            output = self.root / "detail.json"
            result = self.invoke(url, "users", "inspect", str(BIG), "--out", str(output))
        self.assertEqual(seen[0][1], A.USERS_API + "/users/" + str(BIG))
        self.assertEqual(result["user_id"], str(BIG))
        self.assertNotIn(USER["email"], json.dumps(result))
        exported = A.decode_json(output.read_bytes())
        self.assertEqual(exported, {"version": 2, **user(BIG)})
        self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)

    def test_cli_preview_creates_explicit_v2_artifact_and_string_request_ids(self):
        generated = None
        def dispatch(method, path, raw, _):
            nonlocal generated
            if path == A.API + "/policies":
                return 200, {"version": 1, "revision": 4, "config": CONFIG, "capabilities": CAPABILITIES}, {}
            if method == "GET" and path.startswith(A.USERS_API + "/users/"):
                return 200, {"version": 2, **user(int(path.rsplit("/", 1)[1]))}, {}
            self.assertEqual((method, path), ("POST", A.USERS_API + "/users/preview"))
            body = json.loads(raw); generated = body["operation_id"]
            self.assertEqual(body["targets"], [{"user_id": str(uid), "expected_revision": 3} for uid in IDS])
            self.assertIs(type(body["expected_policy_revision"]), int)
            return 200, {**user_payload()["preview"], "operation_id": generated}, {}
        with server(dispatch) as (url, seen), patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}):
            out = self.root / "new.json"
            values = ["honeypot", "preview", "--action", "mark", "--policy-id", "test", "--reason-stdin", "--out", str(out)]
            for uid in reversed(IDS):
                values.extend(["--user-id", str(uid)])
            with patch.object(sys, "stdin", io.TextIOWrapper(io.BytesIO(b"Synthetic evidence"))):
                result = A.run(self.args(url, *values))
        saved = A.decode_json(out.read_bytes())
        self.assertEqual(saved["artifact_version"], 2)
        self.assertEqual(saved["payload"]["preview"]["version"], 2)
        self.assertEqual(result["user_ids"], list(map(str, IDS)))
        self.assertEqual(result["artifact_sha256"], hashlib.sha256(out.read_bytes()).hexdigest())
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "GET", "GET", "POST"])

    def test_legacy_artifact_raw_bytes_sha_and_opaque_commit_survive_exact_large_reconciliation(self):
        applied = False
        def dispatch(method, path, raw, _):
            nonlocal applied
            self.assertTrue(path.startswith(A.USERS_API + "/"))
            if method == "GET":
                self.assertEqual(path, A.USERS_API + "/operations/" + OP)
                return (200, users_receipt(), {}) if applied else missing()
            self.assertEqual(path, A.USERS_API + "/users/commit")
            self.assertEqual(json.loads(raw), {"operation_id": OP, "preview_id": "preview-test-id", "preview_hash": "a" * 64})
            applied = True
            return None
        with server(dispatch) as (url, seen):
            # Deliberately noncanonical bytes and target order. They are the
            # approved artifact, not a new v2 preview or client-side hash domain.
            value = {"payload": user_payload(tuple(reversed(IDS)), version=1), "operation_id": OP,
                     "kind": "users", "server": url, "artifact_version": 1}
            raw = (" \n" + json.dumps(value, ensure_ascii=False, indent=3) + "\n\t").encode()
            path = self.root / "legacy.json"; path.write_bytes(raw); path.chmod(0o600)
            digest = hashlib.sha256(raw).hexdigest()
            args = SimpleNamespace(preview=str(path), confirm=digest, apply=False)
            dry = A.apply_artifact(A.Client(url, ""), args)
            self.assertEqual(dry["artifact_sha256"], digest)
            self.assertEqual(seen, [])
            args.apply = True
            first = A.apply_artifact(A.Client(url, TOKEN), args)
            second = A.apply_artifact(A.Client(url, TOKEN), args)
            self.assertTrue(first["reconciled"])
            self.assertTrue(second["reused"])
            self.assertEqual([row["user_id"] for row in first["users"]], list(map(str, IDS)))
            self.assertEqual(path.read_bytes(), raw)
            self.assertEqual(hashlib.sha256(path.read_bytes()).hexdigest(), digest)
            self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET", "GET"])

    def test_artifact_version_and_id_domain_cannot_be_mixed(self):
        for version, payload in ((2, user_payload(version=1)), (1, user_payload(version=2))):
            path, args = self.artifact("https://example.invalid", version=version, payload=payload)
            with self.subTest(version=version), patch.object(A.Client, "request", side_effect=AssertionError("network forbidden")), self.assertRaises(A.ClientError):
                A.apply_artifact(A.Client("https://example.invalid", TOKEN), args)
            self.assertTrue(path.exists())
        value = A.artifact("https://example.invalid", "policy-local-diff", OP,
                           {"operation_id": OP, "reason": "Synthetic evidence", "expected_revision": 4,
                            "config": CONFIG, "before_config": CONFIG})
        self.assertEqual(value["artifact_version"], 1)
        value["artifact_version"] = 2
        path = self.root / "wrong-policy.json"; digest = A.private_write(str(path), value)
        with patch.object(A.Client, "request", side_effect=AssertionError("network forbidden")), self.assertRaises(A.ClientError):
            A.apply_artifact(A.Client("https://example.invalid", TOKEN), SimpleNamespace(preview=str(path), confirm=digest, apply=True))

    def test_legacy_numeric_domain_rejects_floats_strings_zero_and_overflow(self):
        for invalid in (True, 7.0, "7", 0, -1, A.MAX_I64 + 1):
            payload = user_payload(version=1)
            payload["request"]["targets"][0]["user_id"] = invalid
            path, args = self.artifact("https://example.invalid", version=1, payload=payload)
            with self.subTest(id=invalid), patch.object(A.Client, "request", side_effect=AssertionError("network forbidden")), self.assertRaises(A.ClientError):
                A.apply_artifact(A.Client("https://example.invalid", TOKEN), args)
            self.assertTrue(path.exists())

    def test_manual_operation_dispatches_kind_before_exact_wire_shape(self):
        for value in (users_receipt(), policy_receipt()):
            with self.subTest(kind=value["kind"]), server(lambda *_: (200, value, {})) as (url, seen):
                result = self.invoke(url, "operation", OP)
                self.assertEqual(result["kind"], value["kind"])
                self.assertEqual([row[1] for row in seen], [A.USERS_API + "/operations/" + OP])
        variants = [{**users_receipt(), "version": 1}, {**policy_receipt(), "version": 2},
                    {**policy_receipt(), "users": [user(7)]}, {**policy_receipt(), "kind": "other"},
                    {**users_receipt(), "unknown": True}, {**policy_receipt(), "applied_config_hash": "invalid"}]
        for value in variants:
            with self.subTest(response=value), server(lambda *_: (200, value, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "operation", OP)
                self.assertEqual([row[1] for row in seen], [A.USERS_API + "/operations/" + OP])

    def test_v2_errors_and_v1_success_never_trigger_fallback(self):
        for status, body in ((401, {"error": {"code": "ADMIN_ABUSE_UNAUTHORIZED"}}),
                             (404, {"error": {"code": "ADMIN_ABUSE_NOT_FOUND"}}),
                             (503, {"error": {"code": "ADMIN_ABUSE_UNAVAILABLE"}}),
                             (200, {"version": 1, "users": [{**USER, "user_id": 42}], "next_after": None})):
            with self.subTest(status=status), server(lambda *_: (status, body, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "users", "list")
                self.assertEqual(len(seen), 1)
                self.assertTrue(seen[0][1].startswith(A.USERS_API + "/users?"))
        for status in (401, 404, 503):
            with server(lambda *_: (status, {"error": {"code": "ADMIN_ABUSE_UNAVAILABLE"}}, {})) as (url, seen):
                with self.assertRaises(A.ClientError):
                    self.invoke(url, "operation", OP)
                self.assertEqual([row[1] for row in seen], [A.USERS_API + "/operations/" + OP])

    def test_known_policy_artifact_preflight_and_mutation_stay_v1(self):
        def dispatch(method, path, raw, _):
            self.assertTrue(path.startswith(A.API + "/"))
            if method == "GET":
                self.assertEqual(path, A.API + "/operations/" + OP)
                return missing()
            self.assertEqual((method, path), ("PUT", A.API + "/policies"))
            self.assertEqual(set(json.loads(raw)), {"operation_id", "reason", "expected_revision", "config"})
            return 200, policy_receipt(), {}
        with server(dispatch) as (url, seen):
            path = self.root / "policy.json"
            digest = A.private_write(str(path), A.artifact(url, "policy-local-diff", OP,
                                    {"operation_id": OP, "reason": "Synthetic evidence", "expected_revision": 4,
                                     "config": CONFIG, "before_config": CONFIG}))
            result = A.apply_artifact(A.Client(url, TOKEN), SimpleNamespace(preview=str(path), confirm=digest, apply=True))
            self.assertTrue(result["applied_config_match_verified"])
            self.assertEqual([row[0] for row in seen], ["GET", "PUT"])

    def test_malformed_reconciliation_after_lost_write_remains_unknown_and_never_replays(self):
        bad = users_receipt()
        missing_nullable = copy.deepcopy(bad); del missing_nullable["users"][0]["policy_id"]
        numeric = copy.deepcopy(bad); numeric["users"][0]["user_id"] = BIG
        duplicate = copy.deepcopy(bad); duplicate["users"][1] = copy.deepcopy(duplicate["users"][0])
        wrong_state = copy.deepcopy(bad); wrong_state["users"][0]["marked"] = False
        variants = [{**bad, "version": 1}, policy_receipt(), {**bad, "kind": "unknown"},
                    missing_nullable, numeric, duplicate, wrong_state]
        for value in variants:
            def dispatch(method, _path, _raw, number):
                if method == "POST":
                    return None
                return missing() if number == 1 else (200, value, {})
            with self.subTest(response=value), server(dispatch) as (url, seen):
                path, args = self.artifact(url)
                with self.assertRaises(A.OutcomeUnknown) as error:
                    A.apply_artifact(A.Client(url, TOKEN), args)
                self.assertIn(OP, str(error.exception))
                self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])
                self.assertEqual(seen[0][1], seen[-1][1])
                self.assertEqual(seen[0][1], A.USERS_API + "/operations/" + OP)

    def test_commit_projection_failure_then_unavailable_query_never_means_rollback(self):
        for status, code in ((403, "ADMIN_ABUSE_FORBIDDEN"), (404, "ADMIN_ABUSE_OPERATION_NOT_FOUND"), (503, "ADMIN_ABUSE_UNAVAILABLE")):
            def dispatch(method, _path, _raw, number):
                if number == 1:
                    return missing()
                if method == "POST":
                    return 200, {**RESULT, "version": 1}, {}
                return status, {"error": {"code": code}}, {}
            with self.subTest(status=status), server(dispatch) as (url, seen):
                _, args = self.artifact(url)
                with self.assertRaises(A.OutcomeUnknown):
                    A.apply_artifact(A.Client(url, TOKEN), args)
                self.assertEqual([row[0] for row in seen], ["GET", "POST", "GET"])

    def test_malformed_preflight_blocks_every_mutation(self):
        for value in ({**users_receipt(), "version": 1}, policy_receipt(), {**users_receipt(), "users": []}):
            with self.subTest(response=value), server(lambda *_: (200, value, {})) as (url, seen):
                _, args = self.artifact(url)
                with self.assertRaises(A.ClientError):
                    A.apply_artifact(A.Client(url, TOKEN), args)
                self.assertEqual([row[0] for row in seen], ["GET"])

    def test_preview_closed_nested_fields_and_numeric_revisions_are_not_coerced(self):
        variants = [lambda v: v.update(unexpected=True), lambda v: v.pop("policy_id"),
                    lambda v: v["targets"][0].pop("before"), lambda v: v["targets"][0]["before"].pop("policy_id"),
                    lambda v: v["targets"][0]["after"].update(extra=True), lambda v: v.update(expires_at="123"),
                    lambda v: v.update(policy_revision="4"), lambda v: v["targets"][0].update(expected_revision="3")]
        for mutate in variants:
            value = preview(); mutate(value)
            with self.subTest(response=value), self.assertRaises(A.ClientError):
                A.check_preview(value, preview_request())
        value = A.check_preview(preview(), preview_request())
        self.assertIs(type(value["expires_at"]), int)
        self.assertIs(type(value["policy_revision"]), int)
        self.assertIs(type(value["targets"][0]["expected_revision"]), int)

    def test_exact_method_path_allowlist_never_sends_credentials_elsewhere(self):
        invalid = [("GET", "/anything"), ("GET", A.USERS_API + "evil/users"), ("PUT", A.USERS_API + "/policies"),
                   ("GET", A.API + "/users"), ("GET", A.USERS_API + "/users/7/similarity"),
                   ("GET", A.USERS_API + "/users/0"), ("GET", A.USERS_API + "/users/07"),
                   ("GET", A.USERS_API + "/users/%37"), ("GET", A.USERS_API + "/users/7?token=secret"),
                   ("GET", A.USERS_API + "/users/7#fragment"), ("GET", A.API + "/../policies"),
                   ("DELETE", A.USERS_API + "/users/7"), ("POST", A.USERS_API + "/operations/" + OP)]
        client = A.Client("https://example.invalid", TOKEN)
        with patch.object(client.opener, "open", side_effect=AssertionError("network forbidden")):
            for method, path in invalid:
                with self.subTest(path=path), self.assertRaises(A.ClientError):
                    client.request(method, path)
        for method, path in (("GET", A.API + "/policies"), ("PUT", A.API + "/policies"),
                             ("GET", A.USERS_API + "/users"), ("GET", A.USERS_API + "/users/" + str(A.MAX_I64)),
                             ("POST", A.USERS_API + "/users/preview"), ("POST", A.USERS_API + "/users/commit"),
                             ("GET", A.USERS_API + "/audit"), ("GET", A.API + "/operations/" + OP),
                             ("GET", A.USERS_API + "/operations/" + OP)):
            A.permitted_api_path(method, path)

    def test_expired_legacy_preview_can_reuse_original_v2_receipt_without_post(self):
        payload = user_payload(version=1); payload["preview"]["expires_at"] = 1
        with server(lambda *_: (200, users_receipt(), {})) as (url, seen):
            path, args = self.artifact(url, version=1, payload=payload)
            raw = path.read_bytes()
            result = A.apply_artifact(A.Client(url, TOKEN), args)
        self.assertTrue(result["reused"])
        self.assertFalse(result["exact_request_match_verified"])
        self.assertEqual(path.read_bytes(), raw)
        self.assertEqual([row[0] for row in seen], ["GET"])

    def test_interrupt_after_submission_keeps_unknown_exit_and_original_operation(self):
        path, args = self.artifact("https://example.invalid")
        seen = []
        def interrupted(_client, method, endpoint, *_args, **_kwargs):
            seen.append((method, endpoint))
            if method == "GET":
                raise A.ApiError(404, "ADMIN_ABUSE_OPERATION_NOT_FOUND")
            raise KeyboardInterrupt
        stdout, stderr = io.StringIO(), io.StringIO()
        with patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}), patch.object(A.Client, "request", interrupted), \
                patch.object(sys, "stdout", stdout), patch.object(sys, "stderr", stderr):
            code = A.main(["--server", "https://example.invalid", "apply", "--preview", str(path),
                           "--confirm", args.confirm, "--apply"])
        self.assertEqual(code, 3)
        self.assertIn(OP, stderr.getvalue())
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(seen, [("GET", A.USERS_API + "/operations/" + OP), ("POST", A.USERS_API + "/users/commit")])

    def test_v2_user_views_reject_unknown_roles_invalid_policies_and_inconsistent_marks(self):
        malformed = [{**user(BIG), "platform_role": "owner"}, {**user(BIG), "platform_role": ""},
                     {**user(BIG, marked=True), "policy_id": ""}, {**user(BIG, marked=True), "policy_id": "x" * 65},
                     {**user(BIG, marked=True), "policy_id": "bad/policy"},
                     {**user(BIG, marked=True), "policy_id": "策略"},
                     {**user(BIG, marked=True), "policy_id": None},
                     {**user(BIG), "policy_id": "test"}]
        for row in malformed:
            for command, response in ((["users", "list"], {"version": 2, "users": [row], "next_after": None}),
                                      (["users", "inspect", str(BIG)], {"version": 2, **row}),
                                      (["operation", OP], {**RESULT, "users": [row]})):
                with self.subTest(row=row, command=command), server(lambda *_: (200, response, {})) as (url, seen):
                    with self.assertRaises(A.ClientError):
                        self.invoke(url, *command)
                    self.assertEqual([item[0] for item in seen], ["GET"])
        for role in ("user", "admin", "superadmin"):
            for marked, policy in ((False, None), (True, "policy_1.A-z"), (True, "x" * 64)):
                row = {**user(BIG), "platform_role": role, "marked": marked, "policy_id": policy}
                self.assertEqual(A.safe_user(row)["policy_id"], policy)


if __name__ == "__main__":
    unittest.main()
