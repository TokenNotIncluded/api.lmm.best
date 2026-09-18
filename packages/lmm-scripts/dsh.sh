#!/usr/bin/env bash
set -euo pipefail

if command -v dsh >/dev/null 2>&1; then
  exec dsh web "$@"
fi

echo "dsh is not installed. Install DeepSeek Harness, then run this script again." >&2
exit 1
