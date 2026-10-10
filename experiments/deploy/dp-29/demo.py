#!/usr/bin/env python3
"""Create local synthetic nodes and sample JSON. Never contact a server."""
import argparse
import json
from pathlib import Path

from fixture import create, make_manifest

p = argparse.ArgumentParser(description=__doc__)
p.add_argument("directory", type=Path)
p.add_argument("--nodes", type=int, choices=(1, 3, 5), default=3)
a = p.parse_args()
a.directory.mkdir(parents=True, exist_ok=False)
inventory, before = create(a.directory / "nodes", a.nodes)
after = {component: make_manifest(component, 2, a.directory / "nodes") for component in before}
for name, content in (("inventory", inventory), ("before", before), ("after", after),
                      ("go-next", after["go-extensions"]), ("rust-next", after["rust-core"]),
                      ("web-next", after["web"])):
    (a.directory / (name + ".json")).write_text(json.dumps(content, indent=2) + "\n")
print("Created isolated synthetic fixtures in", a.directory)
