#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'check-aur-candidate-version: %s\n' "$*" >&2
  exit 1
}

[[ $# -eq 3 ]] || fail 'usage: check-candidate-version.sh PACKAGE CANDIDATE PUBLISHED'
readonly package=$1 candidate=$2 published=$3
[[ $package =~ ^[a-z0-9@._+-]+$ && $candidate != *[[:space:]]* && $published != *[[:space:]]* ]] ||
  fail 'package or version is invalid'
command -v vercmp >/dev/null 2>&1 || fail 'vercmp is unavailable'

(( $(vercmp "$published" "$candidate") <= 0 )) ||
  fail "$package candidate $candidate is older than published $published"
printf '%s candidate %s is not older than published %s\n' "$package" "$candidate" "$published"
