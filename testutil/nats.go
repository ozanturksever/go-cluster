// Package testutil provides testing utilities for go-cluster.
package testutil

import (
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
)

// NATSServer wraps an embedded NATS server for testing.
type NATSServer struct {
	server *server.Server
	url    string
}

// StartNATS starts an embedded NATS server with JetStream enabled.
func StartNATS(t *testing.T) *NATSServer {
	t.Helper()

	opts := &server.Options{
		Host:           "127.0.0.1",
		Port:           -1, // Random port
		NoLog:          true,
		NoSigs:         true,
		JetStream:      true,
		StoreDir:       t.TempDir(),
		JetStreamMaxMemory: 64 * 1024 * 1024,
		JetStreamMaxStore:  256 * 1024 * 1024,
	}

	ns, err := server.NewServer(opts)
	if err != nil {
		t.Fatalf("failed to create NATS server: %v", err)
	}

	go ns.Start()

	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("NATS server not ready")
	}

	return &NATSServer{
		server: ns,
		url:    ns.ClientURL(),
	}
}

// URL returns the NATS server URL.
func (n *NATSServer) URL() string {
	return n.url
}

// Stop stops the NATS server.
func (n *NATSServer) Stop() {
	if n.server != nil {
		n.server.Shutdown()
	}
}

// Connect creates a new NATS connection to the test server.
func (n *NATSServer) Connect(t *testing.T) *nats.Conn {
	t.Helper()

	nc, err := nats.Connect(n.url)
	if err != nil {
		t.Fatalf("failed to connect to NATS: %v", err)
	}

	t.Cleanup(func() {
		nc.Close()
	})

	return nc
}

// NATSCluster wraps multiple NATS servers for cluster testing.
type NATSCluster struct {
	servers []*NATSServer
	urls    []string
}

// StartNATSCluster starts a cluster of NATS servers.
func StartNATSCluster(t *testing.T, nodes int) *NATSCluster {
	t.Helper()

	if nodes < 1 {
		nodes = 1
	}

	cluster := &NATSCluster{
		servers: make([]*NATSServer, nodes),
		urls:    make([]string, nodes),
	}

	// For simplicity, just start independent servers
	// In a real implementation, you'd configure them as a cluster
	for i := 0; i < nodes; i++ {
		ns := StartNATS(t)
		cluster.servers[i] = ns
		cluster.urls[i] = ns.URL()
	}

	return cluster
}

// URLs returns all server URLs.
func (c *NATSCluster) URLs() []string {
	return c.urls
}

// URL returns the first server URL.
func (c *NATSCluster) URL() string {
	if len(c.urls) > 0 {
		return c.urls[0]
	}
	return ""
}

// Stop stops all servers in the cluster.
func (c *NATSCluster) Stop() {
	for _, ns := range c.servers {
		if ns != nil {
			ns.Stop()
		}
	}
}

// Partition simulates a network partition by stopping the server for the given node.
func (c *NATSCluster) Partition(nodeIndex int) {
	if nodeIndex >= 0 && nodeIndex < len(c.servers) {
		c.servers[nodeIndex].Stop()
	}
}

// Heal restarts all stopped servers.
func (c *NATSCluster) Heal() {
	// In a real implementation, you'd restart partitioned servers
	// For now, this is a placeholder
}

// WaitForReady waits for all servers to be ready.
func (c *NATSCluster) WaitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		allReady := true
		for _, ns := range c.servers {
			if ns.server == nil || !ns.server.ReadyForConnections(100*time.Millisecond) {
				allReady = false
				break
			}
		}
		if allReady {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("cluster not ready within %v", timeout)
}
