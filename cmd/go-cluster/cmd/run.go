package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var runCmd = &cobra.Command{
	Use:   "run",
	Short: "Run the cluster daemon",
	Long: `Start a go-cluster node that participates in the cluster.

The node will:
- Connect to NATS and join the cluster
- Start health and metrics endpoints
- Participate in leader election for registered apps
- Handle service discovery and coordination

Example:
  go-cluster run --platform myapp --node node-1
  go-cluster run --config /etc/go-cluster/config.yaml`,
	RunE: runDaemon,
}

func init() {
	rootCmd.AddCommand(runCmd)

	// Run-specific flags
	runCmd.Flags().String("health-addr", ":8080", "Health check HTTP address")
	runCmd.Flags().String("metrics-addr", ":9090", "Prometheus metrics HTTP address")
	runCmd.Flags().StringToString("labels", nil, "Node labels (key=value pairs)")

	// Bind to viper
	viper.BindPFlag("health_addr", runCmd.Flags().Lookup("health-addr"))
	viper.BindPFlag("metrics_addr", runCmd.Flags().Lookup("metrics-addr"))
	viper.BindPFlag("labels", runCmd.Flags().Lookup("labels"))
}

func runDaemon(cmd *cobra.Command, args []string) error {
	platformName := getPlatform()
	if platformName == "" {
		return fmt.Errorf("platform name is required (use --platform or set PLATFORM env)")
	}

	nodeID := getNodeID()
	natsURL := getNATSURL()

	healthAddr := viper.GetString("health_addr")
	if healthAddr == "" {
		healthAddr = ":8080"
	}

	metricsAddr := viper.GetString("metrics_addr")
	if metricsAddr == "" {
		metricsAddr = ":9090"
	}

	labels := viper.GetStringMapString("labels")

	fmt.Println("Starting go-cluster daemon...")
	fmt.Printf("  Platform:     %s\n", platformName)
	fmt.Printf("  Node ID:      %s\n", nodeID)
	fmt.Printf("  NATS URL:     %s\n", natsURL)
	fmt.Printf("  Health:       %s\n", healthAddr)
	fmt.Printf("  Metrics:      %s\n", metricsAddr)
	if len(labels) > 0 {
		fmt.Printf("  Labels:       %v\n", labels)
	}
	fmt.Println()

	// Build platform options
	opts := []cluster.PlatformOption{
		cluster.HealthAddr(healthAddr),
		cluster.MetricsAddr(metricsAddr),
	}

	if len(labels) > 0 {
		opts = append(opts, cluster.Labels(labels))
	}

	// Load NATS credentials if specified
	if creds := viper.GetString("nats_creds"); creds != "" {
		opts = append(opts, cluster.NATSCreds(creds))
	}

	// Create platform
	platform, err := cluster.NewPlatform(platformName, nodeID, natsURL, opts...)
	if err != nil {
		return fmt.Errorf("failed to create platform: %w", err)
	}

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start platform
	errCh := make(chan error, 1)
	go func() {
		errCh <- platform.Run(ctx)
	}()

	fmt.Println("Cluster daemon started. Press Ctrl+C to stop.")

	// Wait for shutdown or error
	select {
	case sig := <-sigCh:
		fmt.Printf("\nReceived signal %v, shutting down...\n", sig)
		cancel()
		// Wait for platform to stop
		<-errCh
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("platform error: %w", err)
		}
	}

	fmt.Println("Cluster daemon stopped.")
	return nil
}
