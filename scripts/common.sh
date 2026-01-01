#!/bin/bash
# common.sh - Shared utilities for go-cluster validation scripts
#
# Source this file in other scripts:
#   source "$(dirname "$0")/common.sh"

# Colors for output
export RED='\033[0;31m'
export GREEN='\033[0;32m'
export YELLOW='\033[1;33m'
export BLUE='\033[0;34m'
export NC='\033[0m' # No Color

# Default values
export NATS_URL=${NATS_URL:-"nats://localhost:4222"}
export TIMEOUT=${TIMEOUT:-"5m"}
export VERBOSE=${VERBOSE:-false}
export RACE=${RACE:-false}

# Track test results
export TESTS_PASSED=0
export TESTS_FAILED=0
export FAILED_TESTS=""

# Print colored output
log() {
    echo -e "${BLUE}[$(date +%H:%M:%S)]${NC} $1"
}

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[OK]${NC} $1"
}

# Test result functions
pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    FAILED_TESTS="${FAILED_TESTS}\n  - $1"
}

warn() {
    echo -e "${YELLOW}⚠ WARN${NC}: $1"
}

skip() {
    echo -e "${YELLOW}○ SKIP${NC}: $1"
}

# Build test flags based on options
build_test_flags() {
    local flags="-timeout ${TIMEOUT}"
    if [ "$VERBOSE" = true ]; then
        flags="${flags} -v"
    fi
    if [ "$RACE" = true ]; then
        flags="${flags} -race"
    fi
    echo "$flags"
}

# Run a single Go test and track results
run_test() {
    local test_name=$1
    local description=$2
    local test_flags=$(build_test_flags)
    
    echo -n "  Testing: ${description}... "
    
    local output_file=$(mktemp)
    if go test ${test_flags} -run "^${test_name}$" . > "$output_file" 2>&1; then
        echo -e "${GREEN}PASSED${NC}"
        TESTS_PASSED=$((TESTS_PASSED + 1))
        if [ "$VERBOSE" = true ]; then
            cat "$output_file"
        fi
    else
        echo -e "${RED}FAILED${NC}"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        FAILED_TESTS="${FAILED_TESTS}\n  - ${test_name}"
        if [ "$VERBOSE" = true ]; then
            cat "$output_file"
        fi
    fi
    rm -f "$output_file"
}

# Run a Go test pattern (multiple tests)
run_test_pattern() {
    local pattern=$1
    local description=$2
    local test_flags=$(build_test_flags)
    
    echo -n "  Testing: ${description}... "
    
    local output_file=$(mktemp)
    if go test ${test_flags} -run "${pattern}" . > "$output_file" 2>&1; then
        echo -e "${GREEN}PASSED${NC}"
        TESTS_PASSED=$((TESTS_PASSED + 1))
    else
        echo -e "${RED}FAILED${NC}"
        TESTS_FAILED=$((TESTS_FAILED + 1))
        FAILED_TESTS="${FAILED_TESTS}\n  - ${pattern}"
    fi
    
    if [ "$VERBOSE" = true ]; then
        cat "$output_file"
    fi
    rm -f "$output_file"
}

# Check if code compiles
check_build() {
    echo -n "Checking code compilation... "
    if go build ./... > /dev/null 2>&1; then
        echo -e "${GREEN}OK${NC}"
        return 0
    else
        echo -e "${RED}FAILED${NC}"
        return 1
    fi
}

# Check if NATS is available
check_nats() {
    if command -v nats &> /dev/null; then
        if timeout 5 nats server check --server="${NATS_URL}" &> /dev/null; then
            return 0
        fi
    fi
    return 1
}

# Start embedded NATS server for tests
start_nats() {
    log "Starting embedded NATS server..."
    # Tests use embedded NATS via testutil, this is just informational
    return 0
}

# Wait for a condition with timeout
wait_for() {
    local description=$1
    local command=$2
    local timeout_seconds=${3:-30}
    
    log "Waiting for ${description}..."
    local elapsed=0
    while [ $elapsed -lt $timeout_seconds ]; do
        if eval "$command"; then
            return 0
        fi
        sleep 1
        elapsed=$((elapsed + 1))
    done
    return 1
}

# Print test summary
print_summary() {
    echo ""
    echo -e "${BLUE}========================================${NC}"
    echo -e "${BLUE}  Results${NC}"
    echo -e "${BLUE}========================================${NC}"
    echo ""
    echo -e "  ${GREEN}Passed: ${TESTS_PASSED}${NC}"
    echo -e "  ${RED}Failed: ${TESTS_FAILED}${NC}"
    
    if [ $TESTS_FAILED -gt 0 ]; then
        echo ""
        echo -e "${RED}Failed tests:${FAILED_TESTS}${NC}"
        return 1
    fi
    return 0
}

# Parse common arguments
parse_common_args() {
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
                # Unknown option, let caller handle
                break
                ;;
        esac
    done
}

# Get project root directory
get_project_root() {
    local script_dir="$(cd "$(dirname "${BASH_SOURCE[1]}")" && pwd)"
    echo "$(dirname "$script_dir")"
}

# Assert functions for validation
assert_eq() {
    local actual="$1"
    local expected="$2"
    local message="$3"
    
    if [ "$actual" = "$expected" ]; then
        return 0
    else
        fail "${message}: expected '${expected}', got '${actual}'"
        return 1
    fi
}

assert_neq() {
    local actual="$1"
    local unexpected="$2"
    local message="$3"
    
    if [ "$actual" != "$unexpected" ]; then
        return 0
    else
        fail "${message}: expected value different from '${unexpected}'"
        return 1
    fi
}

assert_not_empty() {
    local value="$1"
    local message="$2"
    
    if [ -n "$value" ]; then
        return 0
    else
        fail "${message}: value is empty"
        return 1
    fi
}

assert_gt() {
    local actual="$1"
    local threshold="$2"
    local message="$3"
    
    if [ "$actual" -gt "$threshold" ]; then
        return 0
    else
        fail "${message}: expected > ${threshold}, got ${actual}"
        return 1
    fi
}
