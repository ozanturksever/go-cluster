package vip

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestNewManager_ValidConfig(t *testing.T) {
	// Find a valid interface for testing
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if m.IP().String() != "10.99.99.99" {
		t.Errorf("IP = %s, want 10.99.99.99", m.IP())
	}

	if m.Interface() != iface {
		t.Errorf("Interface = %s, want %s", m.Interface(), iface)
	}

	if m.CIDR() != "10.99.99.99/24" {
		t.Errorf("CIDR = %s, want 10.99.99.99/24", m.CIDR())
	}
}

func TestNewManager_InvalidCIDR(t *testing.T) {
	config := Config{
		CIDR:      "invalid",
		Interface: "lo0",
	}

	_, err := NewManager(config)
	if err == nil {
		t.Error("Expected error for invalid CIDR")
	}
}

func TestNewManager_EmptyInterface(t *testing.T) {
	config := Config{
		CIDR:      "10.0.0.1/24",
		Interface: "",
	}

	_, err := NewManager(config)
	if err == nil {
		t.Error("Expected error for empty interface")
	}
}

func TestNewManager_InvalidInterface(t *testing.T) {
	config := Config{
		CIDR:      "10.0.0.1/24",
		Interface: "nonexistent_iface_xyz",
	}

	_, err := NewManager(config)
	if err == nil {
		t.Error("Expected error for invalid interface")
	}
}

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.ARPCount != 3 {
		t.Errorf("ARPCount = %d, want 3", cfg.ARPCount)
	}

	if cfg.ARPInterval != 500*time.Millisecond {
		t.Errorf("ARPInterval = %v, want 500ms", cfg.ARPInterval)
	}

	if cfg.HealthInterval != 5*time.Second {
		t.Errorf("HealthInterval = %v, want 5s", cfg.HealthInterval)
	}

	if cfg.HealthTimeout != 2*time.Second {
		t.Errorf("HealthTimeout = %v, want 2s", cfg.HealthTimeout)
	}
}

func TestManager_IsAcquired_Initial(t *testing.T) {
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	if m.IsAcquired() {
		t.Error("IsAcquired should be false initially")
	}
}

func TestManager_Health(t *testing.T) {
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	health := m.Health()

	if health.Acquired {
		t.Error("Health.Acquired should be false")
	}

	if health.IP != "10.99.99.99" {
		t.Errorf("Health.IP = %s, want 10.99.99.99", health.IP)
	}

	if health.Interface != iface {
		t.Errorf("Health.Interface = %s, want %s", health.Interface, iface)
	}

	if health.CheckedAt.IsZero() {
		t.Error("Health.CheckedAt should not be zero")
	}
}

func TestManager_Callbacks(t *testing.T) {
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	acquiredCalled := false
	releasedCalled := false
	healthFailureCalled := false

	m.OnAcquired(func() {
		acquiredCalled = true
	})

	m.OnReleased(func() {
		releasedCalled = true
	})

	m.OnHealthFailure(func(err error) {
		healthFailureCalled = true
	})

	// Verify callbacks are set (can't easily test without actually acquiring VIP)
	if acquiredCalled || releasedCalled || healthFailureCalled {
		t.Error("Callbacks should not be called before acquire/release")
	}
}

func TestManager_StartStop(t *testing.T) {
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()
	if err := m.Start(ctx); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Stop should not panic
	m.Stop()
}

func TestManager_Verify_NotAcquired(t *testing.T) {
	iface := findTestInterface(t)
	if iface == "" {
		t.Skip("No suitable network interface found for testing")
	}

	config := Config{
		CIDR:      "10.99.99.99/24",
		Interface: iface,
	}

	m, err := NewManager(config)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	ctx := context.Background()
	err = m.Verify(ctx)
	if err == nil {
		t.Error("Verify should return error when VIP not acquired")
	}
}

// findTestInterface finds a suitable network interface for testing.
func findTestInterface(t *testing.T) string {
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Logf("Failed to list interfaces: %v", err)
		return ""
	}

	// Prefer loopback for testing
	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 && iface.Flags&net.FlagUp != 0 {
			return iface.Name
		}
	}

	// Fall back to any UP interface
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp != 0 {
			return iface.Name
		}
	}

	return ""
}
