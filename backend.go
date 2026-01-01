package cluster

import (
	"context"

	"github.com/ozanturksever/go-cluster/backend"
)

// Backend defines the interface for managing external services.
type Backend interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Health(ctx context.Context) (backend.Health, error)
}

// Systemd creates a new systemd backend controller.
func Systemd(serviceName string, opts ...backend.SystemdOption) *backend.SystemdBackend {
	return backend.Systemd(serviceName, opts...)
}

// Docker creates a new Docker backend controller.
func Docker(config backend.DockerConfig) *backend.DockerBackend {
	return backend.Docker(config)
}

// Process creates a new process backend controller.
func Process(config backend.ProcessConfig) *backend.ProcessBackend {
	return backend.Process(config)
}

// Re-export backend types for convenience
type (
	// BackendHealth represents the health status of a backend.
	BackendHealth = backend.Health

	// DockerConfig configures a Docker backend.
	DockerConfig = backend.DockerConfig

	// DockerHealthCheck configures container health check.
	DockerHealthCheck = backend.DockerHealthCheck

	// DockerResources configures container resource limits.
	DockerResources = backend.DockerResources

	// ProcessConfig configures a Process backend.
	ProcessConfig = backend.ProcessConfig

	// RestartPolicy defines how a backend should be restarted on failure.
	RestartPolicy = backend.RestartPolicy

	// OutputHandler is a callback for process output.
	OutputHandler = backend.OutputHandler
)

// Re-export systemd backend options for convenience
var (
	// SystemdHealthEndpoint sets the health check endpoint for systemd backend.
	SystemdHealthEndpoint = backend.WithHealthEndpoint

	// SystemdHealthInterval sets the health check interval.
	SystemdHealthInterval = backend.WithHealthInterval

	// SystemdHealthTimeout sets the health check timeout.
	SystemdHealthTimeout = backend.WithHealthTimeout

	// SystemdStartTimeout sets the startup timeout.
	SystemdStartTimeout = backend.WithStartTimeout

	// SystemdStopTimeout sets the stop timeout.
	SystemdStopTimeout = backend.WithStopTimeout

	// SystemdNotify enables systemd notify integration.
	SystemdNotify = backend.WithNotify

	// SystemdDependencies sets service dependencies.
	SystemdDependencies = backend.WithDependencies

	// SystemdJournalLines sets the number of journal lines to retrieve.
	SystemdJournalLines = backend.WithJournalLines

	// SystemdRestart configures restart policy for systemd backend.
	SystemdRestart = backend.WithSystemdRestart
)

// DefaultRestartPolicy returns a sensible default restart policy.
func DefaultRestartPolicy() RestartPolicy {
	return backend.DefaultRestartPolicy()
}
