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
