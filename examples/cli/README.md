# go-cluster CLI Example

A command-line interface for managing go-cluster nodes. This example demonstrates how to use the go-cluster library to build a CLI tool for cluster management.

## Building

```bash
cd examples/cli
go build -o go-cluster-cli .
```

To set a version at build time:

```bash
go build -ldflags "-X main.version=1.0.0" -o go-cluster-cli .
```

## Prerequisites

- NATS server with JetStream enabled running (default: `nats://localhost:4222`)

Start NATS with Docker:

```bash
docker run -d --name nats -p 4222:4222 nats:latest -js
```

## Commands

### init

Initialize a new cluster configuration file.

```bash
go-cluster-cli init --cluster-id my-cluster --node-id node1 --config cluster.json
```

Options:
- `--config` - Path to configuration file (default: `cluster.json`)
- `--cluster-id` - Cluster identifier (required)
- `--node-id` - Node identifier (required)
- `--nats-url` - NATS server URL (default: `nats://localhost:4222`)
- `--vip-address` - Virtual IP address (optional)
- `--vip-netmask` - Virtual IP netmask (default: 24)
- `--vip-interface` - Network interface for VIP

### daemon

Run the cluster daemon. This is the main operating mode where the node participates in leader election.

```bash
go-cluster-cli daemon --config cluster.json
```

Options:
- `--config` - Path to configuration file (default: `cluster.json`)
- `--log-level` - Log level: debug, info, warn, error (default: `info`)

The daemon will:
- Connect to the NATS cluster
- Participate in leader election
- Manage VIP (if configured)
- Handle graceful shutdown on SIGINT/SIGTERM

### status

Show the current node status.

```bash
go-cluster-cli status --config cluster.json
```

Options:
- `--config` - Path to configuration file (default: `cluster.json`)
- `--json` - Output in JSON format

Example output:

```
Cluster:   my-cluster
Node:      node1
Role:      PRIMARY
Connected: true
Leader:    node1
Epoch:     1
VIP Held:  false
```

### watch

Continuously watch cluster status with automatic refresh.

```bash
go-cluster-cli watch --config cluster.json
```

Options:
- `--config` - Path to configuration file (default: `cluster.json`)
- `--interval` - Refresh interval (default: `2s`)
- `--json` - Output in JSON format

Example with custom interval:

```bash
go-cluster-cli watch --config cluster.json --interval 5s
```

The display clears and refreshes at each interval. Press Ctrl+C to stop watching.

### promote

Attempt to become the cluster leader.

```bash
go-cluster-cli promote --config cluster.json
```

This command blocks until this node becomes the leader or another node already holds leadership.

### demote

Voluntarily step down from leadership.

```bash
go-cluster-cli demote --config cluster.json
```

If this node is the current leader, it will release leadership, allowing another node to take over.

### join

Join the cluster and detect the current leader.

```bash
go-cluster-cli join --config cluster.json
```

This is a quick connectivity check that verifies the node can connect to the cluster.

### leave

Gracefully leave the cluster.

```bash
go-cluster-cli leave --config cluster.json
```

If this node is the leader, it will step down first before leaving.

### version

Print version information.

```bash
go-cluster-cli version
```

### help

Show help message.

```bash
go-cluster-cli help
go-cluster-cli <command> -h
```

## Quick Start

1. Start NATS:

```bash
docker run -d --name nats -p 4222:4222 nats:latest -js
```

2. Initialize configuration for two nodes:

```bash
# Terminal 1
go-cluster-cli init --cluster-id demo --node-id node1 --config node1.json

# Terminal 2
go-cluster-cli init --cluster-id demo --node-id node2 --config node2.json
```

3. Start both daemons:

```bash
# Terminal 1
go-cluster-cli daemon --config node1.json --log-level debug

# Terminal 2
go-cluster-cli daemon --config node2.json --log-level debug
```

4. Check status from another terminal:

```bash
go-cluster-cli status --config node1.json
```

5. Test failover by stopping the leader (Ctrl+C) and observe the other node becoming the new leader.

## Configuration File Format

The configuration file is JSON format:

```json
{
  "clusterId": "my-cluster",
  "nodeId": "node1",
  "nats": {
    "servers": ["nats://localhost:4222"],
    "credentials": ""
  },
  "vip": {
    "address": "",
    "netmask": 24,
    "interface": ""
  },
  "election": {
    "leaseTtlMs": 10000,
    "heartbeatIntervalMs": 3000
  }
}
```

### VIP Configuration

To enable Virtual IP failover (Linux only), configure the VIP section:

```json
{
  "vip": {
    "address": "192.168.1.100",
    "netmask": 24,
    "interface": "eth0"
  }
}
```

The leader will acquire the VIP, and it will be released when leadership is lost.

## Signals

The daemon handles the following signals:
- `SIGINT` (Ctrl+C) - Graceful shutdown
- `SIGTERM` - Graceful shutdown

## Exit Codes

- `0` - Success
- `1` - Error (configuration, connection, or operation failure)
