# Cluster Patterns

go-cluster supports three primary patterns for building distributed applications. Choose the pattern that best fits your use case.

## Overview

| Pattern | Mode | Use Case | Instances |
|---------|------|----------|----------|
| Active-Passive | `Singleton()` | Databases, coordinators | 1 active, N standby |
| Pool | `Spread()` / `Replicas(N)` | APIs, workers | All active |
| Ring | `Ring(N)` | Caches, partitioned data | All active, data sharded |

## Active-Passive Pattern

The Active-Passive pattern ensures exactly one instance handles all work while others remain on standby, ready for instant failover.

### When to Use

- **Databases**: When you need a single writer for consistency
- **Coordinators**: Scheduling, job distribution
- **Stateful services**: When state can't be easily shared
- **Legacy applications**: That weren't designed for clustering

### How It Works

1. All nodes compete for leadership via NATS KV
2. One node wins and becomes the **leader** (active)
3. Other nodes become **standbys** (passive)
4. If the leader fails, a standby is promoted (sub-second failover)
5. WAL replication keeps standbys in sync (for SQLite)

### Example

```go
package main

import (
    "context"
    "log"

    "github.com/ozanturksever/go-cluster"
)

func main() {
    platform, _ := cluster.NewPlatform("prod", "node-1", "nats://localhost:4222")

    // Singleton mode = Active-Passive pattern
    app := cluster.NewApp("database",
        cluster.Singleton(),
        cluster.WithSQLite("/data/app.db"),
        cluster.VIP("10.0.1.100/24", "eth0"),
        cluster.WithBackend(cluster.Systemd("postgres")),
    )

    // Called when this node becomes the leader
    app.OnActive(func(ctx context.Context) error {
        log.Println("Starting as PRIMARY")
        // The backend service is automatically started
        // VIP is automatically acquired
        // WAL replication from standbys is started
        return nil
    })

    // Called when this node becomes a standby
    app.OnPassive(func(ctx context.Context) error {
        log.Println("Running as STANDBY")
        // The backend service is automatically stopped
        // VIP is released
        // WAL restoration continues to keep replica in sync
        return nil
    })

    platform.Register(app)
    platform.Run(context.Background())
}
```

### Failover Sequence

When the leader fails:

```
1. Leader failure detected (via NATS KV TTL expiry)
2. Standbys compete for leadership
3. One standby wins election
4. New leader performs final WAL catch-up
5. Replica database promoted to primary
6. VIP acquired (if configured)
7. Backend service started
8. OnActive() hook called
9. New leader starts accepting requests
```

Typical failover time: **< 5 seconds**

### Graceful Stepdown

Leaders can voluntarily step down for maintenance:

```go
// From CLI
// go-cluster stepdown --platform prod --app database

// Programmatically
app.Election().StepDown(ctx)
```

## Pool Pattern

The Pool pattern runs multiple instances, all actively serving requests. Use this for stateless services that can scale horizontally.

### When to Use

- **API servers**: Stateless HTTP/gRPC services
- **Workers**: Job processors, queue consumers
- **Proxies**: Load balancers, API gateways
- **Microservices**: General stateless services

### How It Works

1. All instances start and register with the platform
2. All instances are **active** and serve requests
3. Load balancing happens at the caller level
4. No leader election needed
5. Instances share state via distributed KV/locks when needed

### Example: Spread Mode

Run on all nodes:

```go
package main

import (
    "context"
    "log"
    "net/http"

    "github.com/ozanturksever/go-cluster"
)

func main() {
    platform, _ := cluster.NewPlatform("prod", "node-1", "nats://localhost:4222")

    // Spread mode = run on all nodes
    app := cluster.NewApp("api", cluster.Spread())

    // Called when this instance starts
    app.OnStart(func(ctx context.Context) error {
        log.Println("Starting API server")
        go http.ListenAndServe(":8080", nil)
        return nil
    })

    // Called when this instance stops
    app.OnStop(func(ctx context.Context) error {
        log.Println("Stopping API server")
        return nil
    })

    // Expose the service for discovery
    app.ExposeService("http", cluster.ServiceConfig{
        Port:     8080,
        Protocol: "http",
    })

    platform.Register(app)
    platform.Run(context.Background())
}
```

### Example: Replicas Mode

Run on a specific number of nodes:

```go
// Run on exactly 3 nodes
app := cluster.NewApp("worker", cluster.Replicas(3))
```

### Cross-App Communication

Pool apps can call other apps:

```go
// In your API handler
func handleRequest(ctx context.Context) {
    // Call the database app (routes to leader)
    client := cluster.Call("database")
    resp, err := client.Call(ctx, "query", queryRequest)
    
    // Call another pool app (routes to any instance)
    client := cluster.Call("cache")
    resp, err := client.Call(ctx, "get", getRequest, cluster.ToAny())
}
```

### Using Shared State

```go
// Use distributed KV for shared state
app.KV().Put(ctx, "config/version", []byte("1.0.0"))
value, _ := app.KV().Get(ctx, "config/version")

// Use distributed locks for coordination
lock := app.Lock("processing")
if lock.TryLock(ctx) {
    defer lock.Unlock(ctx)
    // Critical section
}
```

## Ring Pattern

The Ring pattern distributes data across nodes using consistent hashing. Each node owns a subset of partitions, and data is automatically rebalanced when nodes join or leave.

### When to Use

- **Distributed caches**: Memcached-style caching
- **Sharded databases**: Partitioned data stores
- **Session stores**: User session management
- **Any partitioned workload**: Where data locality matters

### How It Works

1. The key space is divided into N virtual partitions
2. Partitions are distributed across nodes using consistent hashing
3. Each node owns a subset of partitions
4. Keys are hashed to determine their partition
5. When nodes join/leave, only affected partitions move

### Example

```go
package main

import (
    "context"
    "log"
    "sync"

    "github.com/ozanturksever/go-cluster"
)

// In-memory cache per partition
var cache = struct {
    sync.RWMutex
    data map[int]map[string][]byte  // partition -> key -> value
}{data: make(map[int]map[string][]byte)}

func main() {
    platform, _ := cluster.NewPlatform("prod", "node-1", "nats://localhost:4222")

    // Ring pattern with 256 partitions
    app := cluster.NewApp("cache",
        cluster.Spread(),
        cluster.Ring(256),
    )

    // Called when this node gains ownership of partitions
    app.OnOwn(func(ctx context.Context, partitions []int) error {
        log.Printf("Acquired partitions: %v", partitions)
        
        cache.Lock()
        defer cache.Unlock()
        
        for _, p := range partitions {
            if cache.data[p] == nil {
                cache.data[p] = make(map[string][]byte)
            }
            // In production: load partition data from persistent storage
        }
        return nil
    })

    // Called when this node releases ownership of partitions
    app.OnRelease(func(ctx context.Context, partitions []int) error {
        log.Printf("Releasing partitions: %v", partitions)
        
        cache.Lock()
        defer cache.Unlock()
        
        for _, p := range partitions {
            // In production: persist partition data before releasing
            delete(cache.data, p)
        }
        return nil
    })

    // Register handlers for cache operations
    app.Handle("get", handleGet)
    app.Handle("set", handleSet)

    platform.Register(app)
    platform.Run(context.Background())
}
```

### Routing Requests

Requests can be routed to the correct node automatically:

```go
// Route to the node that owns this key
client := cluster.Call("cache")
resp, err := client.Call(ctx, "get", request, cluster.ForKey("user:123"))
```

Or check ownership locally:

```go
ring := app.Ring()

// Check if this node owns the key
if ring.OwnsKey("user:123") {
    // Handle locally
} else {
    // Forward to correct node
    node, _ := ring.NodeFor("user:123")
    // ...
}
```

### Partition Assignment

Partitions are distributed evenly across nodes:

```
3 Nodes, 12 Partitions:

Node 1: [0, 1, 2, 3]     (4 partitions)
Node 2: [4, 5, 6, 7]     (4 partitions)
Node 3: [8, 9, 10, 11]   (4 partitions)

When Node 3 leaves:

Node 1: [0, 1, 2, 3, 8, 9]      (6 partitions)
Node 2: [4, 5, 6, 7, 10, 11]    (6 partitions)
```

### Rebalancing

Rebalancing happens automatically when:
- A node joins the cluster
- A node leaves (gracefully or crash)
- Manual rebalance is triggered

```bash
# Trigger manual rebalance
go-cluster app rebalance cache --platform prod
```

## Combining Patterns

You can run multiple apps with different patterns on the same platform:

```go
platform, _ := cluster.NewPlatform("prod", "node-1", natsURL)

// Database - Active-Passive with SQLite replication
db := cluster.NewApp("database",
    cluster.Singleton(),
    cluster.WithSQLite("/data/app.db"),
    cluster.VIP("10.0.1.100/24", "eth0"),
)

// Cache - Ring pattern for distributed caching
cache := cluster.NewApp("cache",
    cluster.Spread(),
    cluster.Ring(256),
)

// API - Pool pattern for stateless API
api := cluster.NewApp("api",
    cluster.Spread(),
)

// Workers - Fixed number of replicas
worker := cluster.NewApp("worker",
    cluster.Replicas(3),
    cluster.AwayFromApp("database"),  // Anti-affinity
)

platform.Register(db)
platform.Register(cache)
platform.Register(api)
platform.Register(worker)

platform.Run(context.Background())
```

## Pattern Selection Guide

| Requirement | Pattern | Mode |
|-------------|---------|------|
| Single writer | Active-Passive | `Singleton()` |
| Strong consistency | Active-Passive | `Singleton()` |
| Horizontal scaling | Pool | `Spread()` or `Replicas(N)` |
| Stateless service | Pool | `Spread()` |
| Data partitioning | Ring | `Ring(N)` |
| Cache cluster | Ring | `Ring(N)` |
| Job queue processing | Pool | `Replicas(N)` |
| Coordinator/scheduler | Active-Passive | `Singleton()` |

## Best Practices

### Active-Passive

1. **Always configure WAL replication** for databases to minimize data loss
2. **Use VIP** for seamless client failover
3. **Test failover** regularly in staging environments
4. **Monitor replication lag** to ensure standbys are up-to-date

### Pool

1. **Keep instances stateless** or use distributed state (KV, locks)
2. **Use health checks** for proper load balancer integration
3. **Implement graceful shutdown** to drain existing requests
4. **Use service discovery** for dynamic instance registration

### Ring

1. **Choose partition count carefully** - more partitions = smoother rebalancing
2. **Implement OnOwn/OnRelease** properly for data handoff
3. **Consider replication factor** for fault tolerance (app-specific)
4. **Monitor partition distribution** for balance
