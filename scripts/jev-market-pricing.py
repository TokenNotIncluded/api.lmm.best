#!/usr/bin/env python3
"""Prepare Jev's 10x price draft through normal marketplace APIs.

Does not publish, alter descriptions, read a TypeSafe key, or edit the database.
The optional save creates a reviewed-version candidate; validate/review normally.
"""
import argparse
import decimal
import json
import os
import pathlib
import stat
import urllib.error
import urllib.parse
import urllib.request


def draft_for(detail, credits_per_usd):
    anchor = decimal.Decimal(str(credits_per_usd))
    if not anchor.is_finite() or anchor <= 0:
        raise ValueError("missing immutable credits_per_usd")
    rate = int((anchor * decimal.Decimal("0.42")).to_integral_value(rounding=decimal.ROUND_CEILING))
    tools = []
    for tool in detail["tools"]:
        if not tool["name"].startswith("jev_"):
            raise ValueError("service contains a non-Jev tool")
        cap = tool.get("max_input_tokens") or 65536
        if not isinstance(cap, int) or not 0 < cap <= 65536:
            raise ValueError("invalid input token cap")
        schema = tool["input_schema"]
        output = tool.get("output_schema")
        tools.append({
            "name": tool["name"], "description": tool["description"],
            "input_schema": json.loads(schema) if isinstance(schema, str) else schema,
            "output_schema": json.loads(output) if isinstance(output, str) and output else output or None,
            "permissions": json.loads(tool["permissions"]) if isinstance(tool["permissions"], str) else tool["permissions"],
            "billing_mode": "input_tokens", "input_token_price_quota": rate,
            "max_input_tokens": cap, "price_quota": (rate * cap + 999999) // 1000000,
        })
    if not tools:
        raise ValueError("no Jev tools")
    v = detail["version"]
    return {"name": v["name"], "description": v["description"], "endpoint": v["endpoint"],
            "execution_type": "remote", "visibility": v["visibility"],
            "allowed_users": detail.get("allowed_users", []), "tools": tools}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise ValueError("API redirects are not allowed")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="https://api.lmm.best")
    parser.add_argument("--service-id", required=True)
    parser.add_argument("--auth-file", type=pathlib.Path, required=True, help="private file containing the service owner's platform API token, not a TypeSafe key")
    parser.add_argument("--output", type=pathlib.Path, required=True)
    parser.add_argument("--save-draft", action="store_true")
    args = parser.parse_args()
    url = urllib.parse.urlsplit(args.base_url)
    if url.scheme != "https" or not url.netloc or url.username or url.query or url.fragment or url.path not in ("", "/"):
        raise ValueError("base URL must be a HTTPS origin")
    if not args.service_id or any(ch not in "0123456789abcdef-" for ch in args.service_id):
        raise ValueError("invalid service ID")
    info = args.auth_file.lstat()
    if not stat.S_ISREG(info.st_mode) or info.st_mode & 0o077 or info.st_size > 4096 or info.st_nlink != 1:
        raise ValueError("auth file must be a private regular file")
    token = args.auth_file.read_text().strip()
    if not token or any(ch.isspace() for ch in token):
        raise ValueError("invalid platform token file")
    opener = urllib.request.build_opener(NoRedirect(), urllib.request.ProxyHandler({}))
    base = args.base_url.rstrip("/") + "/api/tool-market"

    def request(path, method="GET", body=None):
        raw = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(base + path, data=raw, method=method,
                                     headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"})
        with opener.open(req, timeout=30) as response:
            payload = json.load(response)
        if not payload.get("success"):
            raise ValueError("marketplace API rejected request")
        return payload["data"]

    config = request("/config")
    if config.get("usage_policy") != "tool_reported":
        raise ValueError("reported usage deployment is not active")
    # Owner-only draft access avoids losing a shared/private version's allowlist.
    try:
        detail = request(f"/services/{args.service_id}/draft")
    except urllib.error.HTTPError as error:
        if error.code != 404:
            raise
        detail = request(f"/services/{args.service_id}")
    draft = draft_for(detail, config.get("credits_per_usd", ""))
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as stream:
        json.dump(draft, stream, ensure_ascii=False, indent=2)
        stream.write("\n")
    if args.save_draft:
        request(f"/services/{args.service_id}/draft", "PUT", draft)
    print("Jev price prepared: USD 0.42 per million input tokens; output free; descriptions preserved; published price unchanged.")


if __name__ == "__main__":
    try:
        main()
    except Exception:
        raise SystemExit("Price preparation failed. Check deployment, owner credentials, and output path; no automatic publication.") from None
