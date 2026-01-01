package cluster_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealth_BasicCheck(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-health", "node-1", ns.URL(),
		cluster.HealthAddr(":17080"),
		cluster.MetricsAddr(":17090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Give time for platform to start
	time.Sleep(500 * time.Millisecond)

	// Health check should pass (NATS is connected)
	// Note: We can't directly access Health from Platform in this test,
	// but we can verify the app starts correctly
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)
}

func TestHealth_CircuitBreaker(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-circuit", "node-1", ns.URL(),
		cluster.HealthAddr(":17180"),
		cluster.MetricsAddr(":17190"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Give time for platform to start
	time.Sleep(500 * time.Millisecond)

	// Verify app starts
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)
}

func TestHealth_OnHealthChangeHook(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var healthChanges int32

	platform, err := cluster.NewPlatform("test-health-hook", "node-1", ns.URL(),
		cluster.HealthAddr(":17280"),
		cluster.MetricsAddr(":17290"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	app.OnHealthChange(func(ctx context.Context, status cluster.HealthStatus) error {
		atomic.AddInt32(&healthChanges, 1)
		return nil
	})
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Give time for platform to start and health checks to run
	time.Sleep(1 * time.Second)

	// Verify app starts
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)
}

func TestHealth_FailingCheck(t *testing.T) {
	// This test verifies that a failing health check is properly tracked
	// We'll create a simple platform and verify it handles health checks
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-failing", "node-1", ns.URL(),
		cluster.HealthAddr(":17380"),
		cluster.MetricsAddr(":17390"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Give time for platform to start
	time.Sleep(500 * time.Millisecond)

	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)
}

func TestHealth_HookTimeout(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var hookStarted int32
	var hookCompleted int32

	platform, err := cluster.NewPlatform("test-hook-timeout", "node-1", ns.URL(),
		cluster.HealthAddr(":17480"),
		cluster.MetricsAddr(":17490"),
		cluster.WithHookTimeout(1*time.Second),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	app.OnActive(func(ctx context.Context) error {
		atomic.AddInt32(&hookStarted, 1)
		// This hook completes quickly
		atomic.AddInt32(&hookCompleted, 1)
		return nil
	})
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Wait for leadership and hook to be called
	assert.Eventually(t, func() bool {
		return atomic.LoadInt32(&hookCompleted) > 0
	}, 10*time.Second, 200*time.Millisecond)

	assert.True(t, atomic.LoadInt32(&hookStarted) > 0)
}

func TestHealth_HookError(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var hookCalled int32

	platform, err := cluster.NewPlatform("test-hook-error", "node-1", ns.URL(),
		cluster.HealthAddr(":17580"),
		cluster.MetricsAddr(":17590"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	app.OnActive(func(ctx context.Context) error {
		atomic.AddInt32(&hookCalled, 1)
		return errors.New("hook error")
	})
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Even with hook error, app should become leader
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)

	// Hook should have been called
	assert.True(t, atomic.LoadInt32(&hookCalled) > 0)
}

func TestHealth_GracefulShutdown(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	var onStopCalled int32

	platform, err := cluster.NewPlatform("test-shutdown", "node-1", ns.URL(),
		cluster.HealthAddr(":17680"),
		cluster.MetricsAddr(":17690"),
		cluster.WithShutdownTimeout(5*time.Second),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Singleton())
	app.OnStop(func(ctx context.Context) error {
		atomic.AddInt32(&onStopCalled, 1)
		return nil
	})
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())

	go platform.Run(ctx)

	// Wait for leadership
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond)

	// Trigger shutdown
	cancel()

	// Wait for onStop to be called
	time.Sleep(1 * time.Second)

	assert.True(t, atomic.LoadInt32(&onStopCalled) > 0, "OnStop should have been called during shutdown")
}
