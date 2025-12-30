package health

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: false,
		},
		{
			name: "valid config with all fields",
			config: Config{
				ClusterID:       "test-cluster",
				NodeID:          "node-1",
				NATSURLs:        []string{"nats://localhost:4222", "nats://localhost:4223"},
				NATSCredentials: "/path/to/creds",
				Logger:          slog.Default(),
			},
			wantErr: false,
		},
		{
			name: "missing cluster ID",
			config: Config{
				ClusterID: "",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: true,
			errMsg:  "ClusterID is required",
		},
		{
			name: "missing node ID",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: true,
			errMsg:  "NodeID is required",
		},
		{
			name: "missing NATS URLs",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  nil,
			},
			wantErr: true,
			errMsg:  "at least one NATS URL is required",
		},
		{
			name: "empty NATS URLs slice",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{},
			},
			wantErr: true,
			errMsg:  "at least one NATS URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestNewChecker(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name: "valid config",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: false,
		},
		{
			name: "valid config with custom logger",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{"nats://localhost:4222"},
				Logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
			},
			wantErr: false,
		},
		{
			name: "invalid config - missing cluster ID",
			config: Config{
				NodeID:   "node-1",
				NATSURLs: []string{"nats://localhost:4222"},
			},
			wantErr: true,
		},
		{
			name: "invalid config - missing node ID",
			config: Config{
				ClusterID: "test-cluster",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: true,
		},
		{
			name: "invalid config - missing NATS URLs",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker, err := NewChecker(tt.config)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewChecker() expected error, got nil")
				}
				if checker != nil {
					t.Errorf("NewChecker() expected nil checker on error")
				}
			} else {
				if err != nil {
					t.Errorf("NewChecker() unexpected error: %v", err)
				}
				if checker == nil {
					t.Errorf("NewChecker() expected non-nil checker")
				}
			}
		})
	}
}

func TestNewCheckerSubject(t *testing.T) {
	cfg := Config{
		ClusterID: "my-cluster",
		NodeID:    "my-node",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	checker, err := NewChecker(cfg)
	if err != nil {
		t.Fatalf("NewChecker() unexpected error: %v", err)
	}

	expectedSubject := "cluster.my-cluster.health.my-node"
	if checker.subject != expectedSubject {
		t.Errorf("NewChecker() subject = %q, want %q", checker.subject, expectedSubject)
	}
}

func TestNewCheckerDefaultLogger(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
		Logger:    nil,
	}

	checker, err := NewChecker(cfg)
	if err != nil {
		t.Fatalf("NewChecker() unexpected error: %v", err)
	}

	if checker.logger == nil {
		t.Error("NewChecker() logger should not be nil when Config.Logger is nil")
	}
}

func TestCheckerSetRole(t *testing.T) {
	checker := &Checker{
		custom: make(map[string]any),
	}

	tests := []struct {
		name string
		role string
	}{
		{"primary role", "PRIMARY"},
		{"passive role", "PASSIVE"},
		{"empty role", ""},
		{"custom role", "LEADER"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker.SetRole(tt.role)
			if checker.role != tt.role {
				t.Errorf("SetRole() role = %q, want %q", checker.role, tt.role)
			}
		})
	}
}

func TestCheckerSetLeader(t *testing.T) {
	checker := &Checker{
		custom: make(map[string]any),
	}

	tests := []struct {
		name   string
		leader string
	}{
		{"set leader", "node-1"},
		{"empty leader", ""},
		{"another leader", "node-2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker.SetLeader(tt.leader)
			if checker.leader != tt.leader {
				t.Errorf("SetLeader() leader = %q, want %q", checker.leader, tt.leader)
			}
		})
	}
}

func TestCheckerSetCustom(t *testing.T) {
	checker := &Checker{
		custom: make(map[string]any),
	}

	tests := []struct {
		name  string
		key   string
		value any
	}{
		{"string value", "version", "1.0.0"},
		{"int value", "connections", 42},
		{"bool value", "healthy", true},
		{"float value", "loadAvg", 0.75},
		{"nil value", "emptyField", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			checker.SetCustom(tt.key, tt.value)
			if checker.custom[tt.key] != tt.value {
				t.Errorf("SetCustom() custom[%q] = %v, want %v", tt.key, checker.custom[tt.key], tt.value)
			}
		})
	}

	// Verify all keys are set
	if len(checker.custom) != len(tests) {
		t.Errorf("SetCustom() custom map has %d entries, want %d", len(checker.custom), len(tests))
	}
}

func TestCheckerSetCustomOverwrite(t *testing.T) {
	checker := &Checker{
		custom: make(map[string]any),
	}

	// Set initial value
	checker.SetCustom("key", "initial")
	if checker.custom["key"] != "initial" {
		t.Errorf("SetCustom() initial value = %v, want %q", checker.custom["key"], "initial")
	}

	// Overwrite with new value
	checker.SetCustom("key", "updated")
	if checker.custom["key"] != "updated" {
		t.Errorf("SetCustom() overwritten value = %v, want %q", checker.custom["key"], "updated")
	}
}

func TestCheckerBuildResponse(t *testing.T) {
	checker := &Checker{
		cfg: Config{
			NodeID: "test-node",
		},
		role:      "PRIMARY",
		leader:    "test-node",
		startedAt: time.Now().Add(-5 * time.Second),
		custom:    make(map[string]any),
	}
	checker.custom["version"] = "1.0.0"
	checker.custom["connections"] = 10

	resp := checker.buildResponse()

	// Verify NodeID
	if resp.NodeID != "test-node" {
		t.Errorf("buildResponse() NodeID = %q, want %q", resp.NodeID, "test-node")
	}

	// Verify Role
	if resp.Role != "PRIMARY" {
		t.Errorf("buildResponse() Role = %q, want %q", resp.Role, "PRIMARY")
	}

	// Verify Leader
	if resp.Leader != "test-node" {
		t.Errorf("buildResponse() Leader = %q, want %q", resp.Leader, "test-node")
	}

	// Verify UptimeMs is approximately correct (5 seconds = 5000ms, allow some margin)
	if resp.UptimeMs < 4900 || resp.UptimeMs > 6000 {
		t.Errorf("buildResponse() UptimeMs = %d, want approximately 5000", resp.UptimeMs)
	}

	// Verify Timestamp is recent
	now := time.Now().UnixMilli()
	if resp.Timestamp < now-1000 || resp.Timestamp > now+1000 {
		t.Errorf("buildResponse() Timestamp = %d, want approximately %d", resp.Timestamp, now)
	}

	// Verify Custom fields
	if len(resp.Custom) != 2 {
		t.Errorf("buildResponse() Custom has %d entries, want 2", len(resp.Custom))
	}
	if resp.Custom["version"] != "1.0.0" {
		t.Errorf("buildResponse() Custom[version] = %v, want %q", resp.Custom["version"], "1.0.0")
	}
	if resp.Custom["connections"] != 10 {
		t.Errorf("buildResponse() Custom[connections] = %v, want 10", resp.Custom["connections"])
	}
}

func TestCheckerBuildResponseEmptyCustom(t *testing.T) {
	checker := &Checker{
		cfg: Config{
			NodeID: "test-node",
		},
		role:      "PASSIVE",
		leader:    "other-node",
		startedAt: time.Now(),
		custom:    make(map[string]any),
	}

	resp := checker.buildResponse()

	if len(resp.Custom) != 0 {
		t.Errorf("buildResponse() Custom should be empty, got %d entries", len(resp.Custom))
	}
}

func TestCheckerBuildResponseZeroStartedAt(t *testing.T) {
	checker := &Checker{
		cfg: Config{
			NodeID: "test-node",
		},
		startedAt: time.Time{}, // zero value
		custom:    make(map[string]any),
	}

	resp := checker.buildResponse()

	if resp.UptimeMs != 0 {
		t.Errorf("buildResponse() UptimeMs = %d, want 0 for zero startedAt", resp.UptimeMs)
	}
}

func TestCheckerBuildResponseCustomCopy(t *testing.T) {
	checker := &Checker{
		cfg: Config{
			NodeID: "test-node",
		},
		startedAt: time.Now(),
		custom:    make(map[string]any),
	}
	checker.custom["key"] = "original"

	resp := checker.buildResponse()

	// Modify the response custom map
	resp.Custom["key"] = "modified"
	resp.Custom["newkey"] = "newvalue"

	// Original checker custom should not be affected
	if checker.custom["key"] != "original" {
		t.Errorf("buildResponse() should copy custom map, original was modified")
	}
	if _, exists := checker.custom["newkey"]; exists {
		t.Error("buildResponse() should copy custom map, new key was added to original")
	}
}

func TestCheckerConcurrentAccess(t *testing.T) {
	checker := &Checker{
		cfg: Config{
			NodeID: "test-node",
		},
		startedAt: time.Now(),
		custom:    make(map[string]any),
	}

	var wg sync.WaitGroup

	// Concurrent setters and getters
	for i := 0; i < 100; i++ {
		wg.Add(4)

		go func(n int) {
			defer wg.Done()
			checker.SetRole("ROLE" + string(rune('A'+n%26)))
		}(i)

		go func(n int) {
			defer wg.Done()
			checker.SetLeader("node-" + string(rune('0'+n%10)))
		}(i)

		go func(n int) {
			defer wg.Done()
			checker.SetCustom("key"+string(rune('0'+n%10)), n)
		}(i)

		go func() {
			defer wg.Done()
			_ = checker.buildResponse()
		}()
	}

	wg.Wait()
	// If no race conditions occur, the test passes
}

func TestCheckerStopWithoutStart(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "test-node",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	checker, err := NewChecker(cfg)
	if err != nil {
		t.Fatalf("NewChecker() error: %v", err)
	}

	// Should not panic when stopping a checker that was never started
	checker.Stop()
}

func TestResponseStructure(t *testing.T) {
	resp := Response{
		NodeID:    "node-1",
		Role:      "PRIMARY",
		Leader:    "node-1",
		UptimeMs:  12345,
		Timestamp: time.Now().UnixMilli(),
		Custom: map[string]any{
			"version": "1.0.0",
		},
	}

	if resp.NodeID != "node-1" {
		t.Errorf("Response.NodeID = %q, want %q", resp.NodeID, "node-1")
	}
	if resp.Role != "PRIMARY" {
		t.Errorf("Response.Role = %q, want %q", resp.Role, "PRIMARY")
	}
	if resp.Leader != "node-1" {
		t.Errorf("Response.Leader = %q, want %q", resp.Leader, "node-1")
	}
	if resp.UptimeMs != 12345 {
		t.Errorf("Response.UptimeMs = %d, want 12345", resp.UptimeMs)
	}
	if resp.Custom["version"] != "1.0.0" {
		t.Errorf("Response.Custom[version] = %v, want %q", resp.Custom["version"], "1.0.0")
	}
}

func TestQueryNodeEmptyNodeID(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	checker, err := NewChecker(cfg)
	if err != nil {
		t.Fatalf("NewChecker() error: %v", err)
	}

	_, err = checker.QueryNode(context.Background(), "", time.Second)
	if err == nil {
		t.Error("QueryNode() with empty nodeID should return error")
	}
}
