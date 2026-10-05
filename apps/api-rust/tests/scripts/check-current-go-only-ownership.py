#!/usr/bin/env python3
"""Check current money-route ownership without changing frozen Go evidence.

Source/auth checks describe the registered groups, not runtime behavior or
Rust parity. The live Gin manifest must independently match every handler.
"""

import csv
from pathlib import Path
import re
import sys


ROOT = Path(__file__).resolve().parents[4]
SOURCE_PATH = "apps/api-go/router/api-router.go"
HANDLER_PREFIX = "github.com/LIghtJUNction/api.lmm.best/controller."
HEADER = ["method", "path", "owner", "auth_scope", "go_handler", "source_path", "contract"]
RAW_HANDLERS = {
    "/amount": "RequestAmount",
    "/pay": "RequestEpay",
    "/stripe/amount": "RequestStripeAmount",
    "/stripe/pay": "RequestStripePay",
    "/waffo/amount": "RequestWaffoAmount",
    "/waffo/pay": "RequestWaffoPay",
    "/waffo-pancake/amount": "RequestWaffoPancakeAmount",
    "/waffo-pancake/pay": "RequestWaffoPancakePay",
    "/discount-code/validate": "ValidateDiscountCode",
}
PRICING_HANDLERS = {
    ("GET", "/pricing"): "GetUSDPriceOptions",
    ("POST", "/pricing/validate"): "USDPriceOptionsValidate",
    ("POST", "/pricing/bulk"): "USDPriceOptionsBulk",
}
EXPECTED = {
    ("POST", "/api/user/topup/currency" + suffix): ("user", handler)
    for suffix, handler in RAW_HANDLERS.items()
} | {
    (method, "/api/option" + suffix): ("root", handler)
    for (method, suffix), handler in PRICING_HANDLERS.items()
}


def route_rows(path):
    return [line.split("\t") for line in path.read_text().splitlines()
            if line and not line.startswith("#")]


def check_ledger(rows, current, frozen, rust_ledgers):
    seen = set()
    for row in rows:
        if len(row) != len(HEADER):
            raise ValueError("current ownership row must have seven fields")
        method, path, owner, auth, handler, source, contract = row
        identity = (method, path)
        if identity in seen:
            raise ValueError(f"duplicate current ownership: {method} {path}")
        seen.add(identity)
        if identity not in EXPECTED:
            raise ValueError(f"unexpected current money route: {method} {path}")
        expected_auth, expected_handler = EXPECTED[identity]
        if (owner != "go" or auth != expected_auth or
                handler != HANDLER_PREFIX + expected_handler or
                source != SOURCE_PATH or not contract.strip()):
            raise ValueError(f"invalid current Go-only ownership: {method} {path}")
        if current.get(identity) != handler:
            raise ValueError(f"current Gin manifest handler mismatch: {method} {path}")
        if identity in frozen:
            raise ValueError(f"current-only route is in immutable frozen evidence: {method} {path}")
        for name, routes in rust_ledgers.items():
            if identity in routes:
                raise ValueError(f"Go-only route is claimed by {name}: {method} {path}")
    if seen != EXPECTED.keys():
        raise ValueError("current ownership must contain all nine raw-credit POST and three pricing routes")


def without_comments(source):
    # Keep quoted strings intact: comment markers inside a path are not comments.
    return re.sub(r'("(?:\\.|[^"\\])*"|`[^`]*`)|/\*.*?\*/|//[^\n]*',
                  lambda match: match[1] or "", source, flags=re.S)


def group_body(source, name, parent, suffix, auth=None):
    declaration = rf'\b{re.escape(name)}\s*:=\s*{re.escape(parent)}\.Group\s*\(\s*"{re.escape(suffix)}"\s*\)'
    matches = list(re.finditer(declaration, source))
    if len(matches) != 1:
        raise ValueError(f"expected one {name} group under {parent}")
    tail = source[matches[0].end():]
    opening = re.match(rf'\s*((?:{name}\.Use\([^;\n]*\)\s*)*)\{{', tail)
    if not opening:
        raise ValueError(f"cannot establish {name} registration block")
    uses = opening[1]
    if auth and not re.search(rf'\bmiddleware\.{auth}\s*\(\s*\)', uses):
        raise ValueError(f"{name} group lacks its own {auth} boundary")
    start = opening.end()
    depth = 1
    # Ignore braces inside quoted Go strings while finding this group's close.
    for token in re.finditer(r'"(?:\\.|[^"\\])*"|`[^`]*`|[{}]', tail[start:]):
        if token[0] == "{":
            depth += 1
        elif token[0] == "}":
            depth -= 1
            if depth == 0:
                return tail[start:start + token.start()]
    raise ValueError(f"unclosed {name} registration block")


def declared_routes(source, group):
    pattern = rf'(?m)^\s*{group}\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\("([^"]*)",([^\n]*)\)\s*$'
    return [(match[1], match[2], match[3]) for match in re.finditer(pattern, source)]


def check_source(source):
    source = without_comments(source)
    if not re.search(r'\bapiRouter\s*:=\s*&assistantRouterGroup\s*\{\s*group:\s*router\.Group\("/api"\)', source):
        raise ValueError("money route groups are not under /api")
    user = group_body(source, "userRoute", "apiRouter", "/user")
    user_self = group_body(user, "selfRoute", "userRoute", "/", "UserAuth")
    if len(re.findall(r'\bcurrencyTopUpRoute\s*:=\s*selfRoute\.Group\("/topup/currency"\)', user_self)) != 1:
        raise ValueError("canonical money routes lack their distinct user-owned group")
    raw = declared_routes(user_self, "currencyTopUpRoute")
    if {(method, suffix) for method, suffix, _ in raw} != {("POST", suffix) for suffix in RAW_HANDLERS} or len(raw) != 9:
        raise ValueError("canonical user group must declare exactly nine raw-credit POST routes")
    for _, suffix, args in raw:
        if (not re.search(r'\bcontroller\.RequireCanonicalTopUpCredit\s*,', args) or
                not re.search(rf'\bcontroller\.{RAW_HANDLERS[suffix]}\s*$', args)):
            raise ValueError(f"canonical route guard/handler mismatch: {suffix}")
    option = group_body(source, "optionRoute", "apiRouter", "/option", "RootAuth")
    pricing = [(method, suffix, args) for method, suffix, args in declared_routes(option, "optionRoute")
               if suffix.startswith("/pricing")]
    if {(method, suffix) for method, suffix, _ in pricing} != PRICING_HANDLERS.keys() or len(pricing) != 3:
        raise ValueError("root option group must declare exactly three pricing routes")
    for method, suffix, args in pricing:
        if not re.search(rf'\bcontroller\.{PRICING_HANDLERS[(method, suffix)]}\s*$', args):
            raise ValueError(f"root pricing handler mismatch: {method} {suffix}")


def main():
    if len(sys.argv) != 2:
        raise ValueError("usage: check-current-go-only-ownership.py current-go-route-manifest.tsv")
    directory = ROOT / "apps/api-rust/tests/fixtures/routes"
    with (directory / "current-go-only-ownership.tsv").open(newline="") as file:
        rows = list(csv.reader(file, delimiter="\t"))
    if not rows or rows.pop(0) != HEADER:
        raise ValueError("invalid current ownership header")
    current = {}
    for row in route_rows(Path(sys.argv[1])):
        if len(row) != 3 or tuple(row[:2]) in current:
            raise ValueError("malformed or duplicate current Gin route")
        current[tuple(row[:2])] = row[2]
    frozen = {tuple(row[:2]) for row in route_rows(directory / "legacy-go-routes.tsv")}
    rust_ledgers = {
        name: {tuple(row[:2]) for row in route_rows(directory / name)}
        for name in ("rust-implemented-routes.tsv", "rust-normal-mounted-routes.tsv",
                     "rust-mounted-fail-closed-shells.tsv")
    }
    check_ledger(rows, current, frozen, rust_ledgers)
    check_source((ROOT / SOURCE_PATH).read_text())
    print("current Go-only ownership: nine user raw-credit POST + three root pricing routes; exact Gin handlers/source; no Rust ownership or parity credit")


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError) as error:
        print(f"current Go-only ownership: {error}", file=sys.stderr)
        sys.exit(1)
