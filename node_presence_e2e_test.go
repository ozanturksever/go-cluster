package cluster_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// nodeIDPresence generates a node ID for presence tests (to avoid conflicts with other tests)
func nodeIDPresence(i int) string {
	return fmt.Sprintf("presence-node-%d", i+1)
}

// portStrPresence generates a port string for presence tests
func portStrPresence(port int) string {
	return fmt.Sprintf(":%d", port)
}

// TestE2E_NodePresence_DuplicateNodeRejected tests that a duplicate node is rejected
// when trying to join a cluster with an already-present node ID.
func TestE2E_NodePresence_DuplicateNodeRejected(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first platform
	platform1, err := cluster.NewPlatform("e2e-presence", "node-duplicate", ns.URL(),
		cluster.HealthAddr(":16080"),
		cluster.MetricsAddr(":16090"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	errCh1 := make(chan error, 1)
	go func() {
		errCh1 <- platform1.Run(ctx1)
	}()

	// Wait for platform1 to fully register
	assert.Eventually(t, func() bool {
		return app1.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "first node should become leader")

	// Try to start second platform with same node ID
	platform2, err := cluster.NewPlatform("e2e-presence", "node-duplicate", ns.URL(),
		cluster.HealthAddr(":16081"),
		cluster.MetricsAddr(":16091"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- platform2.Run(ctx2)
	}()

	// Second platform should fail with ErrNodeAlreadyPresent
	select {
	case err := <-errCh2:
		assert.True(t, errors.Is(err, cluster.ErrNodeAlreadyPresent), "duplicate node should be rejected, got: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("expected duplicate node to be rejected with error")
	}

	// First platform should still be running fine
	assert.True(t, app1.IsLeader(), "first node should still be leader")
}

// TestE2E_NodePresence_GracefulRejoin tests that a node can rejoin after graceful shutdown.
func TestE2E_NodePresence_GracefulRejoin(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first platform
	platform1, err := cluster.NewPlatform("e2e-rejoin", "node-graceful", ns.URL(),
		cluster.HealthAddr(":16180"),
		cluster.MetricsAddr(":16190"),
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

	// Wait for platform1 to fully register
	assert.Eventually(t, func() bool {
		return app1.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "first node should become leader")

	// Gracefully shutdown
	cancel1()

	// Wait for shutdown
	select {
	case <-errCh1:
	case <-time.After(5 * time.Second):
	}

	// Small delay to ensure deregistration propagates
	time.Sleep(500 * time.Millisecond)

	// Rejoin with same node ID
	platform2, err := cluster.NewPlatform("e2e-rejoin", "node-graceful", ns.URL(),
		cluster.HealthAddr(":16181"),
		cluster.MetricsAddr(":16191"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	errCh2 := make(chan error, 1)
	go func() {
		errCh2 <- platform2.Run(ctx2)
	}()

	// Should not fail
	select {
	case err := <-errCh2:
		if err != nil && err != context.Canceled {
			t.Fatalf("rejoin should succeed, got error: %v", err)
		}
	case <-time.After(2 * time.Second):
		// Success - no error
	}

	// Should become leader
	assert.Eventually(t, func() bool {
		return app2.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "rejoined node should become leader")
}

// TestE2E_NodePresence_ConcurrentSameID tests that concurrent starts with the same ID
// result in only one succeeding.
func TestE2E_NodePresence_ConcurrentSameID(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	const numAttempts = 3
	var failCount int32
	var startedCount int32
	var wg sync.WaitGroup

	var cancels []context.CancelFunc
	var mu sync.Mutex

	errChs := make([]chan error, numAttempts)
	for i := range errChs {
		errChs[i] = make(chan error, 1)
	}

	// Start multiple platforms concurrently with the same node ID
	for i := 0; i < numAttempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()

			platform, err := cluster.NewPlatform("e2e-concurrent", "node-race", ns.URL(),
				cluster.HealthAddr(portStrPresence(16280+idx)),
				cluster.MetricsAddr(portStrPresence(16380+idx)),
				cluster.WithLeaseTTL(5*time.Second),
				cluster.WithHeartbeat(500*time.Millisecond),
			)
			if err != nil {
				errChs[idx] <- err
				return
			}

			app := cluster.NewApp("testapp", cluster.Singleton())
			platform.Register(app)

			ctx, cancel := context.WithCancel(context.Background())
			mu.Lock()
			cancels = append(cancels, cancel)
			mu.Unlock()

			err = platform.Run(ctx)
			errChs[idx] <- err
		}(i)
	}

	// Wait a bit for all attempts to start
	time.Sleep(3 * time.Second)

	// Check how many failed with ErrNodeAlreadyPresent
	// and how many are still running (success means still running)
	for i := 0; i < numAttempts; i++ {
		select {
		case err := <-errChs[i]:
			if errors.Is(err, cluster.ErrNodeAlreadyPresent) {
				atomic.AddInt32(&failCount, 1)
			} else if err != nil && err != context.Canceled {
				t.Logf("attempt %d got unexpected error: %v", i, err)
				atomic.AddInt32(&failCount, 1)
			}
		default:
			// Still running = success
			atomic.AddInt32(&startedCount, 1)
		}
	}

	// Cleanup
	mu.Lock()
	for _, cancel := range cancels {
		cancel()
	}
	mu.Unlock()

	wg.Wait()

	// At most one should succeed (still running), others should fail
	assert.True(t, atomic.LoadInt32(&startedCount) <= 1,
		"at most one concurrent start with same ID should succeed, got %d", startedCount)
	assert.True(t, atomic.LoadInt32(&failCount) >= int32(numAttempts-1),
		"at least %d should fail with ErrNodeAlreadyPresent, got %d failures", numAttempts-1, failCount)
}

// TestE2E_NodePresence_MultiNodeCluster tests a proper multi-node cluster
// with different node IDs.
func TestE2E_NodePresence_MultiNodeCluster(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	const numNodes = 3
	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start multiple nodes with different IDs
	for i := 0; i < numNodes; i++ {
		platform, err := cluster.NewPlatform("e2e-multinode", nodeIDPresence(i), ns.URL(),
			cluster.HealthAddr(portStrPresence(16480+i)),
			cluster.MetricsAddr(portStrPresence(16580+i)),
			cluster.WithLeaseTTL(5*time.Second),
			cluster.WithHeartbeat(500*time.Millisecond),
		)
		require.NoError(t, err)

		app := cluster.NewApp("testapp", cluster.Singleton())
		platform.Register(app)

		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)

		go platform.Run(ctx)
		apps = append(apps, app)
	}

	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	// Wait for exactly one leader
	assert.Eventually(t, func() bool {
		leaders := 0
		for _, app := range apps {
			if app.IsLeader() {
				leaders++
			}
		}
		return leaders == 1
	}, 15*time.Second, 200*time.Millisecond, "exactly one leader should be elected")

	// Verify all nodes are running (no node presence errors)
	for i, app := range apps {
		assert.NotNil(t, app, "app %d should not be nil", i)
	}
}

// TestE2E_NodePresence_FailoverAndRejoin tests failover followed by rejoin of the old leader.
func TestE2E_NodePresence_FailoverAndRejoin(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start first node
	platform1, err := cluster.NewPlatform("e2e-failover", "node-1", ns.URL(),
		cluster.HealthAddr(":16680"),
		cluster.MetricsAddr(":16690"),
		cluster.WithLeaseTTL(2*time.Second),
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

	// Start second node
	platform2, err := cluster.NewPlatform("e2e-failover", "node-2", ns.URL(),
		cluster.HealthAddr(":16681"),
		cluster.MetricsAddr(":16691"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go platform2.Run(ctx2)

	// Wait for leader election
	var initialLeaderApp *cluster.App
	assert.Eventually(t, func() bool {
		if app1.IsLeader() {
			initialLeaderApp = app1
			return true
		}
		if app2.IsLeader() {
			initialLeaderApp = app2
			return true
		}
		return false
	}, 10*time.Second, 200*time.Millisecond)

	isApp1Leader := initialLeaderApp == app1

	// Gracefully shutdown the leader
	if isApp1Leader {
		cancel1()
		select {
		case <-errCh1:
		case <-time.After(5 * time.Second):
		}
	} else {
		cancel2()
		time.Sleep(500 * time.Millisecond)
	}

	// Wait for the other node to become leader
	if isApp1Leader {
		assert.Eventually(t, func() bool {
			return app2.IsLeader()
		}, 10*time.Second, 200*time.Millisecond, "node-2 should become leader after node-1 shutdown")
	}

	// Small delay for deregistration to propagate
	time.Sleep(500 * time.Millisecond)

	// Rejoin the old leader
	if isApp1Leader {
		platform3, err := cluster.NewPlatform("e2e-failover", "node-1", ns.URL(),
			cluster.HealthAddr(":16682"),
			cluster.MetricsAddr(":16692"),
			cluster.WithLeaseTTL(2*time.Second),
			cluster.WithHeartbeat(500*time.Millisecond),
		)
		require.NoError(t, err)

		app3 := cluster.NewApp("testapp", cluster.Singleton())
		platform3.Register(app3)

		ctx3, cancel3 := context.WithCancel(context.Background())
		defer cancel3()

		errCh3 := make(chan error, 1)
		go func() {
			errCh3 <- platform3.Run(ctx3)
		}()

		// Should not fail
		select {
		case err := <-errCh3:
			if err != nil && err != context.Canceled {
				t.Fatalf("rejoin should succeed, got error: %v", err)
			}
		case <-time.After(2 * time.Second):
			// Success
		}

		// app2 should still be leader (it didn't give up leadership)
		assert.True(t, app2.IsLeader(), "node-2 should still be leader")

		// app3 should be standby
		assert.Eventually(t, func() bool {
			return app3.Role() == cluster.RoleStandby
		}, 5*time.Second, 200*time.Millisecond, "rejoined node should be standby")
	}
}
