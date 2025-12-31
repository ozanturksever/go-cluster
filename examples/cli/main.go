// Package main provides a CLI example for go-cluster.
//
// Usage:
//
//	go-cluster-cli init --config cluster.json --cluster-id my-cluster --node-id node1
//	go-cluster-cli daemon --config cluster.json
//	go-cluster-cli status --config cluster.json
//	go-cluster-cli watch --config cluster.json
//	go-cluster-cli promote --config cluster.json
//	go-cluster-cli demote --config cluster.json
//	go-cluster-cli join --config cluster.json
//	go-cluster-cli leave --config cluster.json
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"time"
	"os/signal"
	"syscall"

	cluster "github.com/ozanturksever/go-cluster"
)

var (
	version = "dev"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "init":
		runInit(os.Args[2:])
	case "daemon":
		runDaemon(os.Args[2:])
	case "status":
		runStatus(os.Args[2:])
	case "watch":
		runWatch(os.Args[2:])
	case "promote":
		runPromote(os.Args[2:])
	case "demote":
		runDemote(os.Args[2:])
	case "join":
		runJoin(os.Args[2:])
	case "leave":
		runLeave(os.Args[2:])
	case "version":
		fmt.Printf("go-cluster-cli %s\n", version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`go-cluster-cli - A CLI for managing go-cluster nodes

Usage:
  go-cluster-cli <command> [options]

Commands:
  init      Initialize a new cluster configuration file
  daemon    Run the cluster daemon (main mode)
  status    Show the current node status
  watch     Continuously watch cluster status
  promote   Attempt to become the cluster leader
  demote    Step down from leadership
  join      Join the cluster and detect the current leader
  leave     Gracefully leave the cluster
  version   Print version information
  help      Show this help message

Run 'go-cluster-cli <command> -h' for more information on a command.`)
}

// runInit initializes a new cluster configuration file.
func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")
	clusterID := fs.String("cluster-id", "", "Cluster identifier (required)")
	nodeID := fs.String("node-id", "", "Node identifier (required)")
	natsURL := fs.String("nats-url", "nats://localhost:4222", "NATS server URL")
	vipAddr := fs.String("vip-address", "", "Virtual IP address (optional)")
	vipNetmask := fs.Int("vip-netmask", 24, "Virtual IP netmask")
	vipIface := fs.String("vip-interface", "", "Network interface for VIP")

	fs.Usage = func() {
		fmt.Println(`Initialize a new cluster configuration file.

Usage:
  go-cluster-cli init [options]

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Example:
  go-cluster-cli init --config cluster.json --cluster-id my-cluster --node-id node1`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	if *clusterID == "" || *nodeID == "" {
		fmt.Fprintln(os.Stderr, "Error: --cluster-id and --node-id are required")
		fs.Usage()
		os.Exit(1)
	}

	cfg := cluster.NewDefaultFileConfig(*clusterID, *nodeID, []string{*natsURL})

	if *vipAddr != "" {
		cfg.VIP.Address = *vipAddr
		cfg.VIP.Netmask = *vipNetmask
		cfg.VIP.Interface = *vipIface
	}

	if err := cluster.WriteConfigToFile(cfg, *configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Configuration written to %s\n", *configPath)
}

// runDaemon runs the cluster daemon.
func runDaemon(args []string) {
	fs := flag.NewFlagSet("daemon", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")
	logLevel := fs.String("log-level", "info", "Log level (debug, info, warn, error)")

	fs.Usage = func() {
		fmt.Println(`Run the cluster daemon.

This is the main operating mode. The daemon will participate in leader
election, manage the VIP (if configured), and call your application hooks
for cluster state changes.

Usage:
  go-cluster-cli daemon [options]

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Example:
  go-cluster-cli daemon --config cluster.json --log-level debug`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger(*logLevel)

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	app := &CLIApp{logger: logger}

	mgr, err := cluster.NewManager(*cfg, app, cluster.WithLogger(logger))
	if err != nil {
		logger.Error("failed to create manager", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		logger.Info("Received signal, shutting down...", "signal", sig)
		cancel()
	}()

	logger.Info("Starting daemon",
		"nodeID", cfg.NodeID,
		"clusterID", cfg.ClusterID,
		"natsServers", cfg.NATS.Servers)

	if err := mgr.RunDaemon(ctx); err != nil {
		logger.Error("daemon error", "error", err)
		os.Exit(1)
	}

	logger.Info("Daemon stopped")
}

// runStatus shows the current node status.
func runStatus(args []string) {
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println(`Show the current node status.

Usage:
  go-cluster-cli status [options]

Options:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("warn")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	status, err := mgr.Status(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if *jsonOutput {
		data, _ := json.MarshalIndent(status, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("Cluster:   %s\n", status.ClusterID)
		fmt.Printf("Node:      %s\n", status.NodeID)
		fmt.Printf("Role:      %s\n", status.Role)
		fmt.Printf("Connected: %v\n", status.Connected)
		if status.Leader != "" {
			fmt.Printf("Leader:    %s\n", status.Leader)
		} else {
			fmt.Printf("Leader:    (none)\n")
		}
		fmt.Printf("Epoch:     %d\n", status.Epoch)
		if status.Uptime > 0 {
			fmt.Printf("Uptime:    %s\n", status.Uptime.Round(1e9))
		}
		fmt.Printf("VIP Held:  %v\n", status.VIPHeld)
	}
}

// runWatch continuously displays cluster status updates.
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")
	interval := fs.Duration("interval", 2*time.Second, "Refresh interval")
	jsonOutput := fs.Bool("json", false, "Output in JSON format")

	fs.Usage = func() {
		fmt.Println(`Continuously watch cluster status.

Displays cluster status and refreshes at the specified interval.
Press Ctrl+C to stop watching.

Usage:
  go-cluster-cli watch [options]

Options:`)
		fs.PrintDefaults()
		fmt.Println(`
Example:
  go-cluster-cli watch --config cluster.json --interval 5s`)
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("error")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nStopping watch...")
		cancel()
	}()

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()

	// Display immediately, then on each tick
	displayWatchStatus(ctx, cfg, logger, *jsonOutput)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			displayWatchStatus(ctx, cfg, logger, *jsonOutput)
		}
	}
}

func displayWatchStatus(ctx context.Context, cfg *cluster.FileConfig, logger *slog.Logger, jsonOutput bool) {
	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating manager: %v\n", err)
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	status, err := mgr.Status(queryCtx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting status: %v\n", err)
		return
	}

	// Clear screen (ANSI escape code)
	fmt.Print("\033[H\033[2J")

	timestamp := time.Now().Format("2006-01-02 15:04:05")

	if jsonOutput {
		output := struct {
			Timestamp string          `json:"timestamp"`
			Status    *cluster.Status `json:"status"`
		}{
			Timestamp: timestamp,
			Status:    status,
		}
		data, _ := json.MarshalIndent(output, "", "  ")
		fmt.Println(string(data))
	} else {
		fmt.Printf("=== Cluster Status [%s] ===\n\n", timestamp)
		fmt.Printf("Cluster:   %s\n", status.ClusterID)
		fmt.Printf("Node:      %s\n", status.NodeID)
		fmt.Printf("Role:      %s\n", status.Role)
		fmt.Printf("Connected: %v\n", status.Connected)
		if status.Leader != "" {
			fmt.Printf("Leader:    %s\n", status.Leader)
		} else {
			fmt.Printf("Leader:    (none)\n")
		}
		fmt.Printf("Epoch:     %d\n", status.Epoch)
		if status.Uptime > 0 {
			fmt.Printf("Uptime:    %s\n", status.Uptime.Round(time.Second))
		}
		fmt.Printf("VIP Held:  %v\n", status.VIPHeld)
		fmt.Printf("\nPress Ctrl+C to stop watching...\n")
	}
}

// runPromote attempts to become the cluster leader.
func runPromote(args []string) {
	fs := flag.NewFlagSet("promote", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")

	fs.Usage = func() {
		fmt.Println(`Attempt to become the cluster leader.

This command will block until this node becomes the leader or
another node already holds leadership.

Usage:
  go-cluster-cli promote [options]

Options:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("info")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := mgr.Promote(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully promoted to leader")
}

// runDemote steps down from leadership.
func runDemote(args []string) {
	fs := flag.NewFlagSet("demote", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")

	fs.Usage = func() {
		fmt.Println(`Step down from leadership.

If this node is the current leader, it will voluntarily release
leadership, allowing another node to take over.

Usage:
  go-cluster-cli demote [options]

Options:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("info")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Demote(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully demoted")
}

// runJoin joins the cluster and detects the current leader.
func runJoin(args []string) {
	fs := flag.NewFlagSet("join", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")

	fs.Usage = func() {
		fmt.Println(`Join the cluster and detect the current leader.

This is a quick connectivity check that verifies the node can
connect to the cluster and identifies who the current leader is.

Usage:
  go-cluster-cli join [options]

Options:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("info")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Join(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully joined cluster")
}

// runLeave gracefully leaves the cluster.
func runLeave(args []string) {
	fs := flag.NewFlagSet("leave", flag.ExitOnError)
	configPath := fs.String("config", "cluster.json", "Path to configuration file")

	fs.Usage = func() {
		fmt.Println(`Gracefully leave the cluster.

If this node is the leader, it will step down first before leaving.

Usage:
  go-cluster-cli leave [options]

Options:`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		os.Exit(1)
	}

	logger := setupLogger("info")

	cfg, err := cluster.LoadConfigFromFile(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	mgr, err := cluster.NewManager(*cfg, nil, cluster.WithLogger(logger))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mgr.Leave(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Successfully left cluster")
}

// CLIApp implements cluster.ManagerHooks for the CLI daemon.
type CLIApp struct {
	logger *slog.Logger
}

func (a *CLIApp) OnBecomeLeader(ctx context.Context) error {
	a.logger.Info("This node is now the LEADER")
	return nil
}

func (a *CLIApp) OnLoseLeadership(ctx context.Context) error {
	a.logger.Info("This node lost leadership, now PASSIVE")
	return nil
}

func (a *CLIApp) OnLeaderChange(ctx context.Context, nodeID string) error {
	if nodeID == "" {
		a.logger.Info("No leader currently elected")
	} else {
		a.logger.Info("Cluster leader changed", "leader", nodeID)
	}
	return nil
}

func (a *CLIApp) OnDaemonStart(ctx context.Context) error {
	a.logger.Info("Daemon started")
	return nil
}

func (a *CLIApp) OnDaemonStop(ctx context.Context) error {
	a.logger.Info("Daemon stopped")
	return nil
}

func (a *CLIApp) OnNATSReconnect(ctx context.Context) error {
	a.logger.Info("NATS reconnected")
	return nil
}

func (a *CLIApp) OnNATSDisconnect(ctx context.Context, err error) error {
	a.logger.Warn("NATS disconnected", "error", err)
	return nil
}

func setupLogger(level string) *slog.Logger {
	var logLevel slog.Level
	switch level {
	case "debug":
		logLevel = slog.LevelDebug
	case "info":
		logLevel = slog.LevelInfo
	case "warn":
		logLevel = slog.LevelWarn
	case "error":
		logLevel = slog.LevelError
	default:
		logLevel = slog.LevelInfo
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	}))
	slog.SetDefault(logger)
	return logger
}
