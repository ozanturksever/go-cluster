# App API

## Defining an App

```go
app := cluster.App("convex")

app.KV                    // Key-value store
app.SQLite("/path/to.db") // SQLite3 with Litestream WAL replication  
app.Lock("name")          // Distributed lock

app.Handle("query", queryHandler)
app.Handle("mutate", mutateHandler)

app.OnStart(func(ctx context.Context) error {
    return startBackend(ctx)
})

convex := app.Call("other-app")
resp, _ := convex.Query(ctx, req)
```

---

## App Modes

```go
// Singleton - exactly ONE active instance (with standbys)
app := cluster.App("convex", cluster.Singleton())
app.OnActive(startDB)
app.OnStandby(prepareDB)

// Spread - runs on ALL nodes
app := cluster.App("api", cluster.Spread())
app.OnStart(startServer)

// N-of-M - runs on N nodes out of M total
app := cluster.App("worker", cluster.Replicas(3))
app.OnStart(startWorker)
```

---

## Placement

```go
cluster.Spread()                              // All nodes
cluster.OnNodes("node-1", "node-2", "node-3") // Specific nodes
cluster.OnLabel("role", "compute")            // By label
cluster.AvoidLabel("role", "storage")         // Avoid label
cluster.WithApp("cache")                      // Co-location
cluster.AwayFromApp("other-heavy-app")        // Anti-affinity
cluster.Resources(cluster.ResourceSpec{CPU: 2, Memory: "4Gi"})
```

---

## Pattern (HA Behavior)

```go
cluster.Singleton()  // ActivePassive - one leader, rest standby
cluster.Spread()     // Pool - all instances active, load balanced
cluster.Replicas(N)  // Pool
cluster.Ring(256)    // Consistent hash sharding
```

---

## Complete Examples

```go
// 1. Database (Singleton + ActivePassive + SQLite3/Litestream WAL)
convex := cluster.App("convex",
    cluster.Singleton(),
    cluster.SQLite("/data/convex.db"),  // SQLite3 + Litestream WAL replication
    cluster.VIP("10.0.1.100/24", "eth0"),
    cluster.Backend(cluster.Systemd("convex-backend")),
)

// 2. Cache (Spread + Ring)
cache := cluster.App("cache",
    cluster.Spread(),
    cluster.Ring(256),
)

// 3. API Gateway (Spread + Pool)
api := cluster.App("api", cluster.Spread())

// 4. Worker (3 replicas + Pool + Resource-aware)
worker := cluster.App("worker",
    cluster.Replicas(3),
    cluster.Resources(cluster.ResourceSpec{CPU: 4, Memory: "8Gi"}),
    cluster.AwayFromApp("convex"),
)

// 5. Scheduler (Singleton + Affinity)
scheduler := cluster.App("scheduler",
    cluster.Singleton(),
    cluster.OnLabel("role", "controller"),
)
```

---

## Calling Other Apps

```go
func handleRequest(ctx context.Context, req Request) Response {
    convex := cluster.Call("convex")
    user, err := convex.Query(ctx, QueryRequest{Table: "users", ID: req.UserID})
    
    cache := cluster.Call("cache")
    data, err := cache.Get(ctx, GetRequest{Key: "user:" + req.UserID})
    
    cluster.Notify("analytics", "page.viewed", PageEvent{...})
    
    return Response{User: user, CachedData: data}
}
```

---

## Lifecycle Hooks

```go
// Singleton mode
app.OnActive(fn)    // Called when this instance becomes active
app.OnStandby(fn)   // Called when this instance becomes standby

// Spread/Replicas mode  
app.OnStart(fn)     // Called when instance starts
app.OnStop(fn)      // Called when instance stops

// Ring mode (in addition to above)
app.OnOwn(fn)       // Called when partitions assigned
app.OnRelease(fn)   // Called when partitions released

// Migration hooks (see 13-dynamic-placement.md)
app.OnMigrationStart(fn)     // Called before migration (can reject)
app.OnMigrationComplete(fn)  // Called after migration completes
app.OnPlacementInvalid(fn)   // Called when no nodes satisfy constraints
```

---

## Routing Rules

| Target Mode | Routing |
|-------------|---------|
| Singleton | → Active instance |
| Spread/Pool | → Any healthy instance (load balanced) |
| Ring | → Instance owning the key (if key provided) |
| Ring (no key) | → Any instance |
