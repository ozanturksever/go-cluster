package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Health manages health checks for the platform.
type Health struct {
	platform *Platform

	checks map[string]HealthCheck
	mu     sync.RWMutex

	server *http.Server
}

// HealthCheck is a function that checks if a component is healthy.
type HealthCheck func(ctx context.Context) error

// HealthStatus represents the overall health status.
type HealthStatus struct {
	Status  string                  `json:"status"`
	Checks  map[string]CheckResult  `json:"checks"`
}

// CheckResult represents the result of a single health check.
type CheckResult struct {
	Status    string `json:"status"`
	LatencyMs int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// NewHealth creates a new health manager.
func NewHealth(p *Platform) *Health {
	return &Health{
		platform: p,
		checks:   make(map[string]HealthCheck),
	}
}

// Register adds a health check.
func (h *Health) Register(name string, check HealthCheck) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.checks[name] = check
}

// Start begins serving health endpoints.
func (h *Health) Start(ctx context.Context) error {
	// Register default checks
	h.Register("nats", h.checkNATS)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/ready", h.handleReady)
	mux.HandleFunc("/status", h.handleStatus)

	h.server = &http.Server{
		Addr:    h.platform.opts.healthAddr,
		Handler: mux,
	}

	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			// Log error but don't crash
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(shutdownCtx)
	}()

	return nil
}

// Stop stops the health server.
func (h *Health) Stop() {
	if h.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		h.server.Shutdown(ctx)
	}
}

// Check runs all health checks and returns the overall status.
func (h *Health) Check(ctx context.Context) HealthStatus {
	h.mu.RLock()
	checks := make(map[string]HealthCheck, len(h.checks))
	for name, check := range h.checks {
		checks[name] = check
	}
	h.mu.RUnlock()

	results := make(map[string]CheckResult)
	allPassing := true

	for name, check := range checks {
		start := time.Now()
		err := check(ctx)
		latency := time.Since(start).Milliseconds()

		if err != nil {
			results[name] = CheckResult{
				Status:    "failing",
				LatencyMs: latency,
				Error:     err.Error(),
			}
			allPassing = false
		} else {
			results[name] = CheckResult{
				Status:    "passing",
				LatencyMs: latency,
			}
		}
	}

	status := "passing"
	if !allPassing {
		status = "failing"
	}

	return HealthStatus{
		Status: status,
		Checks: results,
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

	// Add platform info
	response := struct {
		Platform string       `json:"platform"`
		NodeID   string       `json:"node_id"`
		Health   HealthStatus `json:"health"`
	}{
		Platform: h.platform.name,
		NodeID:   h.platform.nodeID,
		Health:   status,
	}

	w.Header().Set("Content-Type", "application/json")
	if status.Status == "passing" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}
	json.NewEncoder(w).Encode(response)
}

// checkNATS checks if NATS is connected.
func (h *Health) checkNATS(ctx context.Context) error {
	if !h.platform.nc.IsConnected() {
		return fmt.Errorf("NATS not connected")
	}
	return nil
}
