# go-cluster Implementation TODO

> **Reference**: See `spec/` folder for detailed specifications
> **Old Implementation**: `/Users/ozant/devel/tries/convex/go-cluster` contains reference code

---

## Current Status

### ✅ Already Implemented (Core Foundation)
- [x] `cluster.NewPlatform()` - Platform creation with NATS connection
- [x] `cluster.NewApp()` - App creation with modes (Singleton, Spread, Replicas)
- [x] Leader election via NATS KV (`election.go`)
- [x] Basic membership tracking (`membership.go`)
- [x] Distributed KV store (`kv.go`)
- [x] Distributed locks (`lock.go`)
- [x] Inter-node RPC (`rpc.go`)
- [x] Event pub/sub (`events.go`)
- [x] Health checks with HTTP endpoints (`health.go`)
- [x] Audit logging to JetStream (`audit.go`)
- [x] Prometheus metrics (`metrics.go`)
- [x] Platform options (`options.go`)
- [x] Backend controllers - Systemd, Docker, Process (`backend/backend.go`)
- [x] Test utilities (`testutil/`)

### 🚧 Partially Implemented
- [ ] Lifecycle hooks (OnActive, OnPassive defined but need full integration)
- [ ] App placement constraints (options defined but scheduler not implemented)

---

## Phase 1: Core Platform & App Model Enhancement

> **Spec**: `01-architecture.md`, `02-app-api.md`
> **Priority**: HIGH

### 1.1 Election Improvements
- [ ] Add `StepDown()` method to Election for graceful leadership transfer
- [ ] Implement election epoch tracking with metrics
- [ ] Add `WaitForLeadership()` with timeout
- [ ] Implement leader health monitoring (detect zombie leaders)
- [ ] Add election audit events (leader_acquired, leader_lost, leader_changed)

### 1.2 Lifecycle Hooks Enhancement
- [ ] Implement `OnLeaderChange(fn func(ctx, leaderID string) error)` hook
- [ ] Add `OnHealthChange(fn func(ctx, status Health) error)` hook
- [ ] Ensure hooks are called in correct order during transitions
- [ ] Add hook timeout handling and error recovery
- [ ] Implement graceful shutdown sequence with hook cleanup

### 1.3 App Data Isolation
- [ ] Ensure KV bucket naming follows `{platform}_{app}` convention
- [ ] Ensure lock bucket naming follows `{platform}_{app}_locks` convention
- [ ] Add validation to prevent cross-app data access
- [ ] Implement per-app JetStream stream isolation

### 1.4 Health Check Improvements
- [ ] Add configurable health check intervals per check
- [ ] Implement health check dependencies (check A depends on B)
- [ ] Add health check history/trending
- [ ] Implement circuit breaker for failing checks

### 1.5 Testing (Phase 1)
- [ ] Unit tests for election contention scenarios
- [ ] Unit tests for graceful leadership transfer
- [ ] Unit tests for hook ordering
- [ ] E2E test: basic election and failover
- [ ] E2E test: split-brain prevention
- [ ] Create `scripts/validate-election.sh`

---

## Phase 2: App Data & Replication

> **Spec**: `05-data-replication.md`
> **Priority**: HIGH

### 2.1 SQLite3 + Litestream WAL Replication
- [ ] Create `wal/` package structure
- [ ] Implement `wal/replicator.go` - Litestream primary mode
  - [ ] Monitor SQLite WAL file for changes
  - [ ] Capture WAL frames as LTX files
  - [ ] Publish LTX files to NATS Object Store
- [ ] Implement `wal/restorer.go` - Litestream passive mode
  - [ ] Subscribe to NATS Object Store bucket
  - [ ] Restore LTX files to local replica database
  - [ ] Maintain near real-time copy of primary
- [ ] Implement `wal/nats.go` - NATS replica backend for Litestream
  - [ ] Object Store integration for LTX files
  - [ ] Stream configuration for WAL frames
- [ ] Implement WAL position tracking and lag monitoring
- [ ] Add `app.WAL().Position()`, `app.WAL().Lag()`, `app.WAL().Sync()`

### 2.2 Snapshot Manager
- [ ] Create `snapshot/` package
- [ ] Implement `snapshot.go` - snapshot creation and management
  - [ ] Create periodic full database snapshots
  - [ ] Store snapshots in NATS Object Store (`{platform}_{app}_snapshots`)
  - [ ] Implement snapshot retention policy
- [ ] Add `app.Snapshots().Create()`, `app.Snapshots().Latest()`, `app.Snapshots().Restore()`
- [ ] Implement snapshot-based bootstrap for new nodes

### 2.3 Database Promotion Flow
- [ ] Implement atomic replica → primary promotion (file swap)
- [ ] Add pre-promotion WAL catch-up from NATS Object Store
- [ ] Implement promotion verification (check WAL mode, integrity)
- [ ] Add promotion audit events

### 2.4 Replication Configuration
- [ ] Implement `cluster.WithSQLite(path, opts...)` fully
- [ ] Add `SQLiteReplica(path)` option
- [ ] Add `SQLiteSnapshotInterval(duration)` option
- [ ] Add `SQLiteRetention(duration)` option
- [ ] Add `SQLiteCompactionLevels(levels)` option

### 2.5 Testing (Phase 2)
- [ ] Unit tests for WAL replication
- [ ] Unit tests for snapshot creation/restore
- [ ] E2E test: data replication across nodes
- [ ] E2E test: failover with data integrity verification
- [ ] Create `scripts/validate-replication.sh`

---

## Phase 3: Cross-App Communication

> **Spec**: `03-platform-api.md`, `06-communication.md`
> **Priority**: HIGH

### 3.1 App API Registration
- [ ] Implement `app.Handle(method, handler)` with request routing
- [ ] Add handler middleware support (logging, auth, metrics)
- [ ] Implement request/response serialization (JSON, protobuf)
- [ ] Add handler timeout configuration

### 3.2 API Bus
- [ ] Implement `platform.API(appName)` - get client for app
- [ ] Add routing modes:
  - [ ] `ToLeader()` - route to leader instance
  - [ ] `ToAny()` - route to any healthy instance
  - [ ] `ForKey(key)` - route to instance owning key (Ring mode)
- [ ] Implement load balancing for pool apps
- [ ] Add retry logic with configurable backoff

### 3.3 Cross-App RPC
- [ ] Implement `cluster.Call(appName)` helper
- [ ] Add cross-app request tracing
- [ ] Implement cross-app timeouts and cancellation
- [ ] Add circuit breaker for failing apps

### 3.4 Platform-Wide Events
- [ ] Implement `platform.Events().Publish(subject, data)`
- [ ] Implement `platform.Events().Subscribe(pattern, handler)`
- [ ] Add event filtering by source app
- [ ] Implement platform-wide event stream (`{platform}_events`)

### 3.5 Platform-Wide Locks
- [ ] Implement `platform.Lock(name)` for cross-app coordination
- [ ] Use `{platform}_platform` KV bucket for platform locks
- [ ] Add lock holder tracking and visibility

### 3.6 VIP Manager
- [ ] Create `vip/` package (partially exists in old impl)
- [ ] Implement `vip.Manager` with acquire/release
- [ ] Add VIP health checking (ARP, ping)
- [ ] Implement graceful VIP migration on failover
- [ ] Add VIP audit events

### 3.7 Testing (Phase 3)
- [ ] Unit tests for cross-app RPC routing
- [ ] Unit tests for event propagation
- [ ] E2E test: cross-app communication flow
- [ ] E2E test: VIP failover
- [ ] Create `scripts/validate-split-brain.sh`

---

## Phase 4: Backend Integration

> **Spec**: `07-backends.md`
> **Priority**: MEDIUM

### 4.1 Backend Coordinator
- [ ] Implement backend lifecycle coordination with cluster manager
- [ ] Add proper sequencing:
  1. Win election
  2. Final WAL catch-up
  3. Promote replica DB
  4. Acquire VIP
  5. Start backend service
  6. Start WAL replication
- [ ] Implement graceful shutdown sequence (reverse order)

### 4.2 Systemd Backend Enhancement
- [ ] Add service dependency management
- [ ] Implement systemd notify integration (Type=notify)
- [ ] Add journal logging integration
- [ ] Implement service restart handling

### 4.3 Docker Backend Enhancement
- [ ] Add container health check integration
- [ ] Implement container resource limits
- [ ] Add network configuration options
- [ ] Implement container restart policies

### 4.4 Process Backend Enhancement
- [ ] Add process group management
- [ ] Implement graceful shutdown with SIGTERM/SIGKILL
- [ ] Add stdout/stderr capture and logging
- [ ] Implement process restart handling

### 4.5 Custom Backend Support
- [ ] Document Backend interface requirements
- [ ] Add backend health check integration
- [ ] Implement backend metrics collection

### 4.6 Testing (Phase 4)
- [ ] Unit tests for backend lifecycle
- [ ] E2E test: systemd backend integration
- [ ] E2E test: docker backend integration
- [ ] E2E test: process backend integration

---

## Phase 5: Cluster Patterns

> **Spec**: `09-cluster-patterns.md`
> **Priority**: MEDIUM

### 5.1 Active-Passive Pattern (Default)
- [ ] Verify singleton mode implementation
- [ ] Add standby readiness tracking
- [ ] Implement failover timing metrics
- [ ] Add standby promotion priority

### 5.2 Pool Pattern
- [ ] Implement `cluster.Pool()` helper function
- [ ] Add instance registration/deregistration
- [ ] Implement load balancer awareness
- [ ] Add pool member health tracking

### 5.3 Ring Pattern (Consistent Hash)
- [ ] Implement `cluster.Ring(partitions)` configuration
- [ ] Create consistent hash ring implementation
- [ ] Implement partition assignment algorithm
- [ ] Add `OnOwn(fn)` and `OnRelease(fn)` hooks
- [ ] Implement partition rebalancing on membership changes
- [ ] Add `ring.NodeFor(key)` - find node for key
- [ ] Implement partition ownership transfer protocol

### 5.4 Ring Data Transfer
- [ ] Implement partition data transfer on rebalance
- [ ] Add transfer progress tracking
- [ ] Implement transfer validation
- [ ] Add transfer retry logic

### 5.5 Testing (Phase 5)
- [ ] Unit tests for consistent hashing
- [ ] Unit tests for partition assignment
- [ ] E2E test: ring rebalancing
- [ ] E2E test: partition ownership transfer
- [ ] Create `scripts/benchmark-failover.sh`

---

## Phase 6: Dynamic Placement

> **Spec**: `13-dynamic-placement.md`
> **Priority**: MEDIUM

### 6.1 Label-Based Placement
- [ ] Implement `OnLabel(key, value)` constraint enforcement
- [ ] Implement `AvoidLabel(key, value)` constraint enforcement
- [ ] Add `PreferLabel(key, value)` soft constraint
- [ ] Implement `OnNodes(nodes...)` constraint enforcement

### 6.2 Affinity/Anti-Affinity
- [ ] Implement `WithApp(appName)` co-location
- [ ] Implement `AwayFromApp(appName)` anti-affinity
- [ ] Add weighted preference support

### 6.3 Label Change Detection
- [ ] Implement `platform.WatchLabels()` for label changes
- [ ] Add automatic placement re-evaluation on label change
- [ ] Implement graceful migration trigger

### 6.4 Migration APIs
- [ ] Implement `platform.MoveApp(ctx, MoveRequest)`
- [ ] Implement `platform.DrainNode(ctx, nodeID, opts)`
- [ ] Implement `platform.RebalanceApp(ctx, appName)`
- [ ] Implement `platform.UpdateNodeLabels(nodeID, labels)`

### 6.5 Migration Hooks
- [ ] Implement `OnMigrationStart(fn)` hook (can reject)
- [ ] Implement `OnMigrationComplete(fn)` hook
- [ ] Implement `OnPlacementInvalid(fn)` hook

### 6.6 Migration Safety
- [ ] Implement cooldown period between migrations
- [ ] Add rate limiting for migrations
- [ ] Implement maintenance windows support
- [ ] Add load threshold checks before migration

### 6.7 Testing (Phase 6)
- [ ] Unit tests for constraint evaluation
- [ ] E2E test: label-triggered migration
- [ ] E2E test: manual migration via API
- [ ] E2E test: node drain
- [ ] Create `scripts/validate-migration.sh`

---

## Phase 7: Service Discovery

> **Spec**: `15-service-discovery.md`
> **Priority**: MEDIUM

### 7.1 Service Configuration
- [ ] Implement `ServiceConfig` struct with full options
- [ ] Add service validation
- [ ] Implement health check configuration per service

### 7.2 Service Registration
- [ ] Implement `app.ExposeService(name, config)`
- [ ] Create NATS KV bucket for service records (`{platform}_services`)
- [ ] Implement automatic registration on app start
- [ ] Implement automatic deregistration on app stop
- [ ] Add service health state updates

### 7.3 Service Discovery APIs
- [ ] Implement `platform.DiscoverServices(ctx, app, service)`
- [ ] Implement `platform.DiscoverServicesByTag(ctx, tag)`
- [ ] Implement `platform.DiscoverServicesByMetadata(ctx, metadata)`
- [ ] Implement `platform.WatchServices(ctx, app, service)`

### 7.4 Service Health Integration
- [ ] Link service health to app health checks
- [ ] Implement service-specific health endpoints
- [ ] Add service health status to discovery responses

### 7.5 Export Formats
- [ ] Implement Prometheus targets export
- [ ] Implement Consul-compatible export
- [ ] Implement custom JSON export

### 7.6 DNS-SD Integration (Optional)
- [ ] Implement DNS server for service discovery
- [ ] Add SRV record support
- [ ] Add A/AAAA record support

### 7.7 Testing (Phase 7)
- [ ] Unit tests for service registration
- [ ] Unit tests for service discovery queries
- [ ] E2E test: service registration and discovery
- [ ] E2E test: service health updates
- [ ] Create `scripts/validate-discovery.sh`

---

## Phase 8: Leaf Nodes & Multi-Zone

> **Spec**: `16-leaf-nodes.md`
> **Priority**: LOW

### 8.1 Hub Configuration
- [ ] Implement `cluster.WithLeafHub(config)` option
- [ ] Configure NATS leaf node listening port
- [ ] Implement TLS configuration for leaf connections
- [ ] Add authorized leaves management

### 8.2 Leaf Connection
- [ ] Implement `cluster.WithLeafConnection(config)` option
- [ ] Add hub URL configuration
- [ ] Implement automatic reconnection
- [ ] Add connection health monitoring

### 8.3 Cross-Platform Communication
- [ ] Implement cross-platform RPC routing
- [ ] Implement cross-platform event propagation
- [ ] Implement cross-platform service discovery

### 8.4 Cross-Zone WAL Replication
- [ ] Implement WAL sharing to leaves
- [ ] Implement `cluster.StandbyOf(platform, app)` for cross-platform standbys
- [ ] Add cross-zone replication lag tracking

### 8.5 Failover & Promotion
- [ ] Implement `cluster.PromoteOnHubFailure(config)`
- [ ] Add quorum-based promotion
- [ ] Implement fencing tokens
- [ ] Add witness node support

### 8.6 Partition Handling
- [ ] Implement partition detection
- [ ] Add partition strategy configuration (FailSafe, Autonomous, Coordinated)
- [ ] Implement reconciliation after partition heals

### 8.7 Testing (Phase 8)
- [ ] Unit tests for leaf connection management
- [ ] E2E test: multi-zone deployment
- [ ] E2E test: hub failure and leaf promotion
- [ ] E2E test: partition handling
- [ ] Create `scripts/validate-leaf-failover.sh`

---

## Phase 9: Distribution & CLI

> **Spec**: `11-deployment.md`
> **Priority**: LOW

### 9.1 CLI Implementation
- [ ] Create `cmd/go-cluster/` main package
- [ ] Implement subcommand structure with cobra

#### Node Commands
- [ ] `go-cluster node list` - List nodes in platform
- [ ] `go-cluster node status <node-id>` - Show node status
- [ ] `go-cluster node drain <node-id>` - Drain node
- [ ] `go-cluster node label <node-id> key=value` - Set/remove labels
- [ ] `go-cluster node cordon/uncordon <node-id>` - Mark node schedulable

#### App Commands
- [ ] `go-cluster app list` - List apps
- [ ] `go-cluster app status <app>` - Show app status
- [ ] `go-cluster app move <app> --from <node> --to <node>` - Move app
- [ ] `go-cluster app rebalance <app>` - Rebalance app instances

#### Cluster Commands
- [ ] `go-cluster run` - Run daemon
- [ ] `go-cluster status` - Show cluster status
- [ ] `go-cluster stepdown` - Gracefully step down as leader
- [ ] `go-cluster health` - Check health

#### Data Commands
- [ ] `go-cluster snapshot create` - Create snapshot
- [ ] `go-cluster snapshot list` - List snapshots
- [ ] `go-cluster snapshot restore <id>` - Restore snapshot

#### Service Commands
- [ ] `go-cluster services list` - List services
- [ ] `go-cluster services show <app> <service>` - Show service details
- [ ] `go-cluster services export --format <format>` - Export for Prometheus/Consul

#### Leaf Commands
- [ ] `go-cluster leaf list` - List leaf connections
- [ ] `go-cluster leaf status <platform>` - Show leaf status
- [ ] `go-cluster leaf promote <platform>` - Promote leaf to hub

### 9.2 Configuration
- [ ] Implement YAML/JSON config file support
- [ ] Add environment variable support
- [ ] Implement config validation
- [ ] Add config file generation (`go-cluster init`)

### 9.3 Packaging
- [ ] Create GoReleaser configuration
- [ ] Build for Linux, macOS, Windows
- [ ] Create checksums and signatures

### 9.4 Systemd Integration
- [ ] Create systemd service template (`go-cluster@.service`)
- [ ] Add systemd notify support
- [ ] Create environment file template

### 9.5 Container Images
- [ ] Create minimal Dockerfile
- [ ] Publish to ghcr.io
- [ ] Create docker-compose examples

### 9.6 Kubernetes
- [ ] Create StatefulSet manifests
- [ ] Create ConfigMap templates
- [ ] Create RBAC manifests
- [ ] Create Helm chart (optional)

### 9.7 Installation Scripts
- [ ] Create `install.sh` one-liner installer
- [ ] Create Homebrew formula
- [ ] Create APT repository setup
- [ ] Create YUM repository setup
- [ ] Create Ansible playbook

### 9.8 Upgrade Support
- [ ] Implement rolling upgrade support
- [ ] Add canary deployment support
- [ ] Implement blue-green deployment

---

## Documentation

### API Documentation
- [ ] Generate godoc documentation
- [ ] Create API reference guide
- [ ] Add inline code examples

### Guides
- [ ] Quick start guide
- [ ] Configuration reference
- [ ] Best practices guide
- [ ] Troubleshooting guide
- [ ] Migration guide (from old implementation)

### Examples
- [ ] `examples/basic/` - Simple active-passive
- [ ] `examples/convex-backend/` - Full Convex integration
- [ ] `examples/distributed-cache/` - Ring pattern cache
- [ ] `examples/api-gateway/` - Pool pattern API
- [ ] `examples/multi-zone/` - Leaf node deployment

---

## Testing Infrastructure

### Test Utilities Enhancement
- [ ] Add `testutil.StartNATSCluster()` with real clustering
- [ ] Implement `NATSCluster.Partition()` network partition simulation
- [ ] Add `testutil.WaitForCondition()` helper
- [ ] Create database test helpers (`testutil/db.go`)
- [ ] Add assertion helpers (`testutil/assert.go`)

### CI/CD
- [ ] Set up GitHub Actions workflow
- [ ] Add linting (golangci-lint)
- [ ] Add test coverage reporting
- [ ] Add benchmark tracking
- [ ] Set up automated releases

### Validation Scripts
- [ ] `scripts/validate-election.sh`
- [ ] `scripts/validate-failover.sh`
- [ ] `scripts/validate-replication.sh`
- [ ] `scripts/validate-split-brain.sh`
- [ ] `scripts/validate-migration.sh`
- [ ] `scripts/validate-discovery.sh`
- [ ] `scripts/validate-leaf-failover.sh`
- [ ] `scripts/benchmark-failover.sh`
- [ ] `scripts/common.sh` - shared utilities

---

## Observability

### Metrics Enhancement
- [ ] Add migration metrics (`gc_migration_*`)
- [ ] Add placement metrics (`gc_placement_*`)
- [ ] Add service discovery metrics (`gc_services_*`)
- [ ] Add leaf connection metrics (`gc_leaf_*`)

### Tracing
- [ ] Integrate OpenTelemetry
- [ ] Add trace propagation across RPC calls
- [ ] Add trace propagation across events
- [ ] Create trace exporter configuration

### Audit Enhancement
- [ ] Add structured audit event types
- [ ] Implement audit event correlation
- [ ] Add audit retention policy configuration
- [ ] Create audit export tools

---

## Priority Order for Implementation

1. **Phase 1** - Core Platform Enhancement (foundation for everything)
2. **Phase 2** - Data Replication (critical for HA)
3. **Phase 3** - Cross-App Communication (enables complex apps)
4. **Phase 4** - Backend Integration (production readiness)
5. **Phase 5** - Cluster Patterns (Ring pattern specifically)
6. **Phase 6** - Dynamic Placement (operational flexibility)
7. **Phase 7** - Service Discovery (integration capability)
8. **Phase 9** - CLI & Distribution (user experience)
9. **Phase 8** - Leaf Nodes (advanced multi-zone)

---

## Notes

### Dependencies
- NATS Server 2.10+ with JetStream
- Litestream for WAL replication
- SQLite3 (CGO required)
- Prometheus client_golang
- OpenTelemetry (for tracing)

### Breaking Changes from Old Implementation
- Old impl uses `Manager` + `Node` pattern
- New impl uses `Platform` + `App` pattern (component model)
- New impl has app-isolated data (KV, locks per app)
- New impl has explicit placement constraints

### Reference Code
The old implementation at `/Users/ozant/devel/tries/convex/go-cluster` contains:
- `manager.go` - High-level orchestration (reference for Phase 4)
- `node.go` - Node lifecycle management
- `election.go` - Election implementation (similar to current)
- `vip/manager.go` - VIP management (reference for Phase 3)
- `config.go` - Configuration patterns
- Various tests for reference
