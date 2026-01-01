package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Election manages leader election for an app using NATS KV.
type Election struct {
	app *App

	kv       jetstream.KeyValue
	watcher  jetstream.KeyWatcher
	leaderCh chan bool

	mu            sync.RWMutex
	isLeader      bool
	leader        string
	epoch         uint64
	acquiredAt    time.Time
	lastRenewal   time.Time
	stepDownUntil time.Time // Cooldown period after stepping down

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// LeaderRecord represents the leader state stored in NATS KV.
type LeaderRecord struct {
	NodeID     string    `json:"node_id"`
	Epoch      uint64    `json:"epoch"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

// NewElection creates a new election instance for the app.
func NewElection(app *App) (*Election, error) {
	p := app.platform
	bucketName := fmt.Sprintf("%s_%s_election", p.name, app.name)

	kv, err := p.js.CreateOrUpdateKeyValue(context.Background(), jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Leader election for %s/%s", p.name, app.name),
		TTL:         p.opts.leaseTTL,
		History:     1,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create election KV bucket: %w", err)
	}

	return &Election{
		app:      app,
		kv:       kv,
		leaderCh: make(chan bool, 16),
	}, nil
}

// Start begins the election process.
func (e *Election) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)

	// Start KV watcher for leader changes
	watcher, err := e.kv.Watch(ctx, "leader")
	if err != nil {
		return fmt.Errorf("failed to create leader watcher: %w", err)
	}
	e.watcher = watcher

	// Start watch loop
	e.wg.Add(1)
	go e.watchLoop()

	// Start campaign loop
	e.wg.Add(1)
	go e.campaignLoop()

	return nil
}

// Stop stops the election process and releases leadership if held.
func (e *Election) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	e.wg.Wait()

	// Stop watcher
	if e.watcher != nil {
		e.watcher.Stop()
	}

	// Release leadership if we have it
	if e.IsLeader() {
		e.release()
	}
}

// StepDown gracefully releases leadership and allows another node to take over.
// It sets a cooldown period to prevent immediate re-acquisition.
func (e *Election) StepDown(ctx context.Context) error {
	e.mu.Lock()
	if !e.isLeader {
		e.mu.Unlock()
		return ErrNotLeader
	}
	// Set cooldown to prevent immediate re-acquisition
	// Use lease TTL as the cooldown period to give other nodes a chance
	e.stepDownUntil = time.Now().Add(e.app.platform.opts.leaseTTL)
	e.mu.Unlock()

	// Log the step down
	e.app.platform.audit.Log(ctx, AuditEntry{
		Category: "election",
		Action:   "step_down_initiated",
		Data:     map[string]any{"app": e.app.name, "epoch": e.epoch},
	})

	return e.release()
}

// WaitForLeadership blocks until this node becomes the leader or the context is cancelled.
// Returns nil if leadership is acquired, or an error if the context is cancelled or times out.
func (e *Election) WaitForLeadership(ctx context.Context) error {
	// Check if already leader
	if e.IsLeader() {
		return nil
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case isLeader := <-e.leaderCh:
			if isLeader {
				return nil
			}
		}
	}
}

// WaitForLeadershipWithTimeout blocks until leadership is acquired or timeout expires.
func (e *Election) WaitForLeadershipWithTimeout(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(e.ctx, timeout)
	defer cancel()
	return e.WaitForLeadership(ctx)
}

// AcquiredAt returns when this node acquired leadership, or zero time if not leader.
func (e *Election) AcquiredAt() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.acquiredAt
}

// LastRenewal returns when the leadership lease was last renewed.
func (e *Election) LastRenewal() time.Time {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastRenewal
}

// IsLeader returns true if this node is the current leader.
func (e *Election) IsLeader() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.isLeader
}

// Leader returns the current leader's node ID.
func (e *Election) Leader() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.leader
}

// Epoch returns the current election epoch.
func (e *Election) Epoch() uint64 {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.epoch
}

// LeaderChan returns a channel that receives leadership changes.
func (e *Election) LeaderChan() <-chan bool {
	return e.leaderCh
}

// watchLoop watches for leader changes via KV watcher.
func (e *Election) watchLoop() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case entry := <-e.watcher.Updates():
			if entry == nil {
				// Initial sync complete
				continue
			}

			e.handleWatchUpdate(entry)
		}
	}
}

// handleWatchUpdate processes a KV watch update.
func (e *Election) handleWatchUpdate(entry jetstream.KeyValueEntry) {
	op := entry.Operation()

	switch op {
	case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
		// Leader key was deleted - if we were leader, we lost it
		e.mu.Lock()
		wasLeader := e.isLeader
		if wasLeader {
			e.isLeader = false
			e.leader = ""
		}
		e.mu.Unlock()

		if wasLeader {
			e.notifyLeaderChange(false)
		}

	case jetstream.KeyValuePut:
		// Leader key was updated
		var record LeaderRecord
		if err := json.Unmarshal(entry.Value(), &record); err != nil {
			return
		}

		e.mu.Lock()
		oldLeader := e.leader
		wasLeader := e.isLeader
		isNewLeader := record.NodeID == e.app.platform.nodeID

		e.leader = record.NodeID
		e.epoch = record.Epoch

		if isNewLeader && !wasLeader {
			e.isLeader = true
			e.acquiredAt = record.AcquiredAt
		} else if !isNewLeader && wasLeader {
			e.isLeader = false
			e.acquiredAt = time.Time{}
		}
		e.mu.Unlock()

		// Update metrics
		e.app.platform.metrics.SetElectionEpoch(e.app.name, record.Epoch)
		e.app.platform.metrics.SetLeader(e.app.name, isNewLeader)

		// Notify about leadership changes
		if isNewLeader && !wasLeader {
			e.notifyLeaderChange(true)
		} else if !isNewLeader && wasLeader {
			e.notifyLeaderChange(false)
		}

		// Notify about leader change to another node
		if !isNewLeader && oldLeader != record.NodeID && e.app.onLeaderChange != nil {
			e.app.platform.audit.Log(e.ctx, AuditEntry{
				Category: "election",
				Action:   "leader_changed",
				Data:     map[string]any{"app": e.app.name, "old_leader": oldLeader, "new_leader": record.NodeID, "epoch": record.Epoch},
			})
			go e.callOnLeaderChange(record.NodeID)
		}
	}
}

// callOnLeaderChange calls the OnLeaderChange hook with timeout handling.
func (e *Election) callOnLeaderChange(leaderID string) {
	if e.app.onLeaderChange == nil {
		return
	}

	ctx, cancel := context.WithTimeout(e.ctx, e.app.platform.opts.hookTimeout)
	defer cancel()

	if err := e.app.onLeaderChange(ctx, leaderID); err != nil {
		e.app.platform.audit.Log(e.ctx, AuditEntry{
			Category: "app",
			Action:   "onLeaderChange_error",
			Data:     map[string]any{"app": e.app.name, "error": err.Error()},
		})
	}
}

// campaignLoop continuously tries to acquire leadership.
func (e *Election) campaignLoop() {
	defer e.wg.Done()

	p := e.app.platform
	heartbeat := p.opts.heartbeat

	// Try to acquire leadership immediately on start
	if err := e.acquire(); err == nil {
		e.notifyLeaderChange(true)
	}

	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			if e.IsLeader() {
				// Renew leadership
				if err := e.renew(); err != nil {
					e.mu.Lock()
					e.isLeader = false
					e.acquiredAt = time.Time{}
					e.mu.Unlock()
					e.notifyLeaderChange(false)
				}
			} else {
				// Try to acquire leadership
				if err := e.acquire(); err == nil {
					e.notifyLeaderChange(true)
				}
			}
		}
	}
}

// notifyLeaderChange sends a leadership change notification.
func (e *Election) notifyLeaderChange(isLeader bool) {
	select {
	case e.leaderCh <- isLeader:
	default:
	}
}

// acquire attempts to acquire leadership.
func (e *Election) acquire() error {
	// Check if in cooldown period after step down
	e.mu.RLock()
	if time.Now().Before(e.stepDownUntil) {
		e.mu.RUnlock()
		return fmt.Errorf("in cooldown after step down")
	}
	e.mu.RUnlock()

	p := e.app.platform
	now := time.Now()

	record := LeaderRecord{
		NodeID:     p.nodeID,
		Epoch:      e.epoch + 1,
		AcquiredAt: now,
		ExpiresAt:  now.Add(p.opts.leaseTTL),
	}

	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	// Try to create the key (will fail if exists)
	_, err = e.kv.Create(e.ctx, "leader", data)
	if err != nil {
		// Key exists, check if it's expired or try to update
		entry, getErr := e.kv.Get(e.ctx, "leader")
		if getErr != nil {
			return err
		}

		var existingRecord LeaderRecord
		if unmarshalErr := json.Unmarshal(entry.Value(), &existingRecord); unmarshalErr != nil {
			return err
		}

		// Update leader tracking even if we don't acquire
		e.mu.Lock()
		e.leader = existingRecord.NodeID
		e.epoch = existingRecord.Epoch
		e.mu.Unlock()

		// Only take over if the existing record is expired
		if time.Now().After(existingRecord.ExpiresAt) {
			record.Epoch = existingRecord.Epoch + 1
			data, _ = json.Marshal(record)
			_, err = e.kv.Update(e.ctx, "leader", data, entry.Revision())
			if err != nil {
				return err
			}
		} else {
			return err
		}
	}

	e.mu.Lock()
	e.isLeader = true
	e.leader = p.nodeID
	e.epoch = record.Epoch
	e.acquiredAt = now
	e.lastRenewal = now
	e.mu.Unlock()

	// Update metrics
	p.metrics.SetLeader(e.app.name, true)
	p.metrics.SetElectionEpoch(e.app.name, record.Epoch)

	// Log audit event
	p.audit.Log(e.ctx, AuditEntry{
		Category: "election",
		Action:   "leader_acquired",
		Data:     map[string]any{"app": e.app.name, "epoch": record.Epoch},
	})

	return nil
}

// renew renews the leadership lease.
func (e *Election) renew() error {
	p := e.app.platform

	entry, err := e.kv.Get(e.ctx, "leader")
	if err != nil {
		return err
	}

	var record LeaderRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return err
	}

	// Verify we're still the leader
	if record.NodeID != p.nodeID {
		p.audit.Log(e.ctx, AuditEntry{
			Category: "election",
			Action:   "leader_lost",
			Data:     map[string]any{"app": e.app.name, "new_leader": record.NodeID},
		})
		return fmt.Errorf("no longer leader")
	}

	// Update the expiration
	now := time.Now()
	record.ExpiresAt = now.Add(p.opts.leaseTTL)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	_, err = e.kv.Update(e.ctx, "leader", data, entry.Revision())
	if err == nil {
		e.mu.Lock()
		e.lastRenewal = now
		e.mu.Unlock()
	}
	return err
}

// release releases leadership.
func (e *Election) release() error {
	e.mu.Lock()
	wasLeader := e.isLeader
	e.isLeader = false
	e.acquiredAt = time.Time{}
	e.mu.Unlock()

	if wasLeader {
		// Notify about leadership loss FIRST (before deleting the key)
		// This ensures the App's watchLeadership receives the notification
		e.notifyLeaderChange(false)

		// Update metrics
		e.app.platform.metrics.SetLeader(e.app.name, false)

		// Log audit event
		e.app.platform.audit.Log(context.Background(), AuditEntry{
			Category: "election",
			Action:   "leader_released",
			Data:     map[string]any{"app": e.app.name, "epoch": e.epoch},
		})

		// Delete the leader key
		return e.kv.Delete(context.Background(), "leader")
	}
	return nil
}

