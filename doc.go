// Package cluster provides NATS-based cluster coordination for building
// highly-available distributed systems in Go.
//
// The package offers leader election using NATS JetStream KV Watch for sub-500ms
// failover, epoch-based fencing to prevent split-brain scenarios, NATS micro
// service endpoints for health and discovery, and a simple hooks-based API for
// reacting to cluster state changes.
//
// # Quick Start
//
// Create a node and implement the Hooks interface to react to leadership changes:
//
//	type MyApp struct{}
//
//	func (a *MyApp) OnBecomeLeader(ctx context.Context) error {
//	    log.Println("I am now the leader!")
//	    return nil
//	}
//
//	func (a *MyApp) OnLoseLeadership(ctx context.Context) error {
//	    log.Println("I lost leadership")
//	    return nil
//	}
//
//	func (a *MyApp) OnLeaderChange(ctx context.Context, nodeID string) error {
//	    log.Printf("Leader changed to: %s", nodeID)
//	    return nil
//	}
//
//	func (a *MyApp) OnNATSReconnect(ctx context.Context) error {
//	    log.Println("NATS reconnected")
//	    return nil
//	}
//
//	func (a *MyApp) OnNATSDisconnect(ctx context.Context, err error) error {
//	    log.Printf("NATS disconnected: %v", err)
//	    return nil
//	}
//
//	func main() {
//	    node, err := cluster.NewNode(cluster.Config{
//	        ClusterID: "my-cluster",
//	        NodeID:    "node-1",
//	        NATSURLs:  []string{"nats://localhost:4222"},
//	    }, &MyApp{})
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//
//	    ctx := context.Background()
//	    if err := node.Start(ctx); err != nil {
//	        log.Fatal(err)
//	    }
//	    defer node.Stop(ctx)
//
//	    // Your application logic here...
//	}
//
// # Architecture
//
// The package uses NATS KV Watch for reactive leader election:
//
//   - Nodes watch the leader key in NATS JetStream KV Store
//   - KV Watch provides instant notification of leader changes (<500ms failover)
//   - NATS KV MaxAge handles lease TTL automatically
//   - Each leadership change increments an epoch counter for fencing
//   - Resilient NATS connections with automatic reconnection
//
// # Configuration
//
// The [Config] struct contains all configuration options:
//
//   - ClusterID: Identifies the cluster (required)
//   - NodeID: Unique identifier for this node (required)
//   - NATSURLs: NATS server URLs (required)
//   - LeaseTTL: How long the lease is valid (default: 10s)
//   - HeartbeatInterval: How often leader refreshes the lease (default: 3s)
//   - ReconnectWait: Wait between NATS reconnection attempts (default: 2s)
//   - MaxReconnects: Maximum reconnection attempts, -1 for unlimited (default: -1)
//
// # NATS Micro Service
//
// Each node registers as a NATS micro service with endpoints:
//
//   - cluster_<clusterID>.status.<nodeID> - Node status (role, leader, epoch)
//   - cluster_<clusterID>.ping.<nodeID> - Health check
//   - cluster_<clusterID>.control.<nodeID>.stepdown - Trigger stepdown
//
// Use nats micro ls/info/stats CLI commands for service discovery.
//
// # High-Level Manager
//
// For applications requiring VIP management, use the [Manager] type:
//
//	mgr, err := cluster.NewManager(cfg, hooks)
//	mgr.RunDaemon(ctx)  // Runs leader election and VIP management
//
// # Sub-packages
//
// The vip sub-package provides Virtual IP management on Linux systems.
package cluster
