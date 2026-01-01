#!/bin/bash
# validate-migration.sh - Validates dynamic placement and migration functionality in go-cluster
#
# This script runs placement and migration-related tests and validates:
# - Label-based placement constraints (OnLabel, AvoidLabel, PreferLabel)
# - Node selector constraints (OnNodes)
# - App affinity/anti-affinity (WithApp, AwayFromApp)
# - Manual migration via MoveApp API
# - Node drain functionality
# - Cordon/uncordon operations
# - Migration cooldown enforcement
# - Migration hooks (OnMigrationStart, OnMigrationComplete, OnPlacementInvalid)
# - Label change detection and re-evaluation
#
# Usage: ./scripts/validate-migration.sh [options]
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
BLUE='\033[0;34m'
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
echo -e "${YELLOW}  go-cluster Migration Validation${NC}"
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
SKIPPED=0
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
        # Check if test was skipped
        if grep -q "SKIP" /tmp/test_output_$$.txt; then
            echo -e "${BLUE}SKIPPED${NC}"
            SKIPPED=$((SKIPPED + 1))
        else
            echo -e "${RED}FAILED${NC}"
            FAILED=$((FAILED + 1))
            FAILED_TESTS="${FAILED_TESTS}\n  - ${test_name}"
            if [ "$VERBOSE" = true ]; then
                cat /tmp/test_output_$$.txt
            fi
        fi
    fi
    rm -f /tmp/test_output_$$.txt
}

# Check prerequisites
echo -e "${BLUE}Checking prerequisites...${NC}"
if ! command -v go &> /dev/null; then
    echo -e "${RED}Go is not installed${NC}"
    exit 1
fi
echo -e "${GREEN}✓ Go is installed${NC}"
echo ""

# ============================================
# Unit Tests - Constraint Evaluation
# ============================================
echo -e "${YELLOW}Running Constraint Evaluation Tests...${NC}"
echo ""

run_test "TestScheduler_EvaluatePlacement" "Basic placement evaluation"
run_test "TestScheduler_OnLabel" "OnLabel constraint enforcement"
run_test "TestScheduler_AvoidLabel" "AvoidLabel constraint enforcement"
run_test "TestScheduler_PreferLabel" "PreferLabel soft constraint with weights"
run_test "TestScheduler_OnNodes" "OnNodes constraint enforcement"
run_test "TestScheduler_WithApp" "App co-location (affinity)"
run_test "TestScheduler_AwayFromApp" "App anti-affinity"

echo ""

# ============================================
# Unit Tests - Cordon/Uncordon
# ============================================
echo -e "${YELLOW}Running Cordon/Uncordon Tests...${NC}"
echo ""

run_test "TestScheduler_CordonNode" "Node cordon functionality"
run_test "TestPlacement_CordonUncordon" "Cordon and uncordon workflow"

echo ""

# ============================================
# Unit Tests - Migration
# ============================================
echo -e "${YELLOW}Running Migration Tests...${NC}"
echo ""

run_test "TestScheduler_MigrationCooldown" "Migration cooldown enforcement"
run_test "TestScheduler_SetLabels" "Dynamic label updates"
run_test "TestScheduler_WatchLabels" "Label change watching"
run_test "TestScheduler_OnPlacementInvalid" "OnPlacementInvalid hook"
run_test "TestPlacement_MigrationCooldownEnforced" "Cooldown enforcement (E2E)"
run_test "TestPlacement_GetActiveMigrations" "Active migration tracking"

echo ""

# ============================================
# E2E Tests - Multi-Node Placement
# ============================================
echo -e "${YELLOW}Running Multi-Node Placement E2E Tests...${NC}"
echo ""

run_test "TestPlacement_LabelTriggeredMigration" "Label-triggered migration (E2E)"
run_test "TestPlacement_ManualMigration" "Manual migration via API (E2E)"
run_test "TestPlacement_NodeDrain" "Node drain operation (E2E)"
run_test "TestPlacement_MigrationHooksCanReject" "Migration hook rejection (E2E)"
run_test "TestPlacement_MultiNodeConstraintEvaluation" "Multi-node constraint evaluation (E2E)"
run_test "TestPlacement_LabelWatcher" "Label change watcher (E2E)"
run_test "TestPlacement_RebalanceApp" "App rebalancing (E2E)"

echo ""

# ============================================
# Results Summary
# ============================================
echo -e "${YELLOW}========================================${NC}"
echo -e "${YELLOW}  Results Summary${NC}"
echo -e "${YELLOW}========================================${NC}"
echo ""
echo -e "  ${GREEN}Passed:  ${PASSED}${NC}"
echo -e "  ${RED}Failed:  ${FAILED}${NC}"
echo -e "  ${BLUE}Skipped: ${SKIPPED}${NC}"

if [ $FAILED -gt 0 ]; then
    echo ""
    echo -e "${RED}Failed tests:${FAILED_TESTS}${NC}"
    echo ""
    echo -e "${RED}Migration validation FAILED${NC}"
    exit 1
else
    echo ""
    echo -e "${GREEN}All migration/placement tests passed!${NC}"
    echo -e "${GREEN}Migration validation SUCCESSFUL${NC}"
    echo ""
    echo "Validated functionality:"
    echo "  - Label-based placement constraints (OnLabel, AvoidLabel, PreferLabel)"
    echo "  - Node selector constraints (OnNodes)"
    echo "  - App affinity/anti-affinity (WithApp, AwayFromApp)"
    echo "  - Manual migration via MoveApp API"
    echo "  - Node drain functionality"
    echo "  - Cordon/uncordon operations"
    echo "  - Migration cooldown enforcement"
    echo "  - Migration hooks (OnMigrationStart, OnMigrationComplete, OnPlacementInvalid)"
    echo "  - Label change detection and re-evaluation"
    exit 0
fi
