package cluster

import (
	"context"
	"fmt"
	"log"
	"runtime/debug"
	"time"
)

// HandlerContext provides metadata about the current request.
type HandlerContext struct {
	// Method is the RPC method being called
	Method string
	// NodeID is the ID of the node handling the request
	NodeID string
	// AppName is the name of the app handling the request
	AppName string
	// PlatformName is the name of the platform
	PlatformName string
	// RequestID is a unique identifier for the request (if provided)
	RequestID string
	// StartTime is when the request started processing
	StartTime time.Time
	// Custom metadata that middleware can use to pass data
	Metadata map[string]any
}

// HandlerContextKey is the key used to store HandlerContext in context.Context
type HandlerContextKey struct{}

// GetHandlerContext retrieves the HandlerContext from a context.
func GetHandlerContext(ctx context.Context) *HandlerContext {
	if hc, ok := ctx.Value(HandlerContextKey{}).(*HandlerContext); ok {
		return hc
	}
	return nil
}

// withHandlerContext adds a HandlerContext to a context.
func withHandlerContext(ctx context.Context, hc *HandlerContext) context.Context {
	return context.WithValue(ctx, HandlerContextKey{}, hc)
}

// Middleware wraps a Handler to add cross-cutting functionality.
// Middleware receives the handler context, request payload, and the next handler in the chain.
// It should call next(ctx, req) to continue the chain, or return early to short-circuit.
type Middleware func(ctx context.Context, req []byte, next Handler) ([]byte, error)

// MiddlewareChain composes multiple middlewares into a single handler wrapper.
type MiddlewareChain struct {
	middlewares []Middleware
}

// NewMiddlewareChain creates a new middleware chain.
func NewMiddlewareChain(middlewares ...Middleware) *MiddlewareChain {
	return &MiddlewareChain{
		middlewares: middlewares,
	}
}

// Use adds middleware(s) to the chain.
func (mc *MiddlewareChain) Use(middlewares ...Middleware) {
	mc.middlewares = append(mc.middlewares, middlewares...)
}

// Wrap wraps a handler with all middlewares in the chain.
// Middlewares are executed in the order they were added.
func (mc *MiddlewareChain) Wrap(handler Handler) Handler {
	if len(mc.middlewares) == 0 {
		return handler
	}

	// Build the chain from the inside out (last middleware wraps first)
	wrapped := handler
	for i := len(mc.middlewares) - 1; i >= 0; i-- {
		mw := mc.middlewares[i]
		next := wrapped
		wrapped = func(ctx context.Context, req []byte) ([]byte, error) {
			return mw(ctx, req, next)
		}
	}

	return wrapped
}

// Clone creates a copy of the middleware chain.
func (mc *MiddlewareChain) Clone() *MiddlewareChain {
	cloned := make([]Middleware, len(mc.middlewares))
	copy(cloned, mc.middlewares)
	return &MiddlewareChain{middlewares: cloned}
}

// Len returns the number of middlewares in the chain.
func (mc *MiddlewareChain) Len() int {
	return len(mc.middlewares)
}

// =============================================================================
// Built-in Middlewares
// =============================================================================

// RecoveryMiddleware catches panics and converts them to errors.
// This should typically be the first middleware in the chain.
func RecoveryMiddleware() Middleware {
	return func(ctx context.Context, req []byte, next Handler) (resp []byte, err error) {
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				if hc := GetHandlerContext(ctx); hc != nil {
					log.Printf("[%s/%s] PANIC in handler %s: %v\n%s",
						hc.PlatformName, hc.AppName, hc.Method, r, stack)
				} else {
					log.Printf("PANIC in handler: %v\n%s", r, stack)
				}
				err = fmt.Errorf("internal error: panic recovered")
			}
		}()
		return next(ctx, req)
	}
}

// LoggingMiddleware logs request/response information.
func LoggingMiddleware() Middleware {
	return LoggingMiddlewareWithOptions(LoggingOptions{})
}

// LoggingOptions configures the logging middleware.
type LoggingOptions struct {
	// LogRequests logs the request payload (may contain sensitive data)
	LogRequests bool
	// LogResponses logs the response payload (may contain sensitive data)
	LogResponses bool
	// SlowThreshold logs a warning if request takes longer than this
	SlowThreshold time.Duration
	// Logger is a custom logger function (defaults to log.Printf)
	Logger func(format string, args ...any)
}

// LoggingMiddlewareWithOptions creates a logging middleware with custom options.
func LoggingMiddlewareWithOptions(opts LoggingOptions) Middleware {
	logger := opts.Logger
	if logger == nil {
		logger = log.Printf
	}

	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		hc := GetHandlerContext(ctx)
		if hc == nil {
			return next(ctx, req)
		}

		start := time.Now()

		if opts.LogRequests {
			logger("[%s/%s] -> %s request_size=%d",
				hc.PlatformName, hc.AppName, hc.Method, len(req))
		}

		resp, err := next(ctx, req)

		duration := time.Since(start)
		statusStr := "OK"
		if err != nil {
			statusStr = "ERROR"
		}

		logLine := fmt.Sprintf("[%s/%s] <- %s status=%s duration=%v",
			hc.PlatformName, hc.AppName, hc.Method, statusStr, duration)

		if opts.LogResponses && resp != nil {
			logLine += fmt.Sprintf(" response_size=%d", len(resp))
		}

		if err != nil {
			logLine += fmt.Sprintf(" error=%q", err.Error())
		}

		if opts.SlowThreshold > 0 && duration > opts.SlowThreshold {
			logger("SLOW: %s", logLine)
		} else {
			logger("%s", logLine)
		}

		return resp, err
	}
}

// MetricsMiddleware records metrics for handler calls.
// It uses the platform's metrics system to record:
// - Request count by method and status
// - Request duration histogram
func MetricsMiddleware(metrics *Metrics) Middleware {
	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		hc := GetHandlerContext(ctx)
		if hc == nil || metrics == nil {
			return next(ctx, req)
		}

		start := time.Now()
		resp, err := next(ctx, req)
		duration := time.Since(start)

		// Record metrics
		metrics.ObserveRPC(hc.AppName, hc.Method, duration, err)

		return resp, err
	}
}

// TimeoutMiddleware enforces a timeout on handler execution.
func TimeoutMiddleware(timeout time.Duration) Middleware {
	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		// Run handler in goroutine
		type result struct {
			resp []byte
			err  error
		}
		resultCh := make(chan result, 1)

		go func() {
			resp, err := next(ctx, req)
			resultCh <- result{resp, err}
		}()

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("handler timeout after %v", timeout)
		case r := <-resultCh:
			return r.resp, r.err
		}
	}
}

// AuthMiddleware provides a framework for authentication.
// The authFunc should return an error if authentication fails.
type AuthFunc func(ctx context.Context, req []byte) error

// AuthMiddleware creates an authentication middleware.
func AuthMiddleware(authFunc AuthFunc) Middleware {
	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		if err := authFunc(ctx, req); err != nil {
			return nil, fmt.Errorf("authentication failed: %w", err)
		}
		return next(ctx, req)
	}
}

// RateLimitMiddleware provides basic rate limiting per method.
// It uses a simple token bucket algorithm.
type RateLimiter interface {
	Allow(method string) bool
}

// RateLimitMiddleware creates a rate limiting middleware.
func RateLimitMiddleware(limiter RateLimiter) Middleware {
	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		hc := GetHandlerContext(ctx)
		method := ""
		if hc != nil {
			method = hc.Method
		}

		if !limiter.Allow(method) {
			return nil, fmt.Errorf("rate limit exceeded for method %s", method)
		}

		return next(ctx, req)
	}
}

// MethodFilterMiddleware only allows specific methods to be called.
func MethodFilterMiddleware(allowedMethods ...string) Middleware {
	allowed := make(map[string]bool, len(allowedMethods))
	for _, m := range allowedMethods {
		allowed[m] = true
	}

	return func(ctx context.Context, req []byte, next Handler) ([]byte, error) {
		hc := GetHandlerContext(ctx)
		if hc != nil && !allowed[hc.Method] {
			return nil, fmt.Errorf("method %s not allowed", hc.Method)
		}
		return next(ctx, req)
	}
}
