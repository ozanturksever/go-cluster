package cluster

import (
	"context"
	"errors"
	"testing"
)

func TestNoOpHooks(t *testing.T) {
	hooks := NoOpHooks{}
	ctx := context.Background()

	// All methods should return nil
	if err := hooks.OnBecomeLeader(ctx); err != nil {
		t.Errorf("OnBecomeLeader() error = %v", err)
	}
	if err := hooks.OnLoseLeadership(ctx); err != nil {
		t.Errorf("OnLoseLeadership() error = %v", err)
	}
	if err := hooks.OnLeaderChange(ctx, "node-1"); err != nil {
		t.Errorf("OnLeaderChange() error = %v", err)
	}
	if err := hooks.OnNATSReconnect(ctx); err != nil {
		t.Errorf("OnNATSReconnect() error = %v", err)
	}
	if err := hooks.OnNATSDisconnect(ctx, errors.New("test error")); err != nil {
		t.Errorf("OnNATSDisconnect() error = %v", err)
	}
}

func TestHooksInterface(t *testing.T) {
	// Verify that NoOpHooks implements Hooks
	var _ Hooks = NoOpHooks{}
}

func TestNoOpManagerHooks(t *testing.T) {
	hooks := NoOpManagerHooks{}
	ctx := context.Background()

	// All methods should return nil
	if err := hooks.OnBecomeLeader(ctx); err != nil {
		t.Errorf("OnBecomeLeader() error = %v", err)
	}
	if err := hooks.OnLoseLeadership(ctx); err != nil {
		t.Errorf("OnLoseLeadership() error = %v", err)
	}
	if err := hooks.OnLeaderChange(ctx, "node-1"); err != nil {
		t.Errorf("OnLeaderChange() error = %v", err)
	}
	if err := hooks.OnNATSReconnect(ctx); err != nil {
		t.Errorf("OnNATSReconnect() error = %v", err)
	}
	if err := hooks.OnNATSDisconnect(ctx, errors.New("test error")); err != nil {
		t.Errorf("OnNATSDisconnect() error = %v", err)
	}
	if err := hooks.OnDaemonStart(ctx); err != nil {
		t.Errorf("OnDaemonStart() error = %v", err)
	}
	if err := hooks.OnDaemonStop(ctx); err != nil {
		t.Errorf("OnDaemonStop() error = %v", err)
	}
}

func TestManagerHooksInterface(t *testing.T) {
	// Verify that NoOpManagerHooks implements ManagerHooks
	var _ ManagerHooks = NoOpManagerHooks{}

	// Verify that ManagerHooks embeds Hooks
	var mh ManagerHooks = NoOpManagerHooks{}
	var h Hooks = mh
	_ = h
}

func TestNoOpManagerHooksEmbedsNoOpHooks(t *testing.T) {
	hooks := NoOpManagerHooks{}

	// The embedded NoOpHooks should work
	ctx := context.Background()
	if err := hooks.NoOpHooks.OnBecomeLeader(ctx); err != nil {
		t.Errorf("NoOpHooks.OnBecomeLeader() error = %v", err)
	}
	if err := hooks.NoOpHooks.OnNATSReconnect(ctx); err != nil {
		t.Errorf("NoOpHooks.OnNATSReconnect() error = %v", err)
	}
}
