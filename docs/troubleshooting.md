# Troubleshooting Guide

This guide covers common issues and their solutions when using go-cluster.

## Connection Issues

### "Failed to connect to NATS"

**Symptoms:**
```
failed to connect to NATS: nats: no servers available for connection
```

**Causes & Solutions:**

1. **NATS server not running**
   ```bash
   # Check if NATS is running
   pgrep nats-server
   
   # Start NATS with JetStream
   nats-server -js
   ```

2. **Wrong URL**
   ```bash
   # Default is nats://localhost:4222
   go-cluster run --nats nats://your-server:4222
   ```

3. **Firewall blocking connection**
   ```bash
   # Check port is accessible
   nc -zv localhost 4222
   ```

4. **Authentication required**
   ```go
   platform, err := cluster.NewPlatform("prod", "node-1", natsURL,
       cluster.NATSCreds("/path/to/creds.creds"),
   )
   ```

### "JetStream not available"

**Symptoms:**
```
failed to create JetStream context: nats: JetStream not enabled
```

**Solution:**

Start NATS with JetStream enabled:
```bash
# Command line
nats-server -js

# Or in config file (nats.conf)
jetstream {
    store_dir: /var/nats/data
    max_mem: 1G
    max_file: 10G
}
```

## Leader Election Issues

### No leader elected

**Symptoms:**
- All nodes remain in standby
- `IsLeader()` always returns false

**Causes & Solutions:**

1. **JetStream streams not created**
   
   go-cluster automatically creates KV buckets, but permission issues might prevent this:
   ```bash
   # Check existing KV buckets
   nats kv ls
   
   # Manually create if needed
   nats kv add PLATFORM_APP_election
   ```

2. **TTL expiry waiting**
   
   If a previous leader crashed, wait for TTL to expire (default 10 seconds).

3. **Network partition**
   
   Ensure all nodes can reach NATS server.

### Election stuck / zombie leader

**Symptoms:**
- Old leader appears active but isn't responding
- New leader won't take over

**Solution:**

1. Wait for lease TTL to expire (default 10 seconds)

2. Force release by deleting the election key:
   ```bash
   nats kv del PLATFORM_APP_election leader
   ```

3. Reduce TTL for faster failover:
   ```go
   cluster.WithLeaseTTL(5 * time.Second)
   ```

### Leader keeps changing

**Symptoms:**
- Frequent leader elections
- "Leader flapping" in logs

**Causes & Solutions:**

1. **Network instability**
   - Check network latency to NATS
   - Increase heartbeat interval:
     ```go
     cluster.WithHeartbeat(5 * time.Second)
     ```

2. **NATS server overloaded**
   - Check NATS server metrics
   - Scale NATS cluster if needed

3. **Node resource exhaustion**
   - Check CPU/memory usage
   - Increase hook timeout:
     ```go
     cluster.WithHookTimeout(60 * time.Second)
     ```

## Data Replication Issues

### WAL replication lag

**Symptoms:**
- Standbys are behind primary
- `app.WAL().Lag()` shows high value

**Causes & Solutions:**

1. **High write volume**
   - Increase WAL segment size
   - Use faster storage

2. **Network bandwidth**
   - Check network between nodes and NATS
   - Use NATS Object Store with compression

3. **NATS storage full**
   ```bash
   # Check JetStream usage
   nats server report jetstream
   
   # Increase limits in NATS config
   jetstream {
       max_file: 100G
   }
   ```

### Database corruption after failover

**Symptoms:**
- SQLite errors after becoming leader
- "database disk image is malformed"

**Prevention:**

1. Always use WAL mode:
   ```sql
   PRAGMA journal_mode=WAL;
   ```

2. Ensure proper shutdown:
   - Use graceful shutdown with `context.Cancel()`
   - Allow time for WAL sync

3. Use snapshots for recovery:
   ```go
   // Restore from last good snapshot
   snapshot, _ := app.Snapshots().Latest(ctx)
   app.Snapshots().Restore(ctx, snapshot.ID)
   ```

## Service Discovery Issues

### Services not being discovered

**Symptoms:**
- `DiscoverServices()` returns empty
- Services registered but not visible

**Causes & Solutions:**

1. **Service not exposed**
   ```go
   // Make sure to expose the service
   app.ExposeService("http", cluster.ServiceConfig{
       Port:     8080,
       Protocol: "http",
   })
   ```

2. **Health check failing**
   ```bash
   # Check health endpoint
   curl http://localhost:8080/health
   ```

3. **Different platform names**
   - Ensure all nodes use the same platform name

### Stale service entries

**Symptoms:**
- Old services still appearing in discovery
- Dead nodes listed as healthy

**Solution:**

Service entries have TTL and should expire. If not:

1. Check node is properly deregistering on shutdown
2. Manually clean up if needed:
   ```bash
   nats kv del PLATFORM_services "app/service/nodeID"
   ```

## Performance Issues

### High latency for RPC calls

**Causes & Solutions:**

1. **Large payloads**
   - Compress large messages
   - Use streaming for big data

2. **NATS server location**
   - Use NATS cluster closer to nodes
   - Consider leaf nodes for multi-region

3. **Too many retries**
   ```go
   // Reduce retry attempts
   cluster.Call("app").Call(ctx, "method", data,
       cluster.WithRetry(2),
       cluster.WithTimeout(5*time.Second),
   )
   ```

### High memory usage

**Causes & Solutions:**

1. **Large KV values**
   - Use Object Store for large data
   - Implement pagination

2. **Event subscribers not unsubscribing**
   ```go
   // Always unsubscribe when done
   defer app.Events().Unsubscribe("subject")
   ```

3. **Ring partition data accumulation**
   - Implement proper OnRelease handler to clean up

## VIP Issues

### VIP not being acquired

**Symptoms:**
- Leader elected but VIP not responding
- "acquire_error" in audit logs

**Causes & Solutions:**

1. **Insufficient permissions**
   ```bash
   # VIP management requires root or CAP_NET_ADMIN
   sudo setcap cap_net_admin+ep ./myapp
   # Or run as root
   ```

2. **Wrong interface**
   ```go
   // Make sure interface exists
   cluster.VIP("10.0.1.100/24", "eth0")  // Check `ip link`
   ```

3. **IP already in use**
   ```bash
   # Check if IP is assigned elsewhere
   ping -c 1 10.0.1.100
   ```

### VIP not releasing on failover

**Symptoms:**
- Both old and new leader have VIP
- "duplicate IP" errors

**Solution:**

1. Ensure graceful shutdown releases VIP
2. Implement manual cleanup:
   ```bash
   # On the stuck node
   ip addr del 10.0.1.100/24 dev eth0
   ```

## Backend Issues

### Systemd service won't start

**Symptoms:**
- Backend reports "start_error"
- Service not running after becoming leader

**Causes & Solutions:**

1. **Service doesn't exist**
   ```bash
   systemctl status myservice
   ```

2. **Permission issues**
   ```bash
   # go-cluster needs permission to manage services
   sudo usermod -aG systemd-journal $USER
   ```

3. **Dependencies not met**
   ```bash
   # Check service dependencies
   systemctl list-dependencies myservice
   ```

### Docker container fails to start

**Symptoms:**
- Docker backend reports error
- Container exits immediately

**Debug:**
```bash
# Check container logs
docker logs myapp-container

# Try running manually
docker run --rm myapp:latest
```

## Debugging Tips

### Enable verbose logging

```bash
go-cluster run --verbose --platform myapp
```

### Check NATS state

```bash
# List all KV buckets
nats kv ls

# Check specific bucket
nats kv get MYPLATFORM_MYAPP_election leader

# List all streams
nats stream ls

# Check stream messages
nats stream view MYPLATFORM_audit
```

### Inspect metrics

```bash
# Check Prometheus metrics
curl http://localhost:9090/metrics | grep gc_
```

### Health check

```bash
# Detailed health status
curl http://localhost:8080/health/checks | jq
```

### Audit log

```bash
# View recent audit events
nats stream view MYPLATFORM_audit --last 20
```

## Getting Help

If you're still stuck:

1. Check the [documentation](./)
2. Search [existing issues](https://github.com/ozanturksever/go-cluster/issues)
3. Open a new issue with:
   - go-cluster version
   - Go version
   - NATS version
   - OS and architecture
   - Relevant logs
   - Minimal reproduction
