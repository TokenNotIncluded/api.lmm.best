#!/usr/bin/env bash
set -euo pipefail
[[ $# = 0 ]] || { echo 'Usage: bash menu.sh' >&2; exit 2; }
tools=(pi dsh lmm codex claude-code cc-switch clash-verge-rev codewhale)
if [[ -n ${TERMUX_VERSION:-} || ${PREFIX:-} == */com.termux/files/usr ]]; then tools=(pi dsh lmm codex claude-code codewhale); fi
dir=$(dirname -- "${BASH_SOURCE[0]:-}")
while true; do
  printf '\nInstall / update\n'
  for i in "${!tools[@]}"; do printf '%s  %s\n' "$((i+1))" "${tools[i]}"; done
  printf '0  Exit\n> '
  read -r choice </dev/tty || exit 1
  [[ $choice = 0 ]] && exit 0
  [[ $choice =~ ^[1-9][0-9]?$ ]] || continue
  ((choice <= ${#tools[@]})) || continue
  tool=${tools[choice-1]}
  # Positional parameters also work with empty arguments under Bash 3 + nounset.
  if [[ $tool = codewhale ]]; then set -- menu; else set --; fi
  if [[ -n ${BASH_SOURCE[0]:-} && -f $dir/$tool.sh ]]; then
    bash "$dir/$tool.sh" "$@" </dev/tty || printf 'Installation failed.\n' >&2
  else
    script=$(curl -fsSL "https://raw.githubusercontent.com/TokenNotIncluded/lmm-scripts/bd0a81de99d28c0ed5701cf290dade1e124dcecd/$tool.sh") || continue
    bash -c "$script" -- "$@" </dev/tty || printf 'Installation failed.\n' >&2
  fi
done
