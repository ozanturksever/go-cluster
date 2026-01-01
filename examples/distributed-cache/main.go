// Package main demonstrates a distributed cache using the Ring pattern.
//
// This example shows how to build a partition-based distributed cache where:
// - Data is sharded across nodes using consistent hashing
// - Each node owns a subset of partitions
// - Requests are automatically routed to the correct node
// - Partition ownership is rebalanced when nodes join/leave
//
// Run multiple instances:
//
//	# Terminal 1 - Start NATS server first
//	nats-server -js
//
//	# Terminal 2 - Node 1
//	go run main.go -node node-1 -http :8081
//
//	# Terminal 3 - Node 2
//	go run main.go -node node-2 -http :8082
//
//	# Terminal 4 - Node 3
//	go run main.go -node node-3 -http :8083
//
// Test the cache:
//
//	curl -X POST http://localhost:8081/set -d '{"key":"user:1","value":"Alice"}'
//	curl http://localhost:8082/get?key=user:1
//	curl -X DELETE http://localhost:8083/delete?key=user:1
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
)

// CacheEntry represents a cached value with metadata.
type CacheEntry struct {
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	TTL       int64     `json:"ttl,omitempty"` // TTL in seconds, 0 means no expiry
	Partition int       `json:"-"`             // Partition this key belongs to
}

// PartitionFunc calculates the partition for a given key.
type PartitionFunc func(key string) int

// DistributedCache is a partition-aware cache that stores data locally
// only for partitions owned by this node.
type DistributedCache struct {
	mu         sync.RWMutex
	data       map[string]*CacheEntry // key -> entry
	partitions map[int]bool           // partitions owned by this node
	nodeID     string

	// Partition calculator function
	partitionFunc PartitionFunc

	// TTL cleanup
	cleanupInterval time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup

	// Stats
	expiredCount          int64 // Total entries expired by TTL cleanup
	partitionCleanedCount int64 // Total entries cleaned due to partition release
}

// NewDistributedCache creates a new distributed cache instance.
func NewDistributedCache(nodeID string, partitionFunc PartitionFunc) *DistributedCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &DistributedCache{
		data:            make(map[string]*CacheEntry),
		partitions:      make(map[int]bool),
		nodeID:          nodeID,
		partitionFunc:   partitionFunc,
		cleanupInterval: 30 * time.Second,
		ctx:             ctx,
		cancel:          cancel,
	}
}

// StartCleanup starts the background TTL expiration cleanup goroutine.
func (c *DistributedCache) StartCleanup() {
	c.wg.Add(1)
	go c.cleanupLoop()
	log.Printf("[%s] Started TTL cleanup goroutine (interval: %v)", c.nodeID, c.cleanupInterval)
}

// StopCleanup stops the background cleanup goroutine.
func (c *DistributedCache) StopCleanup() {
	c.cancel()
	c.wg.Wait()
	log.Printf("[%s] Stopped TTL cleanup goroutine", c.nodeID)
}

// cleanupLoop runs periodically to remove expired entries.
func (c *DistributedCache) cleanupLoop() {
	defer c.wg.Done()

	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			expired := c.cleanupExpired()
			if expired > 0 {
				log.Printf("[%s] TTL cleanup: removed %d expired entries", c.nodeID, expired)
			}
		}
	}
}

// cleanupExpired removes all expired entries and returns the count.
func (c *DistributedCache) cleanupExpired() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	expired := 0

	for key, entry := range c.data {
		if entry.TTL > 0 && now.Sub(entry.CreatedAt).Seconds() > float64(entry.TTL) {
			delete(c.data, key)
			expired++
		}
	}

	c.expiredCount += int64(expired)
	return expired
}

// OnOwn is called when this node acquires ownership of partitions.
// In a real system, you might load data from a persistent store here.
func (c *DistributedCache) OnOwn(partitions []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, p := range partitions {
		c.partitions[p] = true
		log.Printf("[%s] Acquired partition %d", c.nodeID, p)
	}

	log.Printf("[%s] Now owning %d partitions", c.nodeID, len(c.partitions))
}

// OnRelease is called when this node releases ownership of partitions.
// It cleans up all data belonging to the released partitions.
func (c *DistributedCache) OnRelease(partitions []int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Build a set of partitions being released for fast lookup
	releasedSet := make(map[int]bool, len(partitions))
	for _, p := range partitions {
		releasedSet[p] = true
		delete(c.partitions, p)
		log.Printf("[%s] Released partition %d", c.nodeID, p)
	}

	// Clean up data for released partitions
	cleaned := 0
	for key, entry := range c.data {
		if releasedSet[entry.Partition] {
			delete(c.data, key)
			cleaned++
		}
	}

	c.partitionCleanedCount += int64(cleaned)

	if cleaned > 0 {
		log.Printf("[%s] Cleaned up %d entries for released partitions", c.nodeID, cleaned)
	}
	log.Printf("[%s] Now owning %d partitions", c.nodeID, len(c.partitions))
}

// Set stores a value in the cache.
func (c *DistributedCache) Set(key, value string, ttl int64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Calculate partition for this key so we can clean it up later
	partition := -1
	if c.partitionFunc != nil {
		partition = c.partitionFunc(key)
	}

	c.data[key] = &CacheEntry{
		Value:     value,
		CreatedAt: time.Now(),
		TTL:       ttl,
		Partition: partition,
	}
}

// Get retrieves a value from the cache.
func (c *DistributedCache) Get(key string) (*CacheEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.data[key]
	if !ok {
		return nil, false
	}

	// Check TTL
	if entry.TTL > 0 {
		if time.Since(entry.CreatedAt).Seconds() > float64(entry.TTL) {
			return nil, false
		}
	}

	return entry, true
}

// Delete removes a value from the cache.
func (c *DistributedCache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	_, exists := c.data[key]
	delete(c.data, key)
	return exists
}

// Stats returns cache statistics.
func (c *DistributedCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	partList := make([]int, 0, len(c.partitions))
	for p := range c.partitions {
		partList = append(partList, p)
	}

	// Count entries with TTL
	entriesWithTTL := 0
	for _, entry := range c.data {
		if entry.TTL > 0 {
			entriesWithTTL++
		}
	}

	return map[string]interface{}{
		"node_id":                  c.nodeID,
		"entries":                  len(c.data),
		"entries_with_ttl":         entriesWithTTL,
		"partitions":               partList,
		"partition_count":          len(c.partitions),
		"total_expired_by_ttl":     c.expiredCount,
		"total_cleaned_by_release": c.partitionCleanedCount,
	}
}

// RPC request/response types
type (
	SetRequest struct {
		Key   string `json:"key"`
		Value string `json:"value"`
		TTL   int64  `json:"ttl,omitempty"`
	}

	SetResponse struct {
		Success bool   `json:"success"`
		Node    string `json:"node"`
	}

	GetRequest struct {
		Key string `json:"key"`
	}

	GetResponse struct {
		Found bool   `json:"found"`
		Value string `json:"value,omitempty"`
		Node  string `json:"node"`
	}

	DeleteRequest struct {
		Key string `json:"key"`
	}

	DeleteResponse struct {
		Deleted bool   `json:"deleted"`
		Node    string `json:"node"`
	}
)

// CacheServer wraps the cache with RPC handlers and HTTP server.
type CacheServer struct {
	cache    *DistributedCache
	app      *cluster.App
	platform *cluster.Platform
	nodeID   string
}

// NewCacheServer creates a new cache server.
func NewCacheServer(platform *cluster.Platform, app *cluster.App, nodeID string) *CacheServer {
	s := &CacheServer{
		app:      app,
		platform: platform,
		nodeID:   nodeID,
	}

	// Create cache with a partition function that uses the ring
	// The ring might not be initialized yet, so we need a lazy lookup
	partitionFunc := func(key string) int {
		if ring := s.app.Ring(); ring != nil {
			return ring.PartitionFor(key)
		}
		return -1
	}
	s.cache = NewDistributedCache(nodeID, partitionFunc)

	return s
}

// StartCleanup starts the background cleanup goroutine.
func (s *CacheServer) StartCleanup() {
	s.cache.StartCleanup()
}

// StopCleanup stops the background cleanup goroutine.
func (s *CacheServer) StopCleanup() {
	s.cache.StopCleanup()
}

// RegisterRPCHandlers registers the cache RPC handlers.
func (s *CacheServer) RegisterRPCHandlers() {
	// Handle SET requests
	s.app.Handle("cache.set", func(ctx context.Context, req []byte) ([]byte, error) {
		var setReq SetRequest
		if err := json.Unmarshal(req, &setReq); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}

		s.cache.Set(setReq.Key, setReq.Value, setReq.TTL)

		resp := SetResponse{
			Success: true,
			Node:    s.nodeID,
		}
		return json.Marshal(resp)
	})

	// Handle GET requests
	s.app.Handle("cache.get", func(ctx context.Context, req []byte) ([]byte, error) {
		var getReq GetRequest
		if err := json.Unmarshal(req, &getReq); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}

		entry, found := s.cache.Get(getReq.Key)

		resp := GetResponse{
			Found: found,
			Node:  s.nodeID,
		}
		if found {
			resp.Value = entry.Value
		}
		return json.Marshal(resp)
	})

	// Handle DELETE requests
	s.app.Handle("cache.delete", func(ctx context.Context, req []byte) ([]byte, error) {
		var delReq DeleteRequest
		if err := json.Unmarshal(req, &delReq); err != nil {
			return nil, fmt.Errorf("invalid request: %w", err)
		}

		deleted := s.cache.Delete(delReq.Key)

		resp := DeleteResponse{
			Deleted: deleted,
			Node:    s.nodeID,
		}
		return json.Marshal(resp)
	})
}

// Set stores a value, routing to the correct node if needed.
func (s *CacheServer) Set(ctx context.Context, key, value string, ttl int64) (*SetResponse, error) {
	ring := s.app.Ring()
	if ring == nil {
		return nil, fmt.Errorf("ring not initialized")
	}

	// Check if we own this key
	if ring.OwnsKey(key) {
		s.cache.Set(key, value, ttl)
		return &SetResponse{Success: true, Node: s.nodeID}, nil
	}

	// Route to the correct node
	targetNode, err := ring.NodeFor(key)
	if err != nil {
		return nil, fmt.Errorf("failed to find node for key: %w", err)
	}

	req := SetRequest{Key: key, Value: value, TTL: ttl}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respData, err := s.app.RPC().Call(ctx, targetNode, "cache.set", reqData)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}

	var resp SetResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Get retrieves a value, routing to the correct node if needed.
func (s *CacheServer) Get(ctx context.Context, key string) (*GetResponse, error) {
	ring := s.app.Ring()
	if ring == nil {
		return nil, fmt.Errorf("ring not initialized")
	}

	// Check if we own this key
	if ring.OwnsKey(key) {
		entry, found := s.cache.Get(key)
		resp := &GetResponse{Found: found, Node: s.nodeID}
		if found {
			resp.Value = entry.Value
		}
		return resp, nil
	}

	// Route to the correct node
	targetNode, err := ring.NodeFor(key)
	if err != nil {
		return nil, fmt.Errorf("failed to find node for key: %w", err)
	}

	req := GetRequest{Key: key}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respData, err := s.app.RPC().Call(ctx, targetNode, "cache.get", reqData)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}

	var resp GetResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// Delete removes a value, routing to the correct node if needed.
func (s *CacheServer) Delete(ctx context.Context, key string) (*DeleteResponse, error) {
	ring := s.app.Ring()
	if ring == nil {
		return nil, fmt.Errorf("ring not initialized")
	}

	// Check if we own this key
	if ring.OwnsKey(key) {
		deleted := s.cache.Delete(key)
		return &DeleteResponse{Deleted: deleted, Node: s.nodeID}, nil
	}

	// Route to the correct node
	targetNode, err := ring.NodeFor(key)
	if err != nil {
		return nil, fmt.Errorf("failed to find node for key: %w", err)
	}

	req := DeleteRequest{Key: key}
	reqData, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	respData, err := s.app.RPC().Call(ctx, targetNode, "cache.delete", reqData)
	if err != nil {
		return nil, fmt.Errorf("RPC call failed: %w", err)
	}

	var resp DeleteResponse
	if err := json.Unmarshal(respData, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}

// StartHTTPServer starts the HTTP server for cache operations.
func (s *CacheServer) StartHTTPServer(addr string) error {
	mux := http.NewServeMux()

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "node": s.nodeID})
	})

	// Stats endpoint
	mux.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		stats := s.cache.Stats()

		// Add ring stats if available
		if ring := s.app.Ring(); ring != nil {
			ringStats := ring.Stats()
			stats["ring"] = ringStats
		}

		json.NewEncoder(w).Encode(stats)
	})

	// SET endpoint
	mux.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req SetRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid JSON: "+err.Error(), http.StatusBadRequest)
			return
		}

		if req.Key == "" {
			http.Error(w, "Key is required", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		resp, err := s.Set(ctx, req.Key, req.Value, req.TTL)
		if err != nil {
			http.Error(w, "Set failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// GET endpoint
	mux.HandleFunc("/get", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "Key parameter is required", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		resp, err := s.Get(ctx, key)
		if err != nil {
			http.Error(w, "Get failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if !resp.Found {
			w.WriteHeader(http.StatusNotFound)
		}
		json.NewEncoder(w).Encode(resp)
	})

	// DELETE endpoint
	mux.HandleFunc("/delete", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "Key parameter is required", http.StatusBadRequest)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		resp, err := s.Delete(ctx, key)
		if err != nil {
			http.Error(w, "Delete failed: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})

	// Partition info endpoint - shows which node owns a key
	mux.HandleFunc("/owner", func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Query().Get("key")
		if key == "" {
			http.Error(w, "Key parameter is required", http.StatusBadRequest)
			return
		}

		ring := s.app.Ring()
		if ring == nil {
			http.Error(w, "Ring not initialized", http.StatusServiceUnavailable)
			return
		}

		partition := ring.PartitionFor(key)
		owner, err := ring.NodeFor(key)
		if err != nil {
			http.Error(w, "Failed to find owner: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"key":        key,
			"partition":  partition,
			"owner":      owner,
			"is_local":   ring.OwnsKey(key),
			"this_node":  s.nodeID,
		})
	})

	log.Printf("[%s] Starting HTTP server on %s", s.nodeID, addr)
	return http.ListenAndServe(addr, mux)
}

func main() {
	// Parse command line flags
	nodeID := flag.String("node", "node-1", "Node ID")
	natsURL := flag.String("nats", "nats://localhost:4222", "NATS server URL")
	httpAddr := flag.String("http", ":8080", "HTTP server address")
	partitions := flag.Int("partitions", 64, "Number of partitions")
	platformName := flag.String("platform", "cache-platform", "Platform name")
	flag.Parse()

	log.Printf("Starting distributed cache node %s", *nodeID)
	log.Printf("  NATS URL: %s", *natsURL)
	log.Printf("  HTTP address: %s", *httpAddr)
	log.Printf("  Partitions: %d", *partitions)

	// Create platform
	platform, err := cluster.NewPlatform(*platformName, *nodeID, *natsURL,
		cluster.HealthAddr(":0"),   // Use random port for health
		cluster.MetricsAddr(":0"),  // Use random port for metrics
	)
	if err != nil {
		log.Fatalf("Failed to create platform: %v", err)
	}

	// Create cache app with Ring pattern
	// - Spread() means all nodes are active
	// - Ring(partitions) enables consistent hashing
	app := cluster.NewApp("distributed-cache",
		cluster.Spread(),
		cluster.Ring(*partitions),
	)

	// Create cache server
	server := NewCacheServer(platform, app, *nodeID)

	// Register partition lifecycle hooks
	// OnOwn is called when this node acquires partitions
	app.OnOwn(func(ctx context.Context, partitions []int) error {
		server.cache.OnOwn(partitions)
		return nil
	})

	// OnRelease is called when this node releases partitions
	app.OnRelease(func(ctx context.Context, partitions []int) error {
		server.cache.OnRelease(partitions)
		return nil
	})

	// Register RPC handlers for cache operations
	server.RegisterRPCHandlers()

	// Register app with platform
	platform.Register(app)

	// Create context that cancels on interrupt
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server in background
	go func() {
		if err := server.StartHTTPServer(*httpAddr); err != nil {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Start background TTL cleanup
	server.StartCleanup()

	// Start platform in background
	platformErr := make(chan error, 1)
	go func() {
		platformErr <- platform.Run(ctx)
	}()

	log.Printf("[%s] Cache node started. Press Ctrl+C to stop.", *nodeID)

	// Wait for shutdown signal or platform error
	select {
	case sig := <-sigCh:
		log.Printf("[%s] Received signal %v, shutting down...", *nodeID, sig)
		cancel()
	case err := <-platformErr:
		if err != nil {
			log.Printf("[%s] Platform error: %v", *nodeID, err)
		}
	}

	// Stop background cleanup
	server.StopCleanup()

	log.Printf("[%s] Cache node stopped", *nodeID)
}
