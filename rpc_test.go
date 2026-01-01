package cluster_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupRPCTest(t *testing.T) (*cluster.Platform, *cluster.App, func()) {
	t.Helper()

	ns := testutil.StartNATS(t)

	platform, err := cluster.NewPlatform("rpctest", "node-1", ns.URL(),
		cluster.HealthAddr(":68080"),
		cluster.MetricsAddr(":69090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("rpcapp", cluster.Spread())
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

func TestRPC_HandleAndCall(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	// Register a handler
	rpc.Handle("echo", func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil
	})

	// Call the handler
	response, err := rpc.Call(ctx, "node-1", "echo", []byte("hello"))
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), response)
}

func TestRPC_HandleWithError(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	// Register a handler that returns an error
	rpc.Handle("fail", func(ctx context.Context, req []byte) ([]byte, error) {
		return nil, errors.New("intentional error")
	})

	// Call should return the error
	_, err := rpc.Call(ctx, "node-1", "fail", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "intentional error")
}

func TestRPC_UnknownMethod(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	// Call non-existent method
	_, err := rpc.Call(ctx, "node-1", "nonexistent", nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unknown method")
}

func TestRPC_CallWithTimeout(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	// Register a slow handler
	rpc.Handle("slow", func(ctx context.Context, req []byte) ([]byte, error) {
		time.Sleep(2 * time.Second)
		return []byte("done"), nil
	})

	// Call with short timeout should fail
	_, err := rpc.CallWithTimeout(ctx, "node-1", "slow", nil, 100*time.Millisecond)
	assert.Error(t, err)
}

func TestRPC_JSONPayload(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	type Request struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}

	type Response struct {
		Greeting string `json:"greeting"`
	}

	// Register handler that processes JSON
	rpc.Handle("greet", func(ctx context.Context, req []byte) ([]byte, error) {
		var r Request
		if err := json.Unmarshal(req, &r); err != nil {
			return nil, err
		}
		resp := Response{Greeting: "Hello, " + r.Name}
		return json.Marshal(resp)
	})

	// Make request with JSON
	reqData, _ := json.Marshal(Request{Name: "World", Age: 42})
	respData, err := rpc.Call(ctx, "node-1", "greet", reqData)
	require.NoError(t, err)

	var resp Response
	err = json.Unmarshal(respData, &resp)
	require.NoError(t, err)
	assert.Equal(t, "Hello, World", resp.Greeting)
}

func TestRPC_MultipleHandlers(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	ctx := context.Background()
	rpc := app.RPC()

	// Register multiple handlers
	rpc.Handle("add", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("add result"), nil
	})

	rpc.Handle("subtract", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("subtract result"), nil
	})

	rpc.Handle("multiply", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("multiply result"), nil
	})

	// Call each handler
	resp1, err := rpc.Call(ctx, "node-1", "add", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("add result"), resp1)

	resp2, err := rpc.Call(ctx, "node-1", "subtract", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("subtract result"), resp2)

	resp3, err := rpc.Call(ctx, "node-1", "multiply", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("multiply result"), resp3)
}

func TestRPC_CrossNodeCall(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Create two nodes
	platform1, err := cluster.NewPlatform("rpctest2", "node-1", ns.URL(),
		cluster.HealthAddr(":78080"),
		cluster.MetricsAddr(":79090"),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("rpctest2", "node-2", ns.URL(),
		cluster.HealthAddr(":78081"),
		cluster.MetricsAddr(":79091"),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("rpcapp", cluster.Spread())
	app2 := cluster.NewApp("rpcapp", cluster.Spread())

	platform1.Register(app1)
	platform2.Register(app2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform1.Run(ctx)
	go platform2.Run(ctx)

	time.Sleep(300 * time.Millisecond)

	// Register handler on node-2
	app2.RPC().Handle("ping", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("pong from node-2"), nil
	})

	// Call from node-1 to node-2
	resp, err := app1.RPC().Call(ctx, "node-2", "ping", nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("pong from node-2"), resp)
}

func TestRPC_ContextCancellation(t *testing.T) {
	_, app, cleanup := setupRPCTest(t)
	defer cleanup()

	rpc := app.RPC()

	// Register a slow handler
	rpc.Handle("veryslow", func(ctx context.Context, req []byte) ([]byte, error) {
		time.Sleep(10 * time.Second)
		return []byte("done"), nil
	})

	// Create context with short deadline
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	// Call should timeout
	_, err := rpc.Call(ctx, "node-1", "veryslow", nil)
	assert.Error(t, err)
}
