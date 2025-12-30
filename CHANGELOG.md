# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/ozanturksever/go-cluster/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/ozanturksever/go-cluster/releases/tag/v0.1.0
