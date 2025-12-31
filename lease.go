package cluster

import "time"

// Lease represents a leadership lease with epoch-based fencing.
// The actual expiration is handled by NATS KV's MaxAge TTL.
type Lease struct {
	NodeID     string `json:"node_id"`
	Epoch      int64  `json:"epoch"`
	AcquiredAt int64  `json:"acquired_at"` // Unix timestamp in milliseconds
	Revision   uint64 `json:"-"`           // NATS KV revision (not serialized)
}

// NewLease creates a new lease for the given node and epoch.
func NewLease(nodeID string, epoch int64) *Lease {
	return &Lease{
		NodeID:     nodeID,
		Epoch:      epoch,
		AcquiredAt: time.Now().UnixMilli(),
	}
}

// IsValid returns true if the lease has valid data.
func (l *Lease) IsValid() bool {
	return l.NodeID != "" && l.Epoch > 0
}

// Age returns how long ago the lease was acquired.
func (l *Lease) Age() time.Duration {
	if l.AcquiredAt == 0 {
		return 0
	}
	return time.Since(time.UnixMilli(l.AcquiredAt))
}
