package cluster

import (
	"context"
	"testing"
)

func TestNoOpHooks(t *testing.T) {
	hooks := NoOpHooks{}
	ctx := context.Background()

	t.Run("OnBecomeLeader returns nil", func(t *testing.T) {
		if err := hooks.OnBecomeLeader(ctx); err != nil {
			t.Errorf("OnBecomeLeader() error = %v, want nil", err)
		}
	})

	t.Run("OnLoseLeadership returns nil", func(t *testing.T) {
		if err := hooks.OnLoseLeadership(ctx); err != nil {
			t.Errorf("OnLoseLeadership() error = %v, want nil", err)
		}
	})

	t.Run("OnLeaderChange returns nil", func(t *testing.T) {
		if err := hooks.OnLeaderChange(ctx, "node-1"); err != nil {
			t.Errorf("OnLeaderChange() error = %v, want nil", err)
		}
	})

	t.Run("OnLeaderChange with empty nodeID returns nil", func(t *testing.T) {
		if err := hooks.OnLeaderChange(ctx, ""); err != nil {
			t.Errorf("OnLeaderChange() error = %v, want nil", err)
		}
	})
}

func TestNoOpHooksImplementsInterface(t *testing.T) {
	// This test verifies at compile time that NoOpHooks implements Hooks
	var _ Hooks = NoOpHooks{}
	var _ Hooks = &NoOpHooks{}
}
