"""Offline regression coverage for read-only public release acceptance."""
import importlib.util
import json
from pathlib import Path
import sys
import unittest

spec = importlib.util.spec_from_file_location("public_production", Path(__file__).with_name("verify-public-production.py"))
module = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = module
spec.loader.exec_module(module)


class AcceptanceTest(unittest.TestCase):
    def setUp(self):
        self.responses = {
            "/api/status": module.Response(200, "application/json", json.dumps({"success": True, "data": {"version": "0.2.51"}}).encode()),
            "/app.js": module.Response(200, "application/javascript", b"console.log('loaded')"),
            "/app.css": module.Response(200, "text/css", b"body{margin:0}"),
        }
        for path in ("/", "/login", "/console"):
            self.responses[path] = module.Response(200, "text/html", b'<script src="/app.js"></script><link rel="stylesheet" href="/app.css">')
        self.visited = []

    def fetch(self, url):
        self.assertTrue(url.startswith(module.ORIGIN + "/"))
        path = url.removeprefix(module.ORIGIN)
        self.visited.append(path)
        return self.responses[path]

    def test_success_checks_pages_and_deduplicates_assets(self):
        result = module.verify(self.fetch, "0.2.51")
        self.assertEqual(result["assets"], 2)
        self.assertEqual(result["pages"], 3)
        self.assertEqual(self.visited.count("/app.js"), 1)

    def test_old_backend_is_not_accepted_as_success(self):
        with self.assertRaisesRegex(ValueError, "version mismatch"):
            module.verify(self.fetch, "0.2.52")

    def test_http_200_spa_fallback_is_not_a_successful_asset(self):
        self.responses["/app.js"] = self.responses["/"]
        with self.assertRaisesRegex(ValueError, "SPA fallback"):
            module.verify(self.fetch)

    def test_missing_stylesheet_fails(self):
        self.responses["/app.css"] = module.Response(404, "text/css", b"missing")
        with self.assertRaises(ValueError):
            module.verify(self.fetch)

    def test_service_error_disguised_as_html_fails(self):
        self.responses["/"] = module.Response(200, "text/html", b"service unavailable")
        with self.assertRaisesRegex(ValueError, "no local JavaScript"):
            module.verify(self.fetch)

    def test_false_api_success_fails(self):
        self.responses["/api/status"] = module.Response(200, "application/json", b'{"success":false,"data":{"version":"0.2.51"}}')
        with self.assertRaises(ValueError):
            module.verify(self.fetch)

    def test_cross_origin_redirect_is_refused(self):
        with self.assertRaisesRegex(ValueError, "cross-origin"):
            module.SameOriginRedirect().redirect_request(None, None, 302, "", {}, "https://untrusted.test/asset.js")

    def test_insecure_redirect_is_refused(self):
        with self.assertRaisesRegex(ValueError, "non-HTTPS"):
            module.SameOriginRedirect().redirect_request(None, None, 302, "", {}, "http://api.lmm.best/asset.js")


if __name__ == "__main__":
    unittest.main()
