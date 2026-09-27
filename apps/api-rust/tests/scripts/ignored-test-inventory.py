#!/usr/bin/env python3
"""List ignored test names in an integration target and its declared Rust modules."""

import argparse
from pathlib import Path
import re
import sys

TEST = re.compile(r"(?m)^\s*#\[(?:tokio::)?test[^\n]*\]\s*\n(?P<attrs>(?:\s*#\[[^\n]*\]\s*\n)*)\s*(?:async\s+)?fn\s+(?P<name>[A-Za-z0-9_]+)\s*\(")
MODULE = re.compile(r'(?m)^\s*(?:#\[path\s*=\s*"(?P<path>[^"]+)"\]\s*\n)?\s*(?:pub(?:\([^)]*\))?\s+)?mod\s+(?P<name>[A-Za-z0-9_]+)\s*;')


def inventory(root):
    names = []
    files = []
    visited = set()

    def visit(path, prefix, crate_root=False):
        path = path.resolve()
        if path in visited:
            raise ValueError(f"duplicate/cyclic test module source: {path}")
        visited.add(path)
        source = path.read_text()
        files.append(path)
        for test in TEST.finditer(source):
            if re.search(r"#\[ignore(?:\s|=|\])", test["attrs"]):
                names.append(prefix + test["name"])
        for module in MODULE.finditer(source):
            if module["path"]:
                target = path.parent / module["path"]
            else:
                parent = path.parent if crate_root or path.stem in ("mod", "lib", "main") else path.parent / path.stem
                direct = parent / (module["name"] + ".rs")
                target = direct if direct.is_file() else parent / module["name"] / "mod.rs"
            visit(target, prefix + module["name"] + "::")

    visit(Path(root), "", True)
    if len(names) != len(set(names)):
        raise ValueError("duplicate fully qualified ignored test name")
    return sorted(names), files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("--files", action="store_true")
    parser.add_argument("--filter", default="")
    parser.add_argument("--skip", default="")
    args = parser.parse_args()
    names, files = inventory(args.source)
    if args.files:
        print("\n".join(str(path) for path in files))
    else:
        names = [name for name in names if args.filter in name and (not args.skip or args.skip not in name)]
        if names:
            print("\n".join(names))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"ignored test inventory: {error}", file=sys.stderr)
        sys.exit(1)
