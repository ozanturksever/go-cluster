# NATS Leaf Nodes & Multi-Zone Deployments

Support for NATS leaf node connections enables platforms and services to be distributed across multiple zones, regions, or locations while maintaining connectivity through a hub-and-spoke topology.

---

## Overview

NATS Leaf Nodes allow go-cluster platforms to:

- **Span multiple zones/regions** with localized traffic and centralized coordination
- **Reduce WAN latency** by keeping local traffic local
- **Scale horizontally** to thousands of edge locations
- **Isolate failures** - zone failures don't affect other zones
- **Bridge networks** across VPCs, data centers, or cloud providers

---

## Architecture

### Hub-and-Spoke Topology

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              Central Hub (us-east)                           │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                       NATS Cluster (Hub)                             │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │    │
│  │  │   NATS-1    │──│   NATS-2    │──│   NATS-3    │                  │    │
│  │  │  :4222/:7422│  │  :4222/:7422│  │  :4222/:7422│                  │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘                  │    │
│  │        │                │                │                           │    │
│  │        │          Leaf Port 7422         │                           │    │
│  │        └────────────────┼────────────────┘                           │    │
│  └─────────────────────────┼────────────────────────────────────────────┘    │
│                            │                                                 │
│  ┌─────────────────────────┴─────────────────────────┐                      │
│  │              Platform: "prod" (Hub)               │                      │
│  │  Apps: convex (primary), scheduler, api           │                      │
│  └───────────────────────────────────────────────────┘                      │
└─────────────────────────────────────────────────────────────────────────────┘
                            ▲
          ┌─────────────────┼─────────────────┐
          │                 │                 │
          │           Leaf Connections        │
          │          (outbound to hub)        │
          ▼                 ▼                 ▼
┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
│  Zone: us-west  │ │  Zone: eu-west  │ │  Zone: ap-south │
│  ┌───────────┐  │ │  ┌───────────┐  │ │  ┌───────────┐  │
│  │NATS (Leaf)│  │ │  │NATS (Leaf)│  │ │  │NATS (Leaf)│  │
│  │  :4222    │──┼─┼──│  :4222    │──┼─┼──│  :4222    │  │
│  └───────────┘  │ │  └───────────┘  │ │  └───────────┘  │
│                 │ │                 │ │                 │
│  Platform:      │ │  Platform:      │ │  Platform:      │
│  "prod-usw"     │ │  "prod-eu"      │ │  "prod-ap"      │
│  (Leaf)         │ │  (Leaf)         │ │  (Leaf)         │
│                 │ │                 │ │                 │
│  Apps:          │ │  Apps:          │ │  Apps:          │
│  - cache        │ │  - cache        │ │  - cache        │
│  - api (local)  │ │  - api (local)  │ │  - api (local)  │
│  - convex       │ │  - convex       │ │  - convex       │
│    (standby)    │ │    (standby)    │ │    (standby)    │
└─────────────────┘ └─────────────────┘ └─────────────────┘
```

### How Leaf Connections Work

1. **Leaf nodes initiate connections** to the hub (outbound only)
2. **Messages flow bidirectionally** once connected
3. **Subscriptions are forwarded** - hub knows what leaves want
4. **JetStream replicates** across leaf boundaries when configured
5. **Automatic reconnection** with exponential backoff

---

## Configuration

### Hub Platform Configuration

```go
// Central hub platform
hubPlatform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithLeafHub(LeafHubConfig{
        // Enable leaf node listening
        Port: 7422,
        
        // TLS for secure leaf connections
        TLS: &TLSConfig{
            CertFile: "/etc/nats/hub-cert.pem",
            KeyFile:  "/etc/nats/hub-key.pem",
            CAFile:   "/etc/nats/ca.pem",
        },
        
        // Authorized leaf platforms
        AuthorizedLeaves: []LeafAuth{
            {Platform: "prod-usw", Token: "token-usw-xxx"},
            {Platform: "prod-eu", Token: "token-eu-xxx"},
            {Platform: "prod-ap", Token: "token-ap-xxx"},
        },
    }),
)
```

### Leaf Platform Configuration

```go
// Leaf platform in us-west zone
leafPlatform := cluster.NewPlatform("prod-usw", nodeID, localNatsURL,
    cluster.WithLeafConnection(LeafConnectionConfig{
        // Hub connection details
        HubURLs: []string{
            "nats-leaf://hub1.prod.example.com:7422",
            "nats-leaf://hub2.prod.example.com:7422",
            "nats-leaf://hub3.prod.example.com:7422",
        },
        
        // Authentication
        Token: "token-usw-xxx",
        // Or use credentials file
        // Credentials: "/etc/nats/leaf.creds",
        
        // TLS
        TLS: &TLSConfig{
            CertFile: "/etc/nats/leaf-cert.pem",
            KeyFile:  "/etc/nats/leaf-key.pem",
            CAFile:   "/etc/nats/ca.pem",
        },
        
        // Parent platform for cross-platform communication
        HubPlatform: "prod",
        
        // Reconnection settings
        ReconnectWait:    2 * time.Second,
        MaxReconnects:    -1,  // Unlimited
    }),
    
    // Zone metadata
    cluster.Labels(map[string]string{
        "zone":   "us-west-2",
        "region": "us-west",
        "tier":   "edge",
    }),
)
```

### NATS Server Configuration

**Hub NATS Configuration** (`hub.conf`):

```hcl
# Hub cluster configuration
port: 4222

cluster {
    name: "hub-cluster"
    port: 5222
    routes: [
        "nats://hub1:5222",
        "nats://hub2:5222",
        "nats://hub3:5222",
    ]
}

# Enable leaf node connections
leafnodes {
    port: 7422
    
    # TLS for leaf connections
    tls {
        cert_file: "/etc/nats/hub-cert.pem"
        key_file: "/etc/nats/hub-key.pem"
        ca_file: "/etc/nats/ca.pem"
        verify: true
    }
    
    # Authorization per leaf
    authorization {
        users: [
            { user: "prod-usw", password: "secret-usw" }
            { user: "prod-eu", password: "secret-eu" }
            { user: "prod-ap", password: "secret-ap" }
        ]
    }
}

# JetStream for replication
jetstream {
    store_dir: "/data/jetstream"
    max_memory_store: 1GB
    max_file_store: 100GB
}
```

**Leaf NATS Configuration** (`leaf.conf`):

```hcl
# Local listener for zone apps
port: 4222

# Connect to hub as leaf
leafnodes {
    remotes: [
        {
            urls: [
                "nats-leaf://prod-usw:secret-usw@hub1.prod.example.com:7422",
                "nats-leaf://prod-usw:secret-usw@hub2.prod.example.com:7422",
                "nats-leaf://prod-usw:secret-usw@hub3.prod.example.com:7422",
            ]
            tls {
                cert_file: "/etc/nats/leaf-cert.pem"
                key_file: "/etc/nats/leaf-key.pem"
                ca_file: "/etc/nats/ca.pem"
            }
        }
    ]
}

# Local JetStream (can mirror from hub)
jetstream {
    store_dir: "/data/jetstream"
    max_memory_store: 512MB
    max_file_store: 50GB
}
```

---

## Use Cases

### 1. Multi-Region Database with Local Caches

```go
// Hub: Primary database
hubPlatform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithLeafHub(LeafHubConfig{Port: 7422}),
)

convex := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db"),
    cluster.WithVIP("10.0.1.100/24", "eth0"),
)
hubPlatform.Register(convex)

// Leaf: Local cache + read replica
leafPlatform := cluster.NewPlatform("prod-usw", nodeID, localNatsURL,
    cluster.WithLeafConnection(LeafConnectionConfig{
        HubURLs:     []string{"nats-leaf://hub:7422"},
        HubPlatform: "prod",
    }),
)

// Local cache for low-latency reads
cache := cluster.App("cache",
    cluster.Spread(),
    cluster.Ring(256),
)

// Standby replica of hub's convex (read-only)
convexStandby := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex-replica.db"),
    cluster.StandbyOf("prod", "convex"),  // Replicate from hub
)

leafPlatform.Register(cache)
leafPlatform.Register(convexStandby)
```

### 2. Edge Computing with Central Coordination

```go
// Hub: Central scheduler and state
scheduler := cluster.App("scheduler",
    cluster.Singleton(),
    cluster.OnLabel("tier", "hub"),
)

// Each edge location runs local workers
worker := cluster.App("worker",
    cluster.Spread(),
    cluster.OnLabel("tier", "edge"),
)

// Workers in leaf platforms subscribe to hub scheduler
worker.OnStart(func(ctx context.Context) error {
    // Subscribe to job assignments from hub scheduler
    scheduler := cluster.Call("prod", "scheduler")  // Cross-platform call
    return scheduler.Subscribe(ctx, "jobs.assigned", handleJob)
})
```

### 3. Disaster Recovery with Geo-Redundancy

```go
// Primary hub in us-east
primaryHub := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithLeafHub(LeafHubConfig{Port: 7422}),
)

// DR site in eu-west (leaf with standby replicas)
drLeaf := cluster.NewPlatform("prod-dr", nodeID, localNatsURL,
    cluster.WithLeafConnection(LeafConnectionConfig{
        HubURLs:     []string{"nats-leaf://hub.us-east:7422"},
        HubPlatform: "prod",
    }),
    cluster.Labels(map[string]string{
        "role": "disaster-recovery",
        "zone": "eu-west-1",
    }),
)

// All singleton apps have standbys in DR site
convexDR := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex-dr.db"),
    cluster.StandbyOf("prod", "convex"),
    cluster.PromoteOnHubFailure(true),  // Auto-promote if hub unreachable
)
```

---

## Cross-Platform Communication

### Calling Services Across Platforms

```go
// From leaf platform, call service on hub
func handleLocalRequest(ctx context.Context, req Request) Response {
    // Local cache (same platform)
    cache := cluster.Call("cache")
    data, _ := cache.Get(ctx, req.Key)
    
    // Hub database (cross-platform via leaf connection)
    convex := cluster.Call("prod", "convex")  // platform.app format
    user, _ := convex.Query(ctx, QueryRequest{Table: "users", ID: req.UserID})
    
    return Response{User: user, CachedData: data}
}
```

### Event Propagation

```go
// Events can flow hub → leaves or leaves → hub

// Hub publishes event
hubPlatform.Events().Publish(ctx, "config.updated", newConfig)

// All leaf platforms receive it
leafPlatform.Events().Subscribe("config.>", func(e Event) {
    log.Printf("Config update from hub: %v", e.Data)
    applyConfig(e.Data)
})

// Leaf publishes metrics to hub
leafPlatform.Events().PublishToHub(ctx, "metrics.collected", localMetrics)
```

### Service Discovery Across Platforms

```go
// Discover services across all connected platforms
services, _ := platform.DiscoverServices(ctx, DiscoverOptions{
    App:      "convex",
    Service:  "http",
    Scope:    DiscoverGlobal,  // Include hub and all leaves
})

for _, svc := range services {
    fmt.Printf("Platform: %s, Zone: %s, Address: %s:%d\n",
        svc.Platform, svc.Zone, svc.Address, svc.Port)
}

// Output:
// Platform: prod,     Zone: us-east-1, Address: 10.0.1.10:3210
// Platform: prod-usw, Zone: us-west-2, Address: 10.1.1.10:3210
// Platform: prod-eu,  Zone: eu-west-1, Address: 10.2.1.10:3210
```

---

## WAL Replication Across Zones

### Hub as Primary, Leaves as Read Replicas

```
┌─────────────────────────────────────────────────────────────────┐
│  Hub Platform: prod (us-east)                                    │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ convex (PRIMARY)                                            ││
│  │ └─ SQLite3 → Litestream → NATS ObjectStore (prod_convex_wal)││
│  └─────────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────────┘
                              │
                    WAL frames via leaf connection
                              │
         ┌────────────────────┼────────────────────┐
         ▼                    ▼                    ▼
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│  prod-usw       │  │  prod-eu        │  │  prod-ap        │
│  convex         │  │  convex         │  │  convex         │
│  (STANDBY)      │  │  (STANDBY)      │  │  (STANDBY)      │
│  └─ Replica DB  │  │  └─ Replica DB  │  │  └─ Replica DB  │
│    (read-only)  │  │    (read-only)  │  │    (read-only)  │
└─────────────────┘  └─────────────────┘  └─────────────────┘
```

### Configuration

```go
// Hub: Enable WAL sharing to leaves
convex := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db",
        cluster.SQLiteShareWAL(true),  // Share WAL to leaf platforms
    ),
)

// Leaf: Subscribe to hub's WAL for read replica
convexReplica := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex-replica.db",
        cluster.SQLiteReplicateFrom("prod", "convex"),  // Pull WAL from hub
        cluster.SQLiteReadOnly(true),
    ),
)
```

---

## Failover & Promotion

### Automatic Promotion on Hub Failure

```go
// Leaf app configured for auto-promotion
convexReplica := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db"),
    cluster.StandbyOf("prod", "convex"),
    cluster.PromoteOnHubFailure(PromoteConfig{
        Enabled:          true,
        DetectionTimeout: 30 * time.Second,  // Hub unreachable for 30s
        RequireQuorum:    true,              // Majority of leaves must agree
    }),
)

// Hook for promotion event
convexReplica.OnPromoted(func(ctx context.Context) error {
    log.Println("Promoted to PRIMARY after hub failure")
    // Update DNS, notify, etc.
    return nil
})
```

### Manual Promotion

```bash
# Promote leaf platform to hub role
go-cluster leaf promote --platform prod-usw --reason "hub-maintenance"

# Demote back after hub recovery
go-cluster leaf demote --platform prod-usw
```

---

## CLI Commands

```bash
# List leaf connections
go-cluster leaf list
# PLATFORM    ZONE        STATUS      LATENCY   LAST_SEEN
# prod-usw    us-west-2   connected   45ms      2s ago
# prod-eu     eu-west-1   connected   120ms     1s ago
# prod-ap     ap-south-1  connected   180ms     3s ago

# Show leaf status
go-cluster leaf status prod-usw
# Platform:     prod-usw
# Zone:         us-west-2
# Hub:          prod
# Status:       connected
# Latency:      45ms
# Connected:    2024-01-15T10:30:00Z
# Apps:         cache, api, convex (standby)
# Services:     12 registered

# Disconnect/reconnect leaf
go-cluster leaf disconnect prod-usw
go-cluster leaf reconnect prod-usw

# Test leaf connection
go-cluster leaf ping prod-usw
# RTT: 45ms

# View cross-platform traffic
go-cluster leaf traffic
# FROM        TO          MSG/s    BYTES/s
# prod-usw    prod        1,234    45KB/s
# prod        prod-usw    567      12KB/s
# prod-eu     prod        890      34KB/s
```

---

## Metrics

```
# Leaf connection metrics
gc_leaf_connection_status{platform,hub}           # 1=connected, 0=disconnected
gc_leaf_connection_latency_ms{platform,hub}       # RTT to hub
gc_leaf_reconnects_total{platform,hub}            # Reconnection count
gc_leaf_messages_sent_total{platform,hub}         # Messages to hub
gc_leaf_messages_received_total{platform,hub}     # Messages from hub
gc_leaf_bytes_sent_total{platform,hub}            # Bytes to hub
gc_leaf_bytes_received_total{platform,hub}        # Bytes from hub

# Cross-platform replication
gc_leaf_wal_lag_ms{platform,app}                  # WAL replication lag
gc_leaf_wal_bytes_received{platform,app}          # WAL bytes received from hub
```

---

## Network Partition Handling & Split-Brain Prevention

Network partitions between hub and leaf platforms can cause split-brain scenarios where multiple nodes believe they are the primary. go-cluster implements several mechanisms to prevent and handle these situations.

### The Split-Brain Problem

```
Normal Operation:
┌─────────────┐         ┌─────────────┐
│  Hub (prod) │◄───────►│ Leaf (usw)  │
│  PRIMARY    │  leaf   │  STANDBY    │
│  convex ✓   │  conn   │  convex     │
└─────────────┘         └─────────────┘

Partition (DANGEROUS - potential split-brain):
┌─────────────┐    ✗    ┌─────────────┐
│  Hub (prod) │         │ Leaf (usw)  │
│  PRIMARY    │ network │  PRIMARY?   │  ← Both think they're primary!
│  convex ✓   │partition│  convex ✓?  │
└─────────────┘         └─────────────┘
```

### Prevention Mechanisms

#### 1. Quorum-Based Promotion

Leaf platforms require agreement from a majority of connected leaves before promoting to primary:

```go
cluster.PromoteOnHubFailure(PromoteConfig{
    Enabled:          true,
    DetectionTimeout: 30 * time.Second,
    
    // Require majority of leaves to agree hub is down
    RequireQuorum:    true,
    QuorumSize:       0,  // 0 = automatic (majority of known leaves)
    
    // Alternative: require specific number
    // QuorumSize:    2,  // At least 2 leaves must agree
})
```

**How it works:**
1. Leaf detects hub unreachable
2. Leaf broadcasts "hub-down" vote to other leaves
3. Waits for majority of leaves to agree
4. Only promotes if quorum reached
5. If no quorum, remains in read-only standby mode

#### 2. Fencing Tokens

Every primary operation includes a monotonically increasing fencing token:

```go
// Hub generates fencing token on becoming primary
type FencingToken struct {
    Epoch     uint64    // Increments on each promotion
    Platform  string    // Platform that generated token
    Timestamp time.Time // When token was issued
}

// All write operations include the token
func (app *App) Write(ctx context.Context, data []byte) error {
    token := app.CurrentFencingToken()
    // Storage layer rejects writes with stale tokens
    return app.storage.WriteWithToken(ctx, data, token)
}
```

**Configuration:**
```go
convex := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db"),
    cluster.WithFencing(FencingConfig{
        Enabled:     true,
        TokenStore:  "nats",  // Store tokens in NATS KV for distributed validation
        StaleReject: true,    // Reject operations with stale tokens
    }),
)
```

#### 3. Hub Heartbeat & Lease

Hub maintains a lease that leaves monitor:

```go
cluster.WithLeafConnection(LeafConnectionConfig{
    HubURLs:     []string{"nats-leaf://hub:7422"},
    HubPlatform: "prod",
    
    // Partition detection
    PartitionDetection: PartitionConfig{
        // How often hub sends heartbeats
        HeartbeatInterval: 3 * time.Second,
        
        // How long before declaring hub dead
        HeartbeatTimeout: 15 * time.Second,
        
        // Require N consecutive missed heartbeats
        MissedHeartbeats: 5,
        
        // Grace period before taking action
        GracePeriod: 10 * time.Second,
    },
})
```

#### 4. Witness Nodes

Deploy witness nodes in separate failure domains to break ties:

```go
// Witness node - doesn't run apps, only participates in quorum
witness := cluster.NewWitness("prod-witness", WitnessConfig{
    NATSURLs: []string{"nats://witness.example.com:4222"},
    
    // Connect to both hub and leaves
    HubURL:   "nats-leaf://hub:7422",
    LeafURLs: []string{
        "nats://leaf-usw:4222",
        "nats://leaf-eu:4222",
    },
    
    // Vote in partition scenarios
    VoteInPartition: true,
})
```

**Deployment pattern:**
```
┌─────────────┐         ┌─────────────┐
│  Hub (AZ-1) │◄───────►│ Leaf (AZ-2) │
│  PRIMARY    │         │  STANDBY    │
└──────┬──────┘         └──────┬──────┘
       │                       │
       │    ┌─────────────┐    │
       └───►│ Witness     │◄───┘
            │ (AZ-3)      │
            │ Tie-breaker │
            └─────────────┘
```

### Partition Handling Strategies

#### Strategy 1: Fail-Safe (Default)

Prefer availability loss over data inconsistency:

```go
cluster.WithPartitionStrategy(PartitionStrategy{
    Mode: PartitionFailSafe,
    
    // On partition detection:
    // - Leaf: Stop all writes, continue reads from local replica
    // - Hub: Continue operating normally
    
    OnPartition: func(ctx context.Context, event PartitionEvent) error {
        log.Printf("Partition detected: %s", event.Description)
        // Notify ops, trigger alerts
        alerting.Send("Network partition detected", event)
        return nil
    },
})
```

#### Strategy 2: Autonomous Leaf

Allow leaf to operate independently during partition (use with caution):

```go
cluster.WithPartitionStrategy(PartitionStrategy{
    Mode: PartitionAutonomous,
    
    // Leaf continues accepting writes during partition
    // Requires conflict resolution on reconnection
    
    ConflictResolution: ConflictLastWriteWins,  // or ConflictManual
    
    // Maximum time to operate autonomously
    MaxAutonomousDuration: 1 * time.Hour,
    
    // After max duration, switch to read-only
    FallbackToReadOnly: true,
})
```

#### Strategy 3: Coordinated Failover

Orchestrated promotion with external coordination:

```go
cluster.WithPartitionStrategy(PartitionStrategy{
    Mode: PartitionCoordinated,
    
    // Use external coordinator (Consul, etcd, or custom)
    Coordinator: &ConsulCoordinator{
        Address: "consul.example.com:8500",
        LockKey: "go-cluster/prod/leader",
    },
    
    // Only promote if coordinator approves
    RequireCoordinatorApproval: true,
})
```

### Partition Detection

```go
// Platform provides partition detection events
platform.OnPartition(func(event PartitionEvent) {
    switch event.Type {
    case PartitionHubUnreachable:
        // Lost connection to hub
        log.Printf("Hub unreachable for %v", event.Duration)
        
    case PartitionLeafUnreachable:
        // Hub lost connection to a leaf
        log.Printf("Leaf %s unreachable", event.Platform)
        
    case PartitionHealed:
        // Connection restored
        log.Printf("Partition healed after %v", event.Duration)
        
    case PartitionQuorumLost:
        // Lost quorum of leaves
        log.Printf("Quorum lost: %d/%d leaves reachable", 
            event.ReachableLeaves, event.TotalLeaves)
    }
})
```

### Recovery After Partition

#### Automatic Reconciliation

```go
cluster.WithReconciliation(ReconciliationConfig{
    // Automatically sync state after partition heals
    AutoReconcile: true,
    
    // How to handle conflicts
    ConflictResolution: ConflictConfig{
        // Default: hub wins
        Default: ConflictHubWins,
        
        // Per-app overrides
        AppOverrides: map[string]ConflictMode{
            "cache":   ConflictLastWriteWins,  // Cache can use LWW
            "convex":  ConflictHubWins,         // DB must use hub as source of truth
            "metrics": ConflictMerge,           // Metrics can be merged
        },
    },
    
    // Notify on conflicts
    OnConflict: func(ctx context.Context, conflict Conflict) error {
        log.Printf("Conflict detected: %s", conflict.Description)
        audit.Log(ctx, "partition.conflict", conflict)
        return nil
    },
})
```

#### Manual Recovery

```bash
# Check partition status
go-cluster partition status
# STATUS:     PARTITIONED
# Duration:   5m32s
# Hub:        unreachable
# Leaves:     2/3 reachable
# Quorum:     YES (2 >= 2)

# Force reconciliation after partition heals
go-cluster partition reconcile --platform prod-usw

# View conflicts
go-cluster partition conflicts
# APP      KEY              HUB_VALUE    LEAF_VALUE   RESOLUTION
# cache    user:123         v1           v2           pending
# convex   _meta/version    42           43           hub-wins

# Resolve conflict manually
go-cluster partition resolve --app cache --key user:123 --use leaf
```

### Metrics

```
# Partition detection metrics
gc_partition_detected_total{platform,type}        # Partition events
gc_partition_duration_seconds{platform}           # Current/last partition duration
gc_partition_healed_total{platform}               # Healed partitions
gc_partition_quorum_status{platform}              # 1=has quorum, 0=no quorum

# Split-brain prevention metrics
gc_fencing_token_epoch{platform,app}              # Current fencing token epoch
gc_fencing_rejections_total{platform,app}         # Writes rejected due to stale token
gc_promotion_blocked_no_quorum_total{platform}    # Promotions blocked by quorum

# Reconciliation metrics  
gc_reconciliation_total{platform,status}          # Reconciliation attempts
gc_conflicts_total{platform,app,resolution}       # Conflicts and resolutions
gc_conflicts_pending{platform,app}                # Unresolved conflicts
```

### Configuration Summary

```go
// Complete partition-safe leaf configuration
leafPlatform := cluster.NewPlatform("prod-usw", nodeID, localNatsURL,
    cluster.WithLeafConnection(LeafConnectionConfig{
        HubURLs:     []string{"nats-leaf://hub:7422"},
        HubPlatform: "prod",
        
        PartitionDetection: PartitionConfig{
            HeartbeatInterval: 3 * time.Second,
            HeartbeatTimeout:  15 * time.Second,
            MissedHeartbeats:  5,
            GracePeriod:       10 * time.Second,
        },
    }),
    
    cluster.WithPartitionStrategy(PartitionStrategy{
        Mode:                PartitionFailSafe,
        RequireQuorum:       true,
        QuorumSize:          0,  // Auto (majority)
        MaxAutonomousDuration: 0, // Disabled in fail-safe
    }),
    
    cluster.WithReconciliation(ReconciliationConfig{
        AutoReconcile: true,
        ConflictResolution: ConflictConfig{
            Default: ConflictHubWins,
        },
    }),
    
    cluster.WithFencing(FencingConfig{
        Enabled:     true,
        TokenStore:  "nats",
        StaleReject: true,
    }),
)

// App with promotion safety
convex := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db"),
    cluster.StandbyOf("prod", "convex"),
    cluster.PromoteOnHubFailure(PromoteConfig{
        Enabled:          true,
        DetectionTimeout: 30 * time.Second,
        RequireQuorum:    true,
        RequireWitness:   true,  // Also require witness vote
    }),
)
```

---

## Implementation Phase

Add to `12-implementation.md`:

```markdown
## Phase 9: Leaf Nodes & Multi-Zone

- [ ] `cluster.WithLeafHub()` configuration
- [ ] `cluster.WithLeafConnection()` configuration
- [ ] Leaf connection management (connect, reconnect, disconnect)
- [ ] Cross-platform RPC routing
- [ ] Cross-platform event propagation
- [ ] Cross-platform service discovery
- [ ] WAL replication across leaf boundaries
- [ ] `cluster.StandbyOf()` for cross-platform standbys
- [ ] Automatic promotion on hub failure
- [ ] CLI: `leaf list`, `leaf status`, `leaf promote`
- [ ] Leaf connection metrics
- [ ] **E2E: multi-zone deployment**
- [ ] **validate-leaf-failover.sh**
```
