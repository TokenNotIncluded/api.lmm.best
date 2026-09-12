"""Contract tests for the timeout oracle, with no network or database access."""

import contextlib
import http.client
import io
import json
import pathlib
import runpy
import tempfile
import unittest
from unittest.mock import patch


FIXTURES = pathlib.Path(__file__).parent
SPEC = json.loads((FIXTURES / "scenarios/relay_timeouts.json").read_text())
FRAME = b"data: " + json.dumps(
    SPEC["upstream"]["stream_event"], separators=(",", ":")
).encode() + b"\n\n"
BODY = FRAME * SPEC["upstream"]["stream_chunks"] + b"data: [DONE]\n\n"


class Response:
    status = 200

    def __init__(self, body, incomplete=False, content_type="text/event-stream"):
        self.body = body
        self.incomplete = incomplete
        self.content_type = content_type

    def read(self, _limit):
        if self.incomplete:
            raise http.client.IncompleteRead(self.body)
        return self.body

    def getheader(self, name, default=None):
        return self.content_type if name == "content-type" else default

    def getheaders(self):
        return [("content-type", self.content_type)]


class Connection:
    def __init__(self, body, incomplete=False, content_type="text/event-stream"):
        self.response = Response(body, incomplete, content_type)

    def request(self, *_args):
        pass

    def getresponse(self):
        return self.response

    def close(self):
        pass


class TimeoutComparatorTests(unittest.TestCase):
    def compare(self, rust_body, should_fail, profile="active-stream", go_body=BODY,
                incomplete=False, rust_content_type=None):
        content_type = "text/event-stream" if SPEC["profiles"][profile].get("stream") else "application/json"
        with tempfile.TemporaryDirectory() as directory:
            output = pathlib.Path(directory) / "result.json"
            argv = ["oracle", profile, "1", "2", str(output), "unused"]
            with patch("sys.argv", argv), patch(
                "http.client.HTTPConnection",
                side_effect=[
                    Connection(go_body, content_type=content_type),
                    Connection(rust_body, incomplete, rust_content_type or content_type),
                ],
            ), contextlib.redirect_stdout(io.StringIO()):
                if should_fail:
                    with self.assertRaises(SystemExit) as raised:
                        runpy.run_path(str(FIXTURES / "relay_timeout_compare.py"))
                    self.assertEqual(raised.exception.code, 1)
                else:
                    runpy.run_path(str(FIXTURES / "relay_timeout_compare.py"))
            return json.loads(output.read_text())

    def test_accepts_exact_provider_bytes(self):
        result = self.compare(BODY, False)
        self.assertTrue(result["expectations_verified"])

    def test_rejects_unexpected_json_byte_difference(self):
        go_body = b'{"choices":[{"message":{"content":"hello"}}]}'
        rust_body = b'{"choices":[{"message":{"content":"hello"}}],"injected":true}'
        result = self.compare(rust_body, True, profile="slow-headers", go_body=go_body)
        self.assertIn("unexpected Go/Rust response body difference", result["failures"])

    def test_rejects_unexpected_content_type_difference(self):
        body = b'{"choices":[{"message":{"content":"hello"}}]}'
        result = self.compare(
            body, True, profile="slow-headers", go_body=body,
            rust_content_type="text/plain",
        )
        self.assertIn("unexpected Go/Rust content-type difference", result["failures"])

    def test_rejects_injected_comment_even_when_model_content_is_unchanged(self):
        result = self.compare(b": injected\n\n" + BODY, True)
        self.assertIn("rust: provider SSE bytes were modified", result["failures"])

    def test_accepts_total_timeout_in_the_middle_of_a_provider_frame(self):
        result = self.compare(
            FRAME * 2 + FRAME[:40], False,
            profile="total-stream", go_body=FRAME * 2 + b"data: [DONE]\n\n",
            incomplete=True,
        )
        self.assertTrue(result["expectations_verified"])

    def test_partial_frame_handling_does_not_hide_modified_bytes(self):
        result = self.compare(
            FRAME * 2 + b'data: {"unexpected":', True,
            profile="total-stream", go_body=FRAME * 2 + b"data: [DONE]\n\n",
            incomplete=True,
        )
        self.assertIn("rust: provider SSE bytes were modified", result["failures"])


if __name__ == "__main__":
    unittest.main()
