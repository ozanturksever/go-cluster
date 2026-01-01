// Package main demonstrates a full Convex backend integration with go-cluster.
//
// This example shows how to build a production-ready HA setup with:
// - SQLite database with WAL replication via Litestream
// - Virtual IP (VIP) failover for seamless client access
// - Backend service lifecycle management
// - Health checks and observability
//
// This mirrors the convex-cluster-manager pattern from the go-cluster specification.
//
// Prerequisites:
// - NATS server with JetStream
// - Root/sudo for VIP management (or CAP_NET_ADMIN)
// - A backend service binary (this example simulates one)
//
// Run:
//
//	# Terminal 1 - Start NATS server
//	nats-server -js
//
//	# Terminal 2 - Primary node (will get VIP)
//	sudo go run main.go -node node-1 -vip 10.0.1.100/24 -interface eth0
//
//	# Terminal 3 - Standby node
//	sudo go run main.go -node node-2 -vip 10.0.1.100/24 -interface eth0
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/backend"
)

func main() {
	// Parse command line flags
	nodeID := flag.String("node", "node-1", "Node ID")
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	platformName := flag.String("platform", "convex-prod", "Platform name")
	dbPath := flag.String("db", "/tmp/convex-data.db", "Database path")
	replicaPath := flag.String("replica", "/tmp/convex-replica.db", "Replica database path")
	vipCIDR := flag.String("vip", "", "Virtual IP in CIDR notation (e.g., 10.0.1.100/24)")
	vipInterface := flag.String("interface", "eth0", "Network interface for VIP")
	httpPort := flag.Int("http", 8080, "HTTP server port")
	flag.Parse()

	log.Printf("Starting Convex backend with go-cluster")
	log.Printf("  Node ID:    %s", *nodeID)
	log.Printf("  NATS:       %s", *natsURL)
	log.Printf("  Database:   %s", *dbPath)
	log.Printf("  Replica:    %s", *replicaPath)
	if *vipCIDR != "" {
		log.Printf("  VIP:        %s on %s", *vipCIDR, *vipInterface)
	}

	// Platform options
	platformOpts := []cluster.PlatformOption{
		cluster.HealthAddr(fmt.Sprintf(":%d", *httpPort)),
		cluster.MetricsAddr(fmt.Sprintf(":%d", *httpPort+1000)),
		cluster.Labels(map[string]string{
			"role": "convex-backend",
			"zone": "us-east-1a",
		}),
	}

	// Create platform
	platform, err := cluster.NewPlatform(*platformName, *nodeID, *natsURL, platformOpts...)
	if err != nil {
		log.Fatalf("Failed to create platform: %v", err)
	}

	// Build app options
	appOpts := []cluster.AppOption{
		cluster.Singleton(), // Active-passive pattern
	}

	// Add SQLite configuration
	appOpts = append(appOpts, cluster.WithSQLite(*dbPath,
		cluster.SQLiteReplica(*replicaPath),
		cluster.SQLiteSnapshotInterval(1*time.Hour),
		cluster.SQLiteRetention(24*time.Hour),
	))

	// Add VIP configuration if specified
	if *vipCIDR != "" {
		appOpts = append(appOpts, cluster.VIP(*vipCIDR, *vipInterface))
	}

	// Add a simulated backend process
	// In production, this would be cluster.Systemd("convex-backend") or similar
	appOpts = append(appOpts, cluster.WithBackend(
		backend.Process(backend.ProcessConfig{
			Command:         []string{"sleep", "infinity"}, // Simulated backend
			GracefulTimeout: 10 * time.Second,
		}),
	))

	// Create the app
	app := cluster.NewApp("convex", appOpts...)

	// Lifecycle hooks
	app.OnActive(func(ctx context.Context) error {
		log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Println("🟢 PROMOTED TO PRIMARY")
		log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Println("  1. Database promoted to read-write mode")
		log.Println("  2. VIP acquired (if configured)")
		log.Println("  3. Backend service started")
		log.Println("  4. WAL replication started")
		log.Println("  Ready to serve traffic!")
		return nil
	})

	app.OnPassive(func(ctx context.Context) error {
		log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Println("🔵 DEMOTED TO STANDBY")
		log.Println("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
		log.Println("  1. Backend service stopped")
		log.Println("  2. VIP released (if configured)")
		log.Println("  3. Database set to read-only mode")
		log.Println("  4. WAL restore from NATS started")
		log.Println("  Ready for failover!")
		return nil
	})

	app.OnLeaderChange(func(ctx context.Context, leader string) error {
		log.Printf("📢 Cluster leader is now: %s", leader)
		return nil
	})

	// Expose services for discovery
	app.ExposeService("http", cluster.ServiceConfig{
		Port:     3210,
		Protocol: "http",
		Path:     "/",
		Metadata: map[string]string{
			"version": "1.0.0",
			"docs":    "/api/docs",
		},
		Tags: []string{"api", "convex"},
	})

	app.ExposeService("metrics", cluster.ServiceConfig{
		Port:     9090,
		Protocol: "http",
		Path:     "/metrics",
		Metadata: map[string]string{
			"format": "prometheus",
		},
		Tags: []string{"monitoring", "prometheus"},
	})

	// Register app
	platform.Register(app)

	// Create context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start a simple status HTTP server
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			role := "standby"
			if app.IsLeader() {
				role = "primary"
			}
			fmt.Fprintf(w, "Convex Backend\n")
			fmt.Fprintf(w, "Node: %s\n", *nodeID)
			fmt.Fprintf(w, "Role: %s\n", role)
		})
		http.ListenAndServe(fmt.Sprintf(":%d", *httpPort+100), mux)
	}()

	// Start platform
	platformErr := make(chan error, 1)
	go func() {
		platformErr <- platform.Run(ctx)
	}()

	log.Printf("")
	log.Printf("Node started successfully!")
	log.Printf("  Status: http://localhost:%d/", *httpPort+100)
	log.Printf("  Health: http://localhost:%d/health", *httpPort)
	log.Printf("")
	log.Printf("Press Ctrl+C to initiate graceful shutdown.")

	// Wait for shutdown
	select {
	case sig := <-sigCh:
		log.Printf("")
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cancel()
	case err := <-platformErr:
		if err != nil {
			log.Printf("Platform error: %v", err)
		}
	}

	log.Println("Shutdown complete.")
}
