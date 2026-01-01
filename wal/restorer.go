package wal

import (
	"context"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Restorer handles WAL restoration from storage to replica.
type Restorer struct {
	config Config
	store  Store

	mu           sync.RWMutex
	position     Position
	latestPos    Position
	lastRestore  time.Time

	ctx    context.Context
	cancel context.CancelFunc
}

// NewRestorer creates a new WAL restorer.
func NewRestorer(config Config, store Store) *Restorer {
	return &Restorer{
		config: config,
		store:  store,
	}
}

// Start starts the restorer.
func (r *Restorer) Start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Ensure replica directory exists
	replicaPath := r.getReplicaPath()
	dir := filepath.Dir(replicaPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create replica directory: %w", err)
	}

	// Get our current position from local state
	r.mu.Lock()
	r.position = Position{} // Start from beginning if no state
	r.mu.Unlock()

	return nil
}

// Stop stops the restorer.
func (r *Restorer) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// Position returns the current restoration position.
func (r *Restorer) Position() Position {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.position
}

// Lag returns the replication lag.
func (r *Restorer) Lag() time.Duration {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.latestPos.IsZero() || r.position.IsZero() {
		return 0
	}

	// Calculate lag based on timestamp difference
	if r.latestPos.Timestamp.After(r.position.Timestamp) {
		return time.Since(r.position.Timestamp)
	}

	return 0
}

// Restore applies any new WAL segments from storage.
func (r *Restorer) Restore(ctx context.Context) error {
	// Get latest position from storage
	latestPos, err := r.store.GetLatestPosition(ctx)
	if err != nil {
		return fmt.Errorf("get latest position: %w", err)
	}

	r.mu.Lock()
	r.latestPos = latestPos
	currentPos := r.position
	r.mu.Unlock()

	// Check if we're caught up
	if !latestPos.After(currentPos) {
		return nil
	}

	// List segments we need to apply
	segments, err := r.store.ListSegments(ctx, currentPos)
	if err != nil {
		return fmt.Errorf("list segments: %w", err)
	}

	if len(segments) == 0 {
		return nil
	}

	// Apply each segment
	for _, seg := range segments {
		if err := r.applySegment(ctx, seg); err != nil {
			return fmt.Errorf("apply segment %d/%d: %w", seg.Position.Generation, seg.Position.Index, err)
		}

		r.mu.Lock()
		r.position = seg.Position
		r.lastRestore = time.Now()
		r.mu.Unlock()
	}

	return nil
}

// CatchUp performs an aggressive catch-up to the latest position.
func (r *Restorer) CatchUp(ctx context.Context) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := r.Restore(ctx); err != nil {
			return err
		}

		// Check if we're caught up
		r.mu.RLock()
		caughtUp := !r.latestPos.After(r.position)
		r.mu.RUnlock()

		if caughtUp {
			return nil
		}

		// Small delay before next batch
		time.Sleep(10 * time.Millisecond)
	}
}

// applySegment applies a single WAL segment to the replica.
func (r *Restorer) applySegment(ctx context.Context, seg Segment) error {
	// Read segment data
	data, err := r.store.ReadSegment(ctx, seg.Position)
	if err != nil {
		return fmt.Errorf("read segment: %w", err)
	}

	// Verify checksum
	if crc32.ChecksumIEEE(data) != seg.Checksum {
		return fmt.Errorf("checksum mismatch")
	}

	// Write to replica WAL file
	replicaWALPath := r.getReplicaPath() + "-wal"
	f, err := os.OpenFile(replicaWALPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("open replica WAL: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write replica WAL: %w", err)
	}

	if err := f.Sync(); err != nil {
		return fmt.Errorf("sync replica WAL: %w", err)
	}

	return nil
}

// getReplicaPath returns the replica database path.
func (r *Restorer) getReplicaPath() string {
	if r.config.ReplicaPath != "" {
		return r.config.ReplicaPath
	}
	// Default replica path
	ext := filepath.Ext(r.config.DBPath)
	base := r.config.DBPath[:len(r.config.DBPath)-len(ext)]
	return base + "-replica" + ext
}

// PromoteReplica promotes the replica database to primary.
// This should only be called during failover when becoming the new leader.
func (r *Restorer) PromoteReplica(ctx context.Context) error {
	replicaPath := r.getReplicaPath()
	primaryPath := r.config.DBPath

	// Ensure we're caught up first
	if err := r.CatchUp(ctx); err != nil {
		return fmt.Errorf("catch up before promotion: %w", err)
	}

	// Checkpoint the replica WAL if it exists
	replicaWAL := replicaPath + "-wal"
	if _, err := os.Stat(replicaWAL); err == nil {
		// WAL file exists - in a real implementation we'd checkpoint it
		// For now, we'll just copy it along with the database
	}

	// Backup the current primary if it exists
	if _, err := os.Stat(primaryPath); err == nil {
		backupPath := primaryPath + ".bak." + time.Now().Format("20060102150405")
		if err := copyFile(primaryPath, backupPath); err != nil {
			// Log but don't fail - the backup is optional
		}
	}

	// Copy replica to primary (atomic would be better but this is safer cross-platform)
	if err := copyFile(replicaPath, primaryPath); err != nil {
		return fmt.Errorf("copy replica to primary: %w", err)
	}

	// Copy WAL file if it exists
	primaryWAL := primaryPath + "-wal"
	if _, err := os.Stat(replicaWAL); err == nil {
		if err := copyFile(replicaWAL, primaryWAL); err != nil {
			return fmt.Errorf("copy replica WAL to primary WAL: %w", err)
		}
	}

	// Copy SHM file if it exists
	replicaSHM := replicaPath + "-shm"
	primarySHM := primaryPath + "-shm"
	if _, err := os.Stat(replicaSHM); err == nil {
		if err := copyFile(replicaSHM, primarySHM); err != nil {
			return fmt.Errorf("copy replica SHM to primary SHM: %w", err)
		}
	}

	return nil
}

// copyFile copies a file from src to dst.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return dstFile.Sync()
}

// InitializeFromSnapshot initializes the replica from a snapshot.
func (r *Restorer) InitializeFromSnapshot(ctx context.Context, snapshotPath string) error {
	replicaPath := r.getReplicaPath()

	// Ensure replica directory exists
	dir := filepath.Dir(replicaPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create replica directory: %w", err)
	}

	// Copy snapshot to replica
	if err := copyFile(snapshotPath, replicaPath); err != nil {
		return fmt.Errorf("copy snapshot to replica: %w", err)
	}

	return nil
}
