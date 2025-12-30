package cluster

import "context"

// Hooks defines the interface that users must implement to react to cluster
// state changes. All methods are called synchronously - implementations should
// spawn goroutines if async behavior is needed.
type Hooks interface {
	// OnBecomeLeader is called when this node becomes the cluster leader.
	OnBecomeLeader(ctx context.Context) error

	// OnLoseLeadership is called when this node loses leadership.
	OnLoseLeadership(ctx context.Context) error

	// OnLeaderChange is called when the cluster leader changes.
	OnLeaderChange(ctx context.Context, nodeID string) error
}

// NoOpHooks is a default implementation of Hooks that does nothing.
type NoOpHooks struct{}

func (NoOpHooks) OnBecomeLeader(ctx context.Context) error           { return nil }
func (NoOpHooks) OnLoseLeadership(ctx context.Context) error         { return nil }
func (NoOpHooks) OnLeaderChange(ctx context.Context, _ string) error { return nil }

var _ Hooks = NoOpHooks{}

// ManagerHooks extends Hooks with additional callbacks for the Manager lifecycle.
// Consumers can implement this interface to receive notifications about daemon
// start/stop events in addition to the standard cluster state changes.
type ManagerHooks interface {
	Hooks

	// OnDaemonStart is called when the daemon starts running.
	OnDaemonStart(ctx context.Context) error

	// OnDaemonStop is called when the daemon stops running.
	OnDaemonStop(ctx context.Context) error
}

// NoOpManagerHooks is a default implementation of ManagerHooks that does nothing.
type NoOpManagerHooks struct {
	NoOpHooks
}

func (NoOpManagerHooks) OnDaemonStart(ctx context.Context) error { return nil }
func (NoOpManagerHooks) OnDaemonStop(ctx context.Context) error  { return nil }

var _ ManagerHooks = NoOpManagerHooks{}
