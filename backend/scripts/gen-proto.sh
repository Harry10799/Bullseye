#!/usr/bin/env bash
# Generate Go code from bullseye.proto.
# Requires protoc + protoc-gen-go + protoc-gen-go-grpc.
set -euo pipefail

BACKEND_DIR="$(cd "$(dirname "$0")/.." && pwd)"
PROTO_DIR="$BACKEND_DIR/proto"
OUT_DIR="$PROTO_DIR/bullseye/v1"

echo "[gen-proto] ensuring protoc tools are installed"
GOBIN="${GOBIN:-$HOME/go/bin}"
export PATH="$PATH:$GOBIN"

if ! command -v protoc >/dev/null 2>&1; then
  echo "[gen-proto] protoc not installed. Installing..."
  PROTOC_VERSION=27.5
  PROTOC_ZIP=protoc-$PROTOC_VERSION-linux-x86_64.zip
  curl -sL "https://github.com/protocolbuffers/protobuf/releases/download/v$PROTOC_VERSION/$PROTOC_ZIP" -o /tmp/$PROTOC_ZIP
  unzip -o /tmp/$PROTOC_ZIP -d "$HOME/.local" >/dev/null
  export PATH="$HOME/.local/bin:$PATH"
fi

go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

echo "[gen-proto] generating Go code"
protoc \
  --proto_path="$PROTO_DIR" \
  --go_out="$PROTO_DIR" \
  --go_opt=module=github.com/bullseye/bullseye/backend/proto \
  --go-grpc_out="$PROTO_DIR" \
  --go-grpc_opt=module=github.com/bullseye/bullseye/backend/proto \
  "$PROTO_DIR/bullseye.proto"

echo "[gen-proto] generated:"
ls -la "$OUT_DIR"/*.pb.go
