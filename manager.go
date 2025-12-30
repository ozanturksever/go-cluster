package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ozanturksever/go-cluster/health"
	"github.com/ozanturksever/go-cluster/vip"
)

// Manager orchestrates cluster coordination including leader election,
// VIP management, and health checking. It provides high-level methods
// that map to CLI commands.
type Manager struct {
	cfg    FileConfig
	hooks  ManagerHooks
	logger *slog.Logger

	mu        sync.RWMutex
	node      *Node
	vipMgr    *vip.Manager
	healthChk *health.Checker
	running   bool
	startedAt time.Time
	stopCh    chan struct{}
	wg        sync.WaitGroup

	// vipExecutor allows injecting a mock executor for testing
	vipExecutor vip.Executor
}

// ManagerOption is a functional option for configuring a Manager.
type ManagerOption func(*Manager)

// WithVIPExecutor sets a custom VIP executor for testing.
func WithVIPExecutor(executor vip.Executor) ManagerOption {
	return func(m *Manager) {
		m.vipExecutor = executor
	}
}

// WithLogger sets a custom logger for the manager.
func WithLogger(logger *slog.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = logger
	}
}

// NewManager creates a new cluster manager with the given configuration and hooks.
func NewManager(cfg FileConfig, hooks ManagerHooks, opts ...ManagerOption) (*Manager, error) {
	cfg.ApplyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	if hooks == nil {
		hooks = NoOpManagerHooks{}
	}

	m := &Manager{
		cfg:    cfg,
		hooks:  hooks,
		logger: slog.Default().With("component", "manager", "cluster", cfg.ClusterID, "node", cfg.NodeID),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m, nil
}

// Init writes a default configuration file to the specified path.
func (m *Manager) Init(ctx context.Context, outputPath string) error {
	return WriteConfigToFile(&m.cfg, outputPath)
}

// Join connects to the cluster and detects the current leader.
// This is a quick operation that verifies connectivity and cluster state.
func (m *Manager) Join(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrManagerAlreadyRunning
	}
	m.mu.Unlock()

	// Create and start a temporary node to detect the cluster state
	nodeCfg := m.cfg.ToNodeConfig(m.logger)
	node, err := NewNode(nodeCfg, m.hooks)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	if err := node.Start(ctx); err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}

	// Wait briefly to detect leader
	select {
	case <-ctx.Done():
		node.Stop(ctx)
		return ctx.Err()
	case <-time.After(2 * time.Second):
	}

	leader := node.Leader()
	if leader == "" {
		m.logger.Info("joined cluster, no leader detected (this node may become leader)")
	} else {
		m.logger.Info("joined cluster", "leader", leader)
	}

	node.Stop(ctx)
	return nil
}

// Leave gracefully leaves the cluster, stepping down if this node is the leader.
func (m *Manager) Leave(ctx context.Context) error {
	m.mu.Lock()
	running := m.running
	node := m.node
	m.mu.Unlock()

	if !running || node == nil {
		// Try a quick connect/disconnect to step down if we're the leader
		nodeCfg := m.cfg.ToNodeConfig(m.logger)
		tempNode, err := NewNode(nodeCfg, NoOpHooks{})
		if err != nil {
			return fmt.Errorf("failed to create node: %w", err)
		}

		if err := tempNode.Start(ctx); err != nil {
			return fmt.Errorf("failed to connect to cluster: %w", err)
		}

		// Wait briefly to sync state
		time.Sleep(time.Second)

		if tempNode.IsLeader() {
			if err := tempNode.StepDown(ctx); err != nil {
				tempNode.Stop(ctx)
				return fmt.Errorf("failed to step down: %w", err)
			}
		}

		tempNode.Stop(ctx)
		m.logger.Info("left cluster")
		return nil
	}

	// If running, step down if leader
	if node.IsLeader() {
		if err := node.StepDown(ctx); err != nil {
			return fmt.Errorf("failed to step down: %w", err)
		}
	}

	m.logger.Info("left cluster")
	return nil
}

// Status returns the current status of this node in the cluster.
func (m *Manager) Status(ctx context.Context) (*Status, error) {
	m.mu.RLock()
	running := m.running
	node := m.node
	vipMgr := m.vipMgr
	startedAt := m.startedAt
	m.mu.RUnlock()

	status := &Status{
		ClusterID: m.cfg.ClusterID,
		NodeID:    m.cfg.NodeID,
		Role:      RolePassive,
		Connected: false,
	}

	if !running || node == nil {
		// Try to get status by connecting temporarily
		nodeCfg := m.cfg.ToNodeConfig(m.logger)
		tempNode, err := NewNode(nodeCfg, NoOpHooks{})
		if err != nil {
			return status, nil
		}

		if err := tempNode.Start(ctx); err != nil {
			return status, nil
		}
		defer tempNode.Stop(ctx)

		// Wait briefly to sync state
		time.Sleep(time.Second)

		status.Connected = true
		status.Role = tempNode.Role()
		status.Leader = tempNode.Leader()
		status.Epoch = tempNode.Epoch()
		return status, nil
	}

	// Get status from running node
	status.Connected = true
	status.Role = node.Role()
	status.Leader = node.Leader()
	status.Epoch = node.Epoch()

	if !startedAt.IsZero() {
		status.Uptime = time.Since(startedAt)
	}

	// Check VIP status
	if vipMgr != nil {
		acquired, err := vipMgr.IsAcquired(ctx)
		if err == nil {
			status.VIPHeld = acquired
		}
	}

	return status, nil
}

// Promote attempts to make this node become the leader.
// It blocks until leadership is acquired or the timeout is reached.
func (m *Manager) Promote(ctx context.Context) error {
	m.mu.RLock()
	running := m.running
	node := m.node
	m.mu.RUnlock()

	if running && node != nil {
		// Already running, just wait for leadership
		return m.waitForLeadership(ctx, node, 15*time.Second)
	}

	// Create temporary node for promotion
	nodeCfg := m.cfg.ToNodeConfig(m.logger)
	tempNode, err := NewNode(nodeCfg, NoOpHooks{})
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	if err := tempNode.Start(ctx); err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}
	defer tempNode.Stop(ctx)

	return m.waitForLeadership(ctx, tempNode, 15*time.Second)
}

func (m *Manager) waitForLeadership(ctx context.Context, node *Node, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if node.IsLeader() {
				m.logger.Info("promoted to leader")
				return nil
			}

			leader := node.Leader()
			if leader != "" && leader != m.cfg.NodeID {
				return fmt.Errorf("%w: current leader is %s", ErrAnotherLeaderExists, leader)
			}
		}
	}

	return ErrPromotionTimeout
}

// Demote voluntarily releases leadership and becomes a passive node.
func (m *Manager) Demote(ctx context.Context) error {
	m.mu.RLock()
	running := m.running
	node := m.node
	m.mu.RUnlock()

	if running && node != nil {
		if !node.IsLeader() {
			m.logger.Info("not currently the leader")
			return nil
		}
		return node.StepDown(ctx)
	}

	// Not running - connect temporarily to check/demote
	nodeCfg := m.cfg.ToNodeConfig(m.logger)
	tempNode, err := NewNode(nodeCfg, NoOpHooks{})
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	if err := tempNode.Start(ctx); err != nil {
		return fmt.Errorf("failed to connect to cluster: %w", err)
	}
	defer tempNode.Stop(ctx)

	// Wait briefly to sync state
	time.Sleep(time.Second)

	// Check if this node is the leader according to the KV store
	leader := tempNode.Leader()
	if leader != m.cfg.NodeID {
		if leader == "" {
			m.logger.Info("no leader currently exists")
		} else {
			m.logger.Info("this node is not the current leader", "leader", leader)
		}
		return nil
	}

	// Step down
	if err := tempNode.StepDown(ctx); err != nil {
		return fmt.Errorf("failed to step down: %w", err)
	}

	m.logger.Info("demoted from leader")
	return nil
}

// RunDaemon runs the cluster daemon until the context is cancelled.
// This is the main loop that manages leader election, VIP, and health checking.
func (m *Manager) RunDaemon(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return ErrManagerAlreadyRunning
	}
	m.running = true
	m.startedAt = time.Now()
	m.stopCh = make(chan struct{})
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.node = nil
		m.vipMgr = nil
		m.healthChk = nil
		m.mu.Unlock()
	}()

	// Notify daemon start
	if err := m.hooks.OnDaemonStart(ctx); err != nil {
		m.logger.Error("OnDaemonStart hook failed", "error", err)
	}

	defer func() {
		if err := m.hooks.OnDaemonStop(ctx); err != nil {
			m.logger.Error("OnDaemonStop hook failed", "error", err)
		}
	}()

	// Create the internal hooks wrapper that handles VIP
	internalHooks := &managerInternalHooks{
		manager: m,
		wrapped: m.hooks,
	}

	// Create and start the node
	nodeCfg := m.cfg.ToNodeConfig(m.logger)
	node, err := NewNode(nodeCfg, internalHooks)
	if err != nil {
		return fmt.Errorf("failed to create node: %w", err)
	}

	m.mu.Lock()
	m.node = node
	m.mu.Unlock()

	// Initialize VIP manager if configured
	if m.cfg.VIP.IsConfigured() {
		vipCfg := vip.Config{
			Address:   m.cfg.VIP.Address,
			Netmask:   m.cfg.VIP.Netmask,
			Interface: m.cfg.VIP.Interface,
			Logger:    m.logger,
		}
		vipMgr, err := vip.NewManager(vipCfg, m.vipExecutor)
		if err != nil {
			return fmt.Errorf("failed to create VIP manager: %w", err)
		}
		m.mu.Lock()
		m.vipMgr = vipMgr
		m.mu.Unlock()
	}

	// Initialize health checker
	healthCfg := health.Config{
		ClusterID:       m.cfg.ClusterID,
		NodeID:          m.cfg.NodeID,
		NATSURLs:        m.cfg.NATS.Servers,
		NATSCredentials: m.cfg.NATS.Credentials,
		Logger:          m.logger,
	}
	healthChk, err := health.NewChecker(healthCfg)
	if err != nil {
		return fmt.Errorf("failed to create health checker: %w", err)
	}

	m.mu.Lock()
	m.healthChk = healthChk
	m.mu.Unlock()

	// Start the node
	if err := node.Start(ctx); err != nil {
		return fmt.Errorf("failed to start node: %w", err)
	}

	// Start health checker
	if err := healthChk.Start(ctx); err != nil {
		m.logger.Warn("failed to start health checker", "error", err)
	}

	m.logger.Info("daemon started")

	// Run until context is cancelled
	<-ctx.Done()

	m.logger.Info("daemon stopping")

	// Stop health checker
	healthChk.Stop()

	// Release VIP if held
	m.mu.RLock()
	vipMgr := m.vipMgr
	m.mu.RUnlock()

	if vipMgr != nil {
		if err := vipMgr.Release(context.Background()); err != nil {
			m.logger.Warn("failed to release VIP", "error", err)
		}
	}

	// Stop the node
	if err := node.Stop(context.Background()); err != nil {
		m.logger.Warn("failed to stop node", "error", err)
	}

	m.logger.Info("daemon stopped")
	return nil
}

// IsRunning returns true if the daemon is currently running.
func (m *Manager) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// managerInternalHooks wraps the user's hooks to add VIP management.
type managerInternalHooks struct {
	manager *Manager
	wrapped ManagerHooks
}

func (h *managerInternalHooks) OnBecomeLeader(ctx context.Context) error {
	// Acquire VIP
	h.manager.mu.RLock()
	vipMgr := h.manager.vipMgr
	healthChk := h.manager.healthChk
	h.manager.mu.RUnlock()

	if vipMgr != nil {
		if err := vipMgr.Acquire(ctx); err != nil {
			h.manager.logger.Error("failed to acquire VIP", "error", err)
		}
	}

	// Update health checker
	if healthChk != nil {
		healthChk.SetRole("PRIMARY")
		healthChk.SetLeader(h.manager.cfg.NodeID)
	}

	// Call user hook
	return h.wrapped.OnBecomeLeader(ctx)
}

func (h *managerInternalHooks) OnLoseLeadership(ctx context.Context) error {
	// Release VIP
	h.manager.mu.RLock()
	vipMgr := h.manager.vipMgr
	healthChk := h.manager.healthChk
	h.manager.mu.RUnlock()

	if vipMgr != nil {
		if err := vipMgr.Release(ctx); err != nil {
			h.manager.logger.Error("failed to release VIP", "error", err)
		}
	}

	// Update health checker
	if healthChk != nil {
		healthChk.SetRole("PASSIVE")
	}

	// Call user hook
	return h.wrapped.OnLoseLeadership(ctx)
}

func (h *managerInternalHooks) OnLeaderChange(ctx context.Context, nodeID string) error {
	// Update health checker with new leader
	h.manager.mu.RLock()
	healthChk := h.manager.healthChk
	h.manager.mu.RUnlock()

	if healthChk != nil {
		healthChk.SetLeader(nodeID)
	}

	// Call user hook
	return h.wrapped.OnLeaderChange(ctx, nodeID)
}
