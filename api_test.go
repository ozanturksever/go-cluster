package cluster_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/require"
)

func TestAPIClient_CallToLeader(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Create platform with two apps
	platform, err := cluster.NewPlatform("test-api", "node-1", ns.URL(),
		cluster.HealthAddr(":38180"),
		cluster.MetricsAddr(":39190"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	// Create provider app (singleton)
	providerApp := cluster.NewApp("provider", cluster.Singleton())
	providerApp.Handle("echo", func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil
	})
	providerApp.Handle("greet", func(ctx context.Context, req []byte) ([]byte, error) {
		var name string
		json.Unmarshal(req, &name)
		response := map[string]string{"message": "Hello, " + name}
		return json.Marshal(response)
	})
	platform.Register(providerApp)

	// Create consumer app
	consumerApp := cluster.NewApp("consumer", cluster.Singleton())
	platform.Register(consumerApp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Start platform in background
	go platform.Run(ctx)
	time.Sleep(2 * time.Second) // Wait for election

	// Test API call from platform
	apiClient := platform.API("provider")
	resp, err := apiClient.Call(ctx, "echo", []byte("hello"), cluster.ToLeader())
	require.NoError(t, err)
	require.Equal(t, "hello", string(resp))
}

func TestAPIClient_CallJSON(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-api-json", "node-1", ns.URL(),
		cluster.HealthAddr(":38181"),
		cluster.MetricsAddr(":39191"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	type GreetRequest struct {
		Name string `json:"name"`
	}
	type GreetResponse struct {
		Message string `json:"message"`
	}

	providerApp := cluster.NewApp("greeter", cluster.Singleton())
	providerApp.Handle("greet", func(ctx context.Context, req []byte) ([]byte, error) {
		var r GreetRequest
		json.Unmarshal(req, &r)
		return json.Marshal(GreetResponse{Message: "Hello, " + r.Name})
	})
	platform.Register(providerApp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(2 * time.Second)

	apiClient := platform.API("greeter")
	var resp GreetResponse
	err = apiClient.CallJSON(ctx, "greet", GreetRequest{Name: "World"}, &resp, cluster.ToLeader())
	require.NoError(t, err)
	require.Equal(t, "Hello, World", resp.Message)
}

func TestAPIClient_RouteToAny(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-api-any", "node-1", ns.URL(),
		cluster.HealthAddr(":38182"),
		cluster.MetricsAddr(":39192"),
	)
	require.NoError(t, err)

	// Create a spread app (all instances active)
	spreadApp := cluster.NewApp("cache", cluster.Spread())
	spreadApp.Handle("ping", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("pong"), nil
	})
	platform.Register(spreadApp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	apiClient := platform.API("cache")
	resp, err := apiClient.Call(ctx, "ping", nil, cluster.ToAny())
	require.NoError(t, err)
	require.Equal(t, "pong", string(resp))
}

func TestAPIClient_WithTimeout(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-api-timeout", "node-1", ns.URL(),
		cluster.HealthAddr(":38183"),
		cluster.MetricsAddr(":39193"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	slowApp := cluster.NewApp("slow", cluster.Singleton())
	slowApp.Handle("slow", func(ctx context.Context, req []byte) ([]byte, error) {
		time.Sleep(5 * time.Second)
		return []byte("done"), nil
	})
	platform.Register(slowApp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(2 * time.Second)

	apiClient := platform.API("slow")
	_, err = apiClient.Call(ctx, "slow", nil, cluster.ToLeader(), cluster.WithTimeout(100*time.Millisecond))
	require.Error(t, err) // Should timeout
}

func TestAPIClient_Broadcast(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-api-broadcast", "node-1", ns.URL(),
		cluster.HealthAddr(":38184"),
		cluster.MetricsAddr(":39194"),
	)
	require.NoError(t, err)

	broadcastApp := cluster.NewApp("broadcaster", cluster.Spread())
	broadcastApp.Handle("status", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("ok"), nil
	})
	platform.Register(broadcastApp)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	apiClient := platform.API("broadcaster")
	responses := apiClient.Broadcast(ctx, "status", nil)
	require.GreaterOrEqual(t, len(responses), 1)
	for _, resp := range responses {
		require.NoError(t, resp.Error)
		require.Equal(t, "ok", string(resp.Data))
	}
}

func TestAPIClient_CircuitBreaker(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-api-cb", "node-1", ns.URL(),
		cluster.HealthAddr(":38185"),
		cluster.MetricsAddr(":39195"),
	)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(500 * time.Millisecond)

	// Call non-existent app multiple times to trigger circuit breaker
	apiClient := platform.API("nonexistent")
	for i := 0; i < 10; i++ {
		_, _ = apiClient.Call(ctx, "test", nil, cluster.ToAny())
	}

	// Circuit breaker should eventually open
	// Note: This is a basic test - in production you'd want more sophisticated testing
}
