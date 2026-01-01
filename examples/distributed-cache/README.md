# Distributed Cache Example

This example demonstrates a distributed cache using the **Ring pattern** from go-cluster. The cache uses consistent hashing to shard data across multiple nodes, with automatic rebalancing when nodes join or leave the cluster.

## Features

- **Partition-based data sharding**: Data is distributed across 64 partitions (configurable)
- **Consistent hashing**: Keys map to partitions deterministically
- **Automatic routing**: Requests are routed to the correct node
- **Partition lifecycle hooks**: `OnOwn` and `OnRelease` for data management
- **HTTP API**: Simple REST interface for cache operations
- **Multi-node support**: Run multiple nodes for high availability

## Prerequisites

- Go 1.21+
- NATS server with JetStream enabled

## Running the Example

### 1. Start NATS Server

```bash
# Install NATS if needed
brew install nats-server  # macOS
# or download from https://nats.io/download/

# Start NATS with JetStream
nats-server -js
```

### 2. Start Cache Nodes

Open multiple terminals and start cache nodes:

```bash
# Terminal 1 - Node 1
go run main.go -node node-1 -http :8081

# Terminal 2 - Node 2
go run main.go -node node-2 -http :8082

# Terminal 3 - Node 3
go run main.go -node node-3 -http :8083
```

### 3. Test the Cache

```bash
# Set a value (can use any node - it routes automatically)
curl -X POST http://localhost:8081/set \
  -H "Content-Type: application/json" \
  -d '{"key":"user:1","value":"Alice"}'

# Get the value (from any node)
curl http://localhost:8082/get?key=user:1

# Check which node owns a key
curl http://localhost:8081/owner?key=user:1

# Delete a value
curl -X DELETE http://localhost:8083/delete?key=user:1

# View cache statistics
curl http://localhost:8081/stats
```

## API Endpoints

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/set` | POST | Set a cache value. Body: `{"key": "...", "value": "...", "ttl": 0}` |
| `/get?key=...` | GET | Get a cache value |
| `/delete?key=...` | DELETE | Delete a cache value |
| `/owner?key=...` | GET | Show which node owns a key |
| `/stats` | GET | Show cache and ring statistics |
| `/health` | GET | Health check endpoint |

## Command Line Options

| Flag | Default | Description |
|------|---------|-------------|
| `-node` | `node-1` | Unique node identifier |
| `-nats` | `nats://localhost:4222` | NATS server URL |
| `-http` | `:8080` | HTTP server address |
| `-partitions` | `64` | Number of hash ring partitions |
| `-platform` | `cache-platform` | Platform name |

## How It Works

### Ring Pattern

The Ring pattern uses consistent hashing to distribute data across nodes:

1. **Partitions**: The key space is divided into N partitions (default 64)
2. **Hash Function**: Keys are hashed to determine their partition
3. **Ownership**: Each node owns a subset of partitions
4. **Routing**: Operations are routed to the partition owner

### Partition Lifecycle

When nodes join or leave, partitions are rebalanced:

```go
// Called when this node acquires ownership of partitions
app.OnOwn(func(ctx context.Context, partitions []int) error {
    // Load data for these partitions from storage
    return nil
})

// Called when this node releases ownership of partitions
app.OnRelease(func(ctx context.Context, partitions []int) error {
    // Persist or transfer data before releasing
    return nil
})
```

### Request Routing

Requests automatically route to the correct node:

```go
// Check if this node owns the key
if ring.OwnsKey(key) {
    // Handle locally
    cache.Set(key, value)
} else {
    // Route to the correct node
    targetNode, _ := ring.NodeFor(key)
    rpc.Call(ctx, targetNode, "cache.set", request)
}
```

## Testing Rebalancing

1. Start with 2 nodes and add some data
2. Check partition distribution: `curl http://localhost:8081/stats`
3. Start a 3rd node and watch partitions rebalance
4. Stop a node and watch partitions redistribute

## Production Considerations

- **Persistence**: Add storage backend for data durability
- **Replication**: Consider replicating partitions for fault tolerance
- **Data Transfer**: Implement data transfer during partition ownership changes
- **Monitoring**: Export Prometheus metrics for observability
