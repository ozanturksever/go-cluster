# Getting Started with go-cluster

This guide will walk you through installing go-cluster and building your first highly available application.

## Prerequisites

### NATS Server

go-cluster requires NATS Server with JetStream enabled. Install and start NATS:

```bash
# macOS
brew install nats-server

# Linux (download binary)
curl -L https://github.com/nats-io/nats-server/releases/download/v2.10.0/nats-server-v2.10.0-linux-amd64.tar.gz | tar xz
sudo mv nats-server-v2.10.0-linux-amd64/nats-server /usr/local/bin/

# Start with JetStream
nats-server -js
```

### Go

go-cluster requires Go 1.21 or later.

```bash
go version  # Should be 1.21+
```

## Installation

### As a Library

```bash
go get github.com/ozanturksever/go-cluster
```

### CLI Tool

```bash
go install github.com/ozanturksever/go-cluster/cmd/go-cluster@latest
```

### From Source

```bash
git clone https://github.com/ozanturksever/go-cluster.git
cd go-cluster
go build ./cmd/go-cluster
```

## Your First Cluster

### Step 1: Create a Simple Application

Create a file `main.go`:

```go
package main

import (
    "context"
    "flag"
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/ozanturksever/go-cluster"
)

func main() {
    nodeID := flag.String("node", "node-1", "Node ID")
    natsURL := flag.String("nats", "nats://localhost:4222", "NATS URL")
    flag.Parse()

    // Create a platform (represents a cluster)
    platform, err := cluster.NewPlatform("my-cluster", *nodeID, *natsURL,
        cluster.HealthAddr(":8080"),
        cluster.MetricsAddr(":9090"),
    )
    if err != nil {
        log.Fatalf("Failed to create platform: %v", err)
    }

    // Create a singleton app (only one active instance)
    app := cluster.NewApp("my-service", cluster.Singleton())

    // Called when this node becomes the leader
    app.OnActive(func(ctx context.Context) error {
        log.Println("🟢 I am now the LEADER!")
        // Start your service here
        return nil
    })

    // Called when this node becomes a standby
    app.OnPassive(func(ctx context.Context) error {
        log.Println("🔵 I am now STANDBY")
        // Stop your service here
        return nil
    })

    // Called when leadership changes anywhere in the cluster
    app.OnLeaderChange(func(ctx context.Context, leader string) error {
        log.Printf("📢 Leader is now: %s", leader)
        return nil
    })

    // Register the app with the platform
    platform.Register(app)

    // Set up graceful shutdown
    ctx, cancel := context.WithCancel(context.Background())
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

    go func() {
        <-sigCh
        log.Println("Shutting down...")
        cancel()
    }()

    // Run the platform
    if err := platform.Run(ctx); err != nil {
        log.Fatalf("Platform error: %v", err)
    }
}
```

### Step 2: Run Multiple Nodes

Open three terminals and run:

```bash
# Terminal 1
go run main.go -node node-1

# Terminal 2
go run main.go -node node-2

# Terminal 3
go run main.go -node node-3
```

You'll see one node become the leader and the others become standbys.

### Step 3: Test Failover

1. Kill the leader (Ctrl+C)
2. Watch one of the standbys automatically become the new leader
3. Failover typically happens in under 1 second

## Using the CLI

The `go-cluster` CLI provides tools for managing your cluster:

```bash
# Check cluster status
go-cluster status --platform my-cluster --nats nats://localhost:4222

# List all nodes
go-cluster node list --platform my-cluster

# Check health
go-cluster health --platform my-cluster
```

## Key Concepts

### Platform

A Platform represents a cluster of nodes running together. All nodes in a platform share:
- NATS connection
- Membership tracking
- Audit logging
- Metrics collection

```go
platform, err := cluster.NewPlatform("prod", "node-1", "nats://localhost:4222")
```

### App

An App is an isolated component within a platform. Each app has:
- Its own leader election (for singleton mode)
- Isolated KV storage
- Isolated locks
- Its own lifecycle hooks

```go
app := cluster.NewApp("database", cluster.Singleton())
```

### Mode

Apps run in one of three modes:

| Mode | Description | Use Case |
|------|-------------|----------|
| `Singleton()` | One active, rest standby | Databases, coordinators |
| `Spread()` | Run on all nodes | API servers, load-balanced services |
| `Replicas(N)` | Run on N nodes | Workers, specific scaling needs |

### Lifecycle Hooks

| Hook | When Called | Mode |
|------|-------------|------|
| `OnActive(fn)` | Becomes leader | Singleton |
| `OnPassive(fn)` | Becomes standby | Singleton |
| `OnStart(fn)` | Instance starts | Spread/Replicas |
| `OnStop(fn)` | Instance stops | All |
| `OnLeaderChange(fn)` | Leader changes | Singleton |
| `OnOwn(fn)` | Partitions assigned | Ring |
| `OnRelease(fn)` | Partitions released | Ring |

## Next Steps

- [Configuration Guide](configuration.md) - Learn about all configuration options
- [Node Presence & Duplicate Detection](node-presence.md) - Understand node identity and rejoin behavior
- [Cluster Patterns](patterns.md) - Understand Active-Passive, Pool, and Ring patterns
- [API Reference](api-reference.md) - Complete API documentation
- [Examples](../examples/) - Working example applications

## Troubleshooting

### "Failed to connect to NATS"

Make sure NATS server is running with JetStream:

```bash
nats-server -js
```

### "No leader elected"

Check that:
1. All nodes can connect to NATS
2. All nodes use the same platform name
3. JetStream is enabled on the NATS server

### "Leader election stuck"

This can happen if a previous leader didn't clean up properly. Wait for the TTL to expire (default 10 seconds) or restart the NATS server.

### Health check port already in use

Use a different port or use `:0` for automatic port assignment:

```go
platform, err := cluster.NewPlatform("prod", "node-1", natsURL,
    cluster.HealthAddr(":0"),  // Random available port
)
```
