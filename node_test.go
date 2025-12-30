package cluster

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestNewNode(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		hooks   Hooks
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with hooks",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			hooks:   &testHooks{},
			wantErr: false,
		},
		{
			name: "valid config with nil hooks",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			hooks:   nil,
			wantErr: false,
		},
		{
			name: "missing cluster ID",
			config: Config{
				NodeID:   "node-1",
				NATSURLs: []string{"nats://localhost:4222"},
			},
			hooks:   nil,
			wantErr: true,
			errMsg:  "invalid config: ClusterID is required",
		},
		{
			name: "missing node ID",
			config: Config{
				ClusterID: "test-cluster",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			hooks:   nil,
			wantErr: true,
			errMsg:  "invalid config: NodeID is required",
		},
		{
			name: "missing NATS URLs",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
			},
			hooks:   nil,
			wantErr: true,
			errMsg:  "invalid config: at least one NATS URL is required",
		},
		{
			name: "custom LeaseTTL and RenewInterval",
			config: Config{
				ClusterID:     "test-cluster",
				NodeID:        "node-1",
				NATSURLs:      []string{"nats://localhost:4222"},
				LeaseTTL:      20 * time.Second,
				RenewInterval: 5 * time.Second,
			},
			hooks:   nil,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node, err := NewNode(tt.config, tt.hooks)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewNode() expected error, got nil")
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("NewNode() error = %q, want %q", err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("NewNode() unexpected error: %v", err)
				return
			}
			if node == nil {
				t.Error("NewNode() returned nil node")
				return
			}
			// Verify defaults are applied
			if node.cfg.LeaseTTL == 0 {
				t.Error("NewNode() LeaseTTL should have default value")
			}
			if node.cfg.RenewInterval == 0 {
				t.Error("NewNode() RenewInterval should have default value")
			}
			if node.cfg.Logger == nil {
				t.Error("NewNode() Logger should have default value")
			}
			if node.role != RolePassive {
				t.Errorf("NewNode() initial role = %v, want RolePassive", node.role)
			}
		})
	}
}

func TestNewNodeAppliesDefaults(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	node, err := NewNode(cfg, nil)
	if err != nil {
		t.Fatalf("NewNode() unexpected error: %v", err)
	}

	if node.cfg.LeaseTTL != DefaultLeaseTTL {
		t.Errorf("NewNode() LeaseTTL = %v, want %v", node.cfg.LeaseTTL, DefaultLeaseTTL)
	}
	if node.cfg.RenewInterval != DefaultRenewInterval {
		t.Errorf("NewNode() RenewInterval = %v, want %v", node.cfg.RenewInterval, DefaultRenewInterval)
	}
	if node.cfg.Logger == nil {
		t.Error("NewNode() Logger should not be nil")
	}
}

func TestNewNodePreservesCustomConfig(t *testing.T) {
	customLogger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := Config{
		ClusterID:     "test-cluster",
		NodeID:        "node-1",
		NATSURLs:      []string{"nats://localhost:4222"},
		LeaseTTL:      30 * time.Second,
		RenewInterval: 10 * time.Second,
		Logger:        customLogger,
	}

	node, err := NewNode(cfg, nil)
	if err != nil {
		t.Fatalf("NewNode() unexpected error: %v", err)
	}

	if node.cfg.LeaseTTL != 30*time.Second {
		t.Errorf("NewNode() LeaseTTL = %v, want 30s", node.cfg.LeaseTTL)
	}
	if node.cfg.RenewInterval != 10*time.Second {
		t.Errorf("NewNode() RenewInterval = %v, want 10s", node.cfg.RenewInterval)
	}
}

func TestNewNodeWithNilHooksUsesNoOp(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	node, err := NewNode(cfg, nil)
	if err != nil {
		t.Fatalf("NewNode() unexpected error: %v", err)
	}

	// Should not panic when calling hooks
	ctx := context.Background()
	if err := node.hooks.OnBecomeLeader(ctx); err != nil {
		t.Errorf("OnBecomeLeader() on default hooks error = %v", err)
	}
	if err := node.hooks.OnLoseLeadership(ctx); err != nil {
		t.Errorf("OnLoseLeadership() on default hooks error = %v", err)
	}
	if err := node.hooks.OnLeaderChange(ctx, "node-2"); err != nil {
		t.Errorf("OnLeaderChange() on default hooks error = %v", err)
	}
}

func TestNodeRole(t *testing.T) {
	node := &Node{
		role: RolePassive,
	}

	if node.Role() != RolePassive {
		t.Errorf("Role() = %v, want RolePassive", node.Role())
	}

	node.mu.Lock()
	node.role = RolePrimary
	node.mu.Unlock()

	if node.Role() != RolePrimary {
		t.Errorf("Role() = %v, want RolePrimary", node.Role())
	}
}

func TestNodeIsLeader(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{"passive node is not leader", RolePassive, false},
		{"primary node is leader", RolePrimary, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{role: tt.role}
			if got := node.IsLeader(); got != tt.want {
				t.Errorf("IsLeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeLeader(t *testing.T) {
	tests := []struct {
		name  string
		lease *Lease
		want  string
	}{
		{
			name:  "nil lease returns empty",
			lease: nil,
			want:  "",
		},
		{
			name:  "lease with node ID returns ID",
			lease: &Lease{NodeID: "node-leader"},
			want:  "node-leader",
		},
		{
			name:  "lease with empty node ID returns empty",
			lease: &Lease{NodeID: ""},
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{lease: tt.lease}
			if got := node.Leader(); got != tt.want {
				t.Errorf("Leader() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNodeEpoch(t *testing.T) {
	tests := []struct {
		name  string
		lease *Lease
		want  int64
	}{
		{
			name:  "nil lease returns 0",
			lease: nil,
			want:  0,
		},
		{
			name:  "lease with epoch returns epoch",
			lease: &Lease{Epoch: 42},
			want:  42,
		},
		{
			name:  "lease with epoch 0 returns 0",
			lease: &Lease{Epoch: 0},
			want:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &Node{lease: tt.lease}
			if got := node.Epoch(); got != tt.want {
				t.Errorf("Epoch() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestNodeStopNotStarted(t *testing.T) {
	node := &Node{
		running: false,
	}

	err := node.Stop(context.Background())
	if err != ErrNotStarted {
		t.Errorf("Stop() error = %v, want ErrNotStarted", err)
	}
}

func TestNodeStartAlreadyStarted(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}
	cfg.applyDefaults()

	node := &Node{
		cfg:     cfg,
		hooks:   NoOpHooks{},
		running: true,
	}

	err := node.Start(context.Background())
	if err != ErrAlreadyStarted {
		t.Errorf("Start() error = %v, want ErrAlreadyStarted", err)
	}
}

func TestNodeStepDownNotLeader(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}
	cfg.applyDefaults()

	node := &Node{
		cfg:   cfg,
		hooks: NoOpHooks{},
		role:  RolePassive,
	}

	err := node.StepDown(context.Background())
	if err != ErrNotLeader {
		t.Errorf("StepDown() error = %v, want ErrNotLeader", err)
	}
}

func TestNodeConcurrentAccess(t *testing.T) {
	node := &Node{
		role:  RolePassive,
		lease: &Lease{NodeID: "node-1", Epoch: 1},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(4)

		go func() {
			defer wg.Done()
			_ = node.Role()
		}()

		go func() {
			defer wg.Done()
			_ = node.IsLeader()
		}()

		go func() {
			defer wg.Done()
			_ = node.Leader()
		}()

		go func() {
			defer wg.Done()
			_ = node.Epoch()
		}()
	}

	wg.Wait()
}

// testHooks is a test implementation of Hooks interface
type testHooks struct {
	mu                  sync.Mutex
	becomeLeaderCalls   int
	loseLeadershipCalls int
	leaderChangeCalls   int
	lastLeaderID        string
}

func (h *testHooks) OnBecomeLeader(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.becomeLeaderCalls++
	return nil
}

func (h *testHooks) OnLoseLeadership(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.loseLeadershipCalls++
	return nil
}

func (h *testHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.leaderChangeCalls++
	h.lastLeaderID = nodeID
	return nil
}
