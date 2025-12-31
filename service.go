package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/micro"
)

// StatusResponse represents the response from the status endpoint.
type StatusResponse struct {
	NodeID    string `json:"nodeId"`
	Role      string `json:"role"`
	Leader    string `json:"leader"`
	Epoch     int64  `json:"epoch"`
	UptimeMs  int64  `json:"uptimeMs"`
	Connected bool   `json:"connected"`
}

// PingResponse represents the response from the ping endpoint.
type PingResponse struct {
	OK        bool  `json:"ok"`
	Timestamp int64 `json:"timestamp"`
}

// StepdownResponse represents the response from the stepdown endpoint.
type StepdownResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// Service wraps a NATS micro service for cluster coordination.
type Service struct {
	cfg     Config
	logger  *slog.Logger
	nc      *nats.Conn
	service micro.Service

	mu        sync.RWMutex
	startedAt time.Time
	stopped   bool

	// Callbacks for getting node state and triggering actions
	getRole     func() Role
	getLeader   func() string
	getEpoch    func() int64
	isConnected func() bool
	doStepdown  func(context.Context) error
}

// ServiceCallbacks contains the callback functions for the service to query node state.
type ServiceCallbacks struct {
	GetRole     func() Role
	GetLeader   func() string
	GetEpoch    func() int64
	IsConnected func() bool
	DoStepdown  func(context.Context) error
}

// NewService creates a new cluster micro service.
func NewService(cfg Config, nc *nats.Conn, callbacks ServiceCallbacks) (*Service, error) {
	if nc == nil {
		return nil, fmt.Errorf("NATS connection is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		cfg:         cfg,
		logger:      logger.With("component", "service", "cluster", cfg.ClusterID, "node", cfg.NodeID),
		nc:          nc,
		getRole:     callbacks.GetRole,
		getLeader:   callbacks.GetLeader,
		getEpoch:    callbacks.GetEpoch,
		isConnected: callbacks.IsConnected,
		doStepdown:  callbacks.DoStepdown,
	}, nil
}

// Start starts the micro service and registers endpoints.
func (s *Service) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.service != nil {
		return ErrAlreadyStarted
	}

	// Create the micro service
	serviceName := s.cfg.ServiceName()
	srv, err := micro.AddService(s.nc, micro.Config{
		Name:        serviceName,
		Version:     s.cfg.ServiceVersion,
		Description: fmt.Sprintf("Cluster coordination service for %s", s.cfg.ClusterID),
	})
	if err != nil {
		return fmt.Errorf("failed to create micro service: %w", err)
	}

	// Add endpoints with node-specific subjects
	nodeID := s.cfg.NodeID

	// Status endpoint: cluster.<clusterID>.status.<nodeID>
	statusSubject := fmt.Sprintf("%s.status.%s", serviceName, nodeID)
	if err := srv.AddEndpoint("status", micro.HandlerFunc(s.handleStatus),
		micro.WithEndpointSubject(statusSubject),
	); err != nil {
		srv.Stop()
		return fmt.Errorf("failed to add status endpoint: %w", err)
	}

	// Ping endpoint: cluster.<clusterID>.ping.<nodeID>
	pingSubject := fmt.Sprintf("%s.ping.%s", serviceName, nodeID)
	if err := srv.AddEndpoint("ping", micro.HandlerFunc(s.handlePing),
		micro.WithEndpointSubject(pingSubject),
	); err != nil {
		srv.Stop()
		return fmt.Errorf("failed to add ping endpoint: %w", err)
	}

	// Stepdown endpoint: cluster.<clusterID>.control.<nodeID>.stepdown
	stepdownSubject := fmt.Sprintf("%s.control.%s.stepdown", serviceName, nodeID)
	if err := srv.AddEndpoint("stepdown", micro.HandlerFunc(s.handleStepdown),
		micro.WithEndpointSubject(stepdownSubject),
	); err != nil {
		srv.Stop()
		return fmt.Errorf("failed to add stepdown endpoint: %w", err)
	}

	s.service = srv
	s.startedAt = time.Now()
	s.stopped = false

	s.logger.Info("micro service started",
		"name", serviceName,
		"version", s.cfg.ServiceVersion,
		"status_subject", statusSubject,
		"ping_subject", pingSubject,
		"stepdown_subject", stepdownSubject,
	)

	return nil
}

// Stop stops the micro service.
func (s *Service) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.service == nil || s.stopped {
		return nil
	}

	if err := s.service.Stop(); err != nil {
		return fmt.Errorf("failed to stop micro service: %w", err)
	}

	s.stopped = true
	s.logger.Info("micro service stopped")

	return nil
}

// Info returns the service info.
func (s *Service) Info() micro.Info {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.service == nil {
		return micro.Info{}
	}

	return s.service.Info()
}

// Stats returns the service stats.
func (s *Service) Stats() micro.Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if s.service == nil {
		return micro.Stats{}
	}

	return s.service.Stats()
}

// handleStatus handles the status endpoint requests.
func (s *Service) handleStatus(req micro.Request) {
	s.mu.RLock()
	startedAt := s.startedAt
	s.mu.RUnlock()

	var uptimeMs int64
	if !startedAt.IsZero() {
		uptimeMs = time.Since(startedAt).Milliseconds()
	}

	resp := StatusResponse{
		NodeID:    s.cfg.NodeID,
		Role:      s.getRole().String(),
		Leader:    s.getLeader(),
		Epoch:     s.getEpoch(),
		UptimeMs:  uptimeMs,
		Connected: s.isConnected(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("failed to marshal status response", "error", err)
		req.Error("500", "internal error", nil)
		return
	}

	req.Respond(data)
}

// handlePing handles the ping endpoint requests.
func (s *Service) handlePing(req micro.Request) {
	resp := PingResponse{
		OK:        true,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("failed to marshal ping response", "error", err)
		req.Error("500", "internal error", nil)
		return
	}

	req.Respond(data)
}

// handleStepdown handles the stepdown endpoint requests.
func (s *Service) handleStepdown(req micro.Request) {
	ctx := context.Background()

	err := s.doStepdown(ctx)
	if err != nil {
		resp := StepdownResponse{
			OK:    false,
			Error: err.Error(),
		}

		data, _ := json.Marshal(resp)
		req.Respond(data)
		return
	}

	resp := StepdownResponse{
		OK: true,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("failed to marshal stepdown response", "error", err)
		req.Error("500", "internal error", nil)
		return
	}

	req.Respond(data)
}

// StatusSubject returns the subject for querying a node's status.
func StatusSubject(clusterID, nodeID string) string {
	return fmt.Sprintf("cluster_%s.status.%s", clusterID, nodeID)
}

// PingSubject returns the subject for pinging a node.
func PingSubject(clusterID, nodeID string) string {
	return fmt.Sprintf("cluster_%s.ping.%s", clusterID, nodeID)
}

// StepdownSubject returns the subject for triggering stepdown on a node.
func StepdownSubject(clusterID, nodeID string) string {
	return fmt.Sprintf("cluster_%s.control.%s.stepdown", clusterID, nodeID)
}
