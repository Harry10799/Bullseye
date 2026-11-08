#!/bin/bash
# Start the Bullseye hub + fake-sniffer locally for end-to-end testing.
# Robust detached version — survives parent shell exit.
set -e

BACKEND_DIR="/home/z/my-project/backend"
DATA_DIR="/tmp/bullseye-data"
HUB_LOG="/tmp/bullseye-hub.log"
SNIFFER_LOG="/tmp/bullseye-fake-sniffer.log"

# kill any existing instances
pkill -f "bullseye/bin/hub" 2>/dev/null || true
pkill -f "bullseye/bin/fake-sniffer" 2>/dev/null || true
sleep 2

mkdir -p "$DATA_DIR"
rm -f "$DATA_DIR/hub.db"

# start hub — fully detached via setsid + nohup + </dev/null
echo "[dev-local] starting hub..."
( IN_CLUSTER=false \
  WS_PORT=3030 GRPC_PORT=9090 METRICS_PORT=49100 \
  STORAGE_PATH="$DATA_DIR/hub.db" LOG_LEVEL=info \
  setsid /home/z/my-project/backend/bin/hub \
    >"$HUB_LOG" 2>&1 </dev/null & ) 2>/dev/null

# wait for hub to be ready
for i in $(seq 1 30); do
  if curl -s --max-time 1 http://127.0.0.1:3030/healthz 2>/dev/null | grep -q ok; then
    echo "[dev-local] hub is up (port 3030 + 9090 + 49100)"
    break
  fi
  sleep 0.5
done

# start fake-sniffer
echo "[dev-local] starting fake-sniffer..."
( setsid /home/z/my-project/backend/bin/fake-sniffer \
    --hub=localhost:9090 --rate=10 --node=dev-node-1 \
    >"$SNIFFER_LOG" 2>&1 </dev/null & ) 2>/dev/null

sleep 3
echo "[dev-local] fake-sniffer is up"
echo ""
echo "  hub ws:         ws://localhost:3030/   (UI connects here)"
echo "  hub grpc:       localhost:9090          (sniffers connect here)"
echo "  hub metrics:    http://localhost:49100/metrics"
echo "  hub health:     http://localhost:3030/healthz"
echo ""
echo "  logs:           $HUB_LOG  /  $SNIFFER_LOG"
echo ""
echo "  stop with:      pkill -f 'bullseye/bin/hub'; pkill -f 'bullseye/bin/fake-sniffer'"
