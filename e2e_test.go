package cluster

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/nats"
)

// setupNATSContainer starts a NATS container with JetStream enabled and returns the connection URL
func setupNATSContainer(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()

	natsContainer, err := nats.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err, "failed to start NATS container")

	url, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err, "failed to get NATS connection string")

	cleanup := func() {
		if err := natsContainer.Terminate(ctx); err != nil {
			t.Logf("failed to terminate NATS container: %v", err)
		}
	}

	return url, cleanup
}

// trackingHooks is a test implementation of Hooks that tracks all callbacks
type trackingHooks struct {
	mu                   sync.Mutex
	becomeLeaderCalls    int
	loseLeadershipCalls  int
	leaderChangeCalls    int
	leaderChangeNodeIDs  []string
	becomeLeaderErr      error
	loseLeadershipErr    error
	leaderChangeErr      error
}

func newTrackingHooks() *trackingHooks {
	return &trackingHooks{
		leaderChangeNodeIDs: make([]string, 0),
	}
}

func (h *trackingHooks) OnBecomeLeader(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.becomeLeaderCalls++
	return h.becomeLeaderErr
}

func (h *trackingHooks) OnLoseLeadership(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loseLeadershipCalls++
	return h.loseLeadershipErr
}

func (h *trackingHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaderChangeCalls++
	h.leaderChangeNodeIDs = append(h.leaderChangeNodeIDs, nodeID)
	return h.leaderChangeErr
}

func (h *trackingHooks) getBecomeLeaderCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.becomeLeaderCalls
}

func (h *trackingHooks) getLoseLeadershipCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.loseLeadershipCalls
}

func (h *trackingHooks) getLeaderChangeCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.leaderChangeCalls
}

// waitFor waits for a condition to be true within the timeout
func waitFor(t *testing.T, timeout time.Duration, condition func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", msg)
}

func TestE2E_SingleNodeBecomesLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	hooks := newTrackingHooks()
	node, err := NewNode(Config{
		ClusterID:     "test-cluster",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer node.Stop(ctx)

	// Wait for node to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	assert.Equal(t, RolePrimary, node.Role())
	assert.Equal(t, "node-1", node.Leader())
	assert.Equal(t, int64(1), node.Epoch())
	assert.Equal(t, 1, hooks.getBecomeLeaderCalls())
}

func TestE2E_TwoNodes_OneBecomesLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	hooks1 := newTrackingHooks()
	hooks2 := newTrackingHooks()

	node1, err := NewNode(Config{
		ClusterID:     "test-cluster-2",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks1)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:     "test-cluster-2",
		NodeID:        "node-2",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks2)
	require.NoError(t, err)

	// Start both nodes
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer node1.Stop(ctx)

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer node2.Stop(ctx)

	// Wait for one node to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader() || node2.IsLeader()
	}, "one node to become leader")

	// Exactly one should be leader
	assert.True(t, node1.IsLeader() != node2.IsLeader(), "exactly one node should be leader")

	// Both should know who the leader is
	leader := node1.Leader()
	if leader == "" {
		leader = node2.Leader()
	}
	assert.NotEmpty(t, leader)

	// Wait for follower to detect the leader
	waitFor(t, 5*time.Second, func() bool {
		return node1.Leader() == leader && node2.Leader() == leader
	}, "both nodes to agree on leader")
}

func TestE2E_LeaderStepDown_TriggersFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	hooks1 := newTrackingHooks()
	hooks2 := newTrackingHooks()

	node1, err := NewNode(Config{
		ClusterID:     "test-cluster-3",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks1)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:     "test-cluster-3",
		NodeID:        "node-2",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks2)
	require.NoError(t, err)

	// Start node1 first so it becomes leader
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer node1.Stop(ctx)

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	// Now start node2
	err = node2.Start(ctx)
	require.NoError(t, err)
	defer node2.Stop(ctx)

	// Verify node1 is still leader
	time.Sleep(2 * time.Second)
	assert.True(t, node1.IsLeader())
	assert.False(t, node2.IsLeader())

	initialEpoch := node1.Epoch()

	// Node1 steps down
	err = node1.StepDown(ctx)
	require.NoError(t, err)

	assert.False(t, node1.IsLeader())
	assert.Equal(t, 1, hooks1.getLoseLeadershipCalls())

	// Wait for node2 to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node2.IsLeader()
	}, "node2 to become leader after stepdown")

	// Epoch should be >= initial (may increment or stay same depending on timing)
	assert.GreaterOrEqual(t, node2.Epoch(), initialEpoch)
	assert.Equal(t, 1, hooks2.getBecomeLeaderCalls())
}

func TestE2E_EpochIncrementsOnFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node1, err := NewNode(Config{
		ClusterID:     "test-cluster-4",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start and become leader
	err = node1.Start(ctx)
	require.NoError(t, err)

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	epoch1 := node1.Epoch()
	assert.Equal(t, int64(1), epoch1)

	// Stop node1 (simulating failure)
	node1.Stop(ctx)

	// Start a new node
	node2, err := NewNode(Config{
		ClusterID:     "test-cluster-4",
		NodeID:        "node-2",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer node2.Stop(ctx)

	// Wait for node2 to become leader (may need to wait for lease expiry)
	waitFor(t, 15*time.Second, func() bool {
		return node2.IsLeader()
	}, "node2 to become leader after node1 failure")

	epoch2 := node2.Epoch()
	assert.Greater(t, epoch2, epoch1, "epoch should increment on failover")
}

func TestE2E_StepDownNotLeader(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node1, err := NewNode(Config{
		ClusterID:     "test-cluster-5",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:     "test-cluster-5",
		NodeID:        "node-2",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      5 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start node1 first
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer node1.Stop(ctx)

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	// Start node2
	err = node2.Start(ctx)
	require.NoError(t, err)
	defer node2.Stop(ctx)

	// Wait for node2 to see node1 as leader
	waitFor(t, 5*time.Second, func() bool {
		return node2.Leader() == "node-1"
	}, "node2 to see node1 as leader")

	// node2 should not be leader, so StepDown should fail
	err = node2.StepDown(ctx)
	assert.Equal(t, ErrNotLeader, err)
}

func TestE2E_CooldownPreventsImmediateReelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node1, err := NewNode(Config{
		ClusterID:     "test-cluster-6",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:     "test-cluster-6",
		NodeID:        "node-2",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start both nodes
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer node1.Stop(ctx)

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer node2.Stop(ctx)

	// Wait for one to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader() || node2.IsLeader()
	}, "one node to become leader")

	var leader, follower *Node
	if node1.IsLeader() {
		leader = node1
		follower = node2
	} else {
		leader = node2
		follower = node1
	}

	// Step down the leader
	leaderID := leader.Leader()
	err = leader.StepDown(ctx)
	require.NoError(t, err)

	// The follower should become the new leader
	waitFor(t, 10*time.Second, func() bool {
		return follower.IsLeader()
	}, "follower to become leader")

	// The original leader should NOT immediately become leader again due to cooldown
	time.Sleep(1 * time.Second)
	assert.False(t, leader.IsLeader(), "original leader should not immediately re-acquire leadership due to cooldown")
	assert.NotEqual(t, leaderID, follower.Leader(), "leadership should have transferred")
}

func TestE2E_HooksCalledCorrectly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	hooks := newTrackingHooks()

	node, err := NewNode(Config{
		ClusterID:     "test-cluster-7",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, hooks)
	require.NoError(t, err)

	// Before start, no hooks should be called
	assert.Equal(t, 0, hooks.getBecomeLeaderCalls())
	assert.Equal(t, 0, hooks.getLoseLeadershipCalls())

	err = node.Start(ctx)
	require.NoError(t, err)

	// Wait to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// OnBecomeLeader should have been called
	assert.Equal(t, 1, hooks.getBecomeLeaderCalls())
	assert.Equal(t, 0, hooks.getLoseLeadershipCalls())

	// Step down
	err = node.StepDown(ctx)
	require.NoError(t, err)

	// OnLoseLeadership should have been called
	assert.Equal(t, 1, hooks.getBecomeLeaderCalls())
	assert.Equal(t, 1, hooks.getLoseLeadershipCalls())

	// Stop the node
	err = node.Stop(ctx)
	require.NoError(t, err)
}

func TestE2E_MultipleNodesCluster(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	const numNodes = 3
	nodes := make([]*Node, numNodes)
	hooks := make([]*trackingHooks, numNodes)

	// Create all nodes
	for i := 0; i < numNodes; i++ {
		hooks[i] = newTrackingHooks()
		node, err := NewNode(Config{
			ClusterID:     "test-cluster-8",
			NodeID:        fmt.Sprintf("node-%d", i+1),
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}, hooks[i])
		require.NoError(t, err)
		nodes[i] = node
	}

	// Start all nodes
	for i, node := range nodes {
		err := node.Start(ctx)
		require.NoError(t, err, "failed to start node %d", i+1)
		defer node.Stop(ctx)
	}

	// Wait for exactly one leader
	waitFor(t, 15*time.Second, func() bool {
		leaderCount := 0
		for _, node := range nodes {
			if node.IsLeader() {
				leaderCount++
			}
		}
		return leaderCount == 1
	}, "exactly one leader to be elected")

	// All nodes should agree on the leader
	var expectedLeader string
	for _, node := range nodes {
		if node.IsLeader() {
			expectedLeader = node.cfg.NodeID
			break
		}
	}

	waitFor(t, 5*time.Second, func() bool {
		for _, node := range nodes {
			if node.Leader() != expectedLeader {
				return false
			}
		}
		return true
	}, "all nodes to agree on leader")

	// Verify only one OnBecomeLeader was called across all hooks
	totalBecomeLeaderCalls := 0
	for _, h := range hooks {
		totalBecomeLeaderCalls += h.getBecomeLeaderCalls()
	}
	assert.Equal(t, 1, totalBecomeLeaderCalls, "exactly one OnBecomeLeader should be called")
}

func TestE2E_NodeStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node, err := NewNode(Config{
		ClusterID:     "test-cluster-9",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start
	err = node.Start(ctx)
	require.NoError(t, err)

	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// Try to start again - should fail
	err = node.Start(ctx)
	assert.Equal(t, ErrAlreadyStarted, err)

	// Stop
	err = node.Stop(ctx)
	require.NoError(t, err)

	// After stop, should not be leader
	assert.False(t, node.IsLeader())
	assert.Equal(t, RolePassive, node.Role())

	// Try to stop again - should fail
	err = node.Stop(ctx)
	assert.Equal(t, ErrNotStarted, err)
}

// TestE2E_LeaderRenewal verifies that the leader continues to renew its lease
func TestE2E_LeaderRenewal(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node, err := NewNode(Config{
		ClusterID:     "test-cluster-10",
		NodeID:        "node-1",
		NATSURLs:      []string{natsURL},
		LeaseTTL:      3 * time.Second,
		RenewInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer node.Stop(ctx)

	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// Wait for multiple lease renewals (longer than LeaseTTL)
	time.Sleep(5 * time.Second)

	// Node should still be leader after multiple renewals
	assert.True(t, node.IsLeader())
	assert.Equal(t, int64(1), node.Epoch(), "epoch should not change during renewals")
}
