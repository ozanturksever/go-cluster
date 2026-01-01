package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// PlatformLock provides a platform-wide distributed lock for cross-app coordination.
type PlatformLock struct {
	platform *Platform
	name     string

	mu       sync.Mutex
	held     bool
	revision uint64

	kv jetstream.KeyValue
}

// PlatformLockRecord represents the lock state stored in NATS KV.
type PlatformLockRecord struct {
	NodeID     string    `json:"node_id"`
	App        string    `json:"app,omitempty"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewPlatformLock creates a new platform-wide distributed lock.
func NewPlatformLock(p *Platform, name string) *PlatformLock {
	return &PlatformLock{
		platform: p,
		name:     name,
	}
}

// Lock returns a platform-wide lock with the given name.
func (p *Platform) Lock(name string) *PlatformLock {
	return NewPlatformLock(p, name)
}

// Acquire attempts to acquire the lock.
func (l *PlatformLock) Acquire(ctx context.Context) error {
	return l.AcquireWithTTL(ctx, 30*time.Second)
}

// AcquireWithTTL attempts to acquire the lock with a custom TTL.
func (l *PlatformLock) AcquireWithTTL(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.held {
		return ErrLockHeld
	}

	// Get or create the platform lock KV bucket
	if l.kv == nil {
		p := l.platform
		bucketName := fmt.Sprintf("%s_platform_locks", p.name)

		kv, err := p.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:      bucketName,
			Description: fmt.Sprintf("Platform-wide locks for %s", p.name),
			TTL:         ttl,
			History:     1,
		})
		if err != nil {
			return fmt.Errorf("failed to create platform lock KV bucket: %w", err)
		}
		l.kv = kv
	}

	p := l.platform
	record := PlatformLockRecord{
		NodeID:     p.nodeID,
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(ttl),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	// Try to create the lock key
	rev, err := l.kv.Create(ctx, l.name, data)
	if err != nil {
		// Key exists, check if it's expired
		entry, getErr := l.kv.Get(ctx, l.name)
		if getErr != nil {
			return ErrLockHeld
		}

		var existingRecord PlatformLockRecord
		if unmarshalErr := json.Unmarshal(entry.Value(), &existingRecord); unmarshalErr != nil {
			return ErrLockHeld
		}

		// Only take over if the lock is expired
		if time.Now().After(existingRecord.ExpiresAt) {
			rev, err = l.kv.Update(ctx, l.name, data, entry.Revision())
			if err != nil {
				return ErrLockHeld
			}
		} else {
			return ErrLockHeld
		}
	}

	l.held = true
	l.revision = rev

	l.platform.audit.Log(ctx, AuditEntry{
		Category: "platform_lock",
		Action:   "acquired",
		Data:     map[string]any{"lock": l.name},
	})

	return nil
}

// Release releases the lock.
func (l *PlatformLock) Release(ctx context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.held {
		return ErrLockNotHeld
	}

	if l.kv != nil {
		if err := l.kv.Delete(ctx, l.name); err != nil {
			return err
		}
	}

	l.held = false
	l.revision = 0

	l.platform.audit.Log(ctx, AuditEntry{
		Category: "platform_lock",
		Action:   "released",
		Data:     map[string]any{"lock": l.name},
	})

	return nil
}

// Renew extends the lock's TTL.
func (l *PlatformLock) Renew(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.held {
		return ErrLockNotHeld
	}

	p := l.platform
	record := PlatformLockRecord{
		NodeID:     p.nodeID,
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(ttl),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	rev, err := l.kv.Update(ctx, l.name, data, l.revision)
	if err != nil {
		l.held = false
		return err
	}

	l.revision = rev
	return nil
}

// IsHeld returns true if this lock is currently held by this instance.
func (l *PlatformLock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// Holder returns information about the current lock holder.
func (l *PlatformLock) Holder(ctx context.Context) (*PlatformLockRecord, error) {
	// Ensure KV is initialized
	if l.kv == nil {
		p := l.platform
		bucketName := fmt.Sprintf("%s_platform_locks", p.name)
		kv, err := p.js.KeyValue(ctx, bucketName)
		if err != nil {
			return nil, fmt.Errorf("lock bucket not found: %w", err)
		}
		l.kv = kv
	}

	entry, err := l.kv.Get(ctx, l.name)
	if err != nil {
		return nil, err
	}

	var record PlatformLockRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return nil, err
	}

	return &record, nil
}

// Do acquires the lock, executes the function, and releases the lock.
func (l *PlatformLock) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := l.Acquire(ctx); err != nil {
		return err
	}
	defer l.Release(ctx)

	return fn(ctx)
}

// DoWithTTL acquires the lock with TTL, executes the function, and releases the lock.
func (l *PlatformLock) DoWithTTL(ctx context.Context, ttl time.Duration, fn func(context.Context) error) error {
	if err := l.AcquireWithTTL(ctx, ttl); err != nil {
		return err
	}
	defer l.Release(ctx)

	return fn(ctx)
}

// TryAcquire attempts to acquire the lock without blocking.
func (l *PlatformLock) TryAcquire(ctx context.Context) bool {
	err := l.Acquire(ctx)
	return err == nil
}

// TryAcquireWithTTL attempts to acquire the lock with TTL without blocking.
func (l *PlatformLock) TryAcquireWithTTL(ctx context.Context, ttl time.Duration) bool {
	err := l.AcquireWithTTL(ctx, ttl)
	return err == nil
}

// WaitAndAcquire waits until the lock is available and then acquires it.
func (l *PlatformLock) WaitAndAcquire(ctx context.Context, ttl time.Duration) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			err := l.AcquireWithTTL(ctx, ttl)
			if err == nil {
				return nil
			}
			if err != ErrLockHeld {
				return err
			}
			// Lock is held by someone else, continue waiting
		}
	}
}
