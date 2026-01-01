package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
)

// RPC provides inter-node RPC communication for an app.
type RPC struct {
	app *App
	sub *nats.Subscription
}

// RPCRequest represents an RPC request.
type RPCRequest struct {
	Method  string `json:"method"`
	Payload []byte `json:"payload"`
}

// RPCResponse represents an RPC response.
type RPCResponse struct {
	Data  []byte `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

// BroadcastResponse represents a response from a broadcast call.
type BroadcastResponse struct {
	NodeID string
	Data   []byte
	Error  error
}

// NewRPC creates a new RPC instance for the app.
func NewRPC(app *App) (*RPC, error) {
	r := &RPC{
		app: app,
	}

	// Subscribe to RPC requests for this node
	subject := fmt.Sprintf("%s.%s.rpc.%s", app.platform.name, app.name, app.platform.nodeID)
	sub, err := app.platform.nc.Subscribe(subject, r.handleRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to RPC: %w", err)
	}
	r.sub = sub

	// Also subscribe to broadcast subject
	broadcastSubject := fmt.Sprintf("%s.%s.rpc.broadcast", app.platform.name, app.name)
	_, err = app.platform.nc.Subscribe(broadcastSubject, r.handleRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to subscribe to broadcast RPC: %w", err)
	}

	return r, nil
}

// Handle registers an RPC handler for the given method.
func (r *RPC) Handle(method string, handler Handler) {
	r.app.handlers[method] = handler
}

// Call makes an RPC call to a specific node.
func (r *RPC) Call(ctx context.Context, nodeID, method string, payload []byte) ([]byte, error) {
	return r.callWithTimeout(ctx, nodeID, method, payload, 30*time.Second)
}

// CallWithTimeout makes an RPC call with a custom timeout.
func (r *RPC) CallWithTimeout(ctx context.Context, nodeID, method string, payload []byte, timeout time.Duration) ([]byte, error) {
	return r.callWithTimeout(ctx, nodeID, method, payload, timeout)
}

// CallLeader makes an RPC call to the current leader.
func (r *RPC) CallLeader(ctx context.Context, method string, payload []byte) ([]byte, error) {
	if r.app.election == nil {
		return nil, ErrNoLeader
	}

	leader := r.app.election.Leader()
	if leader == "" {
		return nil, ErrNoLeader
	}

	return r.Call(ctx, leader, method, payload)
}

// CallAny makes an RPC call to any available instance.
func (r *RPC) CallAny(ctx context.Context, method string, payload []byte) ([]byte, error) {
	// For singleton apps, call the leader
	if r.app.mode == ModeSingleton {
		return r.CallLeader(ctx, method, payload)
	}

	// For pool apps, we could use NATS request to get any responder
	// For now, call self if available
	return r.Call(ctx, r.app.platform.nodeID, method, payload)
}

// Broadcast sends an RPC request to all instances.
func (r *RPC) Broadcast(ctx context.Context, method string, payload []byte) []BroadcastResponse {
	return r.BroadcastWithTimeout(ctx, method, payload, 5*time.Second)
}

// BroadcastWithTimeout sends an RPC request to all instances with a custom timeout.
func (r *RPC) BroadcastWithTimeout(ctx context.Context, method string, payload []byte, timeout time.Duration) []BroadcastResponse {
	req := RPCRequest{
		Method:  method,
		Payload: payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil
	}

	subject := fmt.Sprintf("%s.%s.rpc.broadcast", r.app.platform.name, r.app.name)
	inbox := nats.NewInbox()

	// Subscribe to collect responses with mutex protection
	var mu sync.Mutex
	responses := make([]BroadcastResponse, 0)
	sub, err := r.app.platform.nc.Subscribe(inbox, func(msg *nats.Msg) {
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
	if err := r.app.platform.nc.PublishMsg(msg); err != nil {
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

// callWithTimeout makes an RPC call with timeout.
func (r *RPC) callWithTimeout(ctx context.Context, nodeID, method string, payload []byte, timeout time.Duration) ([]byte, error) {
	start := time.Now()

	req := RPCRequest{
		Method:  method,
		Payload: payload,
	}

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	subject := fmt.Sprintf("%s.%s.rpc.%s", r.app.platform.name, r.app.name, nodeID)

	// Use context timeout if shorter
	deadline, ok := ctx.Deadline()
	if ok {
		ctxTimeout := time.Until(deadline)
		if ctxTimeout < timeout {
			timeout = ctxTimeout
		}
	}

	msg, err := r.app.platform.nc.Request(subject, data, timeout)
	if err != nil {
		r.app.platform.metrics.ObserveRPC(r.app.name, method, time.Since(start), err)
		return nil, err
	}

	var resp RPCResponse
	if err := json.Unmarshal(msg.Data, &resp); err != nil {
		r.app.platform.metrics.ObserveRPC(r.app.name, method, time.Since(start), err)
		return nil, err
	}

	if resp.Error != "" {
		err := fmt.Errorf("%s", resp.Error)
		r.app.platform.metrics.ObserveRPC(r.app.name, method, time.Since(start), err)
		return nil, err
	}

	r.app.platform.metrics.ObserveRPC(r.app.name, method, time.Since(start), nil)
	return resp.Data, nil
}

// handleRequest handles incoming RPC requests.
func (r *RPC) handleRequest(msg *nats.Msg) {
	var req RPCRequest
	if err := json.Unmarshal(msg.Data, &req); err != nil {
		r.sendResponse(msg, nil, err)
		return
	}

	handler, ok := r.app.handlers[req.Method]
	if !ok {
		r.sendResponse(msg, nil, fmt.Errorf("unknown method: %s", req.Method))
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	data, err := handler(ctx, req.Payload)
	r.sendResponse(msg, data, err)
}

// sendResponse sends an RPC response.
func (r *RPC) sendResponse(msg *nats.Msg, data []byte, err error) {
	if msg.Reply == "" {
		return
	}

	resp := RPCResponse{
		Data: data,
	}
	if err != nil {
		resp.Error = err.Error()
	}

	respData, _ := json.Marshal(resp)

	respMsg := &nats.Msg{
		Subject: msg.Reply,
		Data:    respData,
		Header:  nats.Header{},
	}
	respMsg.Header.Set("X-Node-ID", r.app.platform.nodeID)

	r.app.platform.nc.PublishMsg(respMsg)
}

// Stop stops the RPC listener.
func (r *RPC) Stop() {
	if r.sub != nil {
		r.sub.Unsubscribe()
	}
}
