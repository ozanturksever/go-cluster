# Basic Active-Passive Example

This example demonstrates the simplest use of go-cluster: an active-passive pattern with automatic leader election.

## What This Demonstrates

- Creating a singleton app (exactly one active instance)
- Using lifecycle hooks (`OnActive`, `OnPassive`, `OnLeaderChange`)
- Automatic leader election via NATS KV
- Graceful failover when the leader stops

## Prerequisites

1. **NATS Server with JetStream enabled:**
   ```bash
   # Install NATS server
   brew install nats-server  # macOS
   # or download from https://nats.io/download/

   # Start with JetStream
   nats-server -js
   ```

## Running the Example

1. **Start the first node (will become leader):**
   ```bash
   go run main.go -node node-1
   ```

2. **In another terminal, start the second node (standby):**
   ```bash
   go run main.go -node node-2
   ```

3. **In a third terminal, start the third node (standby):**
   ```bash
   go run main.go -node node-3
   ```

## Testing Failover

1. Kill the leader (the first node you started) with `Ctrl+C`
2. Watch one of the standby nodes automatically become the new leader
3. The remaining node stays as standby

## Expected Output

**Node 1 (becomes leader first):**
```
Starting basic active-passive example
  Node ID: node-1
  NATS: nats://localhost:4222
🟢 I am now the LEADER!
   Starting to process work...
📢 Leader changed to: node-1
Node started. Press Ctrl+C to stop.
   [Leader] Doing important work...
```

**Node 2 (standby):**
```
Starting basic active-passive example
  Node ID: node-2
  NATS: nats://localhost:4222
🔵 I am now STANDBY
   Waiting for failover...
📢 Leader changed to: node-1
Node started. Press Ctrl+C to stop.
   [Standby] Ready and waiting...
```

**After killing node-1, node-2 output:**
```
📢 Leader changed to: node-2
🟢 I am now the LEADER!
   Starting to process work...
   [Leader] Doing important work...
```

## Key Concepts

### Singleton Mode
```go
app := cluster.NewApp("my-service",
    cluster.Singleton(), // Only one active instance
)
```

In singleton mode:
- Exactly one instance is active (leader)
- Other instances are standby (passive)
- Automatic failover on leader failure

### Lifecycle Hooks

| Hook | When Called |
|------|-------------|
| `OnActive` | This node became the leader |
| `OnPassive` | This node became a standby |
| `OnLeaderChange` | Any leadership change in the cluster |

## Next Steps

- See `examples/distributed-cache/` for a more complex Ring pattern example
- See `examples/convex-backend/` for SQLite replication and VIP failover
- See `examples/api-gateway/` for a load-balanced pool pattern
