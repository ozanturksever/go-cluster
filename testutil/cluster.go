package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
)

// TestCluster wraps multiple cluster nodes for testing.
type TestCluster struct {
	t         *testing.T
	nats      *NATSServer
	platforms []*cluster.Platform
	nodes     map[string]*TestNode
}

// TestNode represents a node in the test cluster.
type TestNode struct {
	platform *cluster.Platform
	app      *cluster.App
	cancel   context.CancelFunc
}

// ClusterConfig configures a test cluster.
type ClusterConfig struct {
	Nodes       int
	PlatformName string
	AppName     string
	Mode        cluster.Mode
}

// StartCluster starts a test cluster with the given configuration.
func StartCluster(t *testing.T, cfg ClusterConfig) *TestCluster {
	t.Helper()

	if cfg.Nodes < 1 {
		cfg.Nodes = 1
	}
	if cfg.PlatformName == "" {
		cfg.PlatformName = "test"
	}
	if cfg.AppName == "" {
		cfg.AppName = "testapp"
	}

	// Start NATS
	nats := StartNATS(t)

	tc := &TestCluster{
		t:         t,
		nats:      nats,
		platforms: make([]*cluster.Platform, 0, cfg.Nodes),
		nodes:     make(map[string]*TestNode),
	}

	// Start nodes
	for i := 0; i < cfg.Nodes; i++ {
		nodeID := fmt.Sprintf("node-%d", i+1)
		tc.addNode(nodeID, cfg)
	}

	return tc
}

// addNode adds a new node to the cluster.
func (tc *TestCluster) addNode(nodeID string, cfg ClusterConfig) {
	platform, err := cluster.NewPlatform(
		cfg.PlatformName,
		nodeID,
		tc.nats.URL(),
		cluster.HealthAddr(fmt.Sprintf(":%d", 18080+len(tc.nodes))),
		cluster.MetricsAddr(fmt.Sprintf(":%d", 19090+len(tc.nodes))),
	)
	if err != nil {
		tc.t.Fatalf("failed to create platform for %s: %v", nodeID, err)
	}

	var app *cluster.App
	switch cfg.Mode {
	case cluster.ModeSpread:
		app = cluster.NewApp(cfg.AppName, cluster.Spread())
	case cluster.ModeReplicas:
		app = cluster.NewApp(cfg.AppName, cluster.Replicas(cfg.Nodes))
	default:
		app = cluster.NewApp(cfg.AppName, cluster.Singleton())
	}

	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	go platform.Run(ctx)

	tc.platforms = append(tc.platforms, platform)
	tc.nodes[nodeID] = &TestNode{
		platform: platform,
		app:      app,
		cancel:   cancel,
	}
}

// WaitForLeader waits for a leader to be elected.
func (tc *TestCluster) WaitForLeader(t *testing.T, timeout time.Duration) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		for nodeID, node := range tc.nodes {
			if node.app.IsLeader() {
				return nodeID
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("no leader elected within timeout")
	return ""
}

// Node returns the test node for the given node ID.
func (tc *TestCluster) Node(nodeID string) *TestNode {
	return tc.nodes[nodeID]
}

// Kill stops the node with the given ID.
func (tc *TestCluster) Kill(nodeID string) {
	node, ok := tc.nodes[nodeID]
	if !ok {
		return
	}
	if node.cancel != nil {
		node.cancel()
	}
}

// Restart restarts the node with the given ID.
func (tc *TestCluster) Restart(nodeID string, cfg ClusterConfig) {
	tc.addNode(nodeID, cfg)
}

// LeaderCount returns the number of nodes that think they're the leader.
func (tc *TestCluster) LeaderCount() int {
	count := 0
	for _, node := range tc.nodes {
		if node.app.IsLeader() {
			count++
		}
	}
	return count
}

// Stop stops all nodes and NATS.
func (tc *TestCluster) Stop() {
	for _, node := range tc.nodes {
		if node.cancel != nil {
			node.cancel()
		}
	}
	tc.nats.Stop()
}

// IsLeader returns true if this node is the leader.
func (n *TestNode) IsLeader() bool {
	return n.app.IsLeader()
}

// Role returns the current role of this node.
func (n *TestNode) Role() cluster.Role {
	return n.app.Role()
}

// Platform returns the platform for this node.
func (n *TestNode) Platform() *cluster.Platform {
	return n.platform
}

// App returns the app for this node.
func (n *TestNode) App() *cluster.App {
	return n.app
}
