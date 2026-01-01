// Package main demonstrates a basic active-passive pattern with go-cluster.
//
// This is the simplest example of using go-cluster for high availability.
// It shows how to:
// - Create a singleton app with automatic leader election
// - Use lifecycle hooks (OnActive/OnPassive)
// - Handle graceful shutdown
//
// Run multiple instances:
//
//	# Terminal 1 - Start NATS server first
//	nats-server -js
//
//	# Terminal 2 - Node 1 (will become leader)
//	go run main.go -node node-1
//
//	# Terminal 3 - Node 2 (standby)
//	go run main.go -node node-2
//
//	# Terminal 4 - Node 3 (standby)
//	go run main.go -node node-3
//
// Kill the leader (Ctrl+C) and watch failover happen.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
)

func main() {
	// Parse command line flags
	nodeID := flag.String("node", "node-1", "Node ID")
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	platformName := flag.String("platform", "basic-example", "Platform name")
	flag.Parse()

	log.Printf("Starting basic active-passive example")
	log.Printf("  Node ID: %s", *nodeID)
	log.Printf("  NATS: %s", *natsURL)

	// Create platform
	platform, err := cluster.NewPlatform(*platformName, *nodeID, *natsURL,
		cluster.HealthAddr(":0"),  // Use random port
		cluster.MetricsAddr(":0"), // Use random port
	)
	if err != nil {
		log.Fatalf("Failed to create platform: %v", err)
	}

	// Create a singleton app
	// Only one instance will be active at a time
	app := cluster.NewApp("my-service",
		cluster.Singleton(), // Exactly one active instance
	)

	// Called when this instance becomes the leader
	app.OnActive(func(ctx context.Context) error {
		log.Println("🟢 I am now the LEADER!")
		log.Println("   Starting to process work...")

		// In a real app, you would start your service here:
		// - Start accepting connections
		// - Begin processing jobs
		// - Acquire VIP, etc.

		return nil
	})

	// Called when this instance becomes a standby
	app.OnPassive(func(ctx context.Context) error {
		log.Println("🔵 I am now STANDBY")
		log.Println("   Waiting for failover...")

		// In a real app, you would stop processing here:
		// - Stop accepting new connections
		// - Drain existing work
		// - Release VIP, etc.

		return nil
	})

	// Called whenever the leader changes
	app.OnLeaderChange(func(ctx context.Context, leader string) error {
		log.Printf("📢 Leader changed to: %s", leader)
		return nil
	})

	// Register app with platform
	platform.Register(app)

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

	log.Printf("Node started. Press Ctrl+C to stop.")

	// Simulate some work while running
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if app.IsLeader() {
					fmt.Println("   [Leader] Doing important work...")
				} else {
					fmt.Println("   [Standby] Ready and waiting...")
				}
			}
		}
	}()

	// Wait for shutdown signal or platform error
	select {
	case sig := <-sigCh:
		log.Printf("Received signal %v, shutting down...", sig)
		cancel()
	case err := <-platformErr:
		if err != nil {
			log.Printf("Platform error: %v", err)
		}
	}

	log.Println("Node stopped.")
}
