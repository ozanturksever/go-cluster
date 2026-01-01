#!/bin/bash
# validate-backend.sh - Validates backend failover in go-cluster
#
# This script runs backend-related tests and validates:
# - Backend coordinator start/stop lifecycle
# - Backend state transitions
# - Backend failover between nodes
# - Backend health monitoring
# - Process backend (SIGTERM, SIGKILL, process groups)
#
# Usage: ./scripts/validate-backend.sh [options]
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
echo -e "${YELLOW}  go-cluster Backend Validation${NC}"
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

echo -e "${YELLOW}Running Backend Coordinator Tests...${NC}"
echo ""

# Run backend coordinator tests
run_test "TestBackendCoordinator_StartStop" "Backend coordinator start/stop"
run_test "TestBackendCoordinator_StateTransitions" "Backend state transitions"
run_test "TestBackendCoordinator_FailoverBackendStartStop" "Backend failover start/stop"
run_test "TestBackendCoordinator_HealthMonitoring" "Backend health monitoring"

echo ""
echo -e "${YELLOW}Running Backend Options Tests...${NC}"
echo ""

# Run backend options tests
run_test "TestProcessBackend_Options" "Process backend options"
run_test "TestDockerBackend_Options" "Docker backend options"
run_test "TestSystemdBackend_Options" "Systemd backend options"
run_test "TestBackendHealth_Struct" "Backend health structure"
run_test "TestRestartPolicy_Default" "Default restart policy"

echo ""
echo -e "${YELLOW}Running Process Backend Tests...${NC}"
echo ""

# Run process backend tests
run_test "TestProcessBackend_StartStop" "Process start/stop"
run_test "TestProcessBackend_GracefulSIGTERM" "Graceful SIGTERM shutdown"
run_test "TestProcessBackend_SIGKILLAfterTimeout" "SIGKILL after timeout"
run_test "TestProcessBackend_ProcessGroupSignaling" "Process group signaling"
run_test "TestProcessBackend_OutputCapture" "Stdout/stderr capture"
run_test "TestProcessBackend_LogFile" "Log file output"
run_test "TestProcessBackend_Signal" "Signal sending"
run_test "TestProcessBackend_Restart" "Process restart"
run_test "TestProcessBackend_EnvironmentVariables" "Environment variables"
run_test "TestProcessBackend_WorkingDirectory" "Working directory"
run_test "TestProcessBackend_NoProcessGroup" "No process group mode"
run_test "TestProcessBackend_DefaultProcessGroup" "Default process group"
run_test "TestProcessBackend_QuickExit" "Quick exit handling"
run_test "TestProcessBackend_NonZeroExitCode" "Non-zero exit code"
run_test "TestProcessBackend_StopNotStarted" "Stop when not started"
run_test "TestProcessBackend_HealthNotStarted" "Health when not started"

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
    echo -e "${RED}Backend validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All backend tests passed!${NC}"
    echo -e "${GREEN}Backend validation SUCCESSFUL${NC}"
    exit 0
fi
