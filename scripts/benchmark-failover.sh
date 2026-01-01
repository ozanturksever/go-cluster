#!/bin/bash
# benchmark-failover.sh - Benchmarks failover timing for go-cluster
#
# This script measures failover performance including:
# - Election time (time to elect a new leader)
# - Failover time (time from leader failure to new leader ready)
# - Step-down time (time for graceful leadership transfer)
#
# Usage: ./scripts/benchmark-failover.sh [options]
#   -n, --iterations    Number of iterations (default: 10)
#   -v, --verbose       Show verbose output
#   -h, --help          Show this help message

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(dirname "$SCRIPT_DIR")"

cd "$PROJECT_ROOT"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# Default values
ITERATIONS=10
VERBOSE=false

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -n|--iterations)
            ITERATIONS="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "  -n, --iterations    Number of iterations (default: 10)"
            echo "  -v, --verbose       Show verbose output"
            echo "  -h, --help          Show this help message"
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
echo -e "${BLUE}║          go-cluster Failover Benchmark                     ║${NC}"
echo -e "${BLUE}║                                                            ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""
echo -e "  Iterations: ${ITERATIONS}"
echo ""

# Check if code compiles
echo -e "${YELLOW}Building test binary...${NC}"
if ! go test -c -o /tmp/go-cluster-bench . 2>/dev/null; then
    echo -e "${RED}Build failed${NC}"
    exit 1
fi
echo -e "  ${GREEN}✓${NC} Build successful"
echo ""

# Arrays to store timing results
declare -a ELECTION_TIMES
declare -a STEPDOWN_TIMES
declare -a FAILOVER_TIMES

# Run election benchmark
echo -e "${YELLOW}Running election benchmarks...${NC}"
echo ""

for i in $(seq 1 $ITERATIONS); do
    echo -n "  Iteration $i/$ITERATIONS: "
    
    # Run the benchmark test and capture timing
    START=$(date +%s%N)
    if timeout 60s go test -run "TestElection_LeaderElection" -count=1 . > /dev/null 2>&1; then
        END=$(date +%s%N)
        DURATION=$(( (END - START) / 1000000 )) # Convert to milliseconds
        ELECTION_TIMES+=($DURATION)
        echo -e "${GREEN}${DURATION}ms${NC}"
    else
        echo -e "${RED}failed${NC}"
    fi
done

echo ""
echo -e "${YELLOW}Running step-down benchmarks...${NC}"
echo ""

for i in $(seq 1 $ITERATIONS); do
    echo -n "  Iteration $i/$ITERATIONS: "
    
    START=$(date +%s%N)
    if timeout 60s go test -run "TestElection_StepDown" -count=1 . > /dev/null 2>&1; then
        END=$(date +%s%N)
        DURATION=$(( (END - START) / 1000000 ))
        STEPDOWN_TIMES+=($DURATION)
        echo -e "${GREEN}${DURATION}ms${NC}"
    else
        echo -e "${RED}failed${NC}"
    fi
done

echo ""
echo -e "${YELLOW}Running failover benchmarks...${NC}"
echo ""

for i in $(seq 1 $ITERATIONS); do
    echo -n "  Iteration $i/$ITERATIONS: "
    
    START=$(date +%s%N)
    if timeout 120s go test -run "TestElection_LeaderFailover" -count=1 . > /dev/null 2>&1; then
        END=$(date +%s%N)
        DURATION=$(( (END - START) / 1000000 ))
        FAILOVER_TIMES+=($DURATION)
        echo -e "${GREEN}${DURATION}ms${NC}"
    else
        echo -e "${RED}failed${NC}"
    fi
done

# Calculate statistics
calc_stats() {
    local arr=("$@")
    local len=${#arr[@]}
    
    if [ $len -eq 0 ]; then
        echo "0 0 0 0"
        return
    fi
    
    # Calculate min, max, sum
    local min=${arr[0]}
    local max=${arr[0]}
    local sum=0
    
    for val in "${arr[@]}"; do
        (( val < min )) && min=$val
        (( val > max )) && max=$val
        sum=$((sum + val))
    done
    
    local avg=$((sum / len))
    
    # Calculate p95 (sort and take 95th percentile)
    IFS=$'\n' sorted=($(sort -n <<<"${arr[*]}")); unset IFS
    local p95_idx=$(( (len * 95) / 100 ))
    [ $p95_idx -ge $len ] && p95_idx=$((len - 1))
    local p95=${sorted[$p95_idx]}
    
    echo "$min $max $avg $p95"
}

echo ""
echo -e "${BLUE}╔════════════════════════════════════════════════════════════╗${NC}"
echo -e "${BLUE}║                    BENCHMARK RESULTS                       ║${NC}"
echo -e "${BLUE}╚════════════════════════════════════════════════════════════╝${NC}"
echo ""

# Election stats
if [ ${#ELECTION_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${ELECTION_TIMES[@]}")
    echo -e "${YELLOW}Election Time:${NC}"
    echo -e "  Min:    ${GREEN}${min}ms${NC}"
    echo -e "  Max:    ${max}ms"
    echo -e "  Avg:    ${avg}ms"
    echo -e "  P95:    ${p95}ms"
    echo ""
fi

# Step-down stats
if [ ${#STEPDOWN_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${STEPDOWN_TIMES[@]}")
    echo -e "${YELLOW}Step-Down Time:${NC}"
    echo -e "  Min:    ${GREEN}${min}ms${NC}"
    echo -e "  Max:    ${max}ms"
    echo -e "  Avg:    ${avg}ms"
    echo -e "  P95:    ${p95}ms"
    echo ""
fi

# Failover stats
if [ ${#FAILOVER_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${FAILOVER_TIMES[@]}")
    echo -e "${YELLOW}Failover Time:${NC}"
    echo -e "  Min:    ${GREEN}${min}ms${NC}"
    echo -e "  Max:    ${max}ms"
    echo -e "  Avg:    ${avg}ms"
    echo -e "  P95:    ${p95}ms"
    echo ""
fi

# Summary table
echo -e "${BLUE}────────────────────────────────────────────────────────────${NC}"
echo -e "${YELLOW}Summary (all times in milliseconds):${NC}"
echo ""
printf "  %-15s %8s %8s %8s %8s\n" "Metric" "Min" "Avg" "P95" "Max"
printf "  %-15s %8s %8s %8s %8s\n" "───────────────" "────────" "────────" "────────" "────────"

if [ ${#ELECTION_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${ELECTION_TIMES[@]}")
    printf "  %-15s %8d %8d %8d %8d\n" "Election" "$min" "$avg" "$p95" "$max"
fi

if [ ${#STEPDOWN_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${STEPDOWN_TIMES[@]}")
    printf "  %-15s %8d %8d %8d %8d\n" "Step-Down" "$min" "$avg" "$p95" "$max"
fi

if [ ${#FAILOVER_TIMES[@]} -gt 0 ]; then
    read min max avg p95 <<< $(calc_stats "${FAILOVER_TIMES[@]}")
    printf "  %-15s %8d %8d %8d %8d\n" "Failover" "$min" "$avg" "$p95" "$max"
fi

echo ""
echo -e "${BLUE}────────────────────────────────────────────────────────────${NC}"
echo ""
echo -e "${GREEN}Benchmark complete!${NC}"

# Cleanup
rm -f /tmp/go-cluster-bench
