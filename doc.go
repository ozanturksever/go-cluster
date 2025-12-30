// Package cluster provides NATS-based cluster coordination for building
// highly-available distributed systems in Go.
//
// The package offers leader election using NATS JetStream KV Store, epoch-based
// fencing to prevent split-brain scenarios, and a simple hooks-based API for
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
// The package uses a lease-based leader election mechanism:
//
//   - Nodes compete to acquire a lease in NATS JetStream KV Store
//   - The lease holder is the leader and must periodically renew the lease
//   - If the leader fails to renew, other nodes can acquire the lease
//   - Each leadership change increments an epoch counter for fencing
//
// # Configuration
//
// The [Config] struct contains all configuration options:
//
//   - ClusterID: Identifies the cluster (required)
//   - NodeID: Unique identifier for this node (required)
//   - NATSURLs: NATS server URLs (required)
//   - LeaseTTL: How long the lease is valid (default: 10s)
//   - RenewInterval: How often to renew the lease (default: 3s)
//
// # High-Level Manager
//
// For applications requiring VIP management and health checking, use the
// [Manager] type which provides a higher-level API:
//
//	mgr, err := cluster.NewManager(cfg, hooks)
//	mgr.RunDaemon(ctx)  // Runs leader election, VIP, and health checking
//
// # Sub-packages
//
// The following sub-packages provide optional functionality:
//
//   - health: NATS-based health checking using request/reply
//   - vip: Virtual IP management on Linux systems
package cluster
