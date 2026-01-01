# go-cluster Overview

> The clustering toolkit for Go. Build HA applications, not infrastructure.

## Philosophy

1. **Convention over configuration** - Sensible defaults, override when needed
2. **One way to do things** - Clear patterns, no decision fatigue
3. **NATS-native** - Leverage NATS fully, don't abstract it away
4. **Observable by default** - Audit, metrics, tracing built-in

---

## Quick Start

```go
package main

import (
    "context"
    "github.com/ozanturksever/go-cluster"
)

func main() {
    app := cluster.New("myapp", "node-1", "nats://localhost:4222")
    
    app.OnActive(func(ctx context.Context) error {
        return myService.Start(ctx)
    })
    
    app.OnPassive(func(ctx context.Context) error {
        return myService.Stop(ctx)
    })
    
    app.Run(context.Background())
}
```

**With SQLite3 + Litestream WAL replication:**
```go
app := cluster.New("myapp", "node-1", "nats://localhost:4222",
    cluster.WithSQLite("/var/lib/myapp/data.db"),  // SQLite3 with Litestream WAL replication
    cluster.WithVIP("192.168.1.100/24", "eth0"),
    cluster.WithBackend(cluster.Systemd("myapp-backend")),
)
app.Run(context.Background())
```

---

## Quick Reference

| Concept | Options |
|---------|---------|
| **Mode** | `Singleton()`, `Spread()`, `Replicas(N)` |
| **Placement** | `OnNodes()`, `OnLabel()`, `AwayFromApp()`, `WithApp()` |
| **Pattern** | `Ring(N)` for sharding |
| **Data** | `app.KV`, `app.DB()`, `app.Lock()` |
| **API** | `app.Handle()`, `cluster.Call()` |
| **Lifecycle** | `OnActive`, `OnStandby`, `OnStart`, `OnStop`, `OnOwn`, `OnRelease` |

---

## Spec Documents

| Document | Description |
|----------|-------------|
| [01-architecture.md](01-architecture.md) | Component model, concepts, stack diagram |
| [02-app-api.md](02-app-api.md) | App API, modes, placement, patterns |
| [03-platform-api.md](03-platform-api.md) | Platform API, services, cross-app communication |
| [04-nats-design.md](04-nats-design.md) | NATS subject design, resources, authorization |
| [05-data-replication.md](05-data-replication.md) | Litestream, KV, distributed locks |
| [06-communication.md](06-communication.md) | RPC, events |
| [07-backends.md](07-backends.md) | Systemd, Docker, process backends |
| [08-observability.md](08-observability.md) | Audit, metrics, health, tracing |
| [09-cluster-patterns.md](09-cluster-patterns.md) | Active-passive, pool, ring patterns |
| [10-testing.md](10-testing.md) | Testing strategy, unit/E2E tests, validation |
| [11-deployment.md](11-deployment.md) | Distribution, installation, systemd, Docker, K8s |
| [12-implementation.md](12-implementation.md) | Implementation phases |
| [13-dynamic-placement.md](13-dynamic-placement.md) | Dynamic app movement, label-based placement, migrations |
| [14-convex-integration.md](14-convex-integration.md) | Convex cluster manager integration example |
| [15-service-discovery.md](15-service-discovery.md) | Service discovery, metadata exposure, integrations |
| [16-leaf-nodes.md](16-leaf-nodes.md) | NATS leaf nodes, multi-zone/multi-region deployments |

---

## License

Apache License 2.0
