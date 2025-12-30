// Package vip provides Virtual IP (VIP) management for Linux systems.
//
// The VIP manager allows cluster leaders to acquire a floating IP address
// and release it when stepping down, enabling seamless failover.
//
// # Usage
//
//	mgr, err := vip.NewManager(vip.Config{
//	    Address:   "192.168.1.100",
//	    Netmask:   24,
//	    Interface: "eth0",
//	}, nil)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Acquire VIP when becoming leader
//	if err := mgr.Acquire(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Release VIP when losing leadership
//	if err := mgr.Release(ctx); err != nil {
//	    log.Fatal(err)
//	}
//
//	// Check if VIP is currently held
//	acquired, err := mgr.IsAcquired(ctx)
//
// # Requirements
//
// This package requires:
//   - Linux operating system
//   - The `ip` command available in PATH
//   - The `arping` command (optional, for gratuitous ARP)
//   - Appropriate permissions (typically root or CAP_NET_ADMIN capability)
//
// # Custom Executor
//
// For testing or custom command execution, implement the Executor interface:
//
//	type Executor interface {
//	    Execute(ctx context.Context, cmd string, args ...string) (string, error)
//	}
package vip
