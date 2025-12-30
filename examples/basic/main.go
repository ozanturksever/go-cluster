package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	cluster "github.com/ozanturksever/go-cluster"
)

// MyApp implements cluster.ManagerHooks to react to cluster state changes.
type MyApp struct {
	logger *slog.Logger
}

func (a *MyApp) OnBecomeLeader(ctx context.Context) error {
	a.logger.Info("I am now the LEADER!")
	// Start primary workload: accept writes, start replication, etc.
	return nil
}

func (a *MyApp) OnLoseLeadership(ctx context.Context) error {
	a.logger.Info("I lost leadership, switching to standby")
	// Stop primary workload: stop writes, become passive, etc.
	return nil
}

func (a *MyApp) OnLeaderChange(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		a.logger.Info("No leader currently elected")
	} else {
		a.logger.Info("Leader changed", "leader", nodeID)
	}
	return nil
}

func (a *MyApp) OnDaemonStart(ctx context.Context) error {
	a.logger.Info("Daemon started")
	return nil
}

func (a *MyApp) OnDaemonStop(ctx context.Context) error {
	a.logger.Info("Daemon stopped")
	return nil
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	}))
	slog.SetDefault(logger)

	nodeID := os.Getenv("NODE_ID")
	if nodeID == "" {
		nodeID = "node1"
	}

	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "demo-cluster"
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = "nats://localhost:4222"
	}

	app := &MyApp{logger: logger}

	// Create configuration
	cfg := cluster.NewDefaultFileConfig(clusterID, nodeID, []string{natsURL})

	// Create manager with hooks
	mgr, err := cluster.NewManager(*cfg, app, cluster.WithLogger(logger))
	if err != nil {
		logger.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigCh
		logger.Info("Shutting down...")
		cancel()
	}()

	logger.Info("Starting daemon", "nodeID", nodeID, "clusterID", clusterID, "natsURL", natsURL)

	// Run the daemon until context is cancelled
	if err := mgr.RunDaemon(ctx); err != nil {
		logger.Error("daemon error", "error", err)
		os.Exit(1)
	}

	logger.Info("Goodbye!")
}
