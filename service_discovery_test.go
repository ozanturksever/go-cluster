package cluster_test

import (
	"context"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/require"
)

func TestServiceConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  cluster.ServiceConfig
		wantErr bool
	}{
		{
			name:    "valid minimal config",
			config:  cluster.ServiceConfig{Port: 8080},
			wantErr: false,
		},
		{
			name:    "valid full config",
			config:  cluster.ServiceConfig{Port: 8080, Protocol: "http", Path: "/api", Weight: 100},
			wantErr: false,
		},
		{
			name:    "invalid port zero",
			config:  cluster.ServiceConfig{Port: 0},
			wantErr: true,
		},
		{
			name:    "invalid port negative",
			config:  cluster.ServiceConfig{Port: -1},
			wantErr: true,
		},
		{
			name:    "invalid port too high",
			config:  cluster.ServiceConfig{Port: 70000},
			wantErr: true,
		},
		{
			name:    "invalid protocol",
			config:  cluster.ServiceConfig{Port: 8080, Protocol: "invalid"},
			wantErr: true,
		},
		{
			name:    "invalid negative weight",
			config:  cluster.ServiceConfig{Port: 8080, Weight: -1},
			wantErr: true,
		},
		{
			name: "valid with health check",
			config: cluster.ServiceConfig{
				Port:     8080,
				Protocol: "http",
				HealthCheck: &cluster.HealthCheckConfig{
					Path:           "/health",
					Interval:       10 * time.Second,
					Timeout:        3 * time.Second,
					ExpectedStatus: 200,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid health check timeout exceeds interval",
			config: cluster.ServiceConfig{
				Port:     8080,
				Protocol: "http",
				HealthCheck: &cluster.HealthCheckConfig{
					Interval: 5 * time.Second,
					Timeout:  10 * time.Second,
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestServiceConfig_SetDefaults(t *testing.T) {
	config := cluster.ServiceConfig{Port: 8080}
	config.SetDefaults()

	require.Equal(t, "tcp", config.Protocol)
	require.Equal(t, 100, config.Weight)
	require.NotNil(t, config.Metadata)
}

func TestServiceConfig_IsHTTPBased(t *testing.T) {
	tests := []struct {
		protocol string
		want     bool
	}{
		{"http", true},
		{"https", true},
		{"ws", true},
		{"wss", true},
		{"tcp", false},
		{"udp", false},
		{"grpc", false},
	}

	for _, tt := range tests {
		t.Run(tt.protocol, func(t *testing.T) {
			config := cluster.ServiceConfig{Port: 8080, Protocol: tt.protocol}
			require.Equal(t, tt.want, config.IsHTTPBased())
		})
	}
}

func TestServiceRegistry_RegisterAndDiscover(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd", "node-1", ns.URL(),
		cluster.HealthAddr(":48080"),
		cluster.MetricsAddr(":49090"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("myapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a service
	app.ExposeService("http", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
		Path:     "/api",
		Metadata: map[string]string{"version": "1.0.0"},
		Tags:     []string{"public", "api"},
	})

	// Wait for registration
	time.Sleep(500 * time.Millisecond)

	// Discover the service
	services, err := platform.DiscoverServices(ctx, "myapp", "http")
	require.NoError(t, err)
	require.Len(t, services, 1)

	svc := services[0]
	require.Equal(t, "myapp", svc.AppName)
	require.Equal(t, "http", svc.Name)
	require.Equal(t, "node-1", svc.NodeID)
	require.Equal(t, 8080, svc.Port)
	require.Equal(t, "http", svc.Protocol)
	require.Equal(t, "/api", svc.Path)
	require.Equal(t, "1.0.0", svc.Metadata["version"])
	require.Contains(t, svc.Tags, "public")
	require.Contains(t, svc.Tags, "api")
	require.Equal(t, "passing", svc.Health)
}

func TestServiceRegistry_DiscoverByTag(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-tag", "node-1", ns.URL(),
		cluster.HealthAddr(":48081"),
		cluster.MetricsAddr(":49091"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("tagapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose services with different tags
	app.ExposeService("metrics", cluster.ServiceConfig{
		Port:     9090,
		Protocol: "http",
		Path:     "/metrics",
		Tags:     []string{"prometheus", "monitoring"},
	})

	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
		Tags:     []string{"public"},
	})

	time.Sleep(500 * time.Millisecond)

	// Discover by tag
	prometheusServices, err := platform.DiscoverServicesByTag(ctx, "prometheus")
	require.NoError(t, err)
	require.Len(t, prometheusServices, 1)
	require.Equal(t, "metrics", prometheusServices[0].Name)

	publicServices, err := platform.DiscoverServicesByTag(ctx, "public")
	require.NoError(t, err)
	require.Len(t, publicServices, 1)
	require.Equal(t, "api", publicServices[0].Name)

	// Non-existent tag
	nonExistent, err := platform.DiscoverServicesByTag(ctx, "nonexistent")
	require.NoError(t, err)
	require.Len(t, nonExistent, 0)
}

func TestServiceRegistry_DiscoverByMetadata(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-meta", "node-1", ns.URL(),
		cluster.HealthAddr(":48082"),
		cluster.MetricsAddr(":49092"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("metaapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose services with different metadata
	app.ExposeService("svc1", cluster.ServiceConfig{
		Port:     8081,
		Protocol: "http",
		Metadata: map[string]string{"format": "prometheus", "env": "prod"},
	})

	app.ExposeService("svc2", cluster.ServiceConfig{
		Port:     8082,
		Protocol: "http",
		Metadata: map[string]string{"format": "json", "env": "prod"},
	})

	time.Sleep(500 * time.Millisecond)

	// Discover by metadata
	promServices, err := platform.DiscoverServicesByMetadata(ctx, map[string]string{"format": "prometheus"})
	require.NoError(t, err)
	require.Len(t, promServices, 1)
	require.Equal(t, "svc1", promServices[0].Name)

	prodServices, err := platform.DiscoverServicesByMetadata(ctx, map[string]string{"env": "prod"})
	require.NoError(t, err)
	require.Len(t, prodServices, 2)

	// Multiple metadata filters
	filtered, err := platform.DiscoverServicesByMetadata(ctx, map[string]string{"format": "json", "env": "prod"})
	require.NoError(t, err)
	require.Len(t, filtered, 1)
	require.Equal(t, "svc2", filtered[0].Name)
}

func TestServiceRegistry_WatchServices(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-watch", "node-1", ns.URL(),
		cluster.HealthAddr(":48083"),
		cluster.MetricsAddr(":49093"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("watchapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Start watching
	watcher := platform.WatchServices(ctx, "watchapp", "api")
	defer watcher.Close()

	// Expose a service
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	// Wait for event
	select {
	case event := <-watcher.Events():
		require.Equal(t, cluster.ServiceRegistered, event.Type)
		require.Equal(t, "api", event.Service.Name)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for service registration event")
	}
}

func TestServiceRegistry_UpdateHealth(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-health", "node-1", ns.URL(),
		cluster.HealthAddr(":48084"),
		cluster.MetricsAddr(":49094"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("healthapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a service
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	time.Sleep(500 * time.Millisecond)

	// Verify initial health
	services, err := platform.DiscoverServices(ctx, "healthapp", "api")
	require.NoError(t, err)
	require.Len(t, services, 1)
	require.Equal(t, "passing", services[0].Health)

	// Update health to failing
	err = platform.ServiceRegistry().UpdateHealth(ctx, "healthapp", "api", "failing")
	require.NoError(t, err)

	// Verify health changed
	time.Sleep(100 * time.Millisecond)
	services, err = platform.DiscoverServices(ctx, "healthapp", "api")
	require.NoError(t, err)
	require.Len(t, services, 1)
	require.Equal(t, "failing", services[0].Health)
}

func TestServiceRegistry_ExportPrometheus(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-prom", "node-1", ns.URL(),
		cluster.HealthAddr(":48085"),
		cluster.MetricsAddr(":49095"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("promapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a prometheus-tagged service
	app.ExposeService("metrics", cluster.ServiceConfig{
		Port:     9090,
		Protocol: "http",
		Path:     "/metrics",
		Tags:     []string{"prometheus"},
		Metadata: map[string]string{"job": "myapp"},
	})

	time.Sleep(500 * time.Millisecond)

	// Export Prometheus targets
	targets, err := platform.ServiceRegistry().ExportPrometheusTargets(ctx)
	require.NoError(t, err)
	require.Len(t, targets, 1)

	target := targets[0]
	require.Len(t, target.Targets, 1)
	require.Contains(t, target.Targets[0], ":9090")
	require.Equal(t, "myapp", target.Labels["job"])
	require.Equal(t, "promapp", target.Labels["app"])
	require.Equal(t, "/metrics", target.Labels["__metrics_path__"])
}

func TestServiceRegistry_ExportConsul(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-consul", "node-1", ns.URL(),
		cluster.HealthAddr(":48086"),
		cluster.MetricsAddr(":49096"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("consulapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a service
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
		Tags:     []string{"web", "api"},
		Metadata: map[string]string{"version": "1.0"},
	})

	time.Sleep(500 * time.Millisecond)

	// Export Consul services
	consulServices, err := platform.ServiceRegistry().ExportConsulServices(ctx)
	require.NoError(t, err)
	require.Len(t, consulServices, 1)

	cs := consulServices[0]
	require.Equal(t, "api", cs.Name)
	require.Equal(t, 8080, cs.Port)
	require.Contains(t, cs.Tags, "web")
	require.Contains(t, cs.Tags, "api")
	require.Equal(t, "1.0", cs.Meta["version"])
	require.Equal(t, "passing", cs.Status)
}

func TestServiceRegistry_MultipleNodes(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	// Create two platforms (simulating two nodes)
	platform1, err := cluster.NewPlatform("test-sd-multi", "node-1", ns.URL(),
		cluster.HealthAddr(":48087"),
		cluster.MetricsAddr(":49097"),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("test-sd-multi", "node-2", ns.URL(),
		cluster.HealthAddr(":48088"),
		cluster.MetricsAddr(":49098"),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("cache", cluster.Spread())
	app2 := cluster.NewApp("cache", cluster.Spread())

	platform1.Register(app1)
	platform2.Register(app2)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform1.Run(ctx)
	go platform2.Run(ctx)
	time.Sleep(2 * time.Second)

	// Both nodes expose the same service
	app1.ExposeService("http", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	app2.ExposeService("http", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	time.Sleep(1 * time.Second)

	// Discover from either platform should find both
	services, err := platform1.DiscoverServices(ctx, "cache", "http")
	require.NoError(t, err)
	require.Len(t, services, 2)

	// Verify different nodes
	nodeIDs := make(map[string]bool)
	for _, svc := range services {
		nodeIDs[svc.NodeID] = true
	}
	require.True(t, nodeIDs["node-1"])
	require.True(t, nodeIDs["node-2"])
}

func TestServiceRegistry_Deregister(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-dereg", "node-1", ns.URL(),
		cluster.HealthAddr(":48089"),
		cluster.MetricsAddr(":49099"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("deregapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a service
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	time.Sleep(500 * time.Millisecond)

	// Verify service is registered
	services, err := platform.DiscoverServices(ctx, "deregapp", "api")
	require.NoError(t, err)
	require.Len(t, services, 1)

	// Deregister the service
	err = platform.ServiceRegistry().Deregister(ctx, "deregapp", "api")
	require.NoError(t, err)

	time.Sleep(200 * time.Millisecond)

	// Verify service is gone
	services, err = platform.DiscoverServices(ctx, "deregapp", "api")
	require.NoError(t, err)
	require.Len(t, services, 0)
}

func TestServiceRegistry_InvalidConfig(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-invalid", "node-1", ns.URL(),
		cluster.HealthAddr(":48090"),
		cluster.MetricsAddr(":49100"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("invalidapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Try to register with invalid config
	err = platform.ServiceRegistry().Register(ctx, "invalidapp", "api", &cluster.ServiceConfig{
		Port:     -1, // Invalid
		Protocol: "http",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Port")
}

func TestServiceRegistry_GetLocalServices(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-local", "node-1", ns.URL(),
		cluster.HealthAddr(":48091"),
		cluster.MetricsAddr(":49101"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("localapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose multiple services
	app.ExposeService("http", cluster.ServiceConfig{Port: 8080, Protocol: "http"})
	app.ExposeService("grpc", cluster.ServiceConfig{Port: 50051, Protocol: "grpc"})

	time.Sleep(500 * time.Millisecond)

	// Get local services
	local := platform.ServiceRegistry().GetLocalServices()
	require.Len(t, local, 2)

	names := make(map[string]bool)
	for _, svc := range local {
		names[svc.Name] = true
	}
	require.True(t, names["http"])
	require.True(t, names["grpc"])
}

func TestServiceRegistry_DiscoverHealthyOnly(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	platform, err := cluster.NewPlatform("test-sd-healthy", "node-1", ns.URL(),
		cluster.HealthAddr(":48092"),
		cluster.MetricsAddr(":49102"),
	)
	require.NoError(t, err)

	app := cluster.NewApp("healthyapp", cluster.Singleton())
	platform.Register(app)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	go platform.Run(ctx)
	time.Sleep(1 * time.Second)

	// Expose a service
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
	})

	time.Sleep(500 * time.Millisecond)

	// Initially all services should be healthy
	healthy, err := platform.ServiceRegistry().DiscoverHealthyServices(ctx, "healthyapp", "api")
	require.NoError(t, err)
	require.Len(t, healthy, 1)

	// Mark as failing
	err = platform.ServiceRegistry().UpdateHealth(ctx, "healthyapp", "api", "failing")
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	// Healthy query should return empty
	healthy, err = platform.ServiceRegistry().DiscoverHealthyServices(ctx, "healthyapp", "api")
	require.NoError(t, err)
	require.Len(t, healthy, 0)

	// Regular discover should still return the service
	all, err := platform.DiscoverServices(ctx, "healthyapp", "api")
	require.NoError(t, err)
	require.Len(t, all, 1)
}
