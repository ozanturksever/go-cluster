#!/bin/bash
# validate-discovery.sh - Test NATS micro service discovery
#
# This script validates that cluster nodes register themselves as NATS micro services
# and can be discovered using the standard $SRV.* subjects.
#
# Usage:
#   ./scripts/validate-discovery.sh
#
# Prerequisites:
#   - NATS server running: nats-server -js
#   - NATS CLI installed: brew install nats-io/nats-tools/nats
#   - Build CLI: go build -o go-cluster-cli ./examples/cli

set -e

NATS_URL=${NATS_URL:-"nats://localhost:4222"}
CLUSTER_ID="discovery-test-$(date +%s)"
CLI_BIN="./go-cluster-cli"
TMP_DIR=$(mktemp -d)

cleanup() {
    echo "Cleaning up..."
    jobs -p | xargs -r kill 2>/dev/null || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "=== go-cluster Service Discovery Validation ==="
echo "NATS URL: $NATS_URL"
echo "Cluster ID: $CLUSTER_ID"
echo
# Note: Service names use underscore (cluster_<id>) not dot (cluster.<id>)
# because NATS micro service names work better with underscores for
# subject hierarchy parsing. The dot format is used within subject paths.

# Check prerequisites
if ! command -v nats &> /dev/null; then
    echo "Error: 'nats' CLI is required for this test."
    echo "Install it with: brew install nats-io/nats-tools/nats"
    exit 1
fi

if [ ! -f "$CLI_BIN" ]; then
    echo "Building CLI..."
    go build -o "$CLI_BIN" ./examples/cli
fi

# Create config file
cat > "$TMP_DIR/node1.json" << EOF
{
  "clusterId": "$CLUSTER_ID",
  "nodeId": "node-1",
  "nats": {
    "servers": ["$NATS_URL"]
  },
  "service": {
    "version": "2.0.0"
  },
  "election": {
    "leaseTtlMs": 5000,
    "heartbeatIntervalMs": 1000
  }
}
EOF

echo "1. Starting cluster node..."
$CLI_BIN daemon --config "$TMP_DIR/node1.json" --log-level warn &
NODE_PID=$!
sleep 3

echo "2. Listing micro services..."
echo "   Running: nats micro ls"
nats micro ls --server="$NATS_URL" || true
echo

SERVICE_NAME="cluster_$CLUSTER_ID"

echo "3. Getting service info..."
echo "   Running: nats micro info $SERVICE_NAME"
nats micro info "$SERVICE_NAME" --server="$NATS_URL" || true
echo

echo "4. Getting service stats..."
echo "   Running: nats micro stats $SERVICE_NAME"
nats micro stats "$SERVICE_NAME" --server="$NATS_URL" || true
echo

echo "5. Pinging service..."
echo "   Running: nats micro ping $SERVICE_NAME"
nats micro ping "$SERVICE_NAME" --server="$NATS_URL" --count 1 || true
echo

echo "6. Testing custom endpoints..."
echo

echo "   6a. Status endpoint:"
echo "   Subject: cluster_$CLUSTER_ID.status.node-1"
nats request "cluster_$CLUSTER_ID.status.node-1" '' --server="$NATS_URL" --timeout 5s || true
echo

echo "   6b. Ping endpoint:"
echo "   Subject: cluster_$CLUSTER_ID.ping.node-1"
nats request "cluster_$CLUSTER_ID.ping.node-1" '' --server="$NATS_URL" --timeout 5s || true
echo

echo "   6c. Stepdown endpoint (will trigger stepdown):"
echo "   Subject: cluster_$CLUSTER_ID.control.node-1.stepdown"
nats request "cluster_$CLUSTER_ID.control.node-1.stepdown" '' --server="$NATS_URL" --timeout 5s || true
echo

echo "7. Stopping node..."
kill $NODE_PID 2>/dev/null || true
wait $NODE_PID 2>/dev/null || true

echo
echo "=== Service Discovery Validation Complete ==="
