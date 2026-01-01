package cluster_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestElection_SingleNode(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test1", "node-1", ns.URL(),
		cluster.HealthAddr(":18080"),
		cluster.MetricsAddr(":19090"),
		cluster.WithLeaseTTL(5*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Give some time for the platform to start
	time.Sleep(500 * time.Millisecond)

	// Wait for leadership
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "node should become leader")
}

func TestElection_MultipleNodes(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start 3 nodes
	for i := 0; i < 3; i++ {
		platform, err := cluster.NewPlatform("test2", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(18180+i)),
			cluster.MetricsAddr(portStr(19190+i)),
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

	// Give some time for the platform to start
	time.Sleep(500 * time.Millisecond)

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
}

func TestElection_LeaderFailover(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start 3 nodes
	for i := 0; i < 3; i++ {
		platform, err := cluster.NewPlatform("test3", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(28080+i)),
			cluster.MetricsAddr(portStr(29090+i)),
			cluster.WithLeaseTTL(2*time.Second),
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

	// Give some time for the platform to start
	time.Sleep(500 * time.Millisecond)

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, app := range apps {
			if app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond, "a leader should be elected")

	// Kill the leader
	cancels[leaderIdx]()

	// Wait for a new leader (need to wait for TTL to expire)
	assert.Eventually(t, func() bool {
		leaders := 0
		for i, app := range apps {
			if i == leaderIdx {
				continue // Skip killed node
			}
			if app.IsLeader() {
				leaders++
			}
		}
		return leaders == 1
	}, 15*time.Second, 200*time.Millisecond, "a new leader should be elected after failover")
}

func nodeID(i int) string {
	return fmt.Sprintf("node-%d", i+1)
}

func portStr(port int) string {
	return fmt.Sprintf(":%d", port)
}

func TestElection_StepDown(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start 2 nodes
	for i := 0; i < 2; i++ {
		platform, err := cluster.NewPlatform("test-stepdown", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(38080+i)),
			cluster.MetricsAddr(portStr(39090+i)),
			cluster.WithLeaseTTL(2*time.Second),
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

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, app := range apps {
			if app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond, "a leader should be elected")

	// Step down the leader
	err := apps[leaderIdx].Election().StepDown(context.Background())
	require.NoError(t, err)

	// Verify leader stepped down
	assert.Eventually(t, func() bool {
		return !apps[leaderIdx].IsLeader()
	}, 5*time.Second, 100*time.Millisecond, "leader should have stepped down")

	// Wait for new leader (the other node)
	otherIdx := 1 - leaderIdx
	assert.Eventually(t, func() bool {
		return apps[otherIdx].IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "other node should become leader")
}

func TestElection_WaitForLeadership(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-wait", "node-1", ns.URL(),
		cluster.HealthAddr(":48080"),
		cluster.MetricsAddr(":49090"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Wait for the election to be initialized
	assert.Eventually(t, func() bool {
		return app.Election() != nil
	}, 5*time.Second, 100*time.Millisecond, "election should be initialized")

	// Wait for leadership with timeout
	err = app.Election().WaitForLeadershipWithTimeout(10 * time.Second)
	require.NoError(t, err)

	assert.True(t, app.IsLeader(), "should be leader after WaitForLeadership returns")
}

func TestElection_WaitForLeadershipTimeout(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Start a node that will be leader
	platform1, err := cluster.NewPlatform("test-wait-timeout", "node-1", ns.URL(),
		cluster.HealthAddr(":58080"),
		cluster.MetricsAddr(":59090"),
		cluster.WithLeaseTTL(30*time.Second), // Long TTL so node-1 stays leader
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp", cluster.Singleton())
	platform1.Register(app1)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()

	go platform1.Run(ctx1)

	// Wait for node-1 to become leader
	assert.Eventually(t, func() bool {
		return app1.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)

	// Start second node
	platform2, err := cluster.NewPlatform("test-wait-timeout", "node-2", ns.URL(),
		cluster.HealthAddr(":58081"),
		cluster.MetricsAddr(":59091"),
		cluster.WithLeaseTTL(30*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app2 := cluster.NewApp("testapp", cluster.Singleton())
	platform2.Register(app2)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go platform2.Run(ctx2)

	// Wait for the election to be initialized
	assert.Eventually(t, func() bool {
		return app2.Election() != nil
	}, 5*time.Second, 100*time.Millisecond, "election should be initialized")

	// Wait for leadership with very short timeout - should fail
	err = app2.Election().WaitForLeadershipWithTimeout(1 * time.Second)
	assert.Error(t, err, "should timeout waiting for leadership")
}

func TestElection_OnLeaderChangeHook(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var leaderChanges int32
	var lastLeader atomic.Value

	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start 2 nodes
	for i := 0; i < 2; i++ {
		platform, err := cluster.NewPlatform("test-hook", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(68080+i)),
			cluster.MetricsAddr(portStr(69090+i)),
			cluster.WithLeaseTTL(2*time.Second),
			cluster.WithHeartbeat(500*time.Millisecond),
		)
		require.NoError(t, err)

		app := cluster.NewApp("testapp", cluster.Singleton())
		app.OnLeaderChange(func(ctx context.Context, leaderID string) error {
			atomic.AddInt32(&leaderChanges, 1)
			lastLeader.Store(leaderID)
			return nil
		})
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

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, app := range apps {
			if app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond)

	// Step down the leader
	err := apps[leaderIdx].Election().StepDown(context.Background())
	require.NoError(t, err)

	// Wait for new leader
	otherIdx := 1 - leaderIdx
	assert.Eventually(t, func() bool {
		return apps[otherIdx].IsLeader()
	}, 10*time.Second, 200*time.Millisecond)

	// Give time for hooks to be called
	time.Sleep(500 * time.Millisecond)

	// Verify OnLeaderChange was called
	assert.True(t, atomic.LoadInt32(&leaderChanges) > 0, "OnLeaderChange should have been called")
}

func TestElection_EpochIncrement(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var apps []*cluster.App
	var cancels []context.CancelFunc

	// Start 2 nodes
	for i := 0; i < 2; i++ {
		platform, err := cluster.NewPlatform("test-epoch", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(78080+i)),
			cluster.MetricsAddr(portStr(79090+i)),
			cluster.WithLeaseTTL(2*time.Second),
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

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, app := range apps {
			if app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 10*time.Second, 200*time.Millisecond)

	// Record initial epoch
	initialEpoch := apps[leaderIdx].Election().Epoch()
	assert.True(t, initialEpoch > 0, "initial epoch should be > 0")

	// Step down
	err := apps[leaderIdx].Election().StepDown(context.Background())
	require.NoError(t, err)

	// Wait for new leader
	otherIdx := 1 - leaderIdx
	assert.Eventually(t, func() bool {
		return apps[otherIdx].IsLeader()
	}, 10*time.Second, 200*time.Millisecond)

	// Check epoch increased
	newEpoch := apps[otherIdx].Election().Epoch()
	assert.True(t, newEpoch > initialEpoch, "epoch should have increased after failover")
}

// TestElection_SplitBrainPrevention is an E2E test that verifies only one leader
// can exist at any time, even under various failure scenarios.
func TestElection_SplitBrainPrevention(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	const numNodes = 3
	var apps []*cluster.App
	var cancels []context.CancelFunc
	aliveNodes := make(map[int]bool)

	// Start multiple nodes with short lease TTL to speed up the test
	for i := 0; i < numNodes; i++ {
		platform, err := cluster.NewPlatform("test-splitbrain", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(88080+i)),
			cluster.MetricsAddr(portStr(89090+i)),
			cluster.WithLeaseTTL(2*time.Second),
			cluster.WithHeartbeat(500*time.Millisecond),
		)
		require.NoError(t, err)

		app := cluster.NewApp("testapp", cluster.Singleton())
		platform.Register(app)

		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		aliveNodes[i] = true

		go platform.Run(ctx)

		apps = append(apps, app)
	}

	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	// Helper to count leaders among alive nodes only
	countLeaders := func() int {
		count := 0
		for i, app := range apps {
			if aliveNodes[i] && app.IsLeader() {
				count++
			}
		}
		return count
	}

	// Helper to find leader index among alive nodes
	findLeader := func() int {
		for i, app := range apps {
			if aliveNodes[i] && app.IsLeader() {
				return i
			}
		}
		return -1
	}

	// Wait for initial leader election
	assert.Eventually(t, func() bool {
		return countLeaders() == 1
	}, 15*time.Second, 200*time.Millisecond, "should have exactly one leader")

	// Verify only one leader exists
	assert.Equal(t, 1, countLeaders(), "should have exactly one leader after initial election")

	// Track epochs to ensure monotonic increase
	initialLeaderIdx := findLeader()
	require.NotEqual(t, -1, initialLeaderIdx, "should have a leader")
	lastEpoch := apps[initialLeaderIdx].Election().Epoch()

	// Test 1: Multiple rapid step-downs should never result in split-brain
	t.Run("RapidStepDowns", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			leaderIdx := findLeader()
			if leaderIdx == -1 {
				// Wait for a leader
				assert.Eventually(t, func() bool {
					return countLeaders() == 1
				}, 10*time.Second, 100*time.Millisecond)
				continue
			}

			// Step down current leader
			err := apps[leaderIdx].Election().StepDown(context.Background())
			if err != nil && err != cluster.ErrNotLeader {
				t.Logf("Step down error (may be expected): %v", err)
			}

			// Wait for new leader
			assert.Eventually(t, func() bool {
				return countLeaders() == 1
			}, 10*time.Second, 100*time.Millisecond, "should have exactly one leader after step down %d", i)

			// Verify never more than one leader
			leaders := countLeaders()
			assert.LessOrEqual(t, leaders, 1, "should never have more than one leader")

			// Verify epoch increases
			newLeaderIdx := findLeader()
			if newLeaderIdx != -1 {
				newEpoch := apps[newLeaderIdx].Election().Epoch()
				assert.GreaterOrEqual(t, newEpoch, lastEpoch, "epoch should be monotonically increasing")
				lastEpoch = newEpoch
			}
		}
	})

	// Test 2: Verify epochs are tracked correctly across leadership changes
	t.Run("EpochTracking", func(t *testing.T) {
		leaderIdx := findLeader()
		require.NotEqual(t, -1, leaderIdx)

		initialEpoch := apps[leaderIdx].Election().Epoch()
		require.True(t, initialEpoch > 0, "epoch should be positive")

		// Step down
		err := apps[leaderIdx].Election().StepDown(context.Background())
		if err != nil && err != cluster.ErrNotLeader {
			t.Logf("Step down error: %v", err)
		}

		// Wait for new leader
		assert.Eventually(t, func() bool {
			return countLeaders() == 1
		}, 10*time.Second, 100*time.Millisecond)

		newLeaderIdx := findLeader()
		if newLeaderIdx != -1 {
			newEpoch := apps[newLeaderIdx].Election().Epoch()
			// Epoch should be at least equal (same node can re-acquire) or greater
			assert.GreaterOrEqual(t, newEpoch, initialEpoch, "epoch should be monotonically increasing")
		}
	})

	// Test 3: Kill the leader and verify exactly one new leader emerges
	t.Run("LeaderKillAndReelection", func(t *testing.T) {
		// Wait for stable state
		assert.Eventually(t, func() bool {
			return countLeaders() == 1
		}, 10*time.Second, 100*time.Millisecond)

		leaderIdx := findLeader()
		require.NotEqual(t, -1, leaderIdx)

		// Kill the leader by cancelling its context
		cancels[leaderIdx]()
		// Mark as not alive so we don't count it
		aliveNodes[leaderIdx] = false

		// Wait for lease to expire and new leader to be elected
		// Lease TTL is 2s, so wait a bit longer
		assert.Eventually(t, func() bool {
			return countLeaders() == 1
		}, 15*time.Second, 200*time.Millisecond, "should elect exactly one new leader after killing old leader")

		// Verify exactly one leader among remaining nodes
		assert.Equal(t, 1, countLeaders(), "should have exactly one leader among surviving nodes")
	})
}

// TestElection_ConcurrentCampaigns verifies that concurrent election attempts
// from multiple nodes result in exactly one leader.
func TestElection_ConcurrentCampaigns(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	const numNodes = 3
	var apps []*cluster.App
	var platforms []*cluster.Platform
	var cancels []context.CancelFunc

	// Create all platforms and apps, but don't start them yet
	for i := 0; i < numNodes; i++ {
		platform, err := cluster.NewPlatform("test-concurrent", nodeID(i), ns.URL(),
			cluster.HealthAddr(portStr(98080+i)),
			cluster.MetricsAddr(portStr(99090+i)),
			cluster.WithLeaseTTL(3*time.Second),
			cluster.WithHeartbeat(500*time.Millisecond),
		)
		require.NoError(t, err)

		app := cluster.NewApp("testapp", cluster.Singleton())
		platform.Register(app)

		platforms = append(platforms, platform)
		apps = append(apps, app)
	}

	defer func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	}()

	// Start all platforms simultaneously to create race condition
	for i := 0; i < numNodes; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		go platforms[i].Run(ctx)
	}

	// Wait for leader election to settle
	time.Sleep(5 * time.Second)

	// Count leaders
	leaderCount := 0
	for _, app := range apps {
		if app.IsLeader() {
			leaderCount++
		}
	}

	assert.Equal(t, 1, leaderCount, "concurrent campaigns should result in exactly one leader")
}
