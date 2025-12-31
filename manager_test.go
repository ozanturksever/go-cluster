package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	natsmodule "github.com/testcontainers/testcontainers-go/modules/nats"
)

// startNATSContainer starts a NATS container with JetStream enabled for testing.
func startNATSContainer(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()

	container, err := natsmodule.Run(ctx, "nats:latest")
	if err != nil {
		t.Fatalf("failed to start NATS container: %v", err)
	}

	url, err := container.ConnectionString(ctx)
	if err != nil {
		container.Terminate(ctx)
		t.Fatalf("failed to get NATS connection string: %v", err)
	}

	cleanup := func() {
		if err := container.Terminate(ctx); err != nil {
			t.Logf("failed to terminate NATS container: %v", err)
		}
	}

	// Verify connection works
	nc, err := natsgo.Connect(url)
	if err != nil {
		cleanup()
		t.Fatalf("failed to connect to NATS: %v", err)
	}
	nc.Close()

	return url, cleanup
}

func TestNewManager(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers: []string{"nats://localhost:4222"},
		},
	}

	mgr, err := NewManager(cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if mgr == nil {
		t.Fatal("NewManager() returned nil")
	}
}

func TestNewManagerInvalidConfig(t *testing.T) {
	cfg := FileConfig{
		// Missing required fields
	}

	_, err := NewManager(cfg, nil)
	if err == nil {
		t.Error("NewManager() expected error for invalid config")
	}
}

func TestManagerInit(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := tmpDir + "/cluster.json"

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{"nats://localhost:4222"})
	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	ctx := context.Background()
	if err := mgr.Init(ctx, outputPath); err != nil {
		t.Fatalf("Init() error = %v", err)
	}

	// Verify the file was created
	loaded, err := LoadConfigFromFile(outputPath)
	if err != nil {
		t.Fatalf("LoadConfigFromFile() error = %v", err)
	}

	if loaded.ClusterID != "test-cluster" {
		t.Errorf("loaded ClusterID = %q, want %q", loaded.ClusterID, "test-cluster")
	}
}

func TestManagerJoin(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	// Use shorter timeouts for testing
	cfg.Election.LeaseTTLMs = 5000
	cfg.Election.HeartbeatIntervalMs = 1000

	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Join should succeed
	if err := mgr.Join(ctx); err != nil {
		t.Fatalf("Join() error = %v", err)
	}
}

func TestManagerStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 5000
	cfg.Election.HeartbeatIntervalMs = 1000

	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if status.ClusterID != "test-cluster" {
		t.Errorf("status.ClusterID = %q, want %q", status.ClusterID, "test-cluster")
	}
	if status.NodeID != "node-1" {
		t.Errorf("status.NodeID = %q, want %q", status.NodeID, "node-1")
	}
}

func TestManagerRunDaemon(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	var becameLeader atomic.Bool
	hooks := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			becameLeader.Store(true)
			return nil
		},
	}

	mgr, err := NewManager(*cfg, hooks)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Run daemon in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.RunDaemon(ctx)
	}()

	// Wait for leadership
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if becameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !becameLeader.Load() {
		t.Error("node did not become leader within timeout")
	}

	// Verify status while running
	status, err := mgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}

	if !status.Connected {
		t.Error("status.Connected = false, want true")
	}
	if status.Role != RolePrimary {
		t.Errorf("status.Role = %v, want %v", status.Role, RolePrimary)
	}

	// Stop the daemon
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Errorf("RunDaemon() returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("RunDaemon() did not stop within timeout")
	}
}

func TestManagerPromote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Promote should succeed (no other leader)
	if err := mgr.Promote(ctx); err != nil {
		t.Fatalf("Promote() error = %v", err)
	}
}

func TestManagerDemote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	var becameLeader atomic.Bool
	var lostLeadership atomic.Bool
	hooks := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			becameLeader.Store(true)
			return nil
		},
		onLoseLeadership: func(ctx context.Context) error {
			lostLeadership.Store(true)
			return nil
		},
	}

	mgr, err := NewManager(*cfg, hooks)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Run daemon in background
	go mgr.RunDaemon(ctx)

	// Wait for leadership
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if becameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !becameLeader.Load() {
		t.Fatal("node did not become leader within timeout")
	}

	// Demote
	if err := mgr.Demote(ctx); err != nil {
		t.Fatalf("Demote() error = %v", err)
	}

	// Verify lost leadership hook was called
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if lostLeadership.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !lostLeadership.Load() {
		t.Error("OnLoseLeadership hook was not called")
	}
}

func TestManagerLeave(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	var becameLeader atomic.Bool
	hooks := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			becameLeader.Store(true)
			return nil
		},
	}

	mgr, err := NewManager(*cfg, hooks)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Run daemon in background
	go mgr.RunDaemon(ctx)

	// Wait for leadership
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if becameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Leave should work
	if err := mgr.Leave(ctx); err != nil {
		t.Fatalf("Leave() error = %v", err)
	}
}

func TestManagerAlreadyRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	// Run daemon in background
	go mgr.RunDaemon(ctx)

	// Wait a bit for it to start
	time.Sleep(500 * time.Millisecond)

	// Try to run again
	err = mgr.RunDaemon(ctx)
	if err != ErrManagerAlreadyRunning {
		t.Errorf("RunDaemon() error = %v, want %v", err, ErrManagerAlreadyRunning)
	}

	// Join should also fail
	err = mgr.Join(ctx)
	if err != ErrManagerAlreadyRunning {
		t.Errorf("Join() error = %v, want %v", err, ErrManagerAlreadyRunning)
	}
}

func TestManagerIsRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	natsURL, cleanup := startNATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	mgr, err := NewManager(*cfg, nil)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if mgr.IsRunning() {
		t.Error("IsRunning() = true before starting, want false")
	}

	// Run daemon in background
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.RunDaemon(ctx)
	}()

	// Wait a bit for it to start
	time.Sleep(500 * time.Millisecond)

	if !mgr.IsRunning() {
		t.Error("IsRunning() = false while running, want true")
	}

	// Stop
	cancel()
	<-errCh

	// Give it a moment to clean up
	time.Sleep(100 * time.Millisecond)

	if mgr.IsRunning() {
		t.Error("IsRunning() = true after stopping, want false")
	}
}

// testManagerHooks is a test implementation of ManagerHooks
type testManagerHooks struct {
	mu               sync.Mutex
	onBecomeLeader   func(ctx context.Context) error
	onLoseLeadership func(ctx context.Context) error
	onLeaderChange   func(ctx context.Context, nodeID string) error
	onDaemonStart    func(ctx context.Context) error
	onDaemonStop     func(ctx context.Context) error
}

func (h *testManagerHooks) OnBecomeLeader(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onBecomeLeader != nil {
		return h.onBecomeLeader(ctx)
	}
	return nil
}

func (h *testManagerHooks) OnLoseLeadership(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onLoseLeadership != nil {
		return h.onLoseLeadership(ctx)
	}
	return nil
}

func (h *testManagerHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onLeaderChange != nil {
		return h.onLeaderChange(ctx, nodeID)
	}
	return nil
}

func (h *testManagerHooks) OnDaemonStart(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onDaemonStart != nil {
		return h.onDaemonStart(ctx)
	}
	return nil
}

func (h *testManagerHooks) OnDaemonStop(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.onDaemonStop != nil {
		return h.onDaemonStop(ctx)
	}
	return nil
}

func (h *testManagerHooks) OnNATSReconnect(ctx context.Context) error {
	return nil
}

func (h *testManagerHooks) OnNATSDisconnect(ctx context.Context, err error) error {
	return nil
}

var _ ManagerHooks = (*testManagerHooks)(nil)
