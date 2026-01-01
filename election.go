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
	leaderCh chan bool

	mu       sync.RWMutex
	isLeader bool
	leader   string
	epoch    uint64

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// LeaderRecord represents the leader state stored in NATS KV.
type LeaderRecord struct {
	NodeID    string    `json:"node_id"`
	Epoch     uint64    `json:"epoch"`
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
		leaderCh: make(chan bool, 1),
	}, nil
}

// Start begins the election process.
func (e *Election) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)

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

	// Release leadership if we have it
	if e.IsLeader() {
		e.release()
	}
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
	p := e.app.platform

	record := LeaderRecord{
		NodeID:     p.nodeID,
		Epoch:      e.epoch + 1,
		AcquiredAt: time.Now(),
		ExpiresAt:  time.Now().Add(p.opts.leaseTTL),
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
	e.mu.Unlock()

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
		return fmt.Errorf("no longer leader")
	}

	// Update the expiration
	record.ExpiresAt = time.Now().Add(p.opts.leaseTTL)
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}

	_, err = e.kv.Update(e.ctx, "leader", data, entry.Revision())
	return err
}

// release releases leadership.
func (e *Election) release() error {
	e.mu.Lock()
	wasLeader := e.isLeader
	e.isLeader = false
	e.mu.Unlock()

	if wasLeader {
		// Delete the leader key
		return e.kv.Delete(context.Background(), "leader")
	}
	return nil
}


