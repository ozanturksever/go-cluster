#!/bin/bash
# validate-crossapp.sh - Validates cross-app communication in go-cluster
#
# This script runs cross-app communication tests and validates:
# - Multi-service order processing (Order→Inventory→Payment)
# - Platform lock coordination between apps
# - Multi-node cross-app communication
# - Event-driven saga patterns
#
# Usage: ./scripts/validate-crossapp.sh [options]
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
echo -e "${YELLOW}  go-cluster Cross-App Validation${NC}"
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

echo -e "${YELLOW}Running Cross-App Communication Tests...${NC}"
echo ""

# Run cross-app E2E tests
run_test "TestE2E_CrossAppCommunicationFlow" "Order→Inventory→Payment service flow"
run_test "TestE2E_CrossAppLockCoordination" "Platform lock coordination between apps"
run_test "TestE2E_MultiNodeCrossAppCommunication" "Multi-node cross-app communication"
run_test "TestE2E_EventDrivenSaga" "Event-driven saga pattern"

echo ""
echo -e "${YELLOW}Running API Client Tests...${NC}"
echo ""

# Run API client tests
run_test "TestAPIClient_CallToLeader" "API call routing to leader"
run_test "TestAPIClient_CallJSON" "JSON request/response handling"
run_test "TestAPIClient_RouteToAny" "Load-balanced routing (ToAny)"
run_test "TestAPIClient_WithTimeout" "Request timeout handling"
run_test "TestAPIClient_Broadcast" "Broadcast to all instances"
run_test "TestAPIClient_CircuitBreaker" "Circuit breaker for failing apps"

echo ""
echo -e "${YELLOW}Running Platform Events Tests...${NC}"
echo ""

# Run platform events tests
run_test "TestPlatformEvents_PublishSubscribe" "Platform-wide publish/subscribe"
run_test "TestPlatformEvents_SubscribeChannel" "Channel-based subscription"
run_test "TestPlatformEvents_PublishFrom" "Publish with source app"
run_test "TestPlatformEvents_SubscribeFromApp" "Subscribe filtered by source app"
run_test "TestPlatformEvents_MultipleSubscribers" "Multiple subscribers"

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
    echo -e "${RED}Cross-app validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All cross-app tests passed!${NC}"
    echo -e "${GREEN}Cross-app validation SUCCESSFUL${NC}"
    exit 0
fi
