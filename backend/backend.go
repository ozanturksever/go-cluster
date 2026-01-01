// Package backend provides backend controllers for managing external services.
package backend

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Backend defines the interface for managing external services.
type Backend interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) (Health, error)
}

// Health represents the health status of a backend.
type Health struct {
	Status  string        `json:"status"`
	Message string        `json:"message,omitempty"`
	Uptime  time.Duration `json:"uptime,omitempty"`
}

// OutputHandler is a callback for process output.
type OutputHandler func(line string, isStderr bool)

// RestartPolicy defines how a backend should be restarted on failure.
type RestartPolicy struct {
	Enabled     bool
	MaxRestarts int
	Delay       time.Duration
	Backoff     float64 // Multiplier for delay on each restart
}

// DefaultRestartPolicy returns a sensible default restart policy.
func DefaultRestartPolicy() RestartPolicy {
	return RestartPolicy{
		Enabled:     false,
		MaxRestarts: 3,
		Delay:       5 * time.Second,
		Backoff:     2.0,
	}
}

// ============================================================================
// Systemd Backend
// ============================================================================

// SystemdBackend manages a systemd service.
type SystemdBackend struct {
	serviceName    string
	healthEndpoint string
	healthInterval time.Duration
	healthTimeout  time.Duration
	startTimeout   time.Duration
	stopTimeout    time.Duration

	// Systemd notify support
	notifySocket string
	useNotify    bool

	// Service dependencies
	dependencies []string

	// Journal logging
	journalLines int

	// Restart handling
	restartPolicy RestartPolicy
	restartCount  int

	// State tracking
	mu        sync.RWMutex
	startTime time.Time
}

// SystemdOption configures a SystemdBackend.
type SystemdOption func(*SystemdBackend)

// Systemd creates a new systemd backend.
func Systemd(serviceName string, opts ...SystemdOption) *SystemdBackend {
	b := &SystemdBackend{
		serviceName:    serviceName,
		healthInterval: 5 * time.Second,
		healthTimeout:  3 * time.Second,
		startTimeout:   60 * time.Second,
		stopTimeout:    30 * time.Second,
		journalLines:   100,
		restartPolicy:  DefaultRestartPolicy(),
	}
	for _, opt := range opts {
		opt(b)
	}
	return b
}

// WithHealthEndpoint sets the health check endpoint.
func WithHealthEndpoint(url string) SystemdOption {
	return func(b *SystemdBackend) {
		b.healthEndpoint = url
	}
}

// WithHealthInterval sets the health check interval.
func WithHealthInterval(d time.Duration) SystemdOption {
	return func(b *SystemdBackend) {
		b.healthInterval = d
	}
}

// WithHealthTimeout sets the health check timeout.
func WithHealthTimeout(d time.Duration) SystemdOption {
	return func(b *SystemdBackend) {
		b.healthTimeout = d
	}
}

// WithStartTimeout sets the startup timeout.
func WithStartTimeout(d time.Duration) SystemdOption {
	return func(b *SystemdBackend) {
		b.startTimeout = d
	}
}

// WithStopTimeout sets the stop timeout.
func WithStopTimeout(d time.Duration) SystemdOption {
	return func(b *SystemdBackend) {
		b.stopTimeout = d
	}
}

// WithNotify enables systemd notify integration (Type=notify).
func WithNotify() SystemdOption {
	return func(b *SystemdBackend) {
		b.useNotify = true
		b.notifySocket = os.Getenv("NOTIFY_SOCKET")
	}
}

// WithDependencies sets service dependencies to check before starting.
func WithDependencies(services ...string) SystemdOption {
	return func(b *SystemdBackend) {
		b.dependencies = services
	}
}

// WithJournalLines sets the number of journal lines to retrieve.
func WithJournalLines(n int) SystemdOption {
	return func(b *SystemdBackend) {
		b.journalLines = n
	}
}

// WithSystemdRestart configures restart policy for systemd backend.
func WithSystemdRestart(policy RestartPolicy) SystemdOption {
	return func(b *SystemdBackend) {
		b.restartPolicy = policy
	}
}

// Start starts the systemd service.
func (b *SystemdBackend) Start(ctx context.Context) error {
	// Check dependencies first
	for _, dep := range b.dependencies {
		if err := b.checkServiceRunning(ctx, dep); err != nil {
			return fmt.Errorf("dependency %s not running: %w", dep, err)
		}
	}

	cmd := exec.CommandContext(ctx, "systemctl", "start", b.serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start service %s: %w", b.serviceName, err)
	}

	b.mu.Lock()
	b.startTime = time.Now()
	b.mu.Unlock()

	// Wait for service to be healthy
	if b.healthEndpoint != "" {
		deadline := time.Now().Add(b.startTimeout)
		for time.Now().Before(deadline) {
			health, err := b.Health(ctx)
			if err == nil && health.Status == "running" {
				// Send notify if configured
				if b.useNotify && b.notifySocket != "" {
					b.sendNotify("READY=1")
				}
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(b.healthInterval):
			}
		}
		return fmt.Errorf("service %s failed to become healthy within %v", b.serviceName, b.startTimeout)
	}

	// Send notify if configured (no health endpoint case)
	if b.useNotify && b.notifySocket != "" {
		b.sendNotify("READY=1")
	}

	return nil
}

// Stop stops the systemd service.
func (b *SystemdBackend) Stop(ctx context.Context) error {
	// Send notify if configured
	if b.useNotify && b.notifySocket != "" {
		b.sendNotify("STOPPING=1")
	}

	// Create context with stop timeout
	stopCtx, cancel := context.WithTimeout(ctx, b.stopTimeout)
	defer cancel()

	cmd := exec.CommandContext(stopCtx, "systemctl", "stop", b.serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", b.serviceName, err)
	}
	return nil
}

// Health checks the health of the systemd service.
func (b *SystemdBackend) Health(ctx context.Context) (Health, error) {
	// Check systemd status
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", b.serviceName)
	output, err := cmd.Output()
	if err != nil {
		return Health{Status: "stopped"}, nil
	}

	status := strings.TrimSpace(string(output))
	if status == "active" {
		// Calculate uptime
		b.mu.RLock()
		uptime := time.Duration(0)
		if !b.startTime.IsZero() {
			uptime = time.Since(b.startTime)
		}
		b.mu.RUnlock()

		// If we have a health endpoint, check it too
		if b.healthEndpoint != "" {
			client := &http.Client{Timeout: b.healthTimeout}
			resp, err := client.Get(b.healthEndpoint)
			if err != nil {
				return Health{Status: "unhealthy", Message: err.Error(), Uptime: uptime}, nil
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return Health{Status: "running", Uptime: uptime}, nil
			}
			return Health{Status: "unhealthy", Message: fmt.Sprintf("health check returned %d", resp.StatusCode), Uptime: uptime}, nil
		}
		return Health{Status: "running", Uptime: uptime}, nil
	}

	return Health{Status: "stopped"}, nil
}

// Restart restarts the systemd service.
func (b *SystemdBackend) Restart(ctx context.Context) error {
	b.mu.Lock()
	b.restartCount++
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, "systemctl", "restart", b.serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart service %s: %w", b.serviceName, err)
	}

	b.mu.Lock()
	b.startTime = time.Now()
	b.mu.Unlock()

	return nil
}

// JournalLogs returns recent journal logs for the service.
func (b *SystemdBackend) JournalLogs(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "journalctl", "-u", b.serviceName, "-n", fmt.Sprintf("%d", b.journalLines), "--no-pager", "-o", "short-iso")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to get journal logs: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return lines, nil
}

// RestartCount returns the number of restarts.
func (b *SystemdBackend) RestartCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.restartCount
}

// checkServiceRunning checks if a systemd service is running.
func (b *SystemdBackend) checkServiceRunning(ctx context.Context, serviceName string) error {
	cmd := exec.CommandContext(ctx, "systemctl", "is-active", serviceName)
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("service not active")
	}
	if strings.TrimSpace(string(output)) != "active" {
		return fmt.Errorf("service not active")
	}
	return nil
}

// sendNotify sends a notification to systemd.
func (b *SystemdBackend) sendNotify(state string) error {
	if b.notifySocket == "" {
		return nil
	}

	conn, err := net.Dial("unixgram", b.notifySocket)
	if err != nil {
		return fmt.Errorf("failed to connect to notify socket: %w", err)
	}
	defer conn.Close()

	_, err = conn.Write([]byte(state))
	return err
}

// ============================================================================
// Docker Backend
// ============================================================================

// DockerHealthCheck configures container health check.
type DockerHealthCheck struct {
	Cmd         string        // Health check command
	Interval    time.Duration // Time between checks
	Timeout     time.Duration // Timeout for each check
	StartPeriod time.Duration // Grace period before checks start
	Retries     int           // Number of retries before unhealthy
}

// DockerResources configures container resource limits.
type DockerResources struct {
	Memory     string // Memory limit (e.g., "512m", "2g")
	CPUs       string // CPU limit (e.g., "0.5", "2")
	MemorySwap string // Memory + swap limit
	Pids       int    // Max PIDs
}

// DockerConfig configures a Docker backend.
type DockerConfig struct {
	Container     string
	Image         string
	Ports         []string
	Env           map[string]string
	Volumes       []string
	Network       string
	HealthCheck   *DockerHealthCheck
	Resources     *DockerResources
	RestartPolicy string        // "no", "on-failure:N", "always", "unless-stopped"
	StopTimeout   time.Duration // Graceful stop timeout
	AutoRemove    bool          // Remove container after stop
	Labels        map[string]string
	ExtraArgs     []string // Additional docker run arguments
}

// DockerBackend manages a Docker container.
type DockerBackend struct {
	config DockerConfig

	mu           sync.RWMutex
	startTime    time.Time
	restartCount int
}

// Docker creates a new Docker backend.
func Docker(config DockerConfig) *DockerBackend {
	if config.StopTimeout == 0 {
		config.StopTimeout = 10 * time.Second
	}
	return &DockerBackend{config: config}
}

// Start starts the Docker container.
func (b *DockerBackend) Start(ctx context.Context) error {
	// Remove existing container if exists
	exec.CommandContext(ctx, "docker", "rm", "-f", b.config.Container).Run()

	args := []string{"run", "-d", "--name", b.config.Container}

	// Port mappings
	for _, port := range b.config.Ports {
		args = append(args, "-p", port)
	}

	// Environment variables
	for k, v := range b.config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	// Volume mounts
	for _, vol := range b.config.Volumes {
		args = append(args, "-v", vol)
	}

	// Network
	if b.config.Network != "" {
		args = append(args, "--network", b.config.Network)
	}

	// Labels
	for k, v := range b.config.Labels {
		args = append(args, "--label", fmt.Sprintf("%s=%s", k, v))
	}

	// Health check
	if b.config.HealthCheck != nil {
		hc := b.config.HealthCheck
		if hc.Cmd != "" {
			args = append(args, "--health-cmd", hc.Cmd)
		}
		if hc.Interval > 0 {
			args = append(args, "--health-interval", hc.Interval.String())
		}
		if hc.Timeout > 0 {
			args = append(args, "--health-timeout", hc.Timeout.String())
		}
		if hc.StartPeriod > 0 {
			args = append(args, "--health-start-period", hc.StartPeriod.String())
		}
		if hc.Retries > 0 {
			args = append(args, "--health-retries", fmt.Sprintf("%d", hc.Retries))
		}
	}

	// Resource limits
	if b.config.Resources != nil {
		res := b.config.Resources
		if res.Memory != "" {
			args = append(args, "--memory", res.Memory)
		}
		if res.CPUs != "" {
			args = append(args, "--cpus", res.CPUs)
		}
		if res.MemorySwap != "" {
			args = append(args, "--memory-swap", res.MemorySwap)
		}
		if res.Pids > 0 {
			args = append(args, "--pids-limit", fmt.Sprintf("%d", res.Pids))
		}
	}

	// Restart policy
	if b.config.RestartPolicy != "" {
		args = append(args, "--restart", b.config.RestartPolicy)
	}

	// Auto remove
	if b.config.AutoRemove {
		args = append(args, "--rm")
	}

	// Extra args
	args = append(args, b.config.ExtraArgs...)

	// Image
	args = append(args, b.config.Image)

	cmd := exec.CommandContext(ctx, "docker", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start container %s: %w", b.config.Container, err)
	}

	b.mu.Lock()
	b.startTime = time.Now()
	b.mu.Unlock()

	return nil
}

// Stop stops the Docker container.
func (b *DockerBackend) Stop(ctx context.Context) error {
	// Graceful stop with timeout
	stopCmd := exec.CommandContext(ctx, "docker", "stop", "-t", fmt.Sprintf("%d", int(b.config.StopTimeout.Seconds())), b.config.Container)
	if err := stopCmd.Run(); err != nil {
		// Force kill if graceful stop fails
		exec.CommandContext(ctx, "docker", "kill", b.config.Container).Run()
	}

	// Remove container if not auto-remove
	if !b.config.AutoRemove {
		removeCmd := exec.CommandContext(ctx, "docker", "rm", b.config.Container)
		removeCmd.Run() // Ignore error on remove
	}

	return nil
}

// Health checks the health of the Docker container.
func (b *DockerBackend) Health(ctx context.Context) (Health, error) {
	// Check container status
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", b.config.Container)
	output, err := cmd.Output()
	if err != nil {
		return Health{Status: "stopped"}, nil
	}

	status := strings.TrimSpace(string(output))

	// Calculate uptime
	b.mu.RLock()
	uptime := time.Duration(0)
	if !b.startTime.IsZero() {
		uptime = time.Since(b.startTime)
	}
	b.mu.RUnlock()

	if status == "running" {
		// Check Docker health status if available
		if b.config.HealthCheck != nil {
			healthCmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Health.Status}}", b.config.Container)
			healthOutput, err := healthCmd.Output()
			if err == nil {
				healthStatus := strings.TrimSpace(string(healthOutput))
				switch healthStatus {
				case "healthy":
					return Health{Status: "running", Uptime: uptime}, nil
				case "unhealthy":
					return Health{Status: "unhealthy", Message: "container health check failed", Uptime: uptime}, nil
				case "starting":
					return Health{Status: "starting", Message: "health check in progress", Uptime: uptime}, nil
				}
			}
		}
		return Health{Status: "running", Uptime: uptime}, nil
	}

	return Health{Status: status, Uptime: uptime}, nil
}

// Restart restarts the Docker container.
func (b *DockerBackend) Restart(ctx context.Context) error {
	b.mu.Lock()
	b.restartCount++
	b.mu.Unlock()

	cmd := exec.CommandContext(ctx, "docker", "restart", b.config.Container)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart container %s: %w", b.config.Container, err)
	}

	b.mu.Lock()
	b.startTime = time.Now()
	b.mu.Unlock()

	return nil
}

// Logs returns recent logs from the container.
func (b *DockerBackend) Logs(ctx context.Context, lines int) ([]string, error) {
	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", fmt.Sprintf("%d", lines), b.config.Container)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to get container logs: %w", err)
	}

	logLines := strings.Split(strings.TrimSpace(string(output)), "\n")
	return logLines, nil
}

// RestartCount returns the number of restarts.
func (b *DockerBackend) RestartCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.restartCount
}

// ============================================================================
// Process Backend
// ============================================================================

// ProcessConfig configures a Process backend.
type ProcessConfig struct {
	Command         []string
	Dir             string
	Env             []string
	GracefulTimeout time.Duration // Time to wait after SIGTERM before SIGKILL
	UseProcessGroup *bool         // Use process group for signal handling (default: true if nil)
	OutputHandler   OutputHandler // Callback for stdout/stderr lines
	LogFile         string        // Path to log file (alternative to OutputHandler)
	RestartPolicy   RestartPolicy
}

// ProcessBackend manages a child process.
type ProcessBackend struct {
	config ProcessConfig

	mu              sync.RWMutex
	cmd             *exec.Cmd
	startTime       time.Time
	restartCount    int
	exited          bool
	exitCode        int
	exitErr         error
	useProcessGroup bool // resolved value from config

	// Output handling
	logFile  *os.File
	waitDone chan struct{}

	// Process group ID for signal handling
	pgid int
}

// Process creates a new Process backend.
func Process(config ProcessConfig) *ProcessBackend {
	if config.GracefulTimeout == 0 {
		config.GracefulTimeout = 10 * time.Second
	}

	// Resolve UseProcessGroup: default to true if not explicitly set
	useProcessGroup := true
	if config.UseProcessGroup != nil {
		useProcessGroup = *config.UseProcessGroup
	}

	return &ProcessBackend{
		config:          config,
		useProcessGroup: useProcessGroup,
	}
}

// BoolPtr is a helper function to create a pointer to a bool.
// Use this to explicitly set UseProcessGroup in ProcessConfig.
func BoolPtr(b bool) *bool {
	return &b
}

// Start starts the process.
func (b *ProcessBackend) Start(ctx context.Context) error {
	if len(b.config.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	b.cmd = exec.CommandContext(ctx, b.config.Command[0], b.config.Command[1:]...)
	b.cmd.Dir = b.config.Dir
	b.cmd.Env = append(os.Environ(), b.config.Env...)

	// Set up process group for proper signal handling
	if b.useProcessGroup {
		b.cmd.SysProcAttr = &syscall.SysProcAttr{
			Setpgid: true,
		}
	}

	// Set up output handling
	if err := b.setupOutput(); err != nil {
		return fmt.Errorf("failed to setup output: %w", err)
	}

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	b.startTime = time.Now()
	b.exited = false
	b.exitCode = 0
	b.exitErr = nil

	// Store process group ID
	if b.useProcessGroup && b.cmd.Process != nil {
		b.pgid = b.cmd.Process.Pid
	}

	// Start a goroutine to wait for process exit
	b.waitDone = make(chan struct{})
	go b.waitForExit()

	return nil
}

// setupOutput configures output handling.
func (b *ProcessBackend) setupOutput() error {
	// Close previous log file if any
	if b.logFile != nil {
		b.logFile.Close()
		b.logFile = nil
	}

	// Set up log file if configured
	if b.config.LogFile != "" {
		f, err := os.OpenFile(b.config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return err
		}
		b.logFile = f
		b.cmd.Stdout = f
		b.cmd.Stderr = f
		return nil
	}

	// Set up output handler if configured
	if b.config.OutputHandler != nil {
		stdoutPipe, err := b.cmd.StdoutPipe()
		if err != nil {
			return err
		}
		stderrPipe, err := b.cmd.StderrPipe()
		if err != nil {
			return err
		}

		go b.readOutput(stdoutPipe, false)
		go b.readOutput(stderrPipe, true)
		return nil
	}

	// Default: inherit stdout/stderr
	b.cmd.Stdout = os.Stdout
	b.cmd.Stderr = os.Stderr
	return nil
}

// readOutput reads from a pipe and calls the output handler.
func (b *ProcessBackend) readOutput(r io.Reader, isStderr bool) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if b.config.OutputHandler != nil {
			b.config.OutputHandler(line, isStderr)
		}
	}
}

// waitForExit waits for the process to exit and records the result.
func (b *ProcessBackend) waitForExit() {
	defer close(b.waitDone)

	err := b.cmd.Wait()

	b.mu.Lock()
	b.exited = true
	if err != nil {
		b.exitErr = err
		if exitErr, ok := err.(*exec.ExitError); ok {
			b.exitCode = exitErr.ExitCode()
		} else {
			b.exitCode = -1
		}
	} else {
		b.exitCode = 0
	}
	b.mu.Unlock()

	// Close log file
	if b.logFile != nil {
		b.logFile.Close()
	}
}

// Stop stops the process.
func (b *ProcessBackend) Stop(ctx context.Context) error {
	b.mu.Lock()
	if b.cmd == nil || b.cmd.Process == nil {
		b.mu.Unlock()
		return nil
	}

	process := b.cmd.Process
	pgid := b.pgid
	useProcessGroup := b.useProcessGroup
	b.mu.Unlock()

	// Send SIGTERM (or to process group)
	var termErr error
	if useProcessGroup && pgid > 0 {
		termErr = syscall.Kill(-pgid, syscall.SIGTERM)
	} else {
		termErr = process.Signal(syscall.SIGTERM)
	}

	if termErr != nil {
		// Process might already be gone
		return nil
	}

	// Wait for graceful exit or timeout
	select {
	case <-b.waitDone:
		// Process exited gracefully
		return nil
	case <-time.After(b.config.GracefulTimeout):
		// Force kill
		if useProcessGroup && pgid > 0 {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			process.Kill()
		}
	case <-ctx.Done():
		// Context cancelled, force kill
		if useProcessGroup && pgid > 0 {
			syscall.Kill(-pgid, syscall.SIGKILL)
		} else {
			process.Kill()
		}
		return ctx.Err()
	}

	// Wait for final exit
	select {
	case <-b.waitDone:
	case <-time.After(5 * time.Second):
		// Give up waiting
	}

	return nil
}

// Health checks if the process is running.
func (b *ProcessBackend) Health(ctx context.Context) (Health, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.cmd == nil || b.cmd.Process == nil {
		return Health{Status: "stopped"}, nil
	}

	// Check if process has exited
	if b.exited {
		msg := ""
		if b.exitErr != nil {
			msg = b.exitErr.Error()
		}
		return Health{Status: "stopped", Message: msg}, nil
	}

	// Calculate uptime
	uptime := time.Duration(0)
	if !b.startTime.IsZero() {
		uptime = time.Since(b.startTime)
	}

	return Health{Status: "running", Uptime: uptime}, nil
}

// Restart restarts the process.
func (b *ProcessBackend) Restart(ctx context.Context) error {
	if err := b.Stop(ctx); err != nil {
		// Log but continue
	}

	b.mu.Lock()
	b.restartCount++
	b.mu.Unlock()

	return b.Start(ctx)
}

// ExitCode returns the exit code of the process (if exited).
func (b *ProcessBackend) ExitCode() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.exitCode
}

// Exited returns true if the process has exited.
func (b *ProcessBackend) Exited() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.exited
}

// RestartCount returns the number of restarts.
func (b *ProcessBackend) RestartCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.restartCount
}

// PID returns the process ID (0 if not running).
func (b *ProcessBackend) PID() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.cmd == nil || b.cmd.Process == nil {
		return 0
	}
	return b.cmd.Process.Pid
}

// Signal sends a signal to the process.
func (b *ProcessBackend) Signal(sig syscall.Signal) error {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.cmd == nil || b.cmd.Process == nil {
		return fmt.Errorf("process not running")
	}

	if b.useProcessGroup && b.pgid > 0 {
		return syscall.Kill(-b.pgid, sig)
	}
	return b.cmd.Process.Signal(sig)
}

// UseProcessGroup returns true if process group signaling is enabled.
func (b *ProcessBackend) UseProcessGroup() bool {
	return b.useProcessGroup
}
