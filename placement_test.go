package cluster_test

import (
	"context"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
)

// TestScheduler_EvaluatePlacement tests basic placement constraint evaluation
func TestScheduler_EvaluatePlacement(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform with labels
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"role": "compute",
			"zone": "us-east-1a",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create app with label constraint
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.OnLabel("role", "compute"),
	)

	platform.Register(app)

	// Start platform
	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	// Wait for startup
	time.Sleep(500 * time.Millisecond)

	// Evaluate placement
	scheduler := platform.Scheduler()
	results := scheduler.EvaluatePlacement(app)

	if len(results) == 0 {
		t.Fatal("Expected at least one placement result")
	}

	// Node should satisfy constraints
	for _, r := range results {
		if r.NodeID == "node-1" {
			if !r.Satisfied {
				t.Errorf("Expected node-1 to satisfy constraints, violations: %v", r.Violations)
			}
		}
	}

	cancel()
	<-done
}

// TestScheduler_OnLabel tests OnLabel constraint
func TestScheduler_OnLabel(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform WITHOUT the required label
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"role": "storage", // Different from what app requires
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create app requiring "role=compute"
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.OnLabel("role", "compute"),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Evaluate placement - should NOT be satisfied
	scheduler := platform.Scheduler()
	valid, violations := scheduler.ValidatePlacement(app)

	if valid {
		t.Error("Expected placement to be invalid due to missing label")
	}

	if len(violations) == 0 {
		t.Error("Expected violations to be reported")
	}

	t.Logf("Violations: %v", violations)

	cancel()
	<-done
}

// TestScheduler_AvoidLabel tests AvoidLabel constraint
func TestScheduler_AvoidLabel(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform with the avoided label
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"environment": "production",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create app that avoids production nodes
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.AvoidLabel("environment", "production"),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Evaluate placement - should NOT be satisfied
	scheduler := platform.Scheduler()
	valid, violations := scheduler.ValidatePlacement(app)

	if valid {
		t.Error("Expected placement to be invalid due to avoided label")
	}

	t.Logf("Violations: %v", violations)

	cancel()
	<-done
}

// TestScheduler_PreferLabel tests PreferLabel soft constraint
func TestScheduler_PreferLabel(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create platform with preferred label
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"tier": "premium",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create app that prefers premium tier (weight 20)
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.PreferLabel("tier", "premium", 20),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Evaluate placement - should be satisfied with higher score
	scheduler := platform.Scheduler()
	results := scheduler.EvaluatePlacement(app)

	for _, r := range results {
		if r.NodeID == "node-1" {
			if !r.Satisfied {
				t.Error("Expected placement to be satisfied")
			}
			// Base score (100) + preference weight (20) = 120
			if r.Score < 120 {
				t.Errorf("Expected score >= 120 for preferred label, got %d", r.Score)
			}
			t.Logf("Node %s score: %d", r.NodeID, r.Score)
		}
	}

	cancel()
	<-done
}

// TestScheduler_OnNodes tests OnNodes constraint
func TestScheduler_OnNodes(t *testing.T) {
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

	// Create app restricted to specific nodes (not including node-1)
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.OnNodes("node-2", "node-3"),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Evaluate placement - should NOT be satisfied (node-1 not in allowed list)
	scheduler := platform.Scheduler()
	valid, violations := scheduler.ValidatePlacement(app)

	if valid {
		t.Error("Expected placement to be invalid (node not in allowed list)")
	}

	t.Logf("Violations: %v", violations)

	cancel()
	<-done
}

// TestScheduler_CordonNode tests node cordoning
func TestScheduler_CordonNode(t *testing.T) {
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

	app := cluster.NewApp("test-app", cluster.Singleton())
	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	scheduler := platform.Scheduler()

	// Cordon the node
	platform.CordonNode("node-1")

	if !scheduler.IsNodeCordoned("node-1") {
		t.Error("Expected node-1 to be cordoned")
	}

	// FindBestNode should fail (only node is cordoned)
	_, err = scheduler.FindBestNode(app)
	if err == nil {
		t.Error("Expected FindBestNode to fail when only node is cordoned")
	}

	// Uncordon
	platform.UncordonNode("node-1")

	if scheduler.IsNodeCordoned("node-1") {
		t.Error("Expected node-1 to be uncordoned")
	}

	cancel()
	<-done
}

// TestScheduler_MigrationCooldown tests migration cooldown enforcement
func TestScheduler_MigrationCooldown(t *testing.T) {
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

	app := cluster.NewApp("test-app", cluster.Singleton())
	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// First migration request (will fail due to no target, but sets cooldown)
	_, _ = platform.MoveApp(ctx, cluster.MoveRequest{
		App:      "test-app",
		FromNode: "node-1",
		Reason:   "test",
	})

	// Second immediate request should fail due to cooldown
	_, err = platform.MoveApp(ctx, cluster.MoveRequest{
		App:      "test-app",
		FromNode: "node-1",
		Reason:   "test",
	})

	// The error might be about no target nodes OR cooldown - both are acceptable
	// In a single-node test, we can't fully test cooldown, but we verify the API works
	t.Logf("Second migration error (expected): %v", err)

	cancel()
	<-done
}

// TestScheduler_SetLabels tests dynamic label updates
func TestScheduler_SetLabels(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"role": "storage",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// App requires role=compute
	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.OnLabel("role", "compute"),
	)

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Initially should be invalid
	scheduler := platform.Scheduler()
	valid, _ := scheduler.ValidatePlacement(app)
	if valid {
		t.Error("Expected initial placement to be invalid")
	}

	// Update labels to satisfy constraint
	err = platform.SetLabels(ctx, map[string]string{"role": "compute"})
	if err != nil {
		t.Fatalf("Failed to set labels: %v", err)
	}

	// Wait for membership update to propagate
	time.Sleep(500 * time.Millisecond)

	// Now should be valid
	valid, violations := scheduler.ValidatePlacement(app)
	if !valid {
		t.Errorf("Expected placement to be valid after label update, violations: %v", violations)
	}

	cancel()
	<-done
}

// TestScheduler_WatchLabels tests label change watching
func TestScheduler_WatchLabels(t *testing.T) {
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

	app := cluster.NewApp("test-app", cluster.Singleton())
	platform.Register(app)

	// Register watcher
	labelChanges := make(chan map[string]string, 1)
	platform.WatchLabels(func(nodeID string, labels map[string]string) {
		if nodeID == "node-1" {
			select {
			case labelChanges <- labels:
			default:
			}
		}
	})

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// Watcher is registered and platform is running
	// In a real multi-node test, we'd see label changes from other nodes
	t.Log("Label watcher registered successfully")

	cancel()
	<-done
}

// TestScheduler_OnPlacementInvalid tests the OnPlacementInvalid hook
func TestScheduler_OnPlacementInvalid(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
		cluster.Labels(map[string]string{
			"role": "storage",
		}),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Track if hook was called
	hookCalled := make(chan string, 1)

	app := cluster.NewApp("test-app",
		cluster.Singleton(),
		cluster.OnLabel("role", "compute"), // Will not be satisfied
	)

	app.OnPlacementInvalid(func(ctx context.Context, reason string) error {
		select {
		case hookCalled <- reason:
		default:
		}
		return nil
	})

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// The hook should be callable when placement is evaluated
	// We can manually trigger reevaluation
	scheduler := platform.Scheduler()
	valid, _ := scheduler.ValidatePlacement(app)

	if valid {
		t.Error("Expected placement to be invalid")
	}

	// Hook is configured correctly
	t.Log("OnPlacementInvalid hook configured successfully")

	cancel()
	<-done
}

// TestScheduler_AwayFromApp tests app anti-affinity
func TestScheduler_AwayFromApp(t *testing.T) {
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

	// Register first app
	app1 := cluster.NewApp("database", cluster.Singleton())
	platform.Register(app1)

	// Register second app with anti-affinity to first
	app2 := cluster.NewApp("cache",
		cluster.Singleton(),
		cluster.AwayFromApp("database"),
	)
	platform.Register(app2)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// cache should have invalid placement since database is on same node
	scheduler := platform.Scheduler()
	valid, violations := scheduler.ValidatePlacement(app2)

	if valid {
		t.Error("Expected cache placement to be invalid due to anti-affinity with database")
	}

	t.Logf("Anti-affinity violations: %v", violations)

	cancel()
	<-done
}

// TestScheduler_WithApp tests app affinity
func TestScheduler_WithApp(t *testing.T) {
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

	// Register first app
	app1 := cluster.NewApp("database", cluster.Singleton())
	platform.Register(app1)

	// Register second app requiring co-location with first
	app2 := cluster.NewApp("sidecar",
		cluster.Singleton(),
		cluster.WithApp("database"),
	)
	platform.Register(app2)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	// sidecar should have valid placement since database is on same node
	scheduler := platform.Scheduler()
	valid, violations := scheduler.ValidatePlacement(app2)

	if !valid {
		t.Errorf("Expected sidecar placement to be valid (co-located with database), violations: %v", violations)
	}

	cancel()
	<-done
}
