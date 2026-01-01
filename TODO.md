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
- [x] Lifecycle hooks (OnActive, OnPassive fully integrated with database promotion/demotion)
- [ ] App placement constraints (options defined but scheduler not implemented)

---

## Phase 1: Core Platform & App Model Enhancement

> **Spec**: `01-architecture.md`, `02-app-api.md`
> **Priority**: HIGH
> **Status**: ✅ COMPLETED

### 1.1 Election Improvements
- [x] Add `StepDown()` method to Election for graceful leadership transfer
- [x] Implement election epoch tracking with metrics
- [x] Add `WaitForLeadership()` with timeout
- [x] Implement leader health monitoring (detect zombie leaders via KV watcher)
- [x] Add election audit events (leader_acquired, leader_lost, leader_changed, leader_released, step_down_initiated)

### 1.2 Lifecycle Hooks Enhancement
- [x] Implement `OnLeaderChange(fn func(ctx, leaderID string) error)` hook
- [x] Add `OnHealthChange(fn func(ctx, status Health) error)` hook
- [x] Ensure hooks are called in correct order during transitions
- [x] Add hook timeout handling and error recovery (`callHookWithTimeout`)
- [x] Implement graceful shutdown sequence with hook cleanup

### 1.3 App Data Isolation
- [x] Ensure KV bucket naming follows `{platform}_{app}` convention
- [x] Ensure lock bucket naming follows `{platform}_{app}_locks` convention
- [ ] Add validation to prevent cross-app data access (deferred - needs more design)
- [ ] Implement per-app JetStream stream isolation (deferred - needs more design)

### 1.4 Health Check Improvements
- [x] Add configurable health check intervals per check (`WithHealthCheckInterval`)
- [x] Implement health check dependencies (check A depends on B) (`WithHealthCheckDependsOn`)
- [x] Add health check history/trending (`GetHistory`, `GetAllHistory`)
- [x] Implement circuit breaker for failing checks (auto-opens after 3 failures)

### 1.5 Testing (Phase 1)
- [x] Unit tests for election contention scenarios
- [x] Unit tests for graceful leadership transfer (`TestElection_StepDown`)
- [x] Unit tests for hook ordering
- [x] E2E test: basic election and failover (`TestElection_LeaderFailover`)
- [x] E2E test: split-brain prevention (`TestElection_SplitBrainPrevention`)
- [x] Create `scripts/validate-election.sh`

---

## Phase 2: App Data & Replication

> **Spec**: `05-data-replication.md`
> **Priority**: HIGH
> **Status**: ✅ COMPLETED

### 2.1 SQLite3 + Litestream WAL Replication
- [x] Create `wal/` package structure
- [x] Implement `wal/replicator.go` - Litestream primary mode
  - [x] Monitor SQLite WAL file for changes
  - [x] Capture WAL frames as segments
  - [x] Publish segments to NATS Object Store
- [x] Implement `wal/restorer.go` - Litestream passive mode
  - [x] Subscribe to NATS Object Store bucket
  - [x] Restore segments to local replica database
  - [x] Maintain near real-time copy of primary
- [x] Implement `wal/nats.go` - NATS replica backend
  - [x] Object Store integration for WAL segments
  - [x] Watch for new segments
- [x] Implement WAL position tracking and lag monitoring
- [x] Add `app.WAL().Position()`, `app.WAL().Lag()`, `app.WAL().Sync()`, `app.WAL().Stats()`

### 2.2 Snapshot Manager
- [x] Create `snapshot/` package
- [x] Implement `snapshot.go` - snapshot creation and management
  - [x] Create periodic full database snapshots
  - [x] Store snapshots in NATS Object Store (`{platform}_{app}_snapshots`)
  - [x] Implement snapshot retention policy (by time and count)
- [x] Add `app.Snapshots().Create()`, `app.Snapshots().Latest()`, `app.Snapshots().Restore()`
- [x] Implement snapshot-based bootstrap for new nodes (`DatabaseManager.Bootstrap()`)

### 2.3 Database Promotion Flow
- [x] Implement replica → primary promotion (`DatabaseManager.Promote()`)
- [x] Add pre-promotion WAL catch-up from NATS Object Store
- [x] Implement promotion verification (check WAL mode, database integrity)
- [x] Add promotion/demotion audit events (promotion_started, wal_catchup_*, database_verified, promotion_completed)

### 2.4 Replication Configuration
- [x] Implement `cluster.WithSQLite(path, opts...)` fully
- [x] Add `SQLiteReplica(path)` option
- [x] Add `SQLiteSnapshotInterval(duration)` option
- [x] Add `SQLiteRetention(duration)` option
- [ ] Add `SQLiteCompactionLevels(levels)` option (deferred - not critical for MVP)

### 2.5 Testing (Phase 2)
- [x] Unit tests for WAL replication (`wal/wal_test.go`)
- [x] Unit tests for snapshot creation/restore (`snapshot/snapshot_test.go`)
- [x] E2E test: data replication across nodes (`TestDatabaseManager_TwoNodeReplication`)
- [x] E2E test: failover with data integrity verification (`TestDatabaseManager_FailoverWithDataIntegrity`)
- [x] Create `scripts/validate-replication.sh`

---

## Phase 3: Cross-App Communication

> **Spec**: `03-platform-api.md`, `06-communication.md`
> **Priority**: HIGH
> **Status**: 🚧 IN PROGRESS

### 3.1 App API Registration
- [x] Implement `app.Handle(method, handler)` with request routing (existing in rpc.go)
- [x] Add handler middleware support (logging, auth, metrics) (`middleware.go`)
- [x] Implement request/response serialization (JSON via CallJSON)
- [x] Add handler timeout configuration (WithTimeout option)

### 3.2 API Bus
- [x] Implement `platform.API(appName)` - get client for app
- [x] Add routing modes:
  - [x] `ToLeader()` - route to leader instance
  - [x] `ToAny()` - route to any healthy instance
  - [x] `ForKey(key)` - route to instance owning key (Ring mode) - stub, needs ring impl
- [x] Implement load balancing for pool apps (via ToAny routing)
- [x] Add retry logic with configurable backoff (WithRetry option)

### 3.3 Cross-App RPC
- [x] Implement `platform.API(appName).Call()` for cross-app calls
- [ ] Add cross-app request tracing (OpenTelemetry - Phase 9)
- [x] Implement cross-app timeouts and cancellation
- [x] Add circuit breaker for failing apps

### 3.4 Platform-Wide Events
- [x] Implement `platform.Events().Publish(subject, data)`
- [x] Implement `platform.Events().Subscribe(pattern, handler)`
- [x] Add event filtering by source app (`SubscribeFromApp`)
- [x] Implement platform-wide event stream (`{platform}_events`)

### 3.5 Platform-Wide Locks
- [x] Implement `platform.Lock(name)` for cross-app coordination
- [x] Use `{platform}_platform_locks` KV bucket for platform locks
- [x] Add lock holder tracking and visibility (`Holder()` method)

### 3.6 VIP Manager
- [x] Create `vip/` package
- [x] Implement `vip.Manager` with acquire/release
- [x] Add VIP health checking (interface monitoring)
- [x] Implement graceful VIP migration on failover (via OnAcquired/OnReleased callbacks)
- [ ] Add VIP audit events (deferred - low priority)

### 3.7 Testing (Phase 3)
- [x] Unit tests for cross-app RPC routing (`api_test.go`)
- [x] Unit tests for event propagation (`platform_events_test.go`)
- [x] Unit tests for platform-wide locks (`platform_lock_test.go`)
- [x] E2E test: cross-app communication flow (`crossapp_test.go`)
  - [x] Multi-service order processing (Order→Inventory→Payment)
  - [x] Platform lock coordination between apps
  - [x] Multi-node cross-app communication
  - [x] Event-driven saga pattern
- [x] Unit tests for VIP manager (`vip/manager_test.go`)
- [ ] E2E test: VIP failover (requires root/CAP_NET_ADMIN - manual testing)
- [x] Create `scripts/validate-split-brain.sh`

---

## Phase 4: Backend Integration

> **Spec**: `07-backends.md`
> **Priority**: MEDIUM
> **Status**: ✅ COMPLETED

### 4.1 Backend Coordinator
- [x] Implement backend lifecycle coordination with cluster manager (`BackendCoordinator`)
- [x] Add proper sequencing:
  1. Win election
  2. Final WAL catch-up
  3. Promote replica DB
  4. Acquire VIP (TODO: VIP manager)
  5. Start backend service
  6. Start WAL replication
- [x] Implement graceful shutdown sequence (reverse order)
- [x] Add state change callbacks (`OnStateChange`)
- [x] Add uptime tracking

### 4.2 Systemd Backend Enhancement
- [x] Add service dependency management (`WithDependencies`)
- [x] Implement systemd notify integration (`WithNotify` - Type=notify support)
- [x] Add journal logging integration (`JournalLogs`, `WithJournalLines`)
- [x] Implement service restart handling (`Restart`, `WithSystemdRestart`)
- [x] Add stop timeout configuration (`WithStopTimeout`)

### 4.3 Docker Backend Enhancement
- [x] Add container health check integration (`DockerHealthCheck` struct)
- [x] Implement container resource limits (`DockerResources` struct)
- [x] Add network configuration options (`Network`, `Labels`, `ExtraArgs`)
- [x] Implement container restart policies (`RestartPolicy`)
- [x] Add graceful stop with timeout (`StopTimeout`)
- [x] Add container logs retrieval (`Logs`)

### 4.4 Process Backend Enhancement
- [x] Add process group management (`UseProcessGroup`, `Setpgid`)
- [x] Implement graceful shutdown with SIGTERM/SIGKILL (`GracefulTimeout`)
- [x] Add stdout/stderr capture and logging (`OutputHandler`, `LogFile`)
- [x] Implement process restart handling (`Restart`, `RestartPolicy`)
- [x] Add signal sending support (`Signal`)
- [x] Add PID and exit code retrieval (`PID`, `ExitCode`, `Exited`)

### 4.5 Custom Backend Support
- [x] Document Backend interface requirements (Start, Stop, Health)
- [x] Add backend health check integration (Health method with uptime)
- [x] Implement backend metrics collection (`gc_backend_*` Prometheus metrics)

### 4.6 Testing (Phase 4)
- [x] Unit tests for backend lifecycle (`backend_test.go`)
- [x] E2E test: backend start/stop with mock backend
- [x] E2E test: backend failover between nodes
- [x] E2E test: backend health monitoring
- [ ] E2E test: systemd backend integration (requires real systemd)
- [ ] E2E test: docker backend integration (requires real docker)
- [ ] E2E test: process backend integration with real processes

---

## Phase 5: Cluster Patterns

> **Spec**: `09-cluster-patterns.md`
> **Priority**: MEDIUM
> **Status**: ✅ COMPLETED (Ring Pattern)

### 5.1 Active-Passive Pattern (Default)
- [x] Verify singleton mode implementation (existing)
- [ ] Add standby readiness tracking
- [ ] Implement failover timing metrics
- [ ] Add standby promotion priority

### 5.2 Pool Pattern
- [x] Implement `cluster.Pool()` helper function (use Spread())
- [x] Add instance registration/deregistration (via membership)
- [ ] Implement load balancer awareness
- [x] Add pool member health tracking (via health checks)

### 5.3 Ring Pattern (Consistent Hash)
- [x] Implement `cluster.Ring(partitions)` configuration
- [x] Create consistent hash ring implementation (`ring.go`)
- [x] Implement partition assignment algorithm
- [x] Add `OnOwn(fn)` and `OnRelease(fn)` hooks
- [x] Implement partition rebalancing on membership changes
- [x] Add `ring.NodeFor(key)` - find node for key
- [x] Implement partition ownership transfer protocol

### 5.4 Ring Data Transfer
- [x] Implement partition data transfer on rebalance (via OnOwn/OnRelease hooks)
- [ ] Add transfer progress tracking (deferred - app-specific)
- [ ] Implement transfer validation (deferred - app-specific)
- [ ] Add transfer retry logic (deferred - app-specific)

### 5.5 Testing (Phase 5)
- [x] Unit tests for consistent hashing (`ring_test.go`)
- [x] Unit tests for partition assignment (`ring_test.go`)
- [x] E2E test: ring rebalancing (`TestRing_TwoNodes`, `TestRing_NodeLeave`)
- [x] E2E test: partition ownership transfer (`TestRing_OnOwnOnRelease`)
- [ ] Create `scripts/benchmark-failover.sh`

---

## Phase 6: Dynamic Placement

> **Spec**: `13-dynamic-placement.md`
> **Priority**: MEDIUM
> **Status**: ✅ COMPLETED

### 6.1 Label-Based Placement
- [x] Implement `OnLabel(key, value)` constraint enforcement (`placement.go`)
- [x] Implement `AvoidLabel(key, value)` constraint enforcement (`placement.go`)
- [x] Add `PreferLabel(key, value)` soft constraint (`options.go`)
- [x] Implement `OnNodes(nodes...)` constraint enforcement (`placement.go`)

### 6.2 Affinity/Anti-Affinity
- [x] Implement `WithApp(appName)` co-location (`placement.go`)
- [x] Implement `AwayFromApp(appName)` anti-affinity (`placement.go`)
- [x] Add weighted preference support (`LabelPreference.Weight`)

### 6.3 Label Change Detection
- [x] Implement `platform.WatchLabels()` for label changes (`cluster.go`)
- [x] Add automatic placement re-evaluation on label change (`Scheduler.watchLabelChanges`)
- [x] Implement graceful migration trigger (`Scheduler.reevaluateAllPlacements`)

### 6.4 Migration APIs
- [x] Implement `platform.MoveApp(ctx, MoveRequest)` (`cluster.go`)
- [x] Implement `platform.DrainNode(ctx, nodeID, opts)` (`cluster.go`)
- [x] Implement `platform.RebalanceApp(ctx, appName)` (`cluster.go`)
- [x] Implement `platform.UpdateNodeLabels(nodeID, labels)` (`cluster.go`)

### 6.5 Migration Hooks
- [x] Implement `OnMigrationStart(fn)` hook (can reject) - existing in cluster.go
- [x] Implement `OnMigrationComplete(fn)` hook - existing in cluster.go
- [x] Implement `OnPlacementInvalid(fn)` hook (`cluster.go`)

### 6.6 Migration Safety
- [x] Implement cooldown period between migrations (`MigrationPolicy.CooldownPeriod`)
- [x] Add rate limiting for migrations (`MigrationPolicy.MaxConcurrent`)
- [x] Implement maintenance windows support (`MigrationPolicy.MaintenanceWindows`)
- [x] Add load threshold checks before migration (`MigrationPolicy.LoadThreshold`)

### 6.7 Testing (Phase 6)
- [x] Unit tests for constraint evaluation (`placement_test.go`)
- [x] E2E test: label-triggered migration (`TestPlacement_LabelTriggeredMigration`)
- [x] E2E test: manual migration via API (`TestPlacement_ManualMigration`)
- [x] E2E test: node drain (`TestPlacement_NodeDrain`)
- [x] Create `scripts/validate-migration.sh`

---

## Phase 7: Service Discovery

> **Spec**: `15-service-discovery.md`
> **Priority**: MEDIUM
> **Status**: ✅ COMPLETED

### 7.1 Service Configuration
- [x] Implement `ServiceConfig` struct with full options (Validate, SetDefaults, IsHTTPBased methods)
- [x] Add service validation (`ServiceConfigError`, validation for port, protocol, weight, health check)
- [x] Implement health check configuration per service (`HealthCheckConfig` with Validate/SetDefaults)

### 7.2 Service Registration
- [x] Implement `app.ExposeService(name, config)` - already in cluster.go
- [x] Create NATS KV bucket for service records (`{platform}_services`)
- [x] Implement automatic registration on app start (`registerServices`)
- [x] Implement automatic deregistration on app stop (`deregisterServices`)
- [x] Add service health state updates (`UpdateHealth`, `UpdateHealthFromAppHealth`)

### 7.3 Service Discovery APIs
- [x] Implement `platform.DiscoverServices(ctx, app, service)`
- [x] Implement `platform.DiscoverServicesByTag(ctx, tag)`
- [x] Implement `platform.DiscoverServicesByMetadata(ctx, metadata)`
- [x] Implement `platform.WatchServices(ctx, app, service)`
- [x] Implement `platform.DiscoverHealthyServices(ctx, app, service)` - bonus: filter by health

### 7.4 Service Health Integration
- [x] Link service health to app health checks (`UpdateHealthFromAppHealth`)
- [x] Implement service-specific health endpoints (`serviceHealthChecker` with HTTP/TCP checks)
- [x] Add service health status to discovery responses (`ServiceInstance.Health`)

### 7.5 Export Formats
- [x] Implement Prometheus targets export (`ExportPrometheusTargets`)
- [x] Implement Consul-compatible export (`ExportConsulServices`)
- [x] Implement custom JSON export (via `DiscoverServices` + JSON marshaling)

### 7.6 DNS-SD Integration (Optional)
- [ ] Implement DNS server for service discovery (deferred - optional feature)
- [ ] Add SRV record support (deferred - optional feature)
- [ ] Add A/AAAA record support (deferred - optional feature)

### 7.7 Testing (Phase 7)
- [x] Unit tests for service registration (`service_discovery_test.go`)
- [x] Unit tests for service discovery queries (`TestServiceRegistry_*`)
- [x] E2E test: service registration and discovery (`TestServiceRegistry_RegisterAndDiscover`)
- [x] E2E test: service health updates (`TestServiceRegistry_UpdateHealth`)
- [x] E2E test: multi-node service discovery (`TestServiceRegistry_MultipleNodes`)
- [x] Create `scripts/validate-discovery.sh`

---

## Phase 8: Leaf Nodes & Multi-Zone

> **Spec**: `16-leaf-nodes.md`
> **Priority**: LOW
> **Status**: ✅ COMPLETED

### 8.1 Hub Configuration
- [x] Implement `cluster.WithLeafHub(config)` option (`options.go`)
- [x] Configure NATS leaf node listening port (`LeafHubConfig.Port`)
- [x] Implement TLS configuration for leaf connections (`TLSConfig`, `LoadTLSConfig`)
- [x] Add authorized leaves management (`LeafHubConfig.AuthorizedLeaves`)

### 8.2 Leaf Connection
- [x] Implement `cluster.WithLeafConnection(config)` option (`options.go`)
- [x] Add hub URL configuration (`LeafConnectionConfig.HubURLs`)
- [x] Implement automatic reconnection (`ReconnectWait`, `MaxReconnects`)
- [x] Add connection health monitoring (heartbeat monitoring, `IsPartitioned()`)

### 8.3 Cross-Platform Communication
- [x] Implement cross-platform RPC routing (`CallHub`, `CallPlatform`, `handleCrossPlatformRPC`)
- [x] Implement cross-platform event propagation (`PublishToHub`, `PublishToPlatform`, `SubscribeCrossPlatform`)
- [x] Implement cross-platform service discovery (`DiscoverCrossPlatform`, `queryPlatformServices`)

### 8.4 Cross-Zone WAL Replication
- [ ] Implement WAL sharing to leaves (deferred - requires NATS leaf node infrastructure)
- [ ] Implement `cluster.StandbyOf(platform, app)` for cross-platform standbys (deferred)
- [ ] Add cross-zone replication lag tracking (deferred)

### 8.5 Failover & Promotion
- [ ] Implement `cluster.PromoteOnHubFailure(config)` (deferred - requires quorum implementation)
- [x] Add quorum-based promotion (`HasQuorum`, `PartitionStrategy.RequireQuorum`)
- [ ] Implement fencing tokens (deferred)
- [ ] Add witness node support (deferred)

### 8.6 Partition Handling
- [x] Implement partition detection (`monitorHubHeartbeat`, `checkHubHeartbeat`, `handlePotentialPartition`)
- [x] Add partition strategy configuration (`PartitionStrategy` with FailSafe, Autonomous, Coordinated modes)
- [x] Implement reconciliation after partition heals (via `PartitionHealed` event)

### 8.7 Testing (Phase 8)
- [x] Unit tests for leaf connection management (`leaf_test.go`)
- [x] E2E test: hub-leaf connection with heartbeats (`leaf_e2e_test.go`)
- [x] E2E test: cross-platform RPC (`TestLeafE2E_CrossPlatformRPC`)
- [x] E2E test: cross-platform events (`TestLeafE2E_CrossPlatformEvents`)
- [x] E2E test: partition detection (`TestLeafE2E_PartitionDetection`)
- [x] E2E test: leaf join/leave callbacks (`TestLeafE2E_LeafAnnouncement`, `TestLeafE2E_LeafDisconnection`)
- [x] E2E test: quorum handling (`TestLeafE2E_QuorumHandling`)
- [x] E2E test: partition handling (`TestLeafManager_PartitionState`, `TestPartitionStrategy`)
- [ ] E2E test: multi-zone deployment with real NATS leaf infrastructure (requires real NATS leaf nodes)
- [x] Create `scripts/validate-leaf-failover.sh`

---

## Phase 9: Distribution & CLI

> **Spec**: `11-deployment.md`
> **Priority**: LOW
> **Status**: ✅ COMPLETED (CLI)

### 9.1 CLI Implementation
- [x] Create `cmd/go-cluster/` main package
- [x] Implement subcommand structure with cobra

#### Node Commands
- [x] `go-cluster node list` - List nodes in platform
- [x] `go-cluster node status <node-id>` - Show node status
- [x] `go-cluster node drain <node-id>` - Drain node
- [x] `go-cluster node label <node-id> key=value` - Set/remove labels
- [x] `go-cluster node cordon/uncordon <node-id>` - Mark node schedulable

#### App Commands
- [x] `go-cluster app list` - List apps
- [x] `go-cluster app status <app>` - Show app status
- [x] `go-cluster app move <app> --from <node> --to <node>` - Move app
- [x] `go-cluster app rebalance <app>` - Rebalance app instances

#### Cluster Commands
- [x] `go-cluster run` - Run daemon
- [x] `go-cluster status` - Show cluster status
- [x] `go-cluster stepdown` - Gracefully step down as leader
- [x] `go-cluster health` - Check health

#### Data Commands
- [x] `go-cluster snapshot create` - Create snapshot
- [x] `go-cluster snapshot list` - List snapshots
- [x] `go-cluster snapshot restore <id>` - Restore snapshot
- [x] `go-cluster snapshot delete <id>` - Delete snapshot

#### Service Commands
- [x] `go-cluster services list` - List services
- [x] `go-cluster services show <app> <service>` - Show service details
- [x] `go-cluster services export --format <format>` - Export for Prometheus/Consul

#### Leaf Commands
- [x] `go-cluster leaf list` - List leaf connections
- [x] `go-cluster leaf status <platform>` - Show leaf status
- [x] `go-cluster leaf promote <platform>` - Promote leaf to hub
- [x] `go-cluster leaf ping <platform>` - Ping a leaf platform

### 9.2 Configuration
- [x] Implement YAML/JSON config file support (via viper)
- [x] Add environment variable support (NATS_URL, NODE_ID, PLATFORM)
- [x] Implement config validation
- [ ] Add config file generation (`go-cluster init`)

### 9.3 Packaging
- [x] Create GoReleaser configuration (`.goreleaser.yaml`)
- [x] Build for Linux, macOS, Windows (multi-arch: amd64, arm64)
- [x] Create checksums and signatures

### 9.4 Systemd Integration
- [x] Create systemd service template (`packaging/go-cluster.service`)
- [x] Add systemd notify support
- [x] Create environment file template (`packaging/go-cluster.yaml`)

### 9.5 Container Images
- [x] Create minimal Dockerfile (`Dockerfile.goreleaser`)
- [x] Publish to ghcr.io (via GoReleaser)
- [x] Create docker-compose examples (`examples/docker-compose/`)

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
- [x] `examples/basic/` - Simple active-passive
- [x] `examples/convex-backend/` - Full Convex integration
- [x] `examples/distributed-cache/` - Ring pattern cache
- [ ] `examples/api-gateway/` - Pool pattern API
- [ ] `examples/multi-zone/` - Leaf node deployment

---

## Testing Infrastructure

### Test Utilities Enhancement
- [x] Add `testutil.StartNATSCluster()` with real clustering
- [x] Implement `NATSCluster.Partition()` network partition simulation
- [ ] Add `testutil.WaitForCondition()` helper
- [x] Create database test helpers (`testNode` struct in `database_test.go`)
- [ ] Add assertion helpers (`testutil/assert.go`)

### CI/CD
- [ ] Set up GitHub Actions workflow
- [ ] Add linting (golangci-lint)
- [ ] Add test coverage reporting
- [ ] Add benchmark tracking
- [ ] Set up automated releases

### Validation Scripts
- [x] `scripts/validate-election.sh`
- [x] `scripts/validate-failover.sh`
- [x] `scripts/validate-replication.sh`
- [x] `scripts/validate-split-brain.sh`
- [x] `scripts/validate-migration.sh`
- [x] `scripts/validate-discovery.sh`
- [x] `scripts/validate-leaf-failover.sh`
- [x] `scripts/benchmark-failover.sh`
- [x] `scripts/common.sh` - shared utilities
- [x] `scripts/validate-all.sh` - run all validations
- [x] `scripts/validate-backend.sh`
- [x] `scripts/validate-crossapp.sh`
- [x] `scripts/validate-health.sh`
- [x] `scripts/validate-locks.sh`
- [x] `scripts/validate-ring.sh`
- [x] `scripts/validate-vip.sh`

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
