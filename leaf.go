package cluster

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// LeafRole represents the role of a platform in the leaf topology.
type LeafRole string

const (
	// LeafRoleHub is a hub platform that accepts leaf connections.
	LeafRoleHub LeafRole = "hub"
	// LeafRoleLeaf is a leaf platform that connects to a hub.
	LeafRoleLeaf LeafRole = "leaf"
	// LeafRoleStandalone is a standalone platform with no leaf connections.
	LeafRoleStandalone LeafRole = "standalone"
)

// LeafHubConfig configures a platform as a hub that accepts leaf connections.
type LeafHubConfig struct {
	// Port for leaf node connections (typically 7422)
	Port int

	// TLS configuration for secure leaf connections
	TLS *TLSConfig

	// AuthorizedLeaves lists platforms authorized to connect as leaves
	AuthorizedLeaves []LeafAuth

	// HeartbeatInterval is how often to send heartbeats to leaves
	HeartbeatInterval time.Duration

	// HeartbeatTimeout is how long before a leaf is considered disconnected
	HeartbeatTimeout time.Duration
}

// LeafAuth represents authorization for a leaf platform.
type LeafAuth struct {
	Platform string
	Token    string
	User     string
	Password string
}

// LeafConnectionConfig configures a platform as a leaf connecting to a hub.
type LeafConnectionConfig struct {
	// HubURLs are the NATS leaf node URLs of the hub (nats-leaf://host:port)
	HubURLs []string

	// HubPlatform is the name of the hub platform
	HubPlatform string

	// Token for authentication (alternative to User/Password)
	Token string

	// User and Password for authentication
	User     string
	Password string

	// Credentials file path (alternative to Token or User/Password)
	Credentials string

	// TLS configuration
	TLS *TLSConfig

	// ReconnectWait is the time to wait between reconnection attempts
	ReconnectWait time.Duration

	// MaxReconnects is the maximum number of reconnection attempts (-1 for unlimited)
	MaxReconnects int

	// PartitionDetection configures how to detect partitions from hub
	PartitionDetection PartitionConfig
}

// TLSConfig configures TLS for leaf connections.
type TLSConfig struct {
	CertFile   string
	KeyFile    string
	CAFile     string
	SkipVerify bool
}

// PartitionConfig configures partition detection.
type PartitionConfig struct {
	// HeartbeatInterval is how often the hub sends heartbeats
	HeartbeatInterval time.Duration

	// HeartbeatTimeout is how long before declaring hub unreachable
	HeartbeatTimeout time.Duration

	// MissedHeartbeats is how many consecutive missed heartbeats before action
	MissedHeartbeats int

	// GracePeriod is additional time before taking partition action
	GracePeriod time.Duration
}

// PartitionMode defines how to handle network partitions.
type PartitionMode int

const (
	// PartitionFailSafe stops writes during partition (default, safest)
	PartitionFailSafe PartitionMode = iota
	// PartitionAutonomous allows continued operation during partition
	PartitionAutonomous
	// PartitionCoordinated uses external coordinator for decisions
	PartitionCoordinated
)

// PartitionStrategy configures how to handle network partitions.
type PartitionStrategy struct {
	// Mode determines the partition handling strategy
	Mode PartitionMode

	// RequireQuorum requires majority of leaves to agree before promotion
	RequireQuorum bool

	// QuorumSize is the number of leaves required (0 = auto/majority)
	QuorumSize int

	// MaxAutonomousDuration limits autonomous operation time
	MaxAutonomousDuration time.Duration

	// FallbackToReadOnly after MaxAutonomousDuration
	FallbackToReadOnly bool

	// OnPartition callback when partition is detected
	OnPartition func(context.Context, PartitionEvent) error
}

// PartitionEventType represents the type of partition event.
type PartitionEventType int

const (
	// PartitionHubUnreachable indicates the hub cannot be reached.
	PartitionHubUnreachable PartitionEventType = iota
	// PartitionLeafUnreachable indicates a leaf cannot be reached.
	PartitionLeafUnreachable
	// PartitionHealed indicates the partition has been resolved.
	PartitionHealed
	// PartitionQuorumLost indicates quorum of leaves was lost.
	PartitionQuorumLost
)

// PartitionEvent represents a partition detection event.
type PartitionEvent struct {
	Type            PartitionEventType
	Platform        string
	Description     string
	Duration        time.Duration
	ReachableLeaves int
	TotalLeaves     int
	Timestamp       time.Time
}

// LeafConnectionStatus represents the status of a leaf connection.
type LeafConnectionStatus string

const (
	LeafStatusConnected    LeafConnectionStatus = "connected"
	LeafStatusDisconnected LeafConnectionStatus = "disconnected"
	LeafStatusConnecting   LeafConnectionStatus = "connecting"
	LeafStatusPartitioned  LeafConnectionStatus = "partitioned"
)

// LeafInfo represents information about a connected leaf.
type LeafInfo struct {
	Platform     string               `json:"platform"`
	Zone         string               `json:"zone"`
	Status       LeafConnectionStatus `json:"status"`
	LatencyMs    int64                `json:"latency_ms"`
	ConnectedAt  time.Time            `json:"connected_at"`
	LastSeen     time.Time            `json:"last_seen"`
	MessagesSent int64                `json:"messages_sent"`
	MessagesRecv int64                `json:"messages_recv"`
	BytesSent    int64                `json:"bytes_sent"`
	BytesRecv    int64                `json:"bytes_recv"`
}

// LeafManager manages leaf node connections for a platform.
type LeafManager struct {
	platform *Platform
	role     LeafRole

	// Hub configuration (if role is hub)
	hubConfig *LeafHubConfig

	// Leaf configuration (if role is leaf)
	leafConfig *LeafConnectionConfig

	// Connected leaves (if hub)
	leaves   map[string]*LeafInfo
	leavesMu sync.RWMutex

	// Hub connection info (if leaf)
	hubInfo   *LeafInfo
	hubInfoMu sync.RWMutex

	// Partition state
	partitionStrategy *PartitionStrategy
	isPartitioned     bool
	partitionStart    time.Time
	partitionMu       sync.RWMutex

	// Heartbeat tracking
	lastHubHeartbeat time.Time
	missedHeartbeats int
	heartbeatMu      sync.RWMutex

	// Callbacks
	onPartition func(PartitionEvent)
	onLeafJoin  func(LeafInfo)
	onLeafLeave func(LeafInfo)

	// NATS subscriptions
	heartbeatSub *nats.Subscription
	leafInfoSub  *nats.Subscription

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewLeafManager creates a new leaf manager for the platform.
func NewLeafManager(p *Platform) *LeafManager {
	return &LeafManager{
		platform: p,
		role:     LeafRoleStandalone,
		leaves:   make(map[string]*LeafInfo),
	}
}

// ConfigureAsHub configures the platform as a hub.
func (m *LeafManager) ConfigureAsHub(config LeafHubConfig) {
	m.role = LeafRoleHub
	m.hubConfig = &config

	// Set defaults
	if m.hubConfig.HeartbeatInterval == 0 {
		m.hubConfig.HeartbeatInterval = 3 * time.Second
	}
	if m.hubConfig.HeartbeatTimeout == 0 {
		m.hubConfig.HeartbeatTimeout = 15 * time.Second
	}
}

// ConfigureAsLeaf configures the platform as a leaf.
func (m *LeafManager) ConfigureAsLeaf(config LeafConnectionConfig) {
	m.role = LeafRoleLeaf
	m.leafConfig = &config

	// Set defaults
	if m.leafConfig.ReconnectWait == 0 {
		m.leafConfig.ReconnectWait = 2 * time.Second
	}
	if m.leafConfig.MaxReconnects == 0 {
		m.leafConfig.MaxReconnects = -1 // Unlimited
	}
	if m.leafConfig.PartitionDetection.HeartbeatInterval == 0 {
		m.leafConfig.PartitionDetection.HeartbeatInterval = 3 * time.Second
	}
	if m.leafConfig.PartitionDetection.HeartbeatTimeout == 0 {
		m.leafConfig.PartitionDetection.HeartbeatTimeout = 15 * time.Second
	}
	if m.leafConfig.PartitionDetection.MissedHeartbeats == 0 {
		m.leafConfig.PartitionDetection.MissedHeartbeats = 5
	}
	if m.leafConfig.PartitionDetection.GracePeriod == 0 {
		m.leafConfig.PartitionDetection.GracePeriod = 10 * time.Second
	}
}

// SetPartitionStrategy sets the partition handling strategy.
func (m *LeafManager) SetPartitionStrategy(strategy PartitionStrategy) {
	m.partitionStrategy = &strategy
}

// Start starts the leaf manager.
func (m *LeafManager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	switch m.role {
	case LeafRoleHub:
		return m.startHub()
	case LeafRoleLeaf:
		return m.startLeaf()
	default:
		return nil // Standalone, nothing to do
	}
}

// Stop stops the leaf manager.
func (m *LeafManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	if m.heartbeatSub != nil {
		m.heartbeatSub.Unsubscribe()
	}
	if m.leafInfoSub != nil {
		m.leafInfoSub.Unsubscribe()
	}

	m.wg.Wait()
}

// Role returns the leaf role of this platform.
func (m *LeafManager) Role() LeafRole {
	return m.role
}

// IsHub returns true if this platform is configured as a hub.
func (m *LeafManager) IsHub() bool {
	return m.role == LeafRoleHub
}

// IsLeaf returns true if this platform is configured as a leaf.
func (m *LeafManager) IsLeaf() bool {
	return m.role == LeafRoleLeaf
}

// HubPlatform returns the hub platform name (for leaf platforms).
func (m *LeafManager) HubPlatform() string {
	if m.leafConfig != nil {
		return m.leafConfig.HubPlatform
	}
	return ""
}

// GetLeaves returns information about all connected leaves.
func (m *LeafManager) GetLeaves() []LeafInfo {
	m.leavesMu.RLock()
	defer m.leavesMu.RUnlock()

	result := make([]LeafInfo, 0, len(m.leaves))
	for _, leaf := range m.leaves {
		result = append(result, *leaf)
	}
	return result
}

// GetLeaf returns information about a specific leaf.
func (m *LeafManager) GetLeaf(platform string) (*LeafInfo, bool) {
	m.leavesMu.RLock()
	defer m.leavesMu.RUnlock()

	leaf, ok := m.leaves[platform]
	if !ok {
		return nil, false
	}
	copy := *leaf
	return &copy, true
}

// GetHubInfo returns information about the hub connection (for leaf platforms).
func (m *LeafManager) GetHubInfo() *LeafInfo {
	m.hubInfoMu.RLock()
	defer m.hubInfoMu.RUnlock()

	if m.hubInfo == nil {
		return nil
	}
	copy := *m.hubInfo
	return &copy
}

// IsPartitioned returns true if the platform is currently partitioned.
func (m *LeafManager) IsPartitioned() bool {
	m.partitionMu.RLock()
	defer m.partitionMu.RUnlock()
	return m.isPartitioned
}

// PartitionDuration returns how long the current partition has lasted.
func (m *LeafManager) PartitionDuration() time.Duration {
	m.partitionMu.RLock()
	defer m.partitionMu.RUnlock()

	if !m.isPartitioned {
		return 0
	}
	return time.Since(m.partitionStart)
}

// ConnectedLeafCount returns the number of connected leaves.
func (m *LeafManager) ConnectedLeafCount() int {
	m.leavesMu.RLock()
	defer m.leavesMu.RUnlock()

	count := 0
	for _, leaf := range m.leaves {
		if leaf.Status == LeafStatusConnected {
			count++
		}
	}
	return count
}

// HasQuorum returns true if the hub has quorum of leaves.
func (m *LeafManager) HasQuorum() bool {
	if m.partitionStrategy == nil || !m.partitionStrategy.RequireQuorum {
		return true
	}

	connected := m.ConnectedLeafCount()
	total := len(m.leaves)

	if m.partitionStrategy.QuorumSize > 0 {
		return connected >= m.partitionStrategy.QuorumSize
	}

	// Auto quorum = majority
	return connected > total/2
}

// OnPartition registers a callback for partition events.
func (m *LeafManager) OnPartition(fn func(PartitionEvent)) {
	m.onPartition = fn
}

// OnLeafJoin registers a callback for when a leaf joins.
func (m *LeafManager) OnLeafJoin(fn func(LeafInfo)) {
	m.onLeafJoin = fn
}

// OnLeafLeave registers a callback for when a leaf leaves.
func (m *LeafManager) OnLeafLeave(fn func(LeafInfo)) {
	m.onLeafLeave = fn
}

// startHub starts hub-specific functionality.
func (m *LeafManager) startHub() error {
	// Subscribe to leaf announcements
	subject := fmt.Sprintf("%s._leaf.announce", m.platform.name)
	sub, err := m.platform.nc.Subscribe(subject, m.handleLeafAnnounce)
	if err != nil {
		return fmt.Errorf("failed to subscribe to leaf announcements: %w", err)
	}
	m.leafInfoSub = sub

	// Setup cross-platform RPC handling
	if err := m.setupCrossPlatformRPC(); err != nil {
		return fmt.Errorf("failed to setup cross-platform RPC: %w", err)
	}

	// Start heartbeat sender
	m.wg.Add(1)
	go m.hubHeartbeatLoop()

	// Start leaf health checker
	m.wg.Add(1)
	go m.checkLeafHealth()

	m.platform.audit.Log(m.ctx, AuditEntry{
		Category: "leaf",
		Action:   "hub_started",
		Data:     map[string]any{"port": m.hubConfig.Port},
	})

	return nil
}

// startLeaf starts leaf-specific functionality.
func (m *LeafManager) startLeaf() error {
	// Initialize hub info
	m.hubInfoMu.Lock()
	m.hubInfo = &LeafInfo{
		Platform: m.leafConfig.HubPlatform,
		Status:   LeafStatusConnecting,
	}
	m.hubInfoMu.Unlock()

	// Subscribe to hub heartbeats
	subject := fmt.Sprintf("%s._leaf.heartbeat", m.leafConfig.HubPlatform)
	sub, err := m.platform.nc.Subscribe(subject, m.handleHubHeartbeat)
	if err != nil {
		return fmt.Errorf("failed to subscribe to hub heartbeats: %w", err)
	}
	m.heartbeatSub = sub

	// Setup cross-platform RPC handling
	if err := m.setupCrossPlatformRPC(); err != nil {
		return fmt.Errorf("failed to setup cross-platform RPC: %w", err)
	}

	// Announce ourselves to the hub
	if err := m.announceToHub(); err != nil {
		// Non-fatal - hub might not be available yet
		m.platform.audit.Log(m.ctx, AuditEntry{
			Category: "leaf",
			Action:   "announce_failed",
			Data:     map[string]any{"error": err.Error()},
		})
	}

	// Start heartbeat monitor
	m.wg.Add(1)
	go m.monitorHubHeartbeat()

	// Start periodic re-announcement
	m.wg.Add(1)
	go m.leafAnnounceLoop()

	m.platform.audit.Log(m.ctx, AuditEntry{
		Category: "leaf",
		Action:   "leaf_started",
		Data:     map[string]any{"hub": m.leafConfig.HubPlatform},
	})

	return nil
}

// hubHeartbeatLoop sends periodic heartbeats to all leaves.
func (m *LeafManager) hubHeartbeatLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.hubConfig.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.sendHubHeartbeat()
		}
	}
}

// sendHubHeartbeat sends a heartbeat to all leaves.
func (m *LeafManager) sendHubHeartbeat() {
	heartbeat := leafHeartbeat{
		Platform:  m.platform.name,
		NodeID:    m.platform.nodeID,
		Timestamp: time.Now(),
		LeafCount: m.ConnectedLeafCount(),
	}

	data, err := json.Marshal(heartbeat)
	if err != nil {
		return
	}

	subject := fmt.Sprintf("%s._leaf.heartbeat", m.platform.name)
	m.platform.nc.Publish(subject, data)
}

// leafHeartbeat represents a heartbeat message.
type leafHeartbeat struct {
	Platform  string    `json:"platform"`
	NodeID    string    `json:"node_id"`
	Timestamp time.Time `json:"timestamp"`
	LeafCount int       `json:"leaf_count"`
}

// handleHubHeartbeat handles incoming heartbeats from the hub.
func (m *LeafManager) handleHubHeartbeat(msg *nats.Msg) {
	var heartbeat leafHeartbeat
	if err := json.Unmarshal(msg.Data, &heartbeat); err != nil {
		return
	}

	m.heartbeatMu.Lock()
	m.lastHubHeartbeat = time.Now()
	m.missedHeartbeats = 0
	m.heartbeatMu.Unlock()

	// Update hub info
	m.hubInfoMu.Lock()
	if m.hubInfo != nil {
		wasDisconnected := m.hubInfo.Status != LeafStatusConnected
		m.hubInfo.Status = LeafStatusConnected
		m.hubInfo.LastSeen = time.Now()
		if wasDisconnected {
			m.hubInfo.ConnectedAt = time.Now()
		}
	}
	m.hubInfoMu.Unlock()

	// Check if we were partitioned and now recovered
	m.partitionMu.Lock()
	if m.isPartitioned {
		m.isPartitioned = false
		duration := time.Since(m.partitionStart)
		m.partitionMu.Unlock()

		m.notifyPartitionEvent(PartitionEvent{
			Type:        PartitionHealed,
			Platform:    m.leafConfig.HubPlatform,
			Description: "Connection to hub restored",
			Duration:    duration,
			Timestamp:   time.Now(),
		})

		m.platform.audit.Log(m.ctx, AuditEntry{
			Category: "leaf",
			Action:   "partition_healed",
			Data:     map[string]any{"duration_seconds": duration.Seconds()},
		})
	} else {
		m.partitionMu.Unlock()
	}

	// Update metrics
	m.platform.metrics.SetLeafConnectionStatus(m.leafConfig.HubPlatform, true)
}

// monitorHubHeartbeat monitors for missed hub heartbeats.
func (m *LeafManager) monitorHubHeartbeat() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.leafConfig.PartitionDetection.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.checkHubHeartbeat()
		}
	}
}

// checkHubHeartbeat checks if hub heartbeats have been missed.
func (m *LeafManager) checkHubHeartbeat() {
	m.heartbeatMu.Lock()
	timeSinceHeartbeat := time.Since(m.lastHubHeartbeat)
	if timeSinceHeartbeat > m.leafConfig.PartitionDetection.HeartbeatInterval*2 {
		m.missedHeartbeats++
	}
	missed := m.missedHeartbeats
	m.heartbeatMu.Unlock()

	threshold := m.leafConfig.PartitionDetection.MissedHeartbeats

	if missed >= threshold {
		m.handlePotentialPartition(timeSinceHeartbeat)
	}
}

// handlePotentialPartition handles a potential partition from the hub.
func (m *LeafManager) handlePotentialPartition(timeSinceHeartbeat time.Duration) {
	m.partitionMu.Lock()
	if m.isPartitioned {
		m.partitionMu.Unlock()
		return // Already handling partition
	}
	m.isPartitioned = true
	m.partitionStart = time.Now()
	m.partitionMu.Unlock()

	// Update hub info
	m.hubInfoMu.Lock()
	if m.hubInfo != nil {
		m.hubInfo.Status = LeafStatusPartitioned
	}
	m.hubInfoMu.Unlock()

	// Notify
	m.notifyPartitionEvent(PartitionEvent{
		Type:        PartitionHubUnreachable,
		Platform:    m.leafConfig.HubPlatform,
		Description: fmt.Sprintf("Hub unreachable for %v", timeSinceHeartbeat),
		Duration:    timeSinceHeartbeat,
		Timestamp:   time.Now(),
	})

	m.platform.audit.Log(m.ctx, AuditEntry{
		Category: "leaf",
		Action:   "partition_detected",
		Data: map[string]any{
			"hub":                    m.leafConfig.HubPlatform,
			"time_since_heartbeat_s": timeSinceHeartbeat.Seconds(),
		},
	})

	// Update metrics
	m.platform.metrics.SetLeafConnectionStatus(m.leafConfig.HubPlatform, false)
	m.platform.metrics.IncLeafPartitionDetected(m.leafConfig.HubPlatform)
}

// notifyPartitionEvent notifies registered callbacks about partition events.
func (m *LeafManager) notifyPartitionEvent(event PartitionEvent) {
	if m.onPartition != nil {
		go m.onPartition(event)
	}

	if m.partitionStrategy != nil && m.partitionStrategy.OnPartition != nil {
		go m.partitionStrategy.OnPartition(m.ctx, event)
	}
}

// leafAnnounceLoop periodically announces this leaf to the hub.
func (m *LeafManager) leafAnnounceLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.announceToHub()
		}
	}
}

// announceToHub announces this leaf platform to the hub.
func (m *LeafManager) announceToHub() error {
	zone := m.platform.opts.labels["zone"]
	if zone == "" {
		zone = m.platform.opts.labels["region"]
	}

	announce := leafAnnouncement{
		Platform:  m.platform.name,
		NodeID:    m.platform.nodeID,
		Zone:      zone,
		Labels:    m.platform.opts.labels,
		Timestamp: time.Now(),
	}

	data, err := json.Marshal(announce)
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("%s._leaf.announce", m.leafConfig.HubPlatform)
	return m.platform.nc.Publish(subject, data)
}

// leafAnnouncement represents a leaf announcement message.
type leafAnnouncement struct {
	Platform  string            `json:"platform"`
	NodeID    string            `json:"node_id"`
	Zone      string            `json:"zone"`
	Labels    map[string]string `json:"labels"`
	Timestamp time.Time         `json:"timestamp"`
}

// handleLeafAnnounce handles incoming leaf announcements (hub side).
func (m *LeafManager) handleLeafAnnounce(msg *nats.Msg) {
	var announce leafAnnouncement
	if err := json.Unmarshal(msg.Data, &announce); err != nil {
		return
	}

	m.leavesMu.Lock()
	existing, existed := m.leaves[announce.Platform]
	if !existed {
		// New leaf
		m.leaves[announce.Platform] = &LeafInfo{
			Platform:    announce.Platform,
			Zone:        announce.Zone,
			Status:      LeafStatusConnected,
			ConnectedAt: time.Now(),
			LastSeen:    time.Now(),
		}
		newLeaf := *m.leaves[announce.Platform]
		m.leavesMu.Unlock()

		m.platform.audit.Log(m.ctx, AuditEntry{
			Category: "leaf",
			Action:   "leaf_connected",
			Data:     map[string]any{"platform": announce.Platform, "zone": announce.Zone},
		})

		if m.onLeafJoin != nil {
			go m.onLeafJoin(newLeaf)
		}

		m.platform.metrics.IncLeafConnections(announce.Platform)
	} else {
		// Update existing
		existing.LastSeen = time.Now()
		existing.Status = LeafStatusConnected
		existing.Zone = announce.Zone
		m.leavesMu.Unlock()
	}
}

// checkLeafHealth periodically checks health of connected leaves.
func (m *LeafManager) checkLeafHealth() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.hubConfig.HeartbeatTimeout / 2)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.pruneStaleLeaves()
		}
	}
}

// pruneStaleLeaves removes leaves that haven't been seen recently.
func (m *LeafManager) pruneStaleLeaves() {
	m.leavesMu.Lock()
	defer m.leavesMu.Unlock()

	now := time.Now()
	for platform, leaf := range m.leaves {
		if now.Sub(leaf.LastSeen) > m.hubConfig.HeartbeatTimeout {
			delete(m.leaves, platform)

			m.platform.audit.Log(m.ctx, AuditEntry{
				Category: "leaf",
				Action:   "leaf_disconnected",
				Data:     map[string]any{"platform": platform},
			})

			if m.onLeafLeave != nil {
				go m.onLeafLeave(*leaf)
			}

			m.platform.metrics.IncLeafDisconnections(platform)
		}
	}
}

// Ping measures the round-trip time to a leaf platform.
func (m *LeafManager) Ping(ctx context.Context, platform string) (time.Duration, error) {
	if !m.IsHub() {
		return 0, fmt.Errorf("ping only available from hub")
	}

	start := time.Now()
	subject := fmt.Sprintf("%s._leaf.ping", platform)

	_, err := m.platform.nc.RequestWithContext(ctx, subject, []byte("ping"))
	if err != nil {
		return 0, err
	}

	return time.Since(start), nil
}

// CallHub makes an RPC call to the hub platform from a leaf.
func (m *LeafManager) CallHub(ctx context.Context, appName, method string, payload []byte) ([]byte, error) {
	if !m.IsLeaf() {
		return nil, fmt.Errorf("CallHub only available from leaf platforms")
	}

	return m.CallPlatform(ctx, m.leafConfig.HubPlatform, appName, method, payload)
}

// CallPlatform makes an RPC call to another platform.
func (m *LeafManager) CallPlatform(ctx context.Context, platform, appName, method string, payload []byte) ([]byte, error) {
	// Route through the cross-platform RPC subject
	subject := fmt.Sprintf("%s.%s.rpc._crossplatform", platform, appName)

	req := crossPlatformRPCRequest{
		SourcePlatform: m.platform.name,
		SourceNode:     m.platform.nodeID,
		Method:         method,
		Payload:        payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	msg, err := m.platform.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, fmt.Errorf("cross-platform call failed: %w", err)
	}

	var resp crossPlatformRPCResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, err
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	return resp.Data, nil
}

// crossPlatformRPCRequest represents a cross-platform RPC request.
type crossPlatformRPCRequest struct {
	SourcePlatform string `json:"source_platform"`
	SourceNode     string `json:"source_node"`
	Method         string `json:"method"`
	Payload        []byte `json:"payload"`
}

// crossPlatformRPCResponse represents a cross-platform RPC response.
type crossPlatformRPCResponse struct {
	Data  []byte `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// PublishToHub publishes an event to the hub platform.
func (m *LeafManager) PublishToHub(ctx context.Context, subject string, data []byte) error {
	if !m.IsLeaf() {
		return fmt.Errorf("PublishToHub only available from leaf platforms")
	}

	return m.PublishToPlatform(ctx, m.leafConfig.HubPlatform, subject, data)
}

// PublishToPlatform publishes an event to another platform.
func (m *LeafManager) PublishToPlatform(ctx context.Context, platform, subject string, data []byte) error {
	fullSubject := fmt.Sprintf("%s._crossplatform.events.%s", platform, subject)

	msg := &nats.Msg{
		Subject: fullSubject,
		Data:    data,
		Header:  nats.Header{},
	}
	msg.Header.Set("X-Source-Platform", m.platform.name)
	msg.Header.Set("X-Source-Node", m.platform.nodeID)

	return m.platform.nc.PublishMsg(msg)
}

// SubscribeCrossPlatform subscribes to events from other platforms.
func (m *LeafManager) SubscribeCrossPlatform(pattern string, handler func(platform, subject string, data []byte)) (*nats.Subscription, error) {
	fullPattern := fmt.Sprintf("%s._crossplatform.events.%s", m.platform.name, pattern)

	return m.platform.nc.Subscribe(fullPattern, func(msg *nats.Msg) {
		sourcePlatform := msg.Header.Get("X-Source-Platform")
		handler(sourcePlatform, msg.Subject, msg.Data)
	})
}

// DiscoverCrossPlatform discovers services across all connected platforms.
func (m *LeafManager) DiscoverCrossPlatform(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	var allServices []ServiceInstance

	// Get local services first
	local, err := m.platform.DiscoverServices(ctx, appName, serviceName)
	if err == nil {
		allServices = append(allServices, local...)
	}

	if m.IsHub() {
		// Query all leaves
		m.leavesMu.RLock()
		leaves := make([]string, 0, len(m.leaves))
		for platform := range m.leaves {
			leaves = append(leaves, platform)
		}
		m.leavesMu.RUnlock()

		for _, platform := range leaves {
			services, err := m.queryPlatformServices(ctx, platform, appName, serviceName)
			if err == nil {
				allServices = append(allServices, services...)
			}
		}
	} else if m.IsLeaf() {
		// Query hub
		services, err := m.queryPlatformServices(ctx, m.leafConfig.HubPlatform, appName, serviceName)
		if err == nil {
			allServices = append(allServices, services...)
		}
	}

	return allServices, nil
}

// queryPlatformServices queries services from a specific platform.
func (m *LeafManager) queryPlatformServices(ctx context.Context, platform, appName, serviceName string) ([]ServiceInstance, error) {
	subject := fmt.Sprintf("%s._services.query", platform)

	req := serviceQueryRequest{
		AppName:     appName,
		ServiceName: serviceName,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	msg, err := m.platform.nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return nil, err
	}

	var resp serviceQueryResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, err
	}

	return resp.Services, nil
}

// serviceQueryRequest represents a service query request.
type serviceQueryRequest struct {
	AppName     string `json:"app_name"`
	ServiceName string `json:"service_name"`
}

// serviceQueryResponse represents a service query response.
type serviceQueryResponse struct {
	Services []ServiceInstance `json:"services"`
}

// setupCrossPlatformRPC sets up cross-platform RPC handling.
func (m *LeafManager) setupCrossPlatformRPC() error {
	// Subscribe to cross-platform RPC requests
	subject := fmt.Sprintf("%s.*.rpc._crossplatform", m.platform.name)
	_, err := m.platform.nc.Subscribe(subject, m.handleCrossPlatformRPC)
	if err != nil {
		return err
	}

	// Subscribe to service queries
	serviceSubject := fmt.Sprintf("%s._services.query", m.platform.name)
	_, err = m.platform.nc.Subscribe(serviceSubject, m.handleServiceQuery)
	if err != nil {
		return err
	}

	// Subscribe to ping requests
	pingSubject := fmt.Sprintf("%s._leaf.ping", m.platform.name)
	_, err = m.platform.nc.Subscribe(pingSubject, func(msg *nats.Msg) {
		if msg.Reply != "" {
			m.platform.nc.Publish(msg.Reply, []byte("pong"))
		}
	})

	return err
}

// handleCrossPlatformRPC handles incoming cross-platform RPC requests.
func (m *LeafManager) handleCrossPlatformRPC(msg *nats.Msg) {
	var req crossPlatformRPCRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		m.sendCrossPlatformResponse(msg, nil, err)
		return
	}

	// Extract app name from subject (platform.app.rpc._crossplatform)
	parts := splitSubject(msg.Subject)
	if len(parts) < 4 {
		m.sendCrossPlatformResponse(msg, nil, fmt.Errorf("invalid subject"))
		return
	}
	appName := parts[1]

	// Find the app
	m.platform.appsMu.RLock()
	app, ok := m.platform.apps[appName]
	m.platform.appsMu.RUnlock()

	if !ok {
		m.sendCrossPlatformResponse(msg, nil, fmt.Errorf("app not found: %s", appName))
		return
	}

	// Find the handler
	handler, ok := app.handlers[req.Method]
	if !ok {
		m.sendCrossPlatformResponse(msg, nil, fmt.Errorf("method not found: %s", req.Method))
		return
	}

	// Execute handler
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := handler(ctx, req.Payload)
	m.sendCrossPlatformResponse(msg, data, err)
}

// sendCrossPlatformResponse sends a cross-platform RPC response.
func (m *LeafManager) sendCrossPlatformResponse(msg *nats.Msg, data []byte, err error) {
	if msg.Reply == "" {
		return
	}

	resp := crossPlatformRPCResponse{Data: data}
	if err != nil {
		resp.Error = err.Error()
	}

	respData, _ := json.Marshal(resp)
	m.platform.nc.Publish(msg.Reply, respData)
}

// handleServiceQuery handles incoming service query requests.
func (m *LeafManager) handleServiceQuery(msg *nats.Msg) {
	var req serviceQueryRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	services, err := m.platform.DiscoverServices(ctx, req.AppName, req.ServiceName)
	if err != nil {
		services = []ServiceInstance{}
	}

	resp := serviceQueryResponse{Services: services}
	respData, _ := json.Marshal(resp)

	if msg.Reply != "" {
		m.platform.nc.Publish(msg.Reply, respData)
	}
}

// splitSubject splits a NATS subject into parts.
func splitSubject(subject string) []string {
	var parts []string
	var current string
	for _, c := range subject {
		if c == '.' {
			parts = append(parts, current)
			current = ""
		} else {
			current += string(c)
		}
	}
	if current != "" {
		parts = append(parts, current)
	}
	return parts
}

// LoadTLSConfig loads TLS configuration from files.
func LoadTLSConfig(config *TLSConfig) (*tls.Config, error) {
	if config == nil {
		return nil, nil
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.SkipVerify,
	}

	if config.CertFile != "" && config.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(config.CertFile, config.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}

	// Note: CA file loading would require additional crypto/x509 imports
	// For now, we'll rely on system CA pool

	return tlsConfig, nil
}
