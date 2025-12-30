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

func (n *Node) leaseKey() string {
	return "lease"
}

func (n *Node) cooldownKey() string {
	return fmt.Sprintf("cooldown-%s", n.cfg.NodeID)
}

func (n *Node) tryAcquireOrRenew(ctx context.Context) {
	n.mu.Lock()

	var notifyType string
	var notifyNodeID string

	if n.role == RolePrimary {
		if err := n.renewLeaseLocked(ctx); err != nil {
			n.logger.Warn("failed to renew lease", "error", err)
			n.role = RolePassive
			n.logger.Warn("lost leadership")
			notifyType = "lose"
		}
	} else {
		notifyType, notifyNodeID = n.tryAcquireLocked(ctx)
	}

	n.mu.Unlock()

	// Call hooks outside the lock to avoid deadlock
	switch notifyType {
	case "become":
		n.notifyBecomeLeader(ctx)
	case "lose":
		n.notifyLoseLeadership(ctx)
	case "change":
		n.notifyLeaderChange(ctx, notifyNodeID)
	}
}

// tryAcquireLocked attempts to acquire the lease. Must be called with mu held.
// Returns (notifyType, nodeID) to indicate what notification should happen after unlock.
func (n *Node) tryAcquireLocked(ctx context.Context) (string, string) {
	key := n.leaseKey()

	entry, err := n.kv.Get(ctx, key)
	if err == nil {
		var existingLease Lease
		if err := json.Unmarshal(entry.Value(), &existingLease); err != nil {
			n.logger.Debug("failed to unmarshal lease", "error", err)
			return "", ""
		}
		existingLease.Revision = entry.Revision()

		oldLeader := ""
		if n.lease != nil {
			oldLeader = n.lease.NodeID
		}
		n.lease = &existingLease

		if existingLease.NodeID != n.cfg.NodeID && !existingLease.IsExpired() {
			if oldLeader != existingLease.NodeID {
				return "change", existingLease.NodeID
			}
			return "", ""
		}

		newEpoch := existingLease.Epoch + 1
		if existingLease.NodeID == n.cfg.NodeID {
			newEpoch = existingLease.Epoch
		}

		if existingLease.NodeID != n.cfg.NodeID && n.isInCooldown(ctx) {
			return "", ""
		}

		return n.createLeaseLocked(ctx, newEpoch, entry.Revision())
	}

	if !errors.Is(err, jetstream.ErrKeyNotFound) {
		n.logger.Debug("failed to get lease", "error", err)
		return "", ""
	}

	if n.lease != nil && n.lease.NodeID != "" {
		n.lease = nil
		return "change", ""
	}

	if n.isInCooldown(ctx) {
		return "", ""
	}

	return n.createLeaseLocked(ctx, 1, 0)
}

// createLeaseLocked creates a new lease. Must be called with mu held.
// Returns (notifyType, nodeID) to indicate what notification should happen after unlock.
func (n *Node) createLeaseLocked(ctx context.Context, epoch int64, expectedRevision uint64) (string, string) {
	now := time.Now()
	lease := Lease{
		NodeID:     n.cfg.NodeID,
		Epoch:      epoch,
		ExpiresAt:  now.Add(n.cfg.LeaseTTL),
		AcquiredAt: now,
	}

	data, err := json.Marshal(lease)
	if err != nil {
		n.logger.Debug("failed to marshal lease", "error", err)
		return "", ""
	}

	var rev uint64
	if expectedRevision == 0 {
		rev, err = n.kv.Create(ctx, n.leaseKey(), data)
	} else {
		rev, err = n.kv.Update(ctx, n.leaseKey(), data, expectedRevision)
	}

	if err != nil {
		return "", ""
	}

	lease.Revision = rev
	wasPassive := n.role == RolePassive
	n.lease = &lease
	n.role = RolePrimary

	n.logger.Info("acquired leadership", "epoch", epoch, "revision", rev)

	if wasPassive {
		return "become", ""
	}

	return "", ""
}

func (n *Node) renewLeaseLocked(ctx context.Context) error {
	if n.lease == nil {
		return ErrNotLeader
	}

	entry, err := n.kv.Get(ctx, n.leaseKey())
	if err != nil {
		return fmt.Errorf("get lease for renewal: %w", err)
	}

	var currentLease Lease
	if err := json.Unmarshal(entry.Value(), &currentLease); err != nil {
		return fmt.Errorf("unmarshal lease: %w", err)
	}

	if currentLease.Epoch != n.lease.Epoch || currentLease.NodeID != n.cfg.NodeID {
		return ErrEpochMismatch
	}

	n.lease.ExpiresAt = time.Now().Add(n.cfg.LeaseTTL)
	n.lease.Revision = entry.Revision()

	data, err := json.Marshal(n.lease)
	if err != nil {
		return fmt.Errorf("marshal lease: %w", err)
	}

	rev, err := n.kv.Update(ctx, n.leaseKey(), data, entry.Revision())
	if err != nil {
		return fmt.Errorf("update lease: %w", err)
	}

	n.lease.Revision = rev
	n.logger.Debug("renewed lease", "epoch", n.lease.Epoch, "revision", rev)

	return nil
}

func (n *Node) isInCooldown(ctx context.Context) bool {
	entry, err := n.kv.Get(ctx, n.cooldownKey())
	if err != nil {
		return false
	}

	expiryMs, err := strconv.ParseInt(string(entry.Value()), 10, 64)
	if err != nil {
		n.logger.Warn("invalid cooldown value", "value", string(entry.Value()))
		_ = n.kv.Delete(ctx, n.cooldownKey())
		return false
	}

	nowMs := time.Now().UnixMilli()
	if nowMs < expiryMs {
		return true
	}

	_ = n.kv.Delete(ctx, n.cooldownKey())
	return false
}

func (n *Node) setCooldown(ctx context.Context) error {
	cooldownExpiry := time.Now().Add(n.cfg.LeaseTTL).UnixMilli()
	data := []byte(strconv.FormatInt(cooldownExpiry, 10))

	_, err := n.kv.Create(ctx, n.cooldownKey(), data)
	if err != nil {
		_, err = n.kv.Put(ctx, n.cooldownKey(), data)
	}
	return err
}

func (n *Node) electionLoop(ctx context.Context) {
	defer n.wg.Done()

	ticker := time.NewTicker(n.cfg.RenewInterval)
	defer ticker.Stop()

	for {
		select {
		case <-n.stopCh:
			return
		case <-ticker.C:
			n.tryAcquireOrRenew(ctx)
		}
	}
}
