# Configuration Reference

This document covers all configuration options available in go-cluster.

## Platform Configuration

### Creating a Platform

```go
platform, err := cluster.NewPlatform(name, nodeID, natsURL, options...)
```

| Parameter | Description |
|-----------|-------------|
| `name` | Platform/cluster name (used for NATS subject prefixes) |
| `nodeID` | Unique identifier for this node |
| `natsURL` | NATS server URL(s), comma-separated for clusters |
| `options` | Variadic platform options |

### Platform Options

#### Labels

Node labels for placement decisions:

```go
cluster.Labels(map[string]string{
    "zone":     "us-east-1a",
    "tier":     "compute",
    "cpu":      "16",
    "memory":   "64Gi",
})
```

#### NATS Credentials

```go
cluster.NATSCreds("/etc/nats/creds")
```

#### Health Check Address

```go
cluster.HealthAddr(":8080")  // Default
cluster.HealthAddr(":0")     // Random port
cluster.HealthAddr("")       // Disable health server
```

#### Metrics Address

```go
cluster.MetricsAddr(":9090")  // Default
cluster.MetricsAddr(":0")     // Random port
cluster.MetricsAddr("")       // Disable metrics server
```

#### Timing Configuration

```go
// Leader lease TTL (how long a leader can be unresponsive)
cluster.WithLeaseTTL(10 * time.Second)  // Default

// Heartbeat interval for health checks
cluster.WithHeartbeat(3 * time.Second)  // Default

// Timeout for lifecycle hooks
cluster.WithHookTimeout(30 * time.Second)  // Default

// Graceful shutdown timeout
cluster.WithShutdownTimeout(30 * time.Second)  // Default
```

#### Auto-Rebalancing

```go
cluster.WithAutoRebalance(cluster.AutoRebalanceConfig{
    Enabled:        true,
    Interval:       5 * time.Minute,
    Threshold:      0.2,   // 20% imbalance triggers rebalance
    MaxConcurrent:  2,     // Max concurrent migrations
    CooldownPeriod: 10 * time.Minute,
    ExcludeApps:    []string{"critical-db"},
})
```

#### Leaf Node Configuration

Configure as a hub (accepts leaf connections):

```go
cluster.WithLeafHub(cluster.LeafHubConfig{
    Port:             7422,
    AuthorizedLeaves: []string{"leaf-1", "leaf-2"},
    TLS:              tlsConfig,
})
```

Configure as a leaf (connects to hub):

```go
cluster.WithLeafConnection(cluster.LeafConnectionConfig{
    HubURLs:       []string{"nats://hub.example.com:7422"},
    PlatformName:  "leaf-platform",
    ReconnectWait: 5 * time.Second,
    MaxReconnects: -1,  // Infinite
})
```

## App Configuration

### Creating an App

```go
app := cluster.NewApp(name, options...)
```

### App Modes

```go
// Singleton - exactly one active instance
cluster.Singleton()

// Spread - run on all eligible nodes
cluster.Spread()

// Replicas - run on N nodes
cluster.Replicas(3)

// Ring - consistent hash ring with N partitions
cluster.Ring(256)
```

### Placement Constraints

#### Node Selection

```go
// Run on specific nodes only
cluster.OnNodes("node-1", "node-2", "node-3")

// Run on nodes with specific label
cluster.OnLabel("zone", "us-east-1a")

// Avoid nodes with specific label
cluster.AvoidLabel("tier", "storage")

// Soft preference for label (uses weight for scoring)
cluster.PreferLabel("zone", "us-east-1a", 10)  // weight 10
```

#### App Affinity

```go
// Co-locate with another app
cluster.WithApp("cache")

// Anti-affinity - avoid nodes running another app
cluster.AwayFromApp("database")
```

#### Resource Requirements

```go
cluster.Resources(cluster.ResourceSpec{
    CPU:    4,
    Memory: "8Gi",
    Disk:   "100Gi",
})
```

### SQLite Configuration

```go
cluster.WithSQLite("/data/app.db",
    // Path for replica database
    cluster.SQLiteReplica("/data/replica.db"),
    
    // How often to create snapshots
    cluster.SQLiteSnapshotInterval(1 * time.Hour),
    
    // How long to retain WAL segments
    cluster.SQLiteRetention(24 * time.Hour),
)
```

### VIP Configuration

```go
// Virtual IP failover
cluster.VIP("10.0.1.100/24", "eth0")
```

### Backend Configuration

#### Systemd Backend

```go
cluster.WithBackend(cluster.Systemd("my-service",
    cluster.SystemdHealthEndpoint("http://localhost:8080/health"),
    cluster.SystemdHealthInterval(10 * time.Second),
    cluster.SystemdHealthTimeout(5 * time.Second),
    cluster.SystemdStartTimeout(60 * time.Second),
    cluster.SystemdStopTimeout(30 * time.Second),
    cluster.SystemdNotify(true),
    cluster.SystemdDependencies("network.target", "nats.service"),
))
```

#### Docker Backend

```go
cluster.WithBackend(cluster.Docker(cluster.DockerConfig{
    Image:       "myapp:latest",
    Name:        "myapp-container",
    Network:     "host",
    Env:         []string{"DB_PATH=/data/app.db"},
    Volumes:     []string{"/data:/data"},
    StopTimeout: 30 * time.Second,
    Resources: &cluster.DockerResources{
        CPUs:   2.0,
        Memory: "4g",
    },
    HealthCheck: &cluster.DockerHealthCheck{
        Cmd:         []string{"curl", "-f", "http://localhost:8080/health"},
        Interval:    10 * time.Second,
        Timeout:     5 * time.Second,
        StartPeriod: 30 * time.Second,
        Retries:     3,
    },
}))
```

#### Process Backend

```go
cluster.WithBackend(cluster.Process(cluster.ProcessConfig{
    Command:         "./myapp",
    Args:            []string{"--config", "/etc/myapp.yaml"},
    Dir:             "/opt/myapp",
    Env:             []string{"LOG_LEVEL=info"},
    GracefulTimeout: 30 * time.Second,
    UseProcessGroup: true,
}))
```

## Service Configuration

### Exposing Services

```go
app.ExposeService("http", cluster.ServiceConfig{
    Port:     8080,
    Protocol: "http",
    Path:     "/api",
    Tags:     []string{"api", "v2"},
    Metadata: map[string]string{
        "version": "2.1.0",
        "region":  "us-east-1",
    },
    HealthCheck: &cluster.HealthCheckConfig{
        Path:           "/health",
        Interval:       10 * time.Second,
        Timeout:        3 * time.Second,
        ExpectedStatus: 200,
    },
    Weight:   100,
    Internal: false,
})
```

### Supported Protocols

- `tcp` (default)
- `udp`
- `http`
- `https`
- `grpc`
- `ws` (WebSocket)
- `wss` (WebSocket Secure)

## CLI Configuration

### Configuration File

Create `~/.go-cluster.yaml` or `/etc/go-cluster/config.yaml`:

```yaml
# NATS connection
nats_url: nats://localhost:4222
nats_creds: /etc/nats/creds

# Node identity
node_id: node-1
platform: production

# Endpoints
health_addr: :8080
metrics_addr: :9090

# Node labels
labels:
  zone: us-east-1a
  tier: compute
  cpu: "16"
  memory: 64Gi
```

### Environment Variables

| Variable | Description |
|----------|-------------|
| `NATS_URL` | NATS server URL |
| `NODE_ID` | Node identifier |
| `PLATFORM` or `CLUSTER_ID` | Platform name |

### CLI Flags

```bash
go-cluster run \
    --nats nats://localhost:4222 \
    --node node-1 \
    --platform production \
    --health-addr :8080 \
    --metrics-addr :9090 \
    --labels zone=us-east-1a,tier=compute
```

## Health Check Configuration

### Custom Health Checks

```go
platform.Health().RegisterCheck("database", func(ctx context.Context) error {
    return db.PingContext(ctx)
}, 
    cluster.WithHealthCheckInterval(10 * time.Second),
    cluster.WithHealthCheckDependsOn("nats"),
)
```

### Health Endpoints

The health server exposes:

- `GET /health` - Overall health status (returns 200 if healthy)
- `GET /ready` - Readiness probe (returns 200 when ready to serve)
- `GET /health/checks` - Detailed health check status

## Metrics Configuration

### Prometheus Metrics

Metrics are automatically exported at the configured metrics address.

#### Available Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `gc_election_is_leader` | Gauge | 1 if this node is leader, 0 otherwise |
| `gc_election_epoch` | Counter | Election epoch number |
| `gc_members_count` | Gauge | Number of members in the cluster |
| `gc_rpc_requests_total` | Counter | Total RPC requests |
| `gc_rpc_latency_seconds` | Histogram | RPC latency distribution |
| `gc_kv_operations_total` | Counter | KV operations count |
| `gc_wal_position` | Gauge | Current WAL position |
| `gc_wal_lag_segments` | Gauge | WAL replication lag |
| `gc_backend_status` | Gauge | Backend service status |

### Example Prometheus Scrape Config

```yaml
scrape_configs:
  - job_name: 'go-cluster'
    static_configs:
      - targets:
        - 'node-1:9090'
        - 'node-2:9090'
        - 'node-3:9090'
```

## Complete Example

```go
package main

import (
    "context"
    "time"

    "github.com/ozanturksever/go-cluster"
)

func main() {
    platform, _ := cluster.NewPlatform("production", "node-1", "nats://localhost:4222",
        cluster.Labels(map[string]string{
            "zone": "us-east-1a",
            "tier": "compute",
        }),
        cluster.HealthAddr(":8080"),
        cluster.MetricsAddr(":9090"),
        cluster.WithLeaseTTL(10*time.Second),
        cluster.WithHookTimeout(30*time.Second),
    )

    app := cluster.NewApp("database",
        cluster.Singleton(),
        cluster.WithSQLite("/data/app.db",
            cluster.SQLiteReplica("/data/replica.db"),
        ),
        cluster.VIP("10.0.1.100/24", "eth0"),
        cluster.WithBackend(cluster.Systemd("my-backend")),
        cluster.OnLabel("tier", "compute"),
    )

    app.ExposeService("http", cluster.ServiceConfig{
        Port:     3000,
        Protocol: "http",
    })

    app.OnActive(func(ctx context.Context) error {
        // Start accepting requests
        return nil
    })

    platform.Register(app)
    platform.Run(context.Background())
}
```
