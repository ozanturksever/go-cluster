package cluster

import (
	"encoding/json"
	"time"
)

// Status represents the current status of a cluster node.
type Status struct {
	// ClusterID is the cluster identifier.
	ClusterID string `json:"clusterId"`

	// NodeID is this node's identifier.
	NodeID string `json:"nodeId"`

	// Role is the current role of this node (PRIMARY or PASSIVE).
	Role Role `json:"role"`

	// Leader is the node ID of the current leader (empty if unknown).
	Leader string `json:"leader"`

	// Epoch is the current epoch number.
	Epoch int64 `json:"epoch"`

	// VIPHeld indicates whether this node currently holds the VIP.
	VIPHeld bool `json:"vipHeld"`

	// Uptime is how long the manager has been running.
	Uptime time.Duration `json:"uptime"`

	// Connected indicates whether the node is connected to NATS.
	Connected bool `json:"connected"`
}

// IsLeader returns true if this node is currently the leader.
func (s *Status) IsLeader() bool {
	return s.Role == RolePrimary
}

// String returns a human-readable string representation of the status.
func (s *Status) String() string {
	return s.Role.String()
}

// statusJSON is used for custom JSON marshaling.
type statusJSON struct {
	ClusterID string `json:"clusterId"`
	NodeID    string `json:"nodeId"`
	Role      string `json:"role"`
	Leader    string `json:"leader"`
	Epoch     int64  `json:"epoch"`
	VIPHeld   bool   `json:"vipHeld"`
	UptimeMs  int64  `json:"uptimeMs"`
	Connected bool   `json:"connected"`
}

// MarshalJSON implements json.Marshaler to serialize Role as string and Uptime as milliseconds.
func (s Status) MarshalJSON() ([]byte, error) {
	return json.Marshal(statusJSON{
		ClusterID: s.ClusterID,
		NodeID:    s.NodeID,
		Role:      s.Role.String(),
		Leader:    s.Leader,
		Epoch:     s.Epoch,
		VIPHeld:   s.VIPHeld,
		UptimeMs:  s.Uptime.Milliseconds(),
		Connected: s.Connected,
	})
}
