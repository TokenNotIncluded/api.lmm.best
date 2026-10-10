#!/usr/bin/env python3
"""Check maintained docs, local skills, README badges, and logo assets offline."""
from __future__ import annotations

import argparse
import hashlib
import json
import re
import sys
import xml.etree.ElementTree as ET
from html import unescape
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urlsplit

ROOT = Path(__file__).resolve().parents[1]
DOCUMENTS = (
    "README.md", "README_EN.md", "docs/README.md", "docs/development.md",
    "CONTRIBUTING.md", "DESIGN.md", ".github/assets/README.md", "AGENTS.md",
    ".agents/skills/README.md", "docs/agent-workflows.md", "docs/ci-workflow-layout.md",
)
SYMBOLS = (
    ".github/assets/lmm-symbol.svg", ".github/assets/lmm-logo.svg",
    "apps/web/public/lmm-cut-mark.svg",
)


class Links(HTMLParser):
    def __init__(self) -> None:
        super().__init__()
        self.targets: list[str] = []
        self.images: list[str] = []

    def handle_starttag(self, tag: str, attrs: list[tuple[str, str | None]]) -> None:
        values = dict(attrs)
        for key in ("href", "src"):
            if values.get(key):
                self.targets.append(values[key])
        if tag == "img" and values.get("src"):
            self.images.append(values["src"])


def prose(text: str) -> str:
    """Remove fenced and inline code, not the prose after a fenced example."""
    lines = []
    fence = ""
    for line in text.splitlines(keepends=True):
        marker = re.match(r"^ {0,3}(`{3,}|~{3,})", line)
        if fence:
            if re.match(r"^ {0,3}" + re.escape(fence[0]) +
                        "{" + str(len(fence)) + r",}\s*$", line):
                fence = ""
            continue
        if marker:
            fence = marker[1]
            continue
        lines.append(line)
    return re.sub(r"(`+)(?!`)(.+?)(?<!`)\1(?!`)", "", "".join(lines), flags=re.S)


def document_links(text: str) -> list[str]:
    # Supports this repository's HTML, inline, and reference-style links.
    # This is a path check, not a complete Markdown parser or an anchor check.
    text = prose(text)
    parser = Links()
    parser.feed(text)
    inline = re.findall(
        r"!?\[[^\]\n]*\]\(\s*(?:<([^>\n]+)>|((?:\\.|[^\s()\\]|\([^()\n]*\))+))"
        r"(?:\s+(?:\"[^\"\n]*\"|'[^'\n]*'))?\s*\)", text,
    )
    references = re.findall(
        r"^ {0,3}\[[^\]\n]+\]:\s*(?:<([^>\n]+)>|(\S+))", text, flags=re.M,
    )
    return parser.targets + [a or b for a, b in inline + references]


def skill_files(root: Path) -> list[Path]:
    # Only project-owned entry points. Do not discover vendored reference copies.
    return sorted((root / ".agents/skills").glob("*/SKILL.md"))


def check_links(root: Path, documents: tuple[str, ...] | None = None) -> list[str]:
    errors = []
    names = documents if documents is not None else (
        *DOCUMENTS, *(path.relative_to(root).as_posix() for path in skill_files(root))
    )
    for name in names:
        source = root / name
        try:
            targets = document_links(source.read_text(encoding="utf-8"))
        except (OSError, UnicodeError) as error:
            errors.append(f"{name}: cannot read document: {error}")
            continue
        for target in targets:
            try:
                url = urlsplit(unescape(target))
                if url.scheme or url.netloc or not url.path:
                    continue
                path = (source.parent / unquote(url.path)).resolve()
                if not path.is_relative_to(root.resolve()) or not path.exists():
                    errors.append(f"{name}: missing local target {target}")
            except (OSError, ValueError, RuntimeError) as error:
                errors.append(f"{name}: invalid local target {target}: {error}")
    return errors


def frontmatter_field(header: str, key: str) -> str:
    """Read required string fields; this deliberately is not a YAML parser."""
    match = re.search(r"^" + re.escape(key) + r":[ \t]*(.*)$", header, re.M)
    if not match:
        return ""
    value = match[1].strip()
    if re.fullmatch(r"[>|][-+]?", value):
        parts = []
        for line in header[match.end():].splitlines():
            if line.strip() and not line.startswith((" ", "\t")):
                break
            parts.append(line.strip())
        return " ".join(parts).strip()
    if value.startswith('"'):
        try:
            parsed = json.loads(value)
            return parsed if isinstance(parsed, str) else ""
        except ValueError:
            return ""
    if value.startswith("'"):
        return value[1:-1].replace("''", "'") if value.endswith("'") else ""
    return "" if value in ("null", "~") or value.startswith(("#", "[", "{")) else value


def check_skills(root: Path) -> list[str]:
    errors = []
    paths = skill_files(root)
    if not paths:
        return [".agents/skills: no project SKILL.md files found"]
    for path in paths:
        name = path.relative_to(root).as_posix()
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeError) as error:
            errors.append(f"{name}: cannot read skill: {error}")
            continue
        header = re.match(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|$)", text, re.S)
        if not header:
            errors.append(f"{name}: missing delimited frontmatter")
            continue
        declared = frontmatter_field(header[1], "name")
        if declared != path.parent.name or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", declared):
            errors.append(f"{name}: name must match its lower-case directory name")
        if not frontmatter_field(header[1], "description"):
            errors.append(f"{name}: missing non-empty description")
        if re.search(r"(?<![/\w.-])web/(?:src|scripts|components\.json)", text) or re.search(
            r"\bcd[ \t]+web(?=[ \t;&\r\n]|$)", text
        ):
            errors.append(f"{name}: stale frontend path; use apps/web")
    return errors


def badge_urls(text: str) -> list[str]:
    parser = Links()
    parser.feed(text)
    return [url for url in parser.images if url.startswith("https://")]


def check_badges(root: Path) -> list[str]:
    repo = "TokenNotIncluded/api.lmm.best"
    expected = [
        f"https://github.com/{repo}/actions/workflows/ci.yml/badge.svg?branch=main",
        *[f"https://img.shields.io/github/v/release/{repo}?filter={kind}-v%2A&label={label}&display_name=tag"
          for kind, label in (("go", "Go"), ("web", "Web"))],
        "https://img.shields.io/badge/license-AGPL--3.0-blue",
    ]
    return [f"{name}: unexpected CI, component release, or license badges"
            for name in ("README.md", "README_EN.md")
            if badge_urls((root / name).read_text(encoding="utf-8")) != expected]


def check_svg(path: Path, symbol: str) -> list[str]:
    text = path.read_text()
    if re.search(r"<!DOCTYPE|<!ENTITY|@import|url\s*\(", text, re.I):
        return [f"{path.name}: external or active SVG content"]
    svg = ET.fromstring(text)
    errors = []
    paths = []
    allowed = {"svg", "title", "desc", "style", "path"}
    for element in svg.iter():
        tag = element.tag.rsplit("}", 1)[-1]
        if tag not in allowed:
            errors.append(f"{path.name}: unexpected SVG element {tag}")
        if any(key.rsplit("}", 1)[-1].lower().startswith("on")
               or key.rsplit("}", 1)[-1] == "href" for key in element.attrib):
            errors.append(f"{path.name}: active SVG attribute")
        if tag == "path":
            paths.append(element.get("d"))
    if paths.count(symbol) != 1 or svg.get("viewBox") != "0 0 128 128":
        errors.append(f"{path.name}: symbol path or viewBox differs from master")
    ids = {element.get("id") for element in svg.iter() if element.text}
    labels = svg.get("aria-labelledby", "").split()
    if len(labels) != 2 or not set(labels).issubset(ids):
        errors.append(f"{path.name}: missing accessible title or description")
    return errors


def check_assets(root: Path) -> list[str]:
    geometry = json.loads((root / ".github/assets/logo-geometry.json").read_text())
    symbol = geometry["symbol"]["path"]
    errors = [error for name in SYMBOLS for error in check_svg(root / name, symbol)]
    component = (root / "apps/web/src/components/lmm-brand-mark.tsx").read_text()
    if re.findall(r"\bd='([^']+)'", component) != [symbol]:
        errors.append("LmmBrandMark: symbol differs from master")
    for name in geometry["provenance"]["productionRasterAssets"]:
        path = root / name
        sidecar = json.loads(path.with_name(path.name + ".json").read_text())
        if hashlib.sha256(path.read_bytes()).hexdigest() != sidecar["sha256"]:
            errors.append(f"{name}: raster differs from recorded source output")
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--docs-only", action="store_true", help="skip logo asset checks")
    args = parser.parse_args(argv)
    try:
        errors = check_links(ROOT) + check_skills(ROOT) + check_badges(ROOT)
        if not args.docs_only:
            errors += check_assets(ROOT)
    except (OSError, ValueError, KeyError, ET.ParseError) as error:
        errors = [str(error)]
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    if errors:
        return 1
    checked = "maintained document paths, skill metadata, and README badges"
    if not args.docs_only:
        checked += ", SVGs, shared geometry, and raster hashes"
    print(f"PASS: {checked}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
