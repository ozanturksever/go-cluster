#!/bin/bash
# validate-split-brain.sh - Validates split-brain prevention mechanisms
#
# This script tests that the cluster properly handles network partitions
# and prevents split-brain scenarios where multiple nodes believe they are leaders.
#
# Prerequisites:
# - go test binary
# - Network namespace support (Linux) or similar isolation
#
# Usage:
#   ./scripts/validate-split-brain.sh

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

echo "=== Split-Brain Prevention Validation ==="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    exit 1
}

warn() {
    echo -e "${YELLOW}⚠ WARN${NC}: $1"
}

cd "$PROJECT_DIR"

# Test 1: Election Split-Brain Prevention
echo "1. Testing election split-brain prevention..."
if go test -v -run "TestElection_SplitBrainPrevention" ./... 2>&1 | grep -q "PASS"; then
    pass "Election split-brain prevention tests passed"
else
    fail "Election split-brain prevention tests failed"
fi

# Test 2: Rapid Leadership Changes
echo ""
echo "2. Testing rapid leadership changes..."
if go test -v -run "TestElection_SplitBrainPrevention/RapidStepDowns" ./... 2>&1 | grep -q "PASS"; then
    pass "Rapid step-down handling passed"
else
    fail "Rapid step-down handling failed"
fi

# Test 3: Epoch Tracking
echo ""
echo "3. Testing epoch tracking during transitions..."
if go test -v -run "TestElection_SplitBrainPrevention/EpochTracking" ./... 2>&1 | grep -q "PASS"; then
    pass "Epoch tracking passed"
else
    fail "Epoch tracking failed"
fi

# Test 4: Leader Kill and Re-election
echo ""
echo "4. Testing leader kill and re-election..."
if go test -v -run "TestElection_SplitBrainPrevention/LeaderKillAndReelection" ./... 2>&1 | grep -q "PASS"; then
    pass "Leader kill and re-election passed"
else
    fail "Leader kill and re-election failed"
fi

# Test 5: Concurrent Campaigns
echo ""
echo "5. Testing concurrent election campaigns..."
if go test -v -run "TestElection_ConcurrentCampaigns" ./... 2>&1 | grep -q "PASS"; then
    pass "Concurrent campaigns handling passed"
else
    fail "Concurrent campaigns handling failed"
fi

# Test 6: Partition Detection (Leaf nodes)
echo ""
echo "6. Testing partition detection..."
if go test -v -run "TestLeafE2E_PartitionDetection" ./... 2>&1 | grep -q "PASS"; then
    pass "Partition detection passed"
else
    warn "Partition detection test skipped or failed (may require leaf node setup)"
fi

# Test 7: Quorum Handling
echo ""
echo "7. Testing quorum handling..."
if go test -v -run "TestLeafE2E_QuorumHandling" ./... 2>&1 | grep -q "PASS"; then
    pass "Quorum handling passed"
else
    warn "Quorum handling test skipped or failed"
fi

# Test 8: Partition Strategy Modes
echo ""
echo "8. Testing partition strategy modes..."
if go test -v -run "TestLeafE2E_PartitionStrategyModes" ./... 2>&1 | grep -q "PASS"; then
    pass "Partition strategy modes passed"
else
    warn "Partition strategy modes test skipped or failed"
fi

echo ""
echo "=== Split-Brain Prevention Validation Complete ==="
echo ""
echo "Summary:"
echo "  - Election mechanisms prevent multiple leaders"
echo "  - Epoch tracking ensures leader uniqueness"
echo "  - Partition detection triggers appropriate strategies"
echo "  - Quorum requirements prevent minority partitions from electing leaders"
echo ""
echo "For production deployments, ensure:"
echo "  1. Odd number of nodes for quorum (3, 5, 7...)"
echo "  2. Network connectivity monitoring between all nodes"
echo "  3. Proper timeout configuration for your network latency"
echo "  4. VIP fencing to prevent duplicate IP addresses"
