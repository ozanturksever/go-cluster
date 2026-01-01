// Package main demonstrates a multi-node go-cluster deployment with Docker Compose.
//
// This example shows how to:
// - Connect to a NATS cluster for high availability
// - Participate in leader election across multiple nodes
// - Use environment variables for configuration (Docker-friendly)
// - Expose health and metrics endpoints
// - Handle graceful shutdown
//
// Run with Docker Compose:
//
//	cd examples/docker-compose
//	docker compose up -d
//	docker compose logs -f
//
// Test failover:
//
//	docker compose stop node-1
//	# Watch node-2 or node-3 become the leader
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
)

func main() {
	// Read configuration from environment variables (Docker-friendly)
	nodeID := getEnv("NODE_ID", "node-1")
	natsURL := getEnv("NATS_URL", "nats://localhost:4222")
	platformName := getEnv("PLATFORM", "demo-cluster")
	healthAddr := getEnv("HEALTH_ADDR", ":8080")
	metricsAddr := getEnv("METRICS_ADDR", ":9090")

	// Parse labels from environment (LABELS_KEY=value format)
	labels := parseLabelsFromEnv()

	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("  go-cluster Multi-Node Example")
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")
	log.Printf("  Node ID:    %s", nodeID)
	log.Printf("  Platform:   %s", platformName)
	log.Printf("  NATS:       %s", natsURL)
	log.Printf("  Health:     %s", healthAddr)
	log.Printf("  Metrics:    %s", metricsAddr)
	if len(labels) > 0 {
		log.Printf("  Labels:     %v", labels)
	}
	log.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// Build platform options
	opts := []cluster.PlatformOption{
		cluster.HealthAddr(healthAddr),
		cluster.MetricsAddr(metricsAddr),
	}

	if len(labels) > 0 {
		opts = append(opts, cluster.Labels(labels))
	}

	// Create platform with retry logic for NATS connection
	var platform *cluster.Platform
	var err error

	for attempt := 1; attempt <= 10; attempt++ {
		platform, err = cluster.NewPlatform(platformName, nodeID, natsURL, opts...)
		if err == nil {
			break
		}
		log.Printf("Attempt %d: Failed to connect to NATS, retrying in 2s... (%v)", attempt, err)
		time.Sleep(2 * time.Second)
	}

	if err != nil {
		log.Fatalf("Failed to create platform after 10 attempts: %v", err)
	}

	// Create a singleton app (active-passive pattern)
	// Only one node will be the leader at any time
	app := cluster.NewApp("demo-service",
		cluster.Singleton(),
	)

	// Track current role for status endpoint
	var isLeader bool
	var currentLeader string

	// Called when this node becomes the leader
	app.OnActive(func(ctx context.Context) error {
		isLeader = true
		log.Println("")
		log.Println("🟢 ═══════════════════════════════════════════════════════")
		log.Println("🟢  THIS NODE IS NOW THE LEADER")
		log.Println("🟢 ═══════════════════════════════════════════════════════")
		log.Println("")
		log.Println("   → Ready to process requests")
		log.Println("   → Other nodes are standing by")
		log.Println("")
		return nil
	})

	// Called when this node becomes a standby
	app.OnPassive(func(ctx context.Context) error {
		isLeader = false
		log.Println("")
		log.Println("🔵 ═══════════════════════════════════════════════════════")
		log.Println("🔵  THIS NODE IS NOW ON STANDBY")
		log.Println("🔵 ═══════════════════════════════════════════════════════")
		log.Println("")
		log.Println("   → Waiting for failover")
		log.Printf("   → Current leader: %s", currentLeader)
		log.Println("")
		return nil
	})

	// Called when leadership changes (on all nodes)
	app.OnLeaderChange(func(ctx context.Context, leader string) error {
		currentLeader = leader
		log.Printf("📢 Leader changed to: %s", leader)
		return nil
	})

	// Expose a service for discovery
	app.ExposeService("api", cluster.ServiceConfig{
		Port:     8080,
		Protocol: "http",
		Path:     "/",
		Tags:     []string{"demo", "api"},
		Metadata: map[string]string{
			"version": "1.0.0",
		},
	})

	// Register app with platform
	platform.Register(app)

	// Start a simple HTTP status server
	go func() {
		mux := http.NewServeMux()

		// Root endpoint - shows node status
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			role := "standby"
			if isLeader {
				role = "leader"
			}

			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprintf(w, "go-cluster Demo Node\n")
			fmt.Fprintf(w, "====================\n")
			fmt.Fprintf(w, "Node ID:        %s\n", nodeID)
			fmt.Fprintf(w, "Platform:       %s\n", platformName)
			fmt.Fprintf(w, "Role:           %s\n", role)
			fmt.Fprintf(w, "Current Leader: %s\n", currentLeader)
			fmt.Fprintf(w, "Labels:         %v\n", labels)
		})

		// Status endpoint for quick checks
		mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			role := "standby"
			if isLeader {
				role = "leader"
			}
			fmt.Fprintf(w, `{"node":"%s","role":"%s","leader":"%s"}`, nodeID, role, currentLeader)
		})

		// The health endpoint is handled by go-cluster platform
		// This server runs on a different path for demo purposes
		log.Printf("Starting status server on :8888")
		http.ListenAndServe(":8888", mux)
	}()

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start platform in background
	platformErr := make(chan error, 1)
	go func() {
		platformErr <- platform.Run(ctx)
	}()

	log.Printf("")
	log.Printf("✓ Node started successfully!")
	log.Printf("  Health:  http://localhost%s/health", healthAddr)
	log.Printf("  Metrics: http://localhost%s/metrics", metricsAddr)
	log.Printf("  Status:  http://localhost:8888/")
	log.Printf("")
	log.Printf("Waiting for leader election...")

	// Periodic status logging
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				role := "standby"
				if isLeader {
					role = "LEADER"
				}
				log.Printf("[%s] Role: %s | Leader: %s", nodeID, role, currentLeader)
			}
		}
	}()

	// Wait for shutdown signal or platform error
	select {
	case sig := <-sigCh:
		log.Printf("")
		log.Printf("Received signal %v, initiating graceful shutdown...", sig)
		cancel()

		// Give platform time to clean up
		select {
		case <-platformErr:
		case <-time.After(10 * time.Second):
			log.Printf("Shutdown timeout, forcing exit")
		}

	case err := <-platformErr:
		if err != nil {
			log.Printf("Platform error: %v", err)
		}
	}

	log.Printf("")
	log.Printf("Node stopped. Goodbye!")
}

// getEnv returns the value of an environment variable or a default value
func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// parseLabelsFromEnv parses labels from environment variables
// Environment variables with prefix LABELS_ are treated as labels
// e.g., LABELS_ZONE=us-east-1a becomes {"zone": "us-east-1a"}
func parseLabelsFromEnv() map[string]string {
	labels := make(map[string]string)

	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "LABELS_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				// Convert LABELS_ZONE to zone
				key := strings.ToLower(strings.TrimPrefix(parts[0], "LABELS_"))
				labels[key] = parts[1]
			}
		}
	}

	return labels
}
