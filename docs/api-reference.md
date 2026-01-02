# API Reference

Complete API documentation for go-cluster.

## Package: cluster

```go
import "github.com/ozanturksever/go-cluster"
```

---

## Platform

### NewPlatform

```go
func NewPlatform(name, nodeID, natsURL string, opts ...PlatformOption) (*Platform, error)
```

Creates a new platform instance.

**Parameters:**
- `name` - Platform/cluster name
- `nodeID` - Unique identifier for this node
- `natsURL` - NATS server URL
- `opts` - Platform options

**Returns:**
- `*Platform` - Platform instance
- `error` - Error if creation fails

**Example:**
```go
platform, err := cluster.NewPlatform("prod", "node-1", "nats://localhost:4222",
    cluster.Labels(map[string]string{"zone": "us-east-1"}),
    cluster.HealthAddr(":8080"),
)
```

### Platform.Register

```go
func (p *Platform) Register(app *App)
```

Registers an app with the platform.

### Platform.Run

```go
func (p *Platform) Run(ctx context.Context) error
```

Starts the platform and all registered apps. Blocks until context is cancelled.

### Platform.Name

```go
func (p *Platform) Name() string
```

Returns the platform name.

### Platform.NodeID

```go
func (p *Platform) NodeID() string
```

Returns this node's ID.

### Platform.NATS

```go
func (p *Platform) NATS() *nats.Conn
```

Returns the NATS connection.

### Platform.JetStream

```go
func (p *Platform) JetStream() jetstream.JetStream
```

Returns the JetStream context.

### Platform.Membership

```go
func (p *Platform) Membership() *Membership
```

Returns the membership manager.

### Platform.Events

```go
func (p *Platform) Events() *PlatformEvents
```

Returns the platform-wide events manager.

### Platform.Audit

```go
func (p *Platform) Audit() *Audit
```

Returns the audit logger.

### Platform.Metrics

```go
func (p *Platform) Metrics() *Metrics
```

Returns the metrics manager.

### Platform.Health

```go
func (p *Platform) Health() *Health
```

Returns the health manager.

### Platform.ServiceRegistry

```go
func (p *Platform) ServiceRegistry() *ServiceRegistry
```

Returns the service registry.

### Platform.LeafManager

```go
func (p *Platform) LeafManager() *LeafManager
```

Returns the leaf node manager.

### Platform Operations

```go
// Move an app from one node to another
func (p *Platform) MoveApp(ctx context.Context, req MoveRequest) (*Migration, error)

// Drain all apps from a node
func (p *Platform) DrainNode(ctx context.Context, nodeID string, opts DrainOptions) error

// Rebalance an app across the cluster
func (p *Platform) RebalanceApp(ctx context.Context, appName string) error

// Update labels on a node
func (p *Platform) UpdateNodeLabels(ctx context.Context, nodeID string, labels map[string]string) error

// Set labels on current node
func (p *Platform) SetLabels(ctx context.Context, labels map[string]string) error

// Watch for label changes
func (p *Platform) WatchLabels(fn LabelWatcherFunc)

// Mark node as unschedulable
func (p *Platform) CordonNode(nodeID string)

// Mark node as schedulable
func (p *Platform) UncordonNode(nodeID string)
```

### Service Discovery

```go
// Discover all instances of a service
func (p *Platform) DiscoverServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error)

// Discover services by tag
func (p *Platform) DiscoverServicesByTag(ctx context.Context, tag string) ([]ServiceInstance, error)

// Discover services by metadata
func (p *Platform) DiscoverServicesByMetadata(ctx context.Context, metadata map[string]string) ([]ServiceInstance, error)

// Discover only healthy services
func (p *Platform) DiscoverHealthyServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error)

// Watch for service changes
func (p *Platform) WatchServices(ctx context.Context, appName, serviceName string) *ServiceWatcher
```

### Cross-Platform Communication

```go
// Check if this is a hub platform
func (p *Platform) IsHub() bool

// Check if this is a leaf platform
func (p *Platform) IsLeaf() bool

// Get hub platform name (for leaves)
func (p *Platform) HubPlatform() string

// Call the hub platform
func (p *Platform) CallHub(ctx context.Context, appName, method string, payload []byte) ([]byte, error)

// Call another platform
func (p *Platform) CallPlatform(ctx context.Context, platform, appName, method string, payload []byte) ([]byte, error)

// Publish event to hub
func (p *Platform) PublishToHub(ctx context.Context, subject string, data []byte) error

// Publish event to another platform
func (p *Platform) PublishToPlatform(ctx context.Context, platform, subject string, data []byte) error

// Discover services across platforms
func (p *Platform) DiscoverCrossPlatform(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error)

// Get connected leaves (hub only)
func (p *Platform) GetLeaves() []LeafInfo

// Get hub connection info (leaf only)
func (p *Platform) GetHubInfo() *LeafInfo

// Check if partitioned from hub
func (p *Platform) IsPartitioned() bool

// Register partition event callback
func (p *Platform) OnPartition(fn func(PartitionEvent))
```

---

## App

### NewApp

```go
func NewApp(name string, opts ...AppOption) *App
```

Creates a new app.

**Parameters:**
- `name` - App name (must be unique within platform)
- `opts` - App options

**Example:**
```go
app := cluster.NewApp("database",
    cluster.Singleton(),
    cluster.WithSQLite("/data/app.db"),
)
```

### App Mode Options

```go
// Exactly one active instance (active-passive)
func Singleton() AppOption

// Run on all eligible nodes
func Spread() AppOption

// Alias for Spread
func Pool() AppOption

// Run on N nodes
func Replicas(n int) AppOption

// Enable consistent hash ring with N partitions
func Ring(partitions int) AppOption
```

### Placement Options

```go
// Run on specific nodes
func OnNodes(nodes ...string) AppOption

// Run on nodes with label
func OnLabel(key, value string) AppOption

// Avoid nodes with label
func AvoidLabel(key, value string) AppOption

// Soft preference for label (weighted)
func PreferLabel(key, value string, weight ...int) AppOption

// Co-locate with app
func WithApp(appName string) AppOption

// Anti-affinity with app
func AwayFromApp(appName string) AppOption

// Resource requirements
func Resources(spec ResourceSpec) AppOption
```

### Data Options

```go
// Enable SQLite with WAL replication
func WithSQLite(path string, opts ...SQLiteOption) AppOption

// SQLite options
func SQLiteReplica(path string) SQLiteOption
func SQLiteSnapshotInterval(d time.Duration) SQLiteOption
func SQLiteRetention(d time.Duration) SQLiteOption

// Enable VIP failover
func VIP(cidr, iface string) AppOption

// Set backend controller
func WithBackend(b Backend) AppOption
```

### App Methods

```go
// Get app name
func (a *App) Name() string

// Get platform
func (a *App) Platform() *Platform

// Get current role
func (a *App) Role() Role

// Check if leader
func (a *App) IsLeader() bool

// Get KV store
func (a *App) KV() *KV

// Get distributed lock
func (a *App) Lock(name string) *Lock

// Get RPC interface
func (a *App) RPC() *RPC

// Get events interface
func (a *App) Events() *Events

// Get ring manager (nil if not ring mode)
func (a *App) Ring() *HashRing

// Get WAL manager (nil if no SQLite)
func (a *App) WAL() *WALManager

// Get snapshot manager (nil if no SQLite)
func (a *App) Snapshots() *SnapshotManager

// Get database manager (nil if no SQLite)
func (a *App) DB() *DatabaseManager

// Get election (nil if not singleton)
func (a *App) Election() *Election
```

### Lifecycle Hooks

```go
// Called when this instance becomes active (leader)
func (a *App) OnActive(fn func(context.Context) error)

// Called when this instance becomes passive (standby)
func (a *App) OnPassive(fn func(context.Context) error)

// Called when instance starts (pool/spread mode)
func (a *App) OnStart(fn func(context.Context) error)

// Called when instance stops
func (a *App) OnStop(fn func(context.Context) error)

// Called when leader changes
func (a *App) OnLeaderChange(fn func(context.Context, string) error)

// Called when partitions are assigned (ring mode)
func (a *App) OnOwn(fn func(context.Context, []int) error)

// Called when partitions are released (ring mode)
func (a *App) OnRelease(fn func(context.Context, []int) error)

// Called when a member joins
func (a *App) OnMemberJoin(fn func(context.Context, Member) error)

// Called when a member leaves
func (a *App) OnMemberLeave(fn func(context.Context, Member) error)

// Called when health status changes
func (a *App) OnHealthChange(fn func(context.Context, HealthStatus) error)

// Called when migration starts (can reject by returning error)
func (a *App) OnMigrationStart(fn func(context.Context, string, string) error)

// Called when migration completes
func (a *App) OnMigrationComplete(fn func(context.Context, string, string) error)

// Called when placement constraints can't be satisfied
func (a *App) OnPlacementInvalid(fn func(context.Context, string) error)
```

### Handlers and Middleware

```go
// Register RPC handler
func (a *App) Handle(method string, handler Handler)

// Add middleware
func (a *App) Use(middlewares ...Middleware)

// Get middleware chain
func (a *App) Middleware() *MiddlewareChain
```

### Services

```go
// Expose a service for discovery
func (a *App) ExposeService(name string, config ServiceConfig)

// Get all exposed services
func (a *App) GetServices() map[string]*ServiceConfig
```

---

## KV (Key-Value Store)

```go
// Get value
func (kv *KV) Get(ctx context.Context, key string) ([]byte, error)

// Put value
func (kv *KV) Put(ctx context.Context, key string, value []byte) error

// Delete key
func (kv *KV) Delete(ctx context.Context, key string) error

// List keys with prefix
func (kv *KV) Keys(ctx context.Context, prefix string) ([]string, error)

// Watch for changes
func (kv *KV) Watch(ctx context.Context, key string) (<-chan KeyValueEntry, error)
```

---

## Lock (Distributed Lock)

```go
// Acquire lock (blocks until acquired or context cancelled)
func (l *Lock) Lock(ctx context.Context) error

// Try to acquire lock (returns immediately)
func (l *Lock) TryLock(ctx context.Context) bool

// Release lock
func (l *Lock) Unlock(ctx context.Context) error

// Get lock holder
func (l *Lock) Holder() string
```

---

## RPC

```go
// Make RPC call
func (r *RPC) Call(ctx context.Context, target, method string, payload []byte) ([]byte, error)

// Call with JSON serialization
func (r *RPC) CallJSON(ctx context.Context, target, method string, req, resp interface{}) error

// Broadcast to all nodes
func (r *RPC) Broadcast(ctx context.Context, method string, payload []byte) ([][]byte, error)
```

---

## Events

```go
// Publish event
func (e *Events) Publish(ctx context.Context, subject string, data []byte) error

// Subscribe to events
func (e *Events) Subscribe(subject string, handler func([]byte)) error

// Unsubscribe
func (e *Events) Unsubscribe(subject string) error
```

---

## HashRing

```go
// Get node responsible for key
func (r *HashRing) NodeFor(key string) (string, error)

// Check if this node owns the key
func (r *HashRing) OwnsKey(key string) bool

// Get partition for key
func (r *HashRing) PartitionFor(key string) int

// Get partitions owned by this node
func (r *HashRing) OwnedPartitions() []int

// Get all members in the ring
func (r *HashRing) Members() []string
```

---

## Election

```go
// Check if this node is leader
func (e *Election) IsLeader() bool

// Get current leader
func (e *Election) Leader() string

// Wait for leadership (with timeout)
func (e *Election) WaitForLeadership(ctx context.Context) error

// Voluntarily step down
func (e *Election) StepDown(ctx context.Context) error

// Get channel for leadership changes
func (e *Election) LeaderChan() <-chan bool
```

---

## Backend Controllers

### Systemd

```go
func Systemd(serviceName string, opts ...backend.SystemdOption) *backend.SystemdBackend

// Options
backend.WithHealthEndpoint(url string)
backend.WithHealthInterval(d time.Duration)
backend.WithHealthTimeout(d time.Duration)
backend.WithStartTimeout(d time.Duration)
backend.WithStopTimeout(d time.Duration)
backend.WithNotify(bool)
backend.WithDependencies(deps ...string)
backend.WithJournalLines(n int)
backend.WithSystemdRestart(policy RestartPolicy)
```

### Docker

```go
func Docker(config DockerConfig) *backend.DockerBackend

type DockerConfig struct {
    Image       string
    Name        string
    Network     string
    Env         []string
    Volumes     []string
    Ports       []string
    StopTimeout time.Duration
    Labels      map[string]string
    ExtraArgs   []string
    HealthCheck *DockerHealthCheck
    Resources   *DockerResources
}
```

### Process

```go
func Process(config ProcessConfig) *backend.ProcessBackend

type ProcessConfig struct {
    Command         string
    Args            []string
    Dir             string
    Env             []string
    GracefulTimeout time.Duration
    UseProcessGroup bool
    LogFile         string
    OutputHandler   OutputHandler
}
```

---

## Types

### Member

```go
type Member struct {
    NodeID    string
    Platform  string
    Address   string
    Labels    map[string]string
    JoinedAt  time.Time
    LastSeen  time.Time
    Apps      []string
    IsHealthy bool
}
```

### ServiceConfig

```go
type ServiceConfig struct {
    Port        int
    Protocol    string  // tcp, udp, http, https, grpc, ws, wss
    Path        string
    HealthCheck *HealthCheckConfig
    Metadata    map[string]string
    Tags        []string
    Internal    bool
    Weight      int
}
```

### ServiceInstance

```go
type ServiceInstance struct {
    AppName     string
    ServiceName string
    NodeID      string
    Address     string
    Port        int
    Protocol    string
    Path        string
    Metadata    map[string]string
    Tags        []string
    Health      ServiceHealth
    Weight      int
}
```

### Role

```go
type Role string

const (
    RoleLeader  Role = "leader"
    RoleStandby Role = "standby"
    RoleActive  Role = "active"  // For pool/spread mode
)
```

### Mode

```go
type Mode int

const (
    ModeSingleton Mode = iota
    ModeSpread
    ModeReplicas
)
```

---

## Errors

```go
var (
    ErrNotLeader     = errors.New("not leader")
    ErrNoLeader      = errors.New("no leader")
    ErrLockHeld      = errors.New("lock already held")
    ErrLockNotHeld   = errors.New("lock not held")
    ErrTimeout       = errors.New("operation timed out")
    ErrAppNotFound   = errors.New("app not found")
    ErrNodeNotFound  = errors.New("node not found")
    ErrHookTimeout   = errors.New("hook timed out")
    ErrShuttingDown  = errors.New("platform is shutting down")
)
```

---

## CLI Commands

### Global Flags

```
--config string      Config file (default $HOME/.go-cluster.yaml)
--nats string        NATS server URL (default "nats://localhost:4222")
--node string        Node ID (default: hostname)
--platform string    Platform name
--verbose            Enable verbose output
```

### Commands

```bash
# Run daemon
go-cluster run [flags]

# Cluster status
go-cluster status
go-cluster health
go-cluster stepdown [--app APP]

# Node management
go-cluster node list
go-cluster node status <node-id>
go-cluster node drain <node-id> [--timeout DURATION]
go-cluster node cordon <node-id>
go-cluster node uncordon <node-id>
go-cluster node label <node-id> key=value [-key]  # Set or remove labels

# App management
go-cluster app list
go-cluster app status <app>
go-cluster app move <app> --from <node> --to <node>
go-cluster app rebalance <app>

# Snapshots
go-cluster snapshot create --app <app>
go-cluster snapshot list --app <app>
go-cluster snapshot restore <snapshot-id> --app <app>
go-cluster snapshot delete <snapshot-id> --app <app>

# Services
go-cluster services list
go-cluster services show <app> <service>
go-cluster services export --format prometheus|consul

# Leaf nodes
go-cluster leaf list
go-cluster leaf status <platform>
go-cluster leaf ping <platform>
go-cluster leaf promote <platform>

# Version
go-cluster version
```
