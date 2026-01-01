package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// APIClient provides cross-app communication via the API bus.
type APIClient struct {
	platform *Platform
	appName  string

	// Configuration
	timeout     time.Duration
	retryConfig *RetryConfig

	// Circuit breaker state
	mu              sync.RWMutex
	failures        int
	lastFailure     time.Time
	circuitOpen     bool
	circuitOpenTime time.Time
}

// RetryConfig configures retry behavior for API calls.
type RetryConfig struct {
	MaxRetries  int
	InitialWait time.Duration
	MaxWait     time.Duration
	Multiplier  float64
}

// CallOption configures an API call.
type CallOption func(*callOptions)

type callOptions struct {
	routingMode RoutingMode
	key         string
	timeout     time.Duration
	retry       bool
}

// RoutingMode determines how requests are routed to app instances.
type RoutingMode int

const (
	// RouteToLeader routes the request to the leader instance (singleton apps).
	RouteToLeader RoutingMode = iota
	// RouteToAny routes the request to any healthy instance (load balanced).
	RouteToAny
	// RouteForKey routes the request to the instance owning the key (ring mode).
	RouteForKey
)

const (
	// Default API circuit breaker settings
	apiCircuitBreakerThreshold = 5
	apiCircuitBreakerTimeout   = 30 * time.Second
)

// ToLeader routes the request to the leader instance.
func ToLeader() CallOption {
	return func(o *callOptions) {
		o.routingMode = RouteToLeader
	}
}

// ToAny routes the request to any healthy instance.
func ToAny() CallOption {
	return func(o *callOptions) {
		o.routingMode = RouteToAny
	}
}

// ForKey routes the request to the instance owning the key (ring mode).
func ForKey(key string) CallOption {
	return func(o *callOptions) {
		o.routingMode = RouteForKey
		o.key = key
	}
}

// WithTimeout sets a custom timeout for the call.
func WithTimeout(d time.Duration) CallOption {
	return func(o *callOptions) {
		o.timeout = d
	}
}

// WithRetry enables retry logic for the call.
func WithRetry() CallOption {
	return func(o *callOptions) {
		o.retry = true
	}
}

// API returns an API client for the specified app.
func (p *Platform) API(appName string) *APIClient {
	return &APIClient{
		platform: p,
		appName:  appName,
		timeout:  30 * time.Second,
		retryConfig: &RetryConfig{
			MaxRetries:  3,
			InitialWait: 100 * time.Millisecond,
			MaxWait:     5 * time.Second,
			Multiplier:  2.0,
		},
	}
}

// Call makes an API call to the target app.
func (c *APIClient) Call(ctx context.Context, method string, payload []byte, opts ...CallOption) ([]byte, error) {
	// Check circuit breaker
	if c.isCircuitOpen() {
		return nil, fmt.Errorf("circuit breaker open for app %s", c.appName)
	}

	// Apply options
	callOpts := &callOptions{
		routingMode: RouteToLeader,
		timeout:     c.timeout,
	}
	for _, opt := range opts {
		opt(callOpts)
	}

	start := time.Now()
	var resp []byte
	var err error

	if callOpts.retry && c.retryConfig != nil {
		resp, err = c.callWithRetry(ctx, method, payload, callOpts)
	} else {
		resp, err = c.doCall(ctx, method, payload, callOpts)
	}

	duration := time.Since(start)

	// Update circuit breaker state
	if err != nil {
		c.recordFailure()
		c.platform.metrics.ObserveCrossAppCall(c.appName, method, duration, err)
	} else {
		c.recordSuccess()
		c.platform.metrics.ObserveCrossAppCall(c.appName, method, duration, nil)
	}

	return resp, err
}

// CallJSON makes an API call with JSON encoding/decoding.
func (c *APIClient) CallJSON(ctx context.Context, method string, req any, resp any, opts ...CallOption) error {
	payload, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	data, err := c.Call(ctx, method, payload, opts...)
	if err != nil {
		return err
	}

	if resp != nil && len(data) > 0 {
		if err := json.Unmarshal(data, resp); err != nil {
			return fmt.Errorf("failed to unmarshal response: %w", err)
		}
	}

	return nil
}

// doCall performs the actual API call.
func (c *APIClient) doCall(ctx context.Context, method string, payload []byte, opts *callOptions) ([]byte, error) {
	// Determine target node based on routing mode
	targetNode, err := c.resolveTarget(ctx, opts)
	if err != nil {
		return nil, err
	}

	// Build the RPC request
	req := RPCRequest{
		Method:  method,
		Payload: payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Build subject based on target
	subject := fmt.Sprintf("%s.%s.rpc.%s", c.platform.name, c.appName, targetNode)

	// Use context timeout if shorter
	timeout := opts.timeout
	if deadline, ok := ctx.Deadline(); ok {
		ctxTimeout := time.Until(deadline)
		if ctxTimeout < timeout {
			timeout = ctxTimeout
		}
	}

	// Make the request
	msg, err := c.platform.nc.Request(subject, data, timeout)
	if err != nil {
		return nil, fmt.Errorf("API call failed: %w", err)
	}

	// Parse response
	var resp RPCResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if resp.Error != "" {
		return nil, fmt.Errorf("%s", resp.Error)
	}

	return resp.Data, nil
}

// callWithRetry performs the call with retry logic.
func (c *APIClient) callWithRetry(ctx context.Context, method string, payload []byte, opts *callOptions) ([]byte, error) {
	var lastErr error
	wait := c.retryConfig.InitialWait

	for attempt := 0; attempt <= c.retryConfig.MaxRetries; attempt++ {
		resp, err := c.doCall(ctx, method, payload, opts)
		if err == nil {
			return resp, nil
		}

		lastErr = err

		// Don't retry if context is done
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		// Wait before next retry
		if attempt < c.retryConfig.MaxRetries {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			// Exponential backoff
			wait = time.Duration(float64(wait) * c.retryConfig.Multiplier)
			if wait > c.retryConfig.MaxWait {
				wait = c.retryConfig.MaxWait
			}
		}
	}

	return nil, fmt.Errorf("API call failed after %d retries: %w", c.retryConfig.MaxRetries, lastErr)
}

// resolveTarget determines which node to send the request to.
func (c *APIClient) resolveTarget(ctx context.Context, opts *callOptions) (string, error) {
	switch opts.routingMode {
	case RouteToLeader:
		return c.resolveLeader(ctx)
	case RouteToAny:
		return c.resolveAny(ctx)
	case RouteForKey:
		return c.resolveForKey(ctx, opts.key)
	default:
		return c.resolveLeader(ctx)
	}
}

// resolveLeader finds the leader node for the app.
func (c *APIClient) resolveLeader(ctx context.Context) (string, error) {
	// Check if the app is running locally and is the leader
	c.platform.appsMu.RLock()
	localApp, exists := c.platform.apps[c.appName]
	c.platform.appsMu.RUnlock()

	if exists && localApp.election != nil {
		leader := localApp.election.Leader()
		if leader != "" {
			return leader, nil
		}
	}

	// Try to find leader from membership info
	// Query the election KV for the target app
	bucketName := fmt.Sprintf("%s_%s_election", c.platform.name, c.appName)
	kv, err := c.platform.js.KeyValue(ctx, bucketName)
	if err != nil {
		return "", fmt.Errorf("failed to get election KV for app %s: %w", c.appName, err)
	}

	entry, err := kv.Get(ctx, "leader")
	if err != nil {
		return "", ErrNoLeader
	}

	var record LeaderRecord
	if err := json.Unmarshal(entry.Value(), &record); err != nil {
		return "", fmt.Errorf("failed to unmarshal leader record: %w", err)
	}

	// Check if leader is still valid
	if time.Now().After(record.ExpiresAt) {
		return "", ErrNoLeader
	}

	return record.NodeID, nil
}

// resolveAny finds any healthy instance of the app.
func (c *APIClient) resolveAny(ctx context.Context) (string, error) {
	// First check if we have a local instance
	c.platform.appsMu.RLock()
	_, exists := c.platform.apps[c.appName]
	c.platform.appsMu.RUnlock()

	if exists {
		return c.platform.nodeID, nil
	}

	// Find any member running this app
	members := c.platform.membership.Members()
	for _, member := range members {
		for _, app := range member.Apps {
			if app == c.appName && member.IsHealthy {
				return member.NodeID, nil
			}
		}
	}

	return "", ErrAppNotFound
}

// resolveForKey finds the node owning the key (for ring mode apps).
func (c *APIClient) resolveForKey(ctx context.Context, key string) (string, error) {
	// Check if the local app has a ring
	c.platform.appsMu.RLock()
	localApp, exists := c.platform.apps[c.appName]
	c.platform.appsMu.RUnlock()

	if exists && localApp.ring != nil {
		return localApp.ring.NodeFor(key)
	}

	// Try to find ring info from membership and calculate partition
	// For remote apps, we need to query the ring KV
	bucketName := fmt.Sprintf("%s_%s_ring", c.platform.name, c.appName)
	kv, err := c.platform.js.KeyValue(ctx, bucketName)
	if err != nil {
		// No ring configured, fall back to any routing
		return c.resolveAny(ctx)
	}

	// Load partition assignments
	entry, err := kv.Get(ctx, "assignments")
	if err != nil {
		return c.resolveAny(ctx)
	}

	var assignment PartitionAssignment
	if err := json.Unmarshal(entry.Value(), &assignment); err != nil {
		return c.resolveAny(ctx)
	}

	// Calculate partition for the key using ring's hash function
	// Use the same hash function as the Ring
	partition := c.calculatePartition(key, len(assignment.Partitions))
	if partition < 0 {
		return c.resolveAny(ctx)
	}

	node, ok := assignment.Partitions[partition]
	if !ok || node == "" {
		return c.resolveAny(ctx)
	}

	return node, nil
}

// calculatePartition calculates the partition for a key.
func (c *APIClient) calculatePartition(key string, numPartitions int) int {
	if numPartitions <= 0 {
		return -1
	}
	h := hashKey(key)
	return int(h % uint64(numPartitions))
}

// isCircuitOpen checks if the circuit breaker is open.
func (c *APIClient) isCircuitOpen() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if !c.circuitOpen {
		return false
	}

	// Check if circuit breaker timeout has passed
	if time.Since(c.circuitOpenTime) > apiCircuitBreakerTimeout {
		return false // Allow a test request (half-open)
	}

	return true
}

// recordFailure records a failed call.
func (c *APIClient) recordFailure() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures++
	c.lastFailure = time.Now()

	if c.failures >= apiCircuitBreakerThreshold {
		c.circuitOpen = true
		c.circuitOpenTime = time.Now()
	}
}

// recordSuccess records a successful call.
func (c *APIClient) recordSuccess() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.failures = 0
	c.circuitOpen = false
}

// Broadcast sends a request to all instances of the app.
func (c *APIClient) Broadcast(ctx context.Context, method string, payload []byte) []BroadcastResponse {
	return c.BroadcastWithTimeout(ctx, method, payload, 5*time.Second)
}

// BroadcastWithTimeout sends a request to all instances with a custom timeout.
func (c *APIClient) BroadcastWithTimeout(ctx context.Context, method string, payload []byte, timeout time.Duration) []BroadcastResponse {
	req := RPCRequest{
		Method:  method,
		Payload: payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	subject := fmt.Sprintf("%s.%s.rpc.broadcast", c.platform.name, c.appName)
	inbox := nats.NewInbox()

	// Subscribe to collect responses
	var mu sync.Mutex
	responses := make([]BroadcastResponse, 0)
	sub, err := c.platform.nc.Subscribe(inbox, func(msg *nats.Msg) {
		var resp RPCResponse
		if err := json.Unmarshal(msg.Data, &resp); err != nil {
			return
		}

		br := BroadcastResponse{
			NodeID: msg.Header.Get("X-Node-ID"),
			Data:   resp.Data,
		}
		if resp.Error != "" {
			br.Error = fmt.Errorf("%s", resp.Error)
		}
		mu.Lock()
		responses = append(responses, br)
		mu.Unlock()
	})
	if err != nil {
		return nil
	}
	defer sub.Unsubscribe()

	// Publish request
	msg := &nats.Msg{
		Subject: subject,
		Reply:   inbox,
		Data:    data,
	}
	if err := c.platform.nc.PublishMsg(msg); err != nil {
		return nil
	}

	// Wait for timeout
	select {
	case <-ctx.Done():
	case <-time.After(timeout):
	}

	mu.Lock()
	result := make([]BroadcastResponse, len(responses))
	copy(result, responses)
	mu.Unlock()

	return result
}
