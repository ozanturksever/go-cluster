#!/bin/bash
# validate-election.sh - Manual cluster test for leader election
#
# This script starts multiple cluster nodes and validates leader election behavior.
# Requires: NATS server running with JetStream enabled, and go-cluster-cli built.
#
# Usage:
#   ./scripts/validate-election.sh
#
# Prerequisites:
#   - NATS server running: nats-server -js
#   - Build CLI: go build -o go-cluster-cli ./examples/cli

set -e

NATS_URL=${NATS_URL:-"nats://localhost:4222"}
CLUSTER_ID="election-test-$(date +%s)"
CLI_BIN="./go-cluster-cli"
TMP_DIR=$(mktemp -d)

cleanup() {
    echo "Cleaning up..."
    # Kill any background processes
    jobs -p | xargs -r kill 2>/dev/null || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "=== go-cluster Election Validation ==="
echo "NATS URL: $NATS_URL"
echo "Cluster ID: $CLUSTER_ID"
echo "Temp directory: $TMP_DIR"
echo

# Check prerequisites
if ! command -v nats &> /dev/null; then
    echo "Warning: 'nats' CLI not found. Some checks will be skipped."
fi

if [ ! -f "$CLI_BIN" ]; then
    echo "Building CLI..."
    go build -o "$CLI_BIN" ./examples/cli
fi

# Create config files for 3 nodes
for i in 1 2 3; do
    cat > "$TMP_DIR/node${i}.json" << EOF
{
  "clusterId": "$CLUSTER_ID",
  "nodeId": "node-${i}",
  "nats": {
    "servers": ["$NATS_URL"]
  },
  "election": {
    "leaseTtlMs": 5000,
    "heartbeatIntervalMs": 1000
  }
}
EOF
done

echo "1. Starting Node 1..."
$CLI_BIN daemon --config "$TMP_DIR/node1.json" --log-level info &
NODE1_PID=$!
sleep 3

echo "2. Checking Node 1 status..."
STATUS=$($CLI_BIN status --config "$TMP_DIR/node1.json" --json)
echo "$STATUS" | jq .

LEADER=$(echo "$STATUS" | jq -r '.leader')
if [ "$LEADER" = "node-1" ]; then
    echo "✓ Node 1 is the leader"
else
    echo "✗ Expected node-1 as leader, got: $LEADER"
    exit 1
fi

echo
echo "3. Starting Node 2..."
$CLI_BIN daemon --config "$TMP_DIR/node2.json" --log-level info &
NODE2_PID=$!
sleep 2

echo "4. Checking Node 2 status..."
STATUS2=$($CLI_BIN status --config "$TMP_DIR/node2.json" --json)
echo "$STATUS2" | jq .

ROLE2=$(echo "$STATUS2" | jq -r '.role')
if [ "$ROLE2" = "PASSIVE" ]; then
    echo "✓ Node 2 is passive (follower)"
else
    echo "✗ Expected PASSIVE role for node-2, got: $ROLE2"
    exit 1
fi

LEADER2=$(echo "$STATUS2" | jq -r '.leader')
if [ "$LEADER2" = "node-1" ]; then
    echo "✓ Node 2 sees node-1 as leader"
else
    echo "✗ Node 2 should see node-1 as leader, got: $LEADER2"
    exit 1
fi

echo
echo "5. Testing stepdown (demote Node 1)..."
$CLI_BIN demote --config "$TMP_DIR/node1.json"

echo "6. Waiting for failover..."
sleep 5

echo "7. Checking who is the new leader..."
STATUS_AFTER=$($CLI_BIN status --config "$TMP_DIR/node2.json" --json)
echo "$STATUS_AFTER" | jq .

NEW_LEADER=$(echo "$STATUS_AFTER" | jq -r '.leader')
if [ "$NEW_LEADER" = "node-2" ]; then
    echo "✓ Failover successful! Node 2 is now the leader"
else
    echo "✗ Expected node-2 as new leader, got: $NEW_LEADER"
    exit 1
fi

echo
echo "8. Stopping nodes..."
kill $NODE1_PID 2>/dev/null || true
kill $NODE2_PID 2>/dev/null || true
wait

echo
echo "=== Election Validation Complete ==="
echo "All tests passed! ✓"
