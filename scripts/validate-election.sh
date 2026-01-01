#!/bin/bash
# validate-election.sh - Validates election functionality in go-cluster
#
# This script runs election-related tests and validates:
# - Basic leader election
# - Step down functionality
# - Leadership failover
# - Split-brain prevention
# - Epoch tracking
# - Hook ordering
#
# Usage: ./scripts/validate-election.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -t, --timeout    Test timeout (default: 5m)
#   -h, --help       Show this help message

set -e

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
echo -e "${YELLOW}  go-cluster Election Validation${NC}"
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

echo -e "${YELLOW}Running Election Tests...${NC}"
echo ""

# Run individual election tests
run_test "TestElection_BasicElection" "Basic leader election"
run_test "TestElection_LeaderFailover" "Leader failover on node failure"
run_test "TestElection_StepDown" "Graceful leadership step-down"
run_test "TestElection_WaitForLeadership" "Wait for leadership API"
run_test "TestElection_WaitForLeadershipTimeout" "Wait for leadership timeout"
run_test "TestElection_OnLeaderChangeHook" "Leader change hook execution"
run_test "TestElection_EpochIncrement" "Epoch monotonic increment"
run_test "TestElection_SplitBrainPrevention" "Split-brain prevention (E2E)"
run_test "TestElection_ConcurrentCampaigns" "Concurrent election campaigns"

echo ""
echo -e "${YELLOW}Running Health Tests...${NC}"
echo ""

# Run health-related tests that affect election
run_test "TestHealth_BasicCheck" "Basic health check"
run_test "TestHealth_CircuitBreaker" "Circuit breaker functionality"
run_test "TestHealth_OnHealthChangeHook" "Health change hook"
run_test "TestHealth_HookTimeout" "Hook timeout handling"
run_test "TestHealth_HookError" "Hook error recovery"
run_test "TestHealth_GracefulShutdown" "Graceful shutdown"

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
    echo -e "${RED}Election validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All election tests passed!${NC}"
    echo -e "${GREEN}Election validation SUCCESSFUL${NC}"
    exit 0
fi
