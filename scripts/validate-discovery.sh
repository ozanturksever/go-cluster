#!/bin/bash
# Validate service discovery functionality
# This script runs the service discovery tests to validate the implementation.

set -e

echo "=== Validating Service Discovery ==="
echo ""

# Colors for output
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m' # No Color

# Run service discovery unit tests
echo "Running service discovery tests..."
if go test -v -run TestService -count=1 ./... 2>&1; then
    echo -e "${GREEN}✓ Service discovery unit tests passed${NC}"
else
    echo -e "${RED}✗ Service discovery unit tests failed${NC}"
    exit 1
fi

echo ""

# Run service config validation tests
echo "Running service config validation tests..."
if go test -v -run TestServiceConfig -count=1 ./... 2>&1; then
    echo -e "${GREEN}✓ Service config tests passed${NC}"
else
    echo -e "${RED}✗ Service config tests failed${NC}"
    exit 1
fi

echo ""

# Run multi-node service discovery test
echo "Running multi-node service discovery tests..."
if go test -v -run TestServiceRegistry_MultipleNodes -count=1 ./... 2>&1; then
    echo -e "${GREEN}✓ Multi-node service discovery tests passed${NC}"
else
    echo -e "${RED}✗ Multi-node service discovery tests failed${NC}"
    exit 1
fi

echo ""
echo -e "${GREEN}=== All service discovery validations passed ===${NC}"
