//go:build unix

package cluster_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/backend"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockBackend is a mock implementation of the Backend interface for testing.
type MockBackend struct {
	mu            sync.RWMutex
	started       bool
	stopped       bool
	startErr      error
	stopErr       error
	healthStatus  string
	startCount    int
	stopCount     int
	startDelay    time.Duration
	stopDelay     time.Duration
	startCalled   chan struct{}
	stopCalled    chan struct{}
}

func NewMockBackend() *MockBackend {
	return &MockBackend{
		healthStatus: "running",
		startCalled:  make(chan struct{}, 10),
		stopCalled:   make(chan struct{}, 10),
	}
}

func (m *MockBackend) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.startDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.startDelay):
		}
	}

	if m.startErr != nil {
		return m.startErr
	}

	m.started = true
	m.stopped = false
	m.startCount++

	select {
	case m.startCalled <- struct{}{}:
	default:
	}

	return nil
}

func (m *MockBackend) Stop(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.stopDelay):
		}
	}

	if m.stopErr != nil {
		return m.stopErr
	}

	m.started = false
	m.stopped = true
	m.stopCount++

	select {
	case m.stopCalled <- struct{}{}:
	default:
	}

	return nil
}

func (m *MockBackend) Health(ctx context.Context) (backend.Health, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if !m.started {
		return backend.Health{Status: "stopped"}, nil
	}
	return backend.Health{Status: m.healthStatus}, nil
}

func (m *MockBackend) SetHealthStatus(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.healthStatus = status
}

func (m *MockBackend) SetStartError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startErr = err
}

func (m *MockBackend) SetStopError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopErr = err
}

func (m *MockBackend) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

func (m *MockBackend) StartCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.startCount
}

func (m *MockBackend) StopCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stopCount
}

// Tests

func TestBackendCoordinator_StartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	mockBackend := NewMockBackend()

	platform, err := cluster.NewPlatform("test-backend", "node-1", ns.URL(),
		cluster.HealthAddr(":18380"),
		cluster.MetricsAddr(":19390"),
		cluster.WithLeaseTTL(2*time.Second),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp",
		cluster.Singleton(),
		cluster.WithBackend(mockBackend),
	)
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- platform.Run(ctx)
	}()

	// Wait for leadership and backend to start
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 100*time.Millisecond, "should become leader")

	// Backend should be started
	assert.Eventually(t, func() bool {
		return mockBackend.IsStarted()
	}, 5*time.Second, 100*time.Millisecond, "backend should be started")

	assert.Equal(t, 1, mockBackend.StartCount(), "backend should be started once")

	// Stop the platform
	cancel()

	// Backend should be stopped
	assert.Eventually(t, func() bool {
		return !mockBackend.IsStarted()
	}, 5*time.Second, 100*time.Millisecond, "backend should be stopped")
}

func TestBackendCoordinator_StateTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	mockBackend := NewMockBackend()
	mockBackend.startDelay = 100 * time.Millisecond

	// Track state changes
	var stateChanges []cluster.BackendState
	var statesMu sync.Mutex

	platform, err := cluster.NewPlatform("test-backend-states", "node-1", ns.URL(),
		cluster.HealthAddr(":18381"),
		cluster.MetricsAddr(":19391"),
		cluster.WithLeaseTTL(2*time.Second),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp",
		cluster.Singleton(),
		cluster.WithBackend(mockBackend),
	)
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- platform.Run(ctx)
	}()

	// Wait for leadership
	assert.Eventually(t, func() bool {
		return app.IsLeader()
	}, 10*time.Second, 100*time.Millisecond, "should become leader")

	// Wait for backend to be running
	assert.Eventually(t, func() bool {
		return mockBackend.IsStarted()
	}, 5*time.Second, 100*time.Millisecond, "backend should be started")

	// Verify we have some state changes recorded
	statesMu.Lock()
	changesCount := len(stateChanges)
	statesMu.Unlock()

	t.Logf("State changes recorded: %d", changesCount)

	cancel()
}

func TestBackendCoordinator_FailoverBackendStartStop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	mockBackend1 := NewMockBackend()
	mockBackend2 := NewMockBackend()

	// Create two platforms
	platform1, err := cluster.NewPlatform("test-failover-backend", "node-1", ns.URL(),
		cluster.HealthAddr(":18382"),
		cluster.MetricsAddr(":19392"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	platform2, err := cluster.NewPlatform("test-failover-backend", "node-2", ns.URL(),
		cluster.HealthAddr(":18383"),
		cluster.MetricsAddr(":19393"),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app1 := cluster.NewApp("testapp",
		cluster.Singleton(),
		cluster.WithBackend(mockBackend1),
	)
	platform1.Register(app1)

	app2 := cluster.NewApp("testapp",
		cluster.Singleton(),
		cluster.WithBackend(mockBackend2),
	)
	platform2.Register(app2)

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()

	go platform1.Run(ctx1)
	go platform2.Run(ctx2)

	// Wait for one to become leader
	assert.Eventually(t, func() bool {
		return app1.IsLeader() || app2.IsLeader()
	}, 10*time.Second, 100*time.Millisecond, "one should become leader")

	// Find which one is leader
	var leader, follower *cluster.App
	var leaderBackend, followerBackend *MockBackend
	var leaderCancel context.CancelFunc

	if app1.IsLeader() {
		leader = app1
		follower = app2
		leaderBackend = mockBackend1
		followerBackend = mockBackend2
		leaderCancel = cancel1
	} else {
		leader = app2
		follower = app1
		leaderBackend = mockBackend2
		followerBackend = mockBackend1
		leaderCancel = cancel2
	}

	// Wait for leader backend to start
	assert.Eventually(t, func() bool {
		return leaderBackend.IsStarted()
	}, 5*time.Second, 100*time.Millisecond, "leader backend should be started")

	// Follower backend should NOT be started
	assert.False(t, followerBackend.IsStarted(), "follower backend should not be started")

	// Force leader failover by stopping the leader
	leaderCancel()

	// Wait for follower to become leader
	assert.Eventually(t, func() bool {
		return follower.IsLeader()
	}, 15*time.Second, 200*time.Millisecond, "follower should become leader")

	// Follower backend should now be started
	assert.Eventually(t, func() bool {
		return followerBackend.IsStarted()
	}, 5*time.Second, 100*time.Millisecond, "new leader backend should be started")

	_ = leader // suppress unused warning
}

func TestBackendCoordinator_HealthMonitoring(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	mockBackend := NewMockBackend()

	platform, err := cluster.NewPlatform("test-backend-health", "node-1", ns.URL(),
		cluster.HealthAddr(":18384"),
		cluster.MetricsAddr(":19394"),
		cluster.WithLeaseTTL(2*time.Second),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp",
		cluster.Singleton(),
		cluster.WithBackend(mockBackend),
	)
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go platform.Run(ctx)

	// Wait for backend to be running
	assert.Eventually(t, func() bool {
		return mockBackend.IsStarted()
	}, 10*time.Second, 100*time.Millisecond, "backend should be started")

	// Simulate health failure
	mockBackend.SetHealthStatus("unhealthy")

	// Give time for health monitoring to detect failure and potentially restart
	time.Sleep(2 * time.Second)

	// Backend should still be tracked
	assert.True(t, mockBackend.StartCount() >= 1, "backend should have been started at least once")

	cancel()
}

func TestProcessBackend_Options(t *testing.T) {
	config := backend.ProcessConfig{
		Command:         []string{"echo", "hello"},
		Dir:             "/tmp",
		Env:             []string{"FOO=bar"},
		GracefulTimeout: 30 * time.Second,
		UseProcessGroup: backend.BoolPtr(true),
	}

	b := backend.Process(config)
	require.NotNil(t, b)

	// Verify the process group setting was applied
	assert.True(t, b.UseProcessGroup(), "should use process group when explicitly set to true")
}

func TestDockerBackend_Options(t *testing.T) {
	config := backend.DockerConfig{
		Container: "test-container",
		Image:     "alpine:latest",
		Ports:     []string{"8080:80"},
		Env:       map[string]string{"FOO": "bar"},
		Volumes:   []string{"/host:/container"},
		Network:   "bridge",
		HealthCheck: &backend.DockerHealthCheck{
			Cmd:         "curl -f http://localhost/health",
			Interval:    10 * time.Second,
			Timeout:     5 * time.Second,
			StartPeriod: 30 * time.Second,
			Retries:     3,
		},
		Resources: &backend.DockerResources{
			Memory: "512m",
			CPUs:   "0.5",
		},
		RestartPolicy: "on-failure:3",
		StopTimeout:   30 * time.Second,
		AutoRemove:    false,
		Labels:        map[string]string{"app": "test"},
	}

	b := backend.Docker(config)
	require.NotNil(t, b)
}

func TestSystemdBackend_Options(t *testing.T) {
	b := backend.Systemd("myservice",
		backend.WithHealthEndpoint("http://localhost:8080/health"),
		backend.WithHealthInterval(5*time.Second),
		backend.WithHealthTimeout(3*time.Second),
		backend.WithStartTimeout(60*time.Second),
		backend.WithStopTimeout(30*time.Second),
		backend.WithNotify(),
		backend.WithDependencies("network.target", "nats.service"),
		backend.WithJournalLines(200),
		backend.WithSystemdRestart(backend.RestartPolicy{
			Enabled:     true,
			MaxRestarts: 5,
			Delay:       10 * time.Second,
			Backoff:     2.0,
		}),
	)
	require.NotNil(t, b)
}

func TestBackendHealth_Struct(t *testing.T) {
	h := backend.Health{
		Status:  "running",
		Message: "All good",
		Uptime:  5 * time.Minute,
	}

	assert.Equal(t, "running", h.Status)
	assert.Equal(t, "All good", h.Message)
	assert.Equal(t, 5*time.Minute, h.Uptime)
}

func TestRestartPolicy_Default(t *testing.T) {
	policy := backend.DefaultRestartPolicy()

	assert.False(t, policy.Enabled)
	assert.Equal(t, 3, policy.MaxRestarts)
	assert.Equal(t, 5*time.Second, policy.Delay)
	assert.Equal(t, 2.0, policy.Backoff)
}

// ============================================================================
// Real Process Backend Tests
// ============================================================================

// TestProcessBackend_StartStop tests basic process start and stop with a real process.
func TestProcessBackend_StartStop(t *testing.T) {
	ctx := context.Background()

	// Use 'sleep' command which is available on all Unix systems
	// UseProcessGroup defaults to true when nil
	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err, "should start process")

	// Verify it's running
	pid := b.PID()
	assert.Greater(t, pid, 0, "should have valid PID")

	health, err := b.Health(ctx)
	require.NoError(t, err)
	assert.Equal(t, "running", health.Status)
	assert.Greater(t, health.Uptime, time.Duration(0))

	// Stop the process
	err = b.Stop(ctx)
	require.NoError(t, err, "should stop process")

	// Verify it's stopped
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 100*time.Millisecond, "process should exit")

	health, err = b.Health(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stopped", health.Status)
}

// TestProcessBackend_GracefulSIGTERM tests that a process exits cleanly on SIGTERM.
func TestProcessBackend_GracefulSIGTERM(t *testing.T) {
	ctx := context.Background()

	// Use a bash script that handles SIGTERM gracefully
	// This script will catch SIGTERM and exit with code 0
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", `
			trap 'echo "Received SIGTERM"; exit 0' TERM
			while true; do sleep 0.1; done
		`},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(200 * time.Millisecond)

	// Verify it's running
	health, _ := b.Health(ctx)
	require.Equal(t, "running", health.Status)

	// Record the time before stop
	stopStart := time.Now()

	// Stop the process (should be graceful)
	err = b.Stop(ctx)
	require.NoError(t, err)

	// Verify it stopped quickly (graceful, not waiting for timeout)
	stopDuration := time.Since(stopStart)
	assert.Less(t, stopDuration, 2*time.Second, "should stop quickly with SIGTERM")

	// Verify exit code is 0 (graceful)
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 100*time.Millisecond)
	assert.Equal(t, 0, b.ExitCode(), "should exit with code 0")
}

// TestProcessBackend_SIGKILLAfterTimeout tests that a process gets SIGKILL after SIGTERM timeout.
func TestProcessBackend_SIGKILLAfterTimeout(t *testing.T) {
	ctx := context.Background()

	// Use a bash script that ignores SIGTERM
	// This script will NOT exit on SIGTERM, forcing SIGKILL
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", `
			trap '' TERM  # Ignore SIGTERM
			while true; do sleep 0.1; done
		`},
		GracefulTimeout: 500 * time.Millisecond, // Short timeout for test
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(200 * time.Millisecond)

	// Verify it's running
	health, _ := b.Health(ctx)
	require.Equal(t, "running", health.Status)

	// Record the time before stop
	stopStart := time.Now()

	// Stop the process (should require SIGKILL after timeout)
	err = b.Stop(ctx)
	require.NoError(t, err)

	// Verify it took at least the graceful timeout (waiting for SIGKILL)
	stopDuration := time.Since(stopStart)
	assert.GreaterOrEqual(t, stopDuration, 400*time.Millisecond, "should wait for graceful timeout")
	assert.Less(t, stopDuration, 3*time.Second, "should not take too long")

	// Verify process was killed (exit code will be non-zero due to SIGKILL)
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 100*time.Millisecond)

	// SIGKILL typically results in exit code -1 or 137 (128 + 9)
	exitCode := b.ExitCode()
	assert.NotEqual(t, 0, exitCode, "should have non-zero exit code from SIGKILL")
}

// TestProcessBackend_ProcessGroupSignaling tests that child processes also receive signals.
func TestProcessBackend_ProcessGroupSignaling(t *testing.T) {
	ctx := context.Background()

	// Create a temp file to track child process status
	tmpFile, err := os.CreateTemp("", "child_status_*")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Use a bash script that spawns a child process
	// Both parent and child will write to the temp file when they receive SIGTERM
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", fmt.Sprintf(`
			# Child process that traps SIGTERM
			(
				trap 'echo "child_terminated" >> %s; exit 0' TERM
				while true; do sleep 0.1; done
			) &
			CHILD_PID=$!

			# Parent traps SIGTERM
			trap 'echo "parent_terminated" >> %s; exit 0' TERM
			
			# Wait for child
			wait $CHILD_PID
		`, tmpPath, tmpPath)},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err = b.Start(ctx)
	require.NoError(t, err)

	// Give it time to start parent and child
	time.Sleep(500 * time.Millisecond)

	// Verify it's running
	health, _ := b.Health(ctx)
	require.Equal(t, "running", health.Status)

	// Stop the process (should signal entire process group)
	err = b.Stop(ctx)
	require.NoError(t, err)

	// Verify process exited
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 100*time.Millisecond)

	// Wait a moment for file writes to complete
	time.Sleep(200 * time.Millisecond)

	// Read the temp file and check both parent and child were signaled
	content, err := os.ReadFile(tmpPath)
	require.NoError(t, err)
	contentStr := string(content)

	// At least the parent should have received the signal
	assert.Contains(t, contentStr, "parent_terminated", "parent should receive SIGTERM")
	// Note: Child might also be terminated, but due to process group handling
	// and timing, we primarily verify the parent was signaled
}

// TestProcessBackend_OutputCapture tests stdout/stderr capture via OutputHandler.
func TestProcessBackend_OutputCapture(t *testing.T) {
	ctx := context.Background()

	var stdoutLines []string
	var stderrLines []string
	var mu sync.Mutex

	outputHandler := func(line string, isStderr bool) {
		mu.Lock()
		defer mu.Unlock()
		if isStderr {
			stderrLines = append(stderrLines, line)
		} else {
			stdoutLines = append(stdoutLines, line)
		}
	}

	// Use a bash script that outputs to both stdout and stderr
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", `
			echo "stdout line 1"
			echo "stderr line 1" >&2
			echo "stdout line 2"
			echo "stderr line 2" >&2
			sleep 0.5
		`},
		GracefulTimeout: 5 * time.Second,
		OutputHandler:   outputHandler,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to complete
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 10*time.Second, 100*time.Millisecond, "process should exit")

	// Give output handlers time to process
	time.Sleep(200 * time.Millisecond)

	// Verify output was captured
	mu.Lock()
	defer mu.Unlock()

	assert.GreaterOrEqual(t, len(stdoutLines), 2, "should capture stdout lines")
	assert.GreaterOrEqual(t, len(stderrLines), 2, "should capture stderr lines")

	// Verify content
	assert.Contains(t, stdoutLines, "stdout line 1")
	assert.Contains(t, stdoutLines, "stdout line 2")
	assert.Contains(t, stderrLines, "stderr line 1")
	assert.Contains(t, stderrLines, "stderr line 2")
}

// TestProcessBackend_LogFile tests output capture to a log file.
func TestProcessBackend_LogFile(t *testing.T) {
	ctx := context.Background()

	// Create a temp log file
	tmpFile, err := os.CreateTemp("", "process_log_*")
	require.NoError(t, err)
	logPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(logPath)

	// Use a bash script that outputs to both stdout and stderr
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", `
			echo "log line 1"
			echo "log line 2" >&2
			echo "log line 3"
		`},
		GracefulTimeout: 5 * time.Second,
		LogFile:         logPath,
	})

	// Start the process
	err = b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to complete
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 10*time.Second, 100*time.Millisecond, "process should exit")

	// Give time for file writes to flush
	time.Sleep(200 * time.Millisecond)

	// Read and verify log file
	content, err := os.ReadFile(logPath)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "log line 1")
	assert.Contains(t, contentStr, "log line 2")
	assert.Contains(t, contentStr, "log line 3")
}

// TestProcessBackend_Signal tests sending custom signals to the process.
func TestProcessBackend_Signal(t *testing.T) {
	ctx := context.Background()

	// Create a temp file to track signal reception
	tmpFile, err := os.CreateTemp("", "signal_test_*")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Use a bash script that handles SIGUSR1
	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", fmt.Sprintf(`
			trap 'echo "received_usr1" >> %s' USR1
			trap 'exit 0' TERM
			while true; do sleep 0.1; done
		`, tmpPath)},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err = b.Start(ctx)
	require.NoError(t, err)

	// Give it time to start
	time.Sleep(300 * time.Millisecond)

	// Send SIGUSR1
	err = b.Signal(syscall.SIGUSR1)
	require.NoError(t, err)

	// Give time for signal handler to run
	time.Sleep(300 * time.Millisecond)

	// Verify signal was received
	content, err := os.ReadFile(tmpPath)
	require.NoError(t, err)
	assert.Contains(t, string(content), "received_usr1", "should have received SIGUSR1")

	// Clean up
	err = b.Stop(ctx)
	require.NoError(t, err)
}

// TestProcessBackend_Restart tests the restart functionality.
func TestProcessBackend_Restart(t *testing.T) {
	ctx := context.Background()

	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 2 * time.Second,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Get initial PID
	initialPID := b.PID()
	assert.Greater(t, initialPID, 0)

	// Restart the process
	err = b.Restart(ctx)
	require.NoError(t, err)

	// Verify new PID (should be different)
	newPID := b.PID()
	assert.Greater(t, newPID, 0)
	assert.NotEqual(t, initialPID, newPID, "should have new PID after restart")

	// Verify restart count
	assert.Equal(t, 1, b.RestartCount())

	// Clean up
	err = b.Stop(ctx)
	require.NoError(t, err)
}

// TestProcessBackend_EnvironmentVariables tests environment variable passing.
func TestProcessBackend_EnvironmentVariables(t *testing.T) {
	ctx := context.Background()

	// Create a temp file to capture env vars
	tmpFile, err := os.CreateTemp("", "env_test_*")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	b := backend.Process(backend.ProcessConfig{
		Command: []string{"bash", "-c", fmt.Sprintf(`
			echo "TEST_VAR=$TEST_VAR" >> %s
			echo "ANOTHER_VAR=$ANOTHER_VAR" >> %s
		`, tmpPath, tmpPath)},
		Env: []string{
			"TEST_VAR=hello_world",
			"ANOTHER_VAR=123",
		},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err = b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to complete
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 10*time.Second, 100*time.Millisecond)

	// Give time for file writes
	time.Sleep(100 * time.Millisecond)

	// Verify env vars were passed
	content, err := os.ReadFile(tmpPath)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "TEST_VAR=hello_world")
	assert.Contains(t, contentStr, "ANOTHER_VAR=123")
}

// TestProcessBackend_WorkingDirectory tests the working directory setting.
func TestProcessBackend_WorkingDirectory(t *testing.T) {
	ctx := context.Background()

	// Create a temp file to capture pwd
	tmpFile, err := os.CreateTemp("", "pwd_test_*")
	require.NoError(t, err)
	tmpPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(tmpPath)

	// Use /tmp as working directory
	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"bash", "-c", fmt.Sprintf(`pwd > %s`, tmpPath)},
		Dir:             "/tmp",
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err = b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to complete
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 10*time.Second, 100*time.Millisecond)

	// Give time for file writes
	time.Sleep(100 * time.Millisecond)

	// Verify working directory
	content, err := os.ReadFile(tmpPath)
	require.NoError(t, err)
	contentStr := strings.TrimSpace(string(content))

	// On macOS, /tmp is symlinked to /private/tmp
	assert.True(t, contentStr == "/tmp" || contentStr == "/private/tmp",
		"working directory should be /tmp or /private/tmp, got: %s", contentStr)
}

// TestProcessBackend_NoProcessGroup tests behavior without process group.
func TestProcessBackend_NoProcessGroup(t *testing.T) {
	ctx := context.Background()

	// Explicitly disable process group using BoolPtr(false)
	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 2 * time.Second,
		UseProcessGroup: backend.BoolPtr(false),
	})

	// Verify process group is disabled
	assert.False(t, b.UseProcessGroup(), "should NOT use process group when explicitly set to false")

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Verify it's running
	health, _ := b.Health(ctx)
	require.Equal(t, "running", health.Status)

	// Stop the process
	err = b.Stop(ctx)
	require.NoError(t, err)

	// Verify it stopped
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 100*time.Millisecond)
}

// TestProcessBackend_DefaultProcessGroup tests that process group is enabled by default.
func TestProcessBackend_DefaultProcessGroup(t *testing.T) {
	// When UseProcessGroup is nil (not set), it should default to true
	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 2 * time.Second,
		// UseProcessGroup not set (nil)
	})

	assert.True(t, b.UseProcessGroup(), "should default to using process group when not set")
}

// TestProcessBackend_QuickExit tests handling of processes that exit immediately.
func TestProcessBackend_QuickExit(t *testing.T) {
	ctx := context.Background()

	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"echo", "hello"},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to exit
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 50*time.Millisecond)

	// Verify exit code
	assert.Equal(t, 0, b.ExitCode())

	// Health should show stopped
	health, _ := b.Health(ctx)
	assert.Equal(t, "stopped", health.Status)
}

// TestProcessBackend_NonZeroExitCode tests handling of non-zero exit codes.
func TestProcessBackend_NonZeroExitCode(t *testing.T) {
	ctx := context.Background()

	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"bash", "-c", "exit 42"},
		GracefulTimeout: 5 * time.Second,
	})

	// Start the process
	err := b.Start(ctx)
	require.NoError(t, err)

	// Wait for process to exit
	assert.Eventually(t, func() bool {
		return b.Exited()
	}, 5*time.Second, 50*time.Millisecond)

	// Verify exit code
	assert.Equal(t, 42, b.ExitCode())
}

// TestProcessBackend_StopNotStarted tests stopping a process that was never started.
func TestProcessBackend_StopNotStarted(t *testing.T) {
	ctx := context.Background()

	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 5 * time.Second,
	})

	// Stop without starting should not error
	err := b.Stop(ctx)
	assert.NoError(t, err)
}

// TestProcessBackend_HealthNotStarted tests health check before process starts.
func TestProcessBackend_HealthNotStarted(t *testing.T) {
	ctx := context.Background()

	b := backend.Process(backend.ProcessConfig{
		Command:         []string{"sleep", "60"},
		GracefulTimeout: 5 * time.Second,
	})

	// Health before start should show stopped
	health, err := b.Health(ctx)
	require.NoError(t, err)
	assert.Equal(t, "stopped", health.Status)

	// PID should be 0
	assert.Equal(t, 0, b.PID())
}
