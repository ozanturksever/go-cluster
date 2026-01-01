#!/bin/bash
# validate-health.sh - Validates health check circuit breaker in go-cluster
#
# This script runs health-related tests and validates:
# - Basic health checks
# - Circuit breaker functionality (opens after 3 failures)
# - OnHealthChange hooks
# - Failing check detection
# - Hook timeout and error handling
# - Graceful shutdown sequence
#
# Usage: ./scripts/validate-health.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -t, --timeout    Test timeout (default: 3m)
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
TIMEOUT="3m"

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
            echo "  -t, --timeout    Test timeout (default: 3m)"
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
echo -e "${YELLOW}  go-cluster Health Check Validation${NC}"
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

echo -e "${YELLOW}Running Health Check Tests...${NC}"
echo ""

# Run health check tests
run_test "TestHealth_BasicCheck" "Basic health check registration"
run_test "TestHealth_CircuitBreaker" "Circuit breaker (opens after failures)"
run_test "TestHealth_OnHealthChangeHook" "OnHealthChange hook execution"
run_test "TestHealth_FailingCheck" "Failing check detection"
run_test "TestHealth_HookTimeout" "Hook timeout handling"
run_test "TestHealth_HookError" "Hook error recovery"
run_test "TestHealth_GracefulShutdown" "Graceful shutdown sequence"

echo ""
echo -e "${YELLOW}Running Service Health Tests...${NC}"
echo ""

# Run service health tests
run_test "TestServiceRegistry_UpdateHealth" "Service health updates"
run_test "TestServiceRegistry_DiscoverHealthyOnly" "Discover healthy services only"

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
    echo -e "${RED}Health validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All health tests passed!${NC}"
    echo -e "${GREEN}Health validation SUCCESSFUL${NC}"
    exit 0
fi
