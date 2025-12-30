package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFileConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  FileConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
			},
			wantErr: false,
		},
		{
			name: "valid config with VIP",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
				VIP: VIPFileConfig{
					Address:   "192.168.1.100",
					Netmask:   24,
					Interface: "eth0",
				},
			},
			wantErr: false,
		},
		{
			name: "missing cluster ID",
			config: FileConfig{
				NodeID: "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
			},
			wantErr: true,
			errMsg:  "clusterId is required",
		},
		{
			name: "missing node ID",
			config: FileConfig{
				ClusterID: "test-cluster",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
			},
			wantErr: true,
			errMsg:  "nodeId is required",
		},
		{
			name: "missing NATS servers",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
			},
			wantErr: true,
			errMsg:  "nats.servers is required",
		},
		{
			name: "invalid VIP address",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
				VIP: VIPFileConfig{
					Address:   "invalid-ip",
					Netmask:   24,
					Interface: "eth0",
				},
			},
			wantErr: true,
			errMsg:  "vip.address is not a valid IP address",
		},
		{
			name: "VIP missing interface",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
				VIP: VIPFileConfig{
					Address: "192.168.1.100",
					Netmask: 24,
				},
			},
			wantErr: true,
			errMsg:  "vip.interface is required when vip.address is set",
		},
		{
			name: "invalid netmask",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
				VIP: VIPFileConfig{
					Address:   "192.168.1.100",
					Netmask:   33,
					Interface: "eth0",
				},
			},
			wantErr: true,
			errMsg:  "vip.netmask must be between 0 and 32",
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

func TestFileConfigApplyDefaults(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers: []string{"nats://localhost:4222"},
		},
	}

	cfg.ApplyDefaults()

	expectedLeaseTTL := int64(DefaultLeaseTTL / time.Millisecond)
	if cfg.Election.LeaseTTLMs != expectedLeaseTTL {
		t.Errorf("ApplyDefaults() LeaseTTLMs = %v, want %v", cfg.Election.LeaseTTLMs, expectedLeaseTTL)
	}

	expectedRenewInterval := int64(DefaultRenewInterval / time.Millisecond)
	if cfg.Election.RenewIntervalMs != expectedRenewInterval {
		t.Errorf("ApplyDefaults() RenewIntervalMs = %v, want %v", cfg.Election.RenewIntervalMs, expectedRenewInterval)
	}
}

func TestFileConfigApplyDefaultsWithVIP(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers: []string{"nats://localhost:4222"},
		},
		VIP: VIPFileConfig{
			Address:   "192.168.1.100",
			Interface: "eth0",
		},
	}

	cfg.ApplyDefaults()

	if cfg.VIP.Netmask != 24 {
		t.Errorf("ApplyDefaults() VIP.Netmask = %v, want 24", cfg.VIP.Netmask)
	}
}

func TestLoadConfigFromFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	jsonContent := `{
  "clusterId": "test-cluster",
  "nodeId": "node-1",
  "nats": {
    "servers": ["nats://localhost:4222"],
    "credentials": "/path/to/creds"
  },
  "vip": {
    "address": "192.168.1.100",
    "netmask": 24,
    "interface": "eth0"
  },
  "election": {
    "leaseTtlMs": 15000,
    "renewIntervalMs": 5000
  }
}`

	if err := os.WriteFile(configPath, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	cfg, err := LoadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromFile() error = %v", err)
	}

	if cfg.ClusterID != "test-cluster" {
		t.Errorf("ClusterID = %q, want %q", cfg.ClusterID, "test-cluster")
	}
	if cfg.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", cfg.NodeID, "node-1")
	}
	if len(cfg.NATS.Servers) != 1 || cfg.NATS.Servers[0] != "nats://localhost:4222" {
		t.Errorf("NATS.Servers = %v, want [nats://localhost:4222]", cfg.NATS.Servers)
	}
	if cfg.NATS.Credentials != "/path/to/creds" {
		t.Errorf("NATS.Credentials = %q, want %q", cfg.NATS.Credentials, "/path/to/creds")
	}
	if cfg.VIP.Address != "192.168.1.100" {
		t.Errorf("VIP.Address = %q, want %q", cfg.VIP.Address, "192.168.1.100")
	}
	if cfg.VIP.Netmask != 24 {
		t.Errorf("VIP.Netmask = %d, want 24", cfg.VIP.Netmask)
	}
	if cfg.VIP.Interface != "eth0" {
		t.Errorf("VIP.Interface = %q, want %q", cfg.VIP.Interface, "eth0")
	}
	if cfg.Election.LeaseTTLMs != 15000 {
		t.Errorf("Election.LeaseTTLMs = %d, want 15000", cfg.Election.LeaseTTLMs)
	}
	if cfg.Election.RenewIntervalMs != 5000 {
		t.Errorf("Election.RenewIntervalMs = %d, want 5000", cfg.Election.RenewIntervalMs)
	}
}

func TestLoadConfigFromFileNotFound(t *testing.T) {
	_, err := LoadConfigFromFile("/nonexistent/path/config.json")
	if err == nil {
		t.Error("LoadConfigFromFile() expected error for nonexistent file")
	}
}

func TestLoadConfigFromFileInvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	if err := os.WriteFile(configPath, []byte("not valid json"), 0644); err != nil {
		t.Fatalf("failed to write test config: %v", err)
	}

	_, err := LoadConfigFromFile(configPath)
	if err == nil {
		t.Error("LoadConfigFromFile() expected error for invalid JSON")
	}
}

func TestWriteConfigToFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "subdir", "config.json")

	cfg := &FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers: []string{"nats://localhost:4222"},
		},
		VIP: VIPFileConfig{
			Address:   "192.168.1.100",
			Netmask:   24,
			Interface: "eth0",
		},
		Election: ElectionConfig{
			LeaseTTLMs:      10000,
			RenewIntervalMs: 3000,
		},
	}

	if err := WriteConfigToFile(cfg, configPath); err != nil {
		t.Fatalf("WriteConfigToFile() error = %v", err)
	}

	// Read it back
	loaded, err := LoadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromFile() error = %v", err)
	}

	if loaded.ClusterID != cfg.ClusterID {
		t.Errorf("ClusterID = %q, want %q", loaded.ClusterID, cfg.ClusterID)
	}
	if loaded.NodeID != cfg.NodeID {
		t.Errorf("NodeID = %q, want %q", loaded.NodeID, cfg.NodeID)
	}
	if loaded.VIP.Address != cfg.VIP.Address {
		t.Errorf("VIP.Address = %q, want %q", loaded.VIP.Address, cfg.VIP.Address)
	}
}

func TestToNodeConfig(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers:     []string{"nats://localhost:4222"},
			Credentials: "/path/to/creds",
		},
		Election: ElectionConfig{
			LeaseTTLMs:      15000,
			RenewIntervalMs: 5000,
		},
	}

	nodeCfg := cfg.ToNodeConfig(nil)

	if nodeCfg.ClusterID != "test-cluster" {
		t.Errorf("ClusterID = %q, want %q", nodeCfg.ClusterID, "test-cluster")
	}
	if nodeCfg.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", nodeCfg.NodeID, "node-1")
	}
	if len(nodeCfg.NATSURLs) != 1 || nodeCfg.NATSURLs[0] != "nats://localhost:4222" {
		t.Errorf("NATSURLs = %v, want [nats://localhost:4222]", nodeCfg.NATSURLs)
	}
	if nodeCfg.NATSCredentials != "/path/to/creds" {
		t.Errorf("NATSCredentials = %q, want %q", nodeCfg.NATSCredentials, "/path/to/creds")
	}
	if nodeCfg.LeaseTTL != 15*time.Second {
		t.Errorf("LeaseTTL = %v, want %v", nodeCfg.LeaseTTL, 15*time.Second)
	}
	if nodeCfg.RenewInterval != 5*time.Second {
		t.Errorf("RenewInterval = %v, want %v", nodeCfg.RenewInterval, 5*time.Second)
	}
}

func TestNewDefaultFileConfig(t *testing.T) {
	cfg := NewDefaultFileConfig("my-cluster", "node-1", []string{"nats://localhost:4222"})

	if cfg.ClusterID != "my-cluster" {
		t.Errorf("ClusterID = %q, want %q", cfg.ClusterID, "my-cluster")
	}
	if cfg.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", cfg.NodeID, "node-1")
	}
	if len(cfg.NATS.Servers) != 1 {
		t.Errorf("NATS.Servers length = %d, want 1", len(cfg.NATS.Servers))
	}

	// Defaults should be applied
	expectedLeaseTTL := int64(DefaultLeaseTTL / time.Millisecond)
	if cfg.Election.LeaseTTLMs != expectedLeaseTTL {
		t.Errorf("Election.LeaseTTLMs = %d, want %d", cfg.Election.LeaseTTLMs, expectedLeaseTTL)
	}
}

func TestVIPFileConfigCIDR(t *testing.T) {
	tests := []struct {
		name     string
		vip      VIPFileConfig
		expected string
	}{
		{
			name: "valid VIP",
			vip: VIPFileConfig{
				Address: "192.168.1.100",
				Netmask: 24,
			},
			expected: "192.168.1.100/24",
		},
		{
			name:     "empty VIP",
			vip:      VIPFileConfig{},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.vip.CIDR()
			if result != tt.expected {
				t.Errorf("CIDR() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestVIPFileConfigIsConfigured(t *testing.T) {
	tests := []struct {
		name     string
		vip      VIPFileConfig
		expected bool
	}{
		{
			name: "configured",
			vip: VIPFileConfig{
				Address: "192.168.1.100",
			},
			expected: true,
		},
		{
			name:     "not configured",
			vip:      VIPFileConfig{},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.vip.IsConfigured()
			if result != tt.expected {
				t.Errorf("IsConfigured() = %v, want %v", result, tt.expected)
			}
		})
	}
}
