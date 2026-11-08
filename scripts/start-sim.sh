#!/bin/bash
# Start the Kubeshark traffic simulator mini-service.
# Designed to be fully detached — survives the parent shell.

set -e
SIM_DIR="/home/z/my-project/mini-services/traffic-simulator"
LOG_FILE="/tmp/kubeshark-sim.log"
PID_FILE="/tmp/kubeshark-sim.pid"

# kill any existing instance
if [ -f "$PID_FILE" ]; then
  OLDPID=$(cat "$PID_FILE" 2>/dev/null || echo "")
  if [ -n "$OLDPID" ] && kill -0 "$OLDPID" 2>/dev/null; then
    echo "Stopping existing simulator (pid $OLDPID)"
    kill "$OLDPID" 2>/dev/null || true
    sleep 1
  fi
fi
pkill -f "bun.*traffic-simulator/index.ts" 2>/dev/null || true
sleep 1

cd "$SIM_DIR"
# fully detached — setsid + nohup + redirect stdin from /dev/null
setsid nohup bun index.ts > "$LOG_FILE" 2>&1 < /dev/null &
NEW_PID=$!
echo "$NEW_PID" > "$PID_FILE"
disown "$NEW_PID" 2>/dev/null || true

# wait for it to come up
for i in $(seq 1 30); do
  if curl -s --max-time 1 "http://127.0.0.1:3030/" -o /dev/null 2>&1; then
    echo "OK: simulator is up (pid $NEW_PID, port 3030)"
    exit 0
  fi
  sleep 0.5
done

echo "FAIL: simulator did not come up"
echo "--- log ---"
cat "$LOG_FILE" 2>/dev/null
exit 1
