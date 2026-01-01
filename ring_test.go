package cluster_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
)

// TestRing_Basic tests basic ring functionality
func TestRing_Basic(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create ring app with 16 partitions
	app := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(16),
	)

	platform.Register(app)

	// Start platform in background
	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	// Wait for ring to initialize
	time.Sleep(500 * time.Millisecond)

	// Get ring
	ring := app.Ring()
	if ring == nil {
		t.Fatal("Ring should not be nil")
	}

	// Check basic properties
	if ring.NumPartitions() != 16 {
		t.Errorf("Expected 16 partitions, got %d", ring.NumPartitions())
	}

	// Wait for partition assignment
	time.Sleep(500 * time.Millisecond)

	// Check that we own all partitions (single node)
	owned := ring.OwnedPartitions()
	if len(owned) != 16 {
		t.Errorf("Expected to own 16 partitions, got %d", len(owned))
	}

	// Test NodeFor
	node, err := ring.NodeFor("test-key")
	if err != nil {
		t.Errorf("NodeFor failed: %v", err)
	}
	if node != "node-1" {
		t.Errorf("Expected node-1, got %s", node)
	}

	// Test PartitionFor consistency
	p1 := ring.PartitionFor("test-key")
	p2 := ring.PartitionFor("test-key")
	if p1 != p2 {
		t.Errorf("PartitionFor not consistent: %d != %d", p1, p2)
	}

	// Cancel and check cleanup
	cancel()
	<-done
}

// TestRing_OnOwnOnRelease tests partition ownership hooks
func TestRing_OnOwnOnRelease(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Track hook calls
	var ownedPartitions []int
	var ownMu sync.Mutex
	ownCalled := make(chan struct{}, 1)

	// Create ring app
	app := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(8),
	)

	app.OnOwn(func(ctx context.Context, partitions []int) error {
		ownMu.Lock()
		ownedPartitions = append(ownedPartitions, partitions...)
		ownMu.Unlock()
		select {
		case ownCalled <- struct{}{}:
		default:
		}
		return nil
	})

	platform.Register(app)

	// Start platform
	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	// Wait for OnOwn to be called
	select {
	case <-ownCalled:
		// Good
	case <-time.After(5 * time.Second):
		t.Error("OnOwn was not called within timeout")
	}

	// Verify partitions were assigned
	ownMu.Lock()
	count := len(ownedPartitions)
	ownMu.Unlock()

	if count != 8 {
		t.Errorf("Expected 8 owned partitions, got %d", count)
	}

	cancel()
	<-done
}

// TestRing_KeyDistribution tests that keys are distributed across partitions
func TestRing_KeyDistribution(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	app := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(32),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	ring := app.Ring()
	if ring == nil {
		t.Fatal("Ring is nil")
	}

	// Test key distribution
	partitionCounts := make(map[int]int)
	for i := 0; i < 1000; i++ {
		key := string(rune('a' + (i % 26)))
		for j := 0; j < i; j++ {
			key += string(rune('a' + (j % 26)))
		}
		p := ring.PartitionFor(key)
		partitionCounts[p]++
	}

	// Verify that keys are distributed across multiple partitions
	if len(partitionCounts) < 10 {
		t.Errorf("Expected keys to be distributed across at least 10 partitions, got %d", len(partitionCounts))
	}

	cancel()
	<-done
}

// TestRing_OwnsPartition tests the OwnsPartition helper
func TestRing_OwnsPartition(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	app := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(8),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	// Wait for ring to initialize and get assignments
	time.Sleep(1 * time.Second)

	ring := app.Ring()
	if ring == nil {
		t.Fatal("Ring is nil")
	}

	// Single node should own all partitions
	for p := 0; p < 8; p++ {
		if !ring.OwnsPartition(p) {
			t.Errorf("Expected to own partition %d", p)
		}
	}

	// Test OwnsKey
	key := "test-key"
	if !ring.OwnsKey(key) {
		t.Errorf("Expected to own key %s", key)
	}

	cancel()
	<-done
}

// TestRing_Stats tests the Stats method
func TestRing_Stats(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	app := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(16),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(1 * time.Second)

	ring := app.Ring()
	if ring == nil {
		t.Fatal("Ring is nil")
	}

	stats := ring.Stats()

	if stats.NumPartitions != 16 {
		t.Errorf("Expected 16 partitions, got %d", stats.NumPartitions)
	}

	if stats.NumNodes != 1 {
		t.Errorf("Expected 1 node, got %d", stats.NumNodes)
	}

	if stats.OwnedPartitions != 16 {
		t.Errorf("Expected 16 owned partitions, got %d", stats.OwnedPartitions)
	}

	if !stats.IsCoordinator {
		t.Error("Expected to be coordinator")
	}

	cancel()
	<-done
}

// TestRing_TwoNodes tests ring behavior with two nodes
func TestRing_TwoNodes(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create first platform
	platform1, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform1: %v", err)
	}

	app1 := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(16),
	)

	platform1.Register(app1)

	// Start first platform
	done1 := make(chan error, 1)
	go func() {
		done1 <- platform1.Run(ctx)
	}()

	// Wait for first node to get all partitions using polling
	ring1 := app1.Ring()
	for ring1 == nil {
		time.Sleep(50 * time.Millisecond)
		ring1 = app1.Ring()
	}

	if err := ring1.WaitForPartitions(16, 10*time.Second); err != nil {
		t.Fatalf("Node1 failed to get initial partitions: %v", err)
	}

	// Verify node1 owns all partitions initially
	owned1 := ring1.OwnedPartitions()
	if len(owned1) != 16 {
		t.Errorf("Expected node1 to own 16 partitions initially, got %d", len(owned1))
	}

	// Create second platform
	platform2, err := cluster.NewPlatform("test-platform", "node-2", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform2: %v", err)
	}

	app2 := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(16),
	)

	platform2.Register(app2)

	// Start second platform
	done2 := make(chan error, 1)
	go func() {
		done2 <- platform2.Run(ctx)
	}()

	// Wait for ring2 to be initialized
	ring2 := app2.Ring()
	for ring2 == nil {
		time.Sleep(50 * time.Millisecond)
		ring2 = app2.Ring()
	}

	// Wait for both rings to see 2 nodes (this also triggers rebalance)
	if err := ring1.WaitForNodes(2, 10*time.Second); err != nil {
		t.Fatalf("Ring1 failed to see 2 nodes: %v", err)
	}
	if err := ring2.WaitForNodes(2, 10*time.Second); err != nil {
		t.Fatalf("Ring2 failed to see 2 nodes: %v", err)
	}

	// Force rebalance from coordinator (node-1 since it's lexicographically first)
	ring1.ForceRebalance()

	// Wait for node2 to get some partitions (at least 6, expecting ~8)
	if err := ring2.WaitForPartitions(6, 10*time.Second); err != nil {
		t.Errorf("Node2 didn't get expected partitions: %v", err)
	}

	// Wait a bit for the rebalance to settle
	time.Sleep(500 * time.Millisecond)

	// Check partition distribution using actual ring state (not atomic counters)
	n1 := len(ring1.OwnedPartitions())
	n2 := len(ring2.OwnedPartitions())

	t.Logf("Node1 owns %d partitions, Node2 owns %d partitions", n1, n2)

	// Total should be 16
	total := n1 + n2
	if total != 16 {
		t.Errorf("Total partitions should be 16, got %d", total)
	}

	// Each should have roughly 8 (allow some variance)
	if n1 < 6 || n1 > 10 {
		t.Errorf("Node1 should have ~8 partitions, got %d", n1)
	}
	if n2 < 6 || n2 > 10 {
		t.Errorf("Node2 should have ~8 partitions, got %d", n2)
	}

	// Check that NodeFor returns consistent results between both rings
	for i := 0; i < 100; i++ {
		key := string(rune('a' + i%26))
		owner1, err1 := ring1.NodeFor(key)
		owner2, err2 := ring2.NodeFor(key)

		if err1 != nil || err2 != nil {
			t.Errorf("NodeFor failed for key %s: %v, %v", key, err1, err2)
			continue
		}

		if owner1 != owner2 {
			t.Errorf("Inconsistent ownership for key %s: ring1=%s, ring2=%s", key, owner1, owner2)
		}
	}

	cancel()
	<-done1
	<-done2
}

// TestRing_NodeLeave tests partition rebalancing when a node leaves
func TestRing_NodeLeave(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel1()
	defer cancel2()

	// Create first platform
	platform1, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform1: %v", err)
	}

	app1 := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(8),
	)

	platform1.Register(app1)

	done1 := make(chan error, 1)
	go func() {
		done1 <- platform1.Run(ctx1)
	}()

	// Wait for ring1 to be initialized and get all partitions
	ring1 := app1.Ring()
	for ring1 == nil {
		time.Sleep(50 * time.Millisecond)
		ring1 = app1.Ring()
	}

	if err := ring1.WaitForPartitions(8, 10*time.Second); err != nil {
		t.Fatalf("Node1 failed to get initial partitions: %v", err)
	}

	// Verify node1 owns all partitions initially
	owned1 := ring1.OwnedPartitions()
	if len(owned1) != 8 {
		t.Errorf("Expected node1 to own 8 partitions initially, got %d", len(owned1))
	}

	// Create and start second platform
	platform2, err := cluster.NewPlatform("test-platform", "node-2", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform2: %v", err)
	}

	app2 := cluster.NewApp("cache",
		cluster.Spread(),
		cluster.Ring(8),
	)

	platform2.Register(app2)

	done2 := make(chan error, 1)
	go func() {
		done2 <- platform2.Run(ctx2)
	}()

	// Wait for ring2 to be initialized
	ring2 := app2.Ring()
	for ring2 == nil {
		time.Sleep(50 * time.Millisecond)
		ring2 = app2.Ring()
	}

	// Wait for both nodes to see each other (triggers rebalance)
	if err := ring1.WaitForNodes(2, 10*time.Second); err != nil {
		t.Fatalf("Ring1 failed to see 2 nodes: %v", err)
	}
	if err := ring2.WaitForNodes(2, 10*time.Second); err != nil {
		t.Fatalf("Ring2 failed to see 2 nodes: %v", err)
	}

	// Force rebalance from coordinator
	ring1.ForceRebalance()

	// Wait for node2 to get some partitions (at least 2, expecting ~4)
	if err := ring2.WaitForPartitions(2, 10*time.Second); err != nil {
		t.Logf("Warning: Node2 didn't get expected partitions: %v", err)
	}

	// Wait a bit for rebalance to settle
	time.Sleep(500 * time.Millisecond)

	ownedBefore := len(ring1.OwnedPartitions())
	t.Logf("Node1 owns %d partitions before node2 leaves", ownedBefore)

	// Stop node2 - this will deregister from membership KV
	cancel2()
	<-done2

	// Wait until ring1 sees only 1 node (membership change detected)
	// This uses WaitForNodesExact which polls membership until the node count matches
	if err := ring1.WaitForNodesExact(1, 15*time.Second); err != nil {
		t.Logf("Warning: %v", err)
	}

	// Force final rebalance to reclaim all partitions
	ring1.ForceRebalance()

	// Wait for node1 to reclaim all partitions after node2 leaves
	if err := ring1.WaitForPartitions(8, 10*time.Second); err != nil {
		t.Errorf("Node1 failed to reclaim partitions after node2 left: %v", err)
	}

	// Allow some time for state to settle
	time.Sleep(200 * time.Millisecond)

	ownedAfter := len(ring1.OwnedPartitions())
	t.Logf("Node1 owns %d partitions after node2 leaves", ownedAfter)

	// Node1 should now own all 8 partitions
	if ownedAfter != 8 {
		t.Errorf("Expected node1 to own 8 partitions after node2 leaves, got %d", ownedAfter)
	}

	cancel1()
	<-done1
}

// TestRing_ConsistentHashingProperty tests that consistent hashing minimizes moves
func TestRing_ConsistentHashingProperty(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Create first node
	platform1, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	app1 := cluster.NewApp("cache", cluster.Spread(), cluster.Ring(64))
	platform1.Register(app1)

	done1 := make(chan error, 1)
	go func() {
		done1 <- platform1.Run(ctx)
	}()

	time.Sleep(2 * time.Second)

	ring1 := app1.Ring()
	if ring1 == nil {
		t.Fatal("Ring1 is nil")
	}

	// Record initial key -> partition mapping
	initialMapping := make(map[string]int)
	testKeys := make([]string, 100)
	for i := 0; i < 100; i++ {
		key := string(rune('a' + (i % 26)))
		for j := 0; j < i%10; j++ {
			key += string(rune('0' + j))
		}
		testKeys[i] = key
		initialMapping[key] = ring1.PartitionFor(key)
	}

	// Add second node
	platform2, err := cluster.NewPlatform("test-platform", "node-2", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform2: %v", err)
	}

	app2 := cluster.NewApp("cache", cluster.Spread(), cluster.Ring(64))
	platform2.Register(app2)

	done2 := make(chan error, 1)
	go func() {
		done2 <- platform2.Run(ctx)
	}()

	time.Sleep(3 * time.Second)

	// Check that partition assignments are still consistent
	ring2 := app2.Ring()
	if ring2 == nil {
		t.Fatal("Ring2 is nil")
	}

	// Partition -> key mapping should NOT change (only owner might change)
	for _, key := range testKeys {
		newPartition := ring2.PartitionFor(key)
		oldPartition := initialMapping[key]

		if newPartition != oldPartition {
			t.Errorf("Key %s changed partition from %d to %d (should be stable)", key, oldPartition, newPartition)
		}
	}

	cancel()
	<-done1
	<-done2
}
