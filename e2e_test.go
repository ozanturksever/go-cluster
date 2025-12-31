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
	reconnectCalls       int
	disconnectCalls      int
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

func (h *trackingHooks) OnNATSReconnect(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reconnectCalls++
	return nil
}

func (h *trackingHooks) OnNATSDisconnect(ctx context.Context, err error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.disconnectCalls++
	return nil
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

func (h *trackingHooks) getReconnectCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.reconnectCalls
}

func (h *trackingHooks) getDisconnectCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.disconnectCalls
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
		ClusterID:         "test-cluster",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, hooks)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node.Stop(ctx) }()

	// Wait for node to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	assert.Equal(t, RolePrimary, node.Role())
	assert.Equal(t, "node-1", node.Leader())
	assert.Equal(t, int64(1), node.Epoch())
	assert.Equal(t, 1, hooks.getBecomeLeaderCalls())
	assert.True(t, node.Connected())
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
		ClusterID:         "test-cluster-2",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, hooks1)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-2",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, hooks2)
	require.NoError(t, err)

	// Start both nodes
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node1.Stop(ctx) }()

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

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
		ClusterID:         "test-cluster-3",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, hooks1)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-3",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, hooks2)
	require.NoError(t, err)

	// Start node1 first so it becomes leader
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node1.Stop(ctx) }()

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	// Now start node2
	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

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

	// Wait for node2 to become leader - should be fast with KV watch
	start := time.Now()
	waitFor(t, 10*time.Second, func() bool {
		return node2.IsLeader()
	}, "node2 to become leader after stepdown")
	failoverTime := time.Since(start)

	t.Logf("Failover time: %v", failoverTime)

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

	// Use shorter LeaseTTL for faster crash recovery test
	node1, err := NewNode(Config{
		ClusterID:         "test-cluster-4",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          2 * time.Second,
		HeartbeatInterval: 500 * time.Millisecond,
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

	// Stop node1 (simulating crash - not graceful stepdown)
	_ = node1.Stop(ctx)

	// Start a new node with same timing config
	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-4",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          2 * time.Second,
		HeartbeatInterval: 500 * time.Millisecond,
	}, nil)
	require.NoError(t, err)

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

	// Wait for node2 to become leader (needs to wait for lease expiry)
	// With LeaseTTL=2s, the lease will expire after 2s and node2 can take over
	waitFor(t, 10*time.Second, func() bool {
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
		ClusterID:         "test-cluster-5",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-5",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start node1 first
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node1.Stop(ctx) }()

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	// Start node2
	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

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
		ClusterID:         "test-cluster-6",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-6",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start both nodes
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node1.Stop(ctx) }()

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

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
		ClusterID:         "test-cluster-7",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
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
			ClusterID:         "test-cluster-8",
			NodeID:            fmt.Sprintf("node-%d", i+1),
			NATSURLs:          []string{natsURL},
			LeaseTTL:          3 * time.Second,
			HeartbeatInterval: 1 * time.Second,
		}, hooks[i])
		require.NoError(t, err)
		nodes[i] = node
	}

	// Start all nodes
	for i, node := range nodes {
		err := node.Start(ctx)
		require.NoError(t, err, "failed to start node %d", i+1)
		defer func(n *Node) { _ = n.Stop(ctx) }(node)
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
		ClusterID:         "test-cluster-9",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
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
		ClusterID:         "test-cluster-10",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          3 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node.Stop(ctx) }()

	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// Wait for multiple lease renewals (longer than LeaseTTL)
	time.Sleep(5 * time.Second)

	// Node should still be leader after multiple renewals
	assert.True(t, node.IsLeader())
	assert.Equal(t, int64(1), node.Epoch(), "epoch should not change during renewals")
}

// TestE2E_NATSReconnection verifies that OnNATSDisconnect and OnNATSReconnect hooks are called
func TestE2E_NATSReconnection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()

	// Start NATS container
	natsContainer, err := nats.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err, "failed to start NATS container")
	defer func() { _ = natsContainer.Terminate(ctx) }()

	natsURL, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err)
	t.Logf("NATS URL: %s", natsURL)

	hooks := newTrackingHooks()
	node, err := NewNode(Config{
		ClusterID:         "test-cluster-reconnect",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		ReconnectWait:     500 * time.Millisecond,
		MaxReconnects:     -1, // Unlimited reconnects
	}, hooks)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node.Stop(ctx) }()

	// Wait for node to become leader and stabilize
	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// Verify initial state - no disconnects or reconnects yet
	initialDisconnects := hooks.getDisconnectCalls()
	initialReconnects := hooks.getReconnectCalls()
	t.Logf("Initial state - disconnects: %d, reconnects: %d", initialDisconnects, initialReconnects)

	// Use Docker pause/unpause to simulate network partition
	// This preserves port mappings unlike stop/start
	containerID := natsContainer.GetContainerID()
	t.Logf("Pausing container %s to simulate network partition...", containerID[:12])

	// Access the Docker client to pause the container
	dockerClient, err := testcontainers.NewDockerClientWithOpts(ctx)
	require.NoError(t, err, "failed to create docker client")
	defer dockerClient.Close()

	err = dockerClient.ContainerPause(ctx, containerID)
	require.NoError(t, err, "failed to pause NATS container")

	// Wait for OnNATSDisconnect to be called
	// NATS client uses ping intervals to detect disconnects, so this can take ~30s
	waitFor(t, 45*time.Second, func() bool {
		return hooks.getDisconnectCalls() > initialDisconnects
	}, "OnNATSDisconnect to be called")

	t.Logf("After pause - disconnects: %d", hooks.getDisconnectCalls())
	assert.Greater(t, hooks.getDisconnectCalls(), initialDisconnects, "OnNATSDisconnect should have been called")

	// Unpause the container to restore connectivity
	t.Log("Unpausing NATS container...")
	err = dockerClient.ContainerUnpause(ctx, containerID)
	require.NoError(t, err, "failed to unpause NATS container")

	// Wait for OnNATSReconnect to be called
	waitFor(t, 30*time.Second, func() bool {
		return hooks.getReconnectCalls() > initialReconnects
	}, "OnNATSReconnect to be called")

	t.Logf("After unpause - reconnects: %d", hooks.getReconnectCalls())
	assert.Greater(t, hooks.getReconnectCalls(), initialReconnects, "OnNATSReconnect should have been called")

	// Verify node is connected again
	waitFor(t, 10*time.Second, func() bool {
		return node.Connected()
	}, "node to reconnect")

	assert.True(t, node.Connected(), "node should be connected after reconnection")
}

// TestE2E_FastFailover verifies that graceful stepdown failover happens in under 500ms
func TestE2E_FastFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	// Use fast timing for this test
	node1, err := NewNode(Config{
		ClusterID:         "test-cluster-fast-failover",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-fast-failover",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	// Start node1 first to ensure it becomes leader
	err = node1.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node1.Stop(ctx) }()

	waitFor(t, 10*time.Second, func() bool {
		return node1.IsLeader()
	}, "node1 to become leader")

	// Start node2 as follower
	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

	// Wait for node2 to see node1 as leader
	waitFor(t, 5*time.Second, func() bool {
		return node2.Leader() == "node-1"
	}, "node2 to see node1 as leader")

	// Let the cluster stabilize
	time.Sleep(2 * time.Second)

	// Measure failover time - node1 steps down, measure time until node2 becomes leader
	startTime := time.Now()
	err = node1.StepDown(ctx)
	require.NoError(t, err)

	// Wait for node2 to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node2.IsLeader()
	}, "node2 to become leader after stepdown")

	failoverTime := time.Since(startTime)
	t.Logf("Failover time: %v", failoverTime)

	// Assert failover happened in under 500ms for graceful stepdown
	// KV Watch should make this very fast
	assert.Less(t, failoverTime, 500*time.Millisecond,
		"graceful stepdown failover should complete in under 500ms, got %v", failoverTime)

	// Verify node2 is now the leader
	assert.True(t, node2.IsLeader())
	assert.False(t, node1.IsLeader())
}

// TestE2E_MicroServiceEndpoints verifies the micro service endpoints work
func TestE2E_MicroServiceEndpoints(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	node, err := NewNode(Config{
		ClusterID:         "test-cluster-11",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
	}, nil)
	require.NoError(t, err)

	err = node.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node.Stop(ctx) }()

	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	// Verify service is available
	service := node.Service()
	assert.NotNil(t, service, "service should be started")

	info := service.Info()
	assert.NotEmpty(t, info.Name, "service info should be available")
	assert.Equal(t, "cluster_test-cluster-11", info.Name)
}

// TestE2E_MultiNATSServer verifies that nodes can be configured with multiple NATS server URLs.
// This tests the configuration capability - in production, these URLs would point to a NATS cluster.
func TestE2E_MultiNATSServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e test in short mode")
	}

	ctx := context.Background()

	// Start a single NATS container - we'll configure the node with the same URL twice
	// to simulate having multiple URLs in the config (as would be the case with a real NATS cluster)
	natsURL, cleanup := setupNATSContainer(t, ctx)
	defer cleanup()

	t.Logf("NATS URL: %s", natsURL)

	hooks := newTrackingHooks()

	// Create node with multiple URLs (same server, simulating cluster config)
	// In production, these would be different servers in a NATS cluster
	node, err := NewNode(Config{
		ClusterID:         "test-cluster-multi-nats",
		NodeID:            "node-1",
		NATSURLs:          []string{natsURL, natsURL}, // Multiple URLs pointing to same server
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		ReconnectWait:     500 * time.Millisecond,
		MaxReconnects:     10,
	}, hooks)
	require.NoError(t, err)

	// Start node - it should connect successfully
	err = node.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node.Stop(ctx) }()

	// Wait for node to become leader
	waitFor(t, 10*time.Second, func() bool {
		return node.IsLeader()
	}, "node to become leader")

	assert.True(t, node.Connected(), "node should be connected")
	assert.True(t, node.IsLeader(), "node should be leader")
	t.Logf("Node connected and became leader")

	// Verify the node is working correctly with multiple URLs configured
	assert.Equal(t, int64(1), node.Epoch())
	assert.Equal(t, "node-1", node.Leader())

	// Test that a second node can also connect with multiple URLs to the same cluster
	node2, err := NewNode(Config{
		ClusterID:         "test-cluster-multi-nats",
		NodeID:            "node-2",
		NATSURLs:          []string{natsURL, natsURL},
		LeaseTTL:          5 * time.Second,
		HeartbeatInterval: 1 * time.Second,
		ReconnectWait:     500 * time.Millisecond,
		MaxReconnects:     10,
	}, nil)
	require.NoError(t, err)

	err = node2.Start(ctx)
	require.NoError(t, err)
	defer func() { _ = node2.Stop(ctx) }()

	// Wait for node2 to see node1 as leader
	waitFor(t, 10*time.Second, func() bool {
		return node2.Leader() == "node-1"
	}, "node2 to see node1 as leader")

	assert.True(t, node2.Connected(), "node2 should be connected")
	assert.False(t, node2.IsLeader(), "node2 should not be leader")
	assert.Equal(t, "node-1", node2.Leader(), "node2 should see node-1 as leader")

	t.Logf("Both nodes successfully connected with multiple NATS URLs configured")
}
