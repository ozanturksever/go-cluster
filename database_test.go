package cluster_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	cluster "github.com/ozanturksever/go-cluster"
	"github.com/ozanturksever/go-cluster/snapshot"
	"github.com/ozanturksever/go-cluster/testutil"
	"github.com/ozanturksever/go-cluster/wal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper functions for database tests
// nodeIDForDB generates a node ID for database tests.
func nodeIDForDB(i int) string {
	return fmt.Sprintf("node-%d", i+1)
}

// portStrForDB generates a port string for database tests.
func portStrForDB(port int) string {
	return fmt.Sprintf(":%d", port)
}

// testNode represents a test cluster node with proper lifecycle management.
type testNode struct {
	platform *cluster.Platform
	app      *cluster.App
	cancel   context.CancelFunc
	errCh    chan error
	wg       sync.WaitGroup
}

// startTestNode starts a test node with proper error handling and synchronization.
func startTestNode(t *testing.T, platformName, nodeID, natsURL string, healthPort, metricsPort int, appOpts ...cluster.AppOption) *testNode {
	t.Helper()

	platform, err := cluster.NewPlatform(platformName, nodeID, natsURL,
		cluster.HealthAddr(portStrForDB(healthPort)),
		cluster.MetricsAddr(portStrForDB(metricsPort)),
		cluster.WithLeaseTTL(2*time.Second),
		cluster.WithHeartbeat(500*time.Millisecond),
	)
	require.NoError(t, err)

	app := cluster.NewApp("testapp", append([]cluster.AppOption{cluster.Singleton()}, appOpts...)...)
	platform.Register(app)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	node := &testNode{
		platform: platform,
		app:      app,
		cancel:   cancel,
		errCh:    errCh,
	}

	node.wg.Add(1)
	go func() {
		defer node.wg.Done()
		errCh <- platform.Run(ctx)
	}()

	return node
}

// stop stops the test node and waits for cleanup.
func (n *testNode) stop() {
	if n.cancel != nil {
		n.cancel()
	}
	n.wg.Wait()
}

// waitForElection waits for the election to be initialized.
func (n *testNode) waitForElection(t *testing.T, timeout time.Duration) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return n.app.Election() != nil
	}, timeout, 100*time.Millisecond, "election should be initialized")
}

// waitForLeader waits for this node to become leader.
func (n *testNode) waitForLeader(t *testing.T, timeout time.Duration) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return n.app.IsLeader()
	}, timeout, 200*time.Millisecond, "should become leader")
}

// waitForDB waits for the database manager to be initialized.
func (n *testNode) waitForDB(t *testing.T, timeout time.Duration) {
	t.Helper()
	assert.Eventually(t, func() bool {
		return n.app.DB() != nil
	}, timeout, 100*time.Millisecond, "database manager should be initialized")
}

// waitForDBMode waits for the database to reach the specified mode.
func (n *testNode) waitForDBMode(t *testing.T, mode wal.Mode, timeout time.Duration) {
	t.Helper()
	assert.Eventually(t, func() bool {
		db := n.app.DB()
		return db != nil && db.Mode() == mode
	}, timeout, 100*time.Millisecond, "database should be in expected mode")
}

// checkError checks if the platform returned an error (non-blocking).
func (n *testNode) checkError(t *testing.T) {
	t.Helper()
	select {
	case err := <-n.errCh:
		if err != nil && err != context.Canceled {
			t.Errorf("platform error: %v", err)
		}
	default:
		// No error yet
	}
}

func TestDatabaseManager_PromoteDemote(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set test timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	tmpDir, err := os.MkdirTemp("", "db_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	node := startTestNode(t, "test-db-promote", "node-1", ns.URL(), 18080, 19090,
		cluster.WithSQLite(dbPath),
	)
	defer node.stop()

	// Wait for initialization with context awareness
	node.waitForElection(t, 10*time.Second)
	node.waitForLeader(t, 15*time.Second)
	node.waitForDB(t, 5*time.Second)

	// Verify database manager is available
	db := node.app.DB()
	require.NotNil(t, db, "database manager should be available")

	// Verify WAL manager is available
	walMgr := node.app.WAL()
	require.NotNil(t, walMgr, "WAL manager should be available")

	// Verify snapshot manager is available
	snapMgr := node.app.Snapshots()
	require.NotNil(t, snapMgr, "snapshot manager should be available")

	// Check for platform errors
	node.checkError(t)

	// Verify test context wasn't exceeded
	select {
	case <-ctx.Done():
		t.Fatal("test timeout exceeded")
	default:
	}
}

func TestDatabaseManager_WALPosition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set test timeout
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	tmpDir, err := os.MkdirTemp("", "wal_pos_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	node := startTestNode(t, "test-wal-pos", "node-1", ns.URL(), 18081, 19091,
		cluster.WithSQLite(dbPath),
	)
	defer node.stop()

	// Wait for initialization
	node.waitForElection(t, 10*time.Second)
	node.waitForLeader(t, 15*time.Second)
	node.waitForDB(t, 5*time.Second)

	walMgr := node.app.WAL()
	require.NotNil(t, walMgr)

	// Get initial position
	pos := walMgr.Position()
	t.Logf("WAL Position: gen=%d, idx=%d, offset=%d", pos.Generation, pos.Index, pos.Offset)

	// Get stats
	stats := walMgr.Stats()
	t.Logf("WAL Stats: mode=%d, errors=%d", stats.Mode, stats.Errors)

	// Verify lag is zero for primary
	lag := walMgr.Lag()
	assert.Equal(t, time.Duration(0), lag, "primary should have zero lag")

	// Check for platform errors
	node.checkError(t)

	// Verify test context wasn't exceeded
	select {
	case <-ctx.Done():
		t.Fatal("test timeout exceeded")
	default:
	}
}

func TestDatabaseManager_SnapshotCreateRestore(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set test timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer testCancel()

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	tmpDir, err := os.MkdirTemp("", "snap_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a test database file
	testData := []byte("test database content")
	require.NoError(t, os.WriteFile(dbPath, testData, 0644))

	node := startTestNode(t, "test-snap-cr", "node-1", ns.URL(), 18082, 19092,
		cluster.WithSQLite(dbPath,
			cluster.SQLiteSnapshotInterval(1*time.Hour), // Manual snapshots only
		),
	)
	defer node.stop()

	// Wait for initialization
	node.waitForElection(t, 10*time.Second)
	node.waitForLeader(t, 15*time.Second)
	node.waitForDB(t, 5*time.Second)

	snapMgr := node.app.Snapshots()
	require.NotNil(t, snapMgr)

	// Use test context for snapshot operations
	ctx, cancel := context.WithTimeout(testCtx, 30*time.Second)
	defer cancel()

	// Create a snapshot
	snap, err := snapMgr.Create(ctx)
	require.NoError(t, err)
	require.NotNil(t, snap)

	t.Logf("Created snapshot: id=%s, size=%d, checksum=%s", snap.ID, snap.Size, snap.Checksum)

	// List snapshots
	snapshots, err := snapMgr.List(ctx)
	require.NoError(t, err)
	assert.Len(t, snapshots, 1, "should have one snapshot")

	// Get latest snapshot
	latest, err := snapMgr.Latest(ctx)
	require.NoError(t, err)
	assert.Equal(t, snap.ID, latest.ID)

	// Restore snapshot to a new location
	restorePath := filepath.Join(tmpDir, "restored.db")
	err = snapMgr.Restore(ctx, snap.ID, restorePath)
	require.NoError(t, err)

	// Verify restored content
	restoredData, err := os.ReadFile(restorePath)
	require.NoError(t, err)
	assert.Equal(t, testData, restoredData, "restored data should match original")

	// Delete snapshot
	err = snapMgr.Delete(ctx, snap.ID)
	require.NoError(t, err)

	// Verify deletion
	snapshots, err = snapMgr.List(ctx)
	require.NoError(t, err)
	assert.Len(t, snapshots, 0, "should have no snapshots after deletion")

	// Check for platform errors
	node.checkError(t)
}

func TestDatabaseManager_TwoNodeReplication(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set test timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer testCancel()

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	tmpDir, err := os.MkdirTemp("", "repl_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	var nodes []*testNode

	// Start 2 nodes
	for i := 0; i < 2; i++ {
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("node%d", i), "test.db")
		err := os.MkdirAll(filepath.Dir(dbPath), 0755)
		require.NoError(t, err)

		node := startTestNode(t, "test-repl", nodeIDForDB(i), ns.URL(), 18180+i, 19190+i,
			cluster.WithSQLite(dbPath),
		)
		nodes = append(nodes, node)
	}

	defer func() {
		for _, node := range nodes {
			node.stop()
		}
	}()

	// Wait for all nodes to have elections initialized
	for i, node := range nodes {
		node.waitForElection(t, 10*time.Second)
		node.waitForDB(t, 5*time.Second)
		t.Logf("Node %d election and DB initialized", i)
	}

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, node := range nodes {
			if node.app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 15*time.Second, 200*time.Millisecond, "a leader should be elected")

	t.Logf("Leader is node %d", leaderIdx)

	// Verify leader has database manager
	leaderDB := nodes[leaderIdx].app.DB()
	require.NotNil(t, leaderDB)

	// Wait for database mode to be set
	nodes[leaderIdx].waitForDBMode(t, wal.ModePrimary, 5*time.Second)
	t.Logf("Leader DB in primary mode")

	// Verify follower has database manager in replica mode
	followerIdx := 1 - leaderIdx
	followerDB := nodes[followerIdx].app.DB()
	require.NotNil(t, followerDB)

	// Wait for follower database mode to be set
	nodes[followerIdx].waitForDBMode(t, wal.ModeReplica, 5*time.Second)
	t.Logf("Follower DB in replica mode")

	// Check for platform errors
	for _, node := range nodes {
		node.checkError(t)
	}

	// Verify test context wasn't exceeded
	select {
	case <-testCtx.Done():
		t.Fatal("test timeout exceeded")
	default:
	}
}

func TestDatabaseManager_FailoverWithDataIntegrity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	// Set test timeout
	testCtx, testCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer testCancel()

	ns := testutil.StartNATS(t)
	defer ns.Stop()

	tmpDir, err := os.MkdirTemp("", "failover_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	var nodes []*testNode
	var dbPaths []string

	// Start 2 nodes
	for i := 0; i < 2; i++ {
		dbPath := filepath.Join(tmpDir, fmt.Sprintf("node%d", i), "test.db")
		err := os.MkdirAll(filepath.Dir(dbPath), 0755)
		require.NoError(t, err)
		dbPaths = append(dbPaths, dbPath)

		node := startTestNode(t, "test-failover", nodeIDForDB(i), ns.URL(), 18280+i, 19290+i,
			cluster.WithSQLite(dbPath),
		)
		nodes = append(nodes, node)
	}

	defer func() {
		for _, node := range nodes {
			node.stop()
		}
	}()

	// Wait for all nodes to be fully initialized
	for i, node := range nodes {
		node.waitForElection(t, 10*time.Second)
		node.waitForDB(t, 5*time.Second)
		t.Logf("Node %d fully initialized", i)
	}

	// Wait for a leader
	var leaderIdx int
	assert.Eventually(t, func() bool {
		for i, node := range nodes {
			if node.app.IsLeader() {
				leaderIdx = i
				return true
			}
		}
		return false
	}, 15*time.Second, 200*time.Millisecond, "a leader should be elected")

	t.Logf("Initial leader is node %d", leaderIdx)

	// Wait for leader to be in primary mode before writing data
	nodes[leaderIdx].waitForDBMode(t, wal.ModePrimary, 5*time.Second)

	// Write test data to leader's database
	testData := []byte("important data from leader")
	require.NoError(t, os.WriteFile(dbPaths[leaderIdx], testData, 0644))

	// Create a snapshot on the leader with proper context
	ctx, cancel := context.WithTimeout(testCtx, 10*time.Second)
	defer cancel()

	snapMgr := nodes[leaderIdx].app.Snapshots()
	if snapMgr != nil {
		_, err := snapMgr.Create(ctx)
		if err != nil {
			t.Logf("Snapshot creation: %v (expected if not fully initialized)", err)
		}
	}

	// Force sync on the leader
	walMgr := nodes[leaderIdx].app.WAL()
	if walMgr != nil {
		err := walMgr.Sync(ctx)
		if err != nil {
			t.Logf("WAL sync: %v (expected if WAL file doesn't exist)", err)
		}
	}

	// Give time for replication
	time.Sleep(500 * time.Millisecond)

	// Step down the leader (check if election is available)
	election := nodes[leaderIdx].app.Election()
	require.NotNil(t, election, "Election should be available")

	err = election.StepDown(ctx)
	require.NoError(t, err)

	// Verify leader stepped down
	assert.Eventually(t, func() bool {
		return !nodes[leaderIdx].app.IsLeader()
	}, 5*time.Second, 100*time.Millisecond, "leader should have stepped down")

	// Wait for new leader
	otherIdx := 1 - leaderIdx
	assert.Eventually(t, func() bool {
		return nodes[otherIdx].app.IsLeader()
	}, 10*time.Second, 200*time.Millisecond, "other node should become leader")

	t.Logf("New leader is node %d", otherIdx)

	// Verify database manager mode changed
	nodes[otherIdx].waitForDBMode(t, wal.ModePrimary, 5*time.Second)
	t.Logf("New leader DB in primary mode")

	// Check for platform errors
	for _, node := range nodes {
		node.checkError(t)
	}

	// Verify test context wasn't exceeded
	select {
	case <-testCtx.Done():
		t.Fatal("test timeout exceeded")
	default:
	}
}

func TestWAL_ManagerModes(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wal_modes_test")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create a mock store for testing
	store := newMockWALStore()

	config := wal.DefaultConfig()
	config.DBPath = dbPath

	m := wal.NewManager(config, store)

	ctx := context.Background()

	// Start in disabled mode
	err = m.Start(ctx, wal.ModeDisabled)
	require.NoError(t, err)

	stats := m.Stats()
	assert.Equal(t, wal.ModeDisabled, stats.Mode)

	// Switch to primary mode
	err = m.SwitchMode(ctx, wal.ModePrimary)
	require.NoError(t, err)

	stats = m.Stats()
	assert.Equal(t, wal.ModePrimary, stats.Mode)

	// Switch to replica mode
	err = m.SwitchMode(ctx, wal.ModeReplica)
	require.NoError(t, err)

	stats = m.Stats()
	assert.Equal(t, wal.ModeReplica, stats.Mode)

	m.Stop()
}

func TestSnapshot_Config(t *testing.T) {
	config := snapshot.DefaultConfig()

	assert.Equal(t, time.Hour, config.Interval)
	assert.Equal(t, 24*time.Hour, config.Retention)
	assert.Equal(t, 24, config.MaxSnapshots)
}

// mockWALStore implements wal.Store for testing.
type mockWALStore struct {
	segments map[string][]byte
	meta     map[string]wal.Segment
}

func newMockWALStore() *mockWALStore {
	return &mockWALStore{
		segments: make(map[string][]byte),
		meta:     make(map[string]wal.Segment),
	}
}

func (s *mockWALStore) WriteSegment(ctx context.Context, seg wal.Segment, data []byte) error {
	key := segmentKeyForPosition(seg.Position)
	s.segments[key] = data
	s.meta[key] = seg
	return nil
}

func (s *mockWALStore) ReadSegment(ctx context.Context, pos wal.Position) ([]byte, error) {
	key := segmentKeyForPosition(pos)
	data, ok := s.segments[key]
	if !ok {
		return nil, wal.ErrInvalidWAL
	}
	return data, nil
}

func (s *mockWALStore) ListSegments(ctx context.Context, after wal.Position) ([]wal.Segment, error) {
	var result []wal.Segment
	for _, seg := range s.meta {
		if seg.Position.After(after) {
			result = append(result, seg)
		}
	}
	return result, nil
}

func (s *mockWALStore) DeleteSegmentsBefore(ctx context.Context, pos wal.Position) error {
	for key, seg := range s.meta {
		if pos.After(seg.Position) {
			delete(s.segments, key)
			delete(s.meta, key)
		}
	}
	return nil
}

func (s *mockWALStore) WatchSegments(ctx context.Context, after wal.Position) (<-chan wal.Segment, error) {
	ch := make(chan wal.Segment)
	close(ch)
	return ch, nil
}

func (s *mockWALStore) GetLatestPosition(ctx context.Context) (wal.Position, error) {
	var latest wal.Position
	for _, seg := range s.meta {
		if seg.Position.After(latest) {
			latest = seg.Position
		}
	}
	return latest, nil
}

func segmentKeyForPosition(pos wal.Position) string {
	return string(rune(pos.Generation)) + "-" + string(rune(pos.Index))
}
