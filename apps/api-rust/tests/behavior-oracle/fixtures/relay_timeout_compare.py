#!/usr/bin/env python3
"""Exercise the same timeout fixture through real Go and Rust listeners."""

import http.client
import json
import pathlib
import sys
import time

profile_name, go_port, rust_port, output_path, lifecycle_path = sys.argv[1:]
spec = json.loads((pathlib.Path(__file__).parent / "scenarios/relay_timeouts.json").read_text())
profile = spec["profiles"][profile_name]
observations = {}
failures = []
request_body = spec["request"]
if profile.get("stream"):
    request_body = {
        "model": "gpt-test", "stream": True,
        "messages": [{"role": "user", "content": "relay-timeout:stream"}],
    }
    if profile.get("fragments"):
        request_body["messages"][0]["content"] = "relay-timeout:fragments"
    elif profile.get("heartbeats"):
        request_body["messages"][0]["content"] = "relay-timeout:heartbeats"
for engine, port in (("go", go_port), ("rust", rust_port)):
    case_id = f"{profile_name}-{engine}"
    request_body = {**request_body, "user": case_id}
    connection = http.client.HTTPConnection("127.0.0.1", int(port), timeout=15)
    started = time.monotonic()
    try:
        connection.request("POST", "/v1/chat/completions", json.dumps(request_body), {
            "Content-Type": "application/json", "Authorization": "Bearer sk-relayprobe",
        })
        response = connection.getresponse()
        read_error = None
        try:
            if profile.get("cancel"):
                raw = response.readline(65536)
                response.close()
                connection.close()
            else:
                raw = response.read(65536)
        except http.client.IncompleteRead as error:
            raw = error.partial
            read_error = "incomplete_read"
        if profile.get("stream") and response.status == 200:
            if response.getheader("content-type", "").split(";", 1)[0] != "text/event-stream":
                failures.append(f"{engine}: missing SSE content type")
            if engine == "rust":
                frame = b"data: " + json.dumps(
                    spec["upstream"]["stream_event"], separators=(",", ":")
                ).encode() + b"\n\n"
                expected = frame * spec["upstream"]["stream_chunks"]
                if profile.get("fragments"):
                    expected = frame
                elif profile.get("heartbeats"):
                    expected = b": heartbeat\n\n" * spec["upstream"]["stream_chunks"] + frame
                expected += b"data: [DONE]\n\n"
                if profile.get("cancel"):
                    preserved = raw == frame.splitlines(keepends=True)[0]
                elif profile.get("truncated"):
                    preserved = bool(raw) and expected.startswith(raw)
                else:
                    preserved = raw == expected
                if not preserved:
                    failures.append("rust: provider SSE bytes were modified")
            content = []
            lines = raw.splitlines()
            for index, line in enumerate(lines):
                if line.startswith(b"data: ") and line != b"data: [DONE]":
                    try:
                        event = json.loads(line[6:])
                    except json.JSONDecodeError:
                        if (
                            engine == "rust" and profile.get("truncated") and read_error
                            and index == len(lines) - 1 and not raw.endswith(b"\n")
                        ):
                            continue
                        raise
                    for choice in event.get("choices", []):
                        content.append(choice.get("delta", {}).get("content") or "")
            body = {"content": "".join(content), "done": b"data: [DONE]" in raw}
            if profile.get("heartbeats"):
                if body["content"] != "x" or not body["done"] or read_error:
                    failures.append(f"{engine}: heartbeats did not keep the stream alive")
                if engine == "rust" and raw.count(b": heartbeat\n\n") != spec["upstream"]["stream_chunks"]:
                    failures.append("rust: heartbeat bytes were not preserved")
            elif profile.get("fragments"):
                expected_content = "x" if engine == "rust" else ""
                if body["content"] != expected_content:
                    failures.append(f"{engine}: unexpected fragmented-line outcome")
                if engine == "rust" and (not body["done"] or read_error):
                    failures.append("rust: byte progress must preserve the fragmented line")
            elif profile.get("cancel"):
                if not body["content"] or body["done"]:
                    failures.append(f"{engine}: cancellation must follow the first content")
            elif profile.get("truncated"):
                if not 0 < len(body["content"]) < spec["upstream"]["stream_chunks"]:
                    failures.append(f"{engine}: active stream was not truncated")
                if engine == "rust" and (body["done"] or not read_error):
                    failures.append("rust: truncated stream must fail without synthetic DONE")
            elif body["content"] != "x" * spec["upstream"]["stream_chunks"] or not body["done"] or read_error:
                failures.append(f"{engine}: active stream did not complete")
        else:
            body = json.loads(raw)
        if response.status != profile["status"][engine]:
            failures.append(f"{engine}: unexpected status {response.status}")
        if response.status == 200 and not profile.get("stream"):
            if body.get("choices", [{}])[0].get("message", {}).get("content") != "hello":
                failures.append(f"{engine}: missing expected content")
        elif response.status != 200:
            if body.get("error", {}).get("code") != profile.get("error_code", {}).get(engine):
                failures.append(f"{engine}: unexpected error code")
        observations[engine] = {
            "status": response.status,
            "content_type": response.getheader("content-type"),
            "headers": response.getheaders(),
            "read_error": read_error,
            "body": body,
            "raw_body": raw.decode(),
            "elapsed_seconds": time.monotonic() - started,
        }
        if profile.get("cancel"):
            deadline = time.monotonic() + 5
            provider_event = None
            lifecycle = pathlib.Path(lifecycle_path)
            while time.monotonic() < deadline:
                if lifecycle.exists():
                    for line in lifecycle.read_text().splitlines(keepends=True):
                        if line.endswith("\n"):
                            event = json.loads(line)
                            if event["case_id"] == case_id:
                                provider_event = event
                if provider_event:
                    break
                time.sleep(0.05)
            observations[engine]["provider_event"] = provider_event
            if not provider_event or provider_event["outcome"] != "disconnected":
                failures.append(f"{engine}: downstream cancellation did not release upstream")
    finally:
        connection.close()

if observations["go"]["content_type"] != observations["rust"]["content_type"]:
    failures.append("unexpected Go/Rust content-type difference")
if (
    not profile.get("expected_differences")
    and observations["go"]["raw_body"] != observations["rust"]["raw_body"]
):
    failures.append("unexpected Go/Rust response body difference")

result = {
    "profile": profile_name,
    "expectations_verified": not failures,
    "failures": failures,
    "expected_differences": profile.get("expected_differences", []),
    "status_equal": observations["go"]["status"] == observations["rust"]["status"],
    "raw_body_equal": observations["go"]["raw_body"] == observations["rust"]["raw_body"],
    "content_type_equal": observations["go"]["content_type"] == observations["rust"]["content_type"],
    "observations": observations,
}
pathlib.Path(output_path).write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps({key: value for key, value in result.items() if key != "observations"}))
if failures:
    raise SystemExit(1)
