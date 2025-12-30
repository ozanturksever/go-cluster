# go-cluster

[![Go Reference](https://pkg.go.dev/badge/github.com/ozanturksever/go-cluster.svg)](https://pkg.go.dev/github.com/ozanturksever/go-cluster)
[![Go Report Card](https://goreportcard.com/badge/github.com/ozanturksever/go-cluster)](https://goreportcard.com/report/github.com/ozanturksever/go-cluster)
[![CI](https://github.com/ozanturksever/go-cluster/actions/workflows/ci.yml/badge.svg)](https://github.com/ozanturksever/go-cluster/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A lightweight, NATS-based cluster coordination library for Go that provides leader election, epoch-based fencing, and state management for building highly-available distributed systems.

## Features

- **Dynamic Cluster Membership** - Nodes can join/leave without reconfiguration
- **Lease-based Leader Election** - Using NATS JetStream KV Store
- **Epoch-based Fencing** - Prevents split-brain scenarios
- **Graceful Stepdown** - With cooldown period to prevent thrashing
- **Optional VIP Management** - Virtual IP failover on Linux
- **Health Checking** - NATS-based health check endpoints
- **Simple API** - Interface-based hooks for state change callbacks

## Installation

```bash
go get github.com/ozanturksever/go-cluster
```

## Requirements

- Go 1.24+
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
    // Start primary workload: accept writes, start replication, etc.
    return nil
}

func (a *MyApp) OnLoseLeadership(ctx context.Context) error {
    a.logger.Info("I lost leadership, switching to standby")
    // Stop primary workload: stop writes, become passive, etc.
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

## Configuration

### Basic Configuration

```go
cluster.Config{
    ClusterID:       "my-cluster",     // Required: Identifies the cluster
    NodeID:          "node-1",         // Required: Unique identifier for this node
    NATSURLs:        []string{"nats://localhost:4222"}, // Required: NATS server URLs
    NATSCredentials: "/path/to/creds", // Optional: Path to NATS credentials file
    LeaseTTL:        10 * time.Second, // Optional: Lease validity duration (default: 10s)
    RenewInterval:   3 * time.Second,  // Optional: Lease renewal interval (default: 3s)
    Logger:          slog.Default(),   // Optional: Structured logger
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
    "credentials": ""
  },
  "vip": {
    "address": "192.168.1.100",
    "netmask": 24,
    "interface": "eth0"
  },
  "election": {
    "leaseTtlMs": 10000,
    "renewIntervalMs": 3000
  }
}
```

## Core Concepts

### Leader Election

The package uses NATS JetStream KV Store for coordination:

- No fixed cluster size required
- Lease-based leadership with periodic renewal
- Automatic failover when leader fails

### Epoch-based Fencing

Each leadership change increments an epoch counter, preventing stale leaders from causing split-brain:

1. Leader acquires lease with epoch N
2. Leader fails or steps down
3. New leader acquires lease with epoch N+1
4. Old leader detects epoch mismatch and steps down

### Hooks Interface

Implement the `Hooks` interface to react to cluster state changes:

```go
type Hooks interface {
    OnBecomeLeader(ctx context.Context) error
    OnLoseLeadership(ctx context.Context) error
    OnLeaderChange(ctx context.Context, nodeID string) error
}
```

## API Reference

### Node

```go
// Create a new node
node, err := cluster.NewNode(cfg, hooks)

// Lifecycle
node.Start(ctx)    // Connect to NATS, start election loop
node.Stop(ctx)     // Stop election loop, disconnect

// State queries
node.Role()        // Current role (RolePrimary or RolePassive)
node.IsLeader()    // True if this node is the leader
node.Leader()      // Current leader's node ID
node.Epoch()       // Current epoch

// Control
node.StepDown(ctx) // Voluntarily give up leadership
```

### Manager (High-Level API)

For applications that need VIP management and health checking:

```go
mgr, err := cluster.NewManager(cfg, hooks)

mgr.Join(ctx)       // Quick connect to detect cluster state
mgr.Leave(ctx)      // Gracefully leave the cluster
mgr.Status(ctx)     // Get current node status
mgr.Promote(ctx)    // Attempt to become leader
mgr.Demote(ctx)     // Voluntarily step down
mgr.RunDaemon(ctx)  // Run the full daemon with VIP and health checking
```

## Optional Components

### Health Checker

NATS-based health checking using request/reply pattern:

```go
import "github.com/ozanturksever/go-cluster/health"

checker, err := health.NewChecker(health.Config{
    ClusterID: "my-cluster",
    NodeID:    "node-1",
    NATSURLs:  []string{"nats://localhost:4222"},
})

checker.Start(ctx)
defer checker.Stop()

// Query another node's health
resp, err := checker.QueryNode(ctx, "node-2", 5*time.Second)
```

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

| Parameter     | Default    | Notes                                      |
|---------------|------------|--------------------------------------------|
| LeaseTTL      | 10 seconds | How long before an abandoned lease expires |
| RenewInterval | 3 seconds  | Should be < LeaseTTL/3 for safety margin   |

**Failover Time:** Approximately `LeaseTTL` (10 seconds by default) after the leader fails.

## Testing

```bash
# Run unit tests
go test -v ./...

# Run with race detector
go test -race ./...

# Run benchmarks
go test -bench=. ./...
```

## Examples

See the [examples](./examples) directory for complete working examples:

- [Basic Usage](./examples/basic) - Simple leader election with hooks
- [CLI Tool](./examples/cli) - Command-line interface for cluster management

## Documentation

- [Full Specification](./GOCLUSTER_SPEC.md) - Detailed technical specification
- [API Reference](https://pkg.go.dev/github.com/ozanturksever/go-cluster) - GoDoc documentation

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](./CONTRIBUTING.md) for guidelines.

## License

This project is licensed under the Apache License 2.0 - see the [LICENSE](./LICENSE) file for details.
