# go-cluster

> The clustering toolkit for Go. Build HA applications, not infrastructure.

[![Go Reference](https://pkg.go.dev/badge/github.com/ozanturksever/go-cluster.svg)](https://pkg.go.dev/github.com/ozanturksever/go-cluster)
[![Go Report Card](https://goreportcard.com/badge/github.com/ozanturksever/go-cluster)](https://goreportcard.com/report/github.com/ozanturksever/go-cluster)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](LICENSE)

go-cluster is a NATS-native clustering toolkit that makes building highly available Go applications simple. It provides leader election, distributed coordination, SQLite replication, and service discovery—all built on top of NATS JetStream.

## Features

- **🗳️ Leader Election** - Automatic leader election via NATS KV with sub-second failover
- **🔄 SQLite Replication** - Litestream-style WAL replication for near real-time database sync
- **🔐 Distributed Locks** - Application and platform-wide distributed locking
- **📦 Distributed KV** - Per-app isolated key-value storage backed by NATS
- **🔍 Service Discovery** - Register and discover services with health checking
- **🌐 Multi-Zone Support** - NATS leaf node integration for multi-region deployments
- **📊 Observability** - Built-in Prometheus metrics, health checks, and audit logging
- **🎯 Placement Constraints** - Label-based scheduling with affinity/anti-affinity rules
- **🔌 Backend Integration** - Manage systemd services, Docker containers, or processes

## Quick Start

### Installation

```bash
# As a library
go get github.com/ozanturksever/go-cluster

# CLI tool
go install github.com/ozanturksever/go-cluster/cmd/go-cluster@latest
```

### Prerequisites

NATS Server with JetStream enabled:

```bash
# Install NATS
brew install nats-server  # macOS
# or download from https://nats.io/download/

# Start with JetStream
nats-server -js
```

### Basic Example

```go
package main

import (
    "context"
    "log"

    "github.com/ozanturksever/go-cluster"
)

func main() {
    // Create a platform (cluster of nodes)
    platform, err := cluster.NewPlatform("myapp", "node-1", "nats://localhost:4222")
    if err != nil {
        log.Fatal(err)
    }

    // Create a singleton app (only one leader at a time)
    app := cluster.NewApp("service", cluster.Singleton())

    // Handle leadership changes
    app.OnActive(func(ctx context.Context) error {
        log.Println("I am now the leader!")
        return nil
    })

    app.OnPassive(func(ctx context.Context) error {
        log.Println("I am now standby")
        return nil
    })

    // Register and run
    platform.Register(app)
    platform.Run(context.Background())
}
```

### With SQLite Replication

```go
app := cluster.NewApp("database",
    cluster.Singleton(),
    cluster.WithSQLite("/data/app.db",
        cluster.SQLiteReplica("/data/replica.db"),
        cluster.SQLiteSnapshotInterval(time.Hour),
    ),
    cluster.VIP("10.0.1.100/24", "eth0"),
    cluster.WithBackend(cluster.Systemd("my-backend")),
)
```

## Cluster Patterns

### Active-Passive (Singleton)

One leader handles all traffic, others ready for instant failover.

```go
app := cluster.NewApp("database", cluster.Singleton())
app.OnActive(startDatabase)
app.OnPassive(stopDatabase)
```

### Load-Balanced Pool

All instances are active and handle traffic.

```go
app := cluster.NewApp("api", cluster.Spread())  // Run on all nodes
// or
app := cluster.NewApp("worker", cluster.Replicas(3))  // Run on 3 nodes
```

### Consistent Hash Ring

Partition data across nodes with automatic rebalancing.

```go
app := cluster.NewApp("cache",
    cluster.Spread(),
    cluster.Ring(256),  // 256 virtual partitions
)

app.OnOwn(func(ctx context.Context, partitions []int) error {
    return loadPartitions(partitions)
})

app.OnRelease(func(ctx context.Context, partitions []int) error {
    return savePartitions(partitions)
})
```

## Placement Constraints

```go
app := cluster.NewApp("service",
    cluster.Singleton(),
    cluster.OnLabel("zone", "us-east-1a"),     // Run on nodes with label
    cluster.AvoidLabel("tier", "storage"),     // Avoid nodes with label  
    cluster.WithApp("cache"),                   // Co-locate with another app
    cluster.AwayFromApp("database"),            // Anti-affinity
)
```

## CLI Usage

```bash
# Run a cluster node
go-cluster run --platform myapp --node node-1

# Check cluster status
go-cluster status --platform myapp

# List nodes
go-cluster node list --platform myapp

# Drain a node for maintenance
go-cluster node drain node-1 --platform myapp

# List services
go-cluster services list --platform myapp

# Create a snapshot
go-cluster snapshot create --platform myapp --app database
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         go-cluster                               │
├─────────────────────────────────────────────────────────────────┤
│  Platform     │  Apps + API Bus + Shared Observability          │
├─────────────────────────────────────────────────────────────────┤
│  Patterns     │  ActivePassive │ Pool │ Ring                    │
├─────────────────────────────────────────────────────────────────┤
│  App Data     │  KV │ DB │ Locks │ Events (isolated per app)    │
├─────────────────────────────────────────────────────────────────┤
│  Cross-App    │  API Bus │ Platform Events │ Service Discovery  │
├─────────────────────────────────────────────────────────────────┤
│  Ops          │  Backend │ VIP │ Audit │ Metrics │ Trace        │
├─────────────────────────────────────────────────────────────────┤
│  NATS         │  KV │ JetStream │ ObjectStore │ Micro           │
└─────────────────────────────────────────────────────────────────┘
```

## Documentation

- [Getting Started](docs/getting-started.md) - Installation and first steps
- [Configuration](docs/configuration.md) - All configuration options
- [Cluster Patterns](docs/patterns.md) - Active-Passive, Pool, and Ring patterns
- [API Reference](docs/api-reference.md) - Complete API documentation
- [Examples](examples/) - Working examples

## Examples

| Example | Description |
|---------|-------------|
| [basic](examples/basic/) | Simple active-passive with leader election |
| [convex-backend](examples/convex-backend/) | SQLite replication + VIP failover |
| [distributed-cache](examples/distributed-cache/) | Ring pattern for data sharding |
| [docker-compose](examples/docker-compose/) | Multi-node setup with monitoring |

## Requirements

- Go 1.21+
- NATS Server 2.10+ with JetStream enabled
- CGO enabled (for SQLite support)

## Contributing

Contributions are welcome! Please see [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

Apache License 2.0 - see [LICENSE](LICENSE) for details.
