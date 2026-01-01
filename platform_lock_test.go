package cluster_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/require"
)

func TestPlatformLock_AcquireRelease(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock", "node-1", ns.URL(),
		cluster.HealthAddr(":38380"),
		cluster.MetricsAddr(":39390"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock := platform.Lock("test-resource")

	// Acquire lock
	err = lock.Acquire(ctx)
	require.NoError(t, err)
	require.True(t, lock.IsHeld())

	// Release lock
	err = lock.Release(ctx)
	require.NoError(t, err)
	require.False(t, lock.IsHeld())
}

func TestPlatformLock_MutualExclusion(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-mutex", "node-1", ns.URL(),
		cluster.HealthAddr(":38381"),
		cluster.MetricsAddr(":39391"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock1 := platform.Lock("shared-resource")
	lock2 := platform.Lock("shared-resource")

	// First lock acquires
	err = lock1.AcquireWithTTL(ctx, 5*time.Second)
	require.NoError(t, err)

	// Second lock should fail
	err = lock2.AcquireWithTTL(ctx, 5*time.Second)
	require.Error(t, err)
	require.Equal(t, cluster.ErrLockHeld, err)

	// Release first lock
	lock1.Release(ctx)

	// Now second lock should succeed
	err = lock2.AcquireWithTTL(ctx, 5*time.Second)
	require.NoError(t, err)
	lock2.Release(ctx)
}

func TestPlatformLock_TryAcquire(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-try", "node-1", ns.URL(),
		cluster.HealthAddr(":38382"),
		cluster.MetricsAddr(":39392"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock1 := platform.Lock("try-resource")
	lock2 := platform.Lock("try-resource")

	// First try should succeed
	require.True(t, lock1.TryAcquire(ctx))

	// Second try should fail
	require.False(t, lock2.TryAcquire(ctx))

	lock1.Release(ctx)
}

func TestPlatformLock_Do(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-do", "node-1", ns.URL(),
		cluster.HealthAddr(":38383"),
		cluster.MetricsAddr(":39393"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock := platform.Lock("do-resource")

	var executed atomic.Bool
	err = lock.Do(ctx, func(ctx context.Context) error {
		executed.Store(true)
		require.True(t, lock.IsHeld())
		return nil
	})
	require.NoError(t, err)
	require.True(t, executed.Load())
	require.False(t, lock.IsHeld()) // Should be released after Do
}

func TestPlatformLock_Renew(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-renew", "node-1", ns.URL(),
		cluster.HealthAddr(":38384"),
		cluster.MetricsAddr(":39394"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock := platform.Lock("renew-resource")

	err = lock.AcquireWithTTL(ctx, 2*time.Second)
	require.NoError(t, err)

	// Renew the lock
	err = lock.Renew(ctx, 5*time.Second)
	require.NoError(t, err)

	require.True(t, lock.IsHeld())
	lock.Release(ctx)
}

func TestPlatformLock_Holder(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-holder", "node-1", ns.URL(),
		cluster.HealthAddr(":38385"),
		cluster.MetricsAddr(":39395"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock := platform.Lock("holder-resource")

	err = lock.Acquire(ctx)
	require.NoError(t, err)

	holder, err := lock.Holder(ctx)
	require.NoError(t, err)
	require.Equal(t, "node-1", holder.NodeID)

	lock.Release(ctx)
}

func TestPlatformLock_ConcurrentAccess(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-concurrent", "node-1", ns.URL(),
		cluster.HealthAddr(":38386"),
		cluster.MetricsAddr(":39396"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Test concurrent access to protected resource
	var counter int64
	var wg sync.WaitGroup

	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lock := platform.Lock("counter-resource")
			for j := 0; j < 3; j++ {
				err := lock.DoWithTTL(ctx, 5*time.Second, func(ctx context.Context) error {
					// Critical section
					current := atomic.LoadInt64(&counter)
					time.Sleep(10 * time.Millisecond)
					atomic.StoreInt64(&counter, current+1)
					return nil
				})
				if err != nil {
					// Retry on lock contention
					time.Sleep(100 * time.Millisecond)
					j--
				}
			}
		}()
	}

	wg.Wait()
	// All increments should have happened
	require.Equal(t, int64(15), atomic.LoadInt64(&counter))
}

func TestPlatformLock_WaitAndAcquire(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-plock-wait", "node-1", ns.URL(),
		cluster.HealthAddr(":38387"),
		cluster.MetricsAddr(":39397"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	lock1 := platform.Lock("wait-resource")
	lock2 := platform.Lock("wait-resource")

	// First lock acquires
	err = lock1.AcquireWithTTL(ctx, 1*time.Second)
	require.NoError(t, err)

	// Second lock waits
	var acquired atomic.Bool
	go func() {
		err := lock2.WaitAndAcquire(ctx, 2*time.Second)
		if err == nil {
			acquired.Store(true)
			lock2.Release(ctx)
		}
	}()

	// Release first lock after a short delay
	time.Sleep(500 * time.Millisecond)
	lock1.Release(ctx)

	// Wait for second lock to acquire
	time.Sleep(500 * time.Millisecond)
	require.True(t, acquired.Load())
}
