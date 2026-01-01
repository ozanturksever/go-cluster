package cluster

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

const (
	// DefaultVirtualNodes is the default number of virtual nodes per physical node
	DefaultVirtualNodes = 150
	// DefaultPartitions is the default number of partitions
	DefaultPartitions = 256
)

// HashRing manages a consistent hash ring for partitioning data across nodes.
type HashRing struct {
	app *App

	// Configuration
	numPartitions int
	virtualNodes  int

	// Ring state
	mu           sync.RWMutex
	nodes        []string              // Sorted list of node IDs
	vnodes       []vnode               // Sorted list of virtual nodes by hash
	partitions   map[int]string        // partition -> owner node ID
	ownedParts   []int                 // Partitions owned by this node
	nodePartMap  map[string][]int      // node ID -> partitions owned

	// KV for partition state
	kv jetstream.KeyValue

	// Coordination
	rebalancing   bool
	rebalanceCh   chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// vnode represents a virtual node on the ring
type vnode struct {
	hash   uint64
	nodeID string
}

// PartitionAssignment represents the stored partition assignments
type PartitionAssignment struct {
	Version    uint64            `json:"version"`
	Partitions map[int]string    `json:"partitions"` // partition -> node ID
	UpdatedAt  time.Time         `json:"updated_at"`
	UpdatedBy  string            `json:"updated_by"`
}

// PartitionTransfer represents a partition transfer request
type PartitionTransfer struct {
	Partition  int       `json:"partition"`
	FromNode   string    `json:"from_node"`
	ToNode     string    `json:"to_node"`
	StartedAt  time.Time `json:"started_at"`
	Status     string    `json:"status"` // "pending", "in_progress", "completed", "failed"
}

// NewHashRing creates a new consistent hash ring for the app.
func NewHashRing(app *App) (*HashRing, error) {
	numPartitions := app.ringPartitions
	if numPartitions <= 0 {
		numPartitions = DefaultPartitions
	}

	r := &HashRing{
		app:           app,
		numPartitions: numPartitions,
		virtualNodes:  DefaultVirtualNodes,
		nodes:         make([]string, 0),
		vnodes:        make([]vnode, 0),
		partitions:    make(map[int]string),
		ownedParts:    make([]int, 0),
		nodePartMap:   make(map[string][]int),
		rebalanceCh:   make(chan struct{}, 1),
	}

	return r, nil
}

// Start initializes and starts the ring.
func (r *HashRing) Start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Create KV bucket for partition assignments
	bucketName := fmt.Sprintf("%s_%s_ring", r.app.platform.name, r.app.name)
	kv, err := r.app.platform.js.CreateOrUpdateKeyValue(ctx, jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Ring partition assignments for %s/%s", r.app.platform.name, r.app.name),
		History:     5,
	})
	if err != nil {
		return fmt.Errorf("failed to create ring KV bucket: %w", err)
	}
	r.kv = kv

	// Load existing assignments
	if err := r.loadAssignments(ctx); err != nil {
		return fmt.Errorf("failed to load partition assignments: %w", err)
	}

	// Build initial ring from membership
	r.rebuildRing()

	// Set up membership hooks to trigger rebalancing
	r.setupMembershipHooks()

	// Start background workers
	r.wg.Add(2)
	go r.membershipWatcher()
	go r.assignmentWatcher()

	// Trigger initial rebalance
	r.triggerRebalance()

	return nil
}

// setupMembershipHooks sets up hooks to detect membership changes.
func (r *HashRing) setupMembershipHooks() {
	origOnJoin := r.app.onMemberJoin
	origOnLeave := r.app.onMemberLeave

	r.app.onMemberJoin = func(ctx context.Context, member Member) error {
		if origOnJoin != nil {
			if err := origOnJoin(ctx, member); err != nil {
				return err
			}
		}
		// Check if this member is running our app
		for _, appName := range member.Apps {
			if appName == r.app.name {
				r.triggerRebalance()
				break
			}
		}
		return nil
	}

	r.app.onMemberLeave = func(ctx context.Context, member Member) error {
		if origOnLeave != nil {
			if err := origOnLeave(ctx, member); err != nil {
				return err
			}
		}
		// Check if this member was running our app
		for _, appName := range member.Apps {
			if appName == r.app.name {
				r.triggerRebalance()
				break
			}
		}
		return nil
	}
}

// Stop stops the ring.
func (r *HashRing) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	r.wg.Wait()
}

// NumPartitions returns the number of partitions in the ring.
func (r *HashRing) NumPartitions() int {
	return r.numPartitions
}

// NodeFor returns the node ID responsible for the given key.
func (r *HashRing) NodeFor(key string) (string, error) {
	partition := r.PartitionFor(key)
	return r.NodeForPartition(partition)
}

// PartitionFor returns the partition number for the given key.
func (r *HashRing) PartitionFor(key string) int {
	h := hashKey(key)
	return int(h % uint64(r.numPartitions))
}

// NodeForPartition returns the node ID responsible for the given partition.
func (r *HashRing) NodeForPartition(partition int) (string, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if partition < 0 || partition >= r.numPartitions {
		return "", fmt.Errorf("partition %d out of range [0, %d)", partition, r.numPartitions)
	}

	node, ok := r.partitions[partition]
	if !ok || node == "" {
		// Partition not assigned, use consistent hashing fallback
		return r.getNodeByHash(uint64(partition))
	}

	return node, nil
}

// OwnedPartitions returns the partitions owned by this node.
func (r *HashRing) OwnedPartitions() []int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]int, len(r.ownedParts))
	copy(result, r.ownedParts)
	return result
}

// PartitionsForNode returns the partitions owned by the given node.
func (r *HashRing) PartitionsForNode(nodeID string) []int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	parts, ok := r.nodePartMap[nodeID]
	if !ok {
		return nil
	}

	result := make([]int, len(parts))
	copy(result, parts)
	return result
}

// Nodes returns all nodes in the ring.
func (r *HashRing) Nodes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]string, len(r.nodes))
	copy(result, r.nodes)
	return result
}

// OwnsPartition returns true if this node owns the given partition.
func (r *HashRing) OwnsPartition(partition int) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, p := range r.ownedParts {
		if p == partition {
			return true
		}
	}
	return false
}

// OwnsKey returns true if this node owns the given key.
func (r *HashRing) OwnsKey(key string) bool {
	return r.OwnsPartition(r.PartitionFor(key))
}

// IsRebalancing returns true if the ring is currently rebalancing.
func (r *HashRing) IsRebalancing() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.rebalancing
}

// triggerRebalance triggers a rebalance if this node is the coordinator.
func (r *HashRing) triggerRebalance() {
	select {
	case r.rebalanceCh <- struct{}{}:
	default:
	}
}

// isCoordinator returns true if this node should coordinate rebalancing.
// The coordinator is the node with the lexicographically smallest ID.
func (r *HashRing) isCoordinator() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if len(r.nodes) == 0 {
		return false
	}

	return r.nodes[0] == r.app.platform.nodeID
}

// rebuildRing rebuilds the consistent hash ring from current membership.
func (r *HashRing) rebuildRing() {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Get current members that are running this app
	members := r.app.platform.membership.Members()
	nodes := make([]string, 0)

	for _, m := range members {
		for _, appName := range m.Apps {
			if appName == r.app.name {
				nodes = append(nodes, m.NodeID)
				break
			}
		}
	}

	// Sort nodes for consistent coordinator selection
	sort.Strings(nodes)
	r.nodes = nodes

	// Rebuild virtual nodes
	r.vnodes = make([]vnode, 0, len(nodes)*r.virtualNodes)
	for _, nodeID := range nodes {
		for i := 0; i < r.virtualNodes; i++ {
			key := fmt.Sprintf("%s-%d", nodeID, i)
			h := hashKey(key)
			r.vnodes = append(r.vnodes, vnode{hash: h, nodeID: nodeID})
		}
	}

	// Sort virtual nodes by hash
	sort.Slice(r.vnodes, func(i, j int) bool {
		return r.vnodes[i].hash < r.vnodes[j].hash
	})

	// Rebuild node partition map
	r.nodePartMap = make(map[string][]int)
	for partition, nodeID := range r.partitions {
		r.nodePartMap[nodeID] = append(r.nodePartMap[nodeID], partition)
	}

	// Update owned partitions for this node
	r.ownedParts = r.nodePartMap[r.app.platform.nodeID]

	// Update metrics
	r.app.platform.metrics.SetRingPartitionsOwned(r.app.name, len(r.ownedParts))
	r.app.platform.metrics.SetRingNodesTotal(r.app.name, len(r.nodes))
}

// getNodeByHash returns the node responsible for the given hash.
func (r *HashRing) getNodeByHash(h uint64) (string, error) {
	if len(r.vnodes) == 0 {
		return "", fmt.Errorf("no nodes in ring")
	}

	// Binary search for the first vnode with hash >= h
	idx := sort.Search(len(r.vnodes), func(i int) bool {
		return r.vnodes[i].hash >= h
	})

	// Wrap around if necessary
	if idx >= len(r.vnodes) {
		idx = 0
	}

	return r.vnodes[idx].nodeID, nil
}

// loadAssignments loads partition assignments from KV.
func (r *HashRing) loadAssignments(ctx context.Context) error {
	entry, err := r.kv.Get(ctx, "assignments")
	if err != nil {
		if err == jetstream.ErrKeyNotFound {
			// No assignments yet, that's fine
			return nil
		}
		return err
	}

	var assignment PartitionAssignment
	if err := json.Unmarshal(entry.Value(), &assignment); err != nil {
		return err
	}

	r.mu.Lock()
	r.partitions = assignment.Partitions
	r.mu.Unlock()

	return nil
}

// saveAssignments saves partition assignments to KV.
func (r *HashRing) saveAssignments(ctx context.Context, assignment PartitionAssignment) error {
	data, err := json.Marshal(assignment)
	if err != nil {
		return err
	}

	_, err = r.kv.Put(ctx, "assignments", data)
	return err
}

// membershipWatcher watches for membership changes and triggers rebalancing.
func (r *HashRing) membershipWatcher() {
	defer r.wg.Done()

	// Handle rebalance requests
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-r.rebalanceCh:
			// Always rebuild the ring first to get the latest membership
			r.rebuildRing()
			if r.isCoordinator() {
				r.doRebalance()
			}
		}
	}
}



// assignmentWatcher watches for assignment changes from the coordinator.
func (r *HashRing) assignmentWatcher() {
	defer r.wg.Done()

	watcher, err := r.kv.Watch(r.ctx, "assignments")
	if err != nil {
		return
	}
	defer watcher.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case entry := <-watcher.Updates():
			if entry == nil {
				continue
			}

			if entry.Operation() == jetstream.KeyValueDelete {
				continue
			}

			var assignment PartitionAssignment
			if err := json.Unmarshal(entry.Value(), &assignment); err != nil {
				continue
			}

			r.applyAssignments(assignment)
		}
	}
}

// applyAssignments applies new partition assignments.
func (r *HashRing) applyAssignments(assignment PartitionAssignment) {
	r.mu.Lock()
	oldPartitions := r.ownedParts
	r.partitions = assignment.Partitions

	// Rebuild node partition map
	r.nodePartMap = make(map[string][]int)
	for partition, nodeID := range r.partitions {
		r.nodePartMap[nodeID] = append(r.nodePartMap[nodeID], partition)
	}

	// Sort partitions for each node
	for nodeID := range r.nodePartMap {
		sort.Ints(r.nodePartMap[nodeID])
	}

	// Get new owned partitions
	newPartitions := r.nodePartMap[r.app.platform.nodeID]
	r.ownedParts = newPartitions
	r.mu.Unlock()

	// Determine gained and lost partitions
	gained := difference(newPartitions, oldPartitions)
	lost := difference(oldPartitions, newPartitions)

	// Update metrics
	r.app.platform.metrics.SetRingPartitionsOwned(r.app.name, len(newPartitions))

	// Call OnRelease for lost partitions first
	if len(lost) > 0 {
		r.app.platform.audit.Log(r.ctx, AuditEntry{
			Category: "ring",
			Action:   "partitions_released",
			Data:     map[string]any{"app": r.app.name, "partitions": lost},
		})

		if r.app.onRelease != nil {
			if err := r.callReleaseHook(lost); err != nil {
				r.app.platform.audit.Log(r.ctx, AuditEntry{
					Category: "ring",
					Action:   "onRelease_error",
					Data:     map[string]any{"app": r.app.name, "error": err.Error()},
				})
			}
		}

		r.app.platform.metrics.IncRingRebalanceEvents(r.app.name, "release")
	}

	// Call OnOwn for gained partitions
	if len(gained) > 0 {
		r.app.platform.audit.Log(r.ctx, AuditEntry{
			Category: "ring",
			Action:   "partitions_acquired",
			Data:     map[string]any{"app": r.app.name, "partitions": gained},
		})

		if r.app.onOwn != nil {
			if err := r.callOwnHook(gained); err != nil {
				r.app.platform.audit.Log(r.ctx, AuditEntry{
					Category: "ring",
					Action:   "onOwn_error",
					Data:     map[string]any{"app": r.app.name, "error": err.Error()},
				})
			}
		}

		r.app.platform.metrics.IncRingRebalanceEvents(r.app.name, "acquire")
	}
}

// callOwnHook calls the OnOwn hook with timeout.
func (r *HashRing) callOwnHook(partitions []int) error {
	if r.app.onOwn == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(r.ctx, r.app.platform.opts.hookTimeout)
	defer cancel()

	return r.app.onOwn(ctx, partitions)
}

// callReleaseHook calls the OnRelease hook with timeout.
func (r *HashRing) callReleaseHook(partitions []int) error {
	if r.app.onRelease == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(r.ctx, r.app.platform.opts.hookTimeout)
	defer cancel()

	return r.app.onRelease(ctx, partitions)
}

// doRebalance performs partition rebalancing (coordinator only).
func (r *HashRing) doRebalance() {
	r.mu.Lock()
	if r.rebalancing {
		r.mu.Unlock()
		return
	}
	r.rebalancing = true
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		r.rebalancing = false
		r.mu.Unlock()
	}()

	r.app.platform.audit.Log(r.ctx, AuditEntry{
		Category: "ring",
		Action:   "rebalance_started",
		Data:     map[string]any{"app": r.app.name},
	})

	// Calculate ideal partition distribution
	newAssignment := r.calculateAssignments()

	// Save new assignments
	assignment := PartitionAssignment{
		Version:    uint64(time.Now().UnixNano()),
		Partitions: newAssignment,
		UpdatedAt:  time.Now(),
		UpdatedBy:  r.app.platform.nodeID,
	}

	if err := r.saveAssignments(r.ctx, assignment); err != nil {
		r.app.platform.audit.Log(r.ctx, AuditEntry{
			Category: "ring",
			Action:   "rebalance_failed",
			Data:     map[string]any{"app": r.app.name, "error": err.Error()},
		})
		return
	}

	r.app.platform.audit.Log(r.ctx, AuditEntry{
		Category: "ring",
		Action:   "rebalance_completed",
		Data:     map[string]any{"app": r.app.name, "partitions": r.numPartitions, "nodes": len(r.nodes)},
	})

	r.app.platform.metrics.IncRingRebalanceEvents(r.app.name, "rebalance")
}

// calculateAssignments calculates optimal partition assignments.
func (r *HashRing) calculateAssignments() map[int]string {
	r.mu.RLock()
	nodes := make([]string, len(r.nodes))
	copy(nodes, r.nodes)
	currentAssignments := make(map[int]string)
	for k, v := range r.partitions {
		currentAssignments[k] = v
	}
	r.mu.RUnlock()

	if len(nodes) == 0 {
		return make(map[int]string)
	}

	newAssignment := make(map[int]string)

	// Calculate ideal partitions per node
	partsPerNode := r.numPartitions / len(nodes)
	extraPartitions := r.numPartitions % len(nodes)

	// Track how many partitions each node should have
	targetCount := make(map[string]int)
	for i, node := range nodes {
		targetCount[node] = partsPerNode
		if i < extraPartitions {
			targetCount[node]++
		}
	}

	// Track current count per node
	currentCount := make(map[string]int)
	for _, node := range currentAssignments {
		currentCount[node]++
	}

	// First pass: keep existing assignments that are still valid
	validNodes := make(map[string]bool)
	for _, node := range nodes {
		validNodes[node] = true
	}

	assignedCount := make(map[string]int)
	for partition := 0; partition < r.numPartitions; partition++ {
		if owner, ok := currentAssignments[partition]; ok && validNodes[owner] {
			// Check if this node can still hold more partitions
			if assignedCount[owner] < targetCount[owner] {
				newAssignment[partition] = owner
				assignedCount[owner]++
			}
		}
	}

	// Second pass: assign unassigned partitions using consistent hashing
	for partition := 0; partition < r.numPartitions; partition++ {
		if _, ok := newAssignment[partition]; ok {
			continue
		}

		// Use consistent hashing to pick a node
		h := hashKey(fmt.Sprintf("partition-%d", partition))

		// Find a node that still needs partitions
		for attempts := 0; attempts < len(nodes); attempts++ {
			nodeIdx := int((h + uint64(attempts)) % uint64(len(nodes)))
			node := nodes[nodeIdx]

			if assignedCount[node] < targetCount[node] {
				newAssignment[partition] = node
				assignedCount[node]++
				break
			}
		}

		// Fallback: assign to any node that needs partitions
		if _, ok := newAssignment[partition]; !ok {
			for _, node := range nodes {
				if assignedCount[node] < targetCount[node] {
					newAssignment[partition] = node
					assignedCount[node]++
					break
				}
			}
		}
	}

	return newAssignment
}

// hashKey hashes a key to a uint64.
func hashKey(key string) uint64 {
	h := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint64(h[:8])
}

// difference returns elements in a that are not in b.
func difference(a, b []int) []int {
	bSet := make(map[int]bool)
	for _, v := range b {
		bSet[v] = true
	}

	result := make([]int, 0)
	for _, v := range a {
		if !bSet[v] {
			result = append(result, v)
		}
	}
	return result
}

// RingStats contains ring statistics.
type RingStats struct {
	NumPartitions    int            `json:"num_partitions"`
	NumNodes         int            `json:"num_nodes"`
	OwnedPartitions  int            `json:"owned_partitions"`
	IsCoordinator    bool           `json:"is_coordinator"`
	IsRebalancing    bool           `json:"is_rebalancing"`
	PartitionsByNode map[string]int `json:"partitions_by_node"`
}

// WaitForNodes waits until the ring has at least the specified number of nodes.
func (r *HashRing) WaitForNodes(count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Rebuild the ring to get latest membership
		r.rebuildRing()

		r.mu.RLock()
		nodeCount := len(r.nodes)
		r.mu.RUnlock()

		if nodeCount >= count {
			// Trigger rebalance now that we have enough nodes
			r.triggerRebalance()
			return nil
		}

		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	r.mu.RLock()
	nodeCount := len(r.nodes)
	r.mu.RUnlock()
	return fmt.Errorf("timed out waiting for %d nodes, have %d", count, nodeCount)
}

// WaitForNodesExact waits until the ring has exactly the specified number of nodes.
func (r *HashRing) WaitForNodesExact(count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Rebuild the ring to get latest membership
		r.rebuildRing()

		r.mu.RLock()
		nodeCount := len(r.nodes)
		r.mu.RUnlock()

		if nodeCount == count {
			// Trigger rebalance now that we have the expected nodes
			r.triggerRebalance()
			return nil
		}

		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	r.mu.RLock()
	nodeCount := len(r.nodes)
	r.mu.RUnlock()
	return fmt.Errorf("timed out waiting for exactly %d nodes, have %d", count, nodeCount)
}

// WaitForPartitions waits until this node owns at least the specified number of partitions.
func (r *HashRing) WaitForPartitions(count int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		ownedCount := len(r.ownedParts)
		r.mu.RUnlock()

		if ownedCount >= count {
			return nil
		}

		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	r.mu.RLock()
	ownedCount := len(r.ownedParts)
	r.mu.RUnlock()
	return fmt.Errorf("timed out waiting for %d partitions, have %d", count, ownedCount)
}

// WaitForPartitionsInRange waits until this node owns partitions within the specified range.
func (r *HashRing) WaitForPartitionsInRange(min, max int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		r.mu.RLock()
		ownedCount := len(r.ownedParts)
		r.mu.RUnlock()

		if ownedCount >= min && ownedCount <= max {
			return nil
		}

		select {
		case <-r.ctx.Done():
			return r.ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	r.mu.RLock()
	ownedCount := len(r.ownedParts)
	r.mu.RUnlock()
	return fmt.Errorf("timed out waiting for partitions in range [%d, %d], have %d", min, max, ownedCount)
}

// ForceRebalance forces an immediate rebalancing of partitions.
// This should only be called for testing or administrative purposes.
func (r *HashRing) ForceRebalance() {
	r.rebuildRing()
	if r.isCoordinator() {
		r.doRebalance()
	}
}

// Stats returns current ring statistics.
func (r *HashRing) Stats() RingStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	partsByNode := make(map[string]int)
	for nodeID, parts := range r.nodePartMap {
		partsByNode[nodeID] = len(parts)
	}

	return RingStats{
		NumPartitions:    r.numPartitions,
		NumNodes:         len(r.nodes),
		OwnedPartitions:  len(r.ownedParts),
		IsCoordinator:    len(r.nodes) > 0 && r.nodes[0] == r.app.platform.nodeID,
		IsRebalancing:    r.rebalancing,
		PartitionsByNode: partsByNode,
	}
}
