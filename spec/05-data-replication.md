# Data Replication

## SQLite3 + Litestream WAL Replication

go-cluster uses **SQLite3** as the embedded database with **Litestream** for continuous WAL (Write-Ahead Log) replication via NATS JetStream/Object Store.

### How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                     Primary Node (Leader)                        │
├─────────────────────────────────────────────────────────────────┤
│  Backend Service ──writes──▶ SQLite3 DB (WAL mode)              │
│                                    │                             │
│                              Litestream                          │
│                                    │                             │
│                         WAL frames + snapshots                   │
│                                    ▼                             │
│                          NATS Object Store                       │
│                       (bucket: {cluster}-wal)                    │
└─────────────────────────────────────────────────────────────────┘
                                    │
                    ┌───────────────┴───────────────┐
                    ▼                               ▼
┌─────────────────────────────┐   ┌─────────────────────────────┐
│     Passive Node 1          │   │     Passive Node 2          │
├─────────────────────────────┤   ├─────────────────────────────┤
│  Litestream (restore mode)  │   │  Litestream (restore mode)  │
│           │                 │   │           │                 │
│           ▼                 │   │           ▼                 │
│  SQLite3 Replica DB         │   │  SQLite3 Replica DB         │
│  (ready for failover)       │   │  (ready for failover)       │
└─────────────────────────────┘   └─────────────────────────────┘
```

### Primary Node (Leader)

- Litestream monitors the SQLite3 WAL file
- Captures WAL frames as LTX (Litestream Transaction) files
- Publishes LTX files to NATS Object Store (`{cluster}-wal` bucket)
- Takes periodic snapshots for faster recovery

### Passive Nodes (Standbys)

- Subscribe to the NATS Object Store bucket
- Continuously restore LTX files to local replica database
- Maintain near real-time copy of the primary database
- Ready for instant failover (sub-second promotion)

**Failover sequence:**
1. Leader dies or steps down
2. New leader elected (< 500ms via KV watch)
3. New leader performs final catch-up from NATS Object Store
4. New leader promotes replica → primary (atomic file swap)
5. Backend service starts with promoted database
6. VIP moves to new leader
7. New leader begins WAL replication to NATS

```go
// WithSQLite enables SQLite3 database with Litestream WAL replication
app := cluster.New("myapp", "node-1", natsURL,
    cluster.WithSQLite("/data/app.db"),  // Primary DB path
)

app.WAL().Position()     // Current WAL position
app.WAL().Lag()          // Replication lag (passive only)
app.WAL().Sync(ctx)      // Force sync (primary only)

app.Snapshots().Create(ctx)
app.Snapshots().Latest(ctx)
app.Snapshots().Restore(ctx, id, dest)
```

### SQLite Configuration Options

```go
// Full SQLite3 + Litestream configuration
cluster.WithSQLite("/data/app.db",
    cluster.SQLiteReplica("/data/replica.db"),     // Replica DB path (passive nodes)
    cluster.SQLiteSnapshotInterval(1*time.Hour),   // Snapshot frequency
    cluster.SQLiteRetention(24*time.Hour),         // WAL retention period
    cluster.SQLiteCompactionLevels([]CompactionLevel{
        {Level: 0},
        {Level: 1, Interval: 10 * time.Second},
    }),
)
```

### Requirements

- **SQLite3 WAL mode**: The backend service MUST use SQLite in WAL mode
- **Single writer**: Only the leader writes to the database
- **File-based**: Database must be a local file (not in-memory)

---

## Distributed KV

```go
kv := app.KV()

kv.Get(ctx, "config/max-conn")
kv.Put(ctx, "config/max-conn", []byte("100"))
kv.Delete(ctx, "config/max-conn")

// Atomic
rev, _ := kv.Create(ctx, "lock/migration", []byte("node-1"))
kv.Update(ctx, "lock/migration", []byte("node-1"), rev)

// Watch
for entry := range kv.Watch(ctx, "config/>") {
    fmt.Println(entry.Key, string(entry.Value))
}
```

---

## Distributed Locks

```go
lock := app.Lock("migrations")

if err := lock.Acquire(ctx); err != nil {
    return err
}
defer lock.Release(ctx)

// Or use convenience method
err := lock.Do(ctx, func(ctx context.Context) error {
    return runMigration(ctx)
})
```

---

## Options

```go
// SQLite3 + Litestream WAL replication
cluster.WithSQLite(path)                // Enable SQLite3 WAL replication via Litestream
cluster.WithSQLiteReplica(path)         // Replica destination (default: {db}-replica)
cluster.WithSnapshotInterval(duration)  // Snapshot interval, default: 1h
cluster.WithRetention(duration)         // WAL retention, default: 24h

// Leader election
cluster.WithLeaseTTL(duration)          // Leader lease, default: 10s
cluster.WithHeartbeat(duration)         // Heartbeat interval, default: 3s
```

> **Note:** `WithDB()` is an alias for `WithSQLite()` for backward compatibility.

---

## Hooks

```go
// Active/Passive pattern
app.OnActive(func(ctx context.Context) error { ... })
app.OnPassive(func(ctx context.Context) error { ... })
app.OnLeaderChange(func(ctx context.Context, leader string) error { ... })

// Pool pattern
app.OnStart(func(ctx context.Context) error { ... })
app.OnStop(func(ctx context.Context) error { ... })

// Ring pattern
app.OnOwn(func(ctx context.Context, partitions []int) error { ... })
app.OnRelease(func(ctx context.Context, partitions []int) error { ... })

// Common
app.OnMemberJoin(func(ctx context.Context, m Member) error { ... })
app.OnMemberLeave(func(ctx context.Context, m Member) error { ... })
app.OnHealthChange(func(ctx context.Context, status Health) error { ... })
```
