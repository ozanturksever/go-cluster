// Package wal provides SQLite WAL replication via NATS.
package wal

import (
	"context"
	"errors"
	"sync"
	"time"
)

// Errors for WAL operations.
var (
	ErrNotLeader     = errors.New("not leader")
	ErrNotReplica    = errors.New("not replica")
	ErrNoWALFile     = errors.New("WAL file not found")
	ErrInvalidWAL    = errors.New("invalid WAL data")
	ErrSyncFailed    = errors.New("sync failed")
	ErrRestoreFailed = errors.New("restore failed")
)

// Mode represents the WAL operation mode.
type Mode int

const (
	// ModePrimary replicates WAL to NATS (leader node).
	ModePrimary Mode = iota
	// ModeReplica restores WAL from NATS (standby node).
	ModeReplica
	// ModeDisabled means WAL replication is not active.
	ModeDisabled
)

// Position represents a position in the WAL stream.
type Position struct {
	Generation uint64    `json:"generation"`
	Index      uint64    `json:"index"`
	Offset     int64     `json:"offset"`
	Timestamp  time.Time `json:"timestamp"`
}

// IsZero returns true if the position is unset.
func (p Position) IsZero() bool {
	return p.Generation == 0 && p.Index == 0 && p.Offset == 0
}

// After returns true if this position is after the other position.
func (p Position) After(other Position) bool {
	if p.Generation != other.Generation {
		return p.Generation > other.Generation
	}
	if p.Index != other.Index {
		return p.Index > other.Index
	}
	return p.Offset > other.Offset
}

// Segment represents a WAL segment stored in NATS.
type Segment struct {
	Position  Position  `json:"position"`
	Size      int64     `json:"size"`
	Checksum  uint32    `json:"checksum"`
	CreatedAt time.Time `json:"created_at"`
}

// Stats contains WAL replication statistics.
type Stats struct {
	Mode            Mode          `json:"mode"`
	Position        Position      `json:"position"`
	LastSync        time.Time     `json:"last_sync"`
	Lag             time.Duration `json:"lag"`
	SegmentsWritten uint64        `json:"segments_written"`
	SegmentsRead    uint64        `json:"segments_read"`
	BytesWritten    uint64        `json:"bytes_written"`
	BytesRead       uint64        `json:"bytes_read"`
	Errors          uint64        `json:"errors"`
}

// Config configures WAL replication.
type Config struct {
	// DBPath is the path to the primary SQLite database.
	DBPath string

	// ReplicaPath is the path for the replica database.
	ReplicaPath string

	// SyncInterval is how often to sync WAL changes.
	SyncInterval time.Duration

	// RetentionPeriod is how long to keep WAL segments.
	RetentionPeriod time.Duration

	// MaxSegmentSize is the maximum size of a WAL segment.
	MaxSegmentSize int64
}

// DefaultConfig returns a default WAL configuration.
func DefaultConfig() Config {
	return Config{
		SyncInterval:    100 * time.Millisecond,
		RetentionPeriod: 24 * time.Hour,
		MaxSegmentSize:  4 * 1024 * 1024, // 4MB
	}
}

// Manager manages WAL replication for an app.
type Manager struct {
	config Config
	store  Store

	mu         sync.RWMutex
	mode       Mode
	position   Position
	stats      Stats
	replicator *Replicator
	restorer   *Restorer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// Store defines the interface for WAL segment storage.
type Store interface {
	// WriteSegment writes a WAL segment.
	WriteSegment(ctx context.Context, seg Segment, data []byte) error

	// ReadSegment reads a WAL segment.
	ReadSegment(ctx context.Context, pos Position) ([]byte, error)

	// ListSegments lists segments after the given position.
	ListSegments(ctx context.Context, after Position) ([]Segment, error)

	// DeleteSegmentsBefore deletes segments before the given position.
	DeleteSegmentsBefore(ctx context.Context, pos Position) error

	// WatchSegments watches for new segments.
	WatchSegments(ctx context.Context, after Position) (<-chan Segment, error)

	// GetLatestPosition returns the latest position in the store.
	GetLatestPosition(ctx context.Context) (Position, error)
}

// NewManager creates a new WAL manager.
func NewManager(config Config, store Store) *Manager {
	return &Manager{
		config: config,
		store:  store,
		mode:   ModeDisabled,
	}
}

// Start starts the WAL manager in the given mode.
func (m *Manager) Start(ctx context.Context, mode Mode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancel != nil {
		m.cancel()
		m.wg.Wait()
	}

	m.ctx, m.cancel = context.WithCancel(ctx)
	m.mode = mode
	m.stats.Mode = mode

	switch mode {
	case ModePrimary:
		m.replicator = NewReplicator(m.config, m.store)
		if err := m.replicator.Start(m.ctx); err != nil {
			return err
		}
		m.wg.Add(1)
		go m.runPrimaryLoop()

	case ModeReplica:
		m.restorer = NewRestorer(m.config, m.store)
		if err := m.restorer.Start(m.ctx); err != nil {
			return err
		}
		m.wg.Add(1)
		go m.runReplicaLoop()
	}

	return nil
}

// Stop stops the WAL manager.
func (m *Manager) Stop() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	m.mu.Unlock()

	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.replicator != nil {
		m.replicator.Stop()
		m.replicator = nil
	}
	if m.restorer != nil {
		m.restorer.Stop()
		m.restorer = nil
	}
	m.mode = ModeDisabled
}

// SwitchMode switches the WAL manager to a new mode.
func (m *Manager) SwitchMode(ctx context.Context, mode Mode) error {
	m.Stop()
	return m.Start(ctx, mode)
}

// Position returns the current WAL position.
func (m *Manager) Position() Position {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.position
}

// Lag returns the replication lag (replica mode only).
func (m *Manager) Lag() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats.Lag
}

// Stats returns WAL statistics.
func (m *Manager) Stats() Stats {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stats
}

// Sync forces a WAL sync (primary mode only).
func (m *Manager) Sync(ctx context.Context) error {
	m.mu.RLock()
	repl := m.replicator
	mode := m.mode
	m.mu.RUnlock()

	if mode != ModePrimary {
		return ErrNotLeader
	}
	if repl == nil {
		return ErrNotLeader
	}

	return repl.Sync(ctx)
}

// CatchUp forces the replica to catch up to the latest position.
func (m *Manager) CatchUp(ctx context.Context) error {
	m.mu.RLock()
	rest := m.restorer
	mode := m.mode
	m.mu.RUnlock()

	if mode != ModeReplica {
		return ErrNotReplica
	}
	if rest == nil {
		return ErrNotReplica
	}

	return rest.CatchUp(ctx)
}

// runPrimaryLoop runs the primary replication loop.
func (m *Manager) runPrimaryLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.replicator != nil {
				if err := m.replicator.Sync(m.ctx); err != nil {
					m.mu.Lock()
					m.stats.Errors++
					m.mu.Unlock()
				} else {
					m.mu.Lock()
					m.position = m.replicator.Position()
					m.stats.Position = m.position
					m.stats.LastSync = time.Now()
					m.mu.Unlock()
				}
			}
		}
	}
}

// runReplicaLoop runs the replica restoration loop.
func (m *Manager) runReplicaLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.SyncInterval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.restorer != nil {
				if err := m.restorer.Restore(m.ctx); err != nil {
					m.mu.Lock()
					m.stats.Errors++
					m.mu.Unlock()
				} else {
					m.mu.Lock()
					m.position = m.restorer.Position()
					m.stats.Position = m.position
					m.stats.LastSync = time.Now()
					m.stats.Lag = m.restorer.Lag()
					m.mu.Unlock()
				}
			}
		}
	}
}
