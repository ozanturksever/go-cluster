# Service Discovery & Metadata

Apps can expose services with metadata for discovery by other apps, monitoring systems, and external integrations.

---

## Overview

Service discovery allows apps to:

- **Expose endpoints** with metadata (ports, protocols, paths)
- **Advertise capabilities** for other apps to discover
- **Enable external integrations** (log collectors, monitoring agents, sidecars)
- **Dynamic configuration** for service mesh, load balancers, DNS

---

## Exposing Services

### Basic Service Registration

```go
app := cluster.App("convex",
    cluster.Singleton(),
    cluster.WithSQLite("/data/convex.db"),
)

// Expose HTTP API
app.ExposeService("http", ServiceConfig{
    Port:     3210,
    Protocol: "http",
    Path:     "/",
    Metadata: map[string]string{
        "version": "1.0.0",
        "docs":    "/api/docs",
    },
})

// Expose health endpoint
app.ExposeService("health", ServiceConfig{
    Port:     3210,
    Protocol: "http",
    Path:     "/health",
})
```

### Common Service Types

```go
// Syslog collection endpoint
app.ExposeService("syslog", ServiceConfig{
    Port:     1514,
    Protocol: "tcp",
    Metadata: map[string]string{
        "format":   "rfc5424",
        "tls":      "optional",
        "max_size": "64KB",
    },
})

// Metrics endpoint (Prometheus)
app.ExposeService("metrics", ServiceConfig{
    Port:     9090,
    Protocol: "http",
    Path:     "/metrics",
    Metadata: map[string]string{
        "format": "prometheus",
        "scrape_interval": "15s",
    },
})

// gRPC API
app.ExposeService("grpc", ServiceConfig{
    Port:     50051,
    Protocol: "grpc",
    Metadata: map[string]string{
        "reflection": "true",
        "tls":        "required",
    },
})

// Database connection (for admin tools)
app.ExposeService("postgres-wire", ServiceConfig{
    Port:     5432,
    Protocol: "tcp",
    Metadata: map[string]string{
        "wire_protocol": "postgres",
        "auth":          "md5",
    },
})

// WebSocket endpoint
app.ExposeService("websocket", ServiceConfig{
    Port:     8080,
    Protocol: "ws",
    Path:     "/ws",
    Metadata: map[string]string{
        "subprotocol": "graphql-ws",
    },
})
```

---

## Service Configuration

### ServiceConfig Structure

```go
type ServiceConfig struct {
    // Port number the service listens on
    Port int
    
    // Protocol: http, https, tcp, udp, grpc, ws, wss
    Protocol string
    
    // Path for HTTP-based services (optional)
    Path string
    
    // Health check configuration
    HealthCheck *HealthCheckConfig
    
    // Arbitrary metadata for discovery
    Metadata map[string]string
    
    // Tags for filtering/grouping
    Tags []string
    
    // Whether this service is internal-only
    Internal bool
    
    // Load balancing weight (for Spread/Pool apps)
    Weight int
}

type HealthCheckConfig struct {
    // Health check endpoint (default: use service endpoint)
    Path     string
    Interval time.Duration
    Timeout  time.Duration
    
    // Expected response for health
    ExpectedStatus int      // HTTP status code
    ExpectedBody   string   // Substring match
}
```

### Advanced Configuration

```go
app.ExposeService("api", ServiceConfig{
    Port:     8080,
    Protocol: "https",
    Path:     "/api/v1",
    HealthCheck: &HealthCheckConfig{
        Path:           "/health",
        Interval:       10 * time.Second,
        Timeout:        3 * time.Second,
        ExpectedStatus: 200,
    },
    Metadata: map[string]string{
        "version":      "2.1.0",
        "min_version":  "2.0.0",
        "rate_limit":   "1000/min",
        "auth":         "bearer",
        "content_type": "application/json",
    },
    Tags: []string{"public", "versioned", "rate-limited"},
    Weight: 100,
})
```

---

## Discovering Services

### Query Services from Other Apps

```go
// Discover all instances of an app's service
services, err := platform.DiscoverServices(ctx, "convex", "http")
for _, svc := range services {
    fmt.Printf("Node: %s, Endpoint: %s:%d%s\n", 
        svc.NodeID, svc.Address, svc.Port, svc.Path)
    fmt.Printf("Metadata: %v\n", svc.Metadata)
}

// Discover by tag
services, err := platform.DiscoverServicesByTag(ctx, "metrics")
for _, svc := range services {
    fmt.Printf("App: %s, Node: %s, Endpoint: %s:%d\n",
        svc.AppName, svc.NodeID, svc.Address, svc.Port)
}

// Discover by metadata
services, err := platform.DiscoverServicesByMetadata(ctx, map[string]string{
    "format": "prometheus",
})
```

### Watch for Service Changes

```go
// Watch for service registration/deregistration
watcher := platform.WatchServices(ctx, "convex", "http")
for event := range watcher.Events() {
    switch event.Type {
    case ServiceRegistered:
        fmt.Printf("New service: %s on %s:%d\n", 
            event.Service.Name, event.Service.Address, event.Service.Port)
    case ServiceDeregistered:
        fmt.Printf("Service removed: %s\n", event.Service.Name)
    case ServiceUpdated:
        fmt.Printf("Service updated: %s\n", event.Service.Name)
    }
}
```

---

## Integration Examples

### Syslog Collection

```go
// App exposes syslog endpoint
app.ExposeService("syslog", ServiceConfig{
    Port:     1514,
    Protocol: "tcp",
    Metadata: map[string]string{
        "format":      "rfc5424",
        "facility":    "local0",
        "app_name":    "convex",
        "structured":  "true",
    },
    Tags: []string{"logging", "syslog"},
})

// Log collector discovers and connects
services, _ := platform.DiscoverServicesByTag(ctx, "syslog")
for _, svc := range services {
    collector.AddTarget(svc.Address, svc.Port, svc.Metadata)
}
```

### Prometheus Scraping

```go
// App exposes metrics
app.ExposeService("metrics", ServiceConfig{
    Port:     9090,
    Protocol: "http",
    Path:     "/metrics",
    Metadata: map[string]string{
        "format":          "prometheus",
        "scrape_interval": "15s",
        "job":             "convex-backend",
    },
    Tags: []string{"monitoring", "prometheus"},
})

// Generate Prometheus scrape config from discovery
services, _ := platform.DiscoverServicesByTag(ctx, "prometheus")
for _, svc := range services {
    config.ScrapeConfigs = append(config.ScrapeConfigs, ScrapeConfig{
        JobName:        svc.Metadata["job"],
        StaticConfigs:  []StaticConfig{{Targets: []string{fmt.Sprintf("%s:%d", svc.Address, svc.Port)}}},
        MetricsPath:    svc.Path,
        ScrapeInterval: svc.Metadata["scrape_interval"],
    })
}
```

### Service Mesh / Envoy Sidecar

```go
// App exposes services for Envoy discovery
app.ExposeService("http", ServiceConfig{
    Port:     8080,
    Protocol: "http",
    Metadata: map[string]string{
        "envoy.lb_policy":        "round_robin",
        "envoy.circuit_breaker":  "true",
        "envoy.retry_policy":     "5xx",
        "envoy.timeout":          "30s",
    },
})

// Envoy xDS server reads from platform discovery
func (s *xDSServer) FetchEndpoints(ctx context.Context, cluster string) []Endpoint {
    services, _ := platform.DiscoverServices(ctx, cluster, "http")
    var endpoints []Endpoint
    for _, svc := range services {
        endpoints = append(endpoints, Endpoint{
            Address: svc.Address,
            Port:    svc.Port,
            Metadata: svc.Metadata,
        })
    }
    return endpoints
}
```

### DNS-SD (DNS Service Discovery)

```go
// Platform can expose services via DNS
platform := cluster.NewPlatform("prod", nodeID, natsURL,
    cluster.WithDNSDiscovery(DNSConfig{
        Domain:     "cluster.local",
        TTL:        30 * time.Second,
        ListenAddr: ":5353",
    }),
)

// Services become queryable via DNS
// _http._tcp.convex.prod.cluster.local → SRV records
// convex.prod.cluster.local            → A/AAAA records
```

---

## NATS Subject Design

Services are registered in a dedicated KV bucket:

```
Platform: prod
└── KV: prod_services
    ├── convex/http/node-1     → ServiceRecord{...}
    ├── convex/http/node-2     → ServiceRecord{...}
    ├── convex/syslog/node-1   → ServiceRecord{...}
    ├── cache/http/node-1      → ServiceRecord{...}
    └── cache/http/node-2      → ServiceRecord{...}
```

**ServiceRecord structure:**

```json
{
  "app": "convex",
  "name": "http",
  "node": "node-1",
  "address": "10.0.1.10",
  "port": 3210,
  "protocol": "http",
  "path": "/",
  "metadata": {
    "version": "1.0.0"
  },
  "tags": ["public"],
  "health": "passing",
  "registered_at": "2024-01-15T10:30:00Z",
  "updated_at": "2024-01-15T10:35:00Z"
}
```

---

## CLI Commands

```bash
# List all services
go-cluster services list
# APP      SERVICE   NODE     ADDRESS        PORT   PROTOCOL  HEALTH
# convex   http      node-1   10.0.1.10      3210   http      passing
# convex   http      node-2   10.0.1.11      3210   http      passing
# convex   syslog    node-1   10.0.1.10      1514   tcp       passing
# cache    http      node-1   10.0.1.10      8080   http      passing

# List services for specific app
go-cluster services list --app convex

# List services by tag
go-cluster services list --tag prometheus

# Show service details
go-cluster services show convex http
# Service: convex/http
# Instances: 2
# 
# node-1:
#   Address:  10.0.1.10:3210
#   Protocol: http
#   Path:     /
#   Health:   passing
#   Metadata:
#     version: 1.0.0
#     docs: /api/docs

# Export for Prometheus
go-cluster services export --format prometheus > /etc/prometheus/targets.json

# Export for Consul
go-cluster services export --format consul

# Watch for changes
go-cluster services watch --app convex
```

---

## Platform API

```go
type Platform interface {
    // ... existing methods ...
    
    // Service discovery
    DiscoverServices(ctx context.Context, app, service string) ([]ServiceInstance, error)
    DiscoverServicesByTag(ctx context.Context, tag string) ([]ServiceInstance, error)
    DiscoverServicesByMetadata(ctx context.Context, metadata map[string]string) ([]ServiceInstance, error)
    WatchServices(ctx context.Context, app, service string) ServiceWatcher
    
    // Service registration (called by apps internally)
    RegisterService(ctx context.Context, app string, config ServiceConfig) error
    DeregisterService(ctx context.Context, app, service string) error
    UpdateServiceHealth(ctx context.Context, app, service string, health HealthStatus) error
}

type ServiceInstance struct {
    AppName    string
    Name       string
    NodeID     string
    Address    string
    Port       int
    Protocol   string
    Path       string
    Metadata   map[string]string
    Tags       []string
    Health     HealthStatus
    Weight     int
}

type ServiceWatcher interface {
    Events() <-chan ServiceEvent
    Close()
}

type ServiceEvent struct {
    Type    ServiceEventType  // Registered, Deregistered, Updated, HealthChanged
    Service ServiceInstance
}
```

---

## Metrics

```
# Service discovery metrics
gc_services_total{app,service}                    # Total registered services
gc_services_healthy{app,service}                  # Healthy service instances
gc_service_discovery_latency_seconds{app}         # Discovery query latency
gc_service_registrations_total{app,service}       # Registration count
gc_service_deregistrations_total{app,service}     # Deregistration count
gc_service_health_checks_total{app,service,status} # Health check results
```

---

## Implementation Phase

Add to `12-implementation.md`:

```markdown
## Phase 7: Service Discovery

- [ ] ServiceConfig struct and validation
- [ ] `app.ExposeService()` API
- [ ] NATS KV bucket for service records
- [ ] Service registration on app start
- [ ] Service deregistration on app stop
- [ ] Health check integration
- [ ] `platform.DiscoverServices()` API
- [ ] `platform.WatchServices()` API
- [ ] CLI: `services list`, `services show`, `services export`
- [ ] DNS-SD integration (optional)
- [ ] **E2E: service discovery**
- [ ] **validate-discovery.sh**
```
