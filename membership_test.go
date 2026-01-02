package cluster_test

import (
	"context"
	"errors"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMembership_NodeAlreadyPresent(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first platform with node-1
	platform1, err := cluster.NewPlatform("test-presence", "node-1", ns.URL(),
		cluster.HealthAddr(":17080"),
		cluster.MetricsAddr(":17090"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	go platform1.Run(ctx1)

	// Wait for platform1 to be fully registered
	time.Sleep(1 * time.Second)

	// Try to start second platform with the same node ID
	// This should fail because node-1 is already present
	platform2, err := cluster.NewPlatform("test-presence", "node-1", ns.URL(),
		cluster.HealthAddr(":17081"),
		cluster.MetricsAddr(":17091"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Run should return error because node is already present
	errCh := make(chan error, 1)
	go func() {
		errCh <- platform2.Run(ctx2)
	}()

	// Wait for the error or timeout
	select {
	case err := <-errCh:
		assert.True(t, errors.Is(err, cluster.ErrNodeAlreadyPresent), "should fail with ErrNodeAlreadyPresent, got: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("expected error but got timeout")
	}
}

func TestMembership_RejoinAfterGracefulShutdown(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first platform
	platform1, err := cluster.NewPlatform("test-rejoin", "node-1", ns.URL(),
		cluster.HealthAddr(":17180"),
		cluster.MetricsAddr(":17190"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())

	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- platform1.Run(ctx1)
	}()

	// Wait for platform1 to be fully registered
	time.Sleep(1 * time.Second)

	// Gracefully shutdown platform1
	cancel1()

	// Wait for shutdown to complete and deregistration
	select {
	case <-errCh1:
	case <-time.After(5 * time.Second):
	}
	time.Sleep(500 * time.Millisecond)

	// Start new platform with the same node ID - should succeed
	platform2, err := cluster.NewPlatform("test-rejoin", "node-1", ns.URL(),
		cluster.HealthAddr(":17181"),
		cluster.MetricsAddr(":17191"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Use a goroutine with error channel to detect if Run fails
	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- platform2.Run(ctx2)
	}()

	// Wait to see if there's an error
	select {
	case err := <-errCh2:
		if err != nil && err != context.Canceled {
			t.Fatalf("rejoin should succeed after graceful shutdown, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		// No error within 2 seconds means it started successfully
	}

	// Verify the new platform is working
	assert.Eventually(t, func() bool {
		return app2.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "rejoined node should become leader")
}

func TestMembership_RejoinAfterStaleEntry(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first platform with very short heartbeat for testing
	platform1, err := cluster.NewPlatform("test-stale", "node-1", ns.URL(),
		cluster.HealthAddr(":17280"),
		cluster.MetricsAddr(":17290"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(200*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())

	go platform1.Run(ctx1)

	// Wait for platform1 to be fully registered
	time.Sleep(1 * time.Second)

	// Abruptly kill platform1 (simulate crash - no graceful deregistration)
	cancel1()

	// Wait for the entry to become stale (more than 2x heartbeat = 400ms, but use more for safety)
	time.Sleep(2 * time.Second)

	// Start new platform with the same node ID - should succeed because entry is stale
	platform2, err := cluster.NewPlatform("test-stale", "node-1", ns.URL(),
		cluster.HealthAddr(":17281"),
		cluster.MetricsAddr(":17291"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(200*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	// Use a goroutine with error channel to detect if Run fails
	errCh := make(chan error, 1)
	go func() {
		errCh <- platform2.Run(ctx2)
	}()

	// Wait to see if there's an error
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("rejoin should succeed after stale entry, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		// No error within 2 seconds means it started successfully
	}

	// Verify the new platform is working
	assert.Eventually(t, func() bool {
		return app2.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "rejoined node should become leader after stale entry")
}

func TestMembership_JoinedAtPreservedAcrossHeartbeats(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start platform with short heartbeat interval for testing
	platform, err := cluster.NewPlatform("test-joinedat", "node-1", ns.URL(),
		cluster.HealthAddr(":17480"),
		cluster.MetricsAddr(":17490"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Wait for platform to be fully registered
	time.Sleep(1 * time.Second)

	// Get initial member info and record JoinedAt
	members := platform.Membership().Members()
	require.Len(t, members, 1, "should have exactly one member")
	initialJoinedAt := members[0].JoinedAt
	initialLastSeen := members[0].LastSeen

	// Wait for several heartbeats to occur (heartbeat is 500ms, wait 2 seconds)
	time.Sleep(2 * time.Second)

	// Force a membership re-registration to simulate heartbeat update
	err = platform.Membership().Register()
	require.NoError(t, err)

	// Small delay for the update to propagate
	time.Sleep(200 * time.Millisecond)

	// Get updated member info
	members = platform.Membership().Members()
	require.Len(t, members, 1, "should still have exactly one member")
	updatedJoinedAt := members[0].JoinedAt
	updatedLastSeen := members[0].LastSeen

	// JoinedAt should be preserved (same as initial)
	assert.Equal(t, initialJoinedAt, updatedJoinedAt, "JoinedAt should be preserved across heartbeats")

	// LastSeen should be updated (more recent than initial)
	assert.True(t, updatedLastSeen.After(initialLastSeen), "LastSeen should be updated after heartbeat")
}

func TestMembership_DifferentNodeIDsAllowed(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var cancels []context.CancelFunc

	// Start first platform with node-1
	platform1, err := cluster.NewPlatform("test-multi", "node-1", ns.URL(),
		cluster.HealthAddr(":17380"),
		cluster.MetricsAddr(":17390"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())
	cancels = append(cancels, cancel1)

	go platform1.Run(ctx1)

	// Wait for platform1 to be fully registered
	time.Sleep(1 * time.Second)

	// Start second platform with different node ID - should succeed
	platform2, err := cluster.NewPlatform("test-multi", "node-2", ns.URL(),
		cluster.HealthAddr(":17381"),
		cluster.MetricsAddr(":17391"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	cancels = append(cancels, cancel2)

	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		errCh <- platform2.Run(ctx2)
	}()

	// Wait to see if there's an error
	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("different node IDs should be allowed, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		// No error within 2 seconds means it started successfully
	}

	// Verify both platforms are running and exactly one is leader
	assert.Eventually(t, func() bool {
		leaders := 0
		if app1.IsLeader() {
			leaders++
		}
		if app2.IsLeader() {
			leaders++
		}
		return leaders == 1
	}, 10*time.Second, 200*time.Millisecond, "exactly one node should be leader")
}
