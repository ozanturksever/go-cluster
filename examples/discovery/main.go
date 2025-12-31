// Package main demonstrates NATS micro service discovery for go-cluster.
//
// This example shows how to:
// - Discover cluster nodes using NATS micro service
// - Query node status via NATS request
// - Ping nodes for health checking
// - Trigger stepdown via NATS request
//
// Usage:
//
//	# Start a cluster node first (in another terminal):
//	NODE_ID=node1 CLUSTER_ID=demo go run examples/basic/main.go
//
//	# Then run this discovery example:
//	CLUSTER_ID=demo go run examples/discovery/main.go
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nats-io/nats.go"
	cluster "github.com/ozanturksever/go-cluster"
)

func main() {
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "demo-cluster"
	}

	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		natsURL = nats.DefaultURL
	}

	// Connect to NATS
	nc, err := nats.Connect(natsURL)
	if err != nil {
		log.Fatalf("Failed to connect to NATS: %v", err)
	}
	defer nc.Close()

	fmt.Printf("Connected to NATS at %s\n", natsURL)
	fmt.Printf("Cluster ID: %s\n\n", clusterID)

	// Example 1: Query node status
	fmt.Println("=== Example 1: Query Node Status ===")
	queryNodeStatus(nc, clusterID, "node1")

	// Example 2: Ping a node
	fmt.Println("\n=== Example 2: Ping Node ===")
	pingNode(nc, clusterID, "node1")

	// Example 3: List all cluster services using $SRV.INFO
	fmt.Println("\n=== Example 3: Discover Services via $SRV.INFO ===")
	discoverServices(nc, clusterID)

	// Example 4: Trigger stepdown (commented out by default)
	// fmt.Println("\n=== Example 4: Trigger Stepdown ===")
	// triggerStepdown(nc, clusterID, "node1")
}

// queryNodeStatus queries a node's status via the status endpoint.
func queryNodeStatus(nc *nats.Conn, clusterID, nodeID string) {
	subject := cluster.StatusSubject(clusterID, nodeID)
	fmt.Printf("Querying status: %s\n", subject)

	resp, err := nc.Request(subject, nil, 5*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var status cluster.StatusResponse
	if err := json.Unmarshal(resp.Data, &status); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("Response:\n")
	fmt.Printf("  Node ID:   %s\n", status.NodeID)
	fmt.Printf("  Role:      %s\n", status.Role)
	fmt.Printf("  Leader:    %s\n", status.Leader)
	fmt.Printf("  Epoch:     %d\n", status.Epoch)
	fmt.Printf("  Uptime:    %dms\n", status.UptimeMs)
	fmt.Printf("  Connected: %v\n", status.Connected)
}

// pingNode sends a health ping to a node.
func pingNode(nc *nats.Conn, clusterID, nodeID string) {
	subject := cluster.PingSubject(clusterID, nodeID)
	fmt.Printf("Pinging node: %s\n", subject)

	start := time.Now()
	resp, err := nc.Request(subject, nil, 5*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	latency := time.Since(start)

	var ping cluster.PingResponse
	if err := json.Unmarshal(resp.Data, &ping); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("Response:\n")
	fmt.Printf("  OK:        %v\n", ping.OK)
	fmt.Printf("  Timestamp: %d\n", ping.Timestamp)
	fmt.Printf("  Latency:   %v\n", latency)
}

// discoverServices discovers cluster services using NATS $SRV.INFO subject.
func discoverServices(nc *nats.Conn, clusterID string) {
	// The service name format is cluster_<clusterID>
	serviceName := fmt.Sprintf("cluster_%s", clusterID)

	// Query service info using $SRV.INFO.<service_name>
	subject := fmt.Sprintf("$SRV.INFO.%s", serviceName)
	fmt.Printf("Discovering services: %s\n", subject)

	// Use a wildcard subscription to receive all responses
	inbox := nc.NewRespInbox()
	sub, err := nc.SubscribeSync(inbox)
	if err != nil {
		fmt.Printf("Error creating subscription: %v\n", err)
		return
	}
	defer sub.Unsubscribe()

	// Send the request
	if err := nc.PublishRequest(subject, inbox, nil); err != nil {
		fmt.Printf("Error sending request: %v\n", err)
		return
	}

	// Collect responses with timeout
	fmt.Printf("Waiting for responses...\n")
	timeout := time.After(2 * time.Second)
	count := 0

	for {
		select {
		case <-timeout:
			if count == 0 {
				fmt.Printf("No services found. Make sure cluster nodes are running.\n")
			}
			return
		default:
			msg, err := sub.NextMsg(100 * time.Millisecond)
			if err != nil {
				continue
			}
			count++
			fmt.Printf("\nService %d:\n%s\n", count, string(msg.Data))
		}
	}
}

// triggerStepdown triggers a stepdown on a node via the control endpoint.
func triggerStepdown(nc *nats.Conn, clusterID, nodeID string) {
	subject := cluster.StepdownSubject(clusterID, nodeID)
	fmt.Printf("Triggering stepdown: %s\n", subject)

	resp, err := nc.Request(subject, nil, 5*time.Second)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	var stepdown cluster.StepdownResponse
	if err := json.Unmarshal(resp.Data, &stepdown); err != nil {
		fmt.Printf("Error parsing response: %v\n", err)
		return
	}

	fmt.Printf("Response:\n")
	fmt.Printf("  OK:    %v\n", stepdown.OK)
	if stepdown.Error != "" {
		fmt.Printf("  Error: %s\n", stepdown.Error)
	}
}
