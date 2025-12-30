package vip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
)

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   24,
				Interface: "eth0",
			},
			wantErr: false,
		},
		{
			name: "missing address",
			config: Config{
				Address:   "",
				Netmask:   24,
				Interface: "eth0",
			},
			wantErr: true,
			errMsg:  "Address is required",
		},
		{
			name: "invalid IP address",
			config: Config{
				Address:   "invalid-ip",
				Netmask:   24,
				Interface: "eth0",
			},
			wantErr: true,
			errMsg:  "invalid IP address: invalid-ip",
		},
		{
			name: "invalid IP address - partial",
			config: Config{
				Address:   "192.168.1",
				Netmask:   24,
				Interface: "eth0",
			},
			wantErr: true,
			errMsg:  "invalid IP address: 192.168.1",
		},
		{
			name: "netmask too low",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   -1,
				Interface: "eth0",
			},
			wantErr: true,
			errMsg:  "Netmask must be between 0 and 32",
		},
		{
			name: "netmask too high",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   33,
				Interface: "eth0",
			},
			wantErr: true,
			errMsg:  "Netmask must be between 0 and 32",
		},
		{
			name: "missing interface",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   24,
				Interface: "",
			},
			wantErr: true,
			errMsg:  "Interface is required",
		},
		{
			name: "valid config with netmask 0",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   0,
				Interface: "eth0",
			},
			wantErr: false,
		},
		{
			name: "valid config with netmask 32",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   32,
				Interface: "eth0",
			},
			wantErr: false,
		},
		{
			name: "valid IPv6 address",
			config: Config{
				Address:   "::1",
				Netmask:   128,
				Interface: "eth0",
			},
			wantErr: true, // Netmask > 32 is invalid even for IPv6 in current implementation
			errMsg:  "Netmask must be between 0 and 32",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() expected error, got nil")
					return
				}
				if err.Error() != tt.errMsg {
					t.Errorf("Validate() error = %q, want %q", err.Error(), tt.errMsg)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error: %v", err)
			}
		})
	}
}

func TestConfigCIDR(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		want    string
	}{
		{
			name: "standard CIDR",
			config: Config{
				Address: "192.168.1.100",
				Netmask: 24,
			},
			want: "192.168.1.100/24",
		},
		{
			name: "host CIDR",
			config: Config{
				Address: "10.0.0.1",
				Netmask: 32,
			},
			want: "10.0.0.1/32",
		},
		{
			name: "zero netmask",
			config: Config{
				Address: "0.0.0.0",
				Netmask: 0,
			},
			want: "0.0.0.0/0",
		},
		{
			name: "class A network",
			config: Config{
				Address: "10.0.0.1",
				Netmask: 8,
			},
			want: "10.0.0.1/8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.config.CIDR(); got != tt.want {
				t.Errorf("CIDR() = %q, want %q", got, tt.want)
			}
		})
	}
}

// MockExecutor is a test implementation of Executor that records commands and returns configured responses
type MockExecutor struct {
	mu       sync.Mutex
	calls    []ExecutorCall
	responses map[string]ExecutorResponse
}

type ExecutorCall struct {
	Cmd  string
	Args []string
}

type ExecutorResponse struct {
	Output string
	Err    error
}

func NewMockExecutor() *MockExecutor {
	return &MockExecutor{
		responses: make(map[string]ExecutorResponse),
	}
}

func (m *MockExecutor) SetResponse(cmdPattern string, output string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.responses[cmdPattern] = ExecutorResponse{Output: output, Err: err}
}

func (m *MockExecutor) Execute(ctx context.Context, cmd string, args ...string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.calls = append(m.calls, ExecutorCall{Cmd: cmd, Args: args})

	// Build a key from the command and first few args for matching
	key := cmd
	if len(args) > 0 {
		key = cmd + " " + strings.Join(args, " ")
	}

	// Check for exact match first
	if resp, ok := m.responses[key]; ok {
		return resp.Output, resp.Err
	}

	// Check for pattern matches (contains)
	for pattern, resp := range m.responses {
		if strings.Contains(key, pattern) {
			return resp.Output, resp.Err
		}
	}

	return "", nil
}

func (m *MockExecutor) GetCalls() []ExecutorCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]ExecutorCall, len(m.calls))
	copy(result, m.calls)
	return result
}

func (m *MockExecutor) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = nil
	m.responses = make(map[string]ExecutorResponse)
}

func TestNewManager(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		executor Executor
		wantErr  bool
	}{
		{
			name: "valid config with nil executor",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   24,
				Interface: "eth0",
			},
			executor: nil,
			wantErr:  false,
		},
		{
			name: "valid config with custom executor",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   24,
				Interface: "eth0",
			},
			executor: NewMockExecutor(),
			wantErr:  false,
		},
		{
			name: "valid config with logger",
			config: Config{
				Address:   "192.168.1.100",
				Netmask:   24,
				Interface: "eth0",
				Logger:    slog.New(slog.NewTextHandler(os.Stdout, nil)),
			},
			executor: nil,
			wantErr:  false,
		},
		{
			name: "invalid config - missing address",
			config: Config{
				Netmask:   24,
				Interface: "eth0",
			},
			executor: nil,
			wantErr:  true,
		},
		{
			name: "invalid config - invalid IP",
			config: Config{
				Address:   "not-an-ip",
				Netmask:   24,
				Interface: "eth0",
			},
			executor: nil,
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager, err := NewManager(tt.config, tt.executor)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewManager() expected error, got nil")
				}
				if manager != nil {
					t.Errorf("NewManager() expected nil manager on error")
				}
			} else {
				if err != nil {
					t.Errorf("NewManager() unexpected error: %v", err)
				}
				if manager == nil {
					t.Errorf("NewManager() expected non-nil manager")
				}
			}
		})
	}
}

func TestManagerAcquire(t *testing.T) {
	cfg := Config{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	t.Run("successful acquisition", func(t *testing.T) {
		mock := NewMockExecutor()
		// IP not present initially
		mock.SetResponse("ip addr show", "", nil)
		// Add succeeds
		mock.SetResponse("ip addr add", "", nil)
		// arping may or may not succeed
		mock.SetResponse("arping", "", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Acquire(context.Background())
		if err != nil {
			t.Errorf("Acquire() error = %v, want nil", err)
		}

		// Verify the correct commands were called
		calls := mock.GetCalls()
		if len(calls) < 2 {
			t.Fatalf("Expected at least 2 calls, got %d", len(calls))
		}
		if calls[0].Cmd != "ip" || calls[0].Args[0] != "addr" || calls[0].Args[1] != "show" {
			t.Errorf("First call should be 'ip addr show', got %s %v", calls[0].Cmd, calls[0].Args)
		}
		if calls[1].Cmd != "ip" || calls[1].Args[0] != "addr" || calls[1].Args[1] != "add" {
			t.Errorf("Second call should be 'ip addr add', got %s %v", calls[1].Cmd, calls[1].Args)
		}
	})

	t.Run("already acquired - IP present", func(t *testing.T) {
		mock := NewMockExecutor()
		// IP already present
		mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Acquire(context.Background())
		if err != nil {
			t.Errorf("Acquire() error = %v, want nil", err)
		}

		// Should only check, not add
		calls := mock.GetCalls()
		if len(calls) != 1 {
			t.Errorf("Expected 1 call (just check), got %d", len(calls))
		}
	})

	t.Run("error checking interface", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "", errors.New("device not found"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Acquire(context.Background())
		if err == nil {
			t.Error("Acquire() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to check VIP status") {
			t.Errorf("Acquire() error = %v, want error containing 'failed to check VIP status'", err)
		}
	})

	t.Run("error adding IP", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "", nil)
		mock.SetResponse("ip addr add", "", errors.New("permission denied"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Acquire(context.Background())
		if err == nil {
			t.Error("Acquire() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to add VIP") {
			t.Errorf("Acquire() error = %v, want error containing 'failed to add VIP'", err)
		}
	})

	t.Run("file exists error succeeds", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "", nil)
		mock.SetResponse("ip addr add", "", fmt.Errorf("RTNETLINK answers: File exists"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Acquire(context.Background())
		if err != nil {
			t.Errorf("Acquire() error = %v, want nil (File exists should succeed)", err)
		}
	})
}

func TestManagerRelease(t *testing.T) {
	cfg := Config{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	t.Run("successful release", func(t *testing.T) {
		mock := NewMockExecutor()
		// IP is present
		mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)
		// Delete succeeds
		mock.SetResponse("ip addr del", "", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Release(context.Background())
		if err != nil {
			t.Errorf("Release() error = %v, want nil", err)
		}

		// Verify the correct commands were called
		calls := mock.GetCalls()
		if len(calls) != 2 {
			t.Fatalf("Expected 2 calls, got %d", len(calls))
		}
		if calls[1].Cmd != "ip" || calls[1].Args[0] != "addr" || calls[1].Args[1] != "del" {
			t.Errorf("Second call should be 'ip addr del', got %s %v", calls[1].Cmd, calls[1].Args)
		}
	})

	t.Run("already released - IP not present", func(t *testing.T) {
		mock := NewMockExecutor()
		// IP not present
		mock.SetResponse("ip addr show", "inet 10.0.0.1/24 scope global eth0", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Release(context.Background())
		if err != nil {
			t.Errorf("Release() error = %v, want nil", err)
		}

		// Should only check, not delete
		calls := mock.GetCalls()
		if len(calls) != 1 {
			t.Errorf("Expected 1 call (just check), got %d", len(calls))
		}
	})

	t.Run("error checking interface", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "", errors.New("device not found"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Release(context.Background())
		if err == nil {
			t.Error("Release() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to check VIP status") {
			t.Errorf("Release() error = %v, want error containing 'failed to check VIP status'", err)
		}
	})

	t.Run("error removing IP", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)
		mock.SetResponse("ip addr del", "", errors.New("permission denied"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Release(context.Background())
		if err == nil {
			t.Error("Release() expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to remove VIP") {
			t.Errorf("Release() error = %v, want error containing 'failed to remove VIP'", err)
		}
	})

	t.Run("cannot assign error succeeds", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)
		mock.SetResponse("ip addr del", "", fmt.Errorf("RTNETLINK answers: Cannot assign requested address"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		err = manager.Release(context.Background())
		if err != nil {
			t.Errorf("Release() error = %v, want nil (Cannot assign should succeed)", err)
		}
	})
}

func TestManagerIsAcquired(t *testing.T) {
	cfg := Config{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	t.Run("returns true when IP present", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		acquired, err := manager.IsAcquired(context.Background())
		if err != nil {
			t.Errorf("IsAcquired() error = %v, want nil", err)
		}
		if !acquired {
			t.Error("IsAcquired() = false, want true")
		}
	})

	t.Run("returns false when IP not present", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "inet 10.0.0.1/24 scope global eth0", nil)

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		acquired, err := manager.IsAcquired(context.Background())
		if err != nil {
			t.Errorf("IsAcquired() error = %v, want nil", err)
		}
		if acquired {
			t.Error("IsAcquired() = true, want false")
		}
	})

	t.Run("returns error on check failure", func(t *testing.T) {
		mock := NewMockExecutor()
		mock.SetResponse("ip addr show", "", errors.New("device not found"))

		manager, err := NewManager(cfg, mock)
		if err != nil {
			t.Fatalf("NewManager() error: %v", err)
		}

		_, err = manager.IsAcquired(context.Background())
		if err == nil {
			t.Error("IsAcquired() expected error, got nil")
		}
	})
}

func TestManagerAcquireReleaseCycle(t *testing.T) {
	cfg := Config{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	mock := NewMockExecutor()
	manager, err := NewManager(cfg, mock)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	// Initial state - not acquired
	mock.SetResponse("ip addr show", "", nil)
	mock.SetResponse("ip addr add", "", nil)
	mock.SetResponse("arping", "", nil)

	err = manager.Acquire(context.Background())
	if err != nil {
		t.Errorf("Acquire() error = %v", err)
	}

	// After acquire, IP should be present
	mock.Reset()
	mock.SetResponse("ip addr show", "inet 192.168.1.100/24 scope global eth0", nil)
	mock.SetResponse("ip addr del", "", nil)

	err = manager.Release(context.Background())
	if err != nil {
		t.Errorf("Release() error = %v", err)
	}

	// After release, IP should not be present
	mock.Reset()
	mock.SetResponse("ip addr show", "", nil)

	acquired, err := manager.IsAcquired(context.Background())
	if err != nil {
		t.Errorf("IsAcquired() error = %v", err)
	}
	if acquired {
		t.Error("IsAcquired() after release = true, want false")
	}
}

func TestManagerConcurrentAccess(t *testing.T) {
	cfg := Config{
		Address:   "192.168.1.100",
		Netmask:   24,
		Interface: "eth0",
	}

	mock := NewMockExecutor()
	mock.SetResponse("ip addr show", "", nil)
	mock.SetResponse("ip addr add", "", nil)
	mock.SetResponse("ip addr del", "", nil)
	mock.SetResponse("arping", "", nil)

	manager, err := NewManager(cfg, mock)
	if err != nil {
		t.Fatalf("NewManager() error: %v", err)
	}

	var wg sync.WaitGroup
	ctx := context.Background()

	// Concurrent acquire/release/check operations
	for i := 0; i < 10; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			_ = manager.Acquire(ctx)
		}()

		go func() {
			defer wg.Done()
			_ = manager.Release(ctx)
		}()

		go func() {
			defer wg.Done()
			_, _ = manager.IsAcquired(ctx)
		}()
	}

	wg.Wait()
	// If no race conditions occur, the test passes
}

func TestDefaultExecutor(t *testing.T) {
	// Test that DefaultExecutor implements Executor interface
	var _ Executor = DefaultExecutor{}

	// We can't easily test actual command execution without side effects,
	// so we just verify the interface is satisfied
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
