package cluster

import (
	"testing"
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
			name: "missing cluster ID",
			config: Config{
				NodeID:   "node-1",
				NATSURLs: []string{"nats://localhost:4222"},
			},
			wantErr: true,
			errMsg:  "ClusterID is required",
		},
		{
			name: "missing node ID",
			config: Config{
				ClusterID: "test-cluster",
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

func TestConfigApplyDefaults(t *testing.T) {
	cfg := Config{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		NATSURLs:  []string{"nats://localhost:4222"},
	}

	cfg.applyDefaults()

	if cfg.LeaseTTL != DefaultLeaseTTL {
		t.Errorf("applyDefaults() LeaseTTL = %v, want %v", cfg.LeaseTTL, DefaultLeaseTTL)
	}

	if cfg.RenewInterval != DefaultRenewInterval {
		t.Errorf("applyDefaults() RenewInterval = %v, want %v", cfg.RenewInterval, DefaultRenewInterval)
	}

	if cfg.Logger == nil {
		t.Error("applyDefaults() Logger should not be nil")
	}
}
