package cluster_test

import (
	"context"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupEventsTest(t *testing.T) (*cluster.Platform, *cluster.App, func()) {
	t.Helper()

	ns := testutil.StartNATS(t)

	platform, err := cluster.NewPlatform("eventstest", "node-1", ns.URL(),
		cluster.HealthAddr(":88080"),
		cluster.MetricsAddr(":89090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("eventsapp", cluster.Spread())
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

func TestEvents_PublishAndSubscribe(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	// Subscribe to events
	sub := events.Subscribe("test.*")
	defer sub.Unsubscribe()

	// Publish an event
	err := events.Publish(ctx, "test.created", []byte("event data"))
	require.NoError(t, err)

	// Receive the event
	select {
	case event := <-sub.C():
		assert.Contains(t, event.Subject, "test.created")
		assert.Equal(t, []byte("event data"), event.Data)
		assert.Equal(t, "eventsapp", event.SourceApp)
		assert.Equal(t, "node-1", event.SourceNode)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEvents_MultipleSubscribers(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	// Create multiple subscribers
	sub1 := events.Subscribe("multi.*")
	sub2 := events.Subscribe("multi.*")
	defer sub1.Unsubscribe()
	defer sub2.Unsubscribe()

	// Publish an event
	err := events.Publish(ctx, "multi.event", []byte("shared"))
	require.NoError(t, err)

	// Both should receive the event
	received1 := false
	received2 := false

	timeout := time.After(2 * time.Second)
	for !received1 || !received2 {
		select {
		case <-sub1.C():
			received1 = true
		case <-sub2.C():
			received2 = true
		case <-timeout:
			t.Fatalf("timeout: sub1=%v, sub2=%v", received1, received2)
		}
	}

	assert.True(t, received1)
	assert.True(t, received2)
}

func TestEvents_PatternMatching(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	// Subscribe to specific pattern
	sub := events.Subscribe("orders.*")
	defer sub.Unsubscribe()

	// Publish matching event
	err := events.Publish(ctx, "orders.created", []byte("order1"))
	require.NoError(t, err)

	// Should receive
	select {
	case event := <-sub.C():
		assert.Contains(t, event.Subject, "orders.created")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for matching event")
	}

	// Publish non-matching event
	err = events.Publish(ctx, "users.created", []byte("user1"))
	require.NoError(t, err)

	// Should NOT receive (use short timeout)
	select {
	case <-sub.C():
		t.Fatal("received event that should not match")
	case <-time.After(200 * time.Millisecond):
		// Expected - no event received
	}
}

func TestEvents_MultiplePatterns(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	// Subscribe to different patterns
	ordersSub := events.Subscribe("orders.*")
	usersSub := events.Subscribe("users.*")
	defer ordersSub.Unsubscribe()
	defer usersSub.Unsubscribe()

	// Publish order event
	err := events.Publish(ctx, "orders.created", []byte("order"))
	require.NoError(t, err)

	// Publish user event
	err = events.Publish(ctx, "users.created", []byte("user"))
	require.NoError(t, err)

	// Orders subscriber should only get order events
	select {
	case event := <-ordersSub.C():
		assert.Contains(t, event.Subject, "orders.created")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for order event")
	}

	// Users subscriber should only get user events
	select {
	case event := <-usersSub.C():
		assert.Contains(t, event.Subject, "users.created")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for user event")
	}
}

func TestEvents_HighVolume(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	sub := events.Subscribe("volume.*")
	defer sub.Unsubscribe()

	// Publish many events
	count := 100
	for i := 0; i < count; i++ {
		err := events.Publish(ctx, "volume.test", []byte("data"))
		require.NoError(t, err)
	}

	// Receive events
	received := 0
	timeout := time.After(5 * time.Second)
	for received < count {
		select {
		case <-sub.C():
			received++
		case <-timeout:
			t.Fatalf("timeout: received %d/%d events", received, count)
		}
	}

	assert.Equal(t, count, received)
}

func TestEvents_CrossNode(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Create two nodes
	platform1, err := cluster.NewPlatform("eventstest2", "node-1", ns.URL(),
		cluster.HealthAddr(":98080"),
		cluster.MetricsAddr(":99090"),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("eventstest2", "node-2", ns.URL(),
		cluster.HealthAddr(":98081"),
		cluster.MetricsAddr(":99091"),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("eventsapp", cluster.Spread())
	app2 := cluster.NewApp("eventsapp", cluster.Spread())

	platform1.Register(app1)
	platform2.Register(app2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform1.Run(ctx)
	go platform2.Run(ctx)

	time.Sleep(300 * time.Millisecond)

	// Subscribe on node-2
	sub := app2.Events().Subscribe("cross.*")
	defer sub.Unsubscribe()

	// Give subscription time to propagate across NATS connections
	time.Sleep(100 * time.Millisecond)

	// Publish from node-1
	err = app1.Events().Publish(ctx, "cross.node", []byte("from node-1"))
	require.NoError(t, err)

	// Node-2 should receive
	select {
	case event := <-sub.C():
		assert.Equal(t, []byte("from node-1"), event.Data)
		assert.Equal(t, "node-1", event.SourceNode)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for cross-node event")
	}
}

func TestEvents_Unsubscribe(t *testing.T) {
	_, app, cleanup := setupEventsTest(t)
	defer cleanup()

	ctx := context.Background()
	events := app.Events()

	sub := events.Subscribe("unsub.*")

	// Publish before unsubscribe
	err := events.Publish(ctx, "unsub.before", []byte("before"))
	require.NoError(t, err)

	// Receive it
	select {
	case <-sub.C():
		// Good
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for event before unsubscribe")
	}

	// Unsubscribe
	sub.Unsubscribe()

	// Channel should be closed
	_, ok := <-sub.C()
	assert.False(t, ok, "channel should be closed after unsubscribe")
}
