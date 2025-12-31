package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	leaderKey   = "leader"
	cooldownKey = "cooldown"
)

// leaseKeyName returns the lease key name.
func (n *Node) leaseKeyName() string {
	return leaderKey
}

// cooldownKeyName returns the cooldown key name for this node.
func (n *Node) cooldownKeyName() string {
	return fmt.Sprintf("%s.%s", cooldownKey, n.cfg.NodeID)
}

// initKVBucket creates or gets the KV bucket for this cluster.
func (n *Node) initKVBucket(ctx context.Context) error {
	bucketName := n.cfg.KVBucketName()

	// Try to create the bucket with MaxAge for automatic TTL
	kv, err := n.js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: bucketName,
		TTL:    n.cfg.LeaseTTL,
	})
	if err != nil {
		// Bucket might already exist, try to get it
		kv, err = n.js.KeyValue(ctx, bucketName)
		if err != nil {
			return fmt.Errorf("failed to create/get KV bucket %s: %w", bucketName, err)
		}
	}

	n.kv = kv
	return nil
}

// startWatcher starts the KV watcher for leader changes.
func (n *Node) startWatcher(ctx context.Context) error {
	watcher, err := n.kv.Watch(ctx, n.leaseKeyName())
	if err != nil {
		return fmt.Errorf("failed to create KV watcher: %w", err)
	}

	n.watcher = watcher

	// Start the watcher goroutine
	n.wg.Add(1)
	go n.watchLoop(ctx)

	return nil
}

// stopWatcher stops the KV watcher.
func (n *Node) stopWatcher() {
	n.mu.Lock()
	if n.watcher != nil {
		n.watcher.Stop()
		n.watcher = nil
	}
	n.mu.Unlock()
}

// watchLoop handles KV watcher events.
func (n *Node) watchLoop(ctx context.Context) {
	defer n.wg.Done()

	n.mu.RLock()
	watcher := n.watcher
	n.mu.RUnlock()

	if watcher == nil {
		return
	}

	for {
		select {
		case <-n.stopCh:
			return
		case entry, ok := <-watcher.Updates():
			if !ok {
				// Watcher closed
				n.logger.Warn("KV watcher closed")
				n.handleWatcherClosed(ctx)
				return
			}

			if entry == nil {
				// Initial nil entry indicates watcher is ready
				// Try to acquire leadership if no leader exists
				n.tryAcquire(ctx)
				continue
			}

			n.handleWatchUpdate(ctx, entry)
		}
	}
}

// handleWatchUpdate handles a KV watch update.
func (n *Node) handleWatchUpdate(ctx context.Context, entry jetstream.KeyValueEntry) {
	operation := entry.Operation()

	switch operation {
	case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
		// Leader key was deleted - try to acquire
		n.logger.Debug("leader key deleted, attempting to acquire")
		n.tryAcquire(ctx)

	case jetstream.KeyValuePut:
		// Leader key was updated
		n.handleLeaderUpdate(ctx, entry)
	}
}

// handleLeaderUpdate handles a leader key update.
func (n *Node) handleLeaderUpdate(ctx context.Context, entry jetstream.KeyValueEntry) {
	var lease Lease
	if err := json.Unmarshal(entry.Value(), &lease); err != nil {
		n.logger.Error("failed to unmarshal lease", "error", err)
		return
	}
	lease.Revision = entry.Revision()

	n.mu.Lock()
	oldLeader := ""
	if n.lease != nil {
		oldLeader = n.lease.NodeID
	}
	wasLeader := n.role == RolePrimary
	n.lease = &lease
	isNewLeader := lease.NodeID == n.cfg.NodeID

	var notifyType string
	var notifyNodeID string

	if isNewLeader && !wasLeader {
		// We became the leader
		n.role = RolePrimary
		notifyType = "become"
		n.logger.Info("became leader", "epoch", lease.Epoch)
	} else if !isNewLeader && wasLeader {
		// We lost leadership
		n.role = RolePassive
		notifyType = "lose"
		n.logger.Warn("lost leadership", "newLeader", lease.NodeID)
	} else if !isNewLeader && oldLeader != lease.NodeID {
		// Leader changed to someone else
		notifyType = "change"
		notifyNodeID = lease.NodeID
		n.logger.Info("leader changed", "leader", lease.NodeID)
	}

	n.mu.Unlock()

	// Notify hooks outside the lock
	switch notifyType {
	case "become":
		n.notifyBecomeLeader(ctx)
	case "lose":
		n.notifyLoseLeadership(ctx)
	case "change":
		n.notifyLeaderChange(ctx, notifyNodeID)
	}
}

// handleWatcherClosed handles the watcher being closed unexpectedly.
func (n *Node) handleWatcherClosed(ctx context.Context) {
	n.mu.Lock()
	wasLeader := n.role == RolePrimary
	n.role = RolePassive
	n.mu.Unlock()

	if wasLeader {
		n.notifyLoseLeadership(ctx)
	}
}

// tryAcquire attempts to acquire leadership.
func (n *Node) tryAcquire(ctx context.Context) {
	n.mu.Lock()
	if n.role == RolePrimary {
		n.mu.Unlock()
		return
	}
	// Remember the last known epoch from our stored lease
	lastKnownEpoch := int64(0)
	if n.lease != nil {
		lastKnownEpoch = n.lease.Epoch
	}
	n.mu.Unlock()

	// Check cooldown
	if n.isInCooldown(ctx) {
		n.logger.Debug("in cooldown period, not attempting to acquire")
		return
	}

	// Try to get current lease to determine epoch
	entry, err := n.kv.Get(ctx, n.leaseKeyName())
	var newEpoch int64 = 1

	if err == nil {
		// Key exists - parse it to get epoch
		var existingLease Lease
		if err := json.Unmarshal(entry.Value(), &existingLease); err == nil {
			newEpoch = existingLease.Epoch + 1
			// If someone else holds the lease, don't try to take over
			if existingLease.NodeID != n.cfg.NodeID {
				n.mu.Lock()
				n.lease = &existingLease
				n.lease.Revision = entry.Revision()
				n.mu.Unlock()
				return
			}
		}
	} else if errors.Is(err, jetstream.ErrKeyNotFound) {
		// Key doesn't exist - use last known epoch + 1 if we have one
		if lastKnownEpoch > 0 {
			newEpoch = lastKnownEpoch + 1
		}
	} else {
		n.logger.Debug("failed to get current lease", "error", err)
		return
	}

	// Create new lease
	lease := NewLease(n.cfg.NodeID, newEpoch)
	data, err := json.Marshal(lease)
	if err != nil {
		n.logger.Error("failed to marshal lease", "error", err)
		return
	}

	// Try to create (atomic - fails if key exists)
	rev, err := n.kv.Create(ctx, n.leaseKeyName(), data)
	if err != nil {
		// Someone else got there first
		n.logger.Debug("failed to acquire leadership", "error", err)
		return
	}

	lease.Revision = rev

	n.mu.Lock()
	wasPassive := n.role == RolePassive
	n.lease = lease
	n.role = RolePrimary
	n.mu.Unlock()

	n.logger.Info("acquired leadership", "epoch", newEpoch, "revision", rev)

	if wasPassive {
		n.notifyBecomeLeader(ctx)
	}
}

// heartbeatLoop sends periodic heartbeats while leader and attempts acquisition while follower.
func (n *Node) heartbeatLoop(ctx context.Context) {
	defer n.wg.Done()

	ticker := time.NewTicker(n.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.mu.RLock()
			isLeader := n.role == RolePrimary
			n.mu.RUnlock()

			if isLeader {
				n.sendHeartbeat(ctx)
			} else {
				// Periodically try to acquire leadership when not leader.
				// This handles cases where KV TTL expiration doesn't trigger a watcher event.
				n.tryAcquire(ctx)
			}
		}
	}
}

// sendHeartbeat sends a heartbeat by updating the lease.
func (n *Node) sendHeartbeat(ctx context.Context) {
	n.mu.RLock()
	if n.role != RolePrimary || n.lease == nil {
		n.mu.RUnlock()
		return
	}
	currentLease := n.lease
	n.mu.RUnlock()

	// Update the lease to refresh TTL
	newLease := NewLease(n.cfg.NodeID, currentLease.Epoch)
	newLease.Revision = currentLease.Revision

	data, err := json.Marshal(newLease)
	if err != nil {
		n.logger.Error("failed to marshal heartbeat lease", "error", err)
		return
	}

	rev, err := n.kv.Update(ctx, n.leaseKeyName(), data, currentLease.Revision)
	if err != nil {
		n.logger.Warn("heartbeat failed", "error", err)
		// Lost leadership - the watcher will handle the state change
		return
	}

	n.mu.Lock()
	if n.lease != nil {
		n.lease.Revision = rev
		n.lease.AcquiredAt = newLease.AcquiredAt
	}
	n.mu.Unlock()

	n.logger.Debug("heartbeat sent", "epoch", newLease.Epoch, "revision", rev)
}

// releaseLease releases the leadership lease.
func (n *Node) releaseLease(ctx context.Context) error {
	n.mu.Lock()
	if n.role != RolePrimary {
		n.mu.Unlock()
		return ErrNotLeader
	}

	// Set cooldown marker first
	if err := n.setCooldown(ctx); err != nil {
		n.logger.Warn("failed to set cooldown marker", "error", err)
	}

	// Delete the lease
	if err := n.kv.Delete(ctx, n.leaseKeyName()); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		n.mu.Unlock()
		return fmt.Errorf("failed to delete lease: %w", err)
	}

	n.role = RolePassive
	n.lease = nil
	n.mu.Unlock()

	n.logger.Info("released leadership")
	n.notifyLoseLeadership(ctx)

	return nil
}

// isInCooldown checks if this node is in cooldown period.
func (n *Node) isInCooldown(ctx context.Context) bool {
	entry, err := n.kv.Get(ctx, n.cooldownKeyName())
	if err != nil {
		return false
	}

	expiryMs, err := strconv.ParseInt(string(entry.Value()), 10, 64)
	if err != nil {
		n.logger.Warn("invalid cooldown value", "value", string(entry.Value()))
		_ = n.kv.Delete(ctx, n.cooldownKeyName())
		return false
	}

	nowMs := time.Now().UnixMilli()
	if nowMs < expiryMs {
		return true
	}

	_ = n.kv.Delete(ctx, n.cooldownKeyName())
	return false
}

// setCooldown sets the cooldown marker for this node.
func (n *Node) setCooldown(ctx context.Context) error {
	cooldownExpiry := time.Now().Add(n.cfg.LeaseTTL).UnixMilli()
	data := []byte(strconv.FormatInt(cooldownExpiry, 10))

	_, err := n.kv.Create(ctx, n.cooldownKeyName(), data)
	if err != nil {
		_, err = n.kv.Put(ctx, n.cooldownKeyName(), data)
	}
	return err
}

// clearCooldown clears the cooldown marker for this node.
func (n *Node) clearCooldown(ctx context.Context) {
	_ = n.kv.Delete(ctx, n.cooldownKeyName())
}

// restartWatcher restarts the KV watcher after reconnection.
func (n *Node) restartWatcher(ctx context.Context) error {
	n.stopWatcher()

	// Reinitialize KV bucket reference
	if err := n.initKVBucket(ctx); err != nil {
		return err
	}

	// Start new watcher
	return n.startWatcher(ctx)
}
