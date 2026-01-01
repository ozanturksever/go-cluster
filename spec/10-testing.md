# Testing Strategy

## Philosophy

1. **TDD** - Tests first, implementation second
2. **Three layers** - Unit → E2E → Validation scripts
3. **Real dependencies** - Use testcontainers, not mocks for NATS
4. **CI/CD ready** - All tests run in Docker

---

## Test Structure

```
go-cluster/
├── *_test.go              # Unit tests (same package)
├── *_e2e_test.go          # E2E tests (require Docker)
├── testutil/
│   ├── nats.go            # NATS testcontainer helpers
│   ├── cluster.go         # Multi-node test cluster
│   ├── db.go              # SQLite test helpers
│   └── assert.go          # Custom assertions
├── scripts/
│   ├── validate-election.sh
│   ├── validate-failover.sh
│   ├── validate-replication.sh
│   ├── validate-split-brain.sh
│   └── benchmark-failover.sh
└── docker/
    ├── docker-compose.test.yml
    ├── Dockerfile.test
    └── nats-cluster/
```

---

## Unit Tests

Fast, isolated, no external dependencies.

```go
func TestElection_AcquireLeadership(t *testing.T) {
    ns := natstest.RunServer()
    defer ns.Shutdown()
    
    e := NewElection(ElectionConfig{
        ClusterID: "test",
        NodeID:    "node-1",
        NATSURLs:  []string{ns.ClientURL()},
    })
    
    ctx := context.Background()
    require.NoError(t, e.Start(ctx))
    defer e.Stop()
    
    assert.Eventually(t, func() bool {
        return e.IsLeader()
    }, 5*time.Second, 100*time.Millisecond)
}

func TestElection_Contention(t *testing.T) {
    ns := natstest.RunServer()
    defer ns.Shutdown()
    
    var nodes []*Election
    for i := 0; i < 3; i++ {
        e := NewElection(ElectionConfig{
            ClusterID: "test",
            NodeID:    fmt.Sprintf("node-%d", i),
            NATSURLs:  []string{ns.ClientURL()},
        })
        require.NoError(t, e.Start(context.Background()))
        nodes = append(nodes, e)
    }
    defer func() {
        for _, n := range nodes {
            n.Stop()
        }
    }()
    
    // Exactly one leader
    assert.Eventually(t, func() bool {
        leaders := 0
        for _, n := range nodes {
            if n.IsLeader() {
                leaders++
            }
        }
        return leaders == 1
    }, 5*time.Second, 100*time.Millisecond)
}
```

**Run:** `go test -short ./...`

---

## E2E Tests

Full integration with real NATS via testcontainers.

```go
//go:build e2e

func TestE2E_LeaderFailover(t *testing.T) {
    nats := testutil.StartNATSCluster(t, 3)
    defer nats.Stop()
    
    cluster := testutil.StartCluster(t, testutil.ClusterConfig{
        Nodes:    3,
        NATSURLs: nats.URLs(),
    })
    defer cluster.Stop()
    
    leader := cluster.WaitForLeader(t, 10*time.Second)
    require.NotEmpty(t, leader)
    
    cluster.Kill(leader)
    
    newLeader := cluster.WaitForLeader(t, 15*time.Second)
    require.NotEmpty(t, newLeader)
    require.NotEqual(t, leader, newLeader)
    
    assert.Greater(t, cluster.Node(newLeader).Epoch(), uint64(1))
}

func TestE2E_WALReplication(t *testing.T) {
    nats := testutil.StartNATS(t)
    defer nats.Stop()
    
    cluster := testutil.StartCluster(t, testutil.ClusterConfig{
        Nodes:  2,
        WithDB: true,
    })
    defer cluster.Stop()
    
    leader := cluster.WaitForLeader(t, 10*time.Second)
    cluster.Node(leader).Exec("INSERT INTO test VALUES (1, 'hello')")
    
    time.Sleep(2 * time.Second)
    
    cluster.Kill(leader)
    newLeader := cluster.WaitForLeader(t, 15*time.Second)
    
    row := cluster.Node(newLeader).Query("SELECT value FROM test WHERE id = 1")
    assert.Equal(t, "hello", row)
}

func TestE2E_SplitBrain(t *testing.T) {
    nats := testutil.StartNATSCluster(t, 3)
    cluster := testutil.StartCluster(t, testutil.ClusterConfig{
        Nodes:    3,
        NATSURLs: nats.URLs(),
    })
    defer cluster.Stop()
    defer nats.Stop()
    
    leader := cluster.WaitForLeader(t, 10*time.Second)
    
    nats.Partition(leader)
    
    assert.Eventually(t, func() bool {
        return !cluster.Node(leader).IsLeader()
    }, 15*time.Second, 500*time.Millisecond)
    
    newLeader := cluster.WaitForLeader(t, 15*time.Second)
    require.NotEqual(t, leader, newLeader)
    
    nats.Heal()
    
    assert.Eventually(t, func() bool {
        return cluster.Node(leader).Leader() == newLeader
    }, 10*time.Second, 500*time.Millisecond)
    
    assert.Equal(t, 1, cluster.LeaderCount())
}
```

**Run:** `go test -tags=e2e ./...`

---

## Testutil Package

```go
package testutil

func StartNATS(t *testing.T) *NATSCluster { ... }
func StartNATSCluster(t *testing.T, nodes int) *NATSCluster { ... }

func (c *NATSCluster) URLs() []string { ... }
func (c *NATSCluster) Partition(nodeID string) { ... }
func (c *NATSCluster) Heal() { ... }
func (c *NATSCluster) Stop() { ... }

func StartCluster(t *testing.T, cfg ClusterConfig) *TestCluster { ... }
func (c *TestCluster) WaitForLeader(t *testing.T, timeout time.Duration) string { ... }
func (c *TestCluster) Kill(nodeID string) { ... }
func (c *TestCluster) Restart(nodeID string) { ... }
func (c *TestCluster) Node(nodeID string) *TestNode { ... }
func (c *TestCluster) LeaderCount() int { ... }
```

---

## Validation Scripts

```bash
#!/bin/bash
# scripts/validate-election.sh

set -euo pipefail
source "$(dirname "$0")/common.sh"

log "Starting NATS..."
start_nats

log "Starting 3-node cluster..."
start_node node-1 &
start_node node-2 &
start_node node-3 &
wait_for_nodes 3

log "Waiting for leader election..."
LEADER=$(wait_for_leader 10)
assert_not_empty "$LEADER" "No leader elected"

log "Verifying single leader..."
LEADER_COUNT=$(count_leaders)
assert_eq "$LEADER_COUNT" "1" "Expected 1 leader"

log "Killing leader ($LEADER)..."
kill_node "$LEADER"

log "Waiting for new leader..."
NEW_LEADER=$(wait_for_leader 15)
assert_neq "$NEW_LEADER" "$LEADER" "Same leader re-elected"

log "✓ Election validation passed"
cleanup
```

---

## Makefile

```makefile
test-unit:
	go test -short -race ./...

test-e2e:
	go test -tags=e2e -race -timeout 10m ./...

test-all: test-unit test-e2e

validate: validate-election validate-failover validate-replication validate-split-brain

validate-election:
	./scripts/validate-election.sh

bench:
	./scripts/benchmark-failover.sh 10

test-docker:
	docker-compose -f docker/docker-compose.test.yml up --build --abort-on-container-exit

ci: lint test-unit test-docker validate
```

---

## Test Conventions

1. **Naming**: `*_test.go` for unit, `*_e2e_test.go` for E2E
2. **Build tags**: E2E tests use `//go:build e2e`
3. **Timeouts**: Unit tests < 1s, E2E tests < 30s each
4. **Cleanup**: Always use `t.Cleanup()` or `defer`
5. **Parallel**: Use `t.Parallel()` where safe
6. **Assertions**: Use `testify/assert` and `testify/require`
