package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ServiceInstance represents a discovered service instance.
type ServiceInstance struct {
	AppName      string            `json:"app_name"`
	Name         string            `json:"name"`
	NodeID       string            `json:"node_id"`
	Address      string            `json:"address"`
	Port         int               `json:"port"`
	Protocol     string            `json:"protocol"`
	Path         string            `json:"path,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	Tags         []string          `json:"tags,omitempty"`
	Health       string            `json:"health"`
	Weight       int               `json:"weight,omitempty"`
	Internal     bool              `json:"internal,omitempty"`
	RegisteredAt time.Time         `json:"registered_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// ServiceEventType represents the type of service event.
type ServiceEventType int

// Service event types.
const (
	// ServiceRegistered indicates a new service was registered.
	ServiceRegistered ServiceEventType = iota
	// ServiceDeregistered indicates a service was deregistered.
	ServiceDeregistered
	// ServiceUpdated indicates a service was updated.
	ServiceUpdated
	// ServiceHealthChanged indicates service health changed.
	ServiceHealthChanged
)

// String returns the string representation of the event type.
func (t ServiceEventType) String() string {
	switch t {
	case ServiceRegistered:
		return "registered"
	case ServiceDeregistered:
		return "deregistered"
	case ServiceUpdated:
		return "updated"
	case ServiceHealthChanged:
		return "health_changed"
	default:
		return "unknown"
	}
}

// ServiceEvent represents a service discovery event.
type ServiceEvent struct {
	Type    ServiceEventType
	Service ServiceInstance
}

// ServiceWatcher watches for service changes.
type ServiceWatcher struct {
	events chan ServiceEvent
	stop   chan struct{}
	done   chan struct{}
}

// Events returns the channel for receiving service events.
func (w *ServiceWatcher) Events() <-chan ServiceEvent {
	return w.events
}

// Close stops the watcher.
func (w *ServiceWatcher) Close() {
	close(w.stop)
	<-w.done
}

// ServiceRegistry manages service registration and discovery.
type ServiceRegistry struct {
	platform *Platform
	kv       jetstream.KeyValue

	// Local service tracking
	mu       sync.RWMutex
	services map[string]*ServiceInstance // key: "app/service/node"
	configs  map[string]*ServiceConfig   // key: "app/service/node"
	watchers []*serviceWatcherEntry

	// Health check management
	healthCheckers map[string]*serviceHealthChecker // key: "app/service/node"

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// serviceHealthChecker runs health checks for a specific service.
type serviceHealthChecker struct {
	registry *ServiceRegistry
	appName  string
	service  string
	config   *HealthCheckConfig
	instance *ServiceInstance

	ctx    context.Context
	cancel context.CancelFunc
}

type serviceWatcherEntry struct {
	appName string
	service string
	watcher *ServiceWatcher
}

// NewServiceRegistry creates a new service registry.
func NewServiceRegistry(p *Platform) *ServiceRegistry {
	return &ServiceRegistry{
		platform:       p,
		services:       make(map[string]*ServiceInstance),
		configs:        make(map[string]*ServiceConfig),
		watchers:       make([]*serviceWatcherEntry, 0),
		healthCheckers: make(map[string]*serviceHealthChecker),
	}
}

// Start initializes the service registry.
func (r *ServiceRegistry) Start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	bucketName := fmt.Sprintf("%s_services", r.platform.name)

	kv, err := r.platform.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Service registry for platform %s", r.platform.name),
		TTL:         60 * time.Second, // Services expire after 60s without heartbeat
		History:     1,
	})
	if err != nil {
		return fmt.Errorf("failed to create service registry KV bucket: %w", err)
	}
	r.kv = kv

	// Start watching for service changes
	r.wg.Add(1)
	go r.watchServices()

	// Start heartbeat for registered services
	r.wg.Add(1)
	go r.heartbeatLoop()

	return nil
}

// Stop stops the service registry.
func (r *ServiceRegistry) Stop() {
	// Stop all health checkers first
	r.mu.Lock()
	for _, checker := range r.healthCheckers {
		checker.cancel()
	}
	r.healthCheckers = make(map[string]*serviceHealthChecker)
	r.mu.Unlock()

	// Deregister all local services
	r.deregisterAllLocal()

	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()

	// Close all watchers
	r.mu.Lock()
	for _, entry := range r.watchers {
		close(entry.watcher.events)
		close(entry.watcher.done)
	}
	r.watchers = nil
	r.mu.Unlock()
}

// Register registers a service for an app.
func (r *ServiceRegistry) Register(ctx context.Context, appName string, serviceName string, config *ServiceConfig) error {
	if config == nil {
		return fmt.Errorf("service config is required")
	}

	// Set defaults and validate
	config.SetDefaults()
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid service config: %w", err)
	}

	// Get node address from membership or use localhost
	address := r.getNodeAddress()

	instance := &ServiceInstance{
		AppName:      appName,
		Name:         serviceName,
		NodeID:       r.platform.nodeID,
		Address:      address,
		Port:         config.Port,
		Protocol:     config.Protocol,
		Path:         config.Path,
		Metadata:     config.Metadata,
		Tags:         config.Tags,
		Health:       "passing",
		Weight:       config.Weight,
		Internal:     config.Internal,
		RegisteredAt: time.Now(),
		UpdatedAt:    time.Now(),
	}

	// Store locally
	key := r.serviceKey(appName, serviceName, r.platform.nodeID)
	r.mu.Lock()
	r.services[key] = instance
	r.configs[key] = config
	r.mu.Unlock()

	// Store in NATS KV
	if err := r.storeService(ctx, instance); err != nil {
		return fmt.Errorf("failed to store service: %w", err)
	}

	// Start health checker if configured
	if config.HealthCheck != nil {
		r.startHealthChecker(appName, serviceName, config.HealthCheck, instance)
	}

	// Update metrics
	r.platform.metrics.IncServiceRegistration(appName, serviceName)
	r.platform.metrics.SetServiceHealth(appName, serviceName, true)

	// Log audit
	r.platform.audit.Log(ctx, AuditEntry{
		Category: "service",
		Action:   "registered",
		Data: map[string]any{
			"app":      appName,
			"service":  serviceName,
			"port":     config.Port,
			"protocol": config.Protocol,
		},
	})

	return nil
}

// Deregister removes a service registration.
func (r *ServiceRegistry) Deregister(ctx context.Context, appName, serviceName string) error {
	key := r.serviceKey(appName, serviceName, r.platform.nodeID)

	// Stop health checker if running
	r.stopHealthChecker(key)

	r.mu.Lock()
	delete(r.services, key)
	delete(r.configs, key)
	r.mu.Unlock()

	// Remove from NATS KV
	kvKey := fmt.Sprintf("%s/%s/%s", appName, serviceName, r.platform.nodeID)
	if err := r.kv.Delete(ctx, kvKey); err != nil {
		// Ignore not found errors
		if err != jetstream.ErrKeyNotFound {
			return fmt.Errorf("failed to delete service: %w", err)
		}
	}

	// Update metrics
	r.platform.metrics.IncServiceDeregistration(appName, serviceName)

	// Log audit
	r.platform.audit.Log(ctx, AuditEntry{
		Category: "service",
		Action:   "deregistered",
		Data: map[string]any{
			"app":     appName,
			"service": serviceName,
		},
	})

	return nil
}

// UpdateHealth updates the health status of a service.
func (r *ServiceRegistry) UpdateHealth(ctx context.Context, appName, serviceName string, health string) error {
	key := r.serviceKey(appName, serviceName, r.platform.nodeID)

	r.mu.Lock()
	instance, ok := r.services[key]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("service not found: %s/%s", appName, serviceName)
	}
	instance.Health = health
	instance.UpdatedAt = time.Now()
	r.mu.Unlock()

	// Update in NATS KV
	if err := r.storeService(ctx, instance); err != nil {
		return fmt.Errorf("failed to update service health: %w", err)
	}

	// Update metrics
	r.platform.metrics.SetServiceHealth(appName, serviceName, health == "passing")

	return nil
}

// DiscoverServices discovers all instances of a service.
func (r *ServiceRegistry) DiscoverServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	start := time.Now()
	defer func() {
		r.platform.metrics.ObserveServiceDiscoveryLatency(appName, time.Since(start))
	}()

	prefix := fmt.Sprintf("%s/%s/", appName, serviceName)
	return r.discoverByPrefix(ctx, prefix)
}

// DiscoverServicesByTag discovers services by tag.
func (r *ServiceRegistry) DiscoverServicesByTag(ctx context.Context, tag string) ([]ServiceInstance, error) {
	start := time.Now()
	defer func() {
		r.platform.metrics.ObserveServiceDiscoveryLatency("_tag_"+tag, time.Since(start))
	}()

	// Get all services and filter by tag
	all, err := r.discoverByPrefix(ctx, "")
	if err != nil {
		return nil, err
	}

	var result []ServiceInstance
	for _, svc := range all {
		for _, t := range svc.Tags {
			if t == tag {
				result = append(result, svc)
				break
			}
		}
	}

	return result, nil
}

// DiscoverServicesByMetadata discovers services by metadata.
func (r *ServiceRegistry) DiscoverServicesByMetadata(ctx context.Context, metadata map[string]string) ([]ServiceInstance, error) {
	start := time.Now()
	defer func() {
		r.platform.metrics.ObserveServiceDiscoveryLatency("_metadata_", time.Since(start))
	}()

	// Get all services and filter by metadata
	all, err := r.discoverByPrefix(ctx, "")
	if err != nil {
		return nil, err
	}

	var result []ServiceInstance
	for _, svc := range all {
		if matchesMetadata(svc.Metadata, metadata) {
			result = append(result, svc)
		}
	}

	return result, nil
}

// WatchServices watches for changes to a specific service.
func (r *ServiceRegistry) WatchServices(ctx context.Context, appName, serviceName string) *ServiceWatcher {
	watcher := &ServiceWatcher{
		events: make(chan ServiceEvent, 100),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}

	entry := &serviceWatcherEntry{
		appName: appName,
		service: serviceName,
		watcher: watcher,
	}

	r.mu.Lock()
	r.watchers = append(r.watchers, entry)
	r.mu.Unlock()

	return watcher
}

// discoverByPrefix discovers services by key prefix.
func (r *ServiceRegistry) discoverByPrefix(ctx context.Context, prefix string) ([]ServiceInstance, error) {
	var instances []ServiceInstance

	// List all keys
	keys, err := r.kv.Keys(ctx)
	if err != nil {
		if err == jetstream.ErrNoKeysFound {
			return instances, nil
		}
		return nil, fmt.Errorf("failed to list service keys: %w", err)
	}

	for _, key := range keys {
		if prefix != "" && !strings.HasPrefix(key, prefix) {
			continue
		}

		entry, err := r.kv.Get(ctx, key)
		if err != nil {
			continue // Skip on error
		}

		var instance ServiceInstance
		if err := json.Unmarshal(entry.Value(), &instance); err != nil {
			continue
		}

		instances = append(instances, instance)
	}

	return instances, nil
}

// storeService stores a service instance in NATS KV.
func (r *ServiceRegistry) storeService(ctx context.Context, instance *ServiceInstance) error {
	data, err := json.Marshal(instance)
	if err != nil {
		return err
	}

	key := fmt.Sprintf("%s/%s/%s", instance.AppName, instance.Name, instance.NodeID)
	_, err = r.kv.Put(ctx, key, data)
	return err
}

// serviceKey generates a local service key.
func (r *ServiceRegistry) serviceKey(appName, serviceName, nodeID string) string {
	return fmt.Sprintf("%s/%s/%s", appName, serviceName, nodeID)
}

// getNodeAddress returns the address for this node.
func (r *ServiceRegistry) getNodeAddress() string {
	// Try to get from membership
	member, ok := r.platform.membership.GetMember(r.platform.nodeID)
	if ok && member.Address != "" {
		return member.Address
	}
	// Default to localhost
	return "127.0.0.1"
}

// deregisterAllLocal deregisters all locally registered services.
func (r *ServiceRegistry) deregisterAllLocal() {
	r.mu.RLock()
	services := make([]*ServiceInstance, 0, len(r.services))
	for _, svc := range r.services {
		services = append(services, svc)
	}
	r.mu.RUnlock()

	for _, svc := range services {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r.Deregister(ctx, svc.AppName, svc.Name)
		cancel()
	}
}

// heartbeatLoop refreshes service registrations periodically.
func (r *ServiceRegistry) heartbeatLoop() {
	defer r.wg.Done()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			r.refreshServices()
		}
	}
}

// refreshServices refreshes all local service registrations.
func (r *ServiceRegistry) refreshServices() {
	r.mu.RLock()
	services := make([]*ServiceInstance, 0, len(r.services))
	for _, svc := range r.services {
		services = append(services, svc)
	}
	r.mu.RUnlock()

	for _, svc := range services {
		svc.UpdatedAt = time.Now()
		ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
		r.storeService(ctx, svc)
		cancel()
	}
}

// watchServices watches for service changes in NATS KV.
func (r *ServiceRegistry) watchServices() {
	defer r.wg.Done()

	watcher, err := r.kv.WatchAll(r.ctx)
	if err != nil {
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case entry := <-watcher.Updates():
			if entry == nil {
				continue
			}

			r.handleServiceChange(entry)
		}
	}
}

// handleServiceChange processes a service change event.
func (r *ServiceRegistry) handleServiceChange(entry jetstream.KeyValueEntry) {
	key := entry.Key()
	parts := strings.Split(key, "/")
	if len(parts) != 3 {
		return
	}

	appName := parts[0]
	serviceName := parts[1]

	var event ServiceEvent

	if entry.Operation() == jetstream.KeyValueDelete {
		event = ServiceEvent{
			Type: ServiceDeregistered,
			Service: ServiceInstance{
				AppName: appName,
				Name:    serviceName,
				NodeID:  parts[2],
			},
		}
	} else {
		var instance ServiceInstance
		if err := json.Unmarshal(entry.Value(), &instance); err != nil {
			return
		}

		// Determine event type based on revision
		if entry.Revision() == 1 {
			event = ServiceEvent{Type: ServiceRegistered, Service: instance}
		} else {
			event = ServiceEvent{Type: ServiceUpdated, Service: instance}
		}
	}

	// Notify watchers
	r.notifyWatchers(appName, serviceName, event)

	// Update metrics
	r.updateServiceMetrics()
}

// notifyWatchers notifies relevant watchers of a service event.
func (r *ServiceRegistry) notifyWatchers(appName, serviceName string, event ServiceEvent) {
	r.mu.RLock()
	watchers := make([]*serviceWatcherEntry, len(r.watchers))
	copy(watchers, r.watchers)
	r.mu.RUnlock()

	for _, entry := range watchers {
		// Check if this watcher is interested
		if entry.appName != "" && entry.appName != appName {
			continue
		}
		if entry.service != "" && entry.service != serviceName {
			continue
		}

		// Non-blocking send
		select {
		case entry.watcher.events <- event:
		default:
			// Drop event if channel is full
		}
	}
}

// updateServiceMetrics updates service-related metrics.
func (r *ServiceRegistry) updateServiceMetrics() {
	ctx, cancel := context.WithTimeout(r.ctx, 5*time.Second)
	defer cancel()

	services, err := r.discoverByPrefix(ctx, "")
	if err != nil {
		return
	}

	// Count by app/service
	counts := make(map[string]int)
	healthy := make(map[string]int)

	for _, svc := range services {
		key := fmt.Sprintf("%s/%s", svc.AppName, svc.Name)
		counts[key]++
		if svc.Health == "passing" {
			healthy[key]++
		}
	}

	for key, count := range counts {
		parts := strings.Split(key, "/")
		if len(parts) == 2 {
			r.platform.metrics.SetServiceTotal(parts[0], parts[1], count)
			r.platform.metrics.SetServiceHealthy(parts[0], parts[1], healthy[key])
		}
	}
}

// matchesMetadata checks if service metadata contains all required key-value pairs.
func matchesMetadata(serviceMetadata, required map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	if serviceMetadata == nil {
		return false
	}
	for k, v := range required {
		if serviceMetadata[k] != v {
			return false
		}
	}
	return true
}

// ExportPrometheusTargets exports services in Prometheus file_sd format.
func (r *ServiceRegistry) ExportPrometheusTargets(ctx context.Context) ([]PrometheusTarget, error) {
	services, err := r.DiscoverServicesByTag(ctx, "prometheus")
	if err != nil {
		return nil, err
	}

	var targets []PrometheusTarget
	for _, svc := range services {
		target := PrometheusTarget{
			Targets: []string{fmt.Sprintf("%s:%d", svc.Address, svc.Port)},
			Labels: map[string]string{
				"job":      svc.Metadata["job"],
				"app":      svc.AppName,
				"service":  svc.Name,
				"node":     svc.NodeID,
				"platform": r.platform.name,
			},
		}
		if svc.Path != "" {
			target.Labels["__metrics_path__"] = svc.Path
		}
		targets = append(targets, target)
	}

	return targets, nil
}

// PrometheusTarget represents a Prometheus file_sd target.
type PrometheusTarget struct {
	Targets []string          `json:"targets"`
	Labels  map[string]string `json:"labels"`
}

// ExportConsulServices exports services in Consul-compatible format.
func (r *ServiceRegistry) ExportConsulServices(ctx context.Context) ([]ConsulService, error) {
	services, err := r.discoverByPrefix(ctx, "")
	if err != nil {
		return nil, err
	}

	var result []ConsulService
	for _, svc := range services {
		cs := ConsulService{
			ID:      fmt.Sprintf("%s-%s-%s", svc.AppName, svc.Name, svc.NodeID),
			Name:    svc.Name,
			Tags:    svc.Tags,
			Address: svc.Address,
			Port:    svc.Port,
			Meta:    svc.Metadata,
		}
		if svc.Health == "passing" {
			cs.Status = "passing"
		} else {
			cs.Status = "critical"
		}
		result = append(result, cs)
	}

	return result, nil
}

// ConsulService represents a Consul-compatible service.
type ConsulService struct {
	ID      string            `json:"ID"`
	Name    string            `json:"Service"`
	Tags    []string          `json:"Tags"`
	Address string            `json:"Address"`
	Port    int               `json:"Port"`
	Meta    map[string]string `json:"Meta"`
	Status  string            `json:"Status"`
}

// startHealthChecker starts a health checker for a service.
func (r *ServiceRegistry) startHealthChecker(appName, serviceName string, config *HealthCheckConfig, instance *ServiceInstance) {
	key := r.serviceKey(appName, serviceName, r.platform.nodeID)

	r.mu.Lock()
	defer r.mu.Unlock()

	// Stop existing checker if any
	if existing, ok := r.healthCheckers[key]; ok {
		existing.cancel()
	}

	ctx, cancel := context.WithCancel(r.ctx)
	checker := &serviceHealthChecker{
		registry: r,
		appName:  appName,
		service:  serviceName,
		config:   config,
		instance: instance,
		ctx:      ctx,
		cancel:   cancel,
	}

	r.healthCheckers[key] = checker
	r.wg.Add(1)
	go checker.run()
}

// stopHealthChecker stops the health checker for a service.
func (r *ServiceRegistry) stopHealthChecker(key string) {
	r.mu.Lock()
	checker, ok := r.healthCheckers[key]
	if ok {
		delete(r.healthCheckers, key)
	}
	r.mu.Unlock()

	if checker != nil {
		checker.cancel()
	}
}

// run executes the health check loop.
func (c *serviceHealthChecker) run() {
	defer c.registry.wg.Done()

	ticker := time.NewTicker(c.config.Interval)
	defer ticker.Stop()

	// Run initial check
	c.check()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.check()
		}
	}
}

// check performs a single health check.
func (c *serviceHealthChecker) check() {
	ctx, cancel := context.WithTimeout(c.ctx, c.config.Timeout)
	defer cancel()

	var healthy bool
	var err error

	// Determine check type based on protocol and config
	switch c.instance.Protocol {
	case "http", "https", "ws", "wss":
		// HTTP-based protocols get HTTP health checks
		healthy, err = c.checkHTTP(ctx)
	case "tcp", "grpc", "udp":
		// TCP-based protocols get TCP health checks
		healthy, err = c.checkTCP(ctx)
	default:
		// Default to TCP check for unknown protocols
		healthy, err = c.checkTCP(ctx)
	}

	// Update health status
	newStatus := "passing"
	if !healthy || err != nil {
		newStatus = "failing"
	}

	// Only update if status changed
	c.registry.mu.RLock()
	oldStatus := c.instance.Health
	c.registry.mu.RUnlock()

	if oldStatus != newStatus {
		updateCtx, updateCancel := context.WithTimeout(context.Background(), 5*time.Second)
		c.registry.UpdateHealth(updateCtx, c.appName, c.service, newStatus)
		updateCancel()

		// Log status change
		c.registry.platform.audit.Log(c.ctx, AuditEntry{
			Category: "service",
			Action:   "health_changed",
			Data: map[string]any{
				"app":        c.appName,
				"service":    c.service,
				"old_status": oldStatus,
				"new_status": newStatus,
				"error":      errToString(err),
			},
		})
	}
}

// checkHTTP performs an HTTP health check.
func (c *serviceHealthChecker) checkHTTP(ctx context.Context) (bool, error) {
	// Map ws/wss to http/https for health checks
	scheme := c.instance.Protocol
	switch scheme {
	case "ws":
		scheme = "http"
	case "wss":
		scheme = "https"
	}

	path := c.config.Path
	if path == "" {
		path = c.instance.Path
	}
	if path == "" {
		path = "/health"
	}

	url := fmt.Sprintf("%s://%s:%d%s", scheme, c.instance.Address, c.instance.Port, path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	client := &http.Client{
		Timeout: c.config.Timeout,
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	// Check status code
	if c.config.ExpectedStatus > 0 && resp.StatusCode != c.config.ExpectedStatus {
		return false, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	// Check body if expected
	if c.config.ExpectedBody != "" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return false, err
		}
		if !strings.Contains(string(body), c.config.ExpectedBody) {
			return false, fmt.Errorf("expected body not found")
		}
	}

	return true, nil
}

// checkTCP performs a TCP health check.
func (c *serviceHealthChecker) checkTCP(ctx context.Context) (bool, error) {
	addr := fmt.Sprintf("%s:%d", c.instance.Address, c.instance.Port)

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false, err
	}
	conn.Close()
	return true, nil
}

// errToString converts an error to string, handling nil.
func errToString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// GetServiceConfig returns the config for a locally registered service.
func (r *ServiceRegistry) GetServiceConfig(appName, serviceName string) *ServiceConfig {
	key := r.serviceKey(appName, serviceName, r.platform.nodeID)
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.configs[key]
}

// GetLocalServices returns all locally registered services.
func (r *ServiceRegistry) GetLocalServices() []ServiceInstance {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ServiceInstance, 0, len(r.services))
	for _, svc := range r.services {
		result = append(result, *svc)
	}
	return result
}

// UpdateHealthFromAppHealth updates service health based on app health status.
// This is called when the app's overall health changes.
func (r *ServiceRegistry) UpdateHealthFromAppHealth(ctx context.Context, appName string, appHealthy bool) {
	newStatus := "passing"
	if !appHealthy {
		newStatus = "failing"
	}

	r.mu.RLock()
	servicesToUpdate := make([]string, 0)
	for _, svc := range r.services {
		if svc.AppName == appName {
			servicesToUpdate = append(servicesToUpdate, svc.Name)
		}
	}
	r.mu.RUnlock()

	for _, serviceName := range servicesToUpdate {
		r.UpdateHealth(ctx, appName, serviceName, newStatus)
	}
}

// DiscoverHealthyServices discovers only healthy instances of a service.
func (r *ServiceRegistry) DiscoverHealthyServices(ctx context.Context, appName, serviceName string) ([]ServiceInstance, error) {
	all, err := r.DiscoverServices(ctx, appName, serviceName)
	if err != nil {
		return nil, err
	}

	result := make([]ServiceInstance, 0, len(all))
	for _, svc := range all {
		if svc.Health == "passing" {
			result = append(result, svc)
		}
	}
	return result, nil
}
