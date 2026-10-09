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
output="$root/apps/extensions-go"
if [[ ${1:-} == --check ]]; then output="$tools/generated"; fi
mkdir -p "$output/internal/corepb"
protoc -I "$root/contracts/proto" \
  --go_out="$output" --go_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  --go-grpc_out="$output" --go-grpc_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  "$root/contracts/proto/lmm/core/v1/control.proto"
# Distro protoc versions change this comment, not the contract.
sed -i -E 's@^//[[:space:]]+protoc[[:space:]]+v[0-9.]+$@// protoc: proto3 compiler (version header normalized)@' \
  "$output/internal/corepb/"*.pb.go
gofmt -w "$output/internal/corepb/"*.pb.go
if [[ ${1:-} == --check ]]; then
  for file in "$output/internal/corepb/"*.pb.go; do
    diff -u "$root/apps/extensions-go/internal/corepb/$(basename "$file")" "$file"
  done
fi
