#!/usr/bin/env bash
# Pinned generators; generated Go source is checked in.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tools=$(mktemp -d)
trap 'rm -rf -- "$tools"' EXIT
export GOBIN="$tools" GOWORK=off
command -v protoc >/dev/null
(cd "$tools" && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11)
(cd "$tools" && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1)
export PATH="$tools:$PATH"
output="$root/services/extensions"
if [[ ${1:-} == --check ]]; then output="$tools/generated"; fi
mkdir -p "$output/internal/corepb"
protoc -I "$root/contracts/proto" \
  --go_out="$output" --go_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  --go-grpc_out="$output" --go-grpc_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  "$root/contracts/proto/lmm/core/v1/"*.proto
# Distro protoc versions change this comment, not the contract.
sed -i -E 's@^//[[:space:]]+(-[[:space:]]+)?protoc[[:space:]]+v[0-9.]+$@// protoc: proto3 compiler (version header normalized)@' \
  "$output/internal/corepb/"*.pb.go
gofmt -w "$output/internal/corepb/"*.pb.go
if [[ ${1:-} == --check ]]; then
  # Compare both directions. A removed proto must not leave stale Go bindings.
  for file in "$root/services/extensions/internal/corepb/"*.pb.go; do
    if [[ -f "$file" && ! -f "$output/internal/corepb/$(basename "$file")" ]]; then
      printf 'stale generated binding: %s\n' "$(basename "$file")" >&2
      exit 1
    fi
  done
  for file in "$output/internal/corepb/"*.pb.go; do
    diff -u "$root/services/extensions/internal/corepb/$(basename "$file")" "$file"
  done
fi
