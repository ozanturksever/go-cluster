package cluster

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const (
	DefaultLeaseTTL      = 10 * time.Second
	DefaultRenewInterval = 3 * time.Second
)

// Config configures the cluster node.
type Config struct {
	ClusterID       string
	NodeID          string
	NATSURLs        []string
	NATSCredentials string
	LeaseTTL        time.Duration
	RenewInterval   time.Duration
	Logger          *slog.Logger
}

func (c *Config) Validate() error {
	if c.ClusterID == "" {
		return errors.New("ClusterID is required")
	}
	if c.NodeID == "" {
		return errors.New("NodeID is required")
	}
	if len(c.NATSURLs) == 0 {
		return errors.New("at least one NATS URL is required")
	}
	return nil
}

func (c *Config) applyDefaults() {
	if c.LeaseTTL == 0 {
		c.LeaseTTL = DefaultLeaseTTL
	}
	if c.RenewInterval == 0 {
		c.RenewInterval = DefaultRenewInterval
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
}

// Node represents a participant in a cluster.
type Node struct {
	cfg    Config
	hooks  Hooks
	logger *slog.Logger

	nc *nats.Conn
	js jetstream.JetStream
	kv jetstream.KeyValue

	mu      sync.RWMutex
	lease   *Lease
	role    Role
	stopCh  chan struct{}
	wg      sync.WaitGroup
	running bool
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
	n.mu.Unlock()

	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}
	if n.cfg.NATSCredentials != "" {
		opts = append(opts, nats.UserCredentials(n.cfg.NATSCredentials))
	}

	nc, err := nats.Connect(n.cfg.NATSURLs[0], opts...)
	if err != nil {
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("connect to NATS: %w", err)
	}
	n.nc = nc

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		n.mu.Lock()
		n.running = false
		n.mu.Unlock()
		return fmt.Errorf("create JetStream: %w", err)
	}
	n.js = js

	bucketName := fmt.Sprintf("cluster-%s", n.cfg.ClusterID)
	kv, err := js.CreateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket: bucketName,
		TTL:    n.cfg.LeaseTTL * 2,
	})
	if err != nil {
		kv, err = js.KeyValue(ctx, bucketName)
		if err != nil {
			nc.Close()
			n.mu.Lock()
			n.running = false
			n.mu.Unlock()
			return fmt.Errorf("create/get KV bucket: %w", err)
		}
	}
	n.kv = kv

	_ = kv.Delete(ctx, n.cooldownKey())

	n.tryAcquireOrRenew(ctx)

	n.wg.Add(1)
	go n.electionLoop(ctx)

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

// StepDown voluntarily gives up leadership.
func (n *Node) StepDown(ctx context.Context) error {
	n.mu.Lock()
	if n.role != RolePrimary {
		n.mu.Unlock()
		return ErrNotLeader
	}

	if err := n.setCooldown(ctx); err != nil {
		n.logger.Warn("failed to set cooldown marker", "error", err)
	}

	if err := n.kv.Delete(ctx, n.leaseKey()); err != nil && !errors.Is(err, jetstream.ErrKeyNotFound) {
		n.mu.Unlock()
		return fmt.Errorf("delete lease: %w", err)
	}

	n.role = RolePassive
	n.lease = nil
	n.logger.Info("stepped down from leadership", "cooldown", n.cfg.LeaseTTL)
	n.mu.Unlock()

	// Call hook outside the lock to avoid deadlock
	n.notifyLoseLeadership(ctx)

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
