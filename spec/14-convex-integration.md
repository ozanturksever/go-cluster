# Convex Backend Integration

This document shows how go-cluster integrates with **convex-cluster-manager** to provide high-availability clustering for Convex self-hosted backends.

---

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                           Convex HA Cluster                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │                         NATS Cluster                                 │    │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐                  │    │
│  │  │   NATS-1    │──│   NATS-2    │──│   NATS-3    │                  │    │
│  │  │  (JetStream)│  │  (JetStream)│  │  (JetStream)│                  │    │
│  │  └─────────────┘  └─────────────┘  └─────────────┘                  │    │
│  │        │                │                │                           │    │
│  │        └────────────────┼────────────────┘                           │    │
│  │                         │                                            │    │
│  │    Resources:                                                        │    │
│  │    • KV: convex-{cluster}-election  (leader election)               │    │
│  │    • KV: convex-{cluster}-state     (node state)                    │    │
│  │    • KV: convex-{cluster}-services  (service discovery)             │    │
│  │    • ObjectStore: convex-{cluster}-wal (Litestream LTX files)       │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│  ┌─────────────────────┐         ┌─────────────────────┐                    │
│  │       Node 1        │         │       Node 2        │                    │
│  │     (PRIMARY)       │         │     (PASSIVE)       │                    │
│  ├─────────────────────┤         ├─────────────────────┤                    │
│  │                     │         │                     │                    │
│  │  convex-cluster-    │         │  convex-cluster-    │                    │
│  │  manager (daemon)   │         │  manager (daemon)   │                    │
│  │  ├─ Election ✓      │         │  ├─ Election        │                    │
│  │  ├─ VIP: 10.0.1.100 │         │  ├─ VIP: -          │                    │
│  │  ├─ Backend: running│         │  ├─ Backend: stopped│                    │
│  │  └─ WAL: replicating│         │  └─ WAL: restoring  │                    │
│  │                     │         │                     │                    │
│  │  convex-backend     │         │  (standby)          │                    │
│  │  └─ SQLite3 (WAL)   │         │  └─ Replica DB      │                    │
│  │     /data/convex.db │         │     /data/replica.db│                    │
│  │                     │         │                     │                    │
│  └─────────────────────┘         └─────────────────────┘                    │
│                                                                              │
│  VIP: 10.0.1.100 ────────────────┐                                          │
│                                  │                                          │
│                          ┌───────▼───────┐                                  │
│                          │   Clients     │                                  │
│                          │ connect to VIP│                                  │
│                          └───────────────┘                                  │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Component Roles

### convex-cluster-manager (Daemon)

The cluster manager daemon orchestrates all HA functionality:

| Component | Responsibility |
|-----------|----------------|
| **Election** | Leader election via NATS KV with TTL-based leases |
| **State Manager** | Persists node state to NATS KV for observability |
| **Backend Controller** | Starts/stops convex-backend via systemd |
| **VIP Manager** | Acquires/releases virtual IP on leadership changes |
| **Primary Replication** | Runs Litestream to replicate WAL → NATS Object Store |
| **Passive Replication** | Restores WAL from NATS Object Store → local replica |
| **Snapshotter** | Takes periodic full database snapshots |
| **Service** | NATS micro service for health/discovery queries |
| **Service Registry** | Registers exposed services for discovery |

### convex-backend (Managed Service)

The Convex backend is a **managed service** controlled by the cluster manager:

- **NOT** started on boot (no `systemctl enable`)
- Started by cluster manager when node becomes PRIMARY
- Stopped by cluster manager when node steps down
- Must use SQLite3 in WAL mode

---

## Configuration

### Cluster Configuration File

```json
// /etc/convex/cluster.json
{
  "clusterId": "convex-prod",
  "nodeId": "node-1",
  "nats": {
    "servers": [
      "nats://10.0.1.10:4222",
      "nats://10.0.1.11:4222",
      "nats://10.0.1.12:4222"
    ],
    "credentials": "/etc/convex/nats.creds"
  },
  "election": {
    "leaderTtlMs": 10000,
    "heartbeatIntervalMs": 3000
  },
  "wal": {
    "streamRetention": "24h",
    "snapshotIntervalMs": 3600000,
    "replicaPath": "/var/lib/convex/replica.db"
  },
  "backend": {
    "serviceName": "convex-backend",
    "healthEndpoint": "http://localhost:3210/health",
    "dataPath": "/var/lib/convex/data.db"
  },
  "vip": {
    "address": "10.0.1.100",
    "netmask": 24,
    "interface": "eth0"
  },
  "services": {
    "http": {
      "port": 3210,
      "protocol": "http",
      "path": "/",
      "healthPath": "/health",
      "metadata": {
        "version": "1.0.0",
        "docs": "/api/docs"
      },
      "tags": ["api", "public"]
    },
    "metrics": {
      "port": 9090,
      "protocol": "http",
      "path": "/metrics",
      "metadata": {
        "format": "prometheus",
        "scrape_interval": "15s"
      },
      "tags": ["monitoring", "prometheus"]
    },
    "syslog": {
      "port": 1514,
      "protocol": "tcp",
      "metadata": {
        "format": "rfc5424",
        "facility": "local0"
      },
      "tags": ["logging", "syslog"]
    }
  }
}
```

### Systemd Service Files

**Cluster Manager Service** (`/etc/systemd/system/convex-cluster-manager.service`):

```ini
[Unit]
Description=Convex Cluster Manager
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root

ExecStart=/usr/bin/convex-cluster-manager daemon --config /etc/convex/cluster.json

Restart=always
RestartSec=5

# Needs root for VIP management
AmbientCapabilities=CAP_NET_ADMIN

[Install]
WantedBy=multi-user.target
```

**Convex Backend Service** (`/etc/systemd/system/convex-backend.service`):

```ini
[Unit]
Description=Convex Backend
After=network-online.target

[Service]
Type=simple
User=convex
Group=convex

# Database path MUST match cluster.json backend.dataPath
Environment=CONVEX_LOCAL_STORAGE=/var/lib/convex/data.db
Environment=CONVEX_RUNTIME_FLAGS=--wal-mode

ExecStart=/usr/bin/convex-local-backend

Restart=on-failure
RestartSec=5

[Install]
# DO NOT ENABLE - cluster manager controls lifecycle
# WantedBy=multi-user.target
```

> **Important:** The convex-backend service should NOT be enabled to start on boot. The cluster manager controls when it starts and stops.

---

## Failover Sequence

### Node Becomes PRIMARY (Failover)

```
Timeline (Node 2 taking over from failed Node 1):

T+0ms     Node 1 fails (or leader lease expires)
          │
T+500ms   Node 2 wins election (NATS KV watch)
          │
          ├─► setRole("PRIMARY")
          │
T+1000ms  Final catch-up from NATS Object Store
          │   └─► passive.CatchUp() with retries
          │
T+2000ms  Stop passive replication
          │
T+2500ms  Promote replica → data path
          │   └─► Atomic file swap: replica.db → data.db
          │
T+3000ms  Acquire VIP
          │   └─► ip addr add 10.0.1.100/24 dev eth0
          │
T+3500ms  Start backend service
          │   └─► systemctl start convex-backend
          │
T+5000ms  Wait for database file
          │
T+7000ms  Verify WAL mode
          │   └─► PRAGMA journal_mode; == "wal"
          │
T+8000ms  Start primary WAL replication
          │   └─► Litestream monitors data.db
          │   └─► Publishes LTX to NATS Object Store
          │
T+9000ms  Start periodic snapshots
          │
T+10000ms ✓ Failover complete, serving traffic
```

### Node Steps Down (Lost Leadership)

```
Timeline (Node 2 stepping down):

T+0ms     Lost leadership (lease expired or manual demote)
          │
          ├─► setRole("PASSIVE")
          │
T+500ms   Stop snapshotter
          │
T+1000ms  Stop primary WAL replication
          │
T+2000ms  Stop backend service
          │   └─► systemctl stop convex-backend
          │
T+5000ms  Release VIP
          │   └─► ip addr del 10.0.1.100/24 dev eth0
          │
T+6000ms  Start passive replication
          │   └─► Subscribe to NATS Object Store
          │
T+7000ms  Initial catch-up
          │   └─► Restore latest LTX files to replica.db
          │
T+8000ms  ✓ Step-down complete, ready for failover
```

---

## CLI Commands

### Initialization

```bash
# Initialize cluster configuration on a new node
convex-cluster-manager init \
  --cluster-id convex-prod \
  --node-id node-1 \
  --nats nats://10.0.1.10:4222,nats://10.0.1.11:4222 \
  --vip 10.0.1.100 \
  --vip-netmask 24 \
  --interface eth0
```

### Join Existing Cluster

```bash
# Join cluster and bootstrap data from snapshot
convex-cluster-manager join --config /etc/convex/cluster.json

# Output:
# Joining cluster convex-prod as node node-2...
# ✓ Connected to cluster. Current leader: node-1
# Bootstrapping data from cluster snapshot...
# ✓ Data bootstrapped to /var/lib/convex/data.db
# ✓ Successfully joined cluster
```

### Start Daemon

```bash
# Start the cluster manager daemon
convex-cluster-manager daemon --config /etc/convex/cluster.json

# Or via systemd
systemctl start convex-cluster-manager
```

### Status

```bash
# Query cluster status
convex-cluster-manager status --config /etc/convex/cluster.json

# Output:
# Cluster Status
# ==============
# 
# Cluster ID:  convex-prod
# Node ID:     node-1
# NATS:        [nats://10.0.1.10:4222 nats://10.0.1.11:4222]
# VIP:         10.0.1.100/24
# 
# Role:        PRIMARY
# Leader:      node-1
# Backend:     running
# Rep. Lag:    0ms
# VIP Status:  held (this node is PRIMARY)
```

### Manual Failover

```bash
# Demote current leader (triggers failover)
convex-cluster-manager demote --config /etc/convex/cluster.json

# Force promote a specific node
convex-cluster-manager promote --config /etc/convex/cluster.json
```

### Leave Cluster

```bash
# Gracefully leave the cluster
convex-cluster-manager leave --config /etc/convex/cluster.json
```

---

## go-cluster API Equivalent

The convex-cluster-manager is a production implementation. Using go-cluster directly would look like:

```go
package main

import (
    "context"
    "os"
    "os/signal"
    "time"
    
    "github.com/ozanturksever/go-cluster"
)

func main() {
    // Create the cluster app with full configuration
    app := cluster.New(
        "convex-prod",              // Cluster ID
        os.Getenv("NODE_ID"),       // Node ID from environment
        os.Getenv("NATS_URL"),      // NATS connection URL(s)
        
        // SQLite3 + Litestream WAL replication
        cluster.WithSQLite("/var/lib/convex/data.db",
            cluster.SQLiteReplica("/var/lib/convex/replica.db"),
            cluster.SQLiteSnapshotInterval(1*time.Hour),
            cluster.SQLiteRetention(24*time.Hour),
        ),
        
        // Virtual IP for client access
        cluster.WithVIP("10.0.1.100/24", "eth0"),
        
        // Backend service management via systemd
        cluster.WithBackend(cluster.Systemd("convex-backend",
            cluster.BackendHealthEndpoint("http://localhost:3210/health"),
            cluster.BackendHealthInterval(5*time.Second),
            cluster.BackendStartTimeout(60*time.Second),
        )),
        
        // Leader election tuning
        cluster.WithLeaseTTL(10*time.Second),
        cluster.WithHeartbeat(3*time.Second),
        
        // Observability
        cluster.WithMetricsAddr(":9090"),
        cluster.WithHealthAddr(":8080"),
        
        // NATS authentication
        cluster.WithNATSCreds("/etc/convex/nats.creds"),
    )
    
    // Optional: Custom hooks for application-specific logic
    app.OnActive(func(ctx context.Context) error {
        log.Println("This node is now PRIMARY")
        return nil
    })
    
    app.OnPassive(func(ctx context.Context) error {
        log.Println("This node is now PASSIVE")
        return nil
    })
    
    app.OnLeaderChange(func(ctx context.Context, leader string) error {
        log.Printf("Leader changed to: %s", leader)
        return nil
    })
    
    // Run until interrupted
    ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
    defer cancel()
    
    if err := app.Run(ctx); err != nil {
        log.Fatal(err)
    }
}
```

---

## Deployment Checklist

### Prerequisites

- [ ] NATS cluster with JetStream enabled (3+ nodes recommended)
- [ ] Network configured for VIP (all nodes on same subnet)
- [ ] Convex backend binary installed on all nodes
- [ ] Convex cluster manager binary installed on all nodes

### Per-Node Setup

1. **Install binaries:**
   ```bash
   # Install convex-backend (with WAL mode support)
   curl -fsSL https://get.convex.dev/self-hosted | bash
   
   # Install convex-cluster-manager
   curl -fsSL https://get.convex.dev/cluster-manager | bash
   ```

2. **Initialize configuration:**
   ```bash
   convex-cluster-manager init \
     --cluster-id convex-prod \
     --node-id $(hostname) \
     --nats nats://nats1:4222,nats://nats2:4222,nats://nats3:4222 \
     --vip 10.0.1.100 \
     --interface eth0
   ```

3. **Create systemd services:**
   ```bash
   # Copy service files
   cp convex-backend.service /etc/systemd/system/
   cp convex-cluster-manager.service /etc/systemd/system/
   
   # Reload systemd
   systemctl daemon-reload
   
   # Enable ONLY the cluster manager (NOT convex-backend)
   systemctl enable convex-cluster-manager
   ```

4. **Join cluster (for additional nodes):**
   ```bash
   convex-cluster-manager join
   ```

5. **Start cluster manager:**
   ```bash
   systemctl start convex-cluster-manager
   ```

### Verification

```bash
# Check cluster status
convex-cluster-manager status

# Check logs
journalctl -u convex-cluster-manager -f

# Verify VIP
ip addr show eth0 | grep 10.0.1.100

# Test failover
convex-cluster-manager demote
```

---

## Service Discovery

The convex-cluster-manager exposes services for discovery by monitoring systems, log collectors, and other integrations. See [15-service-discovery.md](15-service-discovery.md) for full details.

### Exposed Services

The Convex backend typically exposes:

| Service | Port | Protocol | Description |
|---------|------|----------|-------------|
| `http` | 3210 | HTTP | Main Convex API |
| `metrics` | 9090 | HTTP | Prometheus metrics |
| `syslog` | 1514 | TCP | Syslog collection endpoint |
| `health` | 8080 | HTTP | Cluster manager health |

### Configuration in cluster.json

```json
"services": {
  "http": {
    "port": 3210,
    "protocol": "http",
    "path": "/",
    "healthPath": "/health",
    "metadata": {
      "version": "1.0.0",
      "docs": "/api/docs"
    },
    "tags": ["api", "public"]
  },
  "metrics": {
    "port": 9090,
    "protocol": "http",
    "path": "/metrics",
    "metadata": {
      "format": "prometheus",
      "scrape_interval": "15s",
      "job": "convex-backend"
    },
    "tags": ["monitoring", "prometheus"]
  },
  "syslog": {
    "port": 1514,
    "protocol": "tcp",
    "metadata": {
      "format": "rfc5424",
      "facility": "local0",
      "app_name": "convex"
    },
    "tags": ["logging", "syslog"]
  }
}
```

### go-cluster API

```go
// Expose services in go-cluster API
app := cluster.New("convex-prod", nodeID, natsURL,
    // ... other config ...
)

// Main HTTP API
app.ExposeService("http", cluster.ServiceConfig{
    Port:     3210,
    Protocol: "http",
    Path:     "/",
    HealthCheck: &cluster.HealthCheckConfig{
        Path:           "/health",
        Interval:       10 * time.Second,
        ExpectedStatus: 200,
    },
    Metadata: map[string]string{
        "version": "1.0.0",
        "docs":    "/api/docs",
    },
    Tags: []string{"api", "public"},
})

// Prometheus metrics
app.ExposeService("metrics", cluster.ServiceConfig{
    Port:     9090,
    Protocol: "http",
    Path:     "/metrics",
    Metadata: map[string]string{
        "format":          "prometheus",
        "scrape_interval": "15s",
        "job":             "convex-backend",
    },
    Tags: []string{"monitoring", "prometheus"},
})

// Syslog endpoint for log collection
app.ExposeService("syslog", cluster.ServiceConfig{
    Port:     1514,
    Protocol: "tcp",
    Metadata: map[string]string{
        "format":   "rfc5424",
        "facility": "local0",
        "app_name": "convex",
    },
    Tags: []string{"logging", "syslog"},
})
```

### Discovering Services

```bash
# List all Convex services
convex-cluster-manager services list
# SERVICE   NODE     ADDRESS        PORT   PROTOCOL  HEALTH   TAGS
# http      node-1   10.0.1.10      3210   http      passing  api,public
# http      node-2   10.0.1.11      3210   http      standby  api,public
# metrics   node-1   10.0.1.10      9090   http      passing  monitoring,prometheus
# metrics   node-2   10.0.1.11      9090   http      passing  monitoring,prometheus
# syslog    node-1   10.0.1.10      1514   tcp       passing  logging,syslog
# syslog    node-2   10.0.1.11      1514   tcp       passing  logging,syslog

# Export for Prometheus
convex-cluster-manager services export --format prometheus --tag prometheus
# Output: prometheus_targets.json

# Export for Promtail/Loki (syslog targets)
convex-cluster-manager services export --format syslog --tag syslog
```

### Prometheus Auto-Discovery

Generate Prometheus scrape targets from service discovery:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: 'convex-cluster'
    file_sd_configs:
      - files:
          - '/etc/prometheus/convex-targets.json'
        refresh_interval: 30s
```

```bash
# Cron job to update targets
*/5 * * * * convex-cluster-manager services export \
  --format prometheus \
  --tag prometheus \
  > /etc/prometheus/convex-targets.json
```

### Syslog/Log Collection Integration

For Promtail, Fluentd, or other log collectors:

```yaml
# promtail.yml - auto-discover syslog endpoints
scrape_configs:
  - job_name: convex-syslog
    syslog:
      listen_address: 0.0.0.0:1514
      labels:
        job: convex-logs
    relabel_configs:
      - source_labels: ['__syslog_message_app_name']
        target_label: 'app'
```

Or query services programmatically:

```go
// Log collector discovers syslog endpoints
services, _ := platform.DiscoverServicesByTag(ctx, "syslog")
for _, svc := range services {
    if svc.Health == HealthPassing {
        collector.AddSyslogTarget(svc.Address, svc.Port, svc.Metadata)
    }
}
```

### Service Health States

Services have health states that reflect the node's role:

| Role | `http` Service | `metrics` Service | `syslog` Service |
|------|----------------|-------------------|------------------|
| PRIMARY | `passing` | `passing` | `passing` |
| PASSIVE | `standby` | `passing` | `passing` |

- `http` service is only `passing` on PRIMARY (traffic should go to VIP anyway)
- `metrics` and `syslog` are `passing` on all nodes (collect from all)

---

## Monitoring

### Prometheus Metrics

The cluster manager exposes metrics at `:9090/metrics`:

```
# Leadership
convex_cluster_is_leader{cluster="convex-prod",node="node-1"} 1
convex_cluster_leader_epoch{cluster="convex-prod"} 42

# Backend
convex_cluster_backend_status{cluster="convex-prod",node="node-1",status="running"} 1
convex_cluster_backend_uptime_seconds{cluster="convex-prod",node="node-1"} 3600

# Replication
convex_cluster_replication_lag_ms{cluster="convex-prod",node="node-2"} 50
convex_cluster_wal_bytes_total{cluster="convex-prod",node="node-1"} 1048576

# VIP
convex_cluster_vip_held{cluster="convex-prod",node="node-1"} 1

# Service Discovery
convex_cluster_services_total{cluster="convex-prod",service="http"} 2
convex_cluster_services_healthy{cluster="convex-prod",service="http"} 1
convex_cluster_services_healthy{cluster="convex-prod",service="metrics"} 2
```

### Health Endpoints

- `GET :8080/health` - Liveness probe
- `GET :8080/ready` - Readiness probe (true when backend is healthy)
- `GET :8080/status` - Detailed JSON status
- `GET :8080/services` - Registered services and their health

---

## Troubleshooting

### Common Issues

**Backend not starting:**
```bash
# Check backend service status
systemctl status convex-backend

# Check cluster manager logs
journalctl -u convex-cluster-manager -f

# Verify database path permissions
ls -la /var/lib/convex/
```

**WAL mode not enabled:**
```bash
# Check database journal mode
sqlite3 /var/lib/convex/data.db "PRAGMA journal_mode;"

# Should output: wal
# If not, the convex-backend fork with WAL support is required
```

**VIP not moving:**
```bash
# Check if process has CAP_NET_ADMIN
getcap /usr/bin/convex-cluster-manager

# Or run as root
# Check VIP status
ip addr show eth0 | grep 10.0.1.100
```

**Replication lag increasing:**
```bash
# Check NATS connectivity
nats server check jetstream

# Check Object Store bucket
nats object ls convex-convex-prod-wal
```
