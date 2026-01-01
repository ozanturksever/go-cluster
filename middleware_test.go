package cluster_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/testutil"
)

// TestMiddlewareChain_Basic tests basic middleware chain functionality
func TestMiddlewareChain_Basic(t *testing.T) {
	var order []string

	// Create middleware that records execution order
	mw1 := func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		order = append(order, "mw1-before")
		resp, err := next(ctx, req)
		order = append(order, "mw1-after")
		return resp, err
	}

	mw2 := func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		order = append(order, "mw2-before")
		resp, err := next(ctx, req)
		order = append(order, "mw2-after")
		return resp, err
	}

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		order = append(order, "handler")
		return []byte("response"), nil
	}

	// Create chain and wrap handler
	chain := cluster.NewMiddlewareChain(mw1, mw2)
	wrapped := chain.Wrap(handler)

	// Execute
	resp, err := wrapped(context.Background(), []byte("request"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(resp) != "response" {
		t.Errorf("expected 'response', got %s", string(resp))
	}

	// Verify execution order
	expected := []string{"mw1-before", "mw2-before", "handler", "mw2-after", "mw1-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %d calls, got %d: %v", len(expected), len(order), order)
	}

	for i, v := range expected {
		if order[i] != v {
			t.Errorf("order[%d] = %s, expected %s", i, order[i], v)
		}
	}
}

// TestMiddlewareChain_Use tests adding middleware with Use
func TestMiddlewareChain_Use(t *testing.T) {
	chain := cluster.NewMiddlewareChain()

	if chain.Len() != 0 {
		t.Errorf("expected empty chain, got %d", chain.Len())
	}

	// Add middleware
	chain.Use(func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		return next(ctx, req)
	})

	if chain.Len() != 1 {
		t.Errorf("expected 1 middleware, got %d", chain.Len())
	}

	// Add more middleware
	chain.Use(
		func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) { return next(ctx, req) },
		func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) { return next(ctx, req) },
	)

	if chain.Len() != 3 {
		t.Errorf("expected 3 middlewares, got %d", chain.Len())
	}
}

// TestMiddlewareChain_Clone tests cloning a middleware chain
func TestMiddlewareChain_Clone(t *testing.T) {
	chain := cluster.NewMiddlewareChain(
		func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) { return next(ctx, req) },
	)

	cloned := chain.Clone()

	// Modify original
	chain.Use(func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) { return next(ctx, req) })

	if chain.Len() != 2 {
		t.Errorf("original should have 2, got %d", chain.Len())
	}

	if cloned.Len() != 1 {
		t.Errorf("clone should have 1, got %d", cloned.Len())
	}
}

// TestMiddlewareChain_ShortCircuit tests middleware that doesn't call next
func TestMiddlewareChain_ShortCircuit(t *testing.T) {
	handlerCalled := false

	// Middleware that short-circuits
	mw := func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		return []byte("short-circuited"), nil
	}

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		handlerCalled = true
		return []byte("handler"), nil
	}

	chain := cluster.NewMiddlewareChain(mw)
	wrapped := chain.Wrap(handler)

	resp, err := wrapped(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(resp) != "short-circuited" {
		t.Errorf("expected 'short-circuited', got %s", string(resp))
	}

	if handlerCalled {
		t.Error("handler should not have been called")
	}
}

// TestRecoveryMiddleware tests panic recovery
func TestRecoveryMiddleware(t *testing.T) {
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		panic("test panic")
	}

	chain := cluster.NewMiddlewareChain(cluster.RecoveryMiddleware())
	wrapped := chain.Wrap(handler)

	resp, err := wrapped(context.Background(), nil)

	if resp != nil {
		t.Errorf("expected nil response, got %v", resp)
	}

	if err == nil {
		t.Fatal("expected error from panic recovery")
	}

	if err.Error() != "internal error: panic recovered" {
		t.Errorf("unexpected error message: %v", err)
	}
}

// TestTimeoutMiddleware tests timeout enforcement
func TestTimeoutMiddleware(t *testing.T) {
	// Handler that takes too long
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
			return []byte("done"), nil
		}
	}

	chain := cluster.NewMiddlewareChain(cluster.TimeoutMiddleware(100 * time.Millisecond))
	wrapped := chain.Wrap(handler)

	start := time.Now()
	_, err := wrapped(context.Background(), nil)
	duration := time.Since(start)

	if err == nil {
		t.Fatal("expected timeout error")
	}

	if duration > 500*time.Millisecond {
		t.Errorf("timeout took too long: %v", duration)
	}
}

// TestAuthMiddleware tests authentication middleware
func TestAuthMiddleware(t *testing.T) {
	authFunc := func(ctx context.Context, req []byte) error {
		if string(req) == "valid" {
			return nil
		}
		return errors.New("invalid token")
	}

	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("success"), nil
	}

	chain := cluster.NewMiddlewareChain(cluster.AuthMiddleware(authFunc))
	wrapped := chain.Wrap(handler)

	// Valid auth
	resp, err := wrapped(context.Background(), []byte("valid"))
	if err != nil {
		t.Fatalf("valid auth should succeed: %v", err)
	}
	if string(resp) != "success" {
		t.Errorf("expected 'success', got %s", string(resp))
	}

	// Invalid auth
	_, err = wrapped(context.Background(), []byte("invalid"))
	if err == nil {
		t.Fatal("invalid auth should fail")
	}
}

// TestMethodFilterMiddleware tests method filtering
func TestMethodFilterMiddleware(t *testing.T) {
	handler := func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("ok"), nil
	}

	chain := cluster.NewMiddlewareChain(cluster.MethodFilterMiddleware("allowed.method", "another.allowed"))
	wrapped := chain.Wrap(handler)

	// Create context with allowed method
	hc := &cluster.HandlerContext{Method: "allowed.method"}
	ctx := context.WithValue(context.Background(), cluster.HandlerContextKey{}, hc)

	resp, err := wrapped(ctx, nil)
	if err != nil {
		t.Fatalf("allowed method should succeed: %v", err)
	}
	if string(resp) != "ok" {
		t.Errorf("expected 'ok', got %s", string(resp))
	}

	// Create context with disallowed method
	hc2 := &cluster.HandlerContext{Method: "disallowed.method"}
	ctx2 := context.WithValue(context.Background(), cluster.HandlerContextKey{}, hc2)

	_, err = wrapped(ctx2, nil)
	if err == nil {
		t.Fatal("disallowed method should fail")
	}
}

// TestMiddleware_Integration tests middleware with actual RPC
func TestMiddleware_Integration(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Track middleware calls
	var callCount int32

	// Create platform
	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	// Create app with middleware
	app := cluster.NewApp("test-app", cluster.Spread())

	// Add counting middleware
	app.Use(func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		atomic.AddInt32(&callCount, 1)
		return next(ctx, req)
	})

	// Add recovery middleware
	app.Use(cluster.RecoveryMiddleware())

	// Register handler
	app.Handle("test.echo", func(ctx context.Context, req []byte) ([]byte, error) {
		return req, nil
	})

	platform.Register(app)

	// Start platform
	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	// Wait for startup
	time.Sleep(500 * time.Millisecond)

	// Make RPC call
	resp, err := app.RPC().Call(ctx, "node-1", "test.echo", []byte("hello"))
	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}

	if string(resp) != "hello" {
		t.Errorf("expected 'hello', got %s", string(resp))
	}

	// Verify middleware was called
	if atomic.LoadInt32(&callCount) != 1 {
		t.Errorf("expected middleware to be called once, got %d", callCount)
	}

	// Make another call
	_, err = app.RPC().Call(ctx, "node-1", "test.echo", []byte("world"))
	if err != nil {
		t.Fatalf("Second RPC call failed: %v", err)
	}

	if atomic.LoadInt32(&callCount) != 2 {
		t.Errorf("expected middleware to be called twice, got %d", callCount)
	}

	cancel()
	<-done
}

// TestHandlerContext tests that HandlerContext is properly populated
func TestHandlerContext_Integration(t *testing.T) {
	ns := testutil.StartNATS(t)
	defer ns.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var capturedHC *cluster.HandlerContext

	platform, err := cluster.NewPlatform("test-platform", "node-1", ns.URL(),
		cluster.HealthAddr(":0"),
		cluster.MetricsAddr(":0"),
	)
	if err != nil {
		t.Fatalf("Failed to create platform: %v", err)
	}

	app := cluster.NewApp("test-app", cluster.Spread())

	// Middleware that captures the handler context
	app.Use(func(ctx context.Context, req []byte, next cluster.Handler) ([]byte, error) {
		capturedHC = cluster.GetHandlerContext(ctx)
		return next(ctx, req)
	})

	app.Handle("test.method", func(ctx context.Context, req []byte) ([]byte, error) {
		return []byte("ok"), nil
	})

	platform.Register(app)

	done := make(chan error, 1)
	go func() {
		done <- platform.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)

	_, err = app.RPC().Call(ctx, "node-1", "test.method", nil)
	if err != nil {
		t.Fatalf("RPC call failed: %v", err)
	}

	// Verify handler context
	if capturedHC == nil {
		t.Fatal("HandlerContext was not captured")
	}

	if capturedHC.Method != "test.method" {
		t.Errorf("expected method 'test.method', got %s", capturedHC.Method)
	}

	if capturedHC.NodeID != "node-1" {
		t.Errorf("expected node 'node-1', got %s", capturedHC.NodeID)
	}

	if capturedHC.AppName != "test-app" {
		t.Errorf("expected app 'test-app', got %s", capturedHC.AppName)
	}

	if capturedHC.PlatformName != "test-platform" {
		t.Errorf("expected platform 'test-platform', got %s", capturedHC.PlatformName)
	}

	cancel()
	<-done
}
