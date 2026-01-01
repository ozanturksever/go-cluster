# Convex Backend Integration Example

This example demonstrates a production-ready Convex backend setup with go-cluster, featuring:

- **SQLite3 + Litestream WAL Replication**: Near real-time database replication
- **Virtual IP (VIP) Failover**: Seamless client access during failover
- **Backend Service Management**: Automatic start/stop of the Convex backend
- **Service Discovery**: Expose endpoints for monitoring integration

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     Convex HA Cluster                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────────────┐         ┌─────────────────────┐        │
│  │    Node 1 (PRIMARY) │         │   Node 2 (STANDBY)  │        │
│  ├─────────────────────┤         ├─────────────────────┤        │
│  │ convex-backend      │         │ (backend stopped)   │        │
│  │ └─ SQLite3 (WAL)    │────────▶│ └─ Replica DB       │        │
│  │                     │  NATS   │    (read-only)      │        │
│  │ VIP: 10.0.1.100 ✓   │         │ VIP: -              │        │
│  └─────────────────────┘         └─────────────────────┘        │
│                                                                  │
│  Clients connect to VIP: 10.0.1.100                             │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Prerequisites

1. **NATS Server with JetStream:**
   ```bash
   nats-server -js
   ```

2. **Root/sudo access** (for VIP management) or run without VIP flag

## Running the Example

### Without VIP (Development)

```bash
# Terminal 1 - Primary
go run main.go -node node-1

# Terminal 2 - Standby
go run main.go -node node-2
```

### With VIP (Production-like)

```bash
# Terminal 1 - Primary (requires root for VIP)
sudo go run main.go -node node-1 -vip 10.0.1.100/24 -interface eth0

# Terminal 2 - Standby
sudo go run main.go -node node-2 -vip 10.0.1.100/24 -interface eth0
```

## Failover Sequence

When the primary fails:

1. **Standby detects leader failure** (via NATS KV watch)
2. **Standby wins election** (becomes new leader)
3. **Final WAL catch-up** from NATS Object Store
4. **Database promotion** (replica → primary)
5. **VIP acquired** (if configured)
6. **Backend service started**
7. **WAL replication started** (to new standby)
8. **Clients reconnect** (via VIP, seamless)

Typical failover time: **< 5 seconds**

## Configuration Options

| Flag | Description | Default |
|------|-------------|---------|
| `-node` | Node ID | `node-1` |
| `-nats` | NATS URL | `nats://localhost:4222` |
| `-platform` | Platform name | `convex-prod` |
| `-db` | Primary database path | `/tmp/convex-data.db` |
| `-replica` | Replica database path | `/tmp/convex-replica.db` |
| `-vip` | VIP CIDR (optional) | (none) |
| `-interface` | Network interface for VIP | `eth0` |
| `-http` | HTTP port | `8080` |

## Production Deployment

For production, you would:

1. Use `cluster.Systemd("convex-backend")` instead of the process backend
2. Configure proper database paths on persistent storage
3. Set up monitoring with Prometheus/Grafana
4. Configure NATS credentials and TLS

### Example Production Configuration

```go
app := cluster.NewApp("convex",
    cluster.Singleton(),
    
    // Database replication
    cluster.WithSQLite("/var/lib/convex/data.db",
        cluster.SQLiteReplica("/var/lib/convex/replica.db"),
        cluster.SQLiteSnapshotInterval(1*time.Hour),
    ),
    
    // VIP for client access
    cluster.VIP("10.0.1.100/24", "eth0"),
    
    // Systemd backend management
    cluster.WithBackend(cluster.Systemd("convex-backend",
        cluster.BackendHealthEndpoint("http://localhost:3210/health"),
        cluster.BackendStartTimeout(60*time.Second),
    )),
)
```

## Monitoring

Services exposed for discovery:

| Service | Port | Protocol | Description |
|---------|------|----------|-------------|
| `http` | 3210 | HTTP | Main Convex API |
| `metrics` | 9090 | HTTP | Prometheus metrics |

Export for Prometheus:
```bash
go-cluster services export --format prometheus --platform convex-prod
```

## See Also

- `spec/14-convex-integration.md` - Full specification
- `spec/05-data-replication.md` - WAL replication details
- `spec/07-backends.md` - Backend controller documentation
