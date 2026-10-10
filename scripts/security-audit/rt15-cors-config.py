"""Check explicit Authorization configuration; not an HTTP or browser test."""
import argparse
import json
import re
from pathlib import Path


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="Path to middleware/cors.go")
    args = parser.parse_args()
    source = args.source.read_text(encoding="utf-8")
    match = re.search(r'config\.AllowHeaders\s*=\s*\[\]string\{([^}]*)\}', source)
    if not match:
        raise RuntimeError("CORS assignment changed; inspect the actual configuration")
    headers = re.findall(r'"([^"\\]*)"', match.group(1))
    passed = any(header.lower() == "authorization" for header in headers)
    print(json.dumps({
        "kind": "source_configuration_only",
        "allow_headers": headers,
        "explicit_authorization": passed,
        "status": "pass" if passed else "fail",
    }, indent=2))
    return 0 if passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
