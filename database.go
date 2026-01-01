package cluster

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ozanturksever/go-cluster/snapshot"
	"github.com/ozanturksever/go-cluster/wal"
)

// DatabaseManager manages SQLite database replication for an app.
type DatabaseManager struct {
	app *App

	walManager      *wal.Manager
	snapshotManager *snapshot.Manager

	mu   sync.RWMutex
	mode wal.Mode

	ctx    context.Context
	cancel context.CancelFunc
}

// WALManager provides access to the WAL replication functionality.
type WALManager struct {
	db *DatabaseManager
}

// SnapshotManager provides access to snapshot functionality.
type SnapshotManager struct {
	db *DatabaseManager
}

// NewDatabaseManager creates a new database manager for the app.
func NewDatabaseManager(app *App) (*DatabaseManager, error) {
	if app.sqlitePath == "" {
		return nil, nil // SQLite not configured
	}

	p := app.platform

	// Create WAL store
	walStore, err := wal.NewNATSStore(p.js, p.name, app.name)
	if err != nil {
		return nil, fmt.Errorf("create WAL store: %w", err)
	}

	// Configure WAL
	walConfig := wal.DefaultConfig()
	walConfig.DBPath = app.sqlitePath
	walConfig.ReplicaPath = app.replicaPath
	if app.retention > 0 {
		walConfig.RetentionPeriod = app.retention
	}

	// Create WAL manager
	walManager := wal.NewManager(walConfig, walStore)

	// Configure snapshots
	snapshotConfig := snapshot.DefaultConfig()
	snapshotConfig.DBPath = app.sqlitePath
	if app.snapshotInterval > 0 {
		snapshotConfig.Interval = app.snapshotInterval
	}
	if app.retention > 0 {
		snapshotConfig.Retention = app.retention
	}

	// Create snapshot manager
	snapshotManager, err := snapshot.NewManager(p.js, p.name, app.name, p.nodeID, snapshotConfig)
	if err != nil {
		return nil, fmt.Errorf("create snapshot manager: %w", err)
	}

	return &DatabaseManager{
		app:             app,
		walManager:      walManager,
		snapshotManager: snapshotManager,
		mode:            wal.ModeDisabled,
	}, nil
}

// Start starts the database manager.
func (d *DatabaseManager) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(ctx)

	// Log database manager start
	d.app.platform.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "manager_started",
		Data:     map[string]any{"app": d.app.name, "db_path": d.app.sqlitePath},
	})

	return nil
}

// Stop stops the database manager.
func (d *DatabaseManager) Stop() {
	if d.cancel != nil {
		d.cancel()
	}

	if d.walManager != nil {
		d.walManager.Stop()
	}

	if d.snapshotManager != nil {
		d.snapshotManager.Stop()
	}
}

// SetMode sets the WAL replication mode.
func (d *DatabaseManager) SetMode(ctx context.Context, mode wal.Mode) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.mode == mode {
		return nil
	}

	// Stop current mode
	if d.walManager != nil {
		d.walManager.Stop()
	}
	if d.snapshotManager != nil {
		d.snapshotManager.Stop()
	}

	// Start new mode
	if d.walManager != nil {
		if err := d.walManager.Start(ctx, mode); err != nil {
			return fmt.Errorf("start WAL manager: %w", err)
		}
	}

	// Start snapshots only in primary mode
	if mode == wal.ModePrimary && d.snapshotManager != nil {
		if err := d.snapshotManager.Start(ctx); err != nil {
			return fmt.Errorf("start snapshot manager: %w", err)
		}
	}

	d.mode = mode
	return nil
}

// Mode returns the current WAL mode.
func (d *DatabaseManager) Mode() wal.Mode {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.mode
}

// IsReady returns true if the database manager is ready to serve requests.
func (d *DatabaseManager) IsReady() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.mode != wal.ModeDisabled
}

// WAL returns the WAL manager interface.
func (d *DatabaseManager) WAL() *WALManager {
	return &WALManager{db: d}
}

// Snapshots returns the snapshot manager interface.
func (d *DatabaseManager) Snapshots() *SnapshotManager {
	return &SnapshotManager{db: d}
}

// Promote promotes the replica to primary during failover.
// Handles both initial promotion (ModeDisabled → ModePrimary) and failover (ModeReplica → ModePrimary).
func (d *DatabaseManager) Promote(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.app.platform

	// Log promotion started
	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "promotion_started",
		Data:     map[string]any{"app": d.app.name, "current_mode": d.mode},
	})

	// Handle based on current mode
	switch d.mode {
	case wal.ModePrimary:
		// Already primary, nothing to do
		return nil

	case wal.ModeReplica:
		// Failover scenario: catch up and promote
		p.audit.Log(ctx, AuditEntry{
			Category: "database",
			Action:   "wal_catchup_started",
			Data:     map[string]any{"app": d.app.name},
		})

		// First, catch up to the latest WAL position
		if d.walManager != nil {
			if err := d.walManager.CatchUp(ctx); err != nil {
				p.audit.Log(ctx, AuditEntry{
					Category: "database",
					Action:   "wal_catchup_failed",
					Data:     map[string]any{"app": d.app.name, "error": err.Error()},
				})
				return fmt.Errorf("catch up WAL: %w", err)
			}
		}

		p.audit.Log(ctx, AuditEntry{
			Category: "database",
			Action:   "wal_catchup_completed",
			Data:     map[string]any{"app": d.app.name, "position": d.walManager.Position()},
		})

		// Stop replica mode
		d.walManager.Stop()

	case wal.ModeDisabled:
		// Initial leader startup - nothing to catch up
	}

	// Verify database before promotion
	if err := d.verifyDatabase(ctx); err != nil {
		p.audit.Log(ctx, AuditEntry{
			Category: "database",
			Action:   "verification_failed",
			Data:     map[string]any{"app": d.app.name, "error": err.Error()},
		})
		return fmt.Errorf("database verification failed: %w", err)
	}

	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "database_verified",
		Data:     map[string]any{"app": d.app.name},
	})

	// Start WAL manager in primary mode
	if d.walManager != nil {
		if err := d.walManager.Start(ctx, wal.ModePrimary); err != nil {
			return fmt.Errorf("start WAL manager in primary mode: %w", err)
		}
	}

	// Start snapshot manager
	if d.snapshotManager != nil {
		if err := d.snapshotManager.Start(ctx); err != nil {
			return fmt.Errorf("start snapshot manager: %w", err)
		}
	}

	d.mode = wal.ModePrimary

	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "promotion_completed",
		Data:     map[string]any{"app": d.app.name, "mode": "primary"},
	})

	return nil
}

// Demote demotes the primary to replica.
// Handles both initial standby (ModeDisabled → ModeReplica) and step-down (ModePrimary → ModeReplica).
func (d *DatabaseManager) Demote(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.app.platform

	// Log demotion started
	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "demotion_started",
		Data:     map[string]any{"app": d.app.name, "current_mode": d.mode},
	})

	// Handle based on current mode
	switch d.mode {
	case wal.ModeReplica:
		// Already replica, nothing to do
		return nil

	case wal.ModePrimary:
		// Step-down scenario: stop primary services
		if d.walManager != nil {
			// Final sync before stepping down
			if err := d.walManager.Sync(ctx); err != nil {
				// Log but continue - best effort
				p.audit.Log(ctx, AuditEntry{
					Category: "database",
					Action:   "final_sync_warning",
					Data:     map[string]any{"app": d.app.name, "error": err.Error()},
				})
			}
			d.walManager.Stop()
		}
		if d.snapshotManager != nil {
			d.snapshotManager.Stop()
		}

	case wal.ModeDisabled:
		// Initial standby startup - nothing to stop
	}

	// Bootstrap from snapshot if available (for new replicas)
	if d.mode == wal.ModeDisabled {
		if err := d.bootstrapFromSnapshot(ctx); err != nil {
			// Log but continue - replica can still work without snapshot bootstrap
			p.audit.Log(ctx, AuditEntry{
				Category: "database",
				Action:   "snapshot_bootstrap_skipped",
				Data:     map[string]any{"app": d.app.name, "reason": err.Error()},
			})
		}
	}

	// Start WAL manager in replica mode
	if d.walManager != nil {
		if err := d.walManager.Start(ctx, wal.ModeReplica); err != nil {
			return fmt.Errorf("start WAL manager in replica mode: %w", err)
		}
	}

	d.mode = wal.ModeReplica

	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "demotion_completed",
		Data:     map[string]any{"app": d.app.name, "mode": "replica"},
	})

	return nil
}

// verifyDatabase verifies the database is in a valid state for promotion.
func (d *DatabaseManager) verifyDatabase(ctx context.Context) error {
	dbPath := d.app.sqlitePath

	// Check if database file exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		// Database doesn't exist yet, that's OK for initial leader
		return nil
	}

	// Verify WAL mode by checking the file header
	if err := wal.EnsureWALMode(dbPath); err != nil {
		return fmt.Errorf("WAL mode check failed: %w", err)
	}

	// Verify database file is readable and has valid SQLite header
	if err := d.verifyDatabaseHeader(dbPath); err != nil {
		return fmt.Errorf("database header check failed: %w", err)
	}

	return nil
}

// verifyDatabaseHeader checks the SQLite database file header.
func (d *DatabaseManager) verifyDatabaseHeader(dbPath string) error {
	f, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer f.Close()

	// Read the first 16 bytes - SQLite header string
	header := make([]byte, 16)
	n, err := f.Read(header)
	if err != nil {
		return err
	}
	if n < 16 {
		return fmt.Errorf("database file too small")
	}

	// SQLite databases start with "SQLite format 3\000"
	expected := "SQLite format 3\000"
	if string(header) != expected {
		return fmt.Errorf("invalid SQLite header")
	}

	return nil
}

// bootstrapFromSnapshot bootstraps the replica from the latest snapshot if available.
func (d *DatabaseManager) bootstrapFromSnapshot(ctx context.Context) error {
	if d.snapshotManager == nil {
		return fmt.Errorf("snapshot manager not configured")
	}

	p := d.app.platform

	// Check if replica already exists
	replicaPath := d.app.replicaPath
	if replicaPath == "" {
		replicaPath = d.app.sqlitePath + "-replica"
	}

	if _, err := os.Stat(replicaPath); err == nil {
		// Replica already exists, skip bootstrap
		return nil
	}

	// Get the latest snapshot
	snap, err := d.snapshotManager.Latest(ctx)
	if err != nil {
		return fmt.Errorf("no snapshot available: %w", err)
	}

	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "snapshot_bootstrap_started",
		Data:     map[string]any{"app": d.app.name, "snapshot_id": snap.ID, "snapshot_size": snap.Size},
	})

	// Restore snapshot to replica path
	if err := d.snapshotManager.Restore(ctx, snap.ID, replicaPath); err != nil {
		return fmt.Errorf("restore snapshot: %w", err)
	}

	p.audit.Log(ctx, AuditEntry{
		Category: "database",
		Action:   "snapshot_bootstrap_completed",
		Data:     map[string]any{"app": d.app.name, "snapshot_id": snap.ID},
	})

	return nil
}

// Bootstrap initializes the database from a snapshot if available.
// This is useful for new nodes joining the cluster.
func (d *DatabaseManager) Bootstrap(ctx context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	return d.bootstrapFromSnapshot(ctx)
}

// WALManager methods

// Position returns the current WAL position.
func (w *WALManager) Position() wal.Position {
	if w.db.walManager == nil {
		return wal.Position{}
	}
	return w.db.walManager.Position()
}

// Lag returns the replication lag (replica mode only).
func (w *WALManager) Lag() time.Duration {
	if w.db.walManager == nil {
		return 0
	}
	lag := w.db.walManager.Lag()

	// Update metrics
	if w.db.app != nil && w.db.app.platform != nil {
		w.db.app.platform.metrics.SetReplicationLag(w.db.app.name, lag)
	}

	return lag
}

// Sync forces a WAL sync (primary mode only).
func (w *WALManager) Sync(ctx context.Context) error {
	if w.db.walManager == nil {
		return fmt.Errorf("WAL not configured")
	}
	return w.db.walManager.Sync(ctx)
}

// Stats returns WAL statistics.
func (w *WALManager) Stats() wal.Stats {
	if w.db.walManager == nil {
		return wal.Stats{}
	}
	return w.db.walManager.Stats()
}

// SnapshotManager methods

// Create creates a new snapshot.
func (s *SnapshotManager) Create(ctx context.Context) (*snapshot.Snapshot, error) {
	if s.db.snapshotManager == nil {
		return nil, fmt.Errorf("snapshots not configured")
	}
	return s.db.snapshotManager.Create(ctx)
}

// Latest returns the latest snapshot.
func (s *SnapshotManager) Latest(ctx context.Context) (*snapshot.Snapshot, error) {
	if s.db.snapshotManager == nil {
		return nil, fmt.Errorf("snapshots not configured")
	}
	return s.db.snapshotManager.Latest(ctx)
}

// List lists all snapshots.
func (s *SnapshotManager) List(ctx context.Context) ([]snapshot.Snapshot, error) {
	if s.db.snapshotManager == nil {
		return nil, fmt.Errorf("snapshots not configured")
	}
	return s.db.snapshotManager.List(ctx)
}

// Restore restores a snapshot to the given path.
func (s *SnapshotManager) Restore(ctx context.Context, id, destPath string) error {
	if s.db.snapshotManager == nil {
		return fmt.Errorf("snapshots not configured")
	}
	return s.db.snapshotManager.Restore(ctx, id, destPath)
}

// Delete deletes a snapshot.
func (s *SnapshotManager) Delete(ctx context.Context, id string) error {
	if s.db.snapshotManager == nil {
		return fmt.Errorf("snapshots not configured")
	}
	return s.db.snapshotManager.Delete(ctx, id)
}
