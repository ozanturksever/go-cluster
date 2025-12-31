package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfig_Validate(t *testing.T) {
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
			name: "missing ClusterID",
			config: Config{
				NodeID:   "node-1",
				NATSURLs: []string{"nats://localhost:4222"},
			},
			wantErr: true,
		},
		{
			name: "missing NodeID",
			config: Config{
				ClusterID: "test-cluster",
				NATSURLs:  []string{"nats://localhost:4222"},
			},
			wantErr: true,
		},
		{
			name: "missing NATSURLs",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
			},
			wantErr: true,
		},
		{
			name: "empty NATSURLs",
			config: Config{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATSURLs:  []string{},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_applyDefaults(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}
	cfg.applyDefaults()

	if cfg.LeaseTTL != DefaultLeaseTTL {
		t.Errorf("LeaseTTL = %v, want %v", cfg.LeaseTTL, DefaultLeaseTTL)
	}
	if cfg.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("HeartbeatInterval = %v, want %v", cfg.HeartbeatInterval, DefaultHeartbeatInterval)
	}
	if cfg.ServiceVersion != DefaultServiceVersion {
		t.Errorf("ServiceVersion = %v, want %v", cfg.ServiceVersion, DefaultServiceVersion)
	}
	if cfg.ReconnectWait != DefaultReconnectWait {
		t.Errorf("ReconnectWait = %v, want %v", cfg.ReconnectWait, DefaultReconnectWait)
	}
	if cfg.MaxReconnects != DefaultMaxReconnects {
		t.Errorf("MaxReconnects = %v, want %v", cfg.MaxReconnects, DefaultMaxReconnects)
	}
	if cfg.Logger == nil {
		t.Error("Logger should have default value")
	}
}

func TestConfig_applyDefaultsPreservesCustomValues(t *testing.T) {
	cfg := Config{
		ClusterID:         "test-cluster",
		NodeID:            "node-1",
		NATSURLs:          []string{"nats://localhost:4222"},
		LeaseTTL:          20 * time.Second,
		HeartbeatInterval: 5 * time.Second,
		ServiceVersion:    "2.0.0",
		ReconnectWait:     5 * time.Second,
		MaxReconnects:     100,
	}
	cfg.applyDefaults()

	if cfg.LeaseTTL != 20*time.Second {
		t.Errorf("LeaseTTL = %v, want 20s", cfg.LeaseTTL)
	}
	if cfg.HeartbeatInterval != 5*time.Second {
		t.Errorf("HeartbeatInterval = %v, want 5s", cfg.HeartbeatInterval)
	}
	if cfg.ServiceVersion != "2.0.0" {
		t.Errorf("ServiceVersion = %v, want 2.0.0", cfg.ServiceVersion)
	}
	if cfg.ReconnectWait != 5*time.Second {
		t.Errorf("ReconnectWait = %v, want 5s", cfg.ReconnectWait)
	}
	if cfg.MaxReconnects != 100 {
		t.Errorf("MaxReconnects = %v, want 100", cfg.MaxReconnects)
	}
}

func TestFileConfig_Validate(t *testing.T) {
	tests := []struct {
		name    string
		config  FileConfig
		wantErr bool
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
			name: "invalid VIP - missing interface",
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
		},
		{
			name: "invalid VIP - bad IP",
			config: FileConfig{
				ClusterID: "test-cluster",
				NodeID:    "node-1",
				NATS: NATSFileConfig{
					Servers: []string{"nats://localhost:4222"},
				},
				VIP: VIPFileConfig{
					Address:   "not-an-ip",
					Netmask:   24,
					Interface: "eth0",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("FileConfig.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFileConfig_ApplyDefaults(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers: []string{"nats://localhost:4222"},
		},
	}
	cfg.ApplyDefaults()

	if cfg.Election.LeaseTTLMs != int64(DefaultLeaseTTL/time.Millisecond) {
		t.Errorf("Election.LeaseTTLMs = %v, want %v", cfg.Election.LeaseTTLMs, int64(DefaultLeaseTTL/time.Millisecond))
	}
	if cfg.Election.HeartbeatIntervalMs != int64(DefaultHeartbeatInterval/time.Millisecond) {
		t.Errorf("Election.HeartbeatIntervalMs = %v, want %v", cfg.Election.HeartbeatIntervalMs, int64(DefaultHeartbeatInterval/time.Millisecond))
	}
	if cfg.Service.Version != DefaultServiceVersion {
		t.Errorf("Service.Version = %v, want %v", cfg.Service.Version, DefaultServiceVersion)
	}
	if cfg.NATS.ReconnectWait != int64(DefaultReconnectWait/time.Millisecond) {
		t.Errorf("NATS.ReconnectWait = %v, want %v", cfg.NATS.ReconnectWait, int64(DefaultReconnectWait/time.Millisecond))
	}
	if cfg.NATS.MaxReconnects != DefaultMaxReconnects {
		t.Errorf("NATS.MaxReconnects = %v, want %v", cfg.NATS.MaxReconnects, DefaultMaxReconnects)
	}
}

func TestFileConfig_ToNodeConfig(t *testing.T) {
	cfg := FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATS: NATSFileConfig{
			Servers:       []string{"nats://localhost:4222"},
			Credentials:   "/path/to/creds",
			ReconnectWait: 5000,
			MaxReconnects: 100,
		},
		Election: ElectionConfig{
			LeaseTTLMs:          10000,
			HeartbeatIntervalMs: 3000,
		},
		Service: ServiceConfig{
			Version: "2.0.0",
		},
	}

	nodeCfg := cfg.ToNodeConfig(nil)

	if nodeCfg.ClusterID != "test-cluster" {
		t.Errorf("ClusterID = %v, want test-cluster", nodeCfg.ClusterID)
	}
	if nodeCfg.NodeID != "node-1" {
		t.Errorf("NodeID = %v, want node-1", nodeCfg.NodeID)
	}
	if len(nodeCfg.NATSURLs) != 1 || nodeCfg.NATSURLs[0] != "nats://localhost:4222" {
		t.Errorf("NATSURLs = %v, want [nats://localhost:4222]", nodeCfg.NATSURLs)
	}
	if nodeCfg.NATSCredentials != "/path/to/creds" {
		t.Errorf("NATSCredentials = %v, want /path/to/creds", nodeCfg.NATSCredentials)
	}
	if nodeCfg.LeaseTTL != 10*time.Second {
		t.Errorf("LeaseTTL = %v, want 10s", nodeCfg.LeaseTTL)
	}
	if nodeCfg.HeartbeatInterval != 3*time.Second {
		t.Errorf("HeartbeatInterval = %v, want 3s", nodeCfg.HeartbeatInterval)
	}
	if nodeCfg.ServiceVersion != "2.0.0" {
		t.Errorf("ServiceVersion = %v, want 2.0.0", nodeCfg.ServiceVersion)
	}
	if nodeCfg.ReconnectWait != 5*time.Second {
		t.Errorf("ReconnectWait = %v, want 5s", nodeCfg.ReconnectWait)
	}
	if nodeCfg.MaxReconnects != 100 {
		t.Errorf("MaxReconnects = %v, want 100", nodeCfg.MaxReconnects)
	}
}

func TestVIPFileConfig_CIDR(t *testing.T) {
	tests := []struct {
		name    string
		vip     VIPFileConfig
		want    string
	}{
		{
			name:    "empty address",
			vip:     VIPFileConfig{},
			want:    "",
		},
		{
			name:    "with address",
			vip:     VIPFileConfig{Address: "192.168.1.100", Netmask: 24},
			want:    "192.168.1.100/24",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.vip.CIDR(); got != tt.want {
				t.Errorf("CIDR() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestVIPFileConfig_IsConfigured(t *testing.T) {
	tests := []struct {
		name string
		vip  VIPFileConfig
		want bool
	}{
		{
			name: "not configured",
			vip:  VIPFileConfig{},
			want: false,
		},
		{
			name: "configured",
			vip:  VIPFileConfig{Address: "192.168.1.100"},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.vip.IsConfigured(); got != tt.want {
				t.Errorf("IsConfigured() = %v, want %v", got, tt.want)
			}
		})
	}
}



func TestWriteAndLoadConfigFromFile(t *testing.T) {
	// Create a temp directory
	tmpDir, err := os.MkdirTemp("", "cluster-config-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	configPath := filepath.Join(tmpDir, "config.json")

	// Create a config to write
	cfg := &FileConfig{
		ClusterID: "test-cluster",
		NodeID:    "test-node",
		NATS: NATSFileConfig{
			Servers:       []string{"nats://localhost:4222", "nats://localhost:4223"},
			Credentials:   "/path/to/creds",
			ReconnectWait: 3000,
			MaxReconnects: 50,
		},
		VIP: VIPFileConfig{
			Address:   "192.168.1.100",
			Netmask:   24,
			Interface: "eth0",
		},
		Election: ElectionConfig{
			LeaseTTLMs:          15000,
			HeartbeatIntervalMs: 5000,
		},
		Service: ServiceConfig{
			Version: "1.2.3",
		},
	}

	// Write config
	if err := WriteConfigToFile(cfg, configPath); err != nil {
		t.Fatalf("WriteConfigToFile() error = %v", err)
	}

	// Load config
	loaded, err := LoadConfigFromFile(configPath)
	if err != nil {
		t.Fatalf("LoadConfigFromFile() error = %v", err)
	}

	// Verify all fields
	if loaded.ClusterID != cfg.ClusterID {
		t.Errorf("ClusterID = %v, want %v", loaded.ClusterID, cfg.ClusterID)
	}
	if loaded.NodeID != cfg.NodeID {
		t.Errorf("NodeID = %v, want %v", loaded.NodeID, cfg.NodeID)
	}
	if len(loaded.NATS.Servers) != len(cfg.NATS.Servers) {
		t.Errorf("NATS.Servers length = %v, want %v", len(loaded.NATS.Servers), len(cfg.NATS.Servers))
	}
	if loaded.NATS.Credentials != cfg.NATS.Credentials {
		t.Errorf("NATS.Credentials = %v, want %v", loaded.NATS.Credentials, cfg.NATS.Credentials)
	}
	if loaded.NATS.ReconnectWait != cfg.NATS.ReconnectWait {
		t.Errorf("NATS.ReconnectWait = %v, want %v", loaded.NATS.ReconnectWait, cfg.NATS.ReconnectWait)
	}
	if loaded.NATS.MaxReconnects != cfg.NATS.MaxReconnects {
		t.Errorf("NATS.MaxReconnects = %v, want %v", loaded.NATS.MaxReconnects, cfg.NATS.MaxReconnects)
	}
	if loaded.VIP.Address != cfg.VIP.Address {
		t.Errorf("VIP.Address = %v, want %v", loaded.VIP.Address, cfg.VIP.Address)
	}
	if loaded.VIP.Netmask != cfg.VIP.Netmask {
		t.Errorf("VIP.Netmask = %v, want %v", loaded.VIP.Netmask, cfg.VIP.Netmask)
	}
	if loaded.VIP.Interface != cfg.VIP.Interface {
		t.Errorf("VIP.Interface = %v, want %v", loaded.VIP.Interface, cfg.VIP.Interface)
	}
	if loaded.Election.LeaseTTLMs != cfg.Election.LeaseTTLMs {
		t.Errorf("Election.LeaseTTLMs = %v, want %v", loaded.Election.LeaseTTLMs, cfg.Election.LeaseTTLMs)
	}
	if loaded.Election.HeartbeatIntervalMs != cfg.Election.HeartbeatIntervalMs {
		t.Errorf("Election.HeartbeatIntervalMs = %v, want %v", loaded.Election.HeartbeatIntervalMs, cfg.Election.HeartbeatIntervalMs)
	}
	if loaded.Service.Version != cfg.Service.Version {
		t.Errorf("Service.Version = %v, want %v", loaded.Service.Version, cfg.Service.Version)
	}
}
