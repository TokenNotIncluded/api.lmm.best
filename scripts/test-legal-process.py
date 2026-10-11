#!/usr/bin/env python3
"""Exercise the actual Go host on loopback with temporary operator policies."""
import http.client
import json
import os
from pathlib import Path
import shutil
import socket
import subprocess
import tempfile
import time
import tomllib
import unittest

ROOT = Path(__file__).resolve().parents[1]


def toml_string(value):
    return json.dumps(value, ensure_ascii=False)


def legal_toml(config):
    """Write this fixture's fixed schema; tomllib remains the TOML parser."""
    lines = [
        f"version = {config['version']}",
        f"published = {'true' if config['published'] else 'false'}",
        f"default_language = {toml_string(config['default_language'])}",
        f"template_root = {toml_string(config['template_root'])}",
        "",
        "[variables]",
    ]
    lines += [f"{toml_string(key)} = {toml_string(value)}"
              for key, value in config["variables"].items()]
    for name, translations in config["documents"].items():
        lines += ["", f"[documents.{toml_string(name)}]"]
        lines += [f"{toml_string(language)} = {toml_string(path)}"
                  for language, path in translations.items()]
    return "\n".join(lines) + "\n"


class LegalProcessTests(unittest.TestCase):
    def setUp(self):
        executable = os.environ.get("LMM_EXTENSION_TEST_BIN", "")
        if not executable or not Path(executable).is_file():
            self.fail("Set LMM_EXTENSION_TEST_BIN to the actual built Go host")
        self.binary = str(Path(executable).resolve())
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name).resolve()
        shutil.copytree(ROOT / "config/legal", self.root / "legal")
        self.config = tomllib.loads((self.root / "legal/site.example.toml").read_text())
        self.config["published"] = True
        self.config["template_root"] = str(self.root / "legal")
        for key in self.config["variables"]:
            self.config["variables"][key] = "Isolated test value"
        self.config["variables"]["service_name"] = "Isolated policy test"
        self.path = self.root / "legal.toml"
        self.host_path = self.root / "extensions.toml"
        (self.root / "token").write_text("test-only-host-credential-" * 3)
        (self.root / "token").chmod(0o600)
        self.process = None
        self.addCleanup(self.stop)

    def stop(self):
        if self.process is not None:
            self.process.terminate()
            try:
                self.process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                self.process.kill()
                self.process.wait(timeout=5)
            self.process = None

    def start(self):
        policy = legal_toml(self.config)
        # Validate the generated fixture with the standard parser before the
        # Go child reads it; this writer is not an alternative TOML parser.
        self.assertEqual(tomllib.loads(policy), self.config)
        self.path.write_text(policy, encoding="utf-8")
        with socket.socket() as listener:
            listener.bind(("127.0.0.1", 0))
            self.port = listener.getsockname()[1]
        host_config = (
            "version = 1\n"
            "[host]\n"
            f"listen = {toml_string(f'127.0.0.1:{self.port}')}\n"
            f"token_file = {toml_string(str(self.root / 'token'))}\n"
            'modules = ["none"]\n'
            "web_compat = false\n"
            "[legal]\n"
            f"config_file = {toml_string(str(self.path))}\n"
        )
        self.host_path.write_text(host_config, encoding="utf-8")
        # The host deliberately rejects legacy runtime environment variables.
        # Do not inherit developer service credentials or database connection
        # settings into this isolated policy-only child.
        env = {key: value for key, value in os.environ.items()
               if not key.startswith("LMM_") and key not in {"SQL_DSN", "LOG_SQL_DSN", "DATABASE_URL"}}
        self.process = subprocess.Popen([self.binary, "--config", str(self.host_path)], env=env,
                                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        deadline = time.monotonic() + 10
        while time.monotonic() < deadline:
            if self.process.poll() is not None:
                self.fail("actual Go host stopped during policy startup")
            try:
                if self.request("GET", "/health/live")[0] == 200:
                    return
            except (OSError, http.client.HTTPException):
                time.sleep(0.05)
        self.fail("actual Go host did not become live")

    def request(self, method, path):
        connection = http.client.HTTPConnection("127.0.0.1", self.port, timeout=2)
        try:
            connection.request(method, path)
            response = connection.getresponse()
            return response.status, response.read()
        finally:
            connection.close()

    def test_public_reads_without_core_do_not_open_protected_modules(self):
        self.start()
        for name in ("user-agreement", "privacy-policy", "refund-policy"):
            status, data = self.request("GET", f"/api/{name}?lang=en-US")
            self.assertEqual(status, 200)
            body = json.loads(data)
            self.assertTrue(body["success"])
            self.assertEqual(body["language"], "en")
            self.assertIn("Isolated policy test", body["data"])
            self.assertNotIn("{{", body["data"])
            self.assertEqual(len(body["revision"]), 64)
        self.assertEqual(self.request("GET", "/extensions/v1/modules")[0], 401)
        self.assertEqual(self.request("POST", "/api/refund-policy")[0], 405)
        self.assertEqual(self.request("HEAD", "/api/user-agreement"), (200, b""))
        self.assertEqual(self.request("GET", "/api/user-agreement?lang=en&lang=zh")[0], 400)

    def test_reviewed_update_and_disable_need_only_extension_restart(self):
        self.start()
        before = json.loads(self.request("GET", "/api/user-agreement")[1])
        self.stop()
        self.config["variables"]["service_name"] = "Updated isolated policy"
        self.start()
        after = json.loads(self.request("GET", "/api/user-agreement")[1])
        self.assertNotEqual(before["revision"], after["revision"])
        self.assertIn("Updated isolated policy", after["data"])
        self.stop()
        self.config["published"] = False
        self.start()
        self.assertEqual(json.loads(self.request("GET", "/api/user-agreement")[1])["data"], "")


if __name__ == "__main__":
    unittest.main()
