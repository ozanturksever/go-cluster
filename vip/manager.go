package vip

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"sync"
)

type Config struct {
	Address   string
	Netmask   int
	Interface string
	Logger    *slog.Logger
}

func (c Config) CIDR() string {
	return fmt.Sprintf("%s/%d", c.Address, c.Netmask)
}

func (c Config) Validate() error {
	if c.Address == "" {
		return fmt.Errorf("Address is required")
	}
	if net.ParseIP(c.Address) == nil {
		return fmt.Errorf("invalid IP address: %s", c.Address)
	}
	if c.Netmask < 0 || c.Netmask > 32 {
		return fmt.Errorf("Netmask must be between 0 and 32")
	}
	if c.Interface == "" {
		return fmt.Errorf("Interface is required")
	}
	return nil
}

type Executor interface {
	Execute(ctx context.Context, cmd string, args ...string) (string, error)
}

type DefaultExecutor struct{}

func (e DefaultExecutor) Execute(ctx context.Context, cmd string, args ...string) (string, error) {
	c := exec.CommandContext(ctx, cmd, args...)
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

type Manager struct {
	cfg      Config
	executor Executor
	logger   *slog.Logger
	mu       sync.Mutex
	acquired bool
}

func NewManager(cfg Config, executor Executor) (*Manager, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if executor == nil {
		executor = DefaultExecutor{}
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		cfg:      cfg,
		executor: executor,
		logger:   logger.With("component", "vip", "address", cfg.Address),
	}, nil
}

func (m *Manager) Acquire(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	acquired, err := m.isAcquiredLocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to check VIP status: %w", err)
	}
	if acquired {
		m.acquired = true
		return nil
	}

	cidr := m.cfg.CIDR()
	_, err = m.executor.Execute(ctx, "ip", "addr", "add", cidr, "dev", m.cfg.Interface)
	if err != nil {
		if strings.Contains(err.Error(), "File exists") {
			m.acquired = true
			return nil
		}
		return fmt.Errorf("failed to add VIP %s: %w", cidr, err)
	}

	m.acquired = true
	m.logger.Info("VIP acquired", "interface", m.cfg.Interface)
	m.sendGratuitousARP(ctx)
	return nil
}

func (m *Manager) Release(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	acquired, err := m.isAcquiredLocked(ctx)
	if err != nil {
		return fmt.Errorf("failed to check VIP status: %w", err)
	}
	if !acquired {
		m.acquired = false
		return nil
	}

	cidr := m.cfg.CIDR()
	_, err = m.executor.Execute(ctx, "ip", "addr", "del", cidr, "dev", m.cfg.Interface)
	if err != nil {
		if strings.Contains(err.Error(), "Cannot assign requested address") {
			m.acquired = false
			return nil
		}
		return fmt.Errorf("failed to remove VIP %s: %w", cidr, err)
	}

	m.acquired = false
	m.logger.Info("VIP released", "interface", m.cfg.Interface)
	return nil
}

func (m *Manager) IsAcquired(ctx context.Context) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isAcquiredLocked(ctx)
}

func (m *Manager) isAcquiredLocked(ctx context.Context) (bool, error) {
	output, err := m.executor.Execute(ctx, "ip", "addr", "show", "dev", m.cfg.Interface)
	if err != nil {
		return false, fmt.Errorf("failed to check interface: %w", err)
	}
	return strings.Contains(output, m.cfg.Address), nil
}

func (m *Manager) sendGratuitousARP(ctx context.Context) {
	_, err := m.executor.Execute(ctx, "arping", "-c", "3", "-A", "-I", m.cfg.Interface, m.cfg.Address)
	if err != nil {
		m.logger.Debug("arping failed (may not be installed)", "error", err)
	}
}
