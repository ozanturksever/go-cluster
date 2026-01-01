# Platform API

## Creating the Platform

```go
platform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.Labels(labels),
    cluster.NATSCreds("/etc/nats/creds"),
    cluster.HealthAddr(":8080"),
    cluster.MetricsAddr(":9090"),
)

platform.Register(convexApp)
platform.Register(cacheApp)
platform.Run(ctx)
```

---

## Platform Interface

```go
type Platform interface {
    Register(app *App)
    Run(ctx context.Context) error
    Lock(name string) Lock          // Platform-wide locks
    Events() Events                 // Platform-wide events
    Audit() Audit
    Metrics() Metrics
    Health() Health
}
```

---

## App Data Isolation

```go
// Inside "convex" app - can only access own data
func handleQuery(ctx context.Context, req Request) Response {
    value, _ := app.KV.Get(ctx, "users/123")     // App's own KV
    lock := app.Lock("migration")                 // App's own lock
    
    // CANNOT access other apps' data
    // cache.KV.Get() - NOT POSSIBLE
    
    // Must use explicit API for cross-app calls
    cache := cluster.Call("cache")
    cache.Invalidate(ctx, InvalidateRequest{Key: req.Key})
}
```

---

## Cross-App Communication

```go
convex := platform.API("convex")
resp, err := convex.Call(ctx, "query", QueryRequest{Table: "users"})

cache := platform.API("cache")
data, err := cache.Call(ctx, "get", GetRequest{Key: "user:123"},
    cluster.ForKey("user:123"),
)

platform.Events().Publish(ctx, "user.created", userData)
platform.Events().Subscribe("user.*", func(e Event) {
    log.Printf("Event from %s: %s", e.SourceApp, e.Type)
})
```

---

## Placement Strategies

### Manual Placement

```go
cluster.OnNodes("node-1", "node-2")
cluster.OnLabel("zone", "us-east-1a")
cluster.WithApp("cache")
cluster.AwayFromApp("convex")
```

### Automatic Placement

```go
cluster.Resources(cluster.ResourceSpec{
    CPU:    2,
    Memory: "4Gi",
    Disk:   "10Gi",
})
```

### Scheduling Strategies

```go
cluster.Strategy(cluster.SpreadEvenly)  // Default
cluster.Strategy(cluster.BinPack)
cluster.Strategy(cluster.ZoneAware)
cluster.Strategy(func(nodes []Node, app App) []Node {
    return selectBestNodes(nodes, app)
})
```

### Node Labels

```go
platform := cluster.NewPlatform("prod", "node-1", natsURL,
    cluster.Labels(map[string]string{
        "zone":      "us-east-1a",
        "tier":      "compute",
        "cpu":       "16",
        "memory":    "64Gi",
    }),
)
```

---

## Dynamic Placement & Migration

Apps can be moved at runtime via label changes or manual triggers. See [13-dynamic-placement.md](13-dynamic-placement.md) for full details.

```go
// Move app to another node
platform.MoveApp(ctx, MoveRequest{
    App:      "convex",
    FromNode: "node-1",
    ToNode:   "node-2",
})

// Drain node for maintenance
platform.DrainNode(ctx, "node-1", DrainOptions{
    Timeout: 5 * time.Minute,
})

// Update node labels (triggers placement re-evaluation)
platform.UpdateNodeLabels("node-2", map[string]string{
    "role": "compute",
    "zone": "us-east-1a",
})

// Rebalance apps across cluster
platform.RebalanceApp(ctx, "cache")
```

---

## Auto-Rebalancing Configuration

Enable automatic app redistribution when cluster state changes.

### Basic Configuration

```go
platform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithAutoRebalance(AutoRebalanceConfig{
        Enabled:  true,
        Interval: 5 * time.Minute,
    }),
)
```

### Full Configuration Options

```go
cluster.WithAutoRebalance(AutoRebalanceConfig{
    // Enable/disable auto-rebalancing
    Enabled: true,
    
    // How often to evaluate cluster balance
    Interval: 5 * time.Minute,
    
    // Imbalance threshold to trigger rebalancing (0.0-1.0)
    // 0.2 = trigger if any node has 20% more/fewer apps than average
    Threshold: 0.2,
    
    // Maximum concurrent migrations during rebalancing
    MaxConcurrent: 2,
    
    // Minimum time between rebalance operations
    CooldownPeriod: 10 * time.Minute,
    
    // Apps to exclude from auto-rebalancing
    ExcludeApps: []string{"critical-db"},
    
    // Only rebalance apps with these labels
    IncludeLabels: map[string]string{"rebalance": "true"},
    
    // Rebalancing strategy
    Strategy: cluster.RebalanceSpreadEvenly,  // or BinPack, ZoneAware
})
```

### Rebalancing Strategies

```go
// Spread apps evenly across all nodes (default)
cluster.RebalanceSpreadEvenly

// Pack apps onto fewer nodes (for cost optimization)
cluster.RebalanceBinPack

// Spread across availability zones first, then nodes
cluster.RebalanceZoneAware

// Custom strategy
cluster.RebalanceStrategy(func(ctx context.Context, state ClusterState) []Migration {
    return calculateOptimalMigrations(state)
})
```

### Rebalancing Triggers

```go
cluster.WithRebalanceTriggers(RebalanceTriggers{
    // Trigger when node joins cluster
    OnNodeJoin: true,
    
    // Trigger when node leaves cluster
    OnNodeLeave: true,
    
    // Trigger on resource utilization imbalance
    OnResourceImbalance: true,
    
    // Trigger on app count imbalance
    OnAppCountImbalance: true,
    
    // Trigger on schedule (cron expression)
    Schedule: "0 2 * * *",  // Daily at 2 AM
})
```

### Resource-Aware Rebalancing

```go
cluster.WithAutoRebalance(AutoRebalanceConfig{
    Enabled: true,
    
    // Consider resource usage when rebalancing
    ResourceAware: true,
    
    // Resource thresholds for triggering rebalance
    ResourceThresholds: ResourceThresholds{
        CPUImbalance:    0.3,  // 30% CPU usage difference
        MemoryImbalance: 0.25, // 25% memory usage difference
        DiskImbalance:   0.4,  // 40% disk usage difference
    },
    
    // Don't migrate to nodes above these limits
    MaxNodeLoad: ResourceLimits{
        CPU:    0.8,  // 80% CPU
        Memory: 0.85, // 85% memory
        Disk:   0.9,  // 90% disk
    },
})
```

### Migration Safety

```go
cluster.WithMigrationPolicy(MigrationPolicy{
    // Minimum time between migrations of same app
    CooldownPeriod: 5 * time.Minute,
    
    // Maximum migrations per time window
    RateLimit: Rate{Count: 10, Window: time.Hour},
    
    // Require explicit approval for migrations
    RequireApproval: false,
    
    // Allowed maintenance windows for migrations
    MaintenanceWindows: []TimeWindow{
        {Start: "02:00", End: "06:00", Timezone: "UTC"},
    },
    
    // Prevent migration during high load
    LoadThreshold: 0.8,
    
    // Maximum migration duration before timeout
    MigrationTimeout: 10 * time.Minute,
    
    // Rollback on failure
    RollbackOnFailure: true,
})
```

### Monitoring Rebalancing

```go
// Subscribe to rebalancing events
platform.OnRebalance(func(event RebalanceEvent) {
    log.Printf("Rebalance %s: %d migrations planned", 
        event.Status, len(event.Migrations))
})

// Get current rebalancing status
status := platform.RebalanceStatus(ctx)
fmt.Printf("In progress: %v, Migrations: %d/%d\n",
    status.InProgress, status.Completed, status.Total)

// Cancel ongoing rebalance
platform.CancelRebalance(ctx)
```

### CLI Commands

```bash
# Trigger manual rebalance
go-cluster rebalance --all
go-cluster rebalance --app cache

# Preview rebalance without executing
go-cluster rebalance --dry-run

# Check rebalance status
go-cluster rebalance status

# Cancel ongoing rebalance
go-cluster rebalance cancel

# Configure auto-rebalance
go-cluster config set auto-rebalance.enabled true
go-cluster config set auto-rebalance.threshold 0.2
go-cluster config set auto-rebalance.interval 5m
```

---

## Example: Complex Placement

```
5 Nodes:
├── node-1: zone=us-east-1a, tier=compute
├── node-2: zone=us-east-1a, tier=compute
├── node-3: zone=us-east-1b, tier=compute
├── node-4: zone=us-east-1b, tier=storage
└── node-5: zone=us-east-1c, tier=controller

Apps:
├── convex:    Singleton, OnLabel(tier=storage)     → node-4
├── cache:     Spread, OnLabel(tier=compute)        → node-1,2,3
├── api:       Spread                               → all nodes
├── worker:    Replicas(3), AwayFromApp(convex)     → node-1,2,3
└── scheduler: Singleton, OnLabel(tier=controller)  → node-5
```
