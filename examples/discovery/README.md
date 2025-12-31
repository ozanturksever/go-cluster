# go-cluster Service Discovery Example

This example demonstrates how to discover and interact with go-cluster nodes using NATS micro service features.

## Features Demonstrated

1. **Query Node Status** - Get detailed status of a specific node
2. **Ping Nodes** - Health check nodes with latency measurement
3. **Service Discovery** - Discover all cluster nodes via `$SRV.INFO`
4. **Remote Stepdown** - Trigger leadership stepdown via NATS request

## Prerequisites

- NATS server with JetStream enabled running (default: `nats://localhost:4222`)
- At least one go-cluster node running

Start NATS with Docker:

```bash
docker run -d --name nats -p 4222:4222 nats:latest -js
```

## Usage

1. Start a cluster node in one terminal:

```bash
cd examples/basic
NODE_ID=node1 CLUSTER_ID=demo-cluster go run .
```

2. Run the discovery example in another terminal:

```bash
cd examples/discovery
go run .
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `CLUSTER_ID` | `demo-cluster` | Cluster ID to query |
| `NATS_URL` | `nats://localhost:4222` | NATS server URL |

## NATS Subject Patterns

The go-cluster library uses the following subject patterns:

| Endpoint | Subject Pattern | Description |
|----------|-----------------|-------------|
| Status | `cluster_<clusterID>.status.<nodeID>` | Query node status |
| Ping | `cluster_<clusterID>.ping.<nodeID>` | Health check |
| Stepdown | `cluster_<clusterID>.control.<nodeID>.stepdown` | Trigger stepdown |

## Service Discovery via NATS CLI

You can also use the NATS CLI for service discovery:

```bash
# List all micro services
nats micro ls

# Get info about cluster services
nats micro info cluster_demo-cluster

# Get stats
nats micro stats cluster_demo-cluster

# Ping all instances
nats micro ping cluster_demo-cluster

# Direct request to a node
nats request cluster_demo-cluster.status.node1 ''
nats request cluster_demo-cluster.ping.node1 ''
```

## Example Output

```
Connected to NATS at nats://localhost:4222
Cluster ID: demo-cluster

=== Example 1: Query Node Status ===
Querying status: cluster_demo-cluster.status.node1
Response:
  Node ID:   node1
  Role:      PRIMARY
  Leader:    node1
  Epoch:     1
  Uptime:    5234ms
  Connected: true

=== Example 2: Ping Node ===
Pinging node: cluster_demo-cluster.ping.node1
Response:
  OK:        true
  Timestamp: 1704067200000
  Latency:   1.234ms

=== Example 3: Discover Services via $SRV.INFO ===
Discovering services: $SRV.INFO.cluster_demo-cluster
Waiting for responses...

Service 1:
{"name":"cluster_demo-cluster","id":"...","version":"1.0.0",...}
```
