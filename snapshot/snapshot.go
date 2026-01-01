// Package snapshot provides database snapshot management via NATS Object Store.
package snapshot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// Errors for snapshot operations.
var (
	ErrSnapshotNotFound = errors.New("snapshot not found")
	ErrInvalidSnapshot  = errors.New("invalid snapshot")
	ErrRestoreFailed    = errors.New("restore failed")
)

// Snapshot represents a database snapshot.
type Snapshot struct {
	ID        string    `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	Size      int64     `json:"size"`
	Checksum  string    `json:"checksum"`
	NodeID    string    `json:"node_id"`
	Metadata  map[string]string `json:"metadata,omitempty"`
}

// Config configures the snapshot manager.
type Config struct {
	// DBPath is the path to the database file.
	DBPath string

	// Interval is how often to create snapshots.
	Interval time.Duration

	// Retention is how long to keep snapshots.
	Retention time.Duration

	// MaxSnapshots is the maximum number of snapshots to keep.
	MaxSnapshots int

	// MaxBytes is the maximum size of the snapshot object store.
	// Defaults to 100MB if not set.
	MaxBytes int64
}

// DefaultConfig returns a default snapshot configuration.
func DefaultConfig() Config {
	return Config{
		Interval:     time.Hour,
		Retention:    24 * time.Hour,
		MaxSnapshots: 24,
		MaxBytes:     100 * 1024 * 1024, // 100MB default
	}
}

// Manager manages database snapshots.
type Manager struct {
	config   Config
	objStore jetstream.ObjectStore
	nodeID   string

	mu           sync.RWMutex
	lastSnapshot time.Time

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewManager creates a new snapshot manager.
func NewManager(js jetstream.JetStream, platform, app, nodeID string, config Config) (*Manager, error) {
	return NewManagerWithContext(context.Background(), js, platform, app, nodeID, config)
}

// NewManagerWithContext creates a new snapshot manager with context.
func NewManagerWithContext(ctx context.Context, js jetstream.JetStream, platform, app, nodeID string, config Config) (*Manager, error) {
	bucketName := fmt.Sprintf("%s_%s_snapshots", platform, app)

	// Use configured max bytes or default to 100MB (reasonable for tests and small deployments)
	maxBytes := config.MaxBytes
	if maxBytes == 0 {
		maxBytes = 100 * 1024 * 1024 // 100MB default
	}

	// Create or get the object store bucket with timeout
	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	objStore, err := js.CreateOrUpdateObjectStore(createCtx, jetstream.ObjectStoreConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("Snapshots for %s/%s", platform, app),
		MaxBytes:    maxBytes,
		Storage:     jetstream.FileStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("create object store: %w", err)
	}

	return &Manager{
		config:   config,
		objStore: objStore,
		nodeID:   nodeID,
	}, nil
}

// Start starts the snapshot manager with automatic snapshots.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx, m.cancel = context.WithCancel(ctx)

	if m.config.Interval > 0 {
		m.wg.Add(1)
		go m.autoSnapshotLoop()
	}

	return nil
}

// Stop stops the snapshot manager.
func (m *Manager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
}

// autoSnapshotLoop creates automatic snapshots.
func (m *Manager) autoSnapshotLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if _, err := m.Create(m.ctx); err != nil {
				// Log error but continue
				continue
			}

			// Cleanup old snapshots
			m.Cleanup(m.ctx)
		}
	}
}

// Create creates a new snapshot of the database.
func (m *Manager) Create(ctx context.Context) (*Snapshot, error) {
	dbPath := m.config.DBPath

	// Check if database exists
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil, fmt.Errorf("stat database: %w", err)
	}

	// Generate snapshot ID
	id := generateSnapshotID()

	// Calculate checksum and read data
	data, checksum, err := readFileWithChecksum(dbPath)
	if err != nil {
		return nil, fmt.Errorf("read database: %w", err)
	}

	snapshot := &Snapshot{
		ID:        id,
		CreatedAt: time.Now(),
		Size:      info.Size(),
		Checksum:  checksum,
		NodeID:    m.nodeID,
	}

	// Store metadata
	meta, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}

	// Store snapshot
	key := snapshotKey(id)
	_, err = m.objStore.Put(ctx, jetstream.ObjectMeta{
		Name:        key,
		Description: string(meta),
	}, strings.NewReader(string(data)))
	if err != nil {
		return nil, fmt.Errorf("store snapshot: %w", err)
	}

	m.mu.Lock()
	m.lastSnapshot = time.Now()
	m.mu.Unlock()

	return snapshot, nil
}

// Latest returns the latest snapshot.
func (m *Manager) Latest(ctx context.Context) (*Snapshot, error) {
	snapshots, err := m.List(ctx)
	if err != nil {
		return nil, err
	}

	if len(snapshots) == 0 {
		return nil, ErrSnapshotNotFound
	}

	return &snapshots[len(snapshots)-1], nil
}

// Get retrieves a snapshot by ID.
func (m *Manager) Get(ctx context.Context, id string) (*Snapshot, error) {
	key := snapshotKey(id)

	info, err := m.objStore.GetInfo(ctx, key)
	if err != nil {
		return nil, ErrSnapshotNotFound
	}

	var snapshot Snapshot
	if info.Description != "" {
		if err := json.Unmarshal([]byte(info.Description), &snapshot); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	} else {
		snapshot.ID = id
		snapshot.Size = int64(info.Size)
	}

	return &snapshot, nil
}

// List lists all snapshots.
func (m *Manager) List(ctx context.Context) ([]Snapshot, error) {
	objects, err := m.objStore.List(ctx)
	if err != nil {
		// NATS returns an error when there are no objects - treat as empty list
		if strings.Contains(err.Error(), "no objects found") {
			return []Snapshot{}, nil
		}
		return nil, fmt.Errorf("list objects: %w", err)
	}

	var snapshots []Snapshot
	for _, obj := range objects {
		if !strings.HasPrefix(obj.Name, "snap/") {
			continue
		}

		var snapshot Snapshot
		if obj.Description != "" {
			if err := json.Unmarshal([]byte(obj.Description), &snapshot); err != nil {
				// Parse ID from key
				snapshot.ID = strings.TrimPrefix(obj.Name, "snap/")
				snapshot.Size = int64(obj.Size)
			}
		} else {
			snapshot.ID = strings.TrimPrefix(obj.Name, "snap/")
			snapshot.Size = int64(obj.Size)
		}

		snapshots = append(snapshots, snapshot)
	}

	// Sort by creation time
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].CreatedAt.Before(snapshots[j].CreatedAt)
	})

	return snapshots, nil
}

// Restore restores a snapshot to the given destination.
func (m *Manager) Restore(ctx context.Context, id, destPath string) error {
	key := snapshotKey(id)

	// Get snapshot data
	data, err := m.objStore.GetBytes(ctx, key)
	if err != nil {
		return fmt.Errorf("get snapshot: %w", err)
	}

	// Get metadata for checksum verification
	info, err := m.objStore.GetInfo(ctx, key)
	if err != nil {
		return fmt.Errorf("get snapshot info: %w", err)
	}

	var snapshot Snapshot
	if info.Description != "" {
		if err := json.Unmarshal([]byte(info.Description), &snapshot); err == nil {
			// Verify checksum
			if snapshot.Checksum != "" {
				h := sha256.Sum256(data)
				actualChecksum := hex.EncodeToString(h[:])
				if actualChecksum != snapshot.Checksum {
					return fmt.Errorf("checksum mismatch: expected %s, got %s", snapshot.Checksum, actualChecksum)
				}
			}
		}
	}

	// Ensure destination directory exists
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}

	// Write snapshot to destination
	if err := os.WriteFile(destPath, data, 0644); err != nil {
		return fmt.Errorf("write snapshot: %w", err)
	}

	return nil
}

// Delete deletes a snapshot.
func (m *Manager) Delete(ctx context.Context, id string) error {
	key := snapshotKey(id)
	return m.objStore.Delete(ctx, key)
}

// Cleanup removes old snapshots based on retention policy.
func (m *Manager) Cleanup(ctx context.Context) error {
	snapshots, err := m.List(ctx)
	if err != nil {
		return err
	}

	cutoff := time.Now().Add(-m.config.Retention)

	// Delete by retention
	for _, snap := range snapshots {
		if snap.CreatedAt.Before(cutoff) {
			m.Delete(ctx, snap.ID)
		}
	}

	// Delete by count
	snapshots, _ = m.List(ctx)
	if len(snapshots) > m.config.MaxSnapshots {
		toDelete := len(snapshots) - m.config.MaxSnapshots
		for i := 0; i < toDelete; i++ {
			m.Delete(ctx, snapshots[i].ID)
		}
	}

	return nil
}

// LastSnapshotTime returns when the last snapshot was created.
func (m *Manager) LastSnapshotTime() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastSnapshot
}

// snapshotKey returns the object key for a snapshot.
func snapshotKey(id string) string {
	return "snap/" + id
}

// generateSnapshotID generates a unique snapshot ID.
func generateSnapshotID() string {
	return time.Now().UTC().Format("20060102T150405Z")
}

// readFileWithChecksum reads a file and returns its contents and checksum.
func readFileWithChecksum(path string) ([]byte, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer f.Close()

	h := sha256.New()
	data, err := io.ReadAll(io.TeeReader(f, h))
	if err != nil {
		return nil, "", err
	}

	checksum := hex.EncodeToString(h.Sum(nil))
	return data, checksum, nil
}
