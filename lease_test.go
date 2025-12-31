package cluster

import (
	"testing"
	"time"
)

func TestNewLease(t *testing.T) {
	lease := NewLease("node-1", 5)

	if lease.NodeID != "node-1" {
		t.Errorf("NewLease() NodeID = %q, want %q", lease.NodeID, "node-1")
	}
	if lease.Epoch != 5 {
		t.Errorf("NewLease() Epoch = %d, want %d", lease.Epoch, 5)
	}
	if lease.AcquiredAt == 0 {
		t.Error("NewLease() AcquiredAt should be set")
	}
	// AcquiredAt should be recent (within last second)
	acquiredAt := time.UnixMilli(lease.AcquiredAt)
	if time.Since(acquiredAt) > time.Second {
		t.Error("NewLease() AcquiredAt should be recent")
	}
}

func TestLeaseIsValid(t *testing.T) {
	tests := []struct {
		name  string
		lease Lease
		want  bool
	}{
		{
			name:  "valid lease",
			lease: Lease{NodeID: "node-1", Epoch: 1},
			want:  true,
		},
		{
			name:  "empty node ID",
			lease: Lease{NodeID: "", Epoch: 1},
			want:  false,
		},
		{
			name:  "zero epoch",
			lease: Lease{NodeID: "node-1", Epoch: 0},
			want:  false,
		},
		{
			name:  "both invalid",
			lease: Lease{NodeID: "", Epoch: 0},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lease.IsValid(); got != tt.want {
				t.Errorf("Lease.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLeaseAge(t *testing.T) {
	// Test with zero AcquiredAt
	lease := Lease{NodeID: "node-1", Epoch: 1, AcquiredAt: 0}
	if lease.Age() != 0 {
		t.Errorf("Lease.Age() with zero AcquiredAt = %v, want 0", lease.Age())
	}

	// Test with recent AcquiredAt
	lease = Lease{
		NodeID:     "node-1",
		Epoch:      1,
		AcquiredAt: time.Now().Add(-5 * time.Second).UnixMilli(),
	}
	age := lease.Age()
	if age < 4*time.Second || age > 6*time.Second {
		t.Errorf("Lease.Age() = %v, want ~5s", age)
	}

	// Test with very old AcquiredAt
	lease = Lease{
		NodeID:     "node-1",
		Epoch:      1,
		AcquiredAt: time.Now().Add(-1 * time.Hour).UnixMilli(),
	}
	age = lease.Age()
	if age < 59*time.Minute || age > 61*time.Minute {
		t.Errorf("Lease.Age() = %v, want ~1h", age)
	}
}

func TestLeaseRevision(t *testing.T) {
	lease := NewLease("node-1", 1)
	if lease.Revision != 0 {
		t.Errorf("NewLease() Revision = %d, want 0", lease.Revision)
	}

	lease.Revision = 42
	if lease.Revision != 42 {
		t.Errorf("Lease.Revision = %d, want 42", lease.Revision)
	}
}
