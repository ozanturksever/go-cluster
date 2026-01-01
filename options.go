package cluster

import "time"

// PlatformOption configures a Platform.
type PlatformOption func(*platformOptions)

type platformOptions struct {
	labels        map[string]string
	natsCreds     string
	healthAddr    string
	metricsAddr   string
	leaseTTL      time.Duration
	heartbeat     time.Duration
	autoRebalance *AutoRebalanceConfig
}

func defaultPlatformOptions() *platformOptions {
	return &platformOptions{
		labels:      make(map[string]string),
		healthAddr:  ":8080",
		metricsAddr: ":9090",
		leaseTTL:    10 * time.Second,
		heartbeat:   3 * time.Second,
	}
}

// Labels sets node labels for placement decisions.
func Labels(labels map[string]string) PlatformOption {
	return func(o *platformOptions) {
		o.labels = labels
	}
}

// NATSCreds sets the NATS credentials file path.
func NATSCreds(path string) PlatformOption {
	return func(o *platformOptions) {
		o.natsCreds = path
	}
}

// HealthAddr sets the health check HTTP address.
func HealthAddr(addr string) PlatformOption {
	return func(o *platformOptions) {
		o.healthAddr = addr
	}
}

// MetricsAddr sets the Prometheus metrics HTTP address.
func MetricsAddr(addr string) PlatformOption {
	return func(o *platformOptions) {
		o.metricsAddr = addr
	}
}

// WithLeaseTTL sets the leader lease TTL.
func WithLeaseTTL(d time.Duration) PlatformOption {
	return func(o *platformOptions) {
		o.leaseTTL = d
	}
}

// WithHeartbeat sets the heartbeat interval.
func WithHeartbeat(d time.Duration) PlatformOption {
	return func(o *platformOptions) {
		o.heartbeat = d
	}
}

// AutoRebalanceConfig configures automatic app rebalancing.
type AutoRebalanceConfig struct {
	Enabled        bool
	Interval       time.Duration
	Threshold      float64
	MaxConcurrent  int
	CooldownPeriod time.Duration
	ExcludeApps    []string
	IncludeLabels  map[string]string
}

// WithAutoRebalance enables automatic app rebalancing.
func WithAutoRebalance(config AutoRebalanceConfig) PlatformOption {
	return func(o *platformOptions) {
		o.autoRebalance = &config
	}
}

// AppOption configures an App.
type AppOption func(*App)

// OnNodes constrains the app to run on specific nodes.
func OnNodes(nodes ...string) AppOption {
	return func(a *App) {
		a.nodeSelector = nodes
	}
}

// OnLabel constrains the app to run on nodes with the given label.
func OnLabel(key, value string) AppOption {
	return func(a *App) {
		a.labelSelector[key] = value
	}
}

// AvoidLabel prevents the app from running on nodes with the given label.
func AvoidLabel(key, value string) AppOption {
	return func(a *App) {
		a.avoidLabels[key] = value
	}
}

// WithApp constrains the app to run on nodes with the given app (co-location).
func WithApp(appName string) AppOption {
	return func(a *App) {
		a.withApps = append(a.withApps, appName)
	}
}

// AwayFromApp constrains the app to run on nodes without the given app (anti-affinity).
func AwayFromApp(appName string) AppOption {
	return func(a *App) {
		a.awayFromApps = append(a.awayFromApps, appName)
	}
}

// WithSQLite enables SQLite3 database with Litestream WAL replication.
func WithSQLite(path string, opts ...SQLiteOption) AppOption {
	return func(a *App) {
		a.sqlitePath = path
		for _, opt := range opts {
			opt(a)
		}
	}
}

// SQLiteOption configures SQLite replication.
type SQLiteOption func(*App)

// SQLiteReplica sets the replica database path.
func SQLiteReplica(path string) SQLiteOption {
	return func(a *App) {
		a.replicaPath = path
	}
}

// SQLiteSnapshotInterval sets the snapshot interval.
func SQLiteSnapshotInterval(d time.Duration) SQLiteOption {
	return func(a *App) {
		a.snapshotInterval = d
	}
}

// SQLiteRetention sets the WAL retention period.
func SQLiteRetention(d time.Duration) SQLiteOption {
	return func(a *App) {
		a.retention = d
	}
}

// ResourceSpec defines resource requirements for an app.
type ResourceSpec struct {
	CPU    int
	Memory string
	Disk   string
}

// Resources sets resource requirements for placement decisions.
func Resources(spec ResourceSpec) AppOption {
	return func(a *App) {
		// Store resource requirements for scheduler
	}
}

// ServiceConfig defines a service exposed by an app.
type ServiceConfig struct {
	Port        int
	Protocol    string
	Path        string
	HealthCheck *HealthCheckConfig
	Metadata    map[string]string
	Tags        []string
	Internal    bool
	Weight      int
}

// HealthCheckConfig defines health check settings for a service.
type HealthCheckConfig struct {
	Path           string
	Interval       time.Duration
	Timeout        time.Duration
	ExpectedStatus int
	ExpectedBody   string
}

// BackendOption configures a backend.
type BackendOption func(*backendOptions)

type backendOptions struct {
	healthEndpoint string
	healthInterval time.Duration
	healthTimeout  time.Duration
	startTimeout   time.Duration
}

// BackendHealthEndpoint sets the health check endpoint for the backend.
func BackendHealthEndpoint(url string) BackendOption {
	return func(o *backendOptions) {
		o.healthEndpoint = url
	}
}

// BackendHealthInterval sets the health check interval.
func BackendHealthInterval(d time.Duration) BackendOption {
	return func(o *backendOptions) {
		o.healthInterval = d
	}
}

// BackendHealthTimeout sets the health check timeout.
func BackendHealthTimeout(d time.Duration) BackendOption {
	return func(o *backendOptions) {
		o.healthTimeout = d
	}
}

// BackendStartTimeout sets the timeout for waiting for the backend to start.
func BackendStartTimeout(d time.Duration) BackendOption {
	return func(o *backendOptions) {
		o.startTimeout = d
	}
}

// VIP configures virtual IP failover.
func VIP(cidr, iface string) AppOption {
	return func(a *App) {
		// Store VIP configuration
	}
}

// WithBackend sets the backend controller for the app.
func WithBackend(backend Backend) AppOption {
	return func(a *App) {
		// Store backend
	}
}
