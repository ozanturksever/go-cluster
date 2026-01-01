#!/bin/bash
# validate-vip.sh - Validates VIP failover in go-cluster
#
# This script runs VIP-related tests and validates:
# - VIP manager configuration
# - VIP health checking
# - VIP acquire/release callbacks
#
# Note: Actual IP operations require root/NET_ADMIN capability.
# These tests validate the VIP manager logic without requiring privileges.
#
# Usage: ./scripts/validate-vip.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -t, --timeout    Test timeout (default: 2m)
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
TIMEOUT="2m"

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
            echo "  -t, --timeout    Test timeout (default: 2m)"
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
echo -e "${YELLOW}  go-cluster VIP Failover Validation${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "${YELLOW}Note: Full VIP operations require root/NET_ADMIN${NC}"
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
    
    if go test ${TEST_FLAGS} -run "^${test_name}$" ./vip/... > /tmp/test_output_$$.txt 2>&1; then
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

echo -e "${YELLOW}Running VIP Manager Configuration Tests...${NC}"
echo ""

# Run VIP configuration tests
run_test "TestNewManager_ValidConfig" "Valid VIP configuration"
run_test "TestNewManager_InvalidCIDR" "Invalid CIDR handling"
run_test "TestNewManager_EmptyInterface" "Empty interface handling"
run_test "TestNewManager_InvalidInterface" "Invalid interface handling"
run_test "TestDefaultConfig" "Default configuration values"

echo ""
echo -e "${YELLOW}Running VIP Manager State Tests...${NC}"
echo ""

# Run VIP state tests
run_test "TestManager_IsAcquired_Initial" "Initial acquisition state"
run_test "TestManager_Health" "Health status reporting"
run_test "TestManager_Callbacks" "Callback registration"
run_test "TestManager_StartStop" "Manager start/stop lifecycle"
run_test "TestManager_Verify_NotAcquired" "Verify when not acquired"

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
    echo -e "${RED}VIP validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All VIP tests passed!${NC}"
    echo -e "${GREEN}VIP validation SUCCESSFUL${NC}"
    echo ""
    echo -e "${YELLOW}To test actual VIP operations, run with root/NET_ADMIN:${NC}"
    echo "  sudo go test -v ./vip/..."
    exit 0
fi
