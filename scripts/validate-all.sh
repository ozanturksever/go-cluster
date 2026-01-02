#!/bin/bash
# validate-all.sh - Run all go-cluster validation scripts
#
# This master script runs all validation scripts in sequence and provides
# a comprehensive summary of all validation results.
#
# Usage: ./scripts/validate-all.sh [options]
#   -v, --verbose    Show verbose output
#   -r, --race       Run with race detector
#   -q, --quick      Run quick validation (subset of tests)
#   -h, --help       Show this help message

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Default values
VERBOSE=""
RACE=""
QUICK=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--verbose)
            VERBOSE="-v"
            shift
            ;;
        -r|--race)
            RACE="-r"
            shift
            ;;
        -q|--quick)
            QUICK=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "  -v, --verbose    Show verbose output"
            echo "  -r, --race       Run with race detector"
            echo "  -q, --quick      Run quick validation (subset of tests)"
            echo "  -h, --help       Show this help message"
            exit 0
            ;;
        *)
            echo "Unknown option: $1"
            exit 1
            ;;
    esac
done

echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                                                            ║${NC}"
echo -e "${BLUE}║          go-cluster Full Validation Suite                  ║${NC}"
echo -e "${BLUE}║                                                            ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Track overall results
TOTAL_PASSED=0
TOTAL_FAILED=0
FAILED_VALIDATIONS=""

START_TIME=$(date +%s)

# Function to run a validation script
run_validation() {
    local script_name=$1
    local description=$2
    
    echo ""
    echo -e "${BLUE}────────────────────────────────────────────────────────────${NC}"
    echo -e "${YELLOW}Running: ${description}${NC}"
    echo -e "${BLUE}────────────────────────────────────────────────────────────${NC}"
    
    if "${SCRIPT_DIR}/${script_name}" ${VERBOSE} ${RACE}; then
        TOTAL_PASSED=$((TOTAL_PASSED + 1))
        return 0
    else
        TOTAL_FAILED=$((TOTAL_FAILED + 1))
        FAILED_VALIDATIONS="${FAILED_VALIDATIONS}\n  - ${description}"
        return 1
    fi
}

# Define validation scripts
if [ "$QUICK" = true ]; then
    echo -e "${YELLOW}Running in QUICK mode (subset of validations)${NC}"
    VALIDATIONS=(
        "validate-election.sh:Election & Leadership"
        "validate-health.sh:Health Checks"
        "validate-locks.sh:Distributed Locks"
        "validate-vip.sh:VIP Failover"
    )
else
    VALIDATIONS=(
        "validate-election.sh:Election & Leadership"
        "validate-node-presence.sh:Node Presence & Duplicate Detection"
        "validate-replication.sh:WAL Replication"
        "validate-discovery.sh:Service Discovery"
        "validate-migration.sh:Dynamic Placement & Migration"
        "validate-leaf-failover.sh:Leaf Node Failover"
        "validate-split-brain.sh:Split-Brain Prevention"
        "validate-crossapp.sh:Cross-App Communication"
        "validate-backend.sh:Backend Failover"
        "validate-ring.sh:Ring Pattern"
        "validate-vip.sh:VIP Failover"
        "validate-health.sh:Health Checks"
        "validate-locks.sh:Distributed Locks"
    )
fi

# Run all validations
for validation in "${VALIDATIONS[@]}"; do
    script="${validation%%:*}"
    description="${validation##*:}"
    
    # Check if script exists
    if [ ! -f "${SCRIPT_DIR}/${script}" ]; then
        echo -e "${YELLOW}Skipping ${script} (not found)${NC}"
        continue
    fi
    
    # Run validation (continue even if it fails)
    run_validation "${script}" "${description}" || true
done

END_TIME=$(date +%s)
DURATION=$((END_TIME - START_TIME))

echo ""
echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                    VALIDATION SUMMARY                      ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  Duration: ${DURATION} seconds"
echo ""
echo -e "  ${GREEN}Passed: ${TOTAL_PASSED} validation suites${NC}"
echo -e "  ${RED}Failed: ${TOTAL_FAILED} validation suites${NC}"

if [ $TOTAL_FAILED -gt 0 ]; then
    echo ""
    echo -e "${RED}Failed validations:${FAILED_VALIDATIONS}${NC}"
    echo ""
    echo -e "${RED}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${RED}║              OVERALL VALIDATION: FAILED                    ║${NC}"
    echo -e "${RED}╚════════════════════════════════════════════════════════════╝${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}╔════════════════════════════════════════════════════════════╗${NC}"
    echo -e "${GREEN}║              OVERALL VALIDATION: PASSED                    ║${NC}"
    echo -e "${GREEN}╚════════════════════════════════════════════════════════════╝${NC}"
    exit 0
fi
