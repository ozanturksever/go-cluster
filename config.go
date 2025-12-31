package cluster

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	DefaultLeaseTTL          = 10 * time.Second
	DefaultHeartbeatInterval = 3 * time.Second
	DefaultReconnectWait     = 2 * time.Second
	DefaultMaxReconnects     = -1 // Unlimited
	DefaultServiceVersion    = "1.0.0"
)

// Config configures the cluster node.
type Config struct {
	ClusterID       string
	NodeID          string
	NATSURLs        []string
	NATSCredentials string

	// Timing configuration
	LeaseTTL          time.Duration
	HeartbeatInterval time.Duration

	// Service configuration
	ServiceVersion string

	// Connection resilience configuration
	ReconnectWait time.Duration
	MaxReconnects int

	Logger *slog.Logger
}

func (c *Config) Validate() error {
	if c.ClusterID == "" {
		return fmt.Errorf("ClusterID is required")
	}
	if c.NodeID == "" {
		return fmt.Errorf("NodeID is required")
	}
	if len(c.NATSURLs) == 0 {
		return fmt.Errorf("at least one NATS URL is required")
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.LeaseTTL == 0 {
		c.LeaseTTL = DefaultLeaseTTL
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = DefaultHeartbeatInterval
	}
	if c.ServiceVersion == "" {
		c.ServiceVersion = DefaultServiceVersion
	}
	if c.ReconnectWait == 0 {
		c.ReconnectWait = DefaultReconnectWait
	}
	if c.MaxReconnects == 0 {
		c.MaxReconnects = DefaultMaxReconnects
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// KVBucketName returns the KV bucket name for this cluster.
func (c *Config) KVBucketName() string {
	return fmt.Sprintf("KV_CLUSTER_%s", c.ClusterID)
}

// ServiceName returns the micro service name for this cluster.
// Uses underscores instead of dots since NATS micro service names must be alphanumeric with dashes/underscores.
func (c *Config) ServiceName() string {
	return fmt.Sprintf("cluster_%s", c.ClusterID)
}

// FileConfig represents the cluster configuration loaded from a JSON file.
// This is the user-facing configuration format that gets converted to the internal Config.
type FileConfig struct {
	ClusterID string         `json:"clusterId"`
	NodeID    string         `json:"nodeId"`
	NATS      NATSFileConfig `json:"nats"`
	VIP       VIPFileConfig  `json:"vip,omitempty"`
	Election  ElectionConfig `json:"election,omitempty"`
	Service   ServiceConfig  `json:"service,omitempty"`
}

// NATSFileConfig contains NATS connection settings.
type NATSFileConfig struct {
	Servers       []string `json:"servers"`
	Credentials   string   `json:"credentials,omitempty"`
	ReconnectWait int64    `json:"reconnectWaitMs,omitempty"`
	MaxReconnects int      `json:"maxReconnects,omitempty"`
}

// VIPFileConfig contains Virtual IP settings.
type VIPFileConfig struct {
	Address   string `json:"address,omitempty"`
	Netmask   int    `json:"netmask,omitempty"`
	Interface string `json:"interface,omitempty"`
}

// CIDR returns the VIP address in CIDR notation.
func (v VIPFileConfig) CIDR() string {
	if v.Address == "" {
		return ""
	}
	return fmt.Sprintf("%s/%d", v.Address, v.Netmask)
}

// IsConfigured returns true if VIP settings are configured.
func (v VIPFileConfig) IsConfigured() bool {
	return v.Address != ""
}

// ElectionConfig contains leader election settings.
type ElectionConfig struct {
	LeaseTTLMs          int64 `json:"leaseTtlMs,omitempty"`
	HeartbeatIntervalMs int64 `json:"heartbeatIntervalMs,omitempty"`
}

// ServiceConfig contains micro service settings.
type ServiceConfig struct {
	Version string `json:"version,omitempty"`
}

// rawFileConfig is used for JSON unmarshaling.
type rawFileConfig struct {
	ClusterID string `json:"clusterId"`
	NodeID    string `json:"nodeId"`
	NATS      struct {
		Servers       []string `json:"servers"`
		Credentials   string   `json:"credentials,omitempty"`
		ReconnectWait int64    `json:"reconnectWaitMs,omitempty"`
		MaxReconnects int      `json:"maxReconnects,omitempty"`
	} `json:"nats"`
	VIP struct {
		Address   string `json:"address,omitempty"`
		Netmask   int    `json:"netmask,omitempty"`
		Interface string `json:"interface,omitempty"`
	} `json:"vip,omitempty"`
	Election struct {
		LeaseTTLMs          int64 `json:"leaseTtlMs,omitempty"`
		HeartbeatIntervalMs int64 `json:"heartbeatIntervalMs,omitempty"`
	} `json:"election,omitempty"`
	Service struct {
		Version string `json:"version,omitempty"`
	} `json:"service,omitempty"`
}

// LoadConfigFromFile loads configuration from a JSON file.
func LoadConfigFromFile(path string) (*FileConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var raw rawFileConfig
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	cfg := &FileConfig{
		ClusterID: raw.ClusterID,
		NodeID:    raw.NodeID,
		NATS: NATSFileConfig{
			Servers:       raw.NATS.Servers,
			Credentials:   raw.NATS.Credentials,
			ReconnectWait: raw.NATS.ReconnectWait,
			MaxReconnects: raw.NATS.MaxReconnects,
		},
		VIP: VIPFileConfig{
			Address:   raw.VIP.Address,
			Netmask:   raw.VIP.Netmask,
			Interface: raw.VIP.Interface,
		},
		Election: ElectionConfig{
			LeaseTTLMs:          raw.Election.LeaseTTLMs,
			HeartbeatIntervalMs: raw.Election.HeartbeatIntervalMs,
		},
		Service: ServiceConfig{
			Version: raw.Service.Version,
		},
	}

	return cfg, nil
}

// WriteConfigToFile writes the configuration to a JSON file.
func WriteConfigToFile(cfg *FileConfig, path string) error {
	// Ensure parent directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate validates the configuration.
func (c *FileConfig) Validate() error {
	if c.ClusterID == "" {
		return fmt.Errorf("clusterId is required")
	}
	if c.NodeID == "" {
		return fmt.Errorf("nodeId is required")
	}
	if len(c.NATS.Servers) == 0 {
		return fmt.Errorf("nats.servers is required")
	}

	// Validate VIP if configured
	if c.VIP.Address != "" {
		ip := net.ParseIP(c.VIP.Address)
		if ip == nil {
			return fmt.Errorf("vip.address is not a valid IP address")
		}
		if c.VIP.Netmask < 0 || c.VIP.Netmask > 32 {
			return fmt.Errorf("vip.netmask must be between 0 and 32")
		}
		if c.VIP.Interface == "" {
			return fmt.Errorf("vip.interface is required when vip.address is set")
		}
	}

	return nil
}

// ApplyDefaults applies default values to unset configuration fields.
func (c *FileConfig) ApplyDefaults() {
	if c.Election.LeaseTTLMs == 0 {
		c.Election.LeaseTTLMs = int64(DefaultLeaseTTL / time.Millisecond)
	}
	if c.Election.HeartbeatIntervalMs == 0 {
		c.Election.HeartbeatIntervalMs = int64(DefaultHeartbeatInterval / time.Millisecond)
	}
	if c.VIP.Netmask == 0 && c.VIP.Address != "" {
		c.VIP.Netmask = 24
	}
	if c.NATS.ReconnectWait == 0 {
		c.NATS.ReconnectWait = int64(DefaultReconnectWait / time.Millisecond)
	}
	if c.NATS.MaxReconnects == 0 {
		c.NATS.MaxReconnects = DefaultMaxReconnects
	}
	if c.Service.Version == "" {
		c.Service.Version = DefaultServiceVersion
	}
}

// ToNodeConfig converts FileConfig to the internal Config used by Node.
func (c *FileConfig) ToNodeConfig(logger *slog.Logger) Config {
	return Config{
		ClusterID:         c.ClusterID,
		NodeID:            c.NodeID,
		NATSURLs:          c.NATS.Servers,
		NATSCredentials:   c.NATS.Credentials,
		LeaseTTL:          time.Duration(c.Election.LeaseTTLMs) * time.Millisecond,
		HeartbeatInterval: time.Duration(c.Election.HeartbeatIntervalMs) * time.Millisecond,
		ServiceVersion:    c.Service.Version,
		ReconnectWait:     time.Duration(c.NATS.ReconnectWait) * time.Millisecond,
		MaxReconnects:     c.NATS.MaxReconnects,
		Logger:            logger,
	}
}

// NewDefaultFileConfig creates a new FileConfig with the given required fields and default values.
func NewDefaultFileConfig(clusterID, nodeID string, natsServers []string) *FileConfig {
	cfg := &FileConfig{
		ClusterID: clusterID,
		NodeID:    nodeID,
		NATS: NATSFileConfig{
			Servers: natsServers,
		},
	}
	cfg.ApplyDefaults()
	return cfg
}
