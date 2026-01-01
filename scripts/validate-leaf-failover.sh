#!/bin/bash
# Validate leaf node failover functionality
# This script tests the leaf node connection management and partition handling

set -e

echo "=== Leaf Node Failover Validation ==="
echo ""

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Track test results
TESTS_PASSED=0
TESTS_FAILED=0

pass() {
    echo -e "${GREEN}✓ PASS${NC}: $1"
    ((TESTS_PASSED++))
}

fail() {
    echo -e "${RED}✗ FAIL${NC}: $1"
    ((TESTS_FAILED++))
}

warn() {
    echo -e "${YELLOW}⚠ WARN${NC}: $1"
}

# Check if NATS is available
check_nats() {
    echo "Checking NATS connectivity..."
    if command -v nats &> /dev/null; then
        if nats server check &> /dev/null; then
            pass "NATS server is available"
            return 0
        else
            warn "NATS server not responding"
            return 1
        fi
    else
        warn "NATS CLI not installed, skipping NATS checks"
        return 1
    fi
}

# Run leaf node unit tests
run_unit_tests() {
    echo ""
    echo "Running leaf node unit tests..."
    
    if go test -v -run "TestLeaf" -count=1 -timeout 60s ./... 2>&1 | tee /tmp/leaf-test-output.txt; then
        pass "Leaf node unit tests passed"
    else
        fail "Leaf node unit tests failed"
        cat /tmp/leaf-test-output.txt
    fi
}

# Run partition handling tests
run_partition_tests() {
    echo ""
    echo "Running partition handling tests..."
    
    if go test -v -run "TestPartition" -count=1 -timeout 60s ./... 2>&1 | tee /tmp/partition-test-output.txt; then
        pass "Partition handling tests passed"
    else
        fail "Partition handling tests failed"
    fi
}

# Validate leaf manager configuration
validate_leaf_config() {
    echo ""
    echo "Validating leaf configuration structures..."
    
    # Check that the code compiles
    if go build ./...; then
        pass "Code compiles successfully"
    else
        fail "Code compilation failed"
        return 1
    fi
}

# Test TLS configuration loading
test_tls_config() {
    echo ""
    echo "Testing TLS configuration..."
    
    if go test -v -run "TestTLSConfig" -count=1 -timeout 30s ./... 2>&1; then
        pass "TLS configuration tests passed"
    else
        fail "TLS configuration tests failed"
    fi
}

# Test quorum calculations
test_quorum() {
    echo ""
    echo "Testing quorum calculations..."
    
    if go test -v -run "TestLeafManager_Quorum" -count=1 -timeout 30s ./... 2>&1; then
        pass "Quorum calculation tests passed"
    else
        fail "Quorum calculation tests failed"
    fi
}

# Test callback registrations
test_callbacks() {
    echo ""
    echo "Testing callback registrations..."
    
    if go test -v -run "TestLeafManager_Callbacks" -count=1 -timeout 30s ./... 2>&1; then
        pass "Callback registration tests passed"
    else
        fail "Callback registration tests failed"
    fi
}

# Main execution
main() {
    echo "Starting leaf node validation at $(date)"
    echo ""
    
    # Validate configuration
    validate_leaf_config
    
    # Run tests
    run_unit_tests
    run_partition_tests
    test_tls_config
    test_quorum
    test_callbacks
    
    # Check NATS if available
    check_nats
    
    # Summary
    echo ""
    echo "=== Validation Summary ==="
    echo -e "Tests Passed: ${GREEN}${TESTS_PASSED}${NC}"
    echo -e "Tests Failed: ${RED}${TESTS_FAILED}${NC}"
    echo ""
    
    if [ $TESTS_FAILED -eq 0 ]; then
        echo -e "${GREEN}All leaf node validations passed!${NC}"
        exit 0
    else
        echo -e "${RED}Some validations failed. Please review the output above.${NC}"
        exit 1
    fi
}

main "$@"
