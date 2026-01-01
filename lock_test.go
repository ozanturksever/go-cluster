package cluster_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupLockTest(t *testing.T) (*cluster.Platform, *cluster.App, func()) {
	t.Helper()

	ns := testutil.StartNATS(t)

	platform, err := cluster.NewPlatform("locktest", "node-1", ns.URL(),
		cluster.HealthAddr(":48080"),
		cluster.MetricsAddr(":49090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("lockapp", cluster.Spread())
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	go platform.Run(ctx)

	// Give platform time to start
	time.Sleep(300 * time.Millisecond)

	return platform, app, func() {
		cancel()
		ns.Stop()
	}
}

func TestLock_AcquireAndRelease(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("test-lock")

	// Initially not held
	assert.False(t, lock.IsHeld())

	// Acquire lock
	err := lock.Acquire(ctx)
	require.NoError(t, err)
	assert.True(t, lock.IsHeld())

	// Release lock
	err = lock.Release(ctx)
	require.NoError(t, err)
	assert.False(t, lock.IsHeld())
}

func TestLock_AcquireWithTTL(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("ttl-lock")

	// Acquire with custom TTL
	err := lock.AcquireWithTTL(ctx, 5*time.Second)
	require.NoError(t, err)
	assert.True(t, lock.IsHeld())

	// Release
	err = lock.Release(ctx)
	require.NoError(t, err)
}

func TestLock_DoubleAcquire(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("double-lock")

	// First acquire should succeed
	err := lock.Acquire(ctx)
	require.NoError(t, err)

	// Second acquire on same lock instance should fail
	err = lock.Acquire(ctx)
	assert.Equal(t, cluster.ErrLockHeld, err)

	// Cleanup
	lock.Release(ctx)
}

func TestLock_ReleaseNotHeld(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("not-held-lock")

	// Release without acquiring should fail
	err := lock.Release(ctx)
	assert.Equal(t, cluster.ErrLockNotHeld, err)
}

func TestLock_TryAcquire(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("try-lock")

	// TryAcquire should succeed on first call
	success := lock.TryAcquire(ctx)
	assert.True(t, success)
	assert.True(t, lock.IsHeld())

	// Cleanup
	lock.Release(ctx)
}

func TestLock_Do(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("do-lock")

	executed := false

	// Do should acquire, execute, and release
	err := lock.Do(ctx, func(ctx context.Context) error {
		executed = true
		assert.True(t, lock.IsHeld())
		return nil
	})

	require.NoError(t, err)
	assert.True(t, executed)
	assert.False(t, lock.IsHeld()) // Should be released after Do
}

func TestLock_DoWithTTL(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("do-ttl-lock")

	executed := false

	err := lock.DoWithTTL(ctx, 10*time.Second, func(ctx context.Context) error {
		executed = true
		return nil
	})

	require.NoError(t, err)
	assert.True(t, executed)
	assert.False(t, lock.IsHeld())
}

func TestLock_Contention(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Create two platforms/apps sharing the same NATS
	platform1, err := cluster.NewPlatform("locktest2", "node-1", ns.URL(),
		cluster.HealthAddr(":58080"),
		cluster.MetricsAddr(":59090"),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("locktest2", "node-2", ns.URL(),
		cluster.HealthAddr(":58081"),
		cluster.MetricsAddr(":59091"),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("lockapp", cluster.Spread())
	app2 := cluster.NewApp("lockapp", cluster.Spread())

	platform1.Register(app1)
	platform2.Register(app2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform1.Run(ctx)
	go platform2.Run(ctx)

	time.Sleep(300 * time.Millisecond)

	// Both try to acquire the same lock
	lock1 := app1.Lock("shared-lock")
	lock2 := app2.Lock("shared-lock")

	// First one should succeed
	err = lock1.Acquire(ctx)
	require.NoError(t, err)

	// Second one should fail (lock is held)
	err = lock2.Acquire(ctx)
	assert.Error(t, err)

	// Release first lock
	lock1.Release(ctx)

	// Now second should be able to acquire
	err = lock2.Acquire(ctx)
	require.NoError(t, err)

	lock2.Release(ctx)
}

func TestLock_Renew(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("renew-lock")

	// Acquire with short TTL
	err := lock.AcquireWithTTL(ctx, 2*time.Second)
	require.NoError(t, err)

	// Renew should succeed
	err = lock.Renew(ctx, 5*time.Second)
	require.NoError(t, err)

	// Still held
	assert.True(t, lock.IsHeld())

	lock.Release(ctx)
}

func TestLock_RenewNotHeld(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()
	lock := app.Lock("renew-not-held")

	// Renew without holding should fail
	err := lock.Renew(ctx, 5*time.Second)
	assert.Equal(t, cluster.ErrLockNotHeld, err)
}

func TestLock_ConcurrentDo(t *testing.T) {
	_, app, cleanup := setupLockTest(t)
	defer cleanup()

	ctx := context.Background()

	var counter int32

	// Run multiple goroutines trying to increment counter with lock
	done := make(chan bool, 5)
	for i := 0; i < 5; i++ {
		go func() {
			lock := app.Lock("concurrent-lock")
			// Retry until we acquire the lock (distributed lock pattern)
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				err := lock.Do(ctx, func(ctx context.Context) error {
					// Read-modify-write should be safe under lock
					val := atomic.LoadInt32(&counter)
					time.Sleep(10 * time.Millisecond) // Simulate work
					atomic.StoreInt32(&counter, val+1)
					return nil
				})
				if err == nil {
					break
				}
				// Lock is held by another goroutine, retry after short delay
				time.Sleep(5 * time.Millisecond)
			}
			done <- true
		}()
	}

	// Wait for all to complete
	for i := 0; i < 5; i++ {
		<-done
	}

	// Counter should be exactly 5 if locks worked correctly
	assert.Equal(t, int32(5), atomic.LoadInt32(&counter))
}
