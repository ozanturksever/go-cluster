# go-cluster Package Specification

## Overview

`go-cluster` is a generic NATS-based cluster coordination library for Go that provides leader election, epoch-based fencing, and state management. It is designed to be a reusable foundation for building highly-available distributed systems.

**Module:** `github.com/ozanturksever/go-cluster`

**Go Version:** 1.21+

**Dependencies:**
- `github.com/nats-io/nats.go` - NATS client with JetStream support

---

## Core Concepts

### Leader Election

The package uses NATS JetStream KV Store as the coordination backend for leader election. Unlike traditional Raft-based consensus, this approach:

- **Does not require a fixed cluster size** - Nodes can join/leave dynamically
- **Uses lease-based leadership** - Leader must periodically renew its lease
- **Implements epoch-based fencing** - Prevents split-brain scenarios

### Epoch-Based Fencing

Each time a new leader takes over, the epoch is incremented. This monotonically increasing counter prevents stale leaders from making changes:

1. Leader acquires lease with epoch N
2. Leader fails or steps down
3. New leader acquires lease with epoch N+1
4. If old leader (epoch N) tries to renew, it detects epoch mismatch and steps down

### Cooldown Period

When a node voluntarily steps down (via `StepDown()`), it enters a cooldown period equal to the lease TTL. During this time, the node will not attempt to re-acquire leadership, allowing other nodes to take over.

---

## Package Structure

```
github.com/ozanturksever/go-cluster/
├── errors.go       # Error definitions
├── hooks.go        # Hooks interface for state change callbacks
├── lease.go        # Lease struct and methods
├── state.go        # Role enum (Primary/Passive)
├── election.go     # Leader election logic
├── node.go         # Main Node type and public API
├── health/
│   └── checker.go  # Optional NATS-based health checking
├── vip/
│   └── manager.go  # Optional Virtual IP management (Linux)
└── examples/
    └── basic/      # Usage example
```

---

## API Reference

### Config

```go
type Config struct {
    ClusterID       string        // Required: Identifies the cluster
    NodeID          string        // Required: Unique identifier for this node
    NATSURLs        []string      // Required: NATS server URLs
    NATSCredentials string        // Optional: Path to NATS credentials file
    LeaseTTL        time.Duration // Optional: Lease validity duration (default: 10s)
    RenewInterval   time.Duration // Optional: Lease renewal interval (default: 3s)
    Logger          *slog.Logger  // Optional: Structured logger (default: slog.Default())
}
```

**Validation Rules:**
- `ClusterID` must not be empty
- `NodeID` must not be empty
- `NATSURLs` must contain at least one URL

**Defaults:**
- `LeaseTTL`: 10 seconds
- `RenewInterval`: 3 seconds (should be < LeaseTTL/3)

---

### Hooks Interface

```go
type Hooks interface {
    // Called when this node becomes the cluster leader
    OnBecomeLeader(ctx context.Context) error

    // Called when this node loses leadership
    OnLoseLeadership(ctx context.Context) error

    // Called when the cluster leader changes (nodeID is empty if no leader)
    OnLeaderChange(ctx context.Context, nodeID string) error
}
```

**Important Notes:**
- Hooks are called **synchronously** - spawn goroutines if async behavior is needed
- Hooks are called **outside the internal mutex** to prevent deadlocks
- Errors returned from hooks are logged but do not prevent state transitions
- `NoOpHooks` provides a default implementation that does nothing

---

### Node

The main entry point for cluster participation.

```go
// Create a new node
func NewNode(cfg Config, hooks Hooks) (*Node, error)

// Lifecycle
func (n *Node) Start(ctx context.Context) error  // Connect to NATS, start election loop
func (n *Node) Stop(ctx context.Context) error   // Stop election loop, disconnect

// State Queries
func (n *Node) Role() Role           // Current role (RolePrimary or RolePassive)
func (n *Node) IsLeader() bool       // True if this node is the leader
func (n *Node) Leader() string       // Current leader's node ID (empty if unknown)
func (n *Node) Epoch() int64         // Current epoch (0 if unknown)

// Control
func (n *Node) StepDown(ctx context.Context) error  // Voluntarily give up leadership
```

---

### Role

```go
type Role int

const (
    RolePassive Role = iota  // Passive/standby node
    RolePrimary              // Active/leader node
)

func (r Role) String() string    // Returns "PASSIVE", "PRIMARY", or "UNKNOWN"
func (r Role) IsPrimary() bool   // True if role is RolePrimary
func (r Role) IsPassive() bool   // True if role is RolePassive
```

---

### Lease

```go
type Lease struct {
    NodeID     string    `json:"node_id"`     // Node holding the lease
    Epoch      int64     `json:"epoch"`       // Monotonic epoch counter
    ExpiresAt  time.Time `json:"expires_at"`  // When the lease expires
    AcquiredAt time.Time `json:"acquired_at"` // When the lease was acquired
    Revision   uint64    `json:"-"`           // NATS KV revision (not serialized)
}

func (l *Lease) IsExpired() bool              // True if lease has expired
func (l *Lease) IsValid() bool                // True if lease is valid (has NodeID and not expired)
func (l *Lease) TimeUntilExpiry() time.Duration  // Duration until expiry (0 if expired)
```

---

### Errors

```go
var (
    ErrLeaseExpired   = errors.New("lease expired")
    ErrEpochMismatch  = errors.New("epoch mismatch - another node took over")
    ErrNotLeader      = errors.New("not the leader")
    ErrAlreadyLeader  = errors.New("already leader")
    ErrCASFailed      = errors.New("compare-and-swap failed")
    ErrNotStarted     = errors.New("node not started")
    ErrAlreadyStarted = errors.New("node already started")
    ErrInCooldown     = errors.New("node in cooldown period")
)
```

---

## Optional Components

### Health Checker (`health` package)

Provides NATS-based health checking using request/reply pattern.

```go
type Config struct {
    ClusterID       string
    NodeID          string
    NATSURLs        []string
    NATSCredentials string
    Logger          *slog.Logger
}

type Response struct {
    NodeID    string         `json:"nodeId"`
    Role      string         `json:"role"`
    Leader    string         `json:"leader"`
    UptimeMs  int64          `json:"uptimeMs"`
    Timestamp int64          `json:"timestamp"`
    Custom    map[string]any `json:"custom,omitempty"`
}

func NewChecker(cfg Config) (*Checker, error)
func (c *Checker) Start(ctx context.Context) error
func (c *Checker) Stop()
func (c *Checker) SetRole(role string)
func (c *Checker) SetLeader(leader string)
func (c *Checker) SetCustom(key string, value any)
func (c *Checker) QueryNode(ctx context.Context, nodeID string, timeout time.Duration) (Response, error)
```

**NATS Subject Pattern:** `cluster.<clusterID>.health.<nodeID>`

---

### VIP Manager (`vip` package)

Manages Virtual IP addresses on Linux systems using the `ip` command.

```go
type Config struct {
    Address   string        // IP address to manage
    Netmask   int           // Network mask in CIDR notation (e.g., 24)
    Interface string        // Network interface (e.g., "eth0")
    Logger    *slog.Logger
}

func (c Config) CIDR() string  // Returns "<Address>/<Netmask>"

type Executor interface {
    Execute(ctx context.Context, cmd string, args ...string) (string, error)
}

func NewManager(cfg Config, executor Executor) (*Manager, error)  // executor can be nil for default
func (m *Manager) Acquire(ctx context.Context) error
func (m *Manager) Release(ctx context.Context) error
func (m *Manager) IsAcquired(ctx context.Context) (bool, error)
```

**Requirements:**
- Linux operating system
- `ip` command available
- `arping` command (optional, for gratuitous ARP)
- Appropriate permissions (typically root or CAP_NET_ADMIN)

---

## Usage Example

```go
package main

import (
    "context"
    "log/slog"
    "os"
    "os/signal"
    "syscall"

    cluster "github.com/ozanturksever/go-cluster"
)

type MyApp struct {
    logger *slog.Logger
}

func (a *MyApp) OnBecomeLeader(ctx context.Context) error {
    a.logger.Info("I am now the LEADER!")
    // Start primary workload: accept writes, start replication, acquire VIP, etc.
    return nil
}

func (a *MyApp) OnLoseLeadership(ctx context.Context) error {
    a.logger.Info("I lost leadership, switching to standby")
    // Stop primary workload: stop writes, start passive replication, release VIP, etc.
    return nil
}

func (a *MyApp) OnLeaderChange(ctx context.Context, nodeID string) error {
    a.logger.Info("Leader changed", "leader", nodeID)
    return nil
}

func main() {
    logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
    app := &MyApp{logger: logger}

    node, err := cluster.NewNode(cluster.Config{
        ClusterID: "my-cluster",
        NodeID:    os.Getenv("NODE_ID"),
        NATSURLs:  []string{"nats://localhost:4222"},
        Logger:    logger,
    }, app)
    if err != nil {
        logger.Error("failed to create node", "error", err)
        os.Exit(1)
    }

    ctx := context.Background()
    if err := node.Start(ctx); err != nil {
        logger.Error("failed to start node", "error", err)
        os.Exit(1)
    }

    // Wait for shutdown signal
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    node.Stop(ctx)
}
```

---

## NATS KV Storage

### Bucket Naming

- **Coordination bucket:** `cluster-<clusterID>`
- **Health subjects:** `cluster.<clusterID>.health.<nodeID>`

### Keys

- **Lease key:** `lease`
- **Cooldown key:** `cooldown-<nodeID>`

### Lease JSON Format

```json
{
    "node_id": "node-1",
    "epoch": 5,
    "expires_at": "2024-01-15T10:30:00Z",
    "acquired_at": "2024-01-15T10:29:50Z"
}
```

---

## Thread Safety

- All public methods on `Node` are thread-safe
- Internal state is protected by `sync.RWMutex`
- Hooks are called **outside** the mutex to prevent deadlocks
- Hook implementations should be thread-safe if they access shared state

---

## Timing Recommendations

| Parameter | Recommended Value | Notes |
|-----------|-------------------|-------|
| LeaseTTL | 10 seconds | How long before an abandoned lease expires |
| RenewInterval | 3 seconds | Should be < LeaseTTL/3 for safety margin |
| KV TTL | 2 × LeaseTTL | Set automatically by the package |

**Failover Time:** In the worst case, failover takes approximately `LeaseTTL` (10 seconds by default) after the leader fails.

---

## Comparison with Alternatives

| Feature | go-cluster (NATS KV) | Raft (e.g., hashicorp/raft) | etcd lease |
|---------|---------------------|----------------------------|------------|
| Fixed cluster size | No | Yes | No |
| Local disk state | No | Yes | No |
| Split-brain prevention | Epoch fencing | Raft consensus | Lease TTL |
| Graceful stepdown | Yes (with cooldown) | Yes | Manual |
| Dependencies | NATS JetStream | None | etcd |
| Complexity | Low | High | Medium |

---

## Testing

Run unit tests:

```bash
cd go-cluster
go test -v ./...
```

**Test Coverage:**
- `config_test.go` - Config validation and defaults
- `lease_test.go` - Lease expiration and validity
- `state_test.go` - Role string conversion and checks
- `hooks_test.go` - NoOpHooks implementation

---

## Version History

### v0.1.0 (Initial Release)

- Core leader election with NATS KV
- Epoch-based fencing
- Graceful stepdown with cooldown
- Interface-based hooks
- Optional health checker
- Optional VIP manager
