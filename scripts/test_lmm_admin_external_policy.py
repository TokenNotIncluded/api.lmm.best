"""Author tests with synthetic local HTTP only; actual Stage A Core remains unrun."""
from __future__ import annotations

import copy
import io
import json
import os
import stat
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

from test_lmm_admin import A, CAPABILITIES, CONFIG, OP, TOKEN, missing, server


def capabilities(origins=None, *, local_http=False):
    origins = {} if origins is None else origins
    return {**copy.deepcopy(CAPABILITIES), "numeric_loopback_only": False,
            "external_https": True, "local_http": local_http,
            "allowed_origins": origins, "credential_references": sorted(origins)}


def custom(url="https://EXAMPLE.invalid:443/v1", alias="sink"):
    value = copy.deepcopy(CONFIG)
    policy = value["policies"][0]
    policy["mode"] = "custom_upstream"
    del policy["fixed_text"]
    target = {"base_url": url, "upstream_protocol": "openai_chat", "model": "synthetic-model",
              "credential_reference": alias}
    policy["clients"] = {"openai_chat": target, "openai_responses": copy.deepcopy(target)}
    return value


def response(caps, config=CONFIG, revision=4):
    return {"version": 1, "revision": revision, "config": config, "capabilities": caps}


def result(config, operation=OP):
    return {"version": 1, "status": "applied", "operation_id": operation, "kind": "policies",
            "policy_revision": 5, "users": [], "applied_config_hash": A.policy_config_hash(config)}


class CapabilityTests(unittest.TestCase):
    def test_exact_old_and_new_empty_factory_envelopes(self):
        self.assertEqual(A.policy_response(response(CAPABILITIES))["capabilities"], CAPABILITIES)
        new = capabilities()
        self.assertEqual(A.policy_response(response(new))["capabilities"], new)
        with self.assertRaisesRegex(A.ClientError, "INVALID_RESPONSE"):
            A.policy_response({**response(new), "unknown_envelope_field": True})
        legacy_eight = {**CAPABILITIES, "external_https": False, "local_http": True, "allowed_origins": None}
        self.assertEqual(A.policy_response(response(legacy_eight))["capabilities"], legacy_eight)
        loopback = custom("http://127.0.0.1:9123/v1")
        for caps in ({**CAPABILITIES, "credential_references": ["sink"]},
                     {**legacy_eight, "credential_references": ["sink"]}):
            A.policy_config(loopback, capabilities=caps)
            with self.assertRaises(A.ClientError):
                A.policy_config(custom(), capabilities=caps)
        with self.assertRaises(A.ClientError):
            A.policy_config(custom())

    def test_eight_capabilities_reject_partial_unknown_or_inconsistent_metadata(self):
        valid = capabilities({"sink": ["https://example.invalid"]})
        changes = [lambda v: v.pop("external_https"), lambda v: v.update(unknown=True),
                   lambda v: v.update(numeric_loopback_only=0), lambda v: v.update(external_https=1),
                   lambda v: v.update(local_http=0), lambda v: v.update(external_https=False),
                   lambda v: v.update(allowed_origins=None), lambda v: v.update(credential_references=[]),
                   lambda v: v.update(credential_references=["sink", "sink"]),
                   lambda v: v.update(credential_references=["/private/credential"]),
                   lambda v: v.update(allowed_origins={"sink": []}),
                   lambda v: v.update(allowed_origins={"sink": "https://example.invalid"}),
                   lambda v: v.update(allowed_origins={"sink": [True]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid"] * 2}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid:443"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://EXAMPLE.invalid"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid/"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid/v1"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid?"]}),
                   lambda v: v.update(allowed_origins={"sink": ["http://example.invalid"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://[::1]"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://[fe80::1%25eth0]"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid:"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid\n"]}),
                   lambda v: v.update(allowed_origins={"sink": ["https://example.invalid/\ud800"]}),
                   lambda v: v.update(allowed_origins={"sink": [f"https://a{i}.invalid" for i in range(17)]}),
                   lambda v: v.update(allowed_origins={str(i): ["https://example.invalid"] for i in range(33)},
                                      credential_references=sorted(str(i) for i in range(33)))]
        for mutate in changes:
            bad = copy.deepcopy(valid)
            mutate(bad)
            with self.subTest(caps=bad), self.assertRaisesRegex(A.ClientError, "INVALID_RESPONSE"):
                A.policy_response(response(bad))
        for caps in ({**CAPABILITIES, "numeric_loopback_only": False},
                     {**CAPABILITIES, "external_https": True, "local_http": True, "allowed_origins": None}):
            with self.assertRaises(A.ClientError):
                A.policy_response(response(caps))

    def test_alias_scoped_canonical_origin_never_authorizes_other_https(self):
        caps = capabilities({"other": ["https://other.invalid"], "sink": ["https://example.invalid"]})
        value = custom()
        self.assertIs(A.policy_config(value, capabilities=caps), value)
        self.assertEqual(value["policies"][0]["clients"]["openai_chat"]["base_url"], "https://EXAMPLE.invalid:443/v1")
        for url, alias in [("https://example.invalid:444/v1", "sink"), ("https://sub.example.invalid/v1", "sink"),
                           ("https://example.invalid/v1", "other"), ("https://other.invalid/v1", "sink"),
                           ("https://example.invalid/v1", "absent"), ("http://example.invalid/v1", "sink"),
                           ("https://user@example.invalid/v1", "sink"), ("https://example.invalid/v1?", "sink"),
                           ("https://example.invalid/v1#", "sink"), ("https://example.invalid\\path", "sink"),
                           ("https://example.invalid/中文", "sink"), ("https://example.invalid/\ud800", "sink"),
                           ("https://example.invalid/v1@part", "sink"),
                           ("https://127.1/v1", "sink"), ("https://example.invalid:0/v1", "sink")]:
            with self.subTest(url=url, alias=alias), self.assertRaises(A.ClientError):
                A.policy_config(custom(url, alias), capabilities=caps)

    def test_local_http_is_independent_and_only_numeric_loopback(self):
        caps = capabilities({"sink": ["http://127.0.0.1:9123", "https://[::1]"]}, local_http=True)
        A.policy_config(custom("http://127.0.0.1:9123/v1"), capabilities=caps)
        A.policy_config(custom("https://[0:0:0:0:0:0:0:1]:443/v1"), capabilities=caps)
        for url in ("http://localhost/v1", "http://10.0.0.1/v1", "http://[::1]/v1", "https://127.0.0.1/v1"):
            with self.subTest(url=url), self.assertRaises(A.ClientError):
                A.policy_config(custom(url), capabilities=caps)
        for url in ("http://127.0.0.1:9123/v1", "https://[::1]/v1"):
            with self.assertRaises(A.ClientError):
                A.policy_config(custom(url), capabilities=capabilities({"sink": ["https://example.invalid"]}))

    def test_current_before_keeps_repair_path_without_granting_old_route(self):
        for url in ("https://旧.example.invalid/中文@path", "https://old.invalid/v1", "http://127.0.0.1:9123/v1"):
            before = custom(url, "removed-alias")
            self.assertEqual(A.policy_response(response(capabilities(), before))["config"], before)
            with self.assertRaises(A.ClientError):
                A.policy_config(before, capabilities=capabilities())


class BoundPolicyHTTPTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="lmm-admin-external-")
        self.root = Path(self.tmp.name)
        self.caps = capabilities({"sink": ["https://example.invalid"]})
        self.sequence = 0

    def tearDown(self):
        self.tmp.cleanup()

    def artifact(self, url, *, after=None, before=CONFIG, caps=None, bound=True):
        after = custom() if after is None else after
        payload = {"operation_id": OP, "reason": "Synthetic evidence\nreviewed", "expected_revision": 4,
                   "config": after, "before_config": before}
        if bound:
            payload["capabilities"] = self.caps if caps is None else caps
        self.sequence += 1
        path = self.root / f"bound-{self.sequence}.json"
        digest = A.private_write(str(path), A.artifact(url, "policy-local-diff", OP, payload))
        return SimpleNamespace(preview=str(path), confirm=digest, apply=True)

    def preview_args(self, url, after):
        self.sequence += 1
        source = self.root / f"input-{self.sequence}.json"
        A.private_write(str(source), after)
        output = self.root / f"preview-{self.sequence}.json"
        return ["--server", url, "--allow-insecure-loopback", "policy", "preview", "--file", str(source),
                "--out", str(output), "--reason-stdin"], output

    def test_actual_cli_preview_binds_actual_caps_and_preserves_private_raw_config(self):
        with server(lambda *_: (200, response(self.caps), {})) as (url, seen):
            args, output = self.preview_args(url, custom())
            env = {**os.environ, "LMM_ADMIN_SESSION": TOKEN}
            process = subprocess.run([sys.executable, "-B", str(Path(A.__file__)), *args],
                                     input="Synthetic evidence\nreviewed", capture_output=True, text=True, env=env, timeout=10)
            self.assertEqual(process.returncode, 0, process.stderr)
            printed = json.loads(process.stdout)
            self.assertEqual(printed["preview_kind"], "LOCAL_POLICY_DIFF")
            self.assertEqual(printed["target_writes"], 0)
            self.assertNotIn("example.invalid", process.stdout + process.stderr)
            self.assertNotIn(TOKEN, process.stdout + process.stderr)
            saved = A.decode_json(output.read_bytes())
            self.assertEqual(saved["payload"]["capabilities"], self.caps)
            self.assertEqual(saved["payload"]["config"], custom())
            self.assertEqual(saved["payload"]["reason"], "Synthetic evidence\nreviewed")
            self.assertEqual(stat.S_IMODE(output.stat().st_mode), 0o600)
            self.assertEqual([row[0] for row in seen], ["GET"])

    def test_fixed_preview_still_requires_complete_eight_capabilities(self):
        for caps in (capabilities(), {k: v for k, v in capabilities().items() if k != "allowed_origins"}):
            with server(lambda *_: (200, response(caps), {})) as (url, seen):
                args, output = self.preview_args(url, CONFIG)
                with patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}), patch.object(sys, "stdin", io.TextIOWrapper(io.BytesIO(b"Synthetic"))):
                    if "allowed_origins" in caps:
                        A.run(A.parser().parse_args(args))
                        self.assertTrue(output.exists())
                        output.unlink()
                    else:
                        with self.assertRaisesRegex(A.ClientError, "INVALID_RESPONSE"):
                            A.run(A.parser().parse_args(args))
                        self.assertFalse(output.exists())
                self.assertEqual([row[0] for row in seen], ["GET"])

    def test_read_export_and_fixed_repair_of_disallowed_unicode_before_config(self):
        before = custom("https://旧.example.invalid/中文@path", "removed-alias")
        caps = capabilities()
        def dispatch(method, path, data, _):
            if path.startswith(A.API + "/operations/"):
                return missing()
            if method == "GET":
                return 200, response(caps, before), {}
            self.assertEqual(json.loads(data)["config"], CONFIG)
            return 200, result(CONFIG, json.loads(data)["operation_id"]), {}
        with server(dispatch) as (url, seen), patch.dict(os.environ, {"LMM_ADMIN_SESSION": TOKEN}):
            export = self.root / "export.json"
            out = A.run(A.parser().parse_args(["--server", url, "--allow-insecure-loopback", "policy", "get", "--out", str(export)]))
            self.assertEqual(A.decode_json(export.read_bytes()), before)
            self.assertNotIn("removed-alias", json.dumps(out))
            args, output = self.preview_args(url, CONFIG)
            with patch.object(sys, "stdin", io.TextIOWrapper(io.BytesIO(b"Synthetic repair"))):
                prepared = A.run(A.parser().parse_args(args))
            applied = A.apply_artifact(A.Client(url, TOKEN), SimpleNamespace(preview=str(output), confirm=prepared["artifact_sha256"], apply=True))
            self.assertTrue(applied["applied_config_match_verified"])
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "GET", "GET", "PUT"])

    def test_bound_dryrun_and_unbound_external_artifact_never_call_network(self):
        with server(lambda *_: (_ for _ in ()).throw(AssertionError("network forbidden"))) as (url, seen):
            args = self.artifact(url)
            args.apply = False
            self.assertEqual(A.apply_artifact(A.Client(url, TOKEN), args)["target_writes"], 0)
            args = self.artifact(url, bound=False)
            with self.assertRaises(A.ClientError):
                A.apply_artifact(A.Client(url, TOKEN), args)
            self.assertEqual(seen, [])

    def test_bound_apply_reads_caps_and_sends_only_canonical_put_fields(self):
        def dispatch(method, path, data, _):
            if path.startswith(A.API + "/operations/"):
                return missing()
            if method == "GET":
                return 200, response(self.caps), {}
            body = json.loads(data)
            self.assertEqual(set(body), {"operation_id", "reason", "expected_revision", "config"})
            self.assertEqual(body["reason"], "Synthetic evidence\nreviewed")
            self.assertEqual(body["config"], custom())
            return 200, result(custom()), {}
        with server(dispatch) as (url, seen):
            applied = A.apply_artifact(A.Client(url, TOKEN), self.artifact(url))
            self.assertTrue(applied["applied_config_match_verified"])
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "PUT"])

    def test_capability_revision_or_before_drift_prevents_put(self):
        variants = [response(capabilities({"sink": ["https://changed.invalid"]})),
                    response(self.caps, revision=5), response(self.caps, {"version": 1, "policies": []}),
                    response({k: v for k, v in self.caps.items() if k != "local_http"})]
        for value in variants:
            def dispatch(_method, path, _data, _count):
                return missing() if path.startswith(A.API + "/operations/") else (200, value, {})
            with server(dispatch) as (url, seen), self.assertRaises(A.ClientError):
                A.apply_artifact(A.Client(url, TOKEN), self.artifact(url))
            self.assertEqual([row[0] for row in seen], ["GET", "GET"])

    def test_recorded_operation_reconciles_without_current_capability_query(self):
        with server(lambda *_: (200, result(custom()), {})) as (url, seen):
            applied = A.apply_artifact(A.Client(url, TOKEN), self.artifact(url))
            self.assertTrue(applied["reused"])
            self.assertTrue(applied["applied_config_match_verified"])
            self.assertFalse(applied["exact_request_match_verified"])
        self.assertEqual([row[0] for row in seen], ["GET"])

    def test_unknown_put_result_queries_same_operation_without_replay(self):
        def dispatch(method, path, _data, _count):
            if path.startswith(A.API + "/operations/"):
                return missing()
            return (200, response(self.caps), {}) if method == "GET" else (503, {"error": {"code": "ADMIN_ABUSE_UNAVAILABLE"}}, {})
        with server(dispatch) as (url, seen):
            with self.assertRaises(A.OutcomeUnknown):
                A.apply_artifact(A.Client(url, TOKEN), self.artifact(url))
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "PUT", "GET"])
        self.assertEqual(seen[0][1], seen[-1][1])

    def test_server_cas_rejection_remains_known_and_is_not_replayed(self):
        def dispatch(method, path, _data, _count):
            if path.startswith(A.API + "/operations/"):
                return missing()
            return (200, response(self.caps), {}) if method == "GET" else (409, {"error": {"code": "ADMIN_ABUSE_CONFLICT"}}, {})
        with server(dispatch) as (url, seen):
            with self.assertRaises(A.ApiError) as rejected:
                A.apply_artifact(A.Client(url, TOKEN), self.artifact(url))
            self.assertEqual(rejected.exception.status, 409)
        self.assertEqual([row[0] for row in seen], ["GET", "GET", "PUT"])


if __name__ == "__main__":
    unittest.main()
