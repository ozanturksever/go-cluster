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

	// joinedAt stores when this node first joined the cluster
	joinedAt time.Time

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

	// Register this node (with duplicate check on initial registration)
	if err := m.registerInitial(); err != nil {
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
	// Deregister FIRST so other nodes see we're leaving before we stop watching
	m.deregister()

	// Then cancel context and wait for goroutines
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
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

// Register registers this node in the membership KV.
// This is called automatically on start and can be called to update membership info.
func (m *Membership) Register() error {
	return m.register()
}

// checkNodePresence checks if a node with the same ID is already present and active.
// Returns true if node is already present and active, false otherwise.
func (m *Membership) checkNodePresence() (bool, error) {
	p := m.platform

	// Try to get existing entry for this node ID
	entry, err := m.kv.Get(m.ctx, p.nodeID)
	if err != nil {
		// No existing entry found, node is not present
		return false, nil
	}

	// Parse the existing member record
	var existingMember Member
	if err := json.Unmarshal(entry.Value(), &existingMember); err != nil {
		// Can't parse, treat as not present
		return false, nil
	}

	// Check if the existing entry is "fresh" (recently updated)
	// We consider a node active if LastSeen is within 2x heartbeat interval
	// This allows for some timing slack while still detecting true duplicates
	freshnessThreshold := 2 * p.opts.heartbeat
	if freshnessThreshold < 5*time.Second {
		freshnessThreshold = 5 * time.Second // minimum threshold
	}

	timeSinceLastSeen := time.Since(existingMember.LastSeen)
	if timeSinceLastSeen < freshnessThreshold {
		// Node is recently active, consider it present
		return true, nil
	}

	// Entry is stale, allow takeover
	return false, nil
}

// register registers this node in the membership KV.
func (m *Membership) register() error {
	data, err := m.buildMemberData()
	if err != nil {
		return err
	}
	_, err = m.kv.Put(m.ctx, m.platform.nodeID, data)
	return err
}

// registerInitial registers this node for the first time, checking for duplicates.
// Uses atomic Create() to prevent race conditions when multiple nodes try to register
// with the same ID simultaneously.
func (m *Membership) registerInitial() error {
	// Set the joinedAt time on initial registration
	m.joinedAt = time.Now()

	data, err := m.buildMemberData()
	if err != nil {
		return err
	}

	// Try to atomically create the key (will fail if key exists)
	_, err = m.kv.Create(m.ctx, m.platform.nodeID, data)
	if err == nil {
		// Successfully created - we're the first with this node ID
		return nil
	}

	// Key exists - check if it's stale or active
	present, checkErr := m.checkNodePresence()
	if checkErr != nil {
		return fmt.Errorf("failed to check node presence: %w", checkErr)
	}

	if present {
		// Node is actively present, reject
		return ErrNodeAlreadyPresent
	}

	// Entry is stale, we can take over by updating
	_, err = m.kv.Put(m.ctx, m.platform.nodeID, data)
	return err
}

// buildMemberData creates and marshals a Member record for this node.
func (m *Membership) buildMemberData() ([]byte, error) {
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
		JoinedAt:  m.joinedAt,
		LastSeen:  time.Now(),
		Apps:      apps,
		IsHealthy: true,
	}

	return json.Marshal(member)
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
