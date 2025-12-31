package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Node represents a participant in a cluster.
type Node struct {
	cfg    Config
	hooks  Hooks
	logger *slog.Logger

	nc      *nats.Conn
	js      jetstream.JetStream
	kv      jetstream.KeyValue
	watcher jetstream.KeyWatcher
	service *Service

	mu      sync.RWMutex
	lease   *Lease
	role    Role
	stopCh  chan struct{}
	wg      sync.WaitGroup
	running bool
	ctx     context.Context
}

// NewNode creates a new cluster node.
func NewNode(cfg Config, hooks Hooks) (*Node, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	cfg.applyDefaults()

	if hooks == nil {
		hooks = NoOpHooks{}
	}

	return &Node{
		cfg:    cfg,
		hooks:  hooks,
		logger: cfg.Logger.With("component", "cluster", "cluster", cfg.ClusterID, "node", cfg.NodeID),
		role:   RolePassive,
		stopCh: make(chan struct{}),
	}, nil
}

// Start connects to NATS and begins participating in leader election.
func (n *Node) Start(ctx context.Context) error {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return ErrAlreadyStarted
	}
	n.running = true
	n.stopCh = make(chan struct{})
	n.ctx = ctx
	n.mu.Unlock()

	// Connect to NATS with resilient options
	nc, err := n.connectNATS()
	if err != nil {
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("connect to NATS: %w", err)
	}
	n.nc = nc

	// Create JetStream context
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("create JetStream: %w", err)
	}
	n.js = js

	// Initialize KV bucket
	if err := n.initKVBucket(ctx); err != nil {
		nc.Close()
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("init KV bucket: %w", err)
	}

	// Clear any existing cooldown from previous runs
	n.clearCooldown(ctx)

	// Start the KV watcher
	if err := n.startWatcher(ctx); err != nil {
		nc.Close()
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("start watcher: %w", err)
	}

	// Start the heartbeat loop
	n.wg.Add(1)
	go n.heartbeatLoop(ctx)

	// Start the micro service
	if err := n.startService(); err != nil {
		n.logger.Warn("failed to start micro service", "error", err)
		// Non-fatal - continue without micro service
	}

	n.logger.Info("node started")
	return nil
}

// Stop stops the node.
func (n *Node) Stop(ctx context.Context) error {
	n.mu.Lock()
	if !n.running {
		n.mu.Unlock()
		return ErrNotStarted
	}
	n.running = false
	n.mu.Unlock()

	close(n.stopCh)
	n.wg.Wait()

	// Stop micro service
	if n.service != nil {
		_ = n.service.Stop()
	}

	// Stop watcher
	n.stopWatcher()

	n.mu.Lock()
	wasLeader := n.role == RolePrimary
	n.role = RolePassive
	n.lease = nil
	n.mu.Unlock()

	// Call hook outside the lock
	if wasLeader {
		n.notifyLoseLeadership(ctx)
	}

	if n.nc != nil {
		n.nc.Close()
		n.nc = nil
	}

	n.logger.Info("node stopped")
	return nil
}

// Role returns the current role of this node.
func (n *Node) Role() Role {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.role
}

// IsLeader returns true if this node is currently the leader.
func (n *Node) IsLeader() bool {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.role == RolePrimary
}

// Leader returns the current leader's node ID.
func (n *Node) Leader() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.lease != nil {
		return n.lease.NodeID
	}
	return ""
}

// Epoch returns the current epoch.
func (n *Node) Epoch() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.lease != nil {
		return n.lease.Epoch
	}
	return 0
}

// Connected returns true if the NATS connection is active.
func (n *Node) Connected() bool {
	n.mu.RLock()
	nc := n.nc
	n.mu.RUnlock()

	if nc == nil {
		return false
	}
	return nc.IsConnected()
}

// StepDown voluntarily gives up leadership.
func (n *Node) StepDown(ctx context.Context) error {
	return n.releaseLease(ctx)
}

// Service returns the micro service (may be nil if not started).
func (n *Node) Service() *Service {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.service
}

// connectNATS establishes a resilient NATS connection.
func (n *Node) connectNATS() (*nats.Conn, error) {
	opts := []nats.Option{
		nats.MaxReconnects(n.cfg.MaxReconnects),
		nats.ReconnectWait(n.cfg.ReconnectWait),
		nats.ReconnectBufSize(8 * 1024 * 1024), // 8MB buffer
		nats.PingInterval(2 * time.Second),      // Faster disconnect detection
		nats.MaxPingsOutstanding(2),             // Allow 2 missed pings before disconnect

		// Connection event handlers
		nats.DisconnectErrHandler(n.handleDisconnect),
		nats.ReconnectHandler(n.handleReconnect),
		nats.ClosedHandler(n.handleClosed),
		nats.ErrorHandler(n.handleError),
		nats.DiscoveredServersHandler(n.handleDiscoveredServers),
	}

	if n.cfg.NATSCredentials != "" {
		opts = append(opts, nats.UserCredentials(n.cfg.NATSCredentials))
	}

	// Connect with all configured URLs for automatic failover
	return nats.Connect(strings.Join(n.cfg.NATSURLs, ","), opts...)
}

// handleDisconnect is called when NATS disconnects.
func (n *Node) handleDisconnect(nc *nats.Conn, err error) {
	n.logger.Warn("NATS disconnected", "error", err)

	// Notify hooks
	ctx := context.Background()
	if hookErr := n.hooks.OnNATSDisconnect(ctx, err); hookErr != nil {
		n.logger.Error("OnNATSDisconnect hook failed", "error", hookErr)
	}
}

// handleReconnect is called when NATS reconnects.
func (n *Node) handleReconnect(nc *nats.Conn) {
	n.logger.Info("NATS reconnected", "server", nc.ConnectedUrl())

	// Restart the watcher after reconnection
	ctx := n.ctx
	if ctx == nil {
		ctx = context.Background()
	}

	if err := n.restartWatcher(ctx); err != nil {
		n.logger.Error("failed to restart watcher after reconnect", "error", err)
	}

	// Notify hooks
	if hookErr := n.hooks.OnNATSReconnect(ctx); hookErr != nil {
		n.logger.Error("OnNATSReconnect hook failed", "error", hookErr)
	}
}

// handleClosed is called when NATS connection is permanently closed.
func (n *Node) handleClosed(nc *nats.Conn) {
	n.logger.Warn("NATS connection closed")
}

// handleError is called on async NATS errors.
func (n *Node) handleError(nc *nats.Conn, sub *nats.Subscription, err error) {
	n.logger.Error("NATS async error", "error", err, "subject", sub.Subject)
}

// handleDiscoveredServers is called when new NATS servers are discovered.
func (n *Node) handleDiscoveredServers(nc *nats.Conn) {
	servers := nc.DiscoveredServers()
	n.logger.Info("discovered NATS servers", "servers", servers)
}

// startService starts the micro service.
func (n *Node) startService() error {
	callbacks := ServiceCallbacks{
		GetRole:     n.Role,
		GetLeader:   n.Leader,
		GetEpoch:    n.Epoch,
		IsConnected: n.Connected,
		DoStepdown:  n.StepDown,
	}

	service, err := NewService(n.cfg, n.nc, callbacks)
	if err != nil {
		return err
	}

	if err := service.Start(); err != nil {
		return err
	}

	n.mu.Lock()
	n.service = service
	n.mu.Unlock()

	return nil
}

func (n *Node) notifyBecomeLeader(ctx context.Context) {
	if err := n.hooks.OnBecomeLeader(ctx); err != nil {
		n.logger.Error("OnBecomeLeader hook failed", "error", err)
	}
}

func (n *Node) notifyLoseLeadership(ctx context.Context) {
	if err := n.hooks.OnLoseLeadership(ctx); err != nil {
		n.logger.Error("OnLoseLeadership hook failed", "error", err)
	}
}

func (n *Node) notifyLeaderChange(ctx context.Context, nodeID string) {
	if err := n.hooks.OnLeaderChange(ctx, nodeID); err != nil {
		n.logger.Error("OnLeaderChange hook failed", "error", err)
	}
}
