# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.2.0] - 2024-12-31

### ⚠️ Breaking Changes

- **Configuration**: `RenewInterval` renamed to `HeartbeatInterval`
- **Hooks interface**: Added required methods `OnNATSReconnect(ctx)` and `OnNATSDisconnect(ctx, err)`
- **Health package removed**: Use NATS micro service endpoints instead
- **Faster failover**: Election now uses KV Watch (reactive) instead of polling

### Added

- **KV Watch-based election**: Reactive leader election using NATS KV Watch for sub-500ms failover on graceful stepdown
- **NATS Micro Service**: Each node registers as a micro service with status, ping, and stepdown endpoints
- **Service discovery**: Use `nats micro ls/info/stats` to discover and monitor cluster nodes
- **Connection resilience**: Automatic NATS reconnection with configurable retry settings
- **New hooks**:
  - `OnNATSReconnect(ctx)` - Called when NATS connection is restored
  - `OnNATSDisconnect(ctx, err)` - Called when NATS connection is lost
- **New configuration options**:
  - `HeartbeatInterval` - Interval for leader heartbeat (replaces `RenewInterval`)
  - `ReconnectWait` - Wait duration between NATS reconnection attempts
  - `MaxReconnects` - Maximum reconnection attempts (-1 for unlimited)
  - `ServiceVersion` - Version string for micro service registration
- **Helper functions**: `StatusSubject()`, `PingSubject()`, `StepdownSubject()` for building NATS subjects
- **Validation scripts**:
  - `scripts/validate-election.sh` - Test leader election behavior
  - `scripts/validate-discovery.sh` - Test service discovery
  - `scripts/validate-reconnection.sh` - Test NATS reconnection
  - `scripts/benchmark-failover.sh` - Measure failover time
- **E2E tests**: Comprehensive end-to-end tests including NATS reconnection test

### Changed

- Election mechanism changed from polling to KV Watch for faster failover
- Lease structure simplified: removed `ExpiresAt` field (NATS KV `MaxAge` handles TTL)
- Service name format uses underscore: `cluster_<clusterID>` (NATS micro service naming requirement)
- KV bucket naming: `KV_CLUSTER_<clusterID>`
- Default ping interval set to 2 seconds for faster disconnect detection

### Removed

- **`health` package**: Replaced by NATS micro service endpoints
  - `health.Checker` - Use `nats request cluster_<cid>.ping.<nid>` instead
  - `health.QueryNode()` - Use NATS request/reply pattern
- **`RenewInterval` config option**: Renamed to `HeartbeatInterval`

### Migration Guide

1. Update configuration:
   ```go
   // Before
   RenewInterval: 3 * time.Second,
   // After
   HeartbeatInterval: 3 * time.Second,
   ```

2. Implement new hooks:
   ```go
   func (h *MyHooks) OnNATSReconnect(ctx context.Context) error {
       return nil
   }
   func (h *MyHooks) OnNATSDisconnect(ctx context.Context, err error) error {
       return nil
   }
   ```

3. Replace health checker:
   ```go
   // Before
   checker.QueryNode(ctx, "node-2", timeout)
   // After
   nc.Request(cluster.PingSubject("my-cluster", "node-2"), nil, timeout)
   ```

## [0.1.0] - 2024-01-15

### Added

- Core leader election using NATS JetStream KV Store
- Epoch-based fencing to prevent split-brain scenarios
- `Node` type for participating in cluster coordination
- `Manager` type for high-level cluster management
- `Hooks` interface for reacting to cluster state changes
- `ManagerHooks` interface with additional daemon lifecycle hooks
- Graceful stepdown with cooldown period
- File-based configuration loading (`FileConfig`)
- Optional health checker (`health` package) using NATS request/reply
- Optional VIP manager (`vip` package) for Linux systems
- Comprehensive test suite with unit and integration tests
- Basic usage example in `examples/basic/`

### Configuration Options

- `ClusterID` - Cluster identifier
- `NodeID` - Unique node identifier
- `NATSURLs` - NATS server URLs
- `NATSCredentials` - Optional NATS credentials file
- `LeaseTTL` - Lease validity duration (default: 10s)
- `RenewInterval` - Lease renewal interval (default: 3s)
- VIP settings (address, netmask, interface)

[Unreleased]: https://github.com/ozanturksever/go-cluster/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/ozanturksever/go-cluster/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/ozanturksever/go-cluster/releases/tag/v0.1.0
