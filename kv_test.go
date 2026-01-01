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

func setupKVTest(t *testing.T) (*cluster.Platform, *cluster.App, func()) {
	t.Helper()

	ns := testutil.StartNATS(t)

	platform, err := cluster.NewPlatform("kvtest", "node-1", ns.URL(),
		cluster.HealthAddr(":38080"),
		cluster.MetricsAddr(":39090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", cluster.Spread())
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

func TestKV_PutAndGet(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Put a value
	rev, err := kv.Put(ctx, "key1", []byte("value1"))
	require.NoError(t, err)
	assert.Greater(t, rev, uint64(0))

	// Get the value
	value, err := kv.Get(ctx, "key1")
	require.NoError(t, err)
	assert.Equal(t, []byte("value1"), value)
}

func TestKV_GetEntry(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Put a value
	_, err := kv.Put(ctx, "key1", []byte("value1"))
	require.NoError(t, err)

	// Get entry with metadata
	entry, err := kv.GetEntry(ctx, "key1")
	require.NoError(t, err)
	assert.Equal(t, "key1", entry.Key)
	assert.Equal(t, []byte("value1"), entry.Value)
	assert.Greater(t, entry.Revision, uint64(0))
}

func TestKV_GetNotFound(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Get non-existent key
	_, err := kv.Get(ctx, "nonexistent")
	assert.Error(t, err)
}

func TestKV_Create(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Create a new key
	rev, err := kv.Create(ctx, "newkey", []byte("newvalue"))
	require.NoError(t, err)
	assert.Greater(t, rev, uint64(0))

	// Try to create same key again - should fail
	_, err = kv.Create(ctx, "newkey", []byte("anothervalue"))
	assert.Error(t, err)
}

func TestKV_Update(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Create initial value
	rev1, err := kv.Put(ctx, "updatekey", []byte("initial"))
	require.NoError(t, err)

	// Update with correct revision
	rev2, err := kv.Update(ctx, "updatekey", []byte("updated"), rev1)
	require.NoError(t, err)
	assert.Greater(t, rev2, rev1)

	// Verify update
	value, err := kv.Get(ctx, "updatekey")
	require.NoError(t, err)
	assert.Equal(t, []byte("updated"), value)

	// Update with wrong revision - should fail
	_, err = kv.Update(ctx, "updatekey", []byte("fail"), rev1)
	assert.Error(t, err)
}

func TestKV_Delete(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Put a value
	_, err := kv.Put(ctx, "deletekey", []byte("todelete"))
	require.NoError(t, err)

	// Delete it
	err = kv.Delete(ctx, "deletekey")
	require.NoError(t, err)

	// Verify it's gone
	_, err = kv.Get(ctx, "deletekey")
	assert.Error(t, err)
}

func TestKV_Keys(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Put multiple values
	_, err := kv.Put(ctx, "a", []byte("1"))
	require.NoError(t, err)
	_, err = kv.Put(ctx, "b", []byte("2"))
	require.NoError(t, err)
	_, err = kv.Put(ctx, "c", []byte("3"))
	require.NoError(t, err)

	// Get all keys
	keys, err := kv.Keys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 3)
	assert.Contains(t, keys, "a")
	assert.Contains(t, keys, "b")
	assert.Contains(t, keys, "c")
}

func TestKV_Watch(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	kv := app.KV()

	// Start watching
	ch, err := kv.Watch(ctx, "watch.*")
	require.NoError(t, err)

	// Put a value that matches the pattern
	go func() {
		time.Sleep(100 * time.Millisecond)
		kv.Put(context.Background(), "watch.key1", []byte("watched"))
	}()

	// Wait for the watch event
	select {
	case entry := <-ch:
		assert.Equal(t, "watch.key1", entry.Key)
		assert.Equal(t, []byte("watched"), entry.Value)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for watch event")
	}
}

func TestKV_MultipleOperations(t *testing.T) {
	_, app, cleanup := setupKVTest(t)
	defer cleanup()

	ctx := context.Background()
	kv := app.KV()

	// Perform multiple operations
	for i := 0; i < 10; i++ {
		key := "multi" + string(rune('0'+i))
		_, err := kv.Put(ctx, key, []byte("value"))
		require.NoError(t, err)
	}

	// Verify all keys exist
	keys, err := kv.Keys(ctx)
	require.NoError(t, err)
	assert.Len(t, keys, 10)
}
