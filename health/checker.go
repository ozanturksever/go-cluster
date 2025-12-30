package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

type Config struct {
	ClusterID       string
	NodeID          string
	NATSURLs        []string
	NATSCredentials string
	Logger          *slog.Logger
}

func (c *Config) Validate() error {
	if c.ClusterID == "" {
		return fmt.Errorf("ClusterID is required")
	}
	if c.NodeID == "" {
		return fmt.Errorf("NodeID is required")
	}
	if len(c.NATSURLs) == 0 {
		return fmt.Errorf("at least one NATS URL is required")
	}
	return nil
}

type Response struct {
	NodeID    string         `json:"nodeId"`
	Role      string         `json:"role"`
	Leader    string         `json:"leader"`
	UptimeMs  int64          `json:"uptimeMs"`
	Timestamp int64          `json:"timestamp"`
	Custom    map[string]any `json:"custom,omitempty"`
}

type Checker struct {
	cfg       Config
	logger    *slog.Logger
	subject   string
	mu        sync.RWMutex
	role      string
	leader    string
	custom    map[string]any
	startedAt time.Time
	nc        *nats.Conn
	sub       *nats.Subscription
}

func NewChecker(cfg Config) (*Checker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Checker{
		cfg:     cfg,
		logger:  logger.With("component", "health", "cluster", cfg.ClusterID, "node", cfg.NodeID),
		subject: fmt.Sprintf("cluster.%s.health.%s", cfg.ClusterID, cfg.NodeID),
		custom:  make(map[string]any),
	}, nil
}

func (c *Checker) Start(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.nc != nil {
		return nil
	}

	opts := []nats.Option{
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
	}
	if c.cfg.NATSCredentials != "" {
		opts = append(opts, nats.UserCredentials(c.cfg.NATSCredentials))
	}

	nc, err := nats.Connect(c.cfg.NATSURLs[0], opts...)
	if err != nil {
		return fmt.Errorf("connect NATS: %w", err)
	}

	sub, err := nc.Subscribe(c.subject, c.handleRequest)
	if err != nil {
		nc.Close()
		return fmt.Errorf("subscribe health subject: %w", err)
	}

	c.nc = nc
	c.sub = sub
	c.startedAt = time.Now()

	c.logger.Info("health checker started", "subject", c.subject)
	return nil
}

func (c *Checker) Stop() {
	c.mu.Lock()
	sub := c.sub
	nc := c.nc
	c.sub = nil
	c.nc = nil
	c.mu.Unlock()

	if sub != nil {
		_ = sub.Unsubscribe()
	}
	if nc != nil {
		nc.Close()
	}
	c.logger.Info("health checker stopped")
}

func (c *Checker) SetRole(role string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.role = role
}

func (c *Checker) SetLeader(leader string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.leader = leader
}

func (c *Checker) SetCustom(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.custom[key] = value
}

func (c *Checker) QueryNode(ctx context.Context, nodeID string, timeout time.Duration) (Response, error) {
	if nodeID == "" {
		return Response{}, fmt.Errorf("nodeID is required")
	}

	subject := fmt.Sprintf("cluster.%s.health.%s", c.cfg.ClusterID, nodeID)
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c.mu.RLock()
	nc := c.nc
	c.mu.RUnlock()

	var msg *nats.Msg
	var err error

	if nc != nil && nc.IsConnected() {
		msg, err = nc.RequestWithContext(reqCtx, subject, nil)
	} else {
		opts := []nats.Option{nats.Timeout(timeout)}
		if c.cfg.NATSCredentials != "" {
			opts = append(opts, nats.UserCredentials(c.cfg.NATSCredentials))
		}
		tmp, errConn := nats.Connect(c.cfg.NATSURLs[0], opts...)
		if errConn != nil {
			return Response{}, fmt.Errorf("connect NATS: %w", errConn)
		}
		defer tmp.Close()
		msg, err = tmp.RequestWithContext(reqCtx, subject, nil)
	}

	if err != nil {
		return Response{}, err
	}

	var resp Response
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return Response{}, err
	}
	return resp, nil
}

func (c *Checker) handleRequest(msg *nats.Msg) {
	if msg.Reply == "" {
		return
	}

	resp := c.buildResponse()
	data, err := json.Marshal(resp)
	if err != nil {
		c.logger.Error("failed to marshal health response", "error", err)
		return
	}

	if err := msg.Respond(data); err != nil {
		c.logger.Error("failed to respond to health request", "error", err)
	}
}

func (c *Checker) buildResponse() Response {
	c.mu.RLock()
	defer c.mu.RUnlock()

	now := time.Now()
	var uptimeMs int64
	if !c.startedAt.IsZero() {
		uptimeMs = now.Sub(c.startedAt).Milliseconds()
	}

	custom := make(map[string]any, len(c.custom))
	for k, v := range c.custom {
		custom[k] = v
	}

	return Response{
		NodeID:    c.cfg.NodeID,
		Role:      c.role,
		Leader:    c.leader,
		UptimeMs:  uptimeMs,
		Timestamp: now.UnixMilli(),
		Custom:    custom,
	}
}
