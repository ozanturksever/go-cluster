package cluster

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	natsc "github.com/testcontainers/testcontainers-go/modules/nats"
)

func TestNewService(t *testing.T) {
	tests := []struct {
		name      string
		cfg       Config
		nc        *nats.Conn
		callbacks ServiceCallbacks
		wantErr   bool
	}{
		{
			name: "nil connection",
			cfg: Config{
				ClusterID:      "test-cluster",
				NodeID:         "node-1",
				ServiceVersion: "1.0.0",
			},
			nc:        nil,
			callbacks: ServiceCallbacks{},
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewService(tt.cfg, tt.nc, tt.callbacks)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestStatusSubject(t *testing.T) {
	tests := []struct {
		clusterID string
		nodeID    string
		want      string
	}{
		{"my-cluster", "node-1", "cluster_my-cluster.status.node-1"},
		{"prod", "server-a", "cluster_prod.status.server-a"},
	}

	for _, tt := range tests {
		t.Run(tt.clusterID+"-"+tt.nodeID, func(t *testing.T) {
			got := StatusSubject(tt.clusterID, tt.nodeID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPingSubject(t *testing.T) {
	tests := []struct {
		clusterID string
		nodeID    string
		want      string
	}{
		{"my-cluster", "node-1", "cluster_my-cluster.ping.node-1"},
		{"prod", "server-a", "cluster_prod.ping.server-a"},
	}

	for _, tt := range tests {
		t.Run(tt.clusterID+"-"+tt.nodeID, func(t *testing.T) {
			got := PingSubject(tt.clusterID, tt.nodeID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestStepdownSubject(t *testing.T) {
	tests := []struct {
		clusterID string
		nodeID    string
		want      string
	}{
		{"my-cluster", "node-1", "cluster_my-cluster.control.node-1.stepdown"},
		{"prod", "server-a", "cluster_prod.control.server-a.stepdown"},
	}

	for _, tt := range tests {
		t.Run(tt.clusterID+"-"+tt.nodeID, func(t *testing.T) {
			got := StepdownSubject(tt.clusterID, tt.nodeID)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestService_StartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := natsc.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err)
	defer func() { _ = natsContainer.Terminate(ctx) }()

	natsURL, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	cfg := Config{
		ClusterID:      "test-cluster",
		NodeID:         "node-1",
		ServiceVersion: "1.0.0",
	}

	callbacks := ServiceCallbacks{
		GetRole:     func() Role { return RolePrimary },
		GetLeader:   func() string { return "node-1" },
		GetEpoch:    func() int64 { return 5 },
		IsConnected: func() bool { return true },
		DoStepdown:  func(ctx context.Context) error { return nil },
	}

	svc, err := NewService(cfg, nc, callbacks)
	require.NoError(t, err)

	// Start should succeed
	err = svc.Start()
	assert.NoError(t, err)

	// Start again should fail
	err = svc.Start()
	assert.Equal(t, ErrAlreadyStarted, err)

	// Info should be available
	info := svc.Info()
	assert.Equal(t, "cluster_test-cluster", info.Name)
	assert.Equal(t, "1.0.0", info.Version)

	// Stats should be available
	stats := svc.Stats()
	assert.Equal(t, "cluster_test-cluster", stats.Name)

	// Stop should succeed
	err = svc.Stop()
	assert.NoError(t, err)

	// Stop again should be idempotent
	err = svc.Stop()
	assert.NoError(t, err)
}

func TestService_StatusEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := natsc.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err)
	defer func() { _ = natsContainer.Terminate(ctx) }()

	natsURL, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	cfg := Config{
		ClusterID:      "test-cluster",
		NodeID:         "node-1",
		ServiceVersion: "1.0.0",
	}

	callbacks := ServiceCallbacks{
		GetRole:     func() Role { return RolePrimary },
		GetLeader:   func() string { return "node-1" },
		GetEpoch:    func() int64 { return 42 },
		IsConnected: func() bool { return true },
		DoStepdown:  func(ctx context.Context) error { return nil },
	}

	svc, err := NewService(cfg, nc, callbacks)
	require.NoError(t, err)

	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	// Wait a bit for service to be ready
	time.Sleep(100 * time.Millisecond)

	// Query the status endpoint
	resp, err := nc.Request(StatusSubject("test-cluster", "node-1"), nil, 5*time.Second)
	require.NoError(t, err)

	var status StatusResponse
	err = json.Unmarshal(resp.Data, &status)
	require.NoError(t, err)

	assert.Equal(t, "node-1", status.NodeID)
	assert.Equal(t, "PRIMARY", status.Role)
	assert.Equal(t, "node-1", status.Leader)
	assert.Equal(t, int64(42), status.Epoch)
	assert.True(t, status.Connected)
	assert.Greater(t, status.UptimeMs, int64(0))
}

func TestService_PingEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := natsc.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err)
	defer func() { _ = natsContainer.Terminate(ctx) }()

	natsURL, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	cfg := Config{
		ClusterID:      "test-cluster",
		NodeID:         "node-1",
		ServiceVersion: "1.0.0",
	}

	callbacks := ServiceCallbacks{
		GetRole:     func() Role { return RolePassive },
		GetLeader:   func() string { return "node-2" },
		GetEpoch:    func() int64 { return 1 },
		IsConnected: func() bool { return true },
		DoStepdown:  func(ctx context.Context) error { return nil },
	}

	svc, err := NewService(cfg, nc, callbacks)
	require.NoError(t, err)

	err = svc.Start()
	require.NoError(t, err)
	defer func() { _ = svc.Stop() }()

	// Wait a bit for service to be ready
	time.Sleep(100 * time.Millisecond)

	// Query the ping endpoint
	resp, err := nc.Request(PingSubject("test-cluster", "node-1"), nil, 5*time.Second)
	require.NoError(t, err)

	var ping PingResponse
	err = json.Unmarshal(resp.Data, &ping)
	require.NoError(t, err)

	assert.True(t, ping.OK)
	assert.Greater(t, ping.Timestamp, int64(0))
}

func TestService_StepdownEndpoint(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsContainer, err := natsc.Run(ctx,
		"nats:2.10",
		testcontainers.WithCmd("--jetstream"),
	)
	require.NoError(t, err)
	defer func() { _ = natsContainer.Terminate(ctx) }()

	natsURL, err := natsContainer.ConnectionString(ctx)
	require.NoError(t, err)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	t.Run("successful stepdown", func(t *testing.T) {
		stepdownCalled := false
		cfg := Config{
			ClusterID:      "test-cluster-stepdown-1",
			NodeID:         "node-1",
			ServiceVersion: "1.0.0",
		}

		callbacks := ServiceCallbacks{
			GetRole:     func() Role { return RolePrimary },
			GetLeader:   func() string { return "node-1" },
			GetEpoch:    func() int64 { return 1 },
			IsConnected: func() bool { return true },
			DoStepdown: func(ctx context.Context) error {
				stepdownCalled = true
				return nil
			},
		}

		svc, err := NewService(cfg, nc, callbacks)
		require.NoError(t, err)

		err = svc.Start()
		require.NoError(t, err)
		defer func() { _ = svc.Stop() }()

		time.Sleep(100 * time.Millisecond)

		resp, err := nc.Request(StepdownSubject("test-cluster-stepdown-1", "node-1"), nil, 5*time.Second)
		require.NoError(t, err)

		var stepdown StepdownResponse
		err = json.Unmarshal(resp.Data, &stepdown)
		require.NoError(t, err)

		assert.True(t, stepdown.OK)
		assert.Empty(t, stepdown.Error)
		assert.True(t, stepdownCalled)
	})

	t.Run("stepdown error", func(t *testing.T) {
		cfg := Config{
			ClusterID:      "test-cluster-stepdown-2",
			NodeID:         "node-2",
			ServiceVersion: "1.0.0",
		}

		callbacks := ServiceCallbacks{
			GetRole:     func() Role { return RolePassive },
			GetLeader:   func() string { return "node-1" },
			GetEpoch:    func() int64 { return 1 },
			IsConnected: func() bool { return true },
			DoStepdown: func(ctx context.Context) error {
				return ErrNotLeader
			},
		}

		svc, err := NewService(cfg, nc, callbacks)
		require.NoError(t, err)

		err = svc.Start()
		require.NoError(t, err)
		defer func() { _ = svc.Stop() }()

		time.Sleep(100 * time.Millisecond)

		resp, err := nc.Request(StepdownSubject("test-cluster-stepdown-2", "node-2"), nil, 5*time.Second)
		require.NoError(t, err)

		var stepdown StepdownResponse
		err = json.Unmarshal(resp.Data, &stepdown)
		require.NoError(t, err)

		assert.False(t, stepdown.OK)
		assert.Contains(t, stepdown.Error, "not the leader")
	})
}

func TestService_InfoAndStats(t *testing.T) {
	// Test when service is not started
	cfg := Config{
		ClusterID:      "test-cluster",
		NodeID:         "node-1",
		ServiceVersion: "1.0.0",
	}

	// Can't create service without a connection, so just test the struct directly
	svc := &Service{
		cfg: cfg,
	}

	// Info and Stats should return empty when not started
	info := svc.Info()
	assert.Empty(t, info.Name)

	stats := svc.Stats()
	assert.Empty(t, stats.Name)
}
