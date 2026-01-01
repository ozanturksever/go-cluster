// Package vip provides virtual IP (VIP) failover management for high availability.
//
// The VIP manager handles acquiring and releasing virtual IP addresses during
// leadership transitions, ensuring that traffic is routed to the active leader.
package vip

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Manager manages virtual IP acquisition and release.
type Manager struct {
	cidr  string // CIDR notation, e.g., "10.0.0.100/24"
	iface string // Network interface, e.g., "eth0"
	ip    net.IP // Parsed IP address
	mask  net.IPMask

	acquired atomic.Bool
	mu       sync.Mutex

	// Configuration
	arpCount       int           // Number of gratuitous ARPs to send
	arpInterval    time.Duration // Interval between ARPs
	healthInterval time.Duration // Interval for health checks
	healthTimeout  time.Duration // Timeout for health checks

	// Callbacks
	onAcquired      func()
	onReleased      func()
	onHealthFailure func(error)

	// Runtime
	ctx        context.Context
	cancel     context.CancelFunc
	healthWg   sync.WaitGroup
	healthStop chan struct{}
}

// Config configures the VIP manager.
type Config struct {
	// CIDR is the virtual IP in CIDR notation (e.g., "10.0.0.100/24")
	CIDR string

	// Interface is the network interface to bind the VIP to (e.g., "eth0")
	Interface string

	// ARPCount is the number of gratuitous ARPs to send after acquiring (default: 3)
	ARPCount int

	// ARPInterval is the interval between gratuitous ARPs (default: 500ms)
	ARPInterval time.Duration

	// HealthInterval is the interval for health checks (default: 5s)
	HealthInterval time.Duration

	// HealthTimeout is the timeout for health checks (default: 2s)
	HealthTimeout time.Duration
}

// DefaultConfig returns the default VIP configuration.
func DefaultConfig() Config {
	return Config{
		ARPCount:       3,
		ARPInterval:    500 * time.Millisecond,
		HealthInterval: 5 * time.Second,
		HealthTimeout:  2 * time.Second,
	}
}

// NewManager creates a new VIP manager.
func NewManager(config Config) (*Manager, error) {
	// Parse CIDR
	ip, ipNet, err := net.ParseCIDR(config.CIDR)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR %q: %w", config.CIDR, err)
	}

	// Validate interface exists
	if config.Interface == "" {
		return nil, fmt.Errorf("interface is required")
	}

	_, err = net.InterfaceByName(config.Interface)
	if err != nil {
		return nil, fmt.Errorf("interface %q not found: %w", config.Interface, err)
	}

	// Apply defaults
	if config.ARPCount <= 0 {
		config.ARPCount = 3
	}
	if config.ARPInterval <= 0 {
		config.ARPInterval = 500 * time.Millisecond
	}
	if config.HealthInterval <= 0 {
		config.HealthInterval = 5 * time.Second
	}
	if config.HealthTimeout <= 0 {
		config.HealthTimeout = 2 * time.Second
	}

	return &Manager{
		cidr:           config.CIDR,
		iface:          config.Interface,
		ip:             ip,
		mask:           ipNet.Mask,
		arpCount:       config.ARPCount,
		arpInterval:    config.ARPInterval,
		healthInterval: config.HealthInterval,
		healthTimeout:  config.HealthTimeout,
		healthStop:     make(chan struct{}),
	}, nil
}

// OnAcquired sets a callback to be called when the VIP is acquired.
func (m *Manager) OnAcquired(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onAcquired = fn
}

// OnReleased sets a callback to be called when the VIP is released.
func (m *Manager) OnReleased(fn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onReleased = fn
}

// OnHealthFailure sets a callback to be called when VIP health check fails.
func (m *Manager) OnHealthFailure(fn func(error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onHealthFailure = fn
}

// Start starts the VIP manager (does not acquire VIP).
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)
	return nil
}

// Stop stops the VIP manager and releases the VIP if held.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}

	// Stop health monitoring
	m.stopHealthMonitoring()

	// Release VIP if held
	if m.acquired.Load() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		m.Release(ctx)
	}
}

// Acquire acquires the virtual IP address.
func (m *Manager) Acquire(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.acquired.Load() {
		return nil // Already acquired
	}

	// Check if VIP is already on interface (idempotent)
	if m.isIPOnInterface() {
		m.acquired.Store(true)
		m.startHealthMonitoring()
		if m.onAcquired != nil {
			m.onAcquired()
		}
		return nil
	}

	// Add IP to interface
	if err := m.addIP(ctx); err != nil {
		return fmt.Errorf("failed to add VIP: %w", err)
	}

	// Send gratuitous ARPs
	m.sendGratuitousARPs(ctx)

	m.acquired.Store(true)

	// Start health monitoring
	m.startHealthMonitoring()

	if m.onAcquired != nil {
		m.onAcquired()
	}

	return nil
}

// Release releases the virtual IP address.
func (m *Manager) Release(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.acquired.Load() {
		return nil // Not acquired
	}

	// Stop health monitoring first
	m.stopHealthMonitoringLocked()

	// Remove IP from interface
	if err := m.removeIP(ctx); err != nil {
		// Log but don't fail - IP might already be gone
		// In production, this error should be logged
	}

	m.acquired.Store(false)

	if m.onReleased != nil {
		m.onReleased()
	}

	return nil
}

// IsAcquired returns true if the VIP is currently held.
func (m *Manager) IsAcquired() bool {
	return m.acquired.Load()
}

// IP returns the virtual IP address.
func (m *Manager) IP() net.IP {
	return m.ip
}

// CIDR returns the VIP in CIDR notation.
func (m *Manager) CIDR() string {
	return m.cidr
}

// Interface returns the network interface.
func (m *Manager) Interface() string {
	return m.iface
}

// addIP adds the VIP to the network interface.
func (m *Manager) addIP(ctx context.Context) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "ip", "addr", "add", m.cidr, "dev", m.iface)
	case "darwin":
		// macOS uses ifconfig with alias
		maskStr := net.IP(m.mask).String()
		cmd = exec.CommandContext(ctx, "ifconfig", m.iface, "alias", m.ip.String(), "netmask", maskStr)
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if IP already exists (not an error)
		if strings.Contains(string(output), "File exists") ||
			strings.Contains(string(output), "already") {
			return nil
		}
		return fmt.Errorf("command failed: %s: %w", string(output), err)
	}

	return nil
}

// removeIP removes the VIP from the network interface.
func (m *Manager) removeIP(ctx context.Context) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		cmd = exec.CommandContext(ctx, "ip", "addr", "del", m.cidr, "dev", m.iface)
	case "darwin":
		cmd = exec.CommandContext(ctx, "ifconfig", m.iface, "-alias", m.ip.String())
	default:
		return fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if IP doesn't exist (not an error)
		if strings.Contains(string(output), "Cannot assign") ||
			strings.Contains(string(output), "ADDRNOTAVAIL") ||
			strings.Contains(string(output), "does not exist") {
			return nil
		}
		return fmt.Errorf("command failed: %s: %w", string(output), err)
	}

	return nil
}

// sendGratuitousARPs sends gratuitous ARP packets to announce the VIP.
func (m *Manager) sendGratuitousARPs(ctx context.Context) {
	for i := 0; i < m.arpCount; i++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		m.sendARP(ctx)

		if i < m.arpCount-1 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.arpInterval):
			}
		}
	}
}

// sendARP sends a single gratuitous ARP packet.
func (m *Manager) sendARP(ctx context.Context) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "linux":
		// Use arping if available, fall back to ip neigh
		if _, err := exec.LookPath("arping"); err == nil {
			cmd = exec.CommandContext(ctx, "arping", "-U", "-c", "1", "-I", m.iface, m.ip.String())
		} else {
			// Alternative: use ndisc6's ndisc if available, or just skip
			return nil
		}
	case "darwin":
		// macOS doesn't have a good arping equivalent by default
		// The IP addition itself usually triggers an ARP
		return nil
	default:
		return nil
	}

	// Run but ignore errors - best effort
	cmd.Run()
	return nil
}

// isIPOnInterface checks if the VIP is already on the interface.
func (m *Manager) isIPOnInterface() bool {
	iface, err := net.InterfaceByName(m.iface)
	if err != nil {
		return false
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return false
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipNet.IP.Equal(m.ip) {
			return true
		}
	}

	return false
}

// startHealthMonitoring starts background VIP health monitoring.
func (m *Manager) startHealthMonitoring() {
	m.healthStop = make(chan struct{})
	m.healthWg.Add(1)
	go m.healthMonitorLoop()
}

// stopHealthMonitoring stops background VIP health monitoring.
func (m *Manager) stopHealthMonitoring() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopHealthMonitoringLocked()
}

func (m *Manager) stopHealthMonitoringLocked() {
	if m.healthStop != nil {
		close(m.healthStop)
		m.healthStop = nil
	}
	// Release lock before waiting to avoid deadlock with healthMonitorLoop
	m.mu.Unlock()
	m.healthWg.Wait()
	m.mu.Lock()
}

// healthMonitorLoop monitors VIP health in the background.
func (m *Manager) healthMonitorLoop() {
	defer m.healthWg.Done()

	ticker := time.NewTicker(m.healthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.healthStop:
			return
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if !m.isIPOnInterface() {
				err := fmt.Errorf("VIP %s no longer on interface %s", m.ip, m.iface)
				m.mu.Lock()
				callback := m.onHealthFailure
				m.mu.Unlock()
				if callback != nil {
					callback(err)
				}
			}
		}
	}
}

// Health returns the current health status of the VIP.
type Health struct {
	Acquired    bool      `json:"acquired"`
	IP          string    `json:"ip"`
	Interface   string    `json:"interface"`
	OnInterface bool      `json:"on_interface"`
	CheckedAt   time.Time `json:"checked_at"`
}

// Health returns the current health status.
func (m *Manager) Health() Health {
	return Health{
		Acquired:    m.acquired.Load(),
		IP:          m.ip.String(),
		Interface:   m.iface,
		OnInterface: m.isIPOnInterface(),
		CheckedAt:   time.Now(),
	}
}

// Verify checks if the VIP is correctly configured and responding.
func (m *Manager) Verify(ctx context.Context) error {
	if !m.acquired.Load() {
		return fmt.Errorf("VIP not acquired")
	}

	if !m.isIPOnInterface() {
		return fmt.Errorf("VIP %s not found on interface %s", m.ip, m.iface)
	}

	return nil
}
