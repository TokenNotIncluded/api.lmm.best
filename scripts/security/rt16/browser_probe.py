# Copyright (C) 2026 LIghtJUNction; SPDX-License-Identifier: AGPL-3.0-or-later
"""Local browser preflight and optional REAL Markdown component probe.

Not a stored-content end-to-end test. Does not create accounts or use any app API.
Never disable browser policy. Never use this runner against a deployed service.
"""
import argparse
import json
import mimetypes
from pathlib import Path
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import threading
import time
from urllib.parse import urlsplit

CONTROLS = {
    "html": ("<p><strong>RT16 safe text</strong></p>", "strong"),
    "link": ("[RT16 safe link](/rt16-safe#section)", "a[href='/rt16-safe#section']"),
    "svg": ('<svg viewBox="0 0 100 100"><circle cx="50" cy="50" r="20"/><text x="10" y="90">RT16</text></svg>', "svg circle"),
    "math": ("$$\\frac{1}{2}+\\sqrt{x}$$", ".katex math"),
    "flow": ("```flow\na=>start: RT16 start\nb=>end: RT16 end\na->b\n```", 'svg[data-diagram="flow"] marker'),
    "sequence": ("```seq\nAlice->Bob: RT16 safe message\n```", 'svg[data-diagram="sequence"]'),
    "encoded": ("&lt;strong&gt;RT16 literal&lt;/strong&gt;", "p"),
}


def load_cases(path):
    if path is None:
        return []
    cases = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(cases, list) or not cases or len(cases) > 100:
        raise ValueError("Private corpus must be a nonempty list, at most 100 cases")
    seen = set()
    for case in cases:
        if not isinstance(case, dict) or not isinstance(case.get("id"), str):
            raise ValueError("Each case must have a string id")
        if case["id"] in seen or not case["id"] or len(case["id"]) > 80:
            raise ValueError("Case ids must be nonempty, short and unique")
        seen.add(case["id"])
        if case.get("control") not in CONTROLS:
            raise ValueError("Each attack must name an existing legal control")
        if not isinstance(case.get("markdown"), str) or len(case["markdown"]) > 50000:
            raise ValueError("Each case must have a Markdown string, at most 50000 characters")
    return cases


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--executable", help="Explicit installed Chromium path")
    parser.add_argument("--bundle", type=Path, help="Local Bun browser build output")
    parser.add_argument("--corpus", type=Path, help="Private paired probe JSON, never committed")
    parser.add_argument("--output", type=Path, required=True, help="Private output directory")
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True, mode=0o700)
    args.output.chmod(0o700)
    result = {
        "scope": "browser preflight and optional component-only probes",
        "stored_paths": {name: "NOT_RUN" for name in ["product", "support", "profile", "assistant"]},
        "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "result": "BLOCKED",
        "cases": [],
    }
    bundle = args.bundle.resolve() if args.bundle else None
    received = []

    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            route = urlsplit(self.path).path
            received.append(route)  # No cookies, headers, or credentials are logged.
            if route == "/rt16-preflight":
                data = b'<!doctype html><title>RT16 preflight</title><p id="rt16-local">local only</p>'
                content_type = "text/html; charset=utf-8"
            elif route == "/":
                data = b'<!doctype html><meta charset="utf-8"><title>RT16 component only</title><link rel="stylesheet" href="/rt16-markdown-entry.css"><div id="rt16-mount"></div><script type="module" src="/rt16-markdown-entry.js"></script>'
                content_type = "text/html; charset=utf-8"
            elif route == "/__rt16_signal":
                data, content_type = b"test marker received", "text/plain"
            else:
                file = (bundle / route.lstrip("/")).resolve() if bundle else None
                if (file is None or not file.is_relative_to(bundle) or
                        file.suffix not in {".js", ".css", ".woff", ".woff2", ".ttf"} or
                        not file.is_file()):
                    self.send_error(404)
                    return
                data = file.read_bytes()
                content_type = mimetypes.guess_type(file.name)[0] or "application/octet-stream"
            self.send_response(200)
            self.send_header("Content-Type", content_type)
            self.send_header("Cache-Control", "no-store")
            self.end_headers()
            self.wfile.write(data)

        def log_message(self, *_):
            pass

    server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    origin = f"http://127.0.0.1:{server.server_port}"
    result["origin"] = origin
    try:
        from playwright.sync_api import sync_playwright
        cases = load_cases(args.corpus)
        with sync_playwright() as playwright:
            options = {"headless": True}
            if args.executable:
                options["executable_path"] = args.executable
            with playwright.chromium.launch(**options) as browser:
                result["browser_version"] = browser.version
                page = browser.new_page()
                # First verify that the normal browser policy permits local pages.
                page.goto(origin + "/rt16-preflight", timeout=10000)
                if page.locator("#rt16-local").count() != 1:
                    raise RuntimeError("Local browser control did not load")
                page.close()
                result["preflight"] = "PASS"
                if bundle is None:
                    result["result"] = "PREFLIGHT_ONLY"
                    return 2
                if not (bundle / "rt16-markdown-entry.js").is_file():
                    raise RuntimeError("The actual Markdown browser bundle is missing")
                probes = [(f"control-{name}", value[0], value[1]) for name, value in CONTROLS.items()]
                for case in cases:
                    control, selector = CONTROLS[case["control"]]
                    probes.append((f"{case['id']}-paired-control", control, selector))
                    probes.append((case["id"], case["markdown"], None))
                for case_id, markdown, selector in probes:
                    # Each probe uses an independent, empty context. This is NOT
                    # an authenticated writer/reader storage test.
                    with browser.new_context(service_workers="block") as context:
                        blocked, signals, errors, dialogs, sockets = [], [], [], [], []
                        observed = []

                        def guard(route):
                            request = route.request
                            target = urlsplit(request.url)
                            observed.append({"method": request.method, "url": request.url,
                                             "resource_type": request.resource_type})
                            same_origin = f"{target.scheme}://{target.netloc}" == origin
                            if not same_origin or request.method != "GET":
                                blocked.append({"method": request.method, "url": request.url,
                                                "resource_type": request.resource_type})
                                route.abort("blockedbyclient")
                            elif target.path == "/__rt16_signal":
                                signals.append(target.query)
                                route.fulfill(status=200, body="synthetic marker")
                            elif target.path == "/" or Path(target.path).suffix in {".js", ".css", ".woff", ".woff2", ".ttf"}:
                                route.continue_()
                            else:
                                blocked.append({"method": request.method, "url": request.url,
                                                "resource_type": request.resource_type})
                                route.abort("blockedbyclient")

                        context.route("**/*", guard)
                        def reject_socket(socket):
                            sockets.append(socket.url)
                            socket.close()

                        context.route_web_socket("**/*", reject_socket)
                        page = context.new_page()
                        page.on("pageerror", lambda error: errors.append(str(error)))

                        def dismiss(dialog):
                            dialogs.append(dialog.type)
                            dialog.dismiss()

                        page.on("dialog", dismiss)
                        page.goto(origin, wait_until="load", timeout=10000)
                        page.wait_for_function("window.__rt16Ready === true", timeout=10000)
                        # A trusted positive canary verifies that the observer is live.
                        page.evaluate("window.__rt16Executed = 1")
                        if page.evaluate("window.__rt16Inspect().executed") != 1:
                            raise RuntimeError("Execution observer failed its positive control")
                        observed.clear()  # Exclude trusted initial page/assets from probe evidence.
                        page.evaluate("text => window.__rt16Render(text)", markdown)
                        # Only synthetic marked anchors are activated; the route
                        # guard prevents navigation or requests to app APIs.
                        for anchor in page.locator("#rt16-mount a[data-rt16-activate]").all():
                            anchor.dispatch_event("click")
                        page.wait_for_timeout(250)
                        observation = page.evaluate("window.__rt16Inspect()")
                        failures = []
                        if selector and page.locator(f"#rt16-mount {selector}").count() == 0:
                            failures.append("legal display control missing")
                        if not observation["stages"] or len(observation["stages"]) != 2:
                            failures.append("expected two real sanitizer passes")
                        if observation["executed"] != 0 or not observation["stateUnchanged"]:
                            failures.append("synthetic execution/state marker changed")
                        if observation["activeElements"] or observation["unsafeAttributes"]:
                            failures.append("active markup survived in final DOM")
                        if signals or dialogs or errors:
                            failures.append("signal, dialog, or browser error observed")
                        if sockets or any(
                            item["resource_type"] in {"fetch", "xhr", "eventsource", "document"}
                            or item["method"] != "GET"
                            or "RT16_SYNTHETIC_NOT_A_CREDENTIAL" in item["url"]
                            for item in observed
                        ):
                            failures.append("active network or synthetic-secret request attempted")
                        result["cases"].append({"id": case_id, "observation": observation,
                            "requests": observed, "blocked_requests": blocked, "signals": signals, "errors": errors,
                            "dialogs": dialogs, "blocked_websockets": sockets, "failures": failures})
                failed = any(case["failures"] for case in result["cases"])
                result["result"] = "COMPONENT_FAIL" if failed else (
                    "COMPONENT_CORPUS_PASS" if cases else "COMPONENT_CONTROLS_ONLY")
                return 1 if failed else (0 if cases else 2)
    except Exception as error:
        result["error"] = str(error)
        return 2
    finally:
        server.shutdown()
        server.server_close()
        result["server_requests"] = received
        output = args.output / "result.json"
        output.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        output.chmod(0o600)
        print(json.dumps({"result": result["result"], "report": str(output)}, ensure_ascii=False))


if __name__ == "__main__":
    raise SystemExit(main())
