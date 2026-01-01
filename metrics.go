package cluster

import (
	"context"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics manages Prometheus metrics for the platform.
type Metrics struct {
	platform *Platform
	registry *prometheus.Registry
	server   *http.Server

	// Platform metrics
	Leader         *prometheus.GaugeVec
	MembersTotal   *prometheus.GaugeVec
	ElectionEpoch  *prometheus.GaugeVec

	// Replication metrics
	ReplicationLag *prometheus.GaugeVec
	WALBytesTotal  *prometheus.CounterVec
	SnapshotSize   *prometheus.GaugeVec

	// Lock metrics
	LockHeldSeconds *prometheus.HistogramVec

	// RPC metrics
	RPCDuration *prometheus.HistogramVec
	RPCTotal    *prometheus.CounterVec

	// Health metrics
	HealthStatus *prometheus.GaugeVec

	// Backend metrics
	BackendState    *prometheus.GaugeVec
	BackendHealth   *prometheus.GaugeVec
	BackendStarts   *prometheus.CounterVec
	BackendFailures *prometheus.CounterVec
	BackendRestarts *prometheus.CounterVec
	BackendUptime   *prometheus.GaugeVec

	// Cross-app communication metrics
	CrossAppCallDuration *prometheus.HistogramVec
	CrossAppCallTotal    *prometheus.CounterVec
	CircuitBreakerState  *prometheus.GaugeVec

	// Ring metrics
	RingPartitionsOwned  *prometheus.GaugeVec
	RingNodesTotal       *prometheus.GaugeVec
	RingRebalanceEvents  *prometheus.CounterVec

	// Placement metrics
	MigrationTotal           *prometheus.CounterVec
	MigrationDuration        *prometheus.HistogramVec
	MigrationInProgress      *prometheus.GaugeVec
	PlacementViolations      *prometheus.CounterVec
	PlacementPending         *prometheus.GaugeVec
	NodeCordonedTotal        *prometheus.GaugeVec
	NodeAppCount             *prometheus.GaugeVec

	// Service discovery metrics
	ServicesTotal              *prometheus.GaugeVec
	ServicesHealthy            *prometheus.GaugeVec
	ServiceDiscoveryLatency    *prometheus.HistogramVec
	ServiceRegistrationsTotal  *prometheus.CounterVec
	ServiceDeregistrationsTotal *prometheus.CounterVec
	ServiceHealthChecksTotal   *prometheus.CounterVec

	// Leaf node metrics
	LeafConnectionStatus    *prometheus.GaugeVec
	LeafConnectionsTotal    *prometheus.CounterVec
	LeafDisconnectionsTotal *prometheus.CounterVec
	LeafPartitionsDetected  *prometheus.CounterVec
	LeafLatency             *prometheus.GaugeVec
	LeafCount               *prometheus.GaugeVec
}

// NewMetrics creates a new metrics manager.
func NewMetrics(p *Platform) *Metrics {
	registry := prometheus.NewRegistry()

	m := &Metrics{
		platform: p,
		registry: registry,

		Leader: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_leader",
			Help: "1 if this node is the leader for the app",
		}, []string{"platform", "node", "app"}),

		MembersTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_members_total",
			Help: "Total number of cluster members",
		}, []string{"platform"}),

		ElectionEpoch: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_election_epoch",
			Help: "Current election epoch",
		}, []string{"platform", "app"}),

		ReplicationLag: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_replication_lag_seconds",
			Help: "WAL replication lag in seconds",
		}, []string{"platform", "node", "app"}),

		WALBytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_wal_bytes_total",
			Help: "Total WAL bytes sent",
		}, []string{"platform", "node", "app"}),

		SnapshotSize: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_snapshot_size_bytes",
			Help: "Last snapshot size in bytes",
		}, []string{"platform", "app"}),

		LockHeldSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gc_lock_held_seconds",
			Help:    "Lock hold time in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"platform", "app", "lock"}),

		RPCDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gc_rpc_duration_seconds",
			Help:    "RPC call duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"platform", "app", "method"}),

		RPCTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_rpc_total",
			Help: "Total RPC calls",
		}, []string{"platform", "app", "method", "status"}),

		HealthStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_health",
			Help: "Health check status (1=passing, 0=failing)",
		}, []string{"platform", "node", "check"}),

		BackendState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_backend_state",
			Help: "Backend state (0=stopped, 1=starting, 2=running, 3=stopping, 4=failed)",
		}, []string{"platform", "node", "app"}),

		BackendHealth: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_backend_health",
			Help: "Backend health status (1=healthy, 0=unhealthy)",
		}, []string{"platform", "node", "app"}),

		BackendStarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_backend_starts_total",
			Help: "Total number of backend starts",
		}, []string{"platform", "node", "app"}),

		BackendFailures: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_backend_failures_total",
			Help: "Total number of backend failures",
		}, []string{"platform", "node", "app"}),

		BackendRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_backend_restarts_total",
			Help: "Total number of backend restarts",
		}, []string{"platform", "node", "app"}),

		BackendUptime: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_backend_uptime_seconds",
			Help: "Backend uptime in seconds",
		}, []string{"platform", "node", "app"}),

		CrossAppCallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gc_cross_app_call_duration_seconds",
			Help:    "Cross-app API call duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 15),
		}, []string{"platform", "target_app", "method"}),

		CrossAppCallTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_cross_app_call_total",
			Help: "Total cross-app API calls",
		}, []string{"platform", "target_app", "method", "status"}),

		CircuitBreakerState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_circuit_breaker_state",
			Help: "Circuit breaker state (1=open, 0=closed)",
		}, []string{"platform", "target_app"}),

		RingPartitionsOwned: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_ring_partitions_owned",
			Help: "Number of partitions owned by this node",
		}, []string{"platform", "node", "app"}),

		RingNodesTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_ring_nodes_total",
			Help: "Total number of nodes in the ring",
		}, []string{"platform", "app"}),

		RingRebalanceEvents: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_ring_rebalance_events_total",
			Help: "Total ring rebalance events",
		}, []string{"platform", "app", "type"}),

		MigrationTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_migration_total",
			Help: "Total number of migrations",
		}, []string{"platform", "app", "reason", "status"}),

		MigrationDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gc_migration_duration_seconds",
			Help:    "Migration duration in seconds",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 12),
		}, []string{"platform", "app"}),

		MigrationInProgress: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_migration_in_progress",
			Help: "Number of migrations currently in progress",
		}, []string{"platform", "app"}),

		PlacementViolations: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_placement_constraint_violations_total",
			Help: "Total placement constraint violations",
		}, []string{"platform", "app"}),

		PlacementPending: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_placement_pending",
			Help: "Apps waiting for placement",
		}, []string{"platform", "app"}),

		NodeCordonedTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_node_cordoned",
			Help: "Whether node is cordoned (1=cordoned, 0=schedulable)",
		}, []string{"platform", "node"}),

		NodeAppCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_node_app_count",
			Help: "Number of apps running on each node",
		}, []string{"platform", "node"}),

		ServicesTotal: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_services_total",
			Help: "Total registered service instances",
		}, []string{"platform", "app", "service"}),

		ServicesHealthy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_services_healthy",
			Help: "Healthy service instances",
		}, []string{"platform", "app", "service"}),

		ServiceDiscoveryLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "gc_service_discovery_latency_seconds",
			Help:    "Service discovery query latency in seconds",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 12),
		}, []string{"platform", "app"}),

		ServiceRegistrationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_service_registrations_total",
			Help: "Total service registrations",
		}, []string{"platform", "app", "service"}),

		ServiceDeregistrationsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_service_deregistrations_total",
			Help: "Total service deregistrations",
		}, []string{"platform", "app", "service"}),

		ServiceHealthChecksTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_service_health_checks_total",
			Help: "Total service health check results",
		}, []string{"platform", "app", "service", "status"}),

		LeafConnectionStatus: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_leaf_connection_status",
			Help: "Leaf connection status (1=connected, 0=disconnected)",
		}, []string{"platform", "leaf_platform"}),

		LeafConnectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_leaf_connections_total",
			Help: "Total leaf connections",
		}, []string{"platform", "leaf_platform"}),

		LeafDisconnectionsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_leaf_disconnections_total",
			Help: "Total leaf disconnections",
		}, []string{"platform", "leaf_platform"}),

		LeafPartitionsDetected: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "gc_leaf_partitions_detected_total",
			Help: "Total partition events detected",
		}, []string{"platform", "leaf_platform"}),

		LeafLatency: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_leaf_latency_ms",
			Help: "Latency to leaf platform in milliseconds",
		}, []string{"platform", "leaf_platform"}),

		LeafCount: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "gc_leaf_count",
			Help: "Number of connected leaf platforms",
		}, []string{"platform"}),
	}

	// Register all metrics
	registry.MustRegister(
		m.Leader,
		m.MembersTotal,
		m.ElectionEpoch,
		m.ReplicationLag,
		m.WALBytesTotal,
		m.SnapshotSize,
		m.LockHeldSeconds,
		m.RPCDuration,
		m.RPCTotal,
		m.HealthStatus,
		m.BackendState,
		m.BackendHealth,
		m.BackendStarts,
		m.BackendFailures,
		m.BackendRestarts,
		m.BackendUptime,
		m.CrossAppCallDuration,
		m.CrossAppCallTotal,
		m.CircuitBreakerState,
		m.RingPartitionsOwned,
		m.RingNodesTotal,
		m.RingRebalanceEvents,
		m.MigrationTotal,
		m.MigrationDuration,
		m.MigrationInProgress,
		m.PlacementViolations,
		m.PlacementPending,
		m.NodeCordonedTotal,
		m.NodeAppCount,
		m.ServicesTotal,
		m.ServicesHealthy,
		m.ServiceDiscoveryLatency,
		m.ServiceRegistrationsTotal,
		m.ServiceDeregistrationsTotal,
		m.ServiceHealthChecksTotal,
		m.LeafConnectionStatus,
		m.LeafConnectionsTotal,
		m.LeafDisconnectionsTotal,
		m.LeafPartitionsDetected,
		m.LeafLatency,
		m.LeafCount,
	)

	// Also register default Go metrics
	registry.MustRegister(prometheus.NewGoCollector())
	registry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))

	return m
}

// Start begins serving the metrics endpoint.
func (m *Metrics) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))

	m.server = &http.Server{
		Addr:    m.platform.opts.metricsAddr,
		Handler: mux,
	}

	go func() {
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.server.Shutdown(shutdownCtx)
	}()

	return nil
}

// Stop stops the metrics server.
func (m *Metrics) Stop() {
	if m.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.server.Shutdown(ctx)
	}
}

// SetLeader updates the leader metric for an app.
func (m *Metrics) SetLeader(app string, isLeader bool) {
	val := 0.0
	if isLeader {
		val = 1.0
	}
	m.Leader.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(val)
}

// SetMembersTotal updates the member count metric.
func (m *Metrics) SetMembersTotal(count int) {
	m.MembersTotal.WithLabelValues(m.platform.name).Set(float64(count))
}

// SetElectionEpoch updates the election epoch metric for an app.
func (m *Metrics) SetElectionEpoch(app string, epoch uint64) {
	m.ElectionEpoch.WithLabelValues(m.platform.name, app).Set(float64(epoch))
}

// SetReplicationLag updates the replication lag metric.
func (m *Metrics) SetReplicationLag(app string, lag time.Duration) {
	m.ReplicationLag.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(lag.Seconds())
}

// AddWALBytes increments the WAL bytes counter.
func (m *Metrics) AddWALBytes(app string, bytes int64) {
	m.WALBytesTotal.WithLabelValues(m.platform.name, m.platform.nodeID, app).Add(float64(bytes))
}

// SetSnapshotSize updates the snapshot size metric.
func (m *Metrics) SetSnapshotSize(app string, size int64) {
	m.SnapshotSize.WithLabelValues(m.platform.name, app).Set(float64(size))
}

// ObserveLockHeld records a lock hold duration.
func (m *Metrics) ObserveLockHeld(app, lock string, duration time.Duration) {
	m.LockHeldSeconds.WithLabelValues(m.platform.name, app, lock).Observe(duration.Seconds())
}

// ObserveRPC records an RPC call duration.
func (m *Metrics) ObserveRPC(app, method string, duration time.Duration, err error) {
	m.RPCDuration.WithLabelValues(m.platform.name, app, method).Observe(duration.Seconds())
	status := "success"
	if err != nil {
		status = "error"
	}
	m.RPCTotal.WithLabelValues(m.platform.name, app, method, status).Inc()
}

// SetHealthStatus updates a health check status.
func (m *Metrics) SetHealthStatus(check string, passing bool) {
	val := 0.0
	if passing {
		val = 1.0
	}
	m.HealthStatus.WithLabelValues(m.platform.name, m.platform.nodeID, check).Set(val)
}

// ObserveCrossAppCall records a cross-app API call.
func (m *Metrics) ObserveCrossAppCall(targetApp, method string, duration time.Duration, err error) {
	m.CrossAppCallDuration.WithLabelValues(m.platform.name, targetApp, method).Observe(duration.Seconds())
	status := "success"
	if err != nil {
		status = "error"
	}
	m.CrossAppCallTotal.WithLabelValues(m.platform.name, targetApp, method, status).Inc()
}

// SetCircuitBreakerState updates the circuit breaker state metric.
func (m *Metrics) SetCircuitBreakerState(targetApp string, open bool) {
	val := 0.0
	if open {
		val = 1.0
	}
	m.CircuitBreakerState.WithLabelValues(m.platform.name, targetApp).Set(val)
}

// SetBackendState updates the backend state metric.
func (m *Metrics) SetBackendState(app string, state string) {
	// Map state to numeric value: stopped=0, starting=1, running=2, stopping=3, failed=4
	stateValues := map[string]float64{
		"stopped":  0,
		"starting": 1,
		"running":  2,
		"stopping": 3,
		"failed":   4,
	}
	val, ok := stateValues[state]
	if !ok {
		val = 0
	}
	m.BackendState.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(val)
}

// SetBackendHealth updates the backend health metric.
func (m *Metrics) SetBackendHealth(app string, healthy bool) {
	val := 0.0
	if healthy {
		val = 1.0
	}
	m.BackendHealth.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(val)
}

// IncBackendStarts increments the backend starts counter.
func (m *Metrics) IncBackendStarts(app string) {
	m.BackendStarts.WithLabelValues(m.platform.name, m.platform.nodeID, app).Inc()
}

// IncBackendFailures increments the backend failures counter.
func (m *Metrics) IncBackendFailures(app string) {
	m.BackendFailures.WithLabelValues(m.platform.name, m.platform.nodeID, app).Inc()
}

// IncBackendRestarts increments the backend restarts counter.
func (m *Metrics) IncBackendRestarts(app string) {
	m.BackendRestarts.WithLabelValues(m.platform.name, m.platform.nodeID, app).Inc()
}

// SetBackendUptime updates the backend uptime metric.
func (m *Metrics) SetBackendUptime(app string, uptime time.Duration) {
	m.BackendUptime.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(uptime.Seconds())
}

// SetRingPartitionsOwned updates the ring partitions owned metric.
func (m *Metrics) SetRingPartitionsOwned(app string, count int) {
	m.RingPartitionsOwned.WithLabelValues(m.platform.name, m.platform.nodeID, app).Set(float64(count))
}

// SetRingNodesTotal updates the ring nodes total metric.
func (m *Metrics) SetRingNodesTotal(app string, count int) {
	m.RingNodesTotal.WithLabelValues(m.platform.name, app).Set(float64(count))
}

// IncRingRebalanceEvents increments the ring rebalance events counter.
func (m *Metrics) IncRingRebalanceEvents(app string, eventType string) {
	m.RingRebalanceEvents.WithLabelValues(m.platform.name, app, eventType).Inc()
}

// IncMigration increments the migration counter.
func (m *Metrics) IncMigration(app, reason, status string) {
	m.MigrationTotal.WithLabelValues(m.platform.name, app, reason, status).Inc()
}

// ObserveMigrationDuration records a migration duration.
func (m *Metrics) ObserveMigrationDuration(app string, duration time.Duration) {
	m.MigrationDuration.WithLabelValues(m.platform.name, app).Observe(duration.Seconds())
}

// SetMigrationInProgress sets the number of in-progress migrations for an app.
func (m *Metrics) SetMigrationInProgress(app string, count int) {
	m.MigrationInProgress.WithLabelValues(m.platform.name, app).Set(float64(count))
}

// IncPlacementViolation increments the placement violation counter.
func (m *Metrics) IncPlacementViolation(app string) {
	m.PlacementViolations.WithLabelValues(m.platform.name, app).Inc()
}

// SetPlacementPending sets the pending placement status for an app.
func (m *Metrics) SetPlacementPending(app string, pending bool) {
	val := 0.0
	if pending {
		val = 1.0
	}
	m.PlacementPending.WithLabelValues(m.platform.name, app).Set(val)
}

// SetNodeCordoned sets the cordoned status for a node.
func (m *Metrics) SetNodeCordoned(nodeID string, cordoned bool) {
	val := 0.0
	if cordoned {
		val = 1.0
	}
	m.NodeCordonedTotal.WithLabelValues(m.platform.name, nodeID).Set(val)
}

// SetNodeAppCount sets the app count for a node.
func (m *Metrics) SetNodeAppCount(nodeID string, count int) {
	m.NodeAppCount.WithLabelValues(m.platform.name, nodeID).Set(float64(count))
}

// SetServiceTotal sets the total count of service instances.
func (m *Metrics) SetServiceTotal(app, service string, count int) {
	m.ServicesTotal.WithLabelValues(m.platform.name, app, service).Set(float64(count))
}

// SetServiceHealthy sets the healthy count of service instances.
func (m *Metrics) SetServiceHealthy(app, service string, count int) {
	m.ServicesHealthy.WithLabelValues(m.platform.name, app, service).Set(float64(count))
}

// ObserveServiceDiscoveryLatency records service discovery latency.
func (m *Metrics) ObserveServiceDiscoveryLatency(app string, duration time.Duration) {
	m.ServiceDiscoveryLatency.WithLabelValues(m.platform.name, app).Observe(duration.Seconds())
}

// IncServiceRegistration increments the service registration counter.
func (m *Metrics) IncServiceRegistration(app, service string) {
	m.ServiceRegistrationsTotal.WithLabelValues(m.platform.name, app, service).Inc()
}

// IncServiceDeregistration increments the service deregistration counter.
func (m *Metrics) IncServiceDeregistration(app, service string) {
	m.ServiceDeregistrationsTotal.WithLabelValues(m.platform.name, app, service).Inc()
}

// SetServiceHealth updates service health metric.
func (m *Metrics) SetServiceHealth(app, service string, healthy bool) {
	status := "failing"
	if healthy {
		status = "passing"
	}
	m.ServiceHealthChecksTotal.WithLabelValues(m.platform.name, app, service, status).Inc()
}

// SetLeafConnectionStatus updates the leaf connection status metric.
func (m *Metrics) SetLeafConnectionStatus(platform string, connected bool) {
	val := 0.0
	if connected {
		val = 1.0
	}
	m.LeafConnectionStatus.WithLabelValues(m.platform.name, platform).Set(val)
}

// IncLeafConnections increments the leaf connections counter.
func (m *Metrics) IncLeafConnections(platform string) {
	m.LeafConnectionsTotal.WithLabelValues(m.platform.name, platform).Inc()
}

// IncLeafDisconnections increments the leaf disconnections counter.
func (m *Metrics) IncLeafDisconnections(platform string) {
	m.LeafDisconnectionsTotal.WithLabelValues(m.platform.name, platform).Inc()
}

// IncLeafPartitionDetected increments the leaf partition detected counter.
func (m *Metrics) IncLeafPartitionDetected(platform string) {
	m.LeafPartitionsDetected.WithLabelValues(m.platform.name, platform).Inc()
}

// SetLeafLatency sets the latency to a leaf platform.
func (m *Metrics) SetLeafLatency(platform string, latency time.Duration) {
	m.LeafLatency.WithLabelValues(m.platform.name, platform).Set(latency.Seconds() * 1000) // in ms
}

// SetLeafCount sets the number of connected leaves.
func (m *Metrics) SetLeafCount(count int) {
	m.LeafCount.WithLabelValues(m.platform.name).Set(float64(count))
}
