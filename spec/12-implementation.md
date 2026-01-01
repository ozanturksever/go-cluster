# Implementation Phases

## Phase 1: Core Platform & App Model

- [ ] `cluster.NewPlatform()` - shared infrastructure
- [ ] `platform.App()` - create isolated app
- [ ] `cluster.ActivePassive()` pattern with election
- [ ] App data isolation (KV, locks per app)
- [ ] Hooks: OnActive, OnPassive, OnLeaderChange
- [ ] Health checks with HTTP endpoint
- [ ] Audit logging to JetStream (platform-wide)
- [ ] Prometheus metrics
- [ ] **testutil package**
- [ ] **Unit tests for all components**
- [ ] **E2E: election, failover**

---

## Phase 2: App Data & Replication

- [ ] App-isolated KV store
- [ ] App-isolated locks
- [ ] Litestream NATS replica (per-app WAL)
- [ ] WAL replicator/restorer
- [ ] Snapshot manager (NATS Object Store per app)
- [ ] **E2E: replication, data integrity**
- [ ] **validate-replication.sh**

---

## Phase 3: Cross-App Communication

- [ ] App API registration (`app.Handle()`)
- [ ] API Bus (`platform.API()`)
- [ ] Cross-app RPC with routing (leader, any, key-based)
- [ ] Platform-wide events
- [ ] App internal events (isolated)
- [ ] Platform-wide locks
- [ ] VIP manager
- [ ] **E2E: cross-app communication**
- [ ] **validate-split-brain.sh**

---

## Phase 4: Backends

- [ ] Systemd backend
- [ ] Docker backend
- [ ] Process backend
- [ ] **E2E: backend lifecycle**

---

## Phase 5: Patterns

- [ ] `cluster.Pool()` - all active
- [ ] `cluster.Ring()` - consistent hash
- [ ] Partition ownership hooks (OnOwn, OnRelease)
- [ ] **benchmark-failover.sh**

---

## Phase 6: Dynamic Placement

- [ ] Label change detection and placement re-evaluation
- [ ] `platform.MoveApp()` API
- [ ] `platform.DrainNode()` API
- [ ] `platform.RebalanceApp()` API
- [ ] `platform.UpdateNodeLabels()` API
- [ ] Migration hooks (OnMigrationStart, OnMigrationComplete, OnPlacementInvalid)
- [ ] Soft vs hard placement constraints
- [ ] Migration safety guardrails (cooldown, rate limiting)
- [ ] CLI: `app move`, `app rebalance`, `node drain`, `node label`
- [ ] **E2E: label-triggered migration**
- [ ] **E2E: manual migration**
- [ ] **validate-migration.sh**

---

## Phase 7: Service Discovery

- [ ] ServiceConfig struct and validation
- [ ] `app.ExposeService()` API
- [ ] NATS KV bucket for service records
- [ ] Service registration on app start
- [ ] Service deregistration on app stop
- [ ] Health check integration
- [ ] `platform.DiscoverServices()` API
- [ ] `platform.DiscoverServicesByTag()` API
- [ ] `platform.WatchServices()` API
- [ ] CLI: `services list`, `services show`, `services export`
- [ ] DNS-SD integration (optional)
- [ ] **E2E: service discovery**
- [ ] **validate-discovery.sh**

---

## Phase 8: Leaf Nodes & Multi-Zone

- [ ] `cluster.WithLeafHub()` configuration
- [ ] `cluster.WithLeafConnection()` configuration
- [ ] Leaf connection management (connect, reconnect, disconnect)
- [ ] Cross-platform RPC routing
- [ ] Cross-platform event propagation
- [ ] Cross-platform service discovery
- [ ] WAL replication across leaf boundaries
- [ ] `cluster.StandbyOf()` for cross-platform standbys
- [ ] Automatic promotion on hub failure
- [ ] CLI: `leaf list`, `leaf status`, `leaf promote`
- [ ] Leaf connection metrics
- [ ] **E2E: multi-zone deployment**
- [ ] **validate-leaf-failover.sh**

---

## Phase 9: Distribution

- [ ] CLI with platform/app/node commands
- [ ] GoReleaser config
- [ ] Systemd service template
- [ ] Docker image
- [ ] Kubernetes manifests (StatefulSet)
- [ ] install.sh
- [ ] Homebrew formula
- [ ] APT/YUM repos
- [ ] Ansible playbook
- [ ] Rolling upgrade support
- [ ] Canary deployment
