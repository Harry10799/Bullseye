#!/usr/bin/env bash
# Build all Bullseye backend components.
# Usage: ./backend/scripts/build.sh [--docker]
set -euo pipefail

BACKEND_DIR="$(cd "$(dirname "$0")/.." && pwd)"
cd "$BACKEND_DIR"

DOCKER="${1:-}"
if [[ "$DOCKER" == "--docker" ]]; then
  echo "[build] building Docker images"
  docker build -t bullseye/hub:latest -f hub/Dockerfile "$BACKEND_DIR/hub"
  docker build -t bullseye/sniffer:latest -f sniffer/Dockerfile "$BACKEND_DIR/sniffer"
  # docker build -t bullseye/tracer:latest -f ebpf/Dockerfile "$BACKEND_DIR/ebpf"
  exit 0
fi

echo "[build] generating protobuf code"
bash "$BACKEND_DIR/scripts/gen-proto.sh"

echo "[build] building hub binary"
cd "$BACKEND_DIR/hub"
go build -o "$BACKEND_DIR/bin/hub" ./cmd/hub

echo "[build] building sniffer binary"
cd "$BACKEND_DIR/sniffer"
go build -o "$BACKEND_DIR/bin/sniffer" ./cmd/sniffer

echo "[build] done. Binaries in $BACKEND_DIR/bin/"
ls -la "$BACKEND_DIR/bin/"
