#!/bin/bash
# benchmark-failover.sh - Measure failover time
#
# This script benchmarks how quickly leadership transfers between nodes
# when the leader steps down.
#
# Usage:
#   ./scripts/benchmark-failover.sh [iterations]
#
# Prerequisites:
#   - NATS server running: nats-server -js
#   - Build CLI: go build -o go-cluster-cli ./examples/cli

set -e

ITERATIONS=${1:-5}
NATS_URL=${NATS_URL:-"nats://localhost:4222"}
CLUSTER_ID="benchmark-$(date +%s)"
CLI_BIN="./go-cluster-cli"
TMP_DIR=$(mktemp -d)

cleanup() {
    echo "Cleaning up..."
    jobs -p | xargs -r kill 2>/dev/null || true
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

echo "=== go-cluster Failover Benchmark ==="
echo "NATS URL: $NATS_URL"
echo "Cluster ID: $CLUSTER_ID"
echo "Iterations: $ITERATIONS"
echo

if [ ! -f "$CLI_BIN" ]; then
    echo "Building CLI..."
    go build -o "$CLI_BIN" ./examples/cli
fi

# Create config files with fast timing
for i in 1 2; do
    cat > "$TMP_DIR/node${i}.json" << EOF
{
  "clusterId": "$CLUSTER_ID",
  "nodeId": "node-${i}",
  "nats": {
    "servers": ["$NATS_URL"]
  },
  "election": {
    "leaseTtlMs": 3000,
    "heartbeatIntervalMs": 1000
  }
}
EOF
done

echo "Starting nodes..."
$CLI_BIN daemon --config "$TMP_DIR/node1.json" --log-level warn &
NODE1_PID=$!
$CLI_BIN daemon --config "$TMP_DIR/node2.json" --log-level warn &
NODE2_PID=$!

echo "Waiting for cluster to stabilize..."
sleep 5

# Function to get current leader
get_leader() {
    $CLI_BIN status --config "$TMP_DIR/node1.json" --json 2>/dev/null | jq -r '.leader' || echo ""
}

# Function to get milliseconds since epoch
get_ms() {
    if [[ "$OSTYPE" == "darwin"* ]]; then
        # macOS
        python3 -c 'import time; print(int(time.time() * 1000))'
    else
        # Linux
        date +%s%3N
    fi
}

# Wait for a leader
echo "Waiting for initial leader election..."
while [ -z "$(get_leader)" ]; do
    sleep 0.5
done

echo "Initial leader: $(get_leader)"
echo

# Benchmark results
declare -a FAILOVER_TIMES

echo "Running benchmark ($ITERATIONS iterations)..."
echo

for i in $(seq 1 $ITERATIONS); do
    CURRENT_LEADER=$(get_leader)
    
    if [ -z "$CURRENT_LEADER" ]; then
        echo "Iteration $i: No leader found, skipping"
        continue
    fi
    
    echo -n "Iteration $i: $CURRENT_LEADER -> "
    
    # Record start time
    START_MS=$(get_ms)
    
    # Trigger stepdown
    $CLI_BIN demote --config "$TMP_DIR/${CURRENT_LEADER}.json" 2>/dev/null || true
    
    # Wait for new leader
    NEW_LEADER=""
    while [ -z "$NEW_LEADER" ] || [ "$NEW_LEADER" = "$CURRENT_LEADER" ]; do
        NEW_LEADER=$(get_leader)
        sleep 0.05
    done
    
    # Record end time
    END_MS=$(get_ms)
    
    FAILOVER_TIME=$((END_MS - START_MS))
    FAILOVER_TIMES+=($FAILOVER_TIME)
    
    echo "$NEW_LEADER (${FAILOVER_TIME}ms)"
    
    # Let the cluster stabilize before next iteration
    sleep 2
done

echo
echo "=== Results ==="

# Calculate statistics
if [ ${#FAILOVER_TIMES[@]} -gt 0 ]; then
    SUM=0
    MIN=${FAILOVER_TIMES[0]}
    MAX=${FAILOVER_TIMES[0]}
    
    for t in "${FAILOVER_TIMES[@]}"; do
        SUM=$((SUM + t))
        if [ $t -lt $MIN ]; then MIN=$t; fi
        if [ $t -gt $MAX ]; then MAX=$t; fi
    done
    
    AVG=$((SUM / ${#FAILOVER_TIMES[@]}))
    
    echo "Samples:  ${#FAILOVER_TIMES[@]}"
    echo "Min:      ${MIN}ms"
    echo "Max:      ${MAX}ms"
    echo "Average:  ${AVG}ms"
    echo
    
    if [ $AVG -lt 500 ]; then
        echo "✓ Average failover time is under 500ms target!"
    else
        echo "⚠ Average failover time exceeds 500ms target"
    fi
else
    echo "No successful failover iterations"
fi

echo
echo "Stopping nodes..."
kill $NODE1_PID $NODE2_PID 2>/dev/null || true
wait

echo "Benchmark complete!"
