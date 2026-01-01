# Observability

## Audit Log

```go
audit := app.Audit()

// Automatic logging:
// - election.leader_acquired
// - election.leader_lost
// - member.joined / member.left
// - lock.acquired / lock.released
// - backend.started / backend.stopped
// - wal.snapshot_created

entries, _ := audit.Query(ctx, audit.Filter{
    Since:    time.Now().Add(-1 * time.Hour),
    Category: "election",
})

for entry := range audit.Stream(ctx, audit.Filter{}) {
    fmt.Println(entry)
}

audit.Log(ctx, audit.Entry{
    Category: "app",
    Action:   "user.created",
    Data:     map[string]any{"user_id": 123},
})
```

**Entry structure:**
```json
{
  "ts": "2024-01-15T10:30:00Z",
  "node": "node-1",
  "category": "election",
  "action": "leader_acquired",
  "data": {"epoch": 42, "previous": "node-2"},
  "trace_id": "abc123"
}
```

---

## Metrics

```go
// Built-in metrics (auto-collected):
// gc_leader{cluster,node}                    - 1 if leader
// gc_members_total{cluster}                  - Member count
// gc_election_epoch{cluster}                 - Current epoch
// gc_replication_lag_seconds{cluster,node}   - WAL lag
// gc_wal_bytes_total{cluster,node}           - WAL bytes sent
// gc_snapshot_size_bytes{cluster}            - Last snapshot size
// gc_lock_held_seconds{cluster,name}         - Lock hold time
// gc_rpc_duration_seconds{cluster,method}    - RPC latency
// gc_health{cluster,node,check}              - Health check status

// HTTP endpoint (default :9090/metrics)
cluster.WithMetricsExport(interval)
```

---

## Health

```go
app.Health().Register("database", func(ctx context.Context) error {
    return db.Ping(ctx)
})

app.Health().Register("disk", func(ctx context.Context) error {
    if diskFree() < 1<<30 {
        return errors.New("low disk space")
    }
    return nil
})
```

**Endpoints:**
- `GET /health` - Liveness
- `GET /ready` - Readiness
- `GET /status` - Detailed status

```json
{
  "status": "passing",
  "checks": {
    "nats": {"status": "passing", "latency_ms": 2},
    "database": {"status": "passing"},
    "replication": {"status": "passing", "lag_ms": 50}
  }
}
```

---

## Tracing

```go
app := cluster.New("myapp", nodeID, natsURL,
    cluster.WithTracing(otel.GetTracerProvider()),
)

// All operations traced:
// - Election transitions
// - RPC calls
// - Lock acquisition
// - WAL operations
```
