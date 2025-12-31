// Package cluster provides a generic NATS-based cluster coordination library
// with leader election, epoch-based fencing, and state management.
package cluster

import "errors"

// Cluster coordination errors.
var (
	// ErrLeaseExpired indicates the lease has expired and needs renewal.
	ErrLeaseExpired = errors.New("lease expired")

	// ErrEpochMismatch indicates another node took over with a higher epoch.
	ErrEpochMismatch = errors.New("epoch mismatch - another node took over")

	// ErrNotLeader indicates the operation requires leadership but this node is not the leader.
	ErrNotLeader = errors.New("not the leader")

	// ErrAlreadyLeader indicates an attempt to acquire leadership when already the leader.
	ErrAlreadyLeader = errors.New("already leader")

	// ErrCASFailed indicates a compare-and-swap operation failed.
	ErrCASFailed = errors.New("compare-and-swap failed")

	// ErrNotStarted indicates the node has not been started yet.
	ErrNotStarted = errors.New("node not started")

	// ErrAlreadyStarted indicates the node is already running.
	ErrAlreadyStarted = errors.New("node already started")

	// ErrInCooldown indicates the node recently stepped down and is in cooldown period.
	ErrInCooldown = errors.New("node in cooldown period")

	// ErrManagerNotRunning indicates the manager is not currently running.
	ErrManagerNotRunning = errors.New("manager not running")

	// ErrManagerAlreadyRunning indicates the manager is already running.
	ErrManagerAlreadyRunning = errors.New("manager already running")

	// ErrPromotionTimeout indicates the promotion to leader timed out.
	ErrPromotionTimeout = errors.New("promotion to leader timed out")

	// ErrAnotherLeaderExists indicates another node is currently the leader.
	ErrAnotherLeaderExists = errors.New("another node is currently the leader")

	// ErrNATSDisconnected indicates the NATS connection is not available.
	ErrNATSDisconnected = errors.New("NATS connection disconnected")

	// ErrNATSClosed indicates the NATS connection was permanently closed.
	ErrNATSClosed = errors.New("NATS connection closed")

	// ErrWatcherClosed indicates the KV watcher was closed unexpectedly.
	ErrWatcherClosed = errors.New("KV watcher closed")

	// ErrServiceNotRunning indicates the micro service is not running.
	ErrServiceNotRunning = errors.New("micro service not running")
)
