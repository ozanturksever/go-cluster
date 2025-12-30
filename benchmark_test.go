package cluster

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	natsmodule "github.com/testcontainers/testcontainers-go/modules/nats"
)

// startBenchmarkNATSContainer starts a NATS container for benchmarks.
func startBenchmarkNATSContainer(ctx context.Context, b *testing.B) (string, func()) {
	b.Helper()

	container, err := natsmodule.Run(ctx, "nats:latest")
	if err != nil {
		b.Fatalf("failed to start NATS container: %v", err)
	}

	url, err := container.ConnectionString(ctx)
	if err != nil {
		container.Terminate(ctx)
		b.Fatalf("failed to get NATS connection string: %v", err)
	}

	cleanup := func() {
		if err := container.Terminate(ctx); err != nil {
			b.Logf("failed to terminate NATS container: %v", err)
		}
	}

	return url, cleanup
}

// BenchmarkSingleNodeLeadershipAcquisition measures the time for a single node
// to acquire leadership in an empty cluster.
func BenchmarkSingleNodeLeadershipAcquisition(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startBenchmarkNATSContainer(ctx, b)
	defer cleanup()

	b.ResetTimer()

	var totalAcquisitionTime time.Duration

	for i := 0; i < b.N; i++ {
		clusterID := fmt.Sprintf("bench-cluster-%d", i)

		cfg := Config{
			ClusterID:     clusterID,
			NodeID:        "node-1",
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}

		var becameLeader atomic.Bool
		var leaderTime time.Time

		hooks := &benchmarkHooks{
			onBecomeLeader: func(ctx context.Context) error {
				leaderTime = time.Now()
				becameLeader.Store(true)
				return nil
			},
		}

		node, err := NewNode(cfg, hooks)
		if err != nil {
			b.Fatalf("NewNode() error = %v", err)
		}

		startTime := time.Now()
		if err := node.Start(ctx); err != nil {
			b.Fatalf("Start() error = %v", err)
		}

		// Wait for leadership
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && !becameLeader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		if !becameLeader.Load() {
			b.Fatal("node did not become leader")
		}

		acquisitionTime := leaderTime.Sub(startTime)
		totalAcquisitionTime += acquisitionTime

		node.Stop(ctx)
	}

	avgAcquisitionMs := float64(totalAcquisitionTime.Milliseconds()) / float64(b.N)
	b.ReportMetric(avgAcquisitionMs, "ms/leadership-acquisition")
}

// BenchmarkTwoNodeFailover measures the RTO (Recovery Time Objective) when
// the leader crashes and a passive node takes over.
func BenchmarkTwoNodeFailover(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startBenchmarkNATSContainer(ctx, b)
	defer cleanup()

	b.ResetTimer()

	var totalFailoverTime time.Duration

	for i := 0; i < b.N; i++ {
		clusterID := fmt.Sprintf("failover-bench-%d", i)

		cfg1 := Config{
			ClusterID:     clusterID,
			NodeID:        "node-1",
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}

		cfg2 := Config{
			ClusterID:     clusterID,
			NodeID:        "node-2",
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}

		var node1Leader atomic.Bool
		var node2Leader atomic.Bool
		var node2LeaderTime time.Time

		hooks1 := &benchmarkHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node1Leader.Store(true)
				return nil
			},
		}

		hooks2 := &benchmarkHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node2LeaderTime = time.Now()
				node2Leader.Store(true)
				return nil
			},
		}

		node1, err := NewNode(cfg1, hooks1)
		if err != nil {
			b.Fatalf("NewNode(node1) error = %v", err)
		}
		node2, err := NewNode(cfg2, hooks2)
		if err != nil {
			b.Fatalf("NewNode(node2) error = %v", err)
		}

		// Start node1 first
		if err := node1.Start(ctx); err != nil {
			b.Fatalf("node1.Start() error = %v", err)
		}

		// Wait for node1 to become leader
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && !node1Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		// Start node2 (passive)
		node2.Start(ctx)
		time.Sleep(500 * time.Millisecond) // Let node2 connect

		// Record the time when leader fails
		leaderFailTime := time.Now()

		// Stop node1 (simulate crash)
		node1.Stop(ctx)

		// Wait for node2 to become leader
		deadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !node2Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		if !node2Leader.Load() {
			b.Fatal("node2 did not become leader after failover")
		}

		failoverTime := node2LeaderTime.Sub(leaderFailTime)
		totalFailoverTime += failoverTime

		node2.Stop(ctx)
	}

	avgFailoverMs := float64(totalFailoverTime.Milliseconds()) / float64(b.N)
	b.ReportMetric(avgFailoverMs, "ms/failover-RTO")
}

// BenchmarkGracefulStepdownFailover measures the RTO when the leader
// gracefully steps down and a passive node takes over.
// Note: This includes the renew interval wait time since node2 must
// detect the lease deletion on its next election tick.
func BenchmarkGracefulStepdownFailover(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startBenchmarkNATSContainer(ctx, b)
	defer cleanup()

	b.ResetTimer()

	var totalFailoverTime time.Duration

	for i := 0; i < b.N; i++ {
		clusterID := fmt.Sprintf("stepdown-bench-%d", i)

		cfg1 := Config{
			ClusterID:     clusterID,
			NodeID:        "node-1",
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}

		cfg2 := Config{
			ClusterID:     clusterID,
			NodeID:        "node-2",
			NATSURLs:      []string{natsURL},
			LeaseTTL:      3 * time.Second,
			RenewInterval: 1 * time.Second,
		}

		var node1Leader atomic.Bool
		var node2Leader atomic.Bool
		var node2LeaderTime time.Time

		hooks1 := &benchmarkHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node1Leader.Store(true)
				return nil
			},
		}

		hooks2 := &benchmarkHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node2LeaderTime = time.Now()
				node2Leader.Store(true)
				return nil
			},
		}

		node1, err := NewNode(cfg1, hooks1)
		if err != nil {
			b.Fatalf("NewNode(node1) error = %v", err)
		}
		node2, err := NewNode(cfg2, hooks2)
		if err != nil {
			b.Fatalf("NewNode(node2) error = %v", err)
		}

		// Start both nodes
		if err := node1.Start(ctx); err != nil {
			b.Fatalf("node1.Start() error = %v", err)
		}

		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && !node1Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		node2.Start(ctx)
		time.Sleep(500 * time.Millisecond)

		// Record time and step down
		stepdownTime := time.Now()
		node1.StepDown(ctx)

		// Wait for node2 to become leader
		deadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !node2Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		if !node2Leader.Load() {
			b.Fatal("node2 did not become leader after stepdown")
		}

		failoverTime := node2LeaderTime.Sub(stepdownTime)
		totalFailoverTime += failoverTime

		node1.Stop(ctx)
		node2.Stop(ctx)
	}

	avgFailoverMs := float64(totalFailoverTime.Milliseconds()) / float64(b.N)
	b.ReportMetric(avgFailoverMs, "ms/graceful-failover-RTO")
}

// BenchmarkFailoverByLeaseTTL measures failover RTO with different lease TTL configurations.
func BenchmarkFailoverByLeaseTTL(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ttlConfigs := []struct {
		name          string
		leaseTTL      time.Duration
		renewInterval time.Duration
	}{
		{"TTL-1s", 1 * time.Second, 300 * time.Millisecond},
		{"TTL-3s", 3 * time.Second, 1 * time.Second},
		{"TTL-5s", 5 * time.Second, 1500 * time.Millisecond},
		{"TTL-10s", 10 * time.Second, 3 * time.Second},
	}

	for _, tc := range ttlConfigs {
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			natsURL, cleanup := startBenchmarkNATSContainer(ctx, b)
			defer cleanup()

			b.ResetTimer()

			var totalFailoverTime time.Duration

			for i := 0; i < b.N; i++ {
				clusterID := fmt.Sprintf("ttl-bench-%s-%d", tc.name, i)

				cfg1 := Config{
					ClusterID:     clusterID,
					NodeID:        "node-1",
					NATSURLs:      []string{natsURL},
					LeaseTTL:      tc.leaseTTL,
					RenewInterval: tc.renewInterval,
				}

				cfg2 := Config{
					ClusterID:     clusterID,
					NodeID:        "node-2",
					NATSURLs:      []string{natsURL},
					LeaseTTL:      tc.leaseTTL,
					RenewInterval: tc.renewInterval,
				}

				var node1Leader atomic.Bool
				var node2Leader atomic.Bool
				var node2LeaderTime time.Time

				hooks1 := &benchmarkHooks{
					onBecomeLeader: func(ctx context.Context) error {
						node1Leader.Store(true)
						return nil
					},
				}

				hooks2 := &benchmarkHooks{
					onBecomeLeader: func(ctx context.Context) error {
						node2LeaderTime = time.Now()
						node2Leader.Store(true)
						return nil
					},
				}

			node1, err := NewNode(cfg1, hooks1)
			if err != nil {
				b.Fatalf("NewNode(node1) error = %v", err)
			}
			node2, err := NewNode(cfg2, hooks2)
			if err != nil {
				b.Fatalf("NewNode(node2) error = %v", err)
			}

			if err := node1.Start(ctx); err != nil {
				b.Fatalf("node1.Start() error = %v", err)
			}

				deadline := time.Now().Add(15 * time.Second)
				for time.Now().Before(deadline) && !node1Leader.Load() {
					time.Sleep(10 * time.Millisecond)
				}

				node2.Start(ctx)
				time.Sleep(500 * time.Millisecond)

				leaderFailTime := time.Now()
				node1.Stop(ctx)

				deadline = time.Now().Add(tc.leaseTTL * 3)
				for time.Now().Before(deadline) && !node2Leader.Load() {
					time.Sleep(10 * time.Millisecond)
				}

				if !node2Leader.Load() {
					b.Fatalf("node2 did not become leader with TTL=%v", tc.leaseTTL)
				}

				failoverTime := node2LeaderTime.Sub(leaderFailTime)
				totalFailoverTime += failoverTime

				node2.Stop(ctx)
			}

			avgFailoverMs := float64(totalFailoverTime.Milliseconds()) / float64(b.N)
			b.ReportMetric(avgFailoverMs, "ms/failover-RTO")
			b.ReportMetric(float64(tc.leaseTTL.Milliseconds()), "ms/lease-TTL")
		})
	}
}

// BenchmarkManagerFailover measures RTO using the Manager API.
func BenchmarkManagerFailover(b *testing.B) {
	if testing.Short() {
		b.Skip("skipping benchmark in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startBenchmarkNATSContainer(ctx, b)
	defer cleanup()

	b.ResetTimer()

	var totalFailoverTime time.Duration

	for i := 0; i < b.N; i++ {
		clusterID := fmt.Sprintf("mgr-bench-%d", i)

		cfg1 := NewDefaultFileConfig(clusterID, "node-1", []string{natsURL})
		cfg1.Election.LeaseTTLMs = 3000
		cfg1.Election.RenewIntervalMs = 1000

		cfg2 := NewDefaultFileConfig(clusterID, "node-2", []string{natsURL})
		cfg2.Election.LeaseTTLMs = 3000
		cfg2.Election.RenewIntervalMs = 1000

		var node1Leader atomic.Bool
		var node2Leader atomic.Bool
		var node2LeaderTime time.Time

		hooks1 := &benchmarkManagerHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node1Leader.Store(true)
				return nil
			},
		}

		hooks2 := &benchmarkManagerHooks{
			onBecomeLeader: func(ctx context.Context) error {
				node2LeaderTime = time.Now()
				node2Leader.Store(true)
				return nil
			},
		}

		mgr1, _ := NewManager(*cfg1, hooks1)
		mgr2, _ := NewManager(*cfg2, hooks2)

		ctx1, cancel1 := context.WithCancel(ctx)
		ctx2, cancel2 := context.WithCancel(ctx)

		go mgr1.RunDaemon(ctx1)

		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) && !node1Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		go mgr2.RunDaemon(ctx2)
		time.Sleep(500 * time.Millisecond)

		leaderFailTime := time.Now()
		cancel1() // Stop manager1

		deadline = time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) && !node2Leader.Load() {
			time.Sleep(10 * time.Millisecond)
		}

		if !node2Leader.Load() {
			b.Fatal("manager2 did not become leader")
		}

		failoverTime := node2LeaderTime.Sub(leaderFailTime)
		totalFailoverTime += failoverTime

		cancel2()
		time.Sleep(100 * time.Millisecond) // Allow cleanup
	}

	avgFailoverMs := float64(totalFailoverTime.Milliseconds()) / float64(b.N)
	b.ReportMetric(avgFailoverMs, "ms/manager-failover-RTO")
}

// benchmarkHooks implements Hooks for benchmarks.
type benchmarkHooks struct {
	onBecomeLeader   func(ctx context.Context) error
	onLoseLeadership func(ctx context.Context) error
	onLeaderChange   func(ctx context.Context, nodeID string) error
}

func (h *benchmarkHooks) OnBecomeLeader(ctx context.Context) error {
	if h.onBecomeLeader != nil {
		return h.onBecomeLeader(ctx)
	}
	return nil
}

func (h *benchmarkHooks) OnLoseLeadership(ctx context.Context) error {
	if h.onLoseLeadership != nil {
		return h.onLoseLeadership(ctx)
	}
	return nil
}

func (h *benchmarkHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	if h.onLeaderChange != nil {
		return h.onLeaderChange(ctx, nodeID)
	}
	return nil
}

var _ Hooks = (*benchmarkHooks)(nil)

// benchmarkManagerHooks implements ManagerHooks for benchmarks.
type benchmarkManagerHooks struct {
	onBecomeLeader   func(ctx context.Context) error
	onLoseLeadership func(ctx context.Context) error
	onLeaderChange   func(ctx context.Context, nodeID string) error
	onDaemonStart    func(ctx context.Context) error
	onDaemonStop     func(ctx context.Context) error
}

func (h *benchmarkManagerHooks) OnBecomeLeader(ctx context.Context) error {
	if h.onBecomeLeader != nil {
		return h.onBecomeLeader(ctx)
	}
	return nil
}

func (h *benchmarkManagerHooks) OnLoseLeadership(ctx context.Context) error {
	if h.onLoseLeadership != nil {
		return h.onLoseLeadership(ctx)
	}
	return nil
}

func (h *benchmarkManagerHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	if h.onLeaderChange != nil {
		return h.onLeaderChange(ctx, nodeID)
	}
	return nil
}

func (h *benchmarkManagerHooks) OnDaemonStart(ctx context.Context) error {
	if h.onDaemonStart != nil {
		return h.onDaemonStart(ctx)
	}
	return nil
}

func (h *benchmarkManagerHooks) OnDaemonStop(ctx context.Context) error {
	if h.onDaemonStop != nil {
		return h.onDaemonStop(ctx)
	}
	return nil
}

var _ ManagerHooks = (*benchmarkManagerHooks)(nil)
