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
