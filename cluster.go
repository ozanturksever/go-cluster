// Package cluster provides a clustering toolkit for building HA applications in Go.
//
// go-cluster is NATS-native and provides:
//   - Leader election via NATS KV
//   - Distributed KV and locks
//   - SQLite3 WAL replication via Litestream
//   - Service discovery
//   - Multi-zone deployments via NATS leaf nodes
package cluster

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

// Mode defines how many instances of an app should run.
type Mode int

const (
	// ModeSingleton runs exactly one active instance with standbys.
	ModeSingleton Mode = iota
	// ModeSpread runs on all eligible nodes.
	ModeSpread
	// ModeReplicas runs on N nodes out of M total.
	ModeReplicas
)

// Role represents the current role of a node in the cluster.
type Role string

const (
	RoleLeader  Role = "leader"
	RoleStandby Role = "standby"
	RoleActive  Role = "active" // For pool/spread mode where all are active
)

// App represents an isolated application component in the cluster.
type App struct {
	name     string
	platform *Platform
	mode     Mode
	replicas int

	// Placement constraints
	nodeSelector  []string
	labelSelector map[string]string
	avoidLabels   map[string]string
	withApps      []string
	awayFromApps  []string

	// Pattern
	ringPartitions int

	// Data
	sqlitePath    string
	replicaPath   string
	snapshotInterval time.Duration
	retention     time.Duration

	// Lifecycle hooks
	onActive         func(context.Context) error
	onPassive        func(context.Context) error
	onStart          func(context.Context) error
	onStop           func(context.Context) error
	onLeaderChange   func(context.Context, string) error
	onOwn            func(context.Context, []int) error
	onRelease        func(context.Context, []int) error
	onMemberJoin     func(context.Context, Member) error
	onMemberLeave    func(context.Context, Member) error
	onMigrationStart func(context.Context, string, string) error
	onMigrationComplete func(context.Context, string, string) error

	// Services
	services map[string]*ServiceConfig

	// Handlers
	handlers map[string]Handler

	// Runtime state
	mu       sync.RWMutex
	role     Role
	election *Election
	kv       *KV
	rpc      *RPC
	events   *Events

	ctx    context.Context
	cancel context.CancelFunc
}

// Handler is a function that handles RPC requests.
type Handler func(ctx context.Context, req []byte) ([]byte, error)

// Platform represents a cluster of nodes running multiple apps.
type Platform struct {
	name   string
	nodeID string

	nc *nats.Conn
	js jetstream.JetStream

	// Configuration
	opts *platformOptions

	// Apps registered on this platform
	apps   map[string]*App
	appsMu sync.RWMutex

	// Components
	membership *Membership
	audit      *Audit
	metrics    *Metrics
	health     *Health

	// Runtime
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Member represents a node in the cluster.
type Member struct {
	NodeID    string            `json:"node_id"`
	Platform  string            `json:"platform"`
	Address   string            `json:"address"`
	Labels    map[string]string `json:"labels"`
	JoinedAt  time.Time         `json:"joined_at"`
	LastSeen  time.Time         `json:"last_seen"`
	Apps      []string          `json:"apps"`
	IsHealthy bool              `json:"is_healthy"`
}

// NewPlatform creates a new platform instance.
func NewPlatform(name, nodeID, natsURL string, opts ...PlatformOption) (*Platform, error) {
	options := defaultPlatformOptions()
	for _, opt := range opts {
		opt(options)
	}

	// Connect to NATS
	natsOpts := []nats.Option{
		nats.Name(fmt.Sprintf("%s-%s", name, nodeID)),
		nats.ReconnectWait(2 * time.Second),
		nats.MaxReconnects(-1),
	}

	if options.natsCreds != "" {
		natsOpts = append(natsOpts, nats.UserCredentials(options.natsCreds))
	}

	nc, err := nats.Connect(natsURL, natsOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("failed to create JetStream context: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	p := &Platform{
		name:   name,
		nodeID: nodeID,
		nc:     nc,
		js:     js,
		opts:   options,
		apps:   make(map[string]*App),
		ctx:    ctx,
		cancel: cancel,
	}

	// Initialize components
	p.audit = NewAudit(p)
	p.metrics = NewMetrics(p)
	p.health = NewHealth(p)
	p.membership = NewMembership(p)

	return p, nil
}

// Register adds an app to the platform.
func (p *Platform) Register(app *App) {
	p.appsMu.Lock()
	defer p.appsMu.Unlock()

	app.platform = p
	p.apps[app.name] = app
}

// Run starts the platform and all registered apps.
func (p *Platform) Run(ctx context.Context) error {
	// Merge contexts
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
			p.cancel()
		case <-p.ctx.Done():
			cancel()
		}
	}()

	// Start platform components
	if err := p.membership.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start membership: %w", err)
	}

	if err := p.health.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start health: %w", err)
	}

	if err := p.metrics.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start metrics: %w", err)
	}

	if err := p.audit.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start audit: %w", err)
	}

	// Start all apps
	p.appsMu.RLock()
	apps := make([]*App, 0, len(p.apps))
	for _, app := range p.apps {
		apps = append(apps, app)
	}
	p.appsMu.RUnlock()

	for _, app := range apps {
		if err := app.start(runCtx); err != nil {
			return fmt.Errorf("failed to start app %s: %w", app.name, err)
		}
	}

	// Wait for shutdown
	<-runCtx.Done()

	// Stop all apps
	for _, app := range apps {
		app.stop()
	}

	// Close NATS connection
	p.nc.Close()

	return nil
}

// Name returns the platform name.
func (p *Platform) Name() string {
	return p.name
}

// NodeID returns this node's ID.
func (p *Platform) NodeID() string {
	return p.nodeID
}

// NATS returns the NATS connection.
func (p *Platform) NATS() *nats.Conn {
	return p.nc
}

// JetStream returns the JetStream context.
func (p *Platform) JetStream() jetstream.JetStream {
	return p.js
}

// NewApp creates a new app with the given name and options.
func NewApp(name string, opts ...AppOption) *App {
	app := &App{
		name:             name,
		mode:             ModeSingleton,
		replicas:         1,
		labelSelector:    make(map[string]string),
		avoidLabels:      make(map[string]string),
		services:         make(map[string]*ServiceConfig),
		handlers:         make(map[string]Handler),
		snapshotInterval: time.Hour,
		retention:        24 * time.Hour,
	}

	for _, opt := range opts {
		opt(app)
	}

	return app
}

// Singleton creates an app option for singleton mode (active-passive).
func Singleton() AppOption {
	return func(a *App) {
		a.mode = ModeSingleton
		a.replicas = 1
	}
}

// Spread creates an app option for spread mode (all nodes).
func Spread() AppOption {
	return func(a *App) {
		a.mode = ModeSpread
	}
}

// Replicas creates an app option for N-of-M mode.
func Replicas(n int) AppOption {
	return func(a *App) {
		a.mode = ModeReplicas
		a.replicas = n
	}
}

// Ring creates an app option for consistent hash ring pattern.
func Ring(partitions int) AppOption {
	return func(a *App) {
		a.ringPartitions = partitions
	}
}

// Name returns the app name.
func (a *App) Name() string {
	return a.name
}

// Platform returns the platform this app belongs to.
func (a *App) Platform() *Platform {
	return a.platform
}

// Role returns the current role of this app instance.
func (a *App) Role() Role {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.role
}

// IsLeader returns true if this instance is the leader.
func (a *App) IsLeader() bool {
	return a.Role() == RoleLeader
}

// KV returns the app's key-value store.
func (a *App) KV() *KV {
	return a.kv
}

// Lock returns a distributed lock with the given name.
func (a *App) Lock(name string) *Lock {
	return NewLock(a, name)
}

// RPC returns the app's RPC interface.
func (a *App) RPC() *RPC {
	return a.rpc
}

// Events returns the app's event interface.
func (a *App) Events() *Events {
	return a.events
}

// Handle registers a handler for the given method.
func (a *App) Handle(method string, handler Handler) {
	a.handlers[method] = handler
}

// OnActive sets the callback for when this instance becomes active (leader).
func (a *App) OnActive(fn func(context.Context) error) {
	a.onActive = fn
}

// OnPassive sets the callback for when this instance becomes passive (standby).
func (a *App) OnPassive(fn func(context.Context) error) {
	a.onPassive = fn
}

// OnStart sets the callback for when the app starts (pool/spread mode).
func (a *App) OnStart(fn func(context.Context) error) {
	a.onStart = fn
}

// OnStop sets the callback for when the app stops.
func (a *App) OnStop(fn func(context.Context) error) {
	a.onStop = fn
}

// OnLeaderChange sets the callback for when the leader changes.
func (a *App) OnLeaderChange(fn func(context.Context, string) error) {
	a.onLeaderChange = fn
}

// OnOwn sets the callback for when partitions are assigned (ring mode).
func (a *App) OnOwn(fn func(context.Context, []int) error) {
	a.onOwn = fn
}

// OnRelease sets the callback for when partitions are released (ring mode).
func (a *App) OnRelease(fn func(context.Context, []int) error) {
	a.onRelease = fn
}

// OnMemberJoin sets the callback for when a member joins.
func (a *App) OnMemberJoin(fn func(context.Context, Member) error) {
	a.onMemberJoin = fn
}

// OnMemberLeave sets the callback for when a member leaves.
func (a *App) OnMemberLeave(fn func(context.Context, Member) error) {
	a.onMemberLeave = fn
}

// ExposeService exposes a service for discovery.
func (a *App) ExposeService(name string, config ServiceConfig) {
	a.services[name] = &config
}

// start initializes and starts the app.
func (a *App) start(ctx context.Context) error {
	a.ctx, a.cancel = context.WithCancel(ctx)

	p := a.platform

	// Initialize KV
	var err error
	a.kv, err = NewKV(a)
	if err != nil {
		return fmt.Errorf("failed to create KV: %w", err)
	}

	// Initialize RPC
	a.rpc, err = NewRPC(a)
	if err != nil {
		return fmt.Errorf("failed to create RPC: %w", err)
	}

	// Initialize Events
	a.events, err = NewEvents(a)
	if err != nil {
		return fmt.Errorf("failed to create Events: %w", err)
	}

	// Start based on mode
	switch a.mode {
	case ModeSingleton:
		// Initialize election
		a.election, err = NewElection(a)
		if err != nil {
			return fmt.Errorf("failed to create election: %w", err)
		}

		if err := a.election.Start(a.ctx); err != nil {
			return fmt.Errorf("failed to start election: %w", err)
		}

		// Watch for leadership changes
		go a.watchLeadership()

	case ModeSpread, ModeReplicas:
		// All instances are active in pool mode
		a.setRole(RoleActive)
		if a.onStart != nil {
			if err := a.onStart(a.ctx); err != nil {
				return fmt.Errorf("onStart failed: %w", err)
			}
		}
	}

	p.audit.Log(ctx, AuditEntry{
		Category: "app",
		Action:   "started",
		Data:     map[string]any{"app": a.name, "mode": a.mode},
	})

	return nil
}

// stop gracefully stops the app.
func (a *App) stop() {
	if a.cancel != nil {
		a.cancel()
	}

	if a.onStop != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a.onStop(ctx)
	}

	if a.election != nil {
		a.election.Stop()
	}
}

// watchLeadership watches for leadership changes in singleton mode.
func (a *App) watchLeadership() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case isLeader := <-a.election.LeaderChan():
			if isLeader {
				a.becomeLeader()
			} else {
				a.becomeStandby()
			}
		}
	}
}

// becomeLeader transitions to leader role.
func (a *App) becomeLeader() {
	a.setRole(RoleLeader)

	a.platform.audit.Log(a.ctx, AuditEntry{
		Category: "election",
		Action:   "leader_acquired",
		Data:     map[string]any{"app": a.name},
	})

	if a.onActive != nil {
		if err := a.onActive(a.ctx); err != nil {
			// Log error but don't crash
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "app",
				Action:   "onActive_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
	}
}

// becomeStandby transitions to standby role.
func (a *App) becomeStandby() {
	a.setRole(RoleStandby)

	a.platform.audit.Log(a.ctx, AuditEntry{
		Category: "election",
		Action:   "leader_lost",
		Data:     map[string]any{"app": a.name},
	})

	if a.onPassive != nil {
		if err := a.onPassive(a.ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "app",
				Action:   "onPassive_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
	}
}

// setRole updates the current role.
func (a *App) setRole(role Role) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.role = role
}

// Call returns a client for calling another app's API.
func Call(appName string) *AppClient {
	return &AppClient{appName: appName}
}

// AppClient is a client for calling another app's API.
type AppClient struct {
	appName  string
	platform string
}

// Errors
var (
	ErrNotLeader    = errors.New("not leader")
	ErrNoLeader     = errors.New("no leader")
	ErrLockHeld     = errors.New("lock already held")
	ErrLockNotHeld  = errors.New("lock not held")
	ErrTimeout      = errors.New("operation timed out")
	ErrAppNotFound  = errors.New("app not found")
	ErrNodeNotFound = errors.New("node not found")
)
