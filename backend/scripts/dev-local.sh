#!/bin/bash
# Run the Bullseye hub + fake-sniffer locally for end-to-end testing.
# Usage: ./backend/scripts/dev-local.sh
#
# This starts:
#   - hub on :9090 (gRPC, sniffer→hub) and :3030 (WebSocket, hub→UI)
#   - fake-sniffer connecting to the hub, streaming 10 entries/sec
#
# Then you can connect a WebSocket client to ws://localhost:3030/ to verify
# entries are being enriched + relayed. The Next.js dashboard at :3000 will
# also connect to the hub once the traffic-store.ts SIM_PORT is updated.

set -e

BACKEND_DIR="/home/z/my-project/backend"
HUB_BIN="$BACKEND_DIR/bin/hub"
SNIFFER_BIN="$BACKEND_DIR/bin/fake-sniffer"
DATA_DIR="/tmp/bullseye-data"
HUB_LOG="/tmp/bullseye-hub.log"
SNIFFER_LOG="/tmp/bullseye-fake-sniffer.log"
HUB_PID_FILE="/tmp/bullseye-hub.pid"
SNIFFER_PID_FILE="/tmp/bullseye-fake-sniffer.pid"

# kill any existing instances
for f in "$HUB_PID_FILE" "$SNIFFER_PID_FILE"; do
  if [ -f "$f" ]; then
    OLDPID=$(cat "$f" 2>/dev/null || echo "")
    if [ -n "$OLDPID" ] && kill -0 "$OLDPID" 2>/dev/null; then
      kill "$OLDPID" 2>/dev/null || true
    fi
  fi
done
pkill -f "bullseye/bin/hub" 2>/dev/null || true
pkill -f "bullseye/bin/fake-sniffer" 2>/dev/null || true
sleep 1

mkdir -p "$DATA_DIR"
rm -f "$DATA_DIR/hub.db"

# start hub — fully detached via setsid
echo "[dev-local] starting hub..."
IN_CLUSTER=false \
  WS_PORT=3030 \
  GRPC_PORT=9090 \
  STORAGE_PATH="$DATA_DIR/hub.db" \
  LOG_LEVEL=info \
  setsid nohup "$HUB_BIN" > "$HUB_LOG" 2>&1 < /dev/null &
HUB_PID=$!
echo "$HUB_PID" > "$HUB_PID_FILE"
disown 2>/dev/null || true

# wait for hub to come up
for i in $(seq 1 20); do
  if curl -s --max-time 1 "http://127.0.0.1:3030/healthz" >/dev/null 2>&1; then
    echo "[dev-local] hub is up (PID $HUB_PID, port 3030 + 9090)"
    break
  fi
  sleep 0.5
done

# start fake-sniffer
echo "[dev-local] starting fake-sniffer..."
setsid nohup "$SNIFFER_BIN" --hub=localhost:9090 --rate=10 --node=dev-node-1 > "$SNIFFER_LOG" 2>&1 < /dev/null &
SNIFFER_PID=$!
echo "$SNIFFER_PID" > "$SNIFFER_PID_FILE"
disown 2>/dev/null || true

sleep 2
echo "[dev-local] fake-sniffer is up (PID $SNIFFER_PID)"
echo ""
echo "  hub log:        $HUB_LOG"
echo "  sniffer log:    $SNIFFER_LOG"
echo "  bolt db:        $DATA_DIR/hub.db"
echo ""
echo "  hub ws:         ws://localhost:3030/   (UI connects here)"
echo "  hub grpc:       localhost:9090          (sniffers connect here)"
echo "  hub health:     http://localhost:3030/healthz"
echo ""
echo "  stop with:      pkill -f 'bullseye/bin/hub'; pkill -f 'bullseye/bin/fake-sniffer'"
