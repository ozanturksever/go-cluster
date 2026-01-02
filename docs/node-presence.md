# Node Presence & Duplicate Detection

This document explains how go-cluster handles node identity, duplicate node detection, and graceful rejoin scenarios.

## Overview

go-cluster uses a membership system to track which nodes are active in the cluster. Each node is identified by a unique `nodeID` that you provide when creating a platform. The system automatically:

- **Detects duplicate nodes** - Prevents two nodes with the same ID from running simultaneously
- **Allows graceful rejoin** - Permits a node to rejoin after proper shutdown
- **Handles stale entries** - Allows takeover of entries from crashed nodes

## Node Identity

Every node in a go-cluster platform has a unique identifier:

```go
platform, err := cluster.NewPlatform("my-cluster", "node-1", natsURL)
//                                    ^platform    ^nodeID
```

**Important:** The `nodeID` must be unique within a platform. Using the same `nodeID` for multiple running nodes will result in an error.

### Choosing Node IDs

Good practices for node IDs:

```go
// Use hostname
nodeID := os.Hostname()

// Use hostname + process ID (for multiple instances per host)
nodeID := fmt.Sprintf("%s-%d", hostname, os.Getpid())

// Use container ID (for containerized deployments)
nodeID := os.Getenv("HOSTNAME")  // Kubernetes pod name

// Use instance ID (for cloud deployments)
nodeID := getEC2InstanceID()
```

## Duplicate Node Detection

### How It Works

When a node starts, go-cluster:

1. **Attempts atomic registration** - Uses NATS KV's `Create()` operation to atomically register the node
2. **Checks for existing entry** - If registration fails, checks if an existing node with the same ID is active
3. **Determines freshness** - An existing entry is considered "active" if it was updated within `2 × heartbeat interval`
4. **Rejects or allows** - Rejects if active duplicate exists; allows if entry is stale

### Behavior Scenarios

| Scenario | Result |
|----------|--------|
| New node, no existing entry | ✅ Registration succeeds |
| Same ID, existing node active | ❌ `ErrNodeAlreadyPresent` |
| Same ID, existing node gracefully stopped | ✅ Registration succeeds |
| Same ID, existing node crashed (entry stale) | ✅ Registration succeeds |
| Different ID, other nodes present | ✅ Registration succeeds |

### Example: Handling Duplicate Detection

```go
platform, err := cluster.NewPlatform("my-cluster", "node-1", natsURL)
if err != nil {
    log.Fatal(err)
}

app := cluster.NewApp("myapp", cluster.Singleton())
platform.Register(app)

err = platform.Run(ctx)
if errors.Is(err, cluster.ErrNodeAlreadyPresent) {
    log.Fatal("Another node with the same ID is already running!")
}
```

## Graceful Shutdown & Rejoin

### Graceful Shutdown

When a node shuts down gracefully (via context cancellation), it:

1. Deregisters from the membership KV
2. Releases any held leadership
3. Stops all services
4. Closes NATS connection

```go
ctx, cancel := context.WithCancel(context.Background())

// Handle shutdown signal
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
go func() {
    <-sigCh
    cancel()  // Triggers graceful shutdown
}()

platform.Run(ctx)
```

### Rejoining After Graceful Shutdown

After a graceful shutdown, a node can immediately rejoin with the same `nodeID`:

```go
// First instance runs and shuts down gracefully
platform1.Run(ctx1)  // cancel ctx1 to stop

// Same node ID can rejoin immediately
platform2, _ := cluster.NewPlatform("my-cluster", "node-1", natsURL)
platform2.Run(ctx2)  // Works immediately
```

### Rejoining After Crash

If a node crashes without graceful shutdown, the membership entry becomes stale after `2 × heartbeat interval`:

```go
// With default heartbeat of 3 seconds:
// - Entry becomes stale after ~6 seconds
// - A new node with same ID can join after this period

// To speed up recovery, use shorter heartbeat:
cluster.WithHeartbeat(500 * time.Millisecond)
// Entry becomes stale in ~1 second
```

## Configuration

### Timing Configuration

```go
platform, err := cluster.NewPlatform("my-cluster", "node-1", natsURL,
    // Heartbeat interval - affects how often membership is updated
    cluster.WithHeartbeat(3 * time.Second),  // Default
    
    // Lease TTL - affects leader election timeout
    cluster.WithLeaseTTL(10 * time.Second),  // Default
)
```

### Freshness Threshold

The freshness threshold for duplicate detection is calculated as:

```
threshold = max(2 × heartbeat, 5 seconds)
```

This means:
- With 3s heartbeat: threshold is 6 seconds
- With 500ms heartbeat: threshold is 5 seconds (minimum)
- With 10s heartbeat: threshold is 20 seconds

## Error Handling

### ErrNodeAlreadyPresent

This error is returned when attempting to start a node with an ID that's already active:

```go
err := platform.Run(ctx)
if errors.Is(err, cluster.ErrNodeAlreadyPresent) {
    // Options:
    // 1. Use a different node ID
    // 2. Wait for the other node to stop
    // 3. Check if it's actually a different process
    log.Fatalf("Node %s is already running in the cluster", nodeID)
}
```

### Concurrent Startup

If multiple processes try to start with the same node ID simultaneously, only one will succeed:

```go
// Process A and Process B both start at the same time with "node-1"
// Result: One succeeds, one gets ErrNodeAlreadyPresent

// The atomic Create() operation ensures only one wins the race
```

## Membership Tracking

### Viewing Members

```go
// Get all members in the cluster
members := platform.Membership().Members()
for _, m := range members {
    fmt.Printf("Node: %s, Joined: %s, LastSeen: %s\n", 
        m.NodeID, m.JoinedAt, m.LastSeen)
}

// Get specific member
member, found := platform.Membership().GetMember("node-1")

// Get member count
count := platform.Membership().MemberCount()
```

### Member Information

Each member record contains:

```go
type Member struct {
    NodeID    string            // Unique node identifier
    Platform  string            // Platform name
    Address   string            // Node address (if configured)
    Labels    map[string]string // Node labels
    JoinedAt  time.Time         // When the node first joined
    LastSeen  time.Time         // Last heartbeat time
    Apps      []string          // Registered apps on this node
    IsHealthy bool              // Health status
}
```

### Member Events

You can react to membership changes:

```go
app.OnMemberJoin(func(ctx context.Context, member cluster.Member) error {
    log.Printf("Node %s joined the cluster", member.NodeID)
    return nil
})

app.OnMemberLeave(func(ctx context.Context, member cluster.Member) error {
    log.Printf("Node %s left the cluster", member.NodeID)
    return nil
})
```

## Best Practices

### 1. Use Unique Node IDs

```go
// DON'T: Hardcoded node ID
platform, _ := cluster.NewPlatform("prod", "node-1", natsURL)

// DO: Dynamic node ID based on environment
nodeID := os.Getenv("NODE_ID")
if nodeID == "" {
    nodeID, _ = os.Hostname()
}
platform, _ := cluster.NewPlatform("prod", nodeID, natsURL)
```

### 2. Handle Startup Errors

```go
platform, err := cluster.NewPlatform("prod", nodeID, natsURL)
if err != nil {
    log.Fatalf("Failed to create platform: %v", err)
}

if err := platform.Run(ctx); err != nil {
    if errors.Is(err, cluster.ErrNodeAlreadyPresent) {
        log.Fatalf("Duplicate node detected! Node ID '%s' is already active", nodeID)
    }
    log.Fatalf("Platform error: %v", err)
}
```

### 3. Implement Graceful Shutdown

```go
ctx, cancel := context.WithCancel(context.Background())

// Setup signal handling
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

go func() {
    sig := <-sigCh
    log.Printf("Received %s, initiating graceful shutdown...", sig)
    cancel()
}()

// Run with graceful shutdown
if err := platform.Run(ctx); err != nil && err != context.Canceled {
    log.Fatalf("Platform error: %v", err)
}
log.Println("Shutdown complete")
```

### 4. Configure Appropriate Timeouts

```go
// For fast failover (requires reliable network)
cluster.WithHeartbeat(500 * time.Millisecond)
cluster.WithLeaseTTL(2 * time.Second)

// For unreliable networks
cluster.WithHeartbeat(5 * time.Second)
cluster.WithLeaseTTL(15 * time.Second)
```

## Troubleshooting

### "Node already present" on restart

**Cause:** Previous instance didn't shut down gracefully.

**Solutions:**
1. Wait for the stale entry to expire (2 × heartbeat interval)
2. Check if another process is actually running with the same ID
3. Use a different node ID

### Node not appearing in membership

**Cause:** Registration failed silently or node is in startup phase.

**Solutions:**
1. Check for errors from `platform.Run()`
2. Wait for startup to complete (membership registers during platform startup)
3. Verify NATS connectivity

### Membership shows stale entries

**Cause:** Nodes crashed without graceful shutdown.

**Solution:** Stale entries are automatically cleaned up by the KV TTL (default 30 seconds). For faster cleanup, manually delete:

```bash
nats kv del PLATFORM_membership nodeID
```

## Validation Script

Run the node presence validation to test these features:

```bash
./scripts/validate-node-presence.sh
```

This validates:
- Duplicate node detection
- Graceful rejoin after shutdown
- Rejoin after stale entry expiry
- Concurrent start handling
- Multi-node cluster formation
- Failover and rejoin scenarios
