package cluster

import (
	"encoding/json"
	"testing"
	"time"
)

func TestStatusIsLeader(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   bool
	}{
		{
			name:   "primary role is leader",
			status: Status{Role: RolePrimary},
			want:   true,
		},
		{
			name:   "passive role is not leader",
			status: Status{Role: RolePassive},
			want:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.IsLeader(); got != tt.want {
				t.Errorf("IsLeader() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusString(t *testing.T) {
	tests := []struct {
		name   string
		status Status
		want   string
	}{
		{
			name:   "primary role",
			status: Status{Role: RolePrimary},
			want:   "PRIMARY",
		},
		{
			name:   "passive role",
			status: Status{Role: RolePassive},
			want:   "PASSIVE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.status.String(); got != tt.want {
				t.Errorf("String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStatusFields(t *testing.T) {
	status := Status{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		Role:      RolePrimary,
		Leader:    "node-1",
		Epoch:     5,
		VIPHeld:   true,
		Uptime:    10 * time.Second,
		Connected: true,
	}

	if status.ClusterID != "test-cluster" {
		t.Errorf("ClusterID = %q, want %q", status.ClusterID, "test-cluster")
	}
	if status.NodeID != "node-1" {
		t.Errorf("NodeID = %q, want %q", status.NodeID, "node-1")
	}
	if status.Role != RolePrimary {
		t.Errorf("Role = %v, want %v", status.Role, RolePrimary)
	}
	if status.Leader != "node-1" {
		t.Errorf("Leader = %q, want %q", status.Leader, "node-1")
	}
	if status.Epoch != 5 {
		t.Errorf("Epoch = %d, want %d", status.Epoch, 5)
	}
	if !status.VIPHeld {
		t.Error("VIPHeld = false, want true")
	}
	if status.Uptime != 10*time.Second {
		t.Errorf("Uptime = %v, want %v", status.Uptime, 10*time.Second)
	}
	if !status.Connected {
		t.Error("Connected = false, want true")
	}
}

func TestStatusMarshalJSON(t *testing.T) {
	status := Status{
		ClusterID: "test-cluster",
		NodeID:    "node-1",
		Role:      RolePrimary,
		Leader:    "node-1",
		Epoch:     5,
		VIPHeld:   true,
		Uptime:    10 * time.Second,
		Connected: true,
	}

	data, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}

	// Parse the JSON to verify structure
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// Role should be serialized as string "PRIMARY"
	if role, ok := result["role"].(string); !ok || role != "PRIMARY" {
		t.Errorf("role = %v, want %q", result["role"], "PRIMARY")
	}

	// Uptime should be serialized as uptimeMs in milliseconds
	if uptimeMs, ok := result["uptimeMs"].(float64); !ok || uptimeMs != 10000 {
		t.Errorf("uptimeMs = %v, want %v", result["uptimeMs"], 10000)
	}

	// Other fields should be present
	if clusterId, ok := result["clusterId"].(string); !ok || clusterId != "test-cluster" {
		t.Errorf("clusterId = %v, want %q", result["clusterId"], "test-cluster")
	}
}
