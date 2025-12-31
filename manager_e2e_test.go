package cluster

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster/vip"

	"github.com/testcontainers/testcontainers-go"
	natsmodule "github.com/testcontainers/testcontainers-go/modules/nats"
)

// TestE2E_TwoNodeFailover tests that when the leader stops, the other node takes over.
func TestE2E_TwoNodeFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startE2ENATSContainer(ctx, t)
	defer cleanup()

	// Create two managers
	// Use shorter LeaseTTL for faster crash recovery test
	cfg1 := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg1.Election.LeaseTTLMs = 2000
	cfg1.Election.HeartbeatIntervalMs = 500

	cfg2 := NewDefaultFileConfig("test-cluster", "node-2", []string{natsURL})
	cfg2.Election.LeaseTTLMs = 2000
	cfg2.Election.HeartbeatIntervalMs = 500

	var node1BecameLeader atomic.Bool
	var node2BecameLeader atomic.Bool

	hooks1 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node1BecameLeader.Store(true)
			return nil
		},
	}

	hooks2 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node2BecameLeader.Store(true)
			return nil
		},
	}

	mgr1, err := NewManager(*cfg1, hooks1)
	if err != nil {
		t.Fatalf("NewManager(node-1) error = %v", err)
	}

	mgr2, err := NewManager(*cfg2, hooks2)
	if err != nil {
		t.Fatalf("NewManager(node-2) error = %v", err)
	}

	// Start both daemons
	ctx1, cancel1 := context.WithCancel(ctx)
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	errCh1 := make(chan error, 1)
	errCh2 := make(chan error, 1)

	go func() { errCh1 <- mgr1.RunDaemon(ctx1) }()
	go func() { errCh2 <- mgr2.RunDaemon(ctx2) }()

	// Wait for one of them to become leader
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if node1BecameLeader.Load() || node2BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1BecameLeader.Load() && !node2BecameLeader.Load() {
		t.Fatal("neither node became leader within timeout")
	}

	// Determine which is the leader
	var leaderMgr, passiveMgr *Manager
	var leaderCtxCancel context.CancelFunc
	var leaderErrCh chan error

	if node1BecameLeader.Load() {
		t.Log("node-1 is the initial leader")
		leaderMgr = mgr1
		passiveMgr = mgr2
		leaderCtxCancel = cancel1
		leaderErrCh = errCh1
	} else {
		t.Log("node-2 is the initial leader")
		leaderMgr = mgr2
		passiveMgr = mgr1
		leaderCtxCancel = cancel2
		leaderErrCh = errCh2
	}

	_ = passiveMgr // unused but kept for clarity

	// Verify the leader is actually leading
	status, err := leaderMgr.Status(ctx)
	if err != nil {
		t.Fatalf("Status() error = %v", err)
	}
	if status.Role != RolePrimary {
		t.Errorf("leader status.Role = %v, want %v", status.Role, RolePrimary)
	}

	// Stop the leader (crash scenario - not graceful stepdown)
	t.Log("stopping the leader...")
	leaderCtxCancel()
	<-leaderErrCh

	// Wait for the other node to become leader (needs to wait for lease expiry)
	// With LeaseTTL=2s, we should wait at least 3-4 seconds for the lease to expire
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node1BecameLeader.Load() && node2BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1BecameLeader.Load() || !node2BecameLeader.Load() {
		t.Error("passive node did not become leader after failover")
	}

	t.Log("failover successful - passive node became leader")
}

// TestE2E_PromoteDemote tests promote and demote operations across two nodes.
func TestE2E_PromoteDemote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startE2ENATSContainer(ctx, t)
	defer cleanup()

	cfg1 := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg1.Election.LeaseTTLMs = 3000
	cfg1.Election.HeartbeatIntervalMs = 1000

	cfg2 := NewDefaultFileConfig("test-cluster", "node-2", []string{natsURL})
	cfg2.Election.LeaseTTLMs = 3000
	cfg2.Election.HeartbeatIntervalMs = 1000

	var node1BecameLeader atomic.Bool
	var node2BecameLeader atomic.Bool
	var node1LostLeader atomic.Bool

	hooks1 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node1BecameLeader.Store(true)
			return nil
		},
		onLoseLeadership: func(ctx context.Context) error {
			node1LostLeader.Store(true)
			return nil
		},
	}

	hooks2 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node2BecameLeader.Store(true)
			return nil
		},
	}

	mgr1, err := NewManager(*cfg1, hooks1)
	if err != nil {
		t.Fatalf("NewManager(node-1) error = %v", err)
	}

	mgr2, err := NewManager(*cfg2, hooks2)
	if err != nil {
		t.Fatalf("NewManager(node-2) error = %v", err)
	}

	// Start node1 first
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()

	go mgr1.RunDaemon(ctx1)

	// Wait for node1 to become leader
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node1BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1BecameLeader.Load() {
		t.Fatal("node-1 did not become leader")
	}

	// Start node2
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	go mgr2.RunDaemon(ctx2)

	// Wait a bit for node2 to connect
	time.Sleep(2 * time.Second)

	// node2 should NOT be leader (node1 is still leader)
	if node2BecameLeader.Load() {
		t.Error("node-2 became leader while node-1 was still running")
	}

	// Demote node1
	t.Log("demoting node-1...")
	if err := mgr1.Demote(ctx); err != nil {
		t.Fatalf("Demote() error = %v", err)
	}

	// Wait for node1 to lose leadership
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if node1LostLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1LostLeader.Load() {
		t.Error("node-1 did not lose leadership after demote")
	}

	// Wait for node2 to become leader
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node2BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node2BecameLeader.Load() {
		t.Error("node-2 did not become leader after node-1 demoted")
	}

	t.Log("promote/demote successful")
}

// TestE2E_VIPFailover tests that VIP moves with leadership using a mock executor.
func TestE2E_VIPFailover(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startE2ENATSContainer(ctx, t)
	defer cleanup()

	// Use mock VIP executor
	mockExec := &mockVIPExecutor{}

	// Use shorter LeaseTTL for faster crash recovery test
	cfg1 := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg1.Election.LeaseTTLMs = 2000
	cfg1.Election.HeartbeatIntervalMs = 500
	cfg1.VIP = VIPFileConfig{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	cfg2 := NewDefaultFileConfig("test-cluster", "node-2", []string{natsURL})
	cfg2.Election.LeaseTTLMs = 2000
	cfg2.Election.HeartbeatIntervalMs = 500
	cfg2.VIP = VIPFileConfig{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	var node1BecameLeader atomic.Bool
	var node2BecameLeader atomic.Bool

	hooks1 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node1BecameLeader.Store(true)
			return nil
		},
	}

	hooks2 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node2BecameLeader.Store(true)
			return nil
		},
	}

	mgr1, err := NewManager(*cfg1, hooks1, WithVIPExecutor(mockExec))
	if err != nil {
		t.Fatalf("NewManager(node-1) error = %v", err)
	}

	mgr2, err := NewManager(*cfg2, hooks2, WithVIPExecutor(mockExec))
	if err != nil {
		t.Fatalf("NewManager(node-2) error = %v", err)
	}

	// Start node1
	ctx1, cancel1 := context.WithCancel(ctx)
	defer cancel1()

	go mgr1.RunDaemon(ctx1)

	// Wait for node1 to become leader
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node1BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1BecameLeader.Load() {
		t.Fatal("node-1 did not become leader")
	}

	// Verify VIP was acquired
	time.Sleep(500 * time.Millisecond) // Give time for VIP acquisition
	if !mockExec.HasIP("192.168.1.100") {
		t.Error("VIP was not acquired when node-1 became leader")
	}

	// Start node2
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel2()

	go mgr2.RunDaemon(ctx2)
	time.Sleep(2 * time.Second)

	// Stop node1 (crash scenario - not graceful stepdown)
	// This tests crash recovery where the lease must expire before failover
	t.Log("stopping node-1...")
	cancel1()

	// Wait for node2 to become leader (needs to wait for lease expiry)
	// With LeaseTTL=2s, we should wait at least 3-4 seconds for the lease to expire
	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node2BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node2BecameLeader.Load() {
		t.Error("node-2 did not become leader after node-1 stopped")
	}

	// Verify VIP was re-acquired by node2
	time.Sleep(500 * time.Millisecond)
	if !mockExec.HasIP("192.168.1.100") {
		t.Error("VIP was not acquired when node-2 became leader")
	}

	t.Log("VIP failover successful")
}

// TestE2E_StatusConsistency tests that Status() returns consistent data across nodes.
func TestE2E_StatusConsistency(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx := context.Background()
	natsURL, cleanup := startE2ENATSContainer(ctx, t)
	defer cleanup()

	cfg1 := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg1.Election.LeaseTTLMs = 2000
	cfg1.Election.HeartbeatIntervalMs = 500

	cfg2 := NewDefaultFileConfig("test-cluster", "node-2", []string{natsURL})
	cfg2.Election.LeaseTTLMs = 2000
	cfg2.Election.HeartbeatIntervalMs = 500

	var node1BecameLeader atomic.Bool
	var node2BecameLeader atomic.Bool

	hooks1 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node1BecameLeader.Store(true)
			return nil
		},
	}

	hooks2 := &testManagerHooks{
		onBecomeLeader: func(ctx context.Context) error {
			node2BecameLeader.Store(true)
			return nil
		},
	}

	mgr1, err := NewManager(*cfg1, hooks1)
	if err != nil {
		t.Fatalf("NewManager(node-1) error = %v", err)
	}

	mgr2, err := NewManager(*cfg2, hooks2)
	if err != nil {
		t.Fatalf("NewManager(node-2) error = %v", err)
	}

	// Start both daemons
	ctx1, cancel1 := context.WithCancel(ctx)
	ctx2, cancel2 := context.WithCancel(ctx)
	defer cancel1()
	defer cancel2()

	go mgr1.RunDaemon(ctx1)
	go mgr2.RunDaemon(ctx2)

	// Wait for one node to become leader (could be either)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if node1BecameLeader.Load() || node2BecameLeader.Load() {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !node1BecameLeader.Load() && !node2BecameLeader.Load() {
		t.Fatal("neither node became leader")
	}

	// Determine which node is the leader
	var expectedLeader string
	if node1BecameLeader.Load() {
		expectedLeader = "node-1"
	} else {
		expectedLeader = "node-2"
	}

	// Give the other node time to detect the leader
	time.Sleep(2 * time.Second)

	// Get status from both nodes
	status1, err := mgr1.Status(ctx)
	if err != nil {
		t.Fatalf("mgr1.Status() error = %v", err)
	}

	status2, err := mgr2.Status(ctx)
	if err != nil {
		t.Fatalf("mgr2.Status() error = %v", err)
	}

	// Both should agree on the leader
	if status1.Leader != status2.Leader {
		t.Errorf("status1.Leader = %q, status2.Leader = %q, want same leader", status1.Leader, status2.Leader)
	}

	// Both should report the expected leader
	if status1.Leader != expectedLeader {
		t.Errorf("status1.Leader = %q, want %q", status1.Leader, expectedLeader)
	}
	if status2.Leader != expectedLeader {
		t.Errorf("status2.Leader = %q, want %q", status2.Leader, expectedLeader)
	}

	// Verify roles are correct based on which node became leader
	if expectedLeader == "node-1" {
		if status1.Role != RolePrimary {
			t.Errorf("status1.Role = %v, want %v", status1.Role, RolePrimary)
		}
		if status2.Role != RolePassive {
			t.Errorf("status2.Role = %v, want %v", status2.Role, RolePassive)
		}
	} else {
		if status2.Role != RolePrimary {
			t.Errorf("status2.Role = %v, want %v", status2.Role, RolePrimary)
		}
		if status1.Role != RolePassive {
			t.Errorf("status1.Role = %v, want %v", status1.Role, RolePassive)
		}
	}

	t.Log("status consistency verified")
}

// TestE2E_DaemonHooks tests that OnDaemonStart and OnDaemonStop are called.
func TestE2E_DaemonHooks(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping E2E test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	natsURL, cleanup := startE2ENATSContainer(ctx, t)
	defer cleanup()

	cfg := NewDefaultFileConfig("test-cluster", "node-1", []string{natsURL})
	cfg.Election.LeaseTTLMs = 3000
	cfg.Election.HeartbeatIntervalMs = 1000

	var daemonStarted atomic.Bool
	var daemonStopped atomic.Bool

	hooks := &testManagerHooks{
		onDaemonStart: func(ctx context.Context) error {
			daemonStarted.Store(true)
			return nil
		},
		onDaemonStop: func(ctx context.Context) error {
			daemonStopped.Store(true)
			return nil
		},
	}

	mgr, err := NewManager(*cfg, hooks)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.RunDaemon(ctx)
	}()

	// Wait for daemon to start
	time.Sleep(500 * time.Millisecond)

	if !daemonStarted.Load() {
		t.Error("OnDaemonStart was not called")
	}

	// Stop the daemon
	cancel()
	<-errCh

	// Give it a moment
	time.Sleep(100 * time.Millisecond)

	if !daemonStopped.Load() {
		t.Error("OnDaemonStop was not called")
	}
}

// startE2ENATSContainer is the same as startNATSContainer but for E2E tests.
func startE2ENATSContainer(ctx context.Context, t *testing.T) (string, func()) {
	t.Helper()

	container, err := natsmodule.Run(ctx, "nats:latest", testcontainers.WithCmd("--jetstream"))
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

	return url, cleanup
}

// mockVIPExecutor is a mock implementation of vip.Executor for testing.
type mockVIPExecutor struct {
	mu  sync.Mutex
	ips map[string]bool
}

func (m *mockVIPExecutor) Execute(ctx context.Context, cmd string, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.ips == nil {
		m.ips = make(map[string]bool)
	}

	// Simulate ip addr add/del commands
	if cmd == "ip" && len(args) >= 4 {
		if args[0] == "addr" && args[1] == "add" {
			// Extract IP from CIDR notation (e.g., "192.168.1.100/24")
			cidr := args[2]
			ip := cidr
			for i := 0; i < len(cidr); i++ {
				if cidr[i] == '/' {
					ip = cidr[:i]
					break
				}
			}
			m.ips[ip] = true
			return "", nil
		}
		if args[0] == "addr" && args[1] == "del" {
			cidr := args[2]
			ip := cidr
			for i := 0; i < len(cidr); i++ {
				if cidr[i] == '/' {
					ip = cidr[:i]
					break
				}
			}
			delete(m.ips, ip)
			return "", nil
		}
		if args[0] == "addr" && args[1] == "show" {
			// Return output that contains the IPs we have
			output := "inet 127.0.0.1/8 scope host lo\n"
			for ip := range m.ips {
				output += "inet " + ip + "/24 scope global eth0\n"
			}
			return output, nil
		}
	}

	// arping - just succeed
	if cmd == "arping" {
		return "", nil
	}

	return "", nil
}

func (m *mockVIPExecutor) HasIP(ip string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ips[ip]
}

var _ vip.Executor = (*mockVIPExecutor)(nil)
