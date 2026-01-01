package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Lock provides a distributed lock for an app.
type Lock struct {
	app  *App
	name string

	mu       sync.Mutex
	held     bool
	revision uint64

	kv jetstream.KeyValue
}

// LockRecord represents the lock state stored in NATS KV.
type LockRecord struct {
	NodeID    string    `json:"node_id"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewLock creates a new distributed lock.
func NewLock(app *App, name string) *Lock {
	return &Lock{
		app:  app,
		name: name,
	}
}

// Acquire attempts to acquire the lock.
func (l *Lock) Acquire(ctx context.Context) error {
	return l.AcquireWithTTL(ctx, 30*time.Second)
}

// AcquireWithTTL attempts to acquire the lock with a custom TTL.
func (l *Lock) AcquireWithTTL(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.held {
		return ErrLockHeld
	}

	// Get or create the lock KV bucket
	if l.kv == nil {
		p := l.app.platform
		bucketName := fmt.Sprintf("%s_%s_locks", p.name, l.app.name)

		kv, err := p.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
			Bucket:      bucketName,
			Description: fmt.Sprintf("Locks for %s/%s", p.name, l.app.name),
			TTL:         ttl,
			History:     1,
		})
		if err != nil {
			return fmt.Errorf("failed to create lock KV bucket: %w", err)
		}
		l.kv = kv
	}

	p := l.app.platform
	record := LockRecord{
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

		var existingRecord LockRecord
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

	l.app.platform.audit.Log(ctx, AuditEntry{
		Category: "lock",
		Action:   "acquired",
		Data:     map[string]any{"app": l.app.name, "lock": l.name},
	})

	return nil
}

// Release releases the lock.
func (l *Lock) Release(ctx context.Context) error {
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

	l.app.platform.audit.Log(ctx, AuditEntry{
		Category: "lock",
		Action:   "released",
		Data:     map[string]any{"app": l.app.name, "lock": l.name},
	})

	return nil
}

// Renew extends the lock's TTL.
func (l *Lock) Renew(ctx context.Context, ttl time.Duration) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.held {
		return ErrLockNotHeld
	}

	p := l.app.platform
	record := LockRecord{
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
func (l *Lock) IsHeld() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// Do acquires the lock, executes the function, and releases the lock.
func (l *Lock) Do(ctx context.Context, fn func(context.Context) error) error {
	if err := l.Acquire(ctx); err != nil {
		return err
	}
	defer l.Release(ctx)

	return fn(ctx)
}

// DoWithTTL acquires the lock with TTL, executes the function, and releases the lock.
func (l *Lock) DoWithTTL(ctx context.Context, ttl time.Duration, fn func(context.Context) error) error {
	if err := l.AcquireWithTTL(ctx, ttl); err != nil {
		return err
	}
	defer l.Release(ctx)

	return fn(ctx)
}

// TryAcquire attempts to acquire the lock without blocking.
func (l *Lock) TryAcquire(ctx context.Context) bool {
	err := l.Acquire(ctx)
	return err == nil
}
