package cluster

import (
	"testing"
	"time"
)

func TestLeaseIsExpired(t *testing.T) {
	tests := []struct {
		name      string
		expiresAt time.Time
		want      bool
	}{
		{
			name:      "not expired - future",
			expiresAt: time.Now().Add(10 * time.Second),
			want:      false,
		},
		{
			name:      "expired - past",
			expiresAt: time.Now().Add(-10 * time.Second),
			want:      true,
		},
		{
			name:      "expired - just now",
			expiresAt: time.Now().Add(-1 * time.Millisecond),
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lease := &Lease{
				NodeID:    "test-node",
				Epoch:     1,
				ExpiresAt: tt.expiresAt,
			}
			if got := lease.IsExpired(); got != tt.want {
				t.Errorf("IsExpired() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLeaseIsValid(t *testing.T) {
	tests := []struct {
		name  string
		lease Lease
		want  bool
	}{
		{
			name: "valid lease",
			lease: Lease{
				NodeID:    "test-node",
				Epoch:     1,
				ExpiresAt: time.Now().Add(10 * time.Second),
			},
			want: true,
		},
		{
			name: "invalid - empty node ID",
			lease: Lease{
				NodeID:    "",
				Epoch:     1,
				ExpiresAt: time.Now().Add(10 * time.Second),
			},
			want: false,
		},
		{
			name: "invalid - expired",
			lease: Lease{
				NodeID:    "test-node",
				Epoch:     1,
				ExpiresAt: time.Now().Add(-10 * time.Second),
			},
			want: false,
		},
		{
			name: "invalid - empty node ID and expired",
			lease: Lease{
				NodeID:    "",
				Epoch:     1,
				ExpiresAt: time.Now().Add(-10 * time.Second),
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.lease.IsValid(); got != tt.want {
				t.Errorf("IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLeaseTimeUntilExpiry(t *testing.T) {
	t.Run("returns positive duration for future expiry", func(t *testing.T) {
		lease := &Lease{
			NodeID:    "test-node",
			ExpiresAt: time.Now().Add(5 * time.Second),
		}
		got := lease.TimeUntilExpiry()
		if got <= 0 {
			t.Errorf("TimeUntilExpiry() = %v, want positive duration", got)
		}
		if got > 5*time.Second {
			t.Errorf("TimeUntilExpiry() = %v, want <= 5s", got)
		}
	})

	t.Run("returns zero for expired lease", func(t *testing.T) {
		lease := &Lease{
			NodeID:    "test-node",
			ExpiresAt: time.Now().Add(-5 * time.Second),
		}
		got := lease.TimeUntilExpiry()
		if got != 0 {
			t.Errorf("TimeUntilExpiry() = %v, want 0", got)
		}
	})
}
