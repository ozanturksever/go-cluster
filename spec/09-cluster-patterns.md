# Cluster Patterns

## Active-Passive (Default)

One leader handles all traffic, others ready for failover.

```go
app := cluster.New("myapp", nodeID, natsURL,
    cluster.WithDB("/data/app.db"),
    cluster.WithVIP("10.0.0.100/24", "eth0"),
    cluster.WithBackend(cluster.Systemd("myapp")),
)
app.Run(ctx)
```

**Characteristics:**
- Exactly one active instance at any time
- Automatic leader election via NATS KV
- WAL replication keeps standbys in sync
- Sub-second failover on leader failure
- VIP moves with leadership

---

## Load-Balanced Pool

All nodes handle traffic, shared state via NATS.

```go
pool := cluster.Pool("api", nodeID, natsURL)

pool.OnStart(func(ctx context.Context) error {
    return apiServer.Start(ctx)
})

pool.Run(ctx)
```

**Characteristics:**
- All instances active and serving requests
- No leader election needed
- Shared state via NATS KV
- Use locks for coordination when needed

---

## Consistent Hash Ring

Partition data across nodes.

```go
ring := cluster.Ring("cache", nodeID, natsURL,
    cluster.WithPartitions(256),
)

ring.OnOwn(func(ctx context.Context, partitions []int) error {
    return cache.LoadPartitions(ctx, partitions)
})

ring.OnRelease(func(ctx context.Context, partitions []int) error {
    return cache.ReleasePartitions(ctx, partitions)
})

node, err := ring.NodeFor(key)
```

**Characteristics:**
- Data partitioned across all nodes
- Consistent hashing ensures even distribution
- Partitions automatically rebalance on membership changes

---

## Pattern Selection Guide

| Use Case | Pattern | Why |
|----------|---------|-----|
| Database, scheduler | `ActivePassive` | Single writer, strong consistency |
| API server, web server | `Pool` | Stateless, all instances equivalent |
| Cache, partitioned data | `Ring` | Data sharding, distributed storage |
| Worker queue | `Pool` | Any worker can process any job |
| Coordinator | `ActivePassive` | Single point of coordination |

---

## Combining Patterns

```go
db := cluster.App("database", cluster.Singleton(), cluster.DB("/data/db.sqlite"))
cache := cluster.App("cache", cluster.Spread(), cluster.Ring(256))
api := cluster.App("api", cluster.Spread())
workers := cluster.App("workers", cluster.Replicas(3))

platform.Register(db, cache, api, workers)
```
