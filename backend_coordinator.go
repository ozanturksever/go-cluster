package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ozanturksever/go-cluster/backend"
)

// BackendState represents the current state of a backend.
type BackendState string

const (
	BackendStateStopped  BackendState = "stopped"
	BackendStateStarting BackendState = "starting"
	BackendStateRunning  BackendState = "running"
	BackendStateStopping BackendState = "stopping"
	BackendStateFailed   BackendState = "failed"
)

// BackendCoordinator manages backend lifecycle in coordination with cluster state.
// It ensures proper sequencing during leadership transitions:
//
// Becoming Leader:
//  1. Win election (handled by Election)
//  2. Final WAL catch-up (handled by DatabaseManager.Promote)
//  3. Promote replica DB (handled by DatabaseManager.Promote)
//  4. Acquire VIP (TODO: handled by VIPManager)
//  5. Start backend service
//  6. Wait for backend to be healthy
//  7. Start WAL replication (handled by DatabaseManager)
//
// Stepping Down:
//  1. Stop WAL replication
//  2. Stop backend service
//  3. Release VIP
//  4. Start passive replication
type BackendCoordinator struct {
	app     *App
	backend Backend

	mu            sync.RWMutex
	state         BackendState
	lastStartTime time.Time
	lastStopTime  time.Time
	lastError     error
	restartCount  int

	// Configuration
	startTimeout    time.Duration
	stopTimeout     time.Duration
	healthInterval  time.Duration
	maxRestarts     int
	restartCooldown time.Duration

	// Health monitoring
	healthCancel context.CancelFunc
	healthWg     sync.WaitGroup

	// Callbacks
	onStateChange func(oldState, newState BackendState)

	ctx    context.Context
	cancel context.CancelFunc
}

// BackendCoordinatorConfig configures the backend coordinator.
type BackendCoordinatorConfig struct {
	StartTimeout    time.Duration
	StopTimeout     time.Duration
	HealthInterval  time.Duration
	MaxRestarts     int
	RestartCooldown time.Duration
}

// DefaultBackendCoordinatorConfig returns default configuration.
func DefaultBackendCoordinatorConfig() BackendCoordinatorConfig {
	return BackendCoordinatorConfig{
		StartTimeout:    60 * time.Second,
		StopTimeout:     30 * time.Second,
		HealthInterval:  5 * time.Second,
		MaxRestarts:     3,
		RestartCooldown: 10 * time.Second,
	}
}

// NewBackendCoordinator creates a new backend coordinator.
func NewBackendCoordinator(app *App, backend Backend, config BackendCoordinatorConfig) *BackendCoordinator {
	return &BackendCoordinator{
		app:             app,
		backend:         backend,
		state:           BackendStateStopped,
		startTimeout:    config.StartTimeout,
		stopTimeout:     config.StopTimeout,
		healthInterval:  config.HealthInterval,
		maxRestarts:     config.MaxRestarts,
		restartCooldown: config.RestartCooldown,
	}
}

// OnStateChange sets a callback for state changes.
func (c *BackendCoordinator) OnStateChange(fn func(oldState, newState BackendState)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onStateChange = fn
}

// setState updates the state and calls the callback.
func (c *BackendCoordinator) setState(newState BackendState) {
	c.mu.Lock()
	oldState := c.state
	c.state = newState
	callback := c.onStateChange
	c.mu.Unlock()

	if callback != nil && oldState != newState {
		callback(oldState, newState)
	}
}

// Start starts the backend coordinator.
func (c *BackendCoordinator) Start(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)
	return nil
}

// Stop stops the backend coordinator and the backend if running.
func (c *BackendCoordinator) Stop() {
	if c.cancel != nil {
		c.cancel()
	}

	// Stop health monitoring
	c.stopHealthMonitoring()

	// Stop backend if running
	if c.State() == BackendStateRunning || c.State() == BackendStateStarting {
		ctx, cancel := context.WithTimeout(context.Background(), c.stopTimeout)
		defer cancel()
		c.StopBackend(ctx)
	}
}

// State returns the current backend state.
func (c *BackendCoordinator) State() BackendState {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.state
}

// LastError returns the last error encountered.
func (c *BackendCoordinator) LastError() error {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastError
}

// RestartCount returns the number of restarts.
func (c *BackendCoordinator) RestartCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.restartCount
}

// StartBackend starts the backend service.
// This should be called after database promotion is complete.
func (c *BackendCoordinator) StartBackend(ctx context.Context) error {
	c.mu.Lock()
	if c.state == BackendStateRunning {
		c.mu.Unlock()
		return nil
	}
	if c.state == BackendStateStarting {
		c.mu.Unlock()
		return fmt.Errorf("backend is already starting")
	}
	c.lastStartTime = time.Now()
	c.mu.Unlock()

	c.setState(BackendStateStarting)

	p := c.app.platform

	// Log backend start
	p.audit.Log(ctx, AuditEntry{
		Category: "backend",
		Action:   "starting",
		Data:     map[string]any{"app": c.app.name},
	})

	// Create timeout context
	startCtx, cancel := context.WithTimeout(ctx, c.startTimeout)
	defer cancel()

	// Start the backend
	if err := c.backend.Start(startCtx); err != nil {
		c.mu.Lock()
		c.lastError = err
		c.mu.Unlock()

		c.setState(BackendStateFailed)

		p.audit.Log(ctx, AuditEntry{
			Category: "backend",
			Action:   "start_failed",
			Data:     map[string]any{"app": c.app.name, "error": err.Error()},
		})

		// Update metrics
		p.metrics.SetBackendState(c.app.name, string(BackendStateFailed))

		return fmt.Errorf("failed to start backend: %w", err)
	}

	// Wait for backend to be healthy
	if err := c.waitForHealthy(startCtx); err != nil {
		c.mu.Lock()
		c.lastError = err
		c.mu.Unlock()

		c.setState(BackendStateFailed)

		// Try to stop the backend since it's not healthy
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		c.backend.Stop(stopCtx)
		stopCancel()

		p.audit.Log(ctx, AuditEntry{
			Category: "backend",
			Action:   "health_check_failed",
			Data:     map[string]any{"app": c.app.name, "error": err.Error()},
		})

		p.metrics.SetBackendState(c.app.name, string(BackendStateFailed))

		return fmt.Errorf("backend failed health check: %w", err)
	}

	c.mu.Lock()
	c.lastError = nil
	c.mu.Unlock()

	c.setState(BackendStateRunning)

	p.audit.Log(ctx, AuditEntry{
		Category: "backend",
		Action:   "started",
		Data:     map[string]any{"app": c.app.name},
	})

	// Update metrics
	p.metrics.SetBackendState(c.app.name, string(BackendStateRunning))
	p.metrics.IncBackendStarts(c.app.name)

	// Start health monitoring
	c.startHealthMonitoring()

	return nil
}

// StopBackend stops the backend service.
// This should be called before database demotion.
func (c *BackendCoordinator) StopBackend(ctx context.Context) error {
	c.mu.Lock()
	if c.state == BackendStateStopped {
		c.mu.Unlock()
		return nil
	}
	if c.state == BackendStateStopping {
		c.mu.Unlock()
		return fmt.Errorf("backend is already stopping")
	}
	c.lastStopTime = time.Now()
	c.mu.Unlock()

	c.setState(BackendStateStopping)

	p := c.app.platform

	// Stop health monitoring first
	c.stopHealthMonitoring()

	// Log backend stop
	p.audit.Log(ctx, AuditEntry{
		Category: "backend",
		Action:   "stopping",
		Data:     map[string]any{"app": c.app.name},
	})

	// Create timeout context
	stopCtx, cancel := context.WithTimeout(ctx, c.stopTimeout)
	defer cancel()

	// Stop the backend
	if err := c.backend.Stop(stopCtx); err != nil {
		c.mu.Lock()
		c.lastError = err
		c.mu.Unlock()

		p.audit.Log(ctx, AuditEntry{
			Category: "backend",
			Action:   "stop_warning",
			Data:     map[string]any{"app": c.app.name, "error": err.Error()},
		})
		// Continue anyway - best effort stop
	}

	c.setState(BackendStateStopped)

	p.audit.Log(ctx, AuditEntry{
		Category: "backend",
		Action:   "stopped",
		Data:     map[string]any{"app": c.app.name},
	})

	// Update metrics
	p.metrics.SetBackendState(c.app.name, string(BackendStateStopped))

	return nil
}

// waitForHealthy waits for the backend to become healthy.
func (c *BackendCoordinator) waitForHealthy(ctx context.Context) error {
	ticker := time.NewTicker(c.healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			health, err := c.backend.Health(ctx)
			if err != nil {
				continue
			}
			if health.Status == "running" {
				return nil
			}
		}
	}
}

// startHealthMonitoring starts background health monitoring.
func (c *BackendCoordinator) startHealthMonitoring() {
	var ctx context.Context
	ctx, c.healthCancel = context.WithCancel(c.ctx)

	c.healthWg.Add(1)
	go func() {
		defer c.healthWg.Done()
		c.healthMonitorLoop(ctx)
	}()
}

// stopHealthMonitoring stops background health monitoring.
func (c *BackendCoordinator) stopHealthMonitoring() {
	if c.healthCancel != nil {
		c.healthCancel()
		c.healthCancel = nil
	}
	c.healthWg.Wait()
}

// healthMonitorLoop monitors backend health and handles failures.
func (c *BackendCoordinator) healthMonitorLoop(ctx context.Context) {
	ticker := time.NewTicker(c.healthInterval)
	defer ticker.Stop()

	failureCount := 0
	const maxFailures = 3

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			health, err := c.backend.Health(ctx)
			if err != nil || health.Status != "running" {
				failureCount++
				if failureCount >= maxFailures {
					c.handleBackendFailure(ctx)
					failureCount = 0
				}
			} else {
				failureCount = 0
				// Update health metric
				c.app.platform.metrics.SetBackendHealth(c.app.name, true)
			}
		}
	}
}

// handleBackendFailure handles a backend failure.
func (c *BackendCoordinator) handleBackendFailure(ctx context.Context) {
	c.mu.Lock()
	c.restartCount++
	restartCount := c.restartCount
	c.mu.Unlock()

	c.setState(BackendStateFailed)

	p := c.app.platform

	p.audit.Log(ctx, AuditEntry{
		Category: "backend",
		Action:   "failure_detected",
		Data:     map[string]any{"app": c.app.name, "restart_count": restartCount},
	})

	// Update metrics
	p.metrics.SetBackendHealth(c.app.name, false)
	p.metrics.SetBackendState(c.app.name, string(BackendStateFailed))
	p.metrics.IncBackendFailures(c.app.name)

	// Check if we should attempt restart
	if restartCount <= c.maxRestarts {
		// Wait cooldown period
		select {
		case <-ctx.Done():
			return
		case <-time.After(c.restartCooldown):
		}

		p.audit.Log(ctx, AuditEntry{
			Category: "backend",
			Action:   "restart_attempt",
			Data:     map[string]any{"app": c.app.name, "attempt": restartCount},
		})

		// Try to restart
		if err := c.backend.Stop(ctx); err != nil {
			// Log but continue
		}

		// Update restart metrics
		p.metrics.IncBackendRestarts(c.app.name)

		if err := c.StartBackend(ctx); err != nil {
			p.audit.Log(ctx, AuditEntry{
				Category: "backend",
				Action:   "restart_failed",
				Data:     map[string]any{"app": c.app.name, "error": err.Error()},
			})
		}
	} else {
		p.audit.Log(ctx, AuditEntry{
			Category: "backend",
			Action:   "max_restarts_exceeded",
			Data:     map[string]any{"app": c.app.name, "max": c.maxRestarts},
		})
	}
}

// Health returns the current health of the backend.
func (c *BackendCoordinator) Health(ctx context.Context) (backend.Health, error) {
	return c.backend.Health(ctx)
}

// Backend returns the underlying backend.
func (c *BackendCoordinator) Backend() Backend {
	return c.backend
}

// Uptime returns the backend uptime since last start.
func (c *BackendCoordinator) Uptime() time.Duration {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.state != BackendStateRunning || c.lastStartTime.IsZero() {
		return 0
	}
	return time.Since(c.lastStartTime)
}

// LastStartTime returns when the backend was last started.
func (c *BackendCoordinator) LastStartTime() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastStartTime
}

// LastStopTime returns when the backend was last stopped.
func (c *BackendCoordinator) LastStopTime() time.Time {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.lastStopTime
}
