#!/bin/bash
# validate-node-presence.sh - Validates node presence handling in go-cluster
#
# This script runs node presence related tests and validates:
# - Duplicate node detection and rejection
# - Graceful rejoin after shutdown
# - Rejoin after stale entry expiry
# - Concurrent start with same ID
# - Multi-node cluster formation
#
# Usage: ./scripts/validate-node-presence.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -t, --timeout    Test timeout (default: 5m)
#   -h, --help       Show this help message

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Source common utilities
if [ -f "${SCRIPT_DIR}/common.sh" ]; then
    source "${SCRIPT_DIR}/common.sh"
else
    # Fallback definitions if common.sh not available
    RED='\033[0;31m'
    GREEN='\033[0;32m'
    YELLOW='\033[1;33m'
    NC='\033[0m'
    TESTS_PASSED=0
    TESTS_FAILED=0
    pass() { echo -e "${GREEN}✓ PASS${NC}: $1"; TESTS_PASSED=$((TESTS_PASSED + 1)); }
    fail() { echo -e "${RED}✗ FAIL${NC}: $1"; TESTS_FAILED=$((TESTS_FAILED + 1)); }
    warn() { echo -e "${YELLOW}⚠ WARN${NC}: $1"; }
fi

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
echo -e "${YELLOW}  go-cluster Node Presence Validation${NC}"
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

# Function to run a test and track results
run_test() {
    local test_name=$1
    local description=$2
    
    echo -n "  Testing: ${description}... "
    
    if go test ${TEST_FLAGS} -run "^${test_name}$" . > /tmp/test_output_$$.txt 2>&1; then
        echo -e "${GREEN}PASSED${NC}"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        if [ "$VERBOSE" = true ]; then
            cat /tmp/test_output_$$.txt
        fi
    else
        echo -e "${RED}FAILED${NC}"
        TESTS_FAILED=$((TESTS_FAILED + 1))
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

# Unit tests for membership node presence
echo -e "${YELLOW}Running Membership Unit Tests...${NC}"
echo ""

run_test "TestMembership_NodeAlreadyPresent" "Duplicate node detection"
run_test "TestMembership_RejoinAfterGracefulShutdown" "Rejoin after graceful shutdown"
run_test "TestMembership_RejoinAfterStaleEntry" "Rejoin after stale entry"
run_test "TestMembership_DifferentNodeIDsAllowed" "Different node IDs allowed"

echo ""
echo -e "${YELLOW}Running E2E Node Presence Tests...${NC}"
echo ""

run_test "TestE2E_NodePresence_DuplicateNodeRejected" "E2E: Duplicate node rejection"
run_test "TestE2E_NodePresence_GracefulRejoin" "E2E: Graceful rejoin"
run_test "TestE2E_NodePresence_ConcurrentSameID" "E2E: Concurrent start same ID"
run_test "TestE2E_NodePresence_MultiNodeCluster" "E2E: Multi-node cluster"
run_test "TestE2E_NodePresence_FailoverAndRejoin" "E2E: Failover and rejoin"

# Print summary
echo ""
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}  Results${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "  ${GREEN}Passed: ${TESTS_PASSED}${NC}"
echo -e "  ${RED}Failed: ${TESTS_FAILED}${NC}"

if [ $TESTS_FAILED -gt 0 ]; then
    echo ""
    echo -e "${RED}Node presence validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All node presence tests passed!${NC}"
    echo -e "${GREEN}Node presence validation SUCCESSFUL${NC}"
    exit 0
fi
