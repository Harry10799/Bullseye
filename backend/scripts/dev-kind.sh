#!/usr/bin/env bash
# Spin up a local kind cluster + deploy Bullseye for development.
# Prereqs: kind, kubectl, docker
set -euo pipefail

BACKEND_DIR="$(cd "$(dirname "$0")/.." && pwd)"
CLUSTER_NAME="${KIND_CLUSTER:-bullseye-dev}"

echo "[dev-kind] ensuring kind is installed"
if ! command -v kind >/dev/null 2>&1; then
  echo "  installing kind..."
  go install sigs.k8s.io/kind@latest
  export PATH="$PATH:$(go env GOPATH)/bin"
fi

echo "[dev-kind] creating cluster '$CLUSTER_NAME' (if not exists)"
if ! kind get clusters 2>/dev/null | grep -q "^$CLUSTER_NAME$"; then
  kind create cluster --name "$CLUSTER_NAME" --image kindest/node:v1.31.0
fi

echo "[dev-kind] loading images into kind"
kind load docker-image harikumar15/bulleyes-hub:latest --name "$CLUSTER_NAME" || true
kind load docker-image harikumar15/bulleyes-sniffer:latest --name "$CLUSTER_NAME" || true

echo "[dev-kind] deploying manifests"
kubectl apply -k "$BACKEND_DIR/deploy"

echo "[dev-kind] waiting for hub to be ready"
kubectl -n bullseye-system wait --for=condition=ready pod -l app.kubernetes.io/component=hub --timeout=120s

echo "[dev-kind] port-forwarding hub"
kubectl -n bullseye-system port-forward svc/bullseye-hub 3030:3030 9090:9090 &
PF_PID=$!
echo "  port-forward PID: $PF_PID"
echo "  dashboard: http://localhost:3030"
echo "  grpc:       localhost:9090"
echo "  stop with: kill $PF_PID"

wait $PF_PID
