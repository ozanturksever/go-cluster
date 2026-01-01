package cluster

import "time"

// PlatformOption configures a Platform.
type PlatformOption func(*platformOptions)

type platformOptions struct {
	labels          map[string]string
	natsCreds       string
	healthAddr      string
	metricsAddr     string
	leaseTTL        time.Duration
	heartbeat       time.Duration
	hookTimeout     time.Duration
	shutdownTimeout time.Duration
	autoRebalance   *AutoRebalanceConfig

	// Leaf node configuration
	leafHubConfig        *LeafHubConfig
	leafConnectionConfig *LeafConnectionConfig
	partitionStrategy    *PartitionStrategy
}

func defaultPlatformOptions() *platformOptions {
	return &platformOptions{
		labels:          make(map[string]string),
		healthAddr:      ":8080",
		metricsAddr:     ":9090",
		leaseTTL:        10 * time.Second,
		heartbeat:       3 * time.Second,
		hookTimeout:     30 * time.Second,
		shutdownTimeout: 30 * time.Second,
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

// WithHookTimeout sets the timeout for lifecycle hooks.
func WithHookTimeout(d time.Duration) PlatformOption {
	return func(o *platformOptions) {
		o.hookTimeout = d
	}
}

// WithShutdownTimeout sets the timeout for graceful shutdown.
func WithShutdownTimeout(d time.Duration) PlatformOption {
	return func(o *platformOptions) {
		o.shutdownTimeout = d
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

// WithLeafHub configures this platform as a hub that accepts leaf connections.
func WithLeafHub(config LeafHubConfig) PlatformOption {
	return func(o *platformOptions) {
		o.leafHubConfig = &config
	}
}

// WithLeafConnection configures this platform as a leaf connecting to a hub.
func WithLeafConnection(config LeafConnectionConfig) PlatformOption {
	return func(o *platformOptions) {
		o.leafConnectionConfig = &config
	}
}

// WithPartitionStrategy sets the partition handling strategy.
func WithPartitionStrategy(strategy PartitionStrategy) PlatformOption {
	return func(o *platformOptions) {
		o.partitionStrategy = &strategy
	}
}

// LabelPreference represents a soft label constraint with weight.
type LabelPreference struct {
	Key    string
	Value  string
	Weight int
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

// PreferLabel adds a soft constraint preferring nodes with the given label.
// Higher weight means stronger preference. Default weight is 10.
func PreferLabel(key, value string, weight ...int) AppOption {
	w := 10
	if len(weight) > 0 {
		w = weight[0]
	}
	return func(a *App) {
		a.preferLabels = append(a.preferLabels, LabelPreference{
			Key:    key,
			Value:  value,
			Weight: w,
		})
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

// Validate validates the service configuration.
func (c *ServiceConfig) Validate() error {
	if c.Port <= 0 || c.Port > 65535 {
		return &ServiceConfigError{Field: "Port", Message: "must be between 1 and 65535"}
	}

	validProtocols := map[string]bool{
		"tcp": true, "udp": true, "http": true, "https": true,
		"grpc": true, "ws": true, "wss": true,
	}
	if c.Protocol != "" && !validProtocols[c.Protocol] {
		return &ServiceConfigError{Field: "Protocol", Message: "must be one of: tcp, udp, http, https, grpc, ws, wss"}
	}

	if c.Weight < 0 {
		return &ServiceConfigError{Field: "Weight", Message: "must be non-negative"}
	}

	if c.HealthCheck != nil {
		if err := c.HealthCheck.Validate(); err != nil {
			return err
		}
	}

	return nil
}

// SetDefaults sets default values for unset fields.
func (c *ServiceConfig) SetDefaults() {
	if c.Protocol == "" {
		c.Protocol = "tcp"
	}
	if c.Weight == 0 {
		c.Weight = 100
	}
	if c.Metadata == nil {
		c.Metadata = make(map[string]string)
	}
	if c.HealthCheck != nil {
		c.HealthCheck.SetDefaults()
	}
}

// IsHTTPBased returns true if the protocol is HTTP-based.
func (c *ServiceConfig) IsHTTPBased() bool {
	return c.Protocol == "http" || c.Protocol == "https" || c.Protocol == "ws" || c.Protocol == "wss"
}

// ServiceConfigError represents a service configuration validation error.
type ServiceConfigError struct {
	Field   string
	Message string
}

func (e *ServiceConfigError) Error() string {
	return "service config: " + e.Field + " " + e.Message
}

// HealthCheckConfig defines health check settings for a service.
type HealthCheckConfig struct {
	Path           string
	Interval       time.Duration
	Timeout        time.Duration
	ExpectedStatus int
	ExpectedBody   string
}

// Validate validates the health check configuration.
func (c *HealthCheckConfig) Validate() error {
	if c.Interval < 0 {
		return &ServiceConfigError{Field: "HealthCheck.Interval", Message: "must be non-negative"}
	}
	if c.Timeout < 0 {
		return &ServiceConfigError{Field: "HealthCheck.Timeout", Message: "must be non-negative"}
	}
	if c.Timeout > 0 && c.Interval > 0 && c.Timeout > c.Interval {
		return &ServiceConfigError{Field: "HealthCheck.Timeout", Message: "must not exceed interval"}
	}
	return nil
}

// SetDefaults sets default values for unset fields.
func (c *HealthCheckConfig) SetDefaults() {
	if c.Interval == 0 {
		c.Interval = 10 * time.Second
	}
	if c.Timeout == 0 {
		c.Timeout = 3 * time.Second
	}
	if c.ExpectedStatus == 0 {
		c.ExpectedStatus = 200
	}
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
func WithBackend(b Backend) AppOption {
	return func(a *App) {
		a.backend = b
	}
}
