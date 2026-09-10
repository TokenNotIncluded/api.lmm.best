#!/usr/bin/env python3
# Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later.
"""Exclude the unused Chrome apt source from ephemeral CI dependency installs."""
import argparse
import os
import re
from email.parser import Parser
from pathlib import Path
from urllib.parse import urlsplit


def chrome_source(uri):
    parsed = urlsplit(uri)
    return parsed.scheme in {"http", "https"} and parsed.hostname == "dl.google.com" and parsed.path.rstrip("/") in {"/linux/chrome/deb", "/linux/chrome-stable/deb"}


def prepare(directory):
    changed = []
    for path in sorted(directory.iterdir()):
        if path.is_symlink() or not path.is_file() or path.suffix not in {".list", ".sources"}:
            continue
        original = path.read_text()
        if path.suffix == ".list":
            lines = []
            for line in original.splitlines(keepends=True):
                match = re.match(r"^\s*deb(?:-src)?\s+(?:\[[^]]*\]\s+)?(\S+)", line)
                lines.append("# Unused by LMM CI: " + line if match and chrome_source(match[1]) else line)
            result = "".join(lines)
        else:
            blocks = re.split(r"(\n[ \t]*\n)", original)
            for index in range(0, len(blocks), 2):
                block = blocks[index]
                uris = " ".join(Parser().parsestr(block).get_all("URIs", [])).split()
                if not any(chrome_source(uri) for uri in uris):
                    continue
                if not all(chrome_source(uri) for uri in uris):
                    raise ValueError("refusing to disable a mixed-source stanza: " + path.name)
                if re.search(r"^Enabled:", block, re.M | re.I):
                    block = re.sub(r"^Enabled:.*$", "Enabled: no", block, flags=re.M | re.I)
                else:
                    block = block.rstrip("\n") + "\nEnabled: no\n"
                blocks[index] = block
            result = "".join(blocks)
        if result != original:
            path.write_text(result)
            changed.append(path.name)
    return changed


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--sources-dir", type=Path, default=Path("/etc/apt/sources.list.d"))
    args = parser.parse_args()
    if os.environ.get("GITHUB_ACTIONS") != "true":
        parser.error("this preparation is limited to GitHub Actions")
    for name in prepare(args.sources_dir):
        print("Disabled unused Chrome source:", name)
