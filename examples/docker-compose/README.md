# go-cluster Docker Compose Example

This example demonstrates a production-like multi-node go-cluster deployment using Docker Compose.

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                        Docker Network                            │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────────── NATS Cluster ───────────────────┐         │
│  │                                                     │         │
│  │   ┌─────────┐   ┌─────────┐   ┌─────────┐         │         │
│  │   │ nats-1  │◄─►│ nats-2  │◄─►│ nats-3  │         │         │
│  │   │ :4222   │   │ :4223   │   │ :4224   │         │         │
│  │   └────┬────┘   └────┬────┘   └────┬────┘         │         │
│  │        │             │             │               │         │
│  └────────┼─────────────┼─────────────┼───────────────┘         │
│           │             │             │                          │
│  ┌────────┴─────────────┴─────────────┴───────────────┐         │
│  │                                                     │         │
│  │              go-cluster Nodes                       │         │
│  │                                                     │         │
│  │   ┌─────────┐   ┌─────────┐   ┌─────────┐         │         │
│  │   │ node-1  │   │ node-2  │   │ node-3  │         │         │
│  │   │ :8081   │   │ :8082   │   │ :8083   │         │         │
│  │   │(Leader) │   │(Standby)│   │(Standby)│         │         │
│  │   └─────────┘   └─────────┘   └─────────┘         │         │
│  │                                                     │         │
│  └─────────────────────────────────────────────────────┘         │
│                                                                  │
│  ┌─────────────────── Monitoring (Optional) ──────────┐         │
│  │                                                     │         │
│  │   ┌─────────────┐      ┌─────────────┐            │         │
│  │   │ Prometheus  │──────│  Grafana    │            │         │
│  │   │ :9090       │      │  :3000      │            │         │
│  │   └─────────────┘      └─────────────┘            │         │
│  │                                                     │         │
│  └─────────────────────────────────────────────────────┘         │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

## Quick Start

### 1. Start the Cluster

```bash
# Start NATS cluster and go-cluster nodes
docker compose up -d

# View logs from all nodes
docker compose logs -f node-1 node-2 node-3
```

### 2. Check Status

```bash
# View running containers
docker compose ps

# Check which node is the leader
curl http://localhost:8081/status  # node-1
curl http://localhost:8082/status  # node-2
curl http://localhost:8083/status  # node-3

# Check health endpoints
curl http://localhost:8081/health
```

### 3. Test Failover

```bash
# In one terminal, watch the logs
docker compose logs -f node-1 node-2 node-3

# In another terminal, kill the leader
docker compose stop node-1

# Watch the logs - another node will become leader
# Restart node-1 and watch it become a standby
docker compose start node-1
```

### 4. Cleanup

```bash
# Stop all containers
docker compose down

# Remove volumes too
docker compose down -v
```

## With Monitoring Stack

To include Prometheus and Grafana:

```bash
# Start with monitoring profile
docker compose --profile monitoring up -d

# Access Grafana
open http://localhost:3000
# Login: admin / admin

# Access Prometheus
open http://localhost:9090
```

## Endpoints

| Service | Endpoint | Description |
|---------|----------|-------------|
| node-1 health | http://localhost:8081/health | Health check |
| node-1 metrics | http://localhost:9091/metrics | Prometheus metrics |
| node-2 health | http://localhost:8082/health | Health check |
| node-2 metrics | http://localhost:9092/metrics | Prometheus metrics |
| node-3 health | http://localhost:8083/health | Health check |
| node-3 metrics | http://localhost:9093/metrics | Prometheus metrics |
| NATS-1 | http://localhost:8222 | NATS monitoring |
| NATS-2 | http://localhost:8223 | NATS monitoring |
| NATS-3 | http://localhost:8224 | NATS monitoring |
| Prometheus | http://localhost:9090 | Metrics UI (with --profile monitoring) |
| Grafana | http://localhost:3000 | Dashboards (with --profile monitoring) |

## Configuration

### Environment Variables

Each go-cluster node accepts these environment variables:

| Variable | Description | Default |
|----------|-------------|--------|
| `NODE_ID` | Unique node identifier | `node-1` |
| `NATS_URL` | NATS server URL(s), comma-separated | `nats://localhost:4222` |
| `PLATFORM` | Cluster/platform name | `demo-cluster` |
| `HEALTH_ADDR` | Health check endpoint address | `:8080` |
| `METRICS_ADDR` | Prometheus metrics endpoint | `:9090` |
| `LABELS_*` | Node labels (e.g., `LABELS_ZONE=us-east-1a`) | - |

### Scaling Nodes

To add more nodes, copy a node definition in `docker-compose.yaml`:

```yaml
  node-4:
    build:
      context: ../..
      dockerfile: examples/docker-compose/Dockerfile
    container_name: go-cluster-node-4
    hostname: node-4
    environment:
      - NODE_ID=node-4
      - NATS_URL=nats://nats-1:4222,nats://nats-2:4222,nats://nats-3:4222
      - PLATFORM=demo-cluster
      - HEALTH_ADDR=:8080
      - METRICS_ADDR=:9090
      - LABELS_ZONE=zone-d
    ports:
      - "8084:8080"
      - "9094:9090"
    # ... rest of config
```

## Production Considerations

1. **Use Pre-built Images**: In production, use the pre-built images from `ghcr.io/ozanturksever/go-cluster` instead of building from source.

2. **NATS Security**: Enable TLS and authentication for NATS connections:
   ```yaml
   environment:
     - NATS_URL=nats://user:password@nats-1:4222
     - NATS_CREDS=/secrets/nats.creds
   ```

3. **Resource Limits**: Add resource limits to containers:
   ```yaml
   deploy:
     resources:
       limits:
         cpus: '0.5'
         memory: 256M
   ```

4. **External Volumes**: Use external volumes for data persistence.

5. **Networking**: Use an overlay network for multi-host deployments.

6. **Monitoring**: Always run the monitoring stack in production.

## Troubleshooting

### Nodes can't connect to NATS

```bash
# Check NATS cluster status
docker compose logs nats-1 nats-2 nats-3

# Verify NATS is healthy
curl http://localhost:8222/healthz
```

### Leader election not working

```bash
# Check JetStream is enabled
curl http://localhost:8222/jsz

# Verify all nodes are connected
docker compose logs -f | grep "election"
```

### Container won't start

```bash
# Check build logs
docker compose build --no-cache node-1

# View container logs
docker compose logs node-1
```

## Files

```
examples/docker-compose/
├── docker-compose.yaml    # Main compose file
├── Dockerfile            # Build file for go-cluster nodes
├── main.go               # Example application
├── README.md             # This file
└── config/
    ├── nats.conf         # NATS server configuration
    ├── prometheus.yml    # Prometheus scrape config
    └── grafana/
        └── provisioning/
            └── datasources/
                └── datasources.yml
```
