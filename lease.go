package cluster

import "time"

// Lease represents a leadership lease with epoch-based fencing.
type Lease struct {
	NodeID     string    `json:"node_id"`
	Epoch      int64     `json:"epoch"`
	ExpiresAt  time.Time `json:"expires_at"`
	AcquiredAt time.Time `json:"acquired_at"`
	Revision   uint64    `json:"-"`
}

// IsExpired returns true if the lease has expired.
func (l *Lease) IsExpired() bool {
	return time.Now().After(l.ExpiresAt)
}

// IsValid returns true if the lease is not expired and has valid data.
func (l *Lease) IsValid() bool {
	return l.NodeID != "" && !l.IsExpired()
}

// TimeUntilExpiry returns the duration until the lease expires.
func (l *Lease) TimeUntilExpiry() time.Duration {
	remaining := time.Until(l.ExpiresAt)
	if remaining < 0 {
		return 0
	}
	return remaining
}
