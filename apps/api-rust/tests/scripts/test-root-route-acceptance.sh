#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
manifest="$repo_root/apps/api-rust/tests/root-route-acceptance/Cargo.toml"
runner="$repo_root/apps/api-rust/tests/scripts/run-root-route-acceptance.sh"
target_dir=${CARGO_TARGET_DIR:-"$repo_root/apps/api-rust/target/root-route-acceptance-tests"}

bash -n "$runner" "$0"
if command -v shellcheck >/dev/null; then
  shellcheck "$runner" "$0"
fi

grep -Fq -- '--features runtime' "$runner" || {
  echo "root-route runner does not enable the real-router runtime" >&2
  exit 1
}
grep -Fq 'legacy-go-routes.tsv' "$repo_root/apps/api-rust/tests/root-route-acceptance/src/inventory.rs" || {
  echo "root-route inventory is not bound to the frozen 353-route baseline" >&2
  exit 1
}
grep -Fq 'route-plan.tsv' "$repo_root/apps/api-rust/tests/root-route-acceptance/src/inventory.rs" || {
  echo "root-route inventory is not bound to route auth classes" >&2
  exit 1
}

python3 - "$repo_root/apps/api-rust/src/main.rs" <<'PY'
from pathlib import Path
import re
import sys


def balanced_block(source: str, start: int) -> tuple[str, int]:
    opening = source.find("{", start)
    if opening < 0:
        raise AssertionError("test-instance branch has no opening brace")
    depth = 0
    for index in range(opening, len(source)):
        if source[index] == "{":
            depth += 1
        elif source[index] == "}":
            depth -= 1
            if depth == 0:
                return source[opening + 1 : index], index + 1
    raise AssertionError("test-instance branch has no closing brace")


def call_arguments(source: str, function: str) -> str:
    match = re.search(rf"\b{re.escape(function)}\s*\(", source)
    if not match:
        raise AssertionError(f"missing {function} call")
    opening = source.find("(", match.start())
    depth = 0
    for index in range(opening, len(source)):
        if source[index] == "(":
            depth += 1
        elif source[index] == ")":
            depth -= 1
            if depth == 0:
                return source[opening + 1 : index]
    raise AssertionError(f"unterminated {function} call")


source = Path(sys.argv[1]).read_text()
marker = "let router = if config.test_instance"
branch_start = source.find(marker)
if branch_start < 0:
    raise AssertionError("missing explicit test-instance listener branch")
test_instance, after_test_instance = balanced_block(source, branch_start)
else_match = re.match(r"\s*else\s*", source[after_test_instance:])
if not else_match:
    raise AssertionError("test-instance listener branch has no normal else branch")
normal, _ = balanced_block(source, after_test_instance + else_match.end())

candidate_args = call_arguments(
    test_instance, "router_with_api_token_and_extra"
)
normal_function = (
    "router_with_api_token_and_extra"
    if "router_with_api_token_and_extra" in normal
    else "router_with_api_token"
)
normal_args = call_arguments(normal, normal_function)
if not re.search(r"\bSome\s*\(\s*api_token\b", candidate_args):
    raise AssertionError("test-instance candidate no longer mounts API-token routes")
if not re.search(r"\bSome\s*\(\s*api_token\b", normal_args):
    raise AssertionError("normal listener can mount API-token routes")
if "with_current_dashboard_discovery_policy" not in normal:
    raise AssertionError("normal listener is missing the current API-token policy")
PY

CARGO_TARGET_DIR="$target_dir" cargo test --offline --locked \
  --manifest-path "$manifest" --no-default-features
CARGO_TARGET_DIR="$target_dir" cargo clippy --offline --locked \
  --manifest-path "$manifest" --no-default-features --all-targets -- -D warnings

echo "root-route acceptance support tests passed"
