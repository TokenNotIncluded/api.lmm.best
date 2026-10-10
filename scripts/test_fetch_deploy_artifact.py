"""Offline tests. Fake curl/cosign; real Bash, tar and SHA-256 checks."""
from __future__ import annotations

import hashlib
import io
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest

SCRIPT = Path(__file__).with_name("fetch-deploy-artifact.sh")
SHA = "a" * 40
MOCK = r'''#!/usr/bin/env bash
set -euo pipefail
name=${0##*/}
{ printf '%s\t' "$name" "$@"; printf '\n'; } >> "$CALLS"
if [[ $name == curl ]]; then
  url=${!#}
  [[ $url == https://github.com/TokenNotIncluded/api.lmm.best/releases/download/* ]] || exit 20
  output=''
  while (( $# )); do
    if [[ $1 == --output ]]; then output=$2; break; fi
    shift
  done
  source=$FIXTURES/${url##*/}
  [[ -f $source ]] || exit 22
  cp -- "$source" "$output"
  if [[ -n ${RACE_OUTPUT:-} ]]; then mkdir -p -- "$RACE_OUTPUT"; fi
  exit 0
fi
if [[ $name == cosign ]]; then
  [[ $1 == verify-blob ]] || exit 21
  shift
  while (( $# > 1 )); do
    case $1 in
      --certificate-identity) [[ $2 == "$EXPECTED_IDENTITY" ]] || exit 23 ;;
      --certificate-oidc-issuer) [[ $2 == https://token.actions.githubusercontent.com ]] || exit 24 ;;
      --bundle) [[ -f $2 ]] || exit 25 ;;
      *) exit 26 ;;
    esac
    shift 2
  done
  [[ -z ${BAD_SIGNATURE:-} ]]
  exit
fi
exit 99
'''


class ArtifactTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.fixtures = self.root / "fixtures"
        self.fixtures.mkdir()
        self.bin = self.root / "bin"
        self.bin.mkdir()
        for name in ("curl", "cosign", "ssh", "scp", "go", "bun", "gh"):
            path = self.bin / name
            path.write_text(MOCK)
            path.chmod(0o755)
        self.calls = self.root / "calls.tsv"
        self.output = self.root / "download"
        self.env = dict(os.environ, PATH=f"{self.bin}:{os.environ['PATH']}",
                        FIXTURES=str(self.fixtures), CALLS=str(self.calls))
        self.asset = self.make_artifact()

    def make_artifact(self, component="web", arch="amd64", revision=SHA,
                      duplicate_revision=False):
        tag = component + "-v1.2.3"
        name = ("lmm-api-web-1.2.3.tar.gz" if component == "web" else
                f"lmm-api-go-1.2.3-linux-{arch}.tar.gz")
        member = "REVISION" if component == "web" else name[:-7] + "/REVISION"
        data = (revision + "\n").encode()
        with tarfile.open(self.fixtures / name, "w:gz") as archive:
            for _ in range(2 if duplicate_revision else 1):
                entry = tarfile.TarInfo(member)
                entry.size = len(data)
                archive.addfile(entry, io.BytesIO(data))
            # The downloader must not unpack other archive entries.
            entry = tarfile.TarInfo("../../must-not-be-extracted")
            entry.size = 6
            archive.addfile(entry, io.BytesIO(b"unsafe"))
        digest = hashlib.sha256((self.fixtures / name).read_bytes()).hexdigest()
        (self.fixtures / (name + ".sha256")).write_text(f"{digest}  {name}\n")
        (self.fixtures / (name + ".sigstore.json")).write_text("{}\n")
        self.env["EXPECTED_IDENTITY"] = (
            "https://github.com/TokenNotIncluded/api.lmm.best/.github/workflows/"
            f"release-{component}.yml@refs/tags/{tag}"
        )
        return name

    def run_script(self, component="web", tag=None, sha=SHA, output=None, arch=None):
        args = ["bash", str(SCRIPT), component, tag or component + "-v1.2.3",
                sha, str(self.output if output is None else output)]
        if arch is not None:
            args.append(arch)
        return subprocess.run(args, env=self.env, text=True,
                              capture_output=True, timeout=10)

    def get_calls(self):
        if not self.calls.exists():
            return []
        rows = [line.rstrip("\t").split("\t") for line in self.calls.read_text().splitlines()]
        return [[row[0], row[1:]] for row in rows]

    def assert_clean_failure(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        self.assertFalse(self.output.exists())
        self.assertEqual(list(self.root.glob(".lmm-artifact.*")), [])

    def test_web_success(self):
        result = self.run_script()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(self.output))
        self.assertEqual(len(list(self.output.iterdir())), 3)
        self.assertEqual([x[0] for x in self.get_calls()], ["curl"] * 3 + ["cosign"])
        self.assertEqual(list(self.root.glob(".lmm-artifact.*")), [])
        self.assertIn(SHA, result.stderr)
        self.assertFalse((self.root / "must-not-be-extracted").exists())

    def test_go_amd64(self):
        self.make_artifact("go")
        result = self.run_script("go")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_go_arm64(self):
        self.make_artifact("go", "arm64")
        result = self.run_script("go", arch="arm64")
        self.assertEqual(result.returncode, 0, result.stderr)

    def test_https_only_and_bounded_downloads(self):
        self.assertEqual(self.run_script().returncode, 0)
        for name, args in self.get_calls():
            if name == "curl":
                self.assertEqual(args[args.index("--proto") + 1], "=https")
                self.assertEqual(args[args.index("--proto-redir") + 1], "=https")
                self.assertIn("--connect-timeout", args)
                self.assertIn("--max-time", args)
                self.assertNotIn("--retry", args)

    def test_bad_signature(self):
        self.env["BAD_SIGNATURE"] = "1"
        self.assert_clean_failure(self.run_script())

    def test_missing_signature(self):
        (self.fixtures / (self.asset + ".sigstore.json")).unlink()
        self.assert_clean_failure(self.run_script())
        self.assertNotIn("cosign", [c[0] for c in self.get_calls()])

    def test_missing_archive(self):
        (self.fixtures / self.asset).unlink()
        self.assert_clean_failure(self.run_script())
        self.assertEqual(len(self.get_calls()), 1)

    def test_bad_checksum(self):
        (self.fixtures / self.asset).write_bytes(b"changed")
        self.assert_clean_failure(self.run_script())
        self.assertNotIn("cosign", [c[0] for c in self.get_calls()])

    def test_checksum_cannot_choose_path(self):
        (self.fixtures / (self.asset + ".sha256")).write_text("a" * 64 + "  /etc/passwd\n")
        self.assert_clean_failure(self.run_script())

    def test_multiple_checksums(self):
        path = self.fixtures / (self.asset + ".sha256")
        path.write_text(path.read_text() * 2)
        self.assert_clean_failure(self.run_script())

    def test_wrong_signed_revision(self):
        self.assert_clean_failure(self.run_script(sha="b" * 40))
        self.assertEqual(self.get_calls()[-1][0], "cosign")

    def test_duplicate_signed_revision(self):
        self.make_artifact(duplicate_revision=True)
        self.assert_clean_failure(self.run_script())

    def test_invalid_inputs_before_network(self):
        cases = [dict(component="all"), dict(tag="web-v1.2.3;false"),
                 dict(tag="web-v01.2.3"), dict(tag="go-v1.2.3"),
                 dict(sha="HEAD"), dict(sha="A" * 40),
                 dict(component="go", arch="riscv64"), dict(arch="amd64")]
        for kwargs in cases:
            with self.subTest(kwargs=kwargs):
                self.assert_clean_failure(self.run_script(**kwargs))
                self.assertEqual(self.get_calls(), [])

    def test_existing_output_preserved(self):
        self.output.mkdir()
        keep = self.output / "existing-backup"
        keep.write_text("keep")
        self.assertNotEqual(self.run_script().returncode, 0)
        self.assertEqual(keep.read_text(), "keep")
        self.assertEqual(self.get_calls(), [])

    def test_output_symlink_preserved(self):
        existing = self.root / "existing"
        existing.mkdir()
        self.output.symlink_to(existing, target_is_directory=True)
        self.assertNotEqual(self.run_script().returncode, 0)
        self.assertTrue(self.output.is_symlink())
        self.assertTrue(existing.is_dir())
        self.assertEqual(self.get_calls(), [])

    def test_concurrent_output_not_replaced(self):
        self.env["RACE_OUTPUT"] = str(self.output)
        self.assertNotEqual(self.run_script().returncode, 0)
        self.assertTrue(self.output.is_dir())
        self.assertEqual(list(self.output.iterdir()), [])
        self.assertEqual(list(self.root.glob(".lmm-artifact.*")), [])

    def test_output_path_with_spaces(self):
        output = self.root / "release download"
        result = self.run_script(output=output)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout.strip(), str(output))

    def test_help_does_not_download(self):
        result = subprocess.run(["bash", str(SCRIPT), "--help"], env=self.env,
                                capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0)
        self.assertEqual(self.get_calls(), [])


if __name__ == "__main__":
    unittest.main()
