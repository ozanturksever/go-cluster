// Package health provides NATS-based health checking for cluster nodes
// using a request/reply pattern.
//
// The health checker allows nodes to advertise their health status and
// query the health of other nodes in the cluster.
//
// # Usage
//
//	checker, err := health.NewChecker(health.Config{
//	    ClusterID: "my-cluster",
//	    NodeID:    "node-1",
//	    NATSURLs:  []string{"nats://localhost:4222"},
//	})
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	if err := checker.Start(ctx); err != nil {
//	    log.Fatal(err)
//	}
//	defer checker.Stop()
//
//	// Update status
//	checker.SetRole("PRIMARY")
//	checker.SetLeader("node-1")
//	checker.SetCustom("connections", 42)
//
//	// Query another node
//	resp, err := checker.QueryNode(ctx, "node-2", 5*time.Second)
//
// # NATS Subject Pattern
//
// Health checks use the subject pattern: cluster.<clusterID>.health.<nodeID>
package health
