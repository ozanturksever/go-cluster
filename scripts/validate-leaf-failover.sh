#!/bin/bash
# validate-leaf-failover.sh - Validates leaf node failover functionality in go-cluster
#
# This script runs leaf node-related tests and validates:
# - Leaf manager configuration (hub and leaf modes)
# - Partition detection and handling
# - Leaf tracking and quorum
# - Cross-platform communication
# - TLS configuration
# - Heartbeat handling
#
# Usage: ./scripts/validate-leaf-failover.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -t, --timeout    Test timeout (default: 5m)
#   -h, --help       Show this help message

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Default values
VERBOSE=false
RACE=false
TIMEOUT="5m"

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -r|--race)
            RACE=true
            shift
            ;;
        -t|--timeout)
            TIMEOUT="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "  -v, --verbose    Show verbose output"
            echo "  -r, --race       Run with race detector"
            echo "  -t, --timeout    Test timeout (default: 5m)"
            echo "  -h, --help       Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}  go-cluster Leaf Node Failover Validation${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""

# Build test flags
TEST_FLAGS="-timeout ${TIMEOUT}"
if [ "$VERBOSE" = true ]; then
    TEST_FLAGS="${TEST_FLAGS} -v"
fi
if [ "$RACE" = true ]; then
    TEST_FLAGS="${TEST_FLAGS} -race"
fi

# Track results
PASSED=0
FAILED=0
FAILED_TESTS=""

# Function to run a test and track results
run_test() {
    local test_name=$1
    local description=$2
    
    echo -n "  Testing: ${description}... "
    
    if go test ${TEST_FLAGS} -run "^${test_name}$" . > /tmp/test_output_$$.txt 2>&1; then
        echo -e "${GREEN}PASSED${NC}"
        PASSED=$((PASSED + 1))
        if [ "$VERBOSE" = true ]; then
            cat /tmp/test_output_$$.txt
        fi
    else
        echo -e "${RED}FAILED${NC}"
        FAILED=$((FAILED + 1))
        FAILED_TESTS="${FAILED_TESTS}\n  - ${test_name}"
        if [ "$VERBOSE" = true ]; then
            cat /tmp/test_output_$$.txt
        fi
    fi
    rm -f /tmp/test_output_$$.txt
}

# Validate code compiles
echo -e "${YELLOW}Checking code compilation...${NC}"
if go build ./... > /tmp/build_output_$$.txt 2>&1; then
    echo -e "  ${GREEN}✓${NC} Code compiles successfully"
else
    echo -e "  ${RED}✗${NC} Code compilation failed"
    cat /tmp/build_output_$$.txt
    rm -f /tmp/build_output_$$.txt
    exit 1
fi
rm -f /tmp/build_output_$$.txt
echo ""

echo -e "${YELLOW}Running Leaf Manager Unit Tests...${NC}"
echo ""

# Leaf Manager configuration tests
run_test "TestLeafManager_NewLeafManager" "New leaf manager creation"
run_test "TestLeafManager_ConfigureAsHub" "Configure as hub"
run_test "TestLeafManager_ConfigureAsLeaf" "Configure as leaf"
run_test "TestLeafManager_ConfigureAsLeaf_Defaults" "Leaf configuration defaults"

echo ""
echo -e "${YELLOW}Running Partition Handling Tests...${NC}"
echo ""

# Partition handling tests
run_test "TestLeafManager_PartitionState" "Partition state tracking"
run_test "TestPartitionStrategy" "Partition strategy modes"
run_test "TestPartitionEvent_Types" "Partition event types"

echo ""
echo -e "${YELLOW}Running Leaf Tracking Tests...${NC}"
echo ""

# Leaf tracking and quorum tests
run_test "TestLeafManager_LeafTracking" "Leaf tracking"
run_test "TestLeafManager_Quorum" "Quorum calculations"
run_test "TestLeafManager_Callbacks" "Callback registrations"

echo ""
echo -e "${YELLOW}Running Configuration Tests...${NC}"
echo ""

# Configuration tests
run_test "TestLeafManager_StartStop_Standalone" "Start/stop standalone mode"
run_test "TestTLSConfig" "TLS configuration loading"
run_test "TestSplitSubject" "Subject splitting"
run_test "TestLeafHeartbeat_Serialization" "Heartbeat serialization"
run_test "TestLeafInfo_Fields" "Leaf info fields"
run_test "TestLeafConnectionStatus_Values" "Connection status values"

echo ""
echo -e "${YELLOW}Running E2E Tests...${NC}"
echo ""

# E2E tests (these require NATS server)
run_test "TestLeafE2E_HubAndLeafConnection" "Hub and leaf connection (E2E)"
run_test "TestLeafE2E_LeafAnnouncement" "Leaf announcement (E2E)"
run_test "TestLeafE2E_MultipleLeaves" "Multiple leaves (E2E)"
run_test "TestLeafE2E_CrossPlatformRPC" "Cross-platform RPC (E2E)"
run_test "TestLeafE2E_CrossPlatformEvents" "Cross-platform events (E2E)"
run_test "TestLeafE2E_PartitionDetection" "Partition detection (E2E)"
run_test "TestLeafE2E_LeafDisconnection" "Leaf disconnection (E2E)"
run_test "TestLeafE2E_QuorumHandling" "Quorum handling (E2E)"
run_test "TestLeafE2E_CrossPlatformServiceDiscovery" "Cross-platform service discovery (E2E)"
run_test "TestLeafE2E_PingLatency" "Ping latency measurement (E2E)"
run_test "TestLeafE2E_PartitionStrategyModes" "Partition strategy modes (E2E)"

echo ""
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}  Results${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "  ${GREEN}Passed: ${PASSED}${NC}"
echo -e "  ${RED}Failed: ${FAILED}${NC}"

if [ $FAILED -gt 0 ]; then
    echo ""
    echo -e "${RED}Failed tests:${FAILED_TESTS}${NC}"
    echo ""
    echo -e "${RED}Leaf node failover validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All leaf node failover tests passed!${NC}"
    echo -e "${GREEN}Leaf node failover validation SUCCESSFUL${NC}"
    exit 0
fi
