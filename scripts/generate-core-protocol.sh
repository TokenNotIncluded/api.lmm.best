#!/usr/bin/env bash
# Pinned generators; generated Go source is checked in.
set -euo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
tools=$(mktemp -d)
trap 'rm -rf -- "$tools"' EXIT
export GOBIN="$tools" GOWORK=off
command -v protoc >/dev/null
(cd "$tools" && go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.10)
(cd "$tools" && go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1)
export PATH="$tools:$PATH"
mkdir -p "$root/apps/extensions-go/internal/corepb"
protoc -I "$root/contracts/proto" \
  --go_out="$root/apps/extensions-go" --go_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  --go-grpc_out="$root/apps/extensions-go" --go-grpc_opt=module=github.com/TokenNotIncluded/api.lmm.best/extensions \
  "$root/contracts/proto/lmm/core/v1/control.proto"
# Distro protoc versions change this comment, not the contract.
sed -i -E 's@^//[[:space:]]+protoc[[:space:]]+v[0-9.]+$@// protoc: proto3 compiler (version header normalized)@' \
  "$root/apps/extensions-go/internal/corepb/"*.go
if [[ ${1:-} == --check ]]; then
  git -C "$root" diff --exit-code -- apps/extensions-go/internal/corepb
fi
