# Dynamic App Placement

Support for dynamically moving apps between nodes based on label changes, manual triggers, or automated policies.

---

## Overview

Apps can be moved between nodes at runtime without requiring restarts of the entire cluster. This enables:

- **Label-based placement changes** - When node labels change, apps with `OnLabel()` placement constraints are re-evaluated
- **Manual migration** - Operators can trigger app movement via CLI or API
- **Automated rebalancing** - Platform can redistribute apps based on load or resource utilization

---

## Label Change Detection

When a node's labels are updated, the platform automatically re-evaluates placement constraints:

```go
// Node label update triggers re-evaluation
platform.UpdateNodeLabels("node-2", map[string]string{
    "role": "compute",  // was "storage"
    "zone": "us-east-1a",
})

// Apps with matching constraints will be moved
scheduler := cluster.App("scheduler",
    cluster.Singleton(),
    cluster.OnLabel("role", "controller"),  // Re-evaluated on label change
)
```

**Behavior:**
- Apps with `OnLabel()` placement are re-evaluated when any node's labels change
- If current placement no longer satisfies constraints, app is gracefully migrated
- If no nodes satisfy constraints, app enters `Pending` state with appropriate alerts

---

## Manual Migration

### CLI Commands

```bash
# Move a specific app instance to another node
go-cluster app move <app-name> --from <node-id> --to <node-id>

# Move app to any eligible node (platform chooses)
go-cluster app move <app-name> --from <node-id>

# Rebalance all instances of an app
go-cluster app rebalance <app-name>

# Drain all apps from a node (for maintenance)
go-cluster node drain <node-id>

# Evacuate apps matching a label selector
go-cluster app evacuate --label "zone=us-east-1a"
```

### API

```go
// Move specific app instance
platform.MoveApp(ctx, MoveRequest{
    App:      "convex",
    FromNode: "node-1",
    ToNode:   "node-2",
})

// Move to any eligible node
platform.MoveApp(ctx, MoveRequest{
    App:      "convex",
    FromNode: "node-1",
    // ToNode omitted - platform chooses best target
})

// Rebalance app instances across cluster
platform.RebalanceApp(ctx, "cache")

// Drain node (move all apps away)
platform.DrainNode(ctx, "node-1", DrainOptions{
    Timeout:       5 * time.Minute,
    Force:         false,
    DeleteLocal:   false,  // Keep local data for potential return
})
```

---

## Migration Process

### Singleton Apps (Active-Passive)

```
1. [Pre-migration]
   ├── Verify target node is healthy
   ├── Verify target node satisfies placement constraints
   └── Create migration lock (prevent concurrent migrations)

2. [Prepare target]
   ├── Start standby instance on target node
   ├── Wait for WAL replication to catch up
   └── Verify standby is ready

3. [Switchover]
   ├── Signal current active to step down
   ├── Wait for graceful shutdown (drain connections, flush writes)
   ├── Transfer leadership to target node
   └── VIP moves with leadership

4. [Cleanup]
   ├── Stop old instance on source node (optional: keep as standby)
   ├── Release migration lock
   └── Emit audit event
```

### Spread/Pool Apps

```
1. [Pre-migration]
   ├── Verify target node is healthy
   └── Verify placement constraints

2. [Start new instance]
   ├── Start instance on target node
   ├── Wait for health checks to pass
   └── Add to load balancer pool

3. [Stop old instance]
   ├── Remove from load balancer (drain connections)
   ├── Wait for graceful shutdown
   └── Stop instance on source node

4. [Cleanup]
   └── Emit audit event
```

### Ring Apps

```
1. [Pre-migration]
   ├── Verify target node is healthy
   └── Calculate partition ownership changes

2. [Prepare target]
   ├── Start instance on target node
   ├── Begin partition data transfer
   └── Wait for data sync

3. [Ownership transfer]
   ├── Transfer partition ownership (atomic)
   ├── OnRelease() called on source
   └── OnOwn() called on target

4. [Cleanup]
   ├── Stop source instance (if no partitions remain)
   └── Emit audit event
```

---

## Hooks

```go
// Called before migration starts (can reject)
app.OnMigrationStart(func(ctx context.Context, from, to string) error {
    if !readyToMigrate() {
        return errors.New("not ready for migration")
    }
    return nil
})

// Called after migration completes
app.OnMigrationComplete(func(ctx context.Context, from, to string) error {
    log.Printf("Migrated from %s to %s", from, to)
    return nil
})

// Called when placement becomes invalid (no eligible nodes)
app.OnPlacementInvalid(func(ctx context.Context, reason string) error {
    alertOps("App placement constraint cannot be satisfied: " + reason)
    return nil
})
```

---

## Label Update API

```go
// Update labels on current node
platform.SetLabels(ctx, map[string]string{
    "role":   "compute",
    "zone":   "us-east-1a",
    "custom": "value",
})

// Remove a label
platform.RemoveLabel(ctx, "custom")

// Watch for label changes (platform-wide)
platform.WatchLabels(ctx, func(nodeID string, labels map[string]string) {
    log.Printf("Node %s labels changed: %v", nodeID, labels)
})
```

### CLI

```bash
# Set labels on a node
go-cluster node label <node-id> role=compute zone=us-east-1a

# Remove a label
go-cluster node label <node-id> role-

# List node labels
go-cluster node show <node-id>
```

---

## Automated Rebalancing

```go
platform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithAutoRebalance(AutoRebalanceConfig{
        Enabled:         true,
        Interval:        5 * time.Minute,
        Threshold:       0.2,  // Trigger if imbalance > 20%
        MaxConcurrent:   2,    // Max concurrent migrations
        CooldownPeriod:  10 * time.Minute,
    }),
)
```

**Rebalancing triggers:**
- Node joins cluster
- Node leaves cluster (redistribute its apps)
- Significant load imbalance detected
- Manual trigger via API/CLI

---

## Placement Constraints

### Soft vs Hard Constraints

```go
// Hard constraint - app CANNOT run if violated (default)
cluster.OnLabel("role", "compute")

// Soft constraint - prefer but allow violation
cluster.PreferLabel("zone", "us-east-1a")

// Weighted preference
cluster.PreferNode("node-1", 100)  // Higher weight = more preferred
cluster.PreferNode("node-2", 50)
```

### Dynamic Constraint Updates

```go
// Update placement constraints at runtime
app.UpdatePlacement(ctx, 
    cluster.OnLabel("role", "storage"),  // Changed from "compute"
    cluster.AwayFromApp("other-app"),
)

// Platform re-evaluates and migrates if needed
```

---

## Events & Audit

```json
{
  "ts": "2024-01-15T10:30:00Z",
  "category": "placement",
  "action": "migration.started",
  "app": "convex",
  "data": {
    "from_node": "node-1",
    "to_node": "node-2",
    "reason": "label_change",
    "trigger": "automatic"
  }
}
```

**Audit events:**
- `placement.migration.started`
- `placement.migration.completed`
- `placement.migration.failed`
- `placement.constraint.invalid`
- `placement.rebalance.triggered`
- `node.labels.updated`

---

## Metrics

```
# Migration metrics
gc_migration_total{app,reason,status}          # Total migrations
gc_migration_duration_seconds{app}             # Migration duration histogram
gc_migration_in_progress{app}                  # Currently migrating

# Placement metrics
gc_placement_pending{app}                      # Apps waiting for placement
gc_placement_constraint_violations{app}        # Soft constraint violations

# Rebalance metrics
gc_rebalance_total{reason}                     # Total rebalance operations
gc_node_app_count{node}                        # Apps per node
gc_node_imbalance_ratio                        # Cluster imbalance metric
```

---

## Safety & Guardrails

```go
cluster.WithMigrationPolicy(MigrationPolicy{
    // Minimum time between migrations of same app
    CooldownPeriod: 5 * time.Minute,
    
    // Maximum migrations per time window
    RateLimit: Rate{Count: 10, Window: time.Hour},
    
    // Require explicit approval for migrations
    RequireApproval: false,
    
    // Maintenance windows
    MaintenanceWindows: []TimeWindow{
        {Start: "02:00", End: "06:00", Timezone: "UTC"},
    },
    
    // Prevent migration during high load
    LoadThreshold: 0.8,  // Don't migrate if target node > 80% load
})
```

---

## Example: Maintenance Workflow

```bash
# 1. Mark node for maintenance (prevents new app placement)
go-cluster node cordon node-1

# 2. Drain apps from node
go-cluster node drain node-1 --timeout 5m

# 3. Perform maintenance
# ...

# 4. Restore node to service
go-cluster node uncordon node-1

# 5. Optionally trigger rebalance
go-cluster app rebalance --all
```

---

## Example: Rolling Label Update

```bash
# Update zone labels one node at a time
for node in node-1 node-2 node-3; do
    go-cluster node label $node zone=us-east-1b
    go-cluster node wait-stable $node --timeout 2m
done
```
