"""Offline pinning and isolation regression tests; real DB checks are opt-in."""
import argparse
import contextlib
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location("pgroll_tool", Path(__file__).with_name("pgroll.py"))
pgroll = importlib.util.module_from_spec(spec)
spec.loader.exec_module(pgroll)


class PgrollTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="pgroll-offline-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name)
        self.binary = b"synthetic pinned binary, never executed"
        self.digest = hashlib.sha256(self.binary).hexdigest()
        self.manifest = f"{self.digest}  pgroll.linux.amd64\n".encode()
        self.lock = {
            "version": "0.16.3", "repository": "https://github.com/xataio/pgroll",
            "checksums_sha256": hashlib.sha256(self.manifest).hexdigest(),
            "assets": {"linux-amd64": {"name": "pgroll.linux.amd64", "sha256": self.digest}},
        }
        self.target = self.root / "0.16.3/pgroll.linux.amd64"

    def installer(self, manifest=None, binary=None):
        def fetch(url, destination):
            self.assertTrue(url.startswith("https://github.com/xataio/pgroll/releases/download/v0.16.3/"))
            destination.write_bytes((self.manifest if manifest is None else manifest)
                                    if url.endswith("checksums.txt") else (self.binary if binary is None else binary))
        return contextlib.ExitStack(), fetch

    def install(self, manifest=None, binary=None):
        stack, fetch = self.installer(manifest, binary)
        with stack:
            stack.enter_context(patch.object(pgroll, "binary_path", return_value=self.target))
            stack.enter_context(patch.object(pgroll.platform, "system", return_value="Linux"))
            stack.enter_context(patch.object(pgroll.platform, "machine", return_value="x86_64"))
            stack.enter_context(patch.object(pgroll, "download", side_effect=fetch))
            return pgroll.install(self.lock)

    def test_installer_checks_manifest_and_asset_before_atomic_install(self):
        self.assertEqual(self.install(), self.target)
        self.assertEqual(self.target.read_bytes(), self.binary)
        self.assertTrue(os.access(self.target, os.X_OK))
        self.assertFalse(list(self.target.parent.glob(".download-*")))

    def test_modified_manifest_is_rejected(self):
        with self.assertRaisesRegex(pgroll.PgrollError, "manifest"):
            self.install(manifest=b"forged checksum manifest")
        self.assertFalse(self.target.exists())

    def test_modified_download_is_rejected(self):
        with self.assertRaisesRegex(pgroll.PgrollError, "binary checksum"):
            self.install(binary=b"modified binary")
        self.assertFalse(self.target.exists())

    def test_existing_cache_is_rechecked_without_network(self):
        self.install()
        with patch.object(pgroll, "binary_path", return_value=self.target), \
                patch.object(pgroll, "asset_for", return_value=self.lock["assets"]["linux-amd64"]), \
                patch.object(pgroll, "download", side_effect=AssertionError("unexpected network")):
            self.assertEqual(pgroll.install(self.lock), self.target)
            self.target.write_bytes(b"modified cache")
            with self.assertRaisesRegex(pgroll.PgrollError, "pinned checksum"):
                pgroll.install(self.lock)

    def test_symlink_binary_is_rejected_even_when_bytes_match(self):
        source = self.root / "actual"
        source.write_bytes(self.binary)
        source.chmod(0o755)
        link = self.root / "link"
        link.symlink_to(source)
        with self.assertRaises(pgroll.PgrollError):
            pgroll.verify_binary(link, self.lock["assets"]["linux-amd64"])

    def test_inherited_database_and_application_environment_is_removed(self):
        hostile = {"PATH": "/usr/bin", "HOME": str(self.root), "PGHOST": "remote.example.invalid",
                   "PGSERVICEFILE": "/private/config", "PGOPTIONS": "-c search_path=untrusted",
                   "PGROLL_PG_URL": "synthetic-forbidden-url", "PGROLL_USE_VERSION_SCHEMA": "false",
                   "SQL_DSN": "synthetic-forbidden-url", "LOG_SQL_DSN": "synthetic-forbidden-url",
                   "LMM_DB_MIGRATION_MODE": "apply", "SESSION_SECRET": "synthetic-secret"}
        with patch.dict(os.environ, hostile, clear=True):
            self.assertEqual(pgroll.clean_environment(), {"PATH": "/usr/bin", "HOME": str(self.root), "LC_ALL": "C"})

    def test_external_database_arguments_are_not_accepted(self):
        with contextlib.redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as error:
            pgroll.main(["rehearse", "--postgres-url", "postgresql://remote.example.invalid/live"])
        self.assertEqual(error.exception.code, 2)

    def test_pg_major_must_be_17_or_newer_and_consistent(self):
        for name in ("initdb", "pg_ctl", "psql"):
            path = self.root / name
            path.touch()
            path.chmod(0o755)
        def version(text):
            return subprocess.CompletedProcess([], 0, f"tool (PostgreSQL) {text}\n", "")
        with patch.object(pgroll.subprocess, "run", return_value=version("16.10")):
            with self.assertRaisesRegex(pgroll.PgrollError, "17 or later"):
                pgroll.postgres_tools(self.root)
        with patch.object(pgroll.subprocess, "run", side_effect=[version("17.6"), version("18.1")]):
            with self.assertRaisesRegex(pgroll.PgrollError, "same major"):
                pgroll.postgres_tools(self.root)

    def test_existing_evidence_is_never_overwritten(self):
        evidence = self.root / "evidence"
        evidence.mkdir()
        marker = evidence / "result.json"
        marker.write_text("previous evidence")
        args = argparse.Namespace(pg_bin=None, evidence=evidence)
        with patch.object(pgroll.os, "geteuid", return_value=1000), \
                patch.object(pgroll, "verify_binary", return_value=self.root / "unused"), \
                patch.object(pgroll, "postgres_tools", return_value={}), \
                patch.object(pgroll, "asset_for", return_value={}), \
                patch.object(pgroll, "binary_path", return_value=self.root / "unused"):
            with self.assertRaises(FileExistsError):
                pgroll.rehearse(self.lock, args)
        self.assertEqual(marker.read_text(), "previous evidence")

    def test_failed_step_retains_private_evidence(self):
        run = pgroll.Rehearsal(self.root / "unused", {}, self.root, self.root)
        failed = subprocess.CompletedProcess([], 7, "synthetic stdout", "synthetic stderr")
        with patch.object(pgroll.subprocess, "run", return_value=failed):
            with self.assertRaisesRegex(pgroll.PgrollError, "fixture-step"):
                run.run("fixture-step", ["unused"])
        self.assertEqual(run.steps, [{"name": "fixture-step", "exit_code": 7}])
        for path in self.root.glob("*.log"):
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)

    def test_status_requires_the_expected_schema_version_and_phase(self):
        run = pgroll.Rehearsal(self.root / "unused", {}, self.root, self.root)
        active = subprocess.CompletedProcess([], 0, '{"schema":"public","version":"v1","status":"In progress"}', "")
        with patch.object(run, "pgroll", return_value=active):
            run.status("active", "v1", "In progress")
            with self.assertRaisesRegex(pgroll.PgrollError, "status"):
                run.status("wrong-version", "v2", "In progress")
            with self.assertRaisesRegex(pgroll.PgrollError, "status"):
                run.status("wrong-phase", "v1", "Complete")

    def test_real_child_cannot_discover_caller_cwd_or_home_configuration(self):
        caller, caller_home, fixture = (self.root / name for name in ("caller", "caller-home", "fixture"))
        for directory in (caller, caller_home, fixture):
            directory.mkdir()
            (directory / ".pgroll.yaml").write_text("postgres-url: synthetic-forbidden-caller-config\n")
        # The fixture starts empty: the two ambient files are the test inputs.
        (fixture / ".pgroll.yaml").unlink()
        previous = Path.cwd()
        try:
            os.chdir(caller)
            with patch.dict(os.environ, {"HOME": str(caller_home), "XDG_CONFIG_HOME": str(caller_home)}):
                run = pgroll.Rehearsal(self.root / "unused", {}, self.root, fixture)
                child = run.run("configuration-probe", [sys.executable, "-c", "import json,os,pathlib; print(json.dumps({'cwd':os.getcwd(),'home':os.environ['HOME'],'cwd_config':pathlib.Path('.pgroll.yaml').exists(),'home_config':(pathlib.Path.home()/'.pgroll.yaml').exists(),'xdg_config':os.environ['XDG_CONFIG_HOME']}))"])
        finally:
            os.chdir(previous)
        observed = json.loads(child.stdout)
        self.assertEqual(observed, {"cwd": str(fixture), "home": str(fixture / "home"), "cwd_config": False,
                                    "home_config": False, "xdg_config": str(fixture / "home/.config")})

    def test_committed_lock_and_migration_have_stable_explicit_scope(self):
        lock = pgroll.load_lock()
        self.assertEqual(lock["version"], "0.16.3")
        self.assertEqual(pgroll.asset_for(lock, "Linux", "x86_64")["name"], "pgroll.linux.amd64")
        with self.assertRaises(pgroll.PgrollError):
            pgroll.asset_for(lock, "unknown", "x86_64")
        migration = json.loads(pgroll.MIGRATION.read_text())
        self.assertNotIn("name", migration)  # pgroll 0.13+ derives it from the filename.
        self.assertEqual(migration["operations"][0]["alter_column"]["table"], "pgroll_fixture_items")


if __name__ == "__main__":
    unittest.main()
