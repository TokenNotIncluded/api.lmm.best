"""Package contract tests. ELF fixtures are compiled locally, not product builds."""
import hashlib
import importlib.util
import json
import platform
from pathlib import Path
import shutil
import subprocess
import tempfile
import tomllib
import unittest
from unittest.mock import patch

HERE = Path(__file__).resolve().parent
SPEC = importlib.util.spec_from_file_location("linux_package", HERE / "build.py")
PKG = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(PKG)
REVISION = "a" * 40


class PackageTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.host_arch = {"x86_64": "amd64", "aarch64": "arm64"}[platform.machine()]
        cls.workspace = tempfile.TemporaryDirectory()
        cls.base = Path(cls.workspace.name)
        cls.root = cls.base / "repo"
        shutil.copytree(HERE / "assets", cls.root / "packaging/linux/assets")
        shutil.copyfile(HERE / "packages.json", cls.root / "packaging/linux/packages.json")
        shutil.copyfile(PKG.ROOT / "packaging/distribution.json", cls.root / "packaging/distribution.json")
        cls.spec = json.loads((cls.root / "packaging/distribution.json").read_text())
        resources = cls.spec["common"] + [name for component in cls.spec["components"].values()
                                          for name in component["resources"]]
        for name in resources:
            dest = cls.root / name
            dest.parent.mkdir(parents=True, exist_ok=True)
            if name.startswith("apps/cli/") or name.endswith(".toml"):
                shutil.copyfile(PKG.ROOT / name, dest)
            else:
                dest.write_text("Synthetic packaging test resource. Not a product license or policy.\n")
        source = cls.base / "fixture.go"
        source.write_text('package main\nimport "fmt"\nfunc main() { fmt.Println("synthetic package fixture") }\n')
        cls.static = {}
        import os
        for arch in ("amd64", "arm64"):
            target = cls.base / ("static-" + arch)
            subprocess.run(["go", "build", "-trimpath", "-o", str(target), str(source)], check=True,
                           env={**os.environ, "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": arch,
                                "GOWORK": "off", "GOTOOLCHAIN": "local", "GOPROXY": "off"}, timeout=120)
            cls.static[arch] = target.read_bytes()
        source_c = cls.base / "fixture.c"
        source_c.write_text('#include <stdio.h>\nint main(void) { puts("synthetic"); return 0; }\n')
        subprocess.run(["cc", str(source_c), "-o", str(cls.base / "dynamic")], check=True, timeout=30)
        cls.dynamic = (cls.base / "dynamic").read_bytes()

    @classmethod
    def tearDownClass(cls):
        cls.workspace.cleanup()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.work = Path(self.temp.name)

    def archive(self, component="core", arch="amd64", binary=None):
        src = self.work / (component + "-" + arch)
        src.mkdir()
        for name in self.spec["components"][component]["binaries"]:
            (src / name).write_bytes(binary if binary is not None else self.static[arch])
        if component == "web":
            (src / "index.html").write_text("<!doctype html><title>Synthetic fixture</title>")
        return PKG.DIST.build(self.root, component, "0.1.0-preview.1", REVISION,
                              "any" if component == "web" else "linux-" + arch,
                              src, self.work / ("archive-" + component + "-" + arch), 1_700_000_000)

    def prepare(self, archive, component="core", fmt="deb", **changes):
        args = dict(root=self.root, archive=archive,
                    expected_hash=hashlib.sha256(archive.read_bytes()).hexdigest(), revision=REVISION,
                    component=component, fmt=fmt, release=1, stage=self.work / ("stage-" + fmt + "-" + component))
        args.update(changes)
        return PKG.prepare(**args)

    def test_two_architectures_and_four_formats(self):
        for arch in ("amd64", "arm64"):
            archive = self.archive(arch=arch)
            for fmt in PKG.FORMATS:
                with self.subTest(arch=arch, fmt=fmt):
                    config, info = self.prepare(archive, fmt=fmt, stage=self.work / (arch + fmt))
                    self.assertEqual(config["arch"], arch)
                    self.assertEqual(info["elf"][0]["linkage"], "static")
                    self.assertFalse(any(d.startswith(("lmm-extensions", "docker", "postgres", "python", "node"))
                                         for d in config["depends"]))
                    self.assertEqual(config["contents"][0]["dst"], "/usr/bin/lmm-core")
                    self.assertNotIn("/usr/bin/lmm-core-admin", info["files"])
                    self.assertTrue(info["preview"])

    def test_cli_package_does_not_install_or_require_a_daemon(self):
        config, info = self.prepare(self.archive("cli"), component="cli")
        self.assertEqual(config["name"], "lmm")
        self.assertEqual(config["scripts"], {})
        self.assertEqual({p for p in info["files"] if p.startswith("/usr/bin/")}, {"/usr/bin/lmm"})
        self.assertFalse(any("systemd" in d or "lmm-core" in d or "lmm-extensions" in d for d in config["depends"]))
        self.assertFalse(any(p.startswith("/etc/") for p in info["files"]))

    def test_cli_package_preserves_magpie_license_and_source_attribution(self):
        archive = self.archive("cli")
        for fmt in PKG.FORMATS:
            with self.subTest(fmt=fmt):
                config, info = self.prepare(archive, component="cli", fmt=fmt)
                contents = {entry["dst"]: entry for entry in config["contents"]}
                for name in ("apps/cli/NOTICE", "apps/cli/third_party/magpie/LICENSE",
                             "apps/cli/third_party/magpie/NOTICE.md"):
                    destination = "/usr/share/lmm/" + name
                    self.assertEqual(Path(contents[destination]["src"]).read_bytes(),
                                     (PKG.ROOT / name).read_bytes())
                    self.assertEqual(info["files"][destination]["mode"], 0o644)

    def test_catalog_has_exactly_three_programs_and_static_web(self):
        catalog = json.loads((self.root / "packaging/linux/packages.json").read_text())
        self.assertEqual(set(catalog["packages"]), {"core", "extensions", "cli", "web"})
        self.assertEqual({p["name"] for p in catalog["packages"].values()},
                         {"lmm-core", "lmm-extensions", "lmm", "lmm-web"})
        self.assertEqual([name for p in catalog["packages"].values() for name in p["binaries"]],
                         ["lmm-core", "lmm-extensions", "lmm"])
        for component in ("core", "extensions", "cli", "web"):
            _, info = self.prepare(self.archive(component), component=component)
            expected = {"/usr/bin/" + b for b in self.spec["components"][component]["binaries"]}
            self.assertEqual({p for p in info["files"] if p.startswith("/usr/bin/")}, expected)
            self.assertEqual({p for p, f in info["files"].items() if f["mode"] == 0o755
                              and not p.startswith("/etc/init.d/")}, expected)

    def test_extensions_are_separate_and_templates_are_shipped(self):
        config, info = self.prepare(self.archive("extensions"), component="extensions")
        self.assertNotIn("lmm-core", config["depends"])
        self.assertIn("/usr/share/lmm/legal/site.example.toml", info["files"])
        self.assertIn("/etc/lmm.conf.d/extensions.toml", info["files"])
        self.assertFalse(any(p.endswith(("/site.json", "/site.toml")) for p in info["files"]))
        self.assertNotIn("/usr/bin/lmm-core", info["files"])

    def test_web_is_noarch_data_not_a_server(self):
        config, info = self.prepare(self.archive("web"), component="web")
        self.assertEqual(config["arch"], "all")
        self.assertEqual(config["depends"], [])
        self.assertEqual(config["scripts"], {})
        self.assertIn("/usr/share/lmm/web/index.html", info["files"])

    def test_configuration_is_preserved_and_contains_no_credentials(self):
        config, _ = self.prepare(self.archive())
        conf = next(c for c in config["contents"] if c["dst"] == "/etc/lmm.conf.d/core.toml")
        self.assertEqual(conf["type"], "config|noreplace")
        self.assertEqual(conf["file_info"]["mode"], 0o644)
        text = Path(conf["src"]).read_text()
        self.assertNotIn("postgres://", text)
        self.assertEqual(tomllib.loads(text)["version"], 1)
        self.assertEqual(tomllib.loads(text)["database"]["url_file"], "/etc/lmm.conf.d/secrets/core/database-url")
        unit = next(c for c in config["contents"] if c["dst"].endswith(".service"))
        service = Path(unit["src"]).read_text()
        self.assertIn("User=lmm-core", service)
        self.assertIn("Group=lmm-core", service)
        self.assertIn("StateDirectory=lmm/core", service)
        self.assertIn("RuntimeDirectory=lmm/core", service)
        self.assertIn("ExecStart=/usr/bin/lmm-core --config /etc/lmm.conf.d/core.toml", service)
        self.assertIn("StandardOutput=journal", service)
        self.assertNotIn("EnvironmentFile=", service)
        self.assertNotIn("LoadCredential=", service)
        self.assertIn("SendSIGKILL=no", service)
        self.assertNotIn("extensions", service)
        self.assertEqual(config["scripts"], {})

    def test_service_configuration_and_paths_match_across_formats(self):
        for component in ("core", "extensions"):
            archive = self.archive(component)
            for fmt in PKG.FORMATS:
                with self.subTest(component=component, fmt=fmt):
                    config, info = self.prepare(archive, component=component, fmt=fmt)
                    contents = {entry["dst"]: entry for entry in config["contents"]}
                    expected = f"/etc/lmm.conf.d/{component}.toml"
                    entry = contents[expected]
                    self.assertEqual(entry["type"], "config|noreplace")
                    self.assertEqual(Path(entry["src"]).read_bytes(),
                        (self.root / f"config/{component}.example.toml").read_bytes())
                    data = tomllib.loads(Path(entry["src"]).read_text())
                    self.assertEqual(data["version"], 1)
                    if component == "extensions":
                        self.assertEqual(data["host"]["modules"], ["none"])
                        self.assertEqual(data["host"]["token_file"], "/etc/lmm.conf.d/secrets/extensions/host-token")
                    self.assertFalse(any(p.endswith(".env") for p in info["files"]))
                    self.assertFalse(any(p.startswith(("/var/", "/run/", "/etc/lmm.conf.d/secrets/")) for p in info["files"]))
                    name = "lmm-" + component
                    if fmt == "apk":
                        script = Path(contents[f"/etc/init.d/{name}"]["src"]).read_text()
                        self.assertIn(f'command_args="--config {expected}"', script)
                        self.assertIn(f'/run/lmm/{component}/service.pid', script)
                        self.assertNotRegex(script, r"(?m)^\s*(?:source\s+|\.\s+|eval\s+)")
                    else:
                        self.assertIn(f"/usr/lib/sysusers.d/{name}.conf", contents)
                        accounts = Path(contents[f"/usr/lib/sysusers.d/{name}.conf"]["src"]).read_text()
                        self.assertNotRegex(accounts, r"(?m)^m\s+")
                        self.assertEqual("g lmm-rpc -" in accounts, component == "core")
                        rpc = Path(contents[f"/usr/share/doc/{name}/rpc.service.conf.example"]["src"]).read_text()
                        if component == "core":
                            self.assertIn("Group=lmm-rpc", rpc)
                            self.assertIn("RuntimeDirectoryMode=0750", rpc)
                        else:
                            self.assertIn("SupplementaryGroups=lmm-rpc", rpc)
                        self.assertFalse(any(".service.d/" in path for path in info["files"]))
                        unit = Path(contents[f"/usr/lib/systemd/system/{name}.service"]["src"]).read_text()
                        self.assertIn(f"ExecStart=/usr/bin/{name} --config {expected}", unit)
                        self.assertNotIn("--legacy-env", unit)
                        self.assertIn("StateDirectoryMode=0700", unit)
                    self.assertFalse(any(hook in config["scripts"] for hook in ("postinstall", "preremove", "postremove")))

    def test_apk_account_hook_does_not_activate_service_or_touch_database(self):
        config, _ = self.prepare(self.archive(), fmt="apk")
        script = Path(config["scripts"]["preinstall"]).read_text()
        self.assertIn("adduser -S", script)
        self.assertEqual(Path(config["scripts"]["preinstall"]).stat().st_mtime_ns, 1_700_000_000_000_000_000)
        for forbidden in ("rc-update", "rc-service", "systemctl", "init-db", "rm -", "curl", "wget"):
            self.assertNotIn(forbidden, script)
        service = next(c for c in config["contents"] if "/init.d/" in c["dst"])
        subprocess.run(["sh", "-n", service["src"]], check=True)
        subprocess.run(["sh", "-n", config["scripts"]["preinstall"]], check=True)

    def test_dnf_smoke_keeps_the_preinstalled_curl_variant(self):
        script = (HERE / "install-smoke.sh").read_text()
        self.assertIn("dnf install -y ca-certificates systemd curl-minimal", script)
        self.assertNotIn("dnf install -y ca-certificates systemd curl\n", script)

    def test_glibc_minimum_is_derived_from_actual_elf(self):
        config, info = self.prepare(self.archive(arch=self.host_arch, binary=self.dynamic))
        self.assertEqual(info["elf"][0]["linkage"], "glibc")
        self.assertIn("libc6 (>= " + info["elf"][0]["glibc_minimum"] + ")", config["depends"])

    def test_glibc_binary_is_not_relabelled_as_alpine(self):
        with self.assertRaisesRegex(ValueError, "Alpine APK"):
            self.prepare(self.archive(arch=self.host_arch, binary=self.dynamic), fmt="apk")

    def test_wrong_hash_revision_and_component_are_rejected(self):
        archive = self.archive()
        for change in ({"expected_hash": "0" * 64}, {"revision": "b" * 40}, {"component": "extensions"}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                self.prepare(archive, stage=self.work / str(len(list(self.work.iterdir()))), **change)

    def test_retired_admin_package_selector_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "unknown native package component"):
            self.prepare(self.archive(), component="core-admin")

    def test_wrong_cpu_is_rejected(self):
        binary = self.work / "wrong-arch"
        binary.write_bytes(self.static["arm64"])
        with self.assertRaises(ValueError):
            PKG.inspect_elf(binary, "amd64")

    def test_symlink_archive_and_existing_stage_are_rejected(self):
        archive = self.archive()
        link = self.work / "linked.tar.gz"
        link.symlink_to(archive)
        with self.assertRaises(ValueError):
            self.prepare(link)
        self.prepare(archive)
        with self.assertRaises(FileExistsError):
            self.prepare(archive)

    def test_unknown_library_and_interpreter_are_not_guessed(self):
        binary = self.work / "binary"
        binary.write_bytes(self.static["amd64"])
        for output in ("(NEEDED) Shared library: [libunknown.so.1]", "[Requesting program interpreter: /tmp/ld.so]"):
            with patch.object(PKG, "run", return_value=output), self.assertRaises(ValueError):
                PKG.inspect_elf(binary, "amd64")

    def test_rust_dynamic_loader_is_mapped_to_its_architecture(self):
        binary = self.work / "binary"
        binary.write_bytes(self.static["amd64"])
        output = """[Requesting program interpreter: /lib64/ld-linux-x86-64.so.2]
(NEEDED) Shared library: [libc.so.6]
(NEEDED) Shared library: [ld-linux-x86-64.so.2]
Name: GLIBC_2.34"""
        with patch.object(PKG, "run", return_value=output):
            report = PKG.inspect_elf(binary, "amd64")
            self.assertEqual(PKG.dependencies([report], "deb", False, False), ["libc6 (>= 2.34)"])
        with patch.object(PKG, "run", return_value=output.replace("[ld-linux-x86-64.so.2]", "[ld-linux-aarch64.so.1]")):
            with self.assertRaises(ValueError):
                PKG.inspect_elf(binary, "amd64")

    def test_prerelease_versions_are_format_specific(self):
        self.assertEqual(PKG.native_version("1.2.3-preview.2", 1, "deb"), ("1.2.3~preview.2", "1"))
        self.assertEqual(PKG.native_version("1.2.3-preview.2", 1, "rpm"), ("1.2.3", "0.preview.2.1"))
        self.assertEqual(PKG.native_version("1.2.3-preview.2", 1, "apk"), ("1.2.3_pre2", "1"))
        self.assertEqual(PKG.native_version("1.2.3-preview.2", 1, "archlinux"), ("1.2.3pre2", "1"))
        for fmt in PKG.FORMATS:
            self.assertEqual(PKG.native_version("1.2.3", 2, fmt), ("1.2.3", "2"))
        with self.assertRaises(ValueError):
            PKG.native_version("1.2.3-unreviewed", 1, "apk")

    def test_release_and_format_validation(self):
        archive = self.archive()
        for args in ({"release": 0}, {"release": -1}, {"fmt": "exe"}, {"expected_hash": "bad"}):
            with self.subTest(args=args), self.assertRaises(ValueError):
                self.prepare(archive, **args)


if __name__ == "__main__":
    unittest.main()
