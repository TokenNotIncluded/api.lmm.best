"""One-time, hash-checked reconstruction of the reviewed task tree.

No production access, refunds, database writes, main writes or PR-branch writes.
Only source changes are published to a new temporary review branch. Workflow
changes are excluded: they require the separately authorized GitHub connection.
"""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys


def git(*args, input=None):
    return subprocess.check_output(["git", *args], input=input).decode().strip()


def blob(path):
    return git("hash-object", str(path))


if os.environ["GITHUB_REF"] != "refs/heads/review/store-refund-build-20261010":
    raise SystemExit("Unexpected construction branch")
data = json.loads(Path(sys.argv[1]).read_text())
assert data["head"] == "9182113dea1502403d501a4ac0d1ef330a49504c"
assert data["main"] == "3076e01b31a62d18e0c9bf52da4d6b36712c01bf"
git("fetch", "--no-tags", "--depth=1", "origin", data["head"], data["main"])
git("checkout", "--detach", data["main"])
assert git("rev-parse", "HEAD") == data["main"]
for name in data["overlays"]:
    path = Path(name)
    assert not path.is_absolute() and ".." not in path.parts
    git("checkout", data["head"], "--", name)
for name, update in data["updates"].items():
    path = Path(name)
    assert not path.is_absolute() and ".." not in path.parts
    assert blob(path) == update["base"], f"Source changed: {name}"
    lines = path.read_text().splitlines(keepends=True)
    for start, end, text in reversed(update["chunks"]):
        assert 0 <= start <= end <= len(lines)
        lines[start:end] = [text]
    path.write_text("".join(lines))
    assert blob(path) == update["result"], f"Unexpected patch output: {name}"
translations = Path(os.environ["RUNNER_TEMP"]) / "refund-translations.json"
translations.write_text(json.dumps(data["translations"], ensure_ascii=False))
helper = ".agents/skills/i18n-translate/scripts/apply-translations.mjs"
subprocess.run(["node", helper, str(translations), "--check"], check=True)
subprocess.run(["node", helper, str(translations)], check=True)
subprocess.run(["node", "scripts/sync-i18n.mjs"], cwd="apps/web", check=True)
git("add", "-A")
git("diff", "--cached", "--check")
manifest = {}
for name in git("diff", "--cached", "--name-only", data["main"]).splitlines():
    mode, sha, _ = git("ls-files", "--stage", "--", name).split(" ", 2)
    manifest[name] = {"mode": mode, "sha": sha}
encoded = json.dumps(manifest, sort_keys=True, separators=(",", ":")).encode()
digest = hashlib.sha256(encoded).hexdigest()
result_dir = Path(os.environ["RUNNER_TEMP"]) / "refund-constructed-tree"
result_dir.mkdir()
(result_dir / "manifest.json").write_bytes(encoded)
assert digest == data["expected_manifest_sha256"], f"Reviewed tree differs: {digest}"
assert not Path(".github/workflows/store-source-review.yml").exists()
assert not Path(".github/workflows/store-refund-build.yml").exists()
git("config", "user.name", "github-actions[bot]")
git("config", "user.email", "41898282+github-actions[bot]@users.noreply.github.com")
tree = git("write-tree")
# Actions has contents permission, not workflows permission. Keep every workflow
# exactly as in the sole parent of this source-only transport commit.
git("rm", "-r", "--cached", "--", ".github/workflows")
git("checkout", data["main"], "--", ".github/workflows")
source_tree = git("write-tree")
assert git("rev-parse", source_tree + ":.github/workflows") == git("rev-parse", data["main"] + ":.github/workflows")
commit = git("commit-tree", source_tree, "-p", data["main"], input=b"chore(store): stage reviewed source objects without workflow changes\n")
# Empty expected value means the staging ref MUST NOT already exist.
ref = "refs/heads/review/store-refund-verified-20261010"
git("push", "--force-with-lease=" + ref + ":", "origin", commit + ":" + ref)
result = {"commit": commit, "reviewed_tree": tree, "source_tree": source_tree, "intended_parents": [data["head"], data["main"]], "manifest_sha256": digest, "files": len(manifest)}
(result_dir / "result.json").write_text(json.dumps(result, indent=2) + "\n")
print(json.dumps(result))
