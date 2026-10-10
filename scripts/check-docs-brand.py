#!/usr/bin/env python3
"""Check entry-document links, release badges, and shared logo assets offline."""
from __future__ import annotations

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
    "CONTRIBUTING.md", "DESIGN.md", ".github/assets/README.md",
    "docs/core-migration.md", "docs/release-architecture.md", "docs/legal/README.md",
    "docs/archive.md", "deployment/docker/README.md",
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


def document_links(text: str) -> list[str]:
    # These entry documents use inline links. Ignore examples inside code fences.
    text = re.sub(r"```.*?```", "", text, flags=re.S)
    parser = Links()
    parser.feed(text)
    return parser.targets + re.findall(r"!?\[[^\]]*\]\(([^\s)]+)\)", text)


def check_links(root: Path) -> list[str]:
    errors = []
    for name in DOCUMENTS:
        source = root / name
        for target in document_links(source.read_text()):
            url = urlsplit(unescape(target))
            if url.scheme or url.netloc or not url.path:
                continue
            path = (source.parent / unquote(url.path)).resolve()
            if not path.is_relative_to(root.resolve()) or not path.exists():
                errors.append(f"{name}: missing local target {target}")
    return errors


def badge_urls(text: str) -> list[str]:
    parser = Links()
    parser.feed(text)
    return [url for url in parser.images if url.startswith("https://")]


def check_badges(root: Path) -> list[str]:
    repo = "TokenNotIncluded/api.lmm.best"
    expected = [
        f"https://github.com/{repo}/actions/workflows/core-protocol.yml/badge.svg?branch=wip%2Frust-core-go-extensions",
        "https://img.shields.io/badge/license-AGPL--3.0-blue",
    ]
    return [f"{name}: unexpected CI, component release, or license badges"
            for name in ("README.md", "README_EN.md")
            if badge_urls((root / name).read_text()) != expected]


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


def main() -> int:
    try:
        errors = check_links(ROOT) + check_badges(ROOT) + check_assets(ROOT)
    except (OSError, ValueError, KeyError, ET.ParseError) as error:
        errors = [str(error)]
    for error in errors:
        print(f"ERROR: {error}", file=sys.stderr)
    if errors:
        return 1
    print("PASS: entry-document paths, README badges, SVGs, shared geometry, and raster hashes")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
