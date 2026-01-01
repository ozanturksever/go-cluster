package cluster_test

import (
	"context"
	"fmt"
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
