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
	"github.com/ozanturksever/go-cluster/vip"
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
	preferLabels  []LabelPreference
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
	onActive            func(context.Context) error
	onPassive           func(context.Context) error
	onStart             func(context.Context) error
	onStop              func(context.Context) error
	onLeaderChange      func(context.Context, string) error
	onOwn               func(context.Context, []int) error
	onRelease           func(context.Context, []int) error
	onMemberJoin        func(context.Context, Member) error
	onMemberLeave       func(context.Context, Member) error
	onMigrationStart    func(context.Context, string, string) error
	onMigrationComplete func(context.Context, string, string) error
	onPlacementInvalid  func(context.Context, string) error
	onHealthChange      func(context.Context, HealthStatus) error

	// Services
	services map[string]*ServiceConfig

	// Handlers
	handlers map[string]Handler

	// Middleware
	middleware *MiddlewareChain

	// Backend
	backend            Backend
	backendCoordinator *BackendCoordinator

	// VIP
	vipConfig  *VIPConfig
	vipManager *vip.Manager

	// Runtime state
	mu       sync.RWMutex
	role     Role
	election *Election
	kv       *KV
	rpc      *RPC
	events   *Events
	db       *DatabaseManager
	ring     *HashRing

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
	membership      *Membership
	audit           *Audit
	metrics         *Metrics
	health          *Health
	platformEvents  *PlatformEvents
	scheduler       *Scheduler
	serviceRegistry *ServiceRegistry
	leafManager     *LeafManager

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
	p.platformEvents = NewPlatformEvents(p)
	p.scheduler = NewScheduler(p, DefaultMigrationPolicy())
	p.serviceRegistry = NewServiceRegistry(p)
	p.leafManager = NewLeafManager(p)

	// Configure leaf manager based on options
	if options.leafHubConfig != nil {
		p.leafManager.ConfigureAsHub(*options.leafHubConfig)
	}
	if options.leafConnectionConfig != nil {
		p.leafManager.ConfigureAsLeaf(*options.leafConnectionConfig)
	}
	if options.partitionStrategy != nil {
		p.leafManager.SetPartitionStrategy(*options.partitionStrategy)
	}

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

	if err := p.scheduler.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start scheduler: %w", err)
	}

	if err := p.serviceRegistry.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start service registry: %w", err)
	}

	if err := p.leafManager.Start(runCtx); err != nil {
		return fmt.Errorf("failed to start leaf manager: %w", err)
	}

	// Setup cross-platform RPC handling
	if err := p.leafManager.setupCrossPlatformRPC(); err != nil {
		return fmt.Errorf("failed to setup cross-platform RPC: %w", err)
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

	// Stop leaf manager
	p.leafManager.Stop()

	// Stop service registry
	p.serviceRegistry.Stop()

	// Stop scheduler
	p.scheduler.Stop()

	// Stop membership first so other nodes see we're leaving
	p.membership.Stop()

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

// Events returns the platform-wide events manager.
func (p *Platform) Events() *PlatformEvents {
	return p.platformEvents
}

// Membership returns the membership manager.
func (p *Platform) Membership() *Membership {
	return p.membership
}

// Audit returns the audit logger.
func (p *Platform) Audit() *Audit {
	return p.audit
}

// Metrics returns the metrics manager.
func (p *Platform) Metrics() *Metrics {
	return p.metrics
}

// Health returns the health manager.
func (p *Platform) Health() *Health {
	return p.health
}

// Scheduler returns the placement scheduler.
func (p *Platform) Scheduler() *Scheduler {
	return p.scheduler
}

// ServiceRegistry returns the service registry.
func (p *Platform) ServiceRegistry() *ServiceRegistry {
	return p.serviceRegistry
}

// LeafManager returns the leaf node manager.
func (p *Platform) LeafManager() *LeafManager {
	return p.leafManager
}

// IsHub returns true if this platform is configured as a hub.
func (p *Platform) IsHub() bool {
	return p.leafManager.IsHub()
}

// IsLeaf returns true if this platform is configured as a leaf.
func (p *Platform) IsLeaf() bool {
	return p.leafManager.IsLeaf()
}

// HubPlatform returns the hub platform name (for leaf platforms).
func (p *Platform) HubPlatform() string {
	return p.leafManager.HubPlatform()
}

// CallHub makes an RPC call to the hub platform from a leaf.
func (p *Platform) CallHub(ctx context.Context, appName, method string, payload []byte) ([]byte, error) {
	return p.leafManager.CallHub(ctx, appName, method, payload)
}

// CallPlatform makes an RPC call to another platform.
func (p *Platform) CallPlatform(ctx context.Context, platform, appName, method string, payload []byte) ([]byte, error) {
	return p.leafManager.CallPlatform(ctx, platform, appName, method, payload)
}

// PublishToHub publishes an event to the hub platform.
func (p *Platform) PublishToHub(ctx context.Context, subject string, data []byte) error {
	return p.leafManager.PublishToHub(ctx, subject, data)
}

// PublishToPlatform publishes an event to another platform.
func (p *Platform) PublishToPlatform(ctx context.Context, platform, subject string, data []byte) error {
	return p.leafManager.PublishToPlatform(ctx, platform, subject, data)
}

// DiscoverCrossPlatform discovers services across all connected platforms.
func (p *Platform) DiscoverCrossPlatform(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	return p.leafManager.DiscoverCrossPlatform(ctx, appName, serviceName)
}

// GetLeaves returns information about all connected leaves (hub only).
func (p *Platform) GetLeaves() []LeafInfo {
	return p.leafManager.GetLeaves()
}

// GetHubInfo returns information about the hub connection (leaf only).
func (p *Platform) GetHubInfo() *LeafInfo {
	return p.leafManager.GetHubInfo()
}

// IsPartitioned returns true if the platform is currently partitioned from hub.
func (p *Platform) IsPartitioned() bool {
	return p.leafManager.IsPartitioned()
}

// OnPartition registers a callback for partition events.
func (p *Platform) OnPartition(fn func(PartitionEvent)) {
	p.leafManager.OnPartition(fn)
}

// DiscoverServices discovers all instances of a service.
func (p *Platform) DiscoverServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	return p.serviceRegistry.DiscoverServices(ctx, appName, serviceName)
}

// DiscoverServicesByTag discovers services by tag.
func (p *Platform) DiscoverServicesByTag(ctx context.Context, tag string) ([]ServiceInstance, error) {
	return p.serviceRegistry.DiscoverServicesByTag(ctx, tag)
}

// DiscoverServicesByMetadata discovers services by metadata.
func (p *Platform) DiscoverServicesByMetadata(ctx context.Context, metadata map[string]string) ([]ServiceInstance, error) {
	return p.serviceRegistry.DiscoverServicesByMetadata(ctx, metadata)
}

// DiscoverHealthyServices discovers only healthy instances of a service.
func (p *Platform) DiscoverHealthyServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	return p.serviceRegistry.DiscoverHealthyServices(ctx, appName, serviceName)
}

// WatchServices watches for changes to a specific service.
func (p *Platform) WatchServices(ctx context.Context, appName, serviceName string) *ServiceWatcher {
	return p.serviceRegistry.WatchServices(ctx, appName, serviceName)
}

// MoveApp initiates a migration of an app from one node to another.
func (p *Platform) MoveApp(ctx context.Context, req MoveRequest) (*Migration, error) {
	return p.scheduler.MoveApp(ctx, req)
}

// DrainNode moves all apps away from a node.
func (p *Platform) DrainNode(ctx context.Context, nodeID string, opts DrainOptions) error {
	return p.scheduler.DrainNode(ctx, nodeID, opts)
}

// RebalanceApp redistributes an app's instances across the cluster.
func (p *Platform) RebalanceApp(ctx context.Context, appName string) error {
	return p.scheduler.RebalanceApp(ctx, appName)
}

// UpdateNodeLabels updates labels on a specific node.
// This should be called via RPC to update labels on a remote node.
func (p *Platform) UpdateNodeLabels(ctx context.Context, nodeID string, labels map[string]string) error {
	if nodeID == p.nodeID {
		return p.SetLabels(ctx, labels)
	}
	// For remote nodes, this would require an RPC call
	return fmt.Errorf("cannot update labels on remote node %s directly", nodeID)
}

// SetLabels sets labels on the current node.
func (p *Platform) SetLabels(ctx context.Context, labels map[string]string) error {
	for k, v := range labels {
		p.opts.labels[k] = v
	}
	// Re-register with updated labels
	return p.membership.Register()
}

// RemoveLabel removes a label from the current node.
func (p *Platform) RemoveLabel(ctx context.Context, key string) error {
	delete(p.opts.labels, key)
	// Re-register with updated labels
	return p.membership.Register()
}

// WatchLabels registers a callback for label changes on any node.
func (p *Platform) WatchLabels(fn LabelWatcherFunc) {
	p.scheduler.WatchLabels(fn)
}

// CordonNode marks a node as unschedulable.
func (p *Platform) CordonNode(nodeID string) {
	p.scheduler.CordonNode(nodeID)
}

// UncordonNode marks a node as schedulable.
func (p *Platform) UncordonNode(nodeID string) {
	p.scheduler.UncordonNode(nodeID)
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
		middleware:       NewMiddlewareChain(),
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

// Pool is an alias for Spread, indicating a load-balanced pool pattern.
func Pool() AppOption {
	return Spread()
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

// Ring returns the app's ring manager (nil if ring not configured).
func (a *App) Ring() *HashRing {
	return a.ring
}

// WAL returns the app's WAL manager (nil if SQLite not configured).
func (a *App) WAL() *WALManager {
	if a.db == nil {
		return nil
	}
	return a.db.WAL()
}

// Snapshots returns the app's snapshot manager (nil if SQLite not configured).
func (a *App) Snapshots() *SnapshotManager {
	if a.db == nil {
		return nil
	}
	return a.db.Snapshots()
}

// DB returns the database manager (nil if SQLite not configured).
func (a *App) DB() *DatabaseManager {
	return a.db
}

// Handle registers a handler for the given method.
func (a *App) Handle(method string, handler Handler) {
	a.handlers[method] = handler
}

// Use adds middleware(s) to the app's middleware chain.
// Middleware is executed in the order added for all handlers.
func (a *App) Use(middlewares ...Middleware) {
	a.middleware.Use(middlewares...)
}

// Middleware returns the app's middleware chain.
func (a *App) Middleware() *MiddlewareChain {
	return a.middleware
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

// OnHealthChange sets the callback for when the health status changes.
func (a *App) OnHealthChange(fn func(context.Context, HealthStatus) error) {
	a.onHealthChange = fn
}

// OnMigrationStart sets the callback for when a migration starts.
// Return an error to reject the migration.
func (a *App) OnMigrationStart(fn func(context.Context, string, string) error) {
	a.onMigrationStart = fn
}

// OnMigrationComplete sets the callback for when a migration completes.
func (a *App) OnMigrationComplete(fn func(context.Context, string, string) error) {
	a.onMigrationComplete = fn
}

// OnPlacementInvalid sets the callback for when placement constraints cannot be satisfied.
func (a *App) OnPlacementInvalid(fn func(context.Context, string) error) {
	a.onPlacementInvalid = fn
}

// ExposeService exposes a service for discovery.
// This can be called before or after the app starts.
// If called after start, the service is immediately registered.
func (a *App) ExposeService(name string, config ServiceConfig) {
	a.mu.Lock()
	a.services[name] = &config
	a.mu.Unlock()

	// If app is already running, register immediately
	if a.platform != nil && a.ctx != nil {
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			defer cancel()
			a.platform.serviceRegistry.Register(ctx, a.name, name, &config)
		}()
	}
}

// GetServices returns all exposed services for this app.
func (a *App) GetServices() map[string]*ServiceConfig {
	a.mu.RLock()
	defer a.mu.RUnlock()

	result := make(map[string]*ServiceConfig, len(a.services))
	for k, v := range a.services {
		result[k] = v
	}
	return result
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

	// Initialize Database Manager (if SQLite configured)
	if a.sqlitePath != "" {
		a.db, err = NewDatabaseManager(a)
		if err != nil {
			return fmt.Errorf("failed to create Database Manager: %w", err)
		}
		if a.db != nil {
			if err := a.db.Start(a.ctx); err != nil {
				return fmt.Errorf("failed to start Database Manager: %w", err)
			}
		}
	}

	// Initialize Backend Coordinator (if backend configured)
	if a.backend != nil {
		config := DefaultBackendCoordinatorConfig()
		a.backendCoordinator = NewBackendCoordinator(a, a.backend, config)
		if err := a.backendCoordinator.Start(a.ctx); err != nil {
			return fmt.Errorf("failed to start Backend Coordinator: %w", err)
		}
	}

	// Initialize VIP Manager (if VIP configured)
	if a.vipConfig != nil {
		vipCfg := vip.Config{
			CIDR:      a.vipConfig.CIDR,
			Interface: a.vipConfig.Interface,
		}
		a.vipManager, err = vip.NewManager(vipCfg)
		if err != nil {
			// Log warning but don't fail - VIP might not be available in all environments
			p.audit.Log(ctx, AuditEntry{
				Category: "vip",
				Action:   "init_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		} else {
			if err := a.vipManager.Start(a.ctx); err != nil {
				p.audit.Log(ctx, AuditEntry{
					Category: "vip",
					Action:   "start_error",
					Data:     map[string]any{"app": a.name, "error": err.Error()},
				})
			}
			// Set up health failure callback
			a.vipManager.OnHealthFailure(func(err error) {
				p.audit.Log(a.ctx, AuditEntry{
					Category: "vip",
					Action:   "health_failure",
					Data:     map[string]any{"app": a.name, "error": err.Error()},
				})
			})
		}
	}

	// Initialize Ring (if ring partitions configured)
	if a.ringPartitions > 0 {
		a.ring, err = NewHashRing(a)
		if err != nil {
			return fmt.Errorf("failed to create Ring: %w", err)
		}
		if err := a.ring.Start(a.ctx); err != nil {
			return fmt.Errorf("failed to start Ring: %w", err)
		}
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

		// Initialize role for non-leaders after a brief delay
		// This handles the case where a node starts up and is never elected leader
		go func() {
			// Brief delay to allow first election attempt to complete
			select {
			case <-time.After(500 * time.Millisecond):
			case <-a.ctx.Done():
				return
			}
			// If we're not leader and role is not set, become standby
			if !a.election.IsLeader() && a.Role() == "" {
				a.becomeStandby()
			}
		}()

	case ModeSpread, ModeReplicas:
		// All instances are active in pool mode
		a.setRole(RoleActive)
		if a.onStart != nil {
			if err := a.onStart(a.ctx); err != nil {
				return fmt.Errorf("onStart failed: %w", err)
			}
		}
	}

	// Register exposed services
	a.registerServices()

	p.audit.Log(ctx, AuditEntry{
		Category: "app",
		Action:   "started",
		Data:     map[string]any{"app": a.name, "mode": a.mode},
	})

	return nil
}

// registerServices registers all exposed services with the service registry.
func (a *App) registerServices() {
	a.mu.RLock()
	services := make(map[string]*ServiceConfig, len(a.services))
	for k, v := range a.services {
		services[k] = v
	}
	a.mu.RUnlock()

	for name, config := range services {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		if err := a.platform.serviceRegistry.Register(ctx, a.name, name, config); err != nil {
			a.platform.audit.Log(ctx, AuditEntry{
				Category: "service",
				Action:   "registration_error",
				Data:     map[string]any{"app": a.name, "service": name, "error": err.Error()},
			})
		}
		cancel()
	}
}

// deregisterServices deregisters all exposed services from the service registry.
func (a *App) deregisterServices() {
	a.mu.RLock()
	serviceNames := make([]string, 0, len(a.services))
	for name := range a.services {
		serviceNames = append(serviceNames, name)
	}
	a.mu.RUnlock()

	for _, name := range serviceNames {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		a.platform.serviceRegistry.Deregister(ctx, a.name, name)
		cancel()
	}
}

// stop gracefully stops the app.
func (a *App) stop() {
	p := a.platform

	// Log stop initiated
	p.audit.Log(context.Background(), AuditEntry{
		Category: "app",
		Action:   "stopping",
		Data:     map[string]any{"app": a.name},
	})

	// Step 1: If leader, step down gracefully first
	if a.election != nil && a.IsLeader() {
		ctx, cancel := context.WithTimeout(context.Background(), p.opts.shutdownTimeout/3)
		a.election.StepDown(ctx)
		cancel()
	}

	// Step 2: Call onStop hook
	if a.onStop != nil {
		ctx, cancel := context.WithTimeout(context.Background(), p.opts.shutdownTimeout/3)
		if err := a.onStop(ctx); err != nil {
			p.audit.Log(context.Background(), AuditEntry{
				Category: "app",
				Action:   "onStop_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
		cancel()
	}

	// Step 3: Stop election
	if a.election != nil {
		a.election.Stop()
	}

	// Step 4: Stop RPC
	if a.rpc != nil {
		a.rpc.Stop()
	}

	// Step 5: Stop events
	if a.events != nil {
		a.events.Stop()
	}

	// Step 6: Stop ring
	if a.ring != nil {
		a.ring.Stop()
	}

	// Step 7: Stop backend coordinator
	if a.backendCoordinator != nil {
		a.backendCoordinator.Stop()
	}

	// Step 8: Stop VIP manager
	if a.vipManager != nil {
		a.vipManager.Stop()
	}

	// Step 9: Deregister services
	a.deregisterServices()

	// Step 10: Stop database manager
	if a.db != nil {
		a.db.Stop()
	}

	// Step 11: Cancel context
	if a.cancel != nil {
		a.cancel()
	}

	p.audit.Log(context.Background(), AuditEntry{
		Category: "app",
		Action:   "stopped",
		Data:     map[string]any{"app": a.name},
	})
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
// Follows the proper sequencing:
// 1. Win election (already done)
// 2. Final WAL catch-up + Promote replica DB
// 3. Acquire VIP (TODO)
// 4. Start backend service
// 5. Call onActive hook
func (a *App) becomeLeader() {
	a.setRole(RoleLeader)

	// Update metrics
	a.platform.metrics.SetLeader(a.name, true)

	// Log leader acquisition
	a.platform.audit.Log(a.ctx, AuditEntry{
		Category: "election",
		Action:   "leader_acquired",
		Data:     map[string]any{"app": a.name},
	})

	// Step 1: Switch database to primary mode (includes WAL catch-up)
	if a.db != nil {
		if err := a.db.Promote(a.ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "database",
				Action:   "promote_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
			// Continue - backend might still work without DB
		}
	}

	// Step 2: Acquire VIP
	if a.vipManager != nil {
		if err := a.vipManager.Acquire(a.ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "vip",
				Action:   "acquire_error",
				Data:     map[string]any{"app": a.name, "vip": a.vipConfig.CIDR, "error": err.Error()},
			})
			// Continue - VIP failure shouldn't prevent becoming leader
		} else {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "vip",
				Action:   "acquired",
				Data:     map[string]any{"app": a.name, "vip": a.vipConfig.CIDR, "interface": a.vipConfig.Interface},
			})
		}
	}

	// Step 3: Start backend service
	if a.backendCoordinator != nil {
		if err := a.backendCoordinator.StartBackend(a.ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "backend",
				Action:   "start_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
			// Continue - we're still the leader even if backend fails
		}
	}

	// Step 4: Call onActive hook
	if a.onActive != nil {
		if err := a.callHookWithTimeout("onActive", func(ctx context.Context) error {
			return a.onActive(ctx)
		}); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "app",
				Action:   "onActive_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
	}
}

// becomeStandby transitions to standby role.
// Follows the proper sequencing (reverse of becomeLeader):
// 1. Call onPassive hook
// 2. Stop backend service
// 3. Release VIP (TODO)
// 4. Switch database to replica mode
func (a *App) becomeStandby() {
	a.setRole(RoleStandby)

	// Update metrics
	a.platform.metrics.SetLeader(a.name, false)

	// Log leader loss
	a.platform.audit.Log(a.ctx, AuditEntry{
		Category: "election",
		Action:   "leader_lost",
		Data:     map[string]any{"app": a.name},
	})

	// Step 1: Call onPassive hook first (app can prepare for demotion)
	if a.onPassive != nil {
		if err := a.callHookWithTimeout("onPassive", func(ctx context.Context) error {
			return a.onPassive(ctx)
		}); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "app",
				Action:   "onPassive_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
	}

	// Step 2: Stop backend service
	if a.backendCoordinator != nil {
		ctx, cancel := context.WithTimeout(context.Background(), a.platform.opts.shutdownTimeout/3)
		if err := a.backendCoordinator.StopBackend(ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "backend",
				Action:   "stop_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
		cancel()
	}

	// Step 3: Release VIP
	if a.vipManager != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := a.vipManager.Release(ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "vip",
				Action:   "release_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		} else {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "vip",
				Action:   "released",
				Data:     map[string]any{"app": a.name, "vip": a.vipConfig.CIDR},
			})
		}
		cancel()
	}

	// Step 4: Switch database to replica mode
	if a.db != nil {
		if err := a.db.Demote(a.ctx); err != nil {
			a.platform.audit.Log(a.ctx, AuditEntry{
				Category: "database",
				Action:   "demote_error",
				Data:     map[string]any{"app": a.name, "error": err.Error()},
			})
		}
	}
}

// callHookWithTimeout calls a hook function with a timeout.
func (a *App) callHookWithTimeout(hookName string, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(a.ctx, a.platform.opts.hookTimeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- fn(ctx)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("hook %s timed out", hookName)
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

// Election returns the election instance for the app (nil for non-singleton modes).
func (a *App) Election() *Election {
	return a.election
}

// Errors
var (
	ErrNotLeader          = errors.New("not leader")
	ErrNoLeader           = errors.New("no leader")
	ErrLockHeld           = errors.New("lock already held")
	ErrLockNotHeld        = errors.New("lock not held")
	ErrTimeout            = errors.New("operation timed out")
	ErrAppNotFound        = errors.New("app not found")
	ErrNodeNotFound       = errors.New("node not found")
	ErrHookTimeout        = errors.New("hook timed out")
	ErrShuttingDown       = errors.New("platform is shutting down")
	ErrNodeAlreadyPresent = errors.New("node with this ID is already present in the cluster")
)
