package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Membership manages cluster membership tracking.
type Membership struct {
	platform *Platform

	kv      jetstream.KeyValue
	members map[string]*Member

	mu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewMembership creates a new membership tracker.
func NewMembership(p *Platform) *Membership {
	return &Membership{
		platform: p,
		members:  make(map[string]*Member),
	}
}

// Start begins membership tracking.
func (m *Membership) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	bucketName := fmt.Sprintf("%s_membership", m.platform.name)

	kv, err := m.platform.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Membership for platform %s", m.platform.name),
		TTL:         30 * time.Second,
		History:     1,
	})
	if err != nil {
		return fmt.Errorf("failed to create membership KV bucket: %w", err)
	}
	m.kv = kv

	// Register this node
	if err := m.register(); err != nil {
		return fmt.Errorf("failed to register node: %w", err)
	}

	// Start heartbeat
	m.wg.Add(1)
	go m.heartbeatLoop()

	// Start watching for membership changes
	m.wg.Add(1)
	go m.watchMembers()

	return nil
}

// Stop stops membership tracking and deregisters this node.
func (m *Membership) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.deregister()
}

// Members returns a copy of all known members.
func (m *Membership) Members() []Member {
	m.mu.RLock()
	defer m.mu.RUnlock()

	members := make([]Member, 0, len(m.members))
	for _, member := range m.members {
		members = append(members, *member)
	}
	return members
}

// GetMember returns a specific member by node ID.
func (m *Membership) GetMember(nodeID string) (*Member, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	member, ok := m.members[nodeID]
	if !ok {
		return nil, false
	}
	memberCopy := *member
	return &memberCopy, true
}

// MemberCount returns the number of known members.
func (m *Membership) MemberCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.members)
}

// register registers this node in the membership KV.
func (m *Membership) register() error {
	p := m.platform

	// Get list of registered apps
	p.appsMu.RLock()
	apps := make([]string, 0, len(p.apps))
	for name := range p.apps {
		apps = append(apps, name)
	}
	p.appsMu.RUnlock()

	member := Member{
		NodeID:    p.nodeID,
		Platform:  p.name,
		Labels:    p.opts.labels,
		JoinedAt:  time.Now(),
		LastSeen:  time.Now(),
		Apps:      apps,
		IsHealthy: true,
	}

	data, err := json.Marshal(member)
	if err != nil {
		return err
	}

	_, err = m.kv.Put(m.ctx, p.nodeID, data)
	return err
}

// deregister removes this node from the membership KV.
func (m *Membership) deregister() error {
	return m.kv.Delete(context.Background(), m.platform.nodeID)
}

// heartbeatLoop periodically updates this node's membership record.
func (m *Membership) heartbeatLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.register()
		}
	}
}

// watchMembers watches for membership changes.
func (m *Membership) watchMembers() {
	defer m.wg.Done()

	watcher, err := m.kv.WatchAll(m.ctx)
	if err != nil {
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case entry := <-watcher.Updates():
			if entry == nil {
				continue
			}

			nodeID := entry.Key()

			if entry.Operation() == jetstream.KeyValueDelete {
				m.mu.Lock()
				oldMember := m.members[nodeID]
				delete(m.members, nodeID)
				m.mu.Unlock()

				if oldMember != nil {
					m.notifyMemberLeft(*oldMember)
				}
				continue
			}

			var member Member
			if err := json.Unmarshal(entry.Value(), &member); err != nil {
				continue
			}

			m.mu.Lock()
			_, existed := m.members[nodeID]
			m.members[nodeID] = &member
			m.mu.Unlock()

			if !existed {
				m.notifyMemberJoined(member)
			}
		}
	}
}

// notifyMemberJoined notifies all apps about a new member.
func (m *Membership) notifyMemberJoined(member Member) {
	m.platform.appsMu.RLock()
	defer m.platform.appsMu.RUnlock()

	for _, app := range m.platform.apps {
		if app.onMemberJoin != nil {
			go app.onMemberJoin(m.ctx, member)
		}
	}

	m.platform.audit.Log(m.ctx, AuditEntry{
		Category: "membership",
		Action:   "member_joined",
		Data:     map[string]any{"node_id": member.NodeID},
	})
}

// notifyMemberLeft notifies all apps about a departing member.
func (m *Membership) notifyMemberLeft(member Member) {
	m.platform.appsMu.RLock()
	defer m.platform.appsMu.RUnlock()

	for _, app := range m.platform.apps {
		if app.onMemberLeave != nil {
			go app.onMemberLeave(m.ctx, member)
		}
	}

	m.platform.audit.Log(m.ctx, AuditEntry{
		Category: "membership",
		Action:   "member_left",
		Data:     map[string]any{"node_id": member.NodeID},
	})
}
