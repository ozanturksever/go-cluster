// Package backend provides backend controllers for managing external services.
package backend

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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
	Status    string `json:"status"`
	Message   string `json:"message,omitempty"`
	Uptime    time.Duration `json:"uptime,omitempty"`
}

// SystemdBackend manages a systemd service.
type SystemdBackend struct {
	serviceName    string
	healthEndpoint string
	healthInterval time.Duration
	healthTimeout  time.Duration
	startTimeout   time.Duration
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

// WithStartTimeout sets the startup timeout.
func WithStartTimeout(d time.Duration) SystemdOption {
	return func(b *SystemdBackend) {
		b.startTimeout = d
	}
}

// Start starts the systemd service.
func (b *SystemdBackend) Start(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "start", b.serviceName)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start service %s: %w", b.serviceName, err)
	}

	// Wait for service to be healthy
	if b.healthEndpoint != "" {
		deadline := time.Now().Add(b.startTimeout)
		for time.Now().Before(deadline) {
			health, err := b.Health(ctx)
			if err == nil && health.Status == "running" {
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

	return nil
}

// Stop stops the systemd service.
func (b *SystemdBackend) Stop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "systemctl", "stop", b.serviceName)
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

	status := string(output)
	if status == "active\n" || status == "active" {
		// If we have a health endpoint, check it too
		if b.healthEndpoint != "" {
			client := &http.Client{Timeout: b.healthTimeout}
			resp, err := client.Get(b.healthEndpoint)
			if err != nil {
				return Health{Status: "unhealthy", Message: err.Error()}, nil
			}
			defer resp.Body.Close()

			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return Health{Status: "running"}, nil
			}
			return Health{Status: "unhealthy", Message: fmt.Sprintf("health check returned %d", resp.StatusCode)}, nil
		}
		return Health{Status: "running"}, nil
	}

	return Health{Status: "stopped"}, nil
}

// DockerConfig configures a Docker backend.
type DockerConfig struct {
	Container string
	Image     string
	Ports     []string
	Env       map[string]string
	Volumes   []string
	Network   string
}

// DockerBackend manages a Docker container.
type DockerBackend struct {
	config DockerConfig
}

// Docker creates a new Docker backend.
func Docker(config DockerConfig) *DockerBackend {
	return &DockerBackend{config: config}
}

// Start starts the Docker container.
func (b *DockerBackend) Start(ctx context.Context) error {
	args := []string{"run", "-d", "--name", b.config.Container}

	for _, port := range b.config.Ports {
		args = append(args, "-p", port)
	}

	for k, v := range b.config.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}

	for _, vol := range b.config.Volumes {
		args = append(args, "-v", vol)
	}

	if b.config.Network != "" {
		args = append(args, "--network", b.config.Network)
	}

	args = append(args, b.config.Image)

	cmd := exec.CommandContext(ctx, "docker", args...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to start container %s: %w", b.config.Container, err)
	}

	return nil
}

// Stop stops the Docker container.
func (b *DockerBackend) Stop(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, "docker", "stop", b.config.Container)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container %s: %w", b.config.Container, err)
	}

	cmd = exec.CommandContext(ctx, "docker", "rm", b.config.Container)
	cmd.Run() // Ignore error on remove

	return nil
}

// Health checks the health of the Docker container.
func (b *DockerBackend) Health(ctx context.Context) (Health, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", b.config.Container)
	output, err := cmd.Output()
	if err != nil {
		return Health{Status: "stopped"}, nil
	}

	status := string(output)
	if status == "running\n" || status == "running" {
		return Health{Status: "running"}, nil
	}

	return Health{Status: status}, nil
}

// ProcessConfig configures a Process backend.
type ProcessConfig struct {
	Command []string
	Dir     string
	Env     []string
}

// ProcessBackend manages a child process.
type ProcessBackend struct {
	config ProcessConfig
	cmd    *exec.Cmd
}

// Process creates a new Process backend.
func Process(config ProcessConfig) *ProcessBackend {
	return &ProcessBackend{config: config}
}

// Start starts the process.
func (b *ProcessBackend) Start(ctx context.Context) error {
	if len(b.config.Command) == 0 {
		return fmt.Errorf("no command specified")
	}

	b.cmd = exec.CommandContext(ctx, b.config.Command[0], b.config.Command[1:]...)
	b.cmd.Dir = b.config.Dir
	b.cmd.Env = append(os.Environ(), b.config.Env...)
	b.cmd.Stdout = os.Stdout
	b.cmd.Stderr = os.Stderr

	if err := b.cmd.Start(); err != nil {
		return fmt.Errorf("failed to start process: %w", err)
	}

	return nil
}

// Stop stops the process.
func (b *ProcessBackend) Stop(ctx context.Context) error {
	if b.cmd == nil || b.cmd.Process == nil {
		return nil
	}

	// Send SIGTERM first
	if err := b.cmd.Process.Signal(os.Interrupt); err != nil {
		// If SIGTERM fails, force kill
		b.cmd.Process.Kill()
	}

	// Wait for process to exit
	done := make(chan error)
	go func() {
		done <- b.cmd.Wait()
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		b.cmd.Process.Kill()
		return ctx.Err()
	case <-time.After(10 * time.Second):
		b.cmd.Process.Kill()
		return nil
	}
}

// Health checks if the process is running.
func (b *ProcessBackend) Health(ctx context.Context) (Health, error) {
	if b.cmd == nil || b.cmd.Process == nil {
		return Health{Status: "stopped"}, nil
	}

	// Check if process is still running
	if b.cmd.ProcessState != nil && b.cmd.ProcessState.Exited() {
		return Health{Status: "stopped"}, nil
	}

	return Health{Status: "running"}, nil
}
