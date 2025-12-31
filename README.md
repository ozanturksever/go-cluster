# go-cluster

[![Go Reference](https://pkg.go.dev/badge/github.com/ozanturksever/go-cluster.svg)](https://pkg.go.dev/github.com/ozanturksever/go-cluster)
[![Go Report Card](https://goreportcard.com/badge/github.com/ozanturksever/go-cluster)](https://goreportcard.com/report/github.com/ozanturksever/go-cluster)
[![CI](https://github.com/ozanturksever/go-cluster/actions/workflows/ci.yml/badge.svg)](https://github.com/ozanturksever/go-cluster/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A lightweight, NATS-based cluster coordination library for Go that provides leader election, epoch-based fencing, and state management for building highly-available distributed systems.

## Features

- **Fast Failover** - Sub-500ms failover using NATS KV Watch (reactive, not polling)
- **Dynamic Cluster Membership** - Nodes can join/leave without reconfiguration
- **Lease-based Leader Election** - Using NATS JetStream KV Store with automatic TTL
- **Epoch-based Fencing** - Prevents split-brain scenarios
- **Graceful Stepdown** - With cooldown period to prevent thrashing
- **NATS Micro Service** - Built-in service discovery via `$SRV.*` subjects
- **Resilient Connections** - Automatic reconnection with configurable retry
- **Optional VIP Management** - Virtual IP failover on Linux
- **Simple API** - Interface-based hooks for state change callbacks

## Installation

```bash
go get github.com/ozanturksever/go-cluster
```

## Requirements

- Go 1.21+
- NATS Server with JetStream enabled

## Quick Start

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
    return nil
}

func (a *MyApp) OnLoseLeadership(ctx context.Context) error {
    a.logger.Info("I lost leadership, switching to standby")
    return nil
}

func (a *MyApp) OnLeaderChange(ctx context.Context, nodeID string) error {
    a.logger.Info("Leader changed", "leader", nodeID)
    return nil
}

func (a *MyApp) OnNATSReconnect(ctx context.Context) error {
    a.logger.Info("NATS connection restored")
    return nil
}

func (a *MyApp) OnNATSDisconnect(ctx context.Context, err error) error {
    a.logger.Warn("NATS connection lost", "error", err)
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

    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    <-sigCh

    node.Stop(ctx)
}
```

## Architecture

### KV Watch-based Election

go-cluster v2 uses NATS KV Watch for reactive leader election, providing sub-500ms failover:

1. **Leader Key**: A single key in NATS KV store holds the current leader lease
2. **KV Watch**: All nodes watch this key for changes (instant notification)
3. **Automatic TTL**: NATS KV `MaxAge` handles lease expiration automatically
4. **Heartbeat**: Leader periodically updates the key to refresh TTL

This is significantly faster than polling-based approaches which typically have 3-10 second failover times.

### NATS Micro Service

Each node registers as a NATS micro service, enabling:

- **Service Discovery**: Use `nats micro ls` to discover all cluster nodes
- **Health Monitoring**: Built-in ping and status endpoints
- **Remote Control**: Trigger stepdown via NATS request

## Configuration

### Basic Configuration

```go
cluster.Config{
    ClusterID:         "my-cluster",     // Required: Identifies the cluster
    NodeID:            "node-1",         // Required: Unique identifier for this node
    NATSURLs:          []string{"nats://localhost:4222"}, // Required: NATS server URLs
    NATSCredentials:   "/path/to/creds", // Optional: Path to NATS credentials file
    LeaseTTL:          10 * time.Second, // Optional: Lease validity duration (default: 10s)
    HeartbeatInterval: 3 * time.Second,  // Optional: Heartbeat interval (default: 3s)
    ReconnectWait:     2 * time.Second,  // Optional: NATS reconnect wait (default: 2s)
    MaxReconnects:     -1,               // Optional: Max reconnect attempts (default: -1 unlimited)
    ServiceVersion:    "1.0.0",          // Optional: Micro service version
    Logger:            slog.Default(),   // Optional: Structured logger
}
```

### File-based Configuration

Load configuration from a JSON file:

```go
cfg, err := cluster.LoadConfigFromFile("config.json")
if err != nil {
    log.Fatal(err)
}

mgr, err := cluster.NewManager(*cfg, myHooks)
```

Example `config.json`:

```json
{
  "clusterId": "my-cluster",
  "nodeId": "node-1",
  "nats": {
    "servers": ["nats://localhost:4222"],
    "credentials": "",
    "reconnectWaitMs": 2000,
    "maxReconnects": -1
  },
  "vip": {
    "address": "192.168.1.100",
    "netmask": 24,
    "interface": "eth0"
  },
  "election": {
    "leaseTtlMs": 10000,
    "heartbeatIntervalMs": 3000
  },
  "service": {
    "version": "1.0.0"
  }
}
```

## Core Concepts

### Leader Election

The package uses NATS JetStream KV Store with Watch for coordination:

- **Reactive**: KV Watch provides instant notification of leader changes
- **No polling**: Eliminates the 3-10 second failover delay of polling approaches
- **Automatic TTL**: NATS KV `MaxAge` handles lease expiration
- **Epoch fencing**: Monotonic epoch counter prevents split-brain

### Failover Performance

| Scenario | Failover Time |
|----------|---------------|
| Graceful stepdown | < 100ms |
| Leader crash (TTL expiry) | ~LeaseTTL (default 10s) |
| With shorter TTL (3s) | ~3s |

For faster crash recovery, use a shorter `LeaseTTL` (with proportionally shorter `HeartbeatInterval`).

### Hooks Interface

Implement the `Hooks` interface to react to cluster state changes:

```go
type Hooks interface {
    // Leadership changes
    OnBecomeLeader(ctx context.Context) error
    OnLoseLeadership(ctx context.Context) error
    OnLeaderChange(ctx context.Context, nodeID string) error
    
    // Connection events
    OnNATSReconnect(ctx context.Context) error
    OnNATSDisconnect(ctx context.Context, err error) error
}
```

## NATS Micro Service

Each node exposes endpoints via NATS micro service:

| Endpoint | Subject | Description |
|----------|---------|-------------|
| status | `cluster_<cid>.status.<nid>` | Node status (role, leader, epoch) |
| ping | `cluster_<cid>.ping.<nid>` | Health check |
| stepdown | `cluster_<cid>.control.<nid>.stepdown` | Trigger stepdown |

### Service Discovery

Use the NATS CLI to discover cluster nodes:

```bash
# List all cluster services
nats micro ls

# Get service info
nats micro info cluster_my-cluster

# Get service stats
nats micro stats cluster_my-cluster

# Ping a specific node
nats request cluster_my-cluster.ping.node-1 ''

# Get node status
nats request cluster_my-cluster.status.node-1 ''

# Trigger stepdown
nats request cluster_my-cluster.control.node-1.stepdown ''
```

## NATS Authorization

The subject naming is designed for NATS authorization compatibility:

```conf
# Full cluster access (all nodes, all operations)
authorization {
  users = [
    { user: "admin", password: "secret",
      permissions: {
        publish: ["cluster_myapp.>", "$KV.KV_CLUSTER_myapp.>"]
        subscribe: ["cluster_myapp.>", "$KV.KV_CLUSTER_myapp.>", "_INBOX.>"]
      }
    }
  ]
}

# Read-only access (status/ping only)
permissions: {
  publish: ["cluster_myapp.status.>", "cluster_myapp.ping.>"]
  subscribe: ["cluster_myapp.status.>", "cluster_myapp.ping.>", "_INBOX.>"]
}

# Single node access
permissions: {
  publish: ["cluster_myapp.*.node-1"]
  subscribe: ["cluster_myapp.*.node-1", "_INBOX.>"]
}
```

## API Reference

### Node

```go
// Create a new node
node, err := cluster.NewNode(cfg, hooks)

// Lifecycle
node.Start(ctx)    // Connect to NATS, start election
node.Stop(ctx)     // Stop and disconnect

// State queries
node.Role()        // Current role (RolePrimary or RolePassive)
node.IsLeader()    // True if this node is the leader
node.Leader()      // Current leader's node ID
node.Epoch()       // Current epoch
node.Connected()   // True if NATS connection is active

// Control
node.StepDown(ctx) // Voluntarily give up leadership

// Service
node.Service()     // Access the micro service (for stats, etc.)
```

### Manager (High-Level API)

For applications that need VIP management:

```go
mgr, err := cluster.NewManager(cfg, hooks)

mgr.Join(ctx)       // Quick connect to detect cluster state
mgr.Leave(ctx)      // Gracefully leave the cluster
mgr.Status(ctx)     // Get current node status
mgr.Promote(ctx)    // Attempt to become leader
mgr.Demote(ctx)     // Voluntarily step down
mgr.RunDaemon(ctx)  // Run the full daemon with VIP management
```

## Optional Components

### VIP Manager

Virtual IP management on Linux:

```go
import "github.com/ozanturksever/go-cluster/vip"

mgr, err := vip.NewManager(vip.Config{
    Address:   "192.168.1.100",
    Netmask:   24,
    Interface: "eth0",
}, nil)

mgr.Acquire(ctx)  // Add VIP to interface
mgr.Release(ctx)  // Remove VIP from interface
```

## Timing Recommendations

| Parameter | Default | Notes |
|-----------|---------|-------|
| LeaseTTL | 10 seconds | How long before an abandoned lease expires |
| HeartbeatInterval | 3 seconds | Should be < LeaseTTL/3 for safety margin |
| ReconnectWait | 2 seconds | Wait between NATS reconnection attempts |

**Failover Time:**
- **Graceful stepdown**: < 100ms (instant via KV Watch)
- **Crash recovery**: ~LeaseTTL (waiting for TTL expiry)

## Migration from v1

### Breaking Changes in v2

1. **Configuration**: `RenewInterval` renamed to `HeartbeatInterval`
2. **Hooks**: New required methods `OnNATSReconnect` and `OnNATSDisconnect`
3. **Health package removed**: Use NATS micro service endpoints instead

### Migration Steps

1. Update configuration:
   ```go
   // Before (v1)
   cfg := cluster.Config{
       RenewInterval: 3 * time.Second,
   }
   
   // After (v2)
   cfg := cluster.Config{
       HeartbeatInterval: 3 * time.Second,
   }
   ```

2. Implement new hooks:
   ```go
   // Add these methods to your Hooks implementation
   func (h *MyHooks) OnNATSReconnect(ctx context.Context) error {
       // Handle reconnection (e.g., log, update metrics)
       return nil
   }
   
   func (h *MyHooks) OnNATSDisconnect(ctx context.Context, err error) error {
       // Handle disconnect (e.g., log, alert)
       return nil
   }
   ```

3. Replace health checker with micro service:
   ```go
   // Before (v1) - using health package
   checker.QueryNode(ctx, "node-2", timeout)
   
   // After (v2) - using NATS request
   nc.Request(cluster.PingSubject("my-cluster", "node-2"), nil, timeout)
   ```

## Testing

```bash
# Run unit tests
go test -v -short ./...

# Run all tests including E2E (requires Docker)
go test -v ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...
```

### Validation Scripts

```bash
# Validate election behavior
./scripts/validate-election.sh

# Validate service discovery (requires nats CLI)
./scripts/validate-discovery.sh

# Validate reconnection behavior
./scripts/validate-reconnection.sh

# Benchmark failover time
./scripts/benchmark-failover.sh
```

## Examples

See the [examples](./examples) directory for complete working examples:

- [Basic Usage](./examples/basic) - Simple leader election with hooks
- [CLI Tool](./examples/cli) - Command-line interface for cluster management

## Documentation

- [API Reference](https://pkg.go.dev/github.com/ozanturksever/go-cluster) - GoDoc documentation

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](./LICENSE) file for details.
