package cluster_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/require"
)

func TestPlatformEvents_PublishSubscribe(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-pevents", "node-1", ns.URL(),
		cluster.HealthAddr(":38280"),
		cluster.MetricsAddr(":39290"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Subscribe to events
	var received atomic.Int32
	sub, err := platform.Events().Subscribe("user.*", func(e cluster.PlatformEvent) {
		received.Add(1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Publish events
	err = platform.Events().Publish(ctx, "user.created", []byte(`{"id": "123"}`))
	require.NoError(t, err)

	err = platform.Events().Publish(ctx, "user.updated", []byte(`{"id": "123"}`))
	require.NoError(t, err)

	// Wait for events to be received
	time.Sleep(500 * time.Millisecond)
	require.GreaterOrEqual(t, received.Load(), int32(2))
}

func TestPlatformEvents_SubscribeChannel(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-pevents-ch", "node-1", ns.URL(),
		cluster.HealthAddr(":38281"),
		cluster.MetricsAddr(":39291"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Subscribe without handler, use channel
	sub, err := platform.Events().Subscribe("order.>", nil)
	require.NoError(t, err)

	// Publish event
	go func() {
		time.Sleep(100 * time.Millisecond)
		platform.Events().Publish(ctx, "order.created", []byte(`{"order_id": "456"}`))
	}()

	// Receive from channel
	select {
	case event := <-sub.C():
		require.Contains(t, event.Subject, "order.created")
		require.Equal(t, `{"order_id": "456"}`, string(event.Data))
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}

	sub.Unsubscribe()
}

func TestPlatformEvents_PublishFrom(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-pevents-from", "node-1", ns.URL(),
		cluster.HealthAddr(":38282"),
		cluster.MetricsAddr(":39292"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Subscribe to events
	var receivedEvent *cluster.PlatformEvent
	sub, err := platform.Events().Subscribe("cache.*", func(e cluster.PlatformEvent) {
		receivedEvent = &e
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Publish from a specific app
	err = platform.Events().PublishFrom(ctx, "cache-app", "cache.invalidated", []byte(`{"key": "user:123"}`))
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)
	require.NotNil(t, receivedEvent)
	require.Equal(t, "cache-app", receivedEvent.SourceApp)
	require.Equal(t, "node-1", receivedEvent.SourceNode)
}

func TestPlatformEvents_SubscribeFromApp(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-pevents-appfilter", "node-1", ns.URL(),
		cluster.HealthAddr(":38283"),
		cluster.MetricsAddr(":39293"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Subscribe to events from specific app
	var received atomic.Int32
	sub, err := platform.Events().SubscribeFromApp("payment-service", "payment.*", func(e cluster.PlatformEvent) {
		received.Add(1)
	})
	require.NoError(t, err)
	defer sub.Unsubscribe()

	// Publish from different apps
	platform.Events().PublishFrom(ctx, "payment-service", "payment.completed", []byte(`{}`))
	platform.Events().PublishFrom(ctx, "other-service", "payment.completed", []byte(`{}`))

	time.Sleep(500 * time.Millisecond)
	// Should only receive the one from payment-service
	require.Equal(t, int32(1), received.Load())
}

func TestPlatformEvents_MultipleSubscribers(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-pevents-multi", "node-1", ns.URL(),
		cluster.HealthAddr(":38284"),
		cluster.MetricsAddr(":39294"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Create multiple subscribers
	var count1, count2 atomic.Int32
	sub1, _ := platform.Events().Subscribe("notification.*", func(e cluster.PlatformEvent) {
		count1.Add(1)
	})
	sub2, _ := platform.Events().Subscribe("notification.*", func(e cluster.PlatformEvent) {
		count2.Add(1)
	})
	defer sub1.Unsubscribe()
	defer sub2.Unsubscribe()

	// Publish event
	platform.Events().Publish(ctx, "notification.sent", []byte(`{}`))

	time.Sleep(500 * time.Millisecond)
	require.Equal(t, int32(1), count1.Load())
	require.Equal(t, int32(1), count2.Load())
}
