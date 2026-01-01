package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestLeafManager_NewLeafManager(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "test-platform",
		nodeID: "node1",
	}

	m := NewLeafManager(p)
	if m == nil {
		t.Fatal("expected non-nil LeafManager")
	}

	if m.Role() != LeafRoleStandalone {
		t.Errorf("expected standalone role, got %v", m.Role())
	}

	if m.IsHub() {
		t.Error("expected IsHub to be false")
	}

	if m.IsLeaf() {
		t.Error("expected IsLeaf to be false")
	}
}

func TestLeafManager_ConfigureAsHub(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "hub-platform",
		nodeID: "hub-node",
	}

	m := NewLeafManager(p)

	config := LeafHubConfig{
		Port:              7422,
		HeartbeatInterval: 5 * time.Second,
		HeartbeatTimeout:  15 * time.Second,
		AuthorizedLeaves: []LeafAuth{
			{Platform: "leaf1", Token: "secret1"},
			{Platform: "leaf2", Token: "secret2"},
		},
	}

	m.ConfigureAsHub(config)

	if m.Role() != LeafRoleHub {
		t.Errorf("expected hub role, got %v", m.Role())
	}

	if !m.IsHub() {
		t.Error("expected IsHub to be true")
	}

	if m.IsLeaf() {
		t.Error("expected IsLeaf to be false")
	}

	if m.hubConfig.Port != 7422 {
		t.Errorf("expected port 7422, got %d", m.hubConfig.Port)
	}
}

func TestLeafManager_ConfigureAsLeaf(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "leaf-platform",
		nodeID: "leaf-node",
	}

	m := NewLeafManager(p)

	config := LeafConnectionConfig{
		HubURLs:       []string{"nats-leaf://hub.example.com:7422"},
		HubPlatform:   "hub-platform",
		Token:         "secret-token",
		ReconnectWait: 5 * time.Second,
		MaxReconnects: 10,
	}

	m.ConfigureAsLeaf(config)

	if m.Role() != LeafRoleLeaf {
		t.Errorf("expected leaf role, got %v", m.Role())
	}

	if m.IsHub() {
		t.Error("expected IsHub to be false")
	}

	if !m.IsLeaf() {
		t.Error("expected IsLeaf to be true")
	}

	if m.HubPlatform() != "hub-platform" {
		t.Errorf("expected hub platform 'hub-platform', got %s", m.HubPlatform())
	}
}

func TestLeafManager_ConfigureAsLeaf_Defaults(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "leaf-platform",
		nodeID: "leaf-node",
	}

	m := NewLeafManager(p)

	// Configure with minimal config - should get defaults
	config := LeafConnectionConfig{
		HubURLs:     []string{"nats-leaf://hub:7422"},
		HubPlatform: "hub",
	}

	m.ConfigureAsLeaf(config)

	// Check defaults were applied
	if m.leafConfig.ReconnectWait != 2*time.Second {
		t.Errorf("expected default ReconnectWait 2s, got %v", m.leafConfig.ReconnectWait)
	}

	if m.leafConfig.MaxReconnects != -1 {
		t.Errorf("expected default MaxReconnects -1, got %d", m.leafConfig.MaxReconnects)
	}

	if m.leafConfig.PartitionDetection.HeartbeatInterval != 3*time.Second {
		t.Errorf("expected default HeartbeatInterval 3s, got %v", m.leafConfig.PartitionDetection.HeartbeatInterval)
	}

	if m.leafConfig.PartitionDetection.MissedHeartbeats != 5 {
		t.Errorf("expected default MissedHeartbeats 5, got %d", m.leafConfig.PartitionDetection.MissedHeartbeats)
	}
}

func TestLeafManager_PartitionState(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "test-platform",
		nodeID: "node1",
	}

	m := NewLeafManager(p)

	// Initially not partitioned
	if m.IsPartitioned() {
		t.Error("expected not partitioned initially")
	}

	if m.PartitionDuration() != 0 {
		t.Error("expected zero partition duration")
	}

	// Simulate partition
	m.partitionMu.Lock()
	m.isPartitioned = true
	m.partitionStart = time.Now().Add(-5 * time.Second)
	m.partitionMu.Unlock()

	if !m.IsPartitioned() {
		t.Error("expected partitioned")
	}

	if m.PartitionDuration() < 5*time.Second {
		t.Errorf("expected partition duration >= 5s, got %v", m.PartitionDuration())
	}
}

func TestLeafManager_LeafTracking(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "hub-platform",
		nodeID: "hub-node",
	}

	m := NewLeafManager(p)

	// Add some leaves
	m.leaves["leaf1"] = &LeafInfo{
		Platform:    "leaf1",
		Zone:        "us-east",
		Status:      LeafStatusConnected,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
	}

	m.leaves["leaf2"] = &LeafInfo{
		Platform:    "leaf2",
		Zone:        "us-west",
		Status:      LeafStatusConnected,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
	}

	m.leaves["leaf3"] = &LeafInfo{
		Platform:    "leaf3",
		Zone:        "eu-west",
		Status:      LeafStatusDisconnected,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now().Add(-1 * time.Hour),
	}

	// Test GetLeaves
	leaves := m.GetLeaves()
	if len(leaves) != 3 {
		t.Errorf("expected 3 leaves, got %d", len(leaves))
	}

	// Test GetLeaf
	leaf, ok := m.GetLeaf("leaf1")
	if !ok {
		t.Error("expected to find leaf1")
	}
	if leaf.Zone != "us-east" {
		t.Errorf("expected zone us-east, got %s", leaf.Zone)
	}

	// Test GetLeaf for non-existent
	_, ok = m.GetLeaf("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent leaf")
	}

	// Test ConnectedLeafCount
	count := m.ConnectedLeafCount()
	if count != 2 {
		t.Errorf("expected 2 connected leaves, got %d", count)
	}
}

func TestLeafManager_Quorum(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "hub-platform",
		nodeID: "hub-node",
	}

	m := NewLeafManager(p)

	// Without quorum requirement, always has quorum
	if !m.HasQuorum() {
		t.Error("expected quorum without strategy")
	}

	// Set up partition strategy with quorum
	m.SetPartitionStrategy(PartitionStrategy{
		RequireQuorum: true,
		QuorumSize:    2,
	})

	// No leaves yet
	if m.HasQuorum() {
		t.Error("expected no quorum with 0 leaves and quorum size 2")
	}

	// Add one connected leaf
	m.leaves["leaf1"] = &LeafInfo{
		Platform: "leaf1",
		Status:   LeafStatusConnected,
	}

	if m.HasQuorum() {
		t.Error("expected no quorum with 1 leaf and quorum size 2")
	}

	// Add second connected leaf
	m.leaves["leaf2"] = &LeafInfo{
		Platform: "leaf2",
		Status:   LeafStatusConnected,
	}

	if !m.HasQuorum() {
		t.Error("expected quorum with 2 connected leaves and quorum size 2")
	}

	// Test auto quorum (majority)
	m.SetPartitionStrategy(PartitionStrategy{
		RequireQuorum: true,
		QuorumSize:    0, // Auto = majority
	})

	// 2 out of 2 = majority
	if !m.HasQuorum() {
		t.Error("expected quorum with 2/2 majority")
	}

	// Add disconnected leaf (3 total, 2 connected = majority)
	m.leaves["leaf3"] = &LeafInfo{
		Platform: "leaf3",
		Status:   LeafStatusDisconnected,
	}

	if !m.HasQuorum() {
		t.Error("expected quorum with 2/3 majority")
	}
}

func TestLeafManager_Callbacks(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "hub-platform",
		nodeID: "hub-node",
	}

	m := NewLeafManager(p)

	// Test OnPartition callback
	var partitionEvent PartitionEvent
	m.OnPartition(func(event PartitionEvent) {
		partitionEvent = event
	})

	// Test OnLeafJoin callback
	m.OnLeafJoin(func(leaf LeafInfo) {
		_ = leaf // callback registered
	})

	// Test OnLeafLeave callback
	m.OnLeafLeave(func(leaf LeafInfo) {
		_ = leaf // callback registered
	})

	// Trigger partition notification
	m.notifyPartitionEvent(PartitionEvent{
		Type:        PartitionHubUnreachable,
		Platform:    "hub",
		Description: "Test partition",
	})

	// Give goroutine time to execute
	time.Sleep(50 * time.Millisecond)

	if partitionEvent.Type != PartitionHubUnreachable {
		t.Error("expected partition callback to be called")
	}
}

func TestLeafManager_StartStop_Standalone(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	p := &Platform{
		nc:     nc,
		name:   "test-platform",
		nodeID: "node1",
	}

	m := NewLeafManager(p)

	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("failed to start standalone: %v", err)
	}

	// Stop should be safe
	m.Stop()
}

func TestPartitionStrategy(t *testing.T) {
	tests := []struct {
		name     string
		strategy PartitionStrategy
	}{
		{
			name: "FailSafe",
			strategy: PartitionStrategy{
				Mode:          PartitionFailSafe,
				RequireQuorum: false,
			},
		},
		{
			name: "Autonomous",
			strategy: PartitionStrategy{
				Mode:                  PartitionAutonomous,
				MaxAutonomousDuration: 5 * time.Minute,
				FallbackToReadOnly:    true,
			},
		},
		{
			name: "Coordinated",
			strategy: PartitionStrategy{
				Mode:          PartitionCoordinated,
				RequireQuorum: true,
				QuorumSize:    3,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nc, cleanup := setupTestNATS(t)
			defer cleanup()

			p := &Platform{
				nc:     nc,
				name:   "test-platform",
				nodeID: "node1",
			}

			m := NewLeafManager(p)
			m.SetPartitionStrategy(tt.strategy)

			if m.partitionStrategy == nil {
				t.Error("expected partition strategy to be set")
			}

			if m.partitionStrategy.Mode != tt.strategy.Mode {
				t.Errorf("expected mode %v, got %v", tt.strategy.Mode, m.partitionStrategy.Mode)
			}
		})
	}
}

func TestTLSConfig(t *testing.T) {
	// Test nil config
	tlsCfg, err := LoadTLSConfig(nil)
	if err != nil {
		t.Errorf("expected no error for nil config, got %v", err)
	}
	if tlsCfg != nil {
		t.Error("expected nil tls config for nil input")
	}

	// Test skip verify only
	tlsCfg, err = LoadTLSConfig(&TLSConfig{
		SkipVerify: true,
	})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("expected non-nil tls config")
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify to be true")
	}
}

func TestSplitSubject(t *testing.T) {
	tests := []struct {
		subject  string
		expected []string
	}{
		{"a.b.c", []string{"a", "b", "c"}},
		{"platform.app.rpc._crossplatform", []string{"platform", "app", "rpc", "_crossplatform"}},
		{"single", []string{"single"}},
		{"", []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.subject, func(t *testing.T) {
			result := splitSubject(tt.subject)
			if len(result) != len(tt.expected) {
				t.Errorf("expected %d parts, got %d", len(tt.expected), len(result))
				return
			}
			for i, part := range result {
				if part != tt.expected[i] {
					t.Errorf("part %d: expected %s, got %s", i, tt.expected[i], part)
				}
			}
		})
	}
}

func TestLeafHeartbeat_Serialization(t *testing.T) {
	nc, cleanup := setupTestNATS(t)
	defer cleanup()

	hubPlatform := &Platform{
		nc:     nc,
		name:   "hub",
		nodeID: "hub-node",
	}
	hub := NewLeafManager(hubPlatform)
	hub.ConfigureAsHub(LeafHubConfig{
		Port:              7422,
		HeartbeatInterval: 100 * time.Millisecond,
	})

	leafPlatform := &Platform{
		nc:     nc,
		name:   "leaf",
		nodeID: "leaf-node",
	}
	leaf := NewLeafManager(leafPlatform)
	leaf.ConfigureAsLeaf(LeafConnectionConfig{
		HubURLs:     []string{"nats://localhost:4222"},
		HubPlatform: "hub",
		PartitionDetection: PartitionConfig{
			HeartbeatInterval: 100 * time.Millisecond,
			HeartbeatTimeout:  500 * time.Millisecond,
			MissedHeartbeats:  3,
		},
	})

	// Verify hub configuration
	if !hub.IsHub() {
		t.Error("expected hub.IsHub() to be true")
	}
	if hub.hubConfig.HeartbeatInterval != 100*time.Millisecond {
		t.Errorf("expected hub heartbeat interval 100ms, got %v", hub.hubConfig.HeartbeatInterval)
	}

	// Verify leaf configuration
	if !leaf.IsLeaf() {
		t.Error("expected leaf.IsLeaf() to be true")
	}
	if leaf.HubPlatform() != "hub" {
		t.Errorf("expected hub platform 'hub', got %s", leaf.HubPlatform())
	}
}

func TestLeafInfo_Fields(t *testing.T) {
	now := time.Now()
	info := LeafInfo{
		Platform:     "test-leaf",
		Zone:         "us-east-1",
		Status:       LeafStatusConnected,
		LatencyMs:    15,
		ConnectedAt:  now,
		LastSeen:     now,
		MessagesSent: 100,
		MessagesRecv: 200,
		BytesSent:    1024,
		BytesRecv:    2048,
	}

	if info.Platform != "test-leaf" {
		t.Errorf("expected platform test-leaf, got %s", info.Platform)
	}

	if info.Status != LeafStatusConnected {
		t.Errorf("expected status connected, got %s", info.Status)
	}

	if info.LatencyMs != 15 {
		t.Errorf("expected latency 15ms, got %d", info.LatencyMs)
	}
}

func TestPartitionEvent_Types(t *testing.T) {
	tests := []struct {
		eventType   PartitionEventType
		description string
	}{
		{PartitionHubUnreachable, "Hub unreachable"},
		{PartitionLeafUnreachable, "Leaf unreachable"},
		{PartitionHealed, "Partition healed"},
		{PartitionQuorumLost, "Quorum lost"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			event := PartitionEvent{
				Type:        tt.eventType,
				Description: tt.description,
				Timestamp:   time.Now(),
			}

			if event.Type != tt.eventType {
				t.Errorf("expected type %v, got %v", tt.eventType, event.Type)
			}
		})
	}
}

func TestLeafConnectionStatus_Values(t *testing.T) {
	statuses := []LeafConnectionStatus{
		LeafStatusConnected,
		LeafStatusDisconnected,
		LeafStatusConnecting,
		LeafStatusPartitioned,
	}

	expected := []string{"connected", "disconnected", "connecting", "partitioned"}

	for i, status := range statuses {
		if string(status) != expected[i] {
			t.Errorf("expected %s, got %s", expected[i], status)
		}
	}
}

// Helper to setup a test NATS connection
func setupTestNATS(t *testing.T) (*nats.Conn, func()) {
	t.Helper()

	// Try to connect to a local NATS server
	nc, err := nats.Connect(nats.DefaultURL, nats.Timeout(2*time.Second))
	if err != nil {
		t.Skip("NATS server not available, skipping test")
	}

	return nc, func() {
		nc.Close()
	}
}
