#!/bin/bash
# validate-reconnection.sh - Test NATS reconnection behavior
#
# This script validates that cluster nodes handle NATS disconnection
# and reconnection gracefully.
#
# Usage:
#   ./scripts/validate-reconnection.sh
#
# Prerequisites:
#   - Docker (for running NATS containers)
#   - Build CLI: go build -o go-cluster-cli ./examples/cli

set -e

CLUSTER_ID="reconnect-test-$(date +%s)"
CLI_BIN="./go-cluster-cli"
TMP_DIR=$(mktemp -d)
NATS_CONTAINER="nats-reconnect-test"

cleanup() {
    echo "Cleaning up..."
    jobs -p | xargs -r kill 2>/dev/null || true
    docker rm -f "$NATS_CONTAINER" 2>/dev/null || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "=== go-cluster Reconnection Validation ==="
echo "Cluster ID: $CLUSTER_ID"
echo

# Check prerequisites
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is required for this test."
    exit 1
fi

if [ ! -f "$CLI_BIN" ]; then
    echo "Building CLI..."
    go build -o "$CLI_BIN" ./examples/cli
fi

echo "1. Starting NATS server in Docker..."
docker run -d --name "$NATS_CONTAINER" -p 4222:4222 nats:2.10 -js
sleep 2

NATS_URL="nats://localhost:4222"

# Create config file with reconnection settings
cat > "$TMP_DIR/node1.json" << EOF
{
  "clusterId": "$CLUSTER_ID",
  "nodeId": "node-1",
  "nats": {
    "servers": ["$NATS_URL"],
    "reconnectWaitMs": 1000,
    "maxReconnects": -1
  },
  "election": {
    "leaseTtlMs": 10000,
    "heartbeatIntervalMs": 3000
  }
}
EOF

echo "2. Starting cluster node..."
$CLI_BIN daemon --config "$TMP_DIR/node1.json" --log-level debug 2>&1 | tee "$TMP_DIR/node.log" &
NODE_PID=$!
sleep 4

echo "3. Checking node status before disconnect..."
STATUS=$($CLI_BIN status --config "$TMP_DIR/node1.json" --json 2>/dev/null || echo '{"connected":false}')
echo "$STATUS" | jq . 2>/dev/null || echo "$STATUS"
echo

CONNECTED=$(echo "$STATUS" | jq -r '.connected' 2>/dev/null || echo "false")
if [ "$CONNECTED" = "true" ]; then
    echo "✓ Node is connected"
else
    echo "✗ Node is not connected"
fi
echo

echo "4. Simulating NATS server restart (stopping container)..."
docker stop "$NATS_CONTAINER"
echo "NATS server stopped. Waiting 5 seconds..."
sleep 5

echo
echo "5. Restarting NATS server..."
docker start "$NATS_CONTAINER"
echo "NATS server restarted. Waiting for reconnection..."
sleep 5

echo
echo "6. Checking node status after reconnect..."
STATUS_AFTER=$($CLI_BIN status --config "$TMP_DIR/node1.json" --json 2>/dev/null || echo '{"connected":false}')
echo "$STATUS_AFTER" | jq . 2>/dev/null || echo "$STATUS_AFTER"
echo

CONNECTED_AFTER=$(echo "$STATUS_AFTER" | jq -r '.connected' 2>/dev/null || echo "false")
if [ "$CONNECTED_AFTER" = "true" ]; then
    echo "✓ Node reconnected successfully!"
else
    echo "✗ Node failed to reconnect"
    echo "Check logs at: $TMP_DIR/node.log"
fi

echo
echo "7. Checking node logs for reconnection events..."
grep -i "reconnect\|disconnect" "$TMP_DIR/node.log" | tail -10 || echo "(no reconnection events logged)"

echo
echo "8. Stopping node..."
kill $NODE_PID 2>/dev/null || true
wait $NODE_PID 2>/dev/null || true

echo
echo "=== Reconnection Validation Complete ==="
