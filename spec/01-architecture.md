# Architecture

## Component Model (Inspired by Convex)

Apps are **isolated components** that:
- Own their **data** (KV, DB, locks) - other apps cannot access
- Expose **APIs** - explicit boundaries for cross-app calls
- Have **placement rules** - where and how many instances run
- Share **infrastructure** - nodes, NATS, observability

```
┌─────────────────────────────────────────────────────────────────┐
│                        Platform: "prod"                          │
│                     Nodes: [n1, n2, n3, n4, n5]                  │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  App: convex                     App: cache                      │
│  ├─ Mode: Singleton              ├─ Mode: Spread (all nodes)    │
│  ├─ Placement: n1 (active)       ├─ Placement: n1,n2,n3,n4,n5   │
│  │              n2,n3 (standby)  ├─ Pattern: Ring(256)          │
│  ├─ Pattern: ActivePassive       └─ Data: partitioned           │
│  ├─ Data: DB + WAL replication                                  │
│  └─ VIP: 10.0.1.100                                             │
│                                                                  │
│  App: api                        App: worker                     │
│  ├─ Mode: Spread (all nodes)     ├─ Mode: N-of-M (3 of 5)       │
│  ├─ Placement: n1,n2,n3,n4,n5    ├─ Placement: n1,n3,n5         │
│  ├─ Pattern: Pool                ├─ Pattern: Pool               │
│  └─ Data: sessions               └─ Data: job state             │
│                                                                  │
│  App: scheduler                                                  │
│  ├─ Mode: Singleton                                             │
│  ├─ Affinity: node with label "scheduler=true"                  │
│  └─ Pattern: ActivePassive                                      │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Concepts

| Concept | Description |
|---------|-------------|
| **Platform** | Cluster of nodes + NATS. Runs multiple apps. |
| **App** | Isolated component with own data. Like Convex component. |
| **Mode** | How many instances: Singleton, Spread, N-of-M |
| **Placement** | Where to run: all nodes, specific nodes, by label |
| **Pattern** | HA behavior: ActivePassive, Pool, Ring |

---

## Stack Diagram

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

---

## Package Structure

```
github.com/ozanturksever/go-cluster/
├── cluster.go          # New(), Pool(), Ring()
├── options.go          # WithXXX options
├── node.go             # Core node implementation
├── election.go         # Leader election
├── membership.go       # Cluster membership
├── health.go           # Health checks
├── kv.go               # Distributed KV
├── lock.go             # Distributed locks
├── rpc.go              # Inter-node RPC
├── events.go           # Pub/sub
├── audit.go            # Audit logging
├── metrics.go          # Prometheus metrics
├── wal/
│   ├── replicator.go   # Litestream primary
│   ├── restorer.go     # Litestream passive
│   └── nats.go         # NATS replica for Litestream
├── snapshot/
│   └── snapshot.go     # Snapshot manager
├── backend/
│   ├── backend.go      # Interface
│   ├── systemd.go
│   ├── docker.go
│   └── process.go
├── vip/
│   └── vip.go          # VIP management
└── examples/
    ├── basic/
    ├── convex-backend/
    └── distributed-cache/
```
