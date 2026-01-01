#!/bin/bash
# validate-locks.sh - Validates platform-wide locks in go-cluster
#
# This script runs lock-related tests and validates:
# - App-level lock acquire/release
# - Platform-wide lock coordination
# - Mutual exclusion between apps
# - TryAcquire, Do, Renew operations
# - Lock holder tracking
# - Concurrent access scenarios
#
# Usage: ./scripts/validate-locks.sh [options]
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
echo -e "${YELLOW}  go-cluster Locks Validation${NC}"
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

echo -e "${YELLOW}Running App-Level Lock Tests...${NC}"
echo ""

# Run app-level lock tests
run_test "TestLock_AcquireAndRelease" "Lock acquire and release"
run_test "TestLock_AcquireWithTTL" "Lock acquire with TTL"
run_test "TestLock_DoubleAcquire" "Double acquire handling"
run_test "TestLock_ReleaseNotHeld" "Release when not held"
run_test "TestLock_TryAcquire" "TryAcquire (non-blocking)"
run_test "TestLock_Do" "Do with automatic release"
run_test "TestLock_DoWithTTL" "Do with TTL"
run_test "TestLock_Contention" "Lock contention handling"
run_test "TestLock_Renew" "Lock renewal"
run_test "TestLock_RenewNotHeld" "Renew when not held"
run_test "TestLock_ConcurrentDo" "Concurrent Do operations"

echo ""
echo -e "${YELLOW}Running Platform-Wide Lock Tests...${NC}"
echo ""

# Run platform-wide lock tests
run_test "TestPlatformLock_AcquireRelease" "Platform lock acquire/release"
run_test "TestPlatformLock_MutualExclusion" "Mutual exclusion between apps"
run_test "TestPlatformLock_TryAcquire" "Platform TryAcquire"
run_test "TestPlatformLock_Do" "Platform Do operation"
run_test "TestPlatformLock_Renew" "Platform lock renewal"
run_test "TestPlatformLock_Holder" "Lock holder tracking"
run_test "TestPlatformLock_ConcurrentAccess" "Concurrent access scenarios"
run_test "TestPlatformLock_WaitAndAcquire" "Wait and acquire"

echo ""
echo -e "${YELLOW}Running Cross-App Lock Coordination...${NC}"
echo ""

# Run cross-app lock coordination test
run_test "TestE2E_CrossAppLockCoordination" "Cross-app lock coordination (E2E)"

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
    echo -e "${RED}Locks validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All lock tests passed!${NC}"
    echo -e "${GREEN}Locks validation SUCCESSFUL${NC}"
    exit 0
fi
