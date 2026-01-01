package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

const (
	// Default health check settings
	defaultHealthCheckInterval = 10 * time.Second
	defaultHealthCheckTimeout  = 5 * time.Second
	defaultCircuitBreakerThreshold = 3
	defaultHealthHistorySize   = 10
)

// Health manages health checks for the platform.
type Health struct {
	platform *Platform

	checks       map[string]*healthCheckEntry
	lastStatus   HealthStatus
	statusChange time.Time
	mu           sync.RWMutex

	server *http.Server

	ctx    context.Context
	cancel context.CancelFunc
}

// healthCheckEntry wraps a health check with configuration and state.
type healthCheckEntry struct {
	check           HealthCheck
	interval        time.Duration
	timeout         time.Duration
	dependsOn       []string
	history         []CheckResult
	lastCheck       time.Time
	failureCount    int
	circuitOpen     bool
	circuitOpenedAt time.Time
}

// HealthCheck is a function that checks if a component is healthy.
type HealthCheck func(ctx context.Context) error

// HealthCheckOption configures a health check.
type HealthCheckOption func(*healthCheckEntry)

// HealthStatus represents the overall health status.
type HealthStatus struct {
	Status    string                 `json:"status"`
	Checks    map[string]CheckResult `json:"checks"`
	Timestamp time.Time              `json:"timestamp"`
}

// CheckResult represents the result of a single health check.
type CheckResult struct {
	Status       string    `json:"status"`
	LatencyMs    int64     `json:"latency_ms,omitempty"`
	Error        string    `json:"error,omitempty"`
	Timestamp    time.Time `json:"timestamp,omitempty"`
	CircuitOpen  bool      `json:"circuit_open,omitempty"`
	FailureCount int       `json:"failure_count,omitempty"`
}

// NewHealth creates a new health manager.
func NewHealth(p *Platform) *Health {
	return &Health{
		platform: p,
		checks:   make(map[string]*healthCheckEntry),
	}
}

// Register adds a health check with default settings.
func (h *Health) Register(name string, check HealthCheck, opts ...HealthCheckOption) {
	h.mu.Lock()
	defer h.mu.Unlock()

	entry := &healthCheckEntry{
		check:    check,
		interval: defaultHealthCheckInterval,
		timeout:  defaultHealthCheckTimeout,
		history:  make([]CheckResult, 0, defaultHealthHistorySize),
	}

	for _, opt := range opts {
		opt(entry)
	}

	h.checks[name] = entry
}

// WithHealthCheckInterval sets the interval for a health check.
func WithHealthCheckInterval(d time.Duration) HealthCheckOption {
	return func(e *healthCheckEntry) {
		e.interval = d
	}
}

// WithHealthCheckTimeout sets the timeout for a health check.
func WithHealthCheckTimeout(d time.Duration) HealthCheckOption {
	return func(e *healthCheckEntry) {
		e.timeout = d
	}
}

// WithHealthCheckDependsOn sets dependencies for a health check.
// A check will be skipped if any of its dependencies are failing.
func WithHealthCheckDependsOn(deps ...string) HealthCheckOption {
	return func(e *healthCheckEntry) {
		e.dependsOn = deps
	}
}

// Unregister removes a health check.
func (h *Health) Unregister(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.checks, name)
}

// Start begins serving health endpoints.
func (h *Health) Start(ctx context.Context) error {
	h.ctx, h.cancel = context.WithCancel(ctx)

	// Register default checks
	h.Register("nats", h.checkNATS)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.HandleFunc("/status", h.handleStatus)
	mux.HandleFunc("/health/history", h.handleHistory)

	h.server = &http.Server{
		Addr:    h.platform.opts.healthAddr,
		Handler: mux,
	}

	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
		}
	}()

	// Start background health check loop
	go h.backgroundCheckLoop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(shutdownCtx)
	}()

	return nil
}

// backgroundCheckLoop runs periodic health checks in the background.
func (h *Health) backgroundCheckLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return
		case <-ticker.C:
			h.runPeriodicChecks()
		}
	}
}

// runPeriodicChecks runs any health checks that are due.
func (h *Health) runPeriodicChecks() {
	h.mu.RLock()
	checks := make(map[string]*healthCheckEntry, len(h.checks))
	for name, entry := range h.checks {
		checks[name] = entry
	}
	h.mu.RUnlock()

	now := time.Now()
	for name, entry := range checks {
		if now.Sub(entry.lastCheck) >= entry.interval {
			h.runSingleCheck(name, entry)
		}
	}

	// Check for status changes and notify
	newStatus := h.Check(h.ctx)
	h.mu.Lock()
	oldStatus := h.lastStatus.Status
	if oldStatus != "" && oldStatus != newStatus.Status {
		h.statusChange = now
		h.notifyHealthChange(newStatus)
	}
	h.lastStatus = newStatus
	h.mu.Unlock()
}

// notifyHealthChange notifies apps about health status changes.
func (h *Health) notifyHealthChange(status HealthStatus) {
	h.platform.appsMu.RLock()
	defer h.platform.appsMu.RUnlock()

	for _, app := range h.platform.apps {
		if app.onHealthChange != nil {
			go func(a *App) {
				ctx, cancel := context.WithTimeout(h.ctx, h.platform.opts.hookTimeout)
				defer cancel()
				a.onHealthChange(ctx, status)
			}(app)
		}

		// Update service health based on app health
		go func(appName string) {
			ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
			defer cancel()
			h.platform.serviceRegistry.UpdateHealthFromAppHealth(ctx, appName, status.Status == "passing")
		}(app.name)
	}

	// Log audit event
	h.platform.audit.Log(h.ctx, AuditEntry{
		Category: "health",
		Action:   "status_changed",
		Data:     map[string]any{"old_status": h.lastStatus.Status, "new_status": status.Status},
	})
}

// runSingleCheck runs a single health check and updates its state.
func (h *Health) runSingleCheck(name string, entry *healthCheckEntry) {
	// Check circuit breaker
	if entry.circuitOpen {
		// Allow retry after circuit breaker timeout (3x interval)
		if time.Since(entry.circuitOpenedAt) < entry.interval*3 {
			return
		}
		// Half-open state - try once
	}

	ctx, cancel := context.WithTimeout(h.ctx, entry.timeout)
	defer cancel()

	start := time.Now()
	err := entry.check(ctx)
	latency := time.Since(start).Milliseconds()

	result := CheckResult{
		LatencyMs: latency,
		Timestamp: time.Now(),
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if err != nil {
		result.Status = "failing"
		result.Error = err.Error()
		entry.failureCount++

		// Open circuit breaker after threshold failures
		if entry.failureCount >= defaultCircuitBreakerThreshold {
			entry.circuitOpen = true
			entry.circuitOpenedAt = time.Now()
			result.CircuitOpen = true
		}
	} else {
		result.Status = "passing"
		entry.failureCount = 0
		entry.circuitOpen = false
	}

	result.FailureCount = entry.failureCount
	entry.lastCheck = time.Now()

	// Add to history, keeping only recent entries
	entry.history = append(entry.history, result)
	if len(entry.history) > defaultHealthHistorySize {
		entry.history = entry.history[1:]
	}

	// Update metrics
	h.platform.metrics.SetHealthStatus(name, err == nil)
}

// Stop stops the health server.
func (h *Health) Stop() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(ctx)
	}
}

// Check runs all health checks and returns the overall status.
func (h *Health) Check(ctx context.Context) HealthStatus {
	h.mu.RLock()
	checks := make(map[string]*healthCheckEntry, len(h.checks))
	for name, entry := range h.checks {
		checks[name] = entry
	}
	h.mu.RUnlock()

	results := make(map[string]CheckResult)
	allPassing := true

	// First pass: run checks without dependencies
	for name, entry := range checks {
		if len(entry.dependsOn) > 0 {
			continue
		}
		result := h.executeCheck(ctx, name, entry, results)
		results[name] = result
		if result.Status != "passing" {
			allPassing = false
		}
	}

	// Second pass: run checks with dependencies
	for name, entry := range checks {
		if len(entry.dependsOn) == 0 {
			continue
		}

		// Check if dependencies are passing
		depsOK := true
		for _, dep := range entry.dependsOn {
			if depResult, ok := results[dep]; ok && depResult.Status != "passing" {
				depsOK = false
				break
			}
		}

		if !depsOK {
			results[name] = CheckResult{
				Status:    "skipped",
				Error:     "dependency check failing",
				Timestamp: time.Now(),
			}
			allPassing = false
			continue
		}

		result := h.executeCheck(ctx, name, entry, results)
		results[name] = result
		if result.Status != "passing" {
			allPassing = false
		}
	}

	status := "passing"
	if !allPassing {
		status = "failing"
	}

	return HealthStatus{
		Status:    status,
		Checks:    results,
		Timestamp: time.Now(),
	}
}

// executeCheck runs a single health check.
func (h *Health) executeCheck(ctx context.Context, name string, entry *healthCheckEntry, results map[string]CheckResult) CheckResult {
	// Check circuit breaker
	if entry.circuitOpen {
		return CheckResult{
			Status:       "failing",
			Error:        "circuit breaker open",
			Timestamp:    time.Now(),
			CircuitOpen:  true,
			FailureCount: entry.failureCount,
		}
	}

	checkCtx, cancel := context.WithTimeout(ctx, entry.timeout)
	defer cancel()

	start := time.Now()
	err := entry.check(checkCtx)
	latency := time.Since(start).Milliseconds()

	result := CheckResult{
		LatencyMs:    latency,
		Timestamp:    time.Now(),
		FailureCount: entry.failureCount,
	}

	if err != nil {
		result.Status = "failing"
		result.Error = err.Error()
	} else {
		result.Status = "passing"
	}

	return result
}

// GetHistory returns the health check history for a specific check.
func (h *Health) GetHistory(name string) []CheckResult {
	h.mu.RLock()
	defer h.mu.RUnlock()

	entry, ok := h.checks[name]
	if !ok {
		return nil
	}

	history := make([]CheckResult, len(entry.history))
	copy(history, entry.history)
	return history
}

// GetAllHistory returns the health check history for all checks.
func (h *Health) GetAllHistory() map[string][]CheckResult {
	h.mu.RLock()
	defer h.mu.RUnlock()

	result := make(map[string][]CheckResult, len(h.checks))
	for name, entry := range h.checks {
		history := make([]CheckResult, len(entry.history))
		copy(history, entry.history)
		result[name] = history
	}
	return result
}

// ResetCircuitBreaker manually resets the circuit breaker for a check.
func (h *Health) ResetCircuitBreaker(name string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if entry, ok := h.checks[name]; ok {
		entry.circuitOpen = false
		entry.failureCount = 0
	}
}

// handleHealth handles the /health endpoint (liveness).
func (h *Health) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleReady handles the /ready endpoint (readiness).
func (h *Health) handleReady(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status := h.Check(ctx)

	w.Header().Set("Content-Type", "application/json")
	if status.Status == "passing" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(status)
}

// handleStatus handles the /status endpoint (detailed status).
func (h *Health) handleStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	status := h.Check(ctx)

	// Get apps info
	h.platform.appsMu.RLock()
	apps := make([]map[string]any, 0, len(h.platform.apps))
	for _, app := range h.platform.apps {
		appInfo := map[string]any{
			"name": app.name,
			"role": app.Role(),
		}
		if app.election != nil {
			appInfo["leader"] = app.election.Leader()
			appInfo["epoch"] = app.election.Epoch()
			appInfo["is_leader"] = app.IsLeader()
		}
		apps = append(apps, appInfo)
	}
	h.platform.appsMu.RUnlock()

	// Add platform info
	response := struct {
		Platform string           `json:"platform"`
		NodeID   string           `json:"node_id"`
		Health   HealthStatus     `json:"health"`
		Apps     []map[string]any `json:"apps"`
		Uptime   string           `json:"uptime,omitempty"`
	}{
		Platform: h.platform.name,
		NodeID:   h.platform.nodeID,
		Health:   status,
		Apps:     apps,
	}

	w.Header().Set("Content-Type", "application/json")
	if status.Status == "passing" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(response)
}

// handleHistory handles the /health/history endpoint.
func (h *Health) handleHistory(w http.ResponseWriter, r *http.Request) {
	checkName := r.URL.Query().Get("check")

	var history interface{}
	if checkName != "" {
		history = h.GetHistory(checkName)
	} else {
		history = h.GetAllHistory()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(history)
}

// checkNATS checks if NATS is connected.
func (h *Health) checkNATS(ctx context.Context) error {
	if !h.platform.nc.IsConnected() {
		return fmt.Errorf("NATS not connected")
	}
	return nil
}
