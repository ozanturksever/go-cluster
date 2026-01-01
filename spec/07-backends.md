# Backend Controllers

Manage external services on leadership changes. The cluster manager coordinates with backends to ensure proper sequencing during failover.

## Backend Coordination Flow

The cluster manager orchestrates the backend lifecycle in coordination with SQLite3/Litestream replication:

```
┌─────────────────────────────────────────────────────────────────┐
│                    Becoming Leader (Failover)                    │
├─────────────────────────────────────────────────────────────────┤
│  1. Win election (NATS KV)                                      │
│  2. Final WAL catch-up from NATS Object Store                   │
│  3. Stop passive replication                                    │
│  4. Promote replica DB → data path (atomic swap)                │
│  5. Acquire VIP                                                 │
│  6. Start backend service (systemd/docker/process)              │
│  7. Wait for DB file + verify WAL mode                          │
│  8. Start primary WAL replication (Litestream → NATS)           │
│  9. Start periodic snapshots                                    │
└─────────────────────────────────────────────────────────────────┘

┌─────────────────────────────────────────────────────────────────┐
│                    Stepping Down (Lost Leadership)               │
├─────────────────────────────────────────────────────────────────┤
│  1. Stop snapshotter                                            │
│  2. Stop primary WAL replication                                │
│  3. Stop backend service                                        │
│  4. Release VIP                                                 │
│  5. Start passive replication (restore from NATS)               │
│  6. Perform initial catch-up                                    │
└─────────────────────────────────────────────────────────────────┘
```

## Systemd

```go
app := cluster.New("myapp", nodeID, natsURL,
    cluster.WithBackend(cluster.Systemd("convex-backend")),
)

// Automatically:
// - Starts service when becoming leader (after DB promotion)
// - Stops service when losing leadership (before releasing VIP)
// - Health checks via systemctl status
// - Waits for service to be fully running before starting replication
```

---

## Docker

```go
cluster.WithBackend(cluster.Docker(cluster.DockerConfig{
    Container: "myapp",
    Image:     "myapp:latest",
    Ports:     []string{"3000:3000"},
    Env:       map[string]string{"DB_PATH": "/data/db"},
}))
```

---

## Process

```go
cluster.WithBackend(cluster.Process(cluster.ProcessConfig{
    Command: []string{"./myapp", "--port", "3000"},
    Dir:     "/opt/myapp",
    Env:     []string{"DB_PATH=/data/db"},
}))
```

---

## Custom

```go
cluster.WithBackend(&MyBackend{})

type Backend interface {
    Start(ctx context.Context) error
    Stop(ctx context.Context) error
    Health(ctx context.Context) (Health, error)
}
```

---

## Backend + Database Coordination

The cluster manager ensures proper coordination between the backend service and SQLite3 database:

### Startup Sequence (Leader)

```go
// 1. Cluster manager promotes replica to data path
// 2. Cluster manager starts backend service
// 3. Backend creates/opens SQLite3 DB in WAL mode
// 4. Cluster manager detects DB file exists
// 5. Cluster manager verifies WAL mode is enabled
// 6. Cluster manager starts Litestream primary replication
```

### Shutdown Sequence (Stepping Down)

```go
// 1. Cluster manager stops Litestream replication
// 2. Cluster manager stops backend service
// 3. Backend closes SQLite3 DB cleanly
// 4. Cluster manager releases VIP
// 5. Cluster manager starts passive replication
```

### Health Checks

```go
cluster.WithBackend(cluster.Systemd("convex-backend",
    cluster.BackendHealthEndpoint("http://localhost:3210/health"),
    cluster.BackendHealthInterval(5*time.Second),
    cluster.BackendHealthTimeout(3*time.Second),
    cluster.BackendStartTimeout(60*time.Second),  // Wait for service to be healthy
))
```

### Database Path Configuration

The backend service must use the same database path configured in the cluster manager:

```go
app := cluster.New("convex", nodeID, natsURL,
    cluster.WithSQLite("/var/lib/convex/data.db"),      // Cluster manager config
    cluster.WithBackend(cluster.Systemd("convex-backend")),
)

// The convex-backend systemd service should be configured with:
// Environment=CONVEX_LOCAL_STORAGE=/var/lib/convex/data.db
```

---

## Example: Convex Backend Manager

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "github.com/ozanturksever/go-cluster"
)

func main() {
    app := cluster.New(
        os.Getenv("CLUSTER_ID"),
        os.Getenv("NODE_ID"),
        os.Getenv("NATS_URL"),
        
        // SQLite3 + Litestream WAL replication
        cluster.WithSQLite("/var/lib/convex/data.db",
            cluster.SQLiteReplica("/var/lib/convex/replica.db"),
            cluster.SQLiteSnapshotInterval(1*time.Hour),
        ),
        
        // VIP for client access
        cluster.WithVIP(os.Getenv("VIP_CIDR"), "eth0"),
        
        // Backend service management
        cluster.WithBackend(cluster.Systemd("convex-backend",
            cluster.BackendHealthEndpoint("http://localhost:3210/health"),
            cluster.BackendStartTimeout(60*time.Second),
        )),
        
        // Observability
        cluster.WithMetricsAddr(":9090"),
        cluster.WithHealthAddr(":8080"),
    )
    
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()
    
    if err := app.Run(ctx); err != nil {
        os.Exit(1)
    }
}
```

**~35 lines. Full HA coordination including:**
- Leader election via NATS KV
- SQLite3 WAL replication via Litestream → NATS Object Store
- Automatic replica promotion on failover
- Backend service lifecycle management
- VIP failover

---

## Systemd Service Configuration

Example systemd unit file for the convex-backend service:

```ini
# /etc/systemd/system/convex-backend.service
[Unit]
Description=Convex Backend
After=network-online.target

[Service]
Type=simple
User=convex
Group=convex

# Database path must match cluster manager configuration
Environment=CONVEX_LOCAL_STORAGE=/var/lib/convex/data.db
Environment=CONVEX_RUNTIME_FLAGS=--wal-mode

ExecStart=/usr/bin/convex-local-backend

Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
```

> **Important:** The cluster manager controls when this service starts/stops. Do NOT enable it to start on boot (`systemctl enable`). The cluster manager will start it when the node becomes leader.
