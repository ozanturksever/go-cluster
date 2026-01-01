package wal

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Replicator handles WAL replication from primary to storage.
type Replicator struct {
	config Config
	store  Store

	mu           sync.RWMutex
	position     Position
	generation   uint64
	lastWALSize  int64
	lastWALMod   time.Time
	segmentIndex uint64

	ctx    context.Context
	cancel context.CancelFunc
}

// NewReplicator creates a new WAL replicator.
func NewReplicator(config Config, store Store) *Replicator {
	return &Replicator{
		config: config,
		store:  store,
	}
}

// Start starts the replicator.
func (r *Replicator) Start(ctx context.Context) error {
	r.ctx, r.cancel = context.WithCancel(ctx)

	// Get the latest position from storage to resume
	pos, err := r.store.GetLatestPosition(ctx)
	if err == nil && !pos.IsZero() {
		r.mu.Lock()
		r.position = pos
		r.generation = pos.Generation
		r.segmentIndex = pos.Index
		r.mu.Unlock()
	} else {
		// Start a new generation
		r.mu.Lock()
		r.generation = uint64(time.Now().UnixNano())
		r.segmentIndex = 0
		r.mu.Unlock()
	}

	return nil
}

// Stop stops the replicator.
func (r *Replicator) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
}

// Position returns the current replication position.
func (r *Replicator) Position() Position {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.position
}

// Sync syncs WAL changes to storage.
func (r *Replicator) Sync(ctx context.Context) error {
	walPath := r.config.DBPath + "-wal"

	// Check if WAL file exists
	info, err := os.Stat(walPath)
	if os.IsNotExist(err) {
		// No WAL file means no changes or WAL mode not enabled
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat WAL file: %w", err)
	}

	r.mu.Lock()
	lastSize := r.lastWALSize
	lastMod := r.lastWALMod
	r.mu.Unlock()

	// Check if WAL has changed
	if info.Size() == lastSize && !info.ModTime().After(lastMod) {
		return nil
	}

	// Read the WAL file
	data, err := r.readWALChanges(walPath, lastSize)
	if err != nil {
		return fmt.Errorf("read WAL changes: %w", err)
	}

	if len(data) == 0 {
		return nil
	}

	// Create and write segment
	r.mu.Lock()
	r.segmentIndex++
	seg := Segment{
		Position: Position{
			Generation: r.generation,
			Index:      r.segmentIndex,
			Offset:     info.Size(),
			Timestamp:  time.Now(),
		},
		Size:      int64(len(data)),
		Checksum:  crc32.ChecksumIEEE(data),
		CreatedAt: time.Now(),
	}
	r.mu.Unlock()

	if err := r.store.WriteSegment(ctx, seg, data); err != nil {
		return fmt.Errorf("write segment: %w", err)
	}

	// Update position
	r.mu.Lock()
	r.position = seg.Position
	r.lastWALSize = info.Size()
	r.lastWALMod = info.ModTime()
	r.mu.Unlock()

	return nil
}

// readWALChanges reads new WAL data since the last sync.
func (r *Replicator) readWALChanges(walPath string, offset int64) ([]byte, error) {
	f, err := os.Open(walPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Get current size
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}

	if info.Size() <= offset {
		return nil, nil
	}

	// Seek to offset
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
	}

	// Read remaining data
	data := make([]byte, info.Size()-offset)
	if _, err := io.ReadFull(f, data); err != nil {
		return nil, err
	}

	return data, nil
}

// NewGeneration starts a new WAL generation (e.g., after promotion).
func (r *Replicator) NewGeneration() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.generation = uint64(time.Now().UnixNano())
	r.segmentIndex = 0
	r.lastWALSize = 0
	r.lastWALMod = time.Time{}
}

// computeDBChecksum computes a checksum of the database file.
func computeDBChecksum(dbPath string) ([]byte, error) {
	f, err := os.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}

	return h.Sum(nil), nil
}

// WALHeader represents the SQLite WAL header.
type WALHeader struct {
	Magic         uint32
	Version       uint32
	PageSize      uint32
	CheckpointSeq uint32
	Salt1         uint32
	Salt2         uint32
	Checksum1     uint32
	Checksum2     uint32
}

// ParseWALHeader parses a SQLite WAL header.
func ParseWALHeader(data []byte) (*WALHeader, error) {
	if len(data) < 32 {
		return nil, ErrInvalidWAL
	}

	h := &WALHeader{
		Magic:         binary.BigEndian.Uint32(data[0:4]),
		Version:       binary.BigEndian.Uint32(data[4:8]),
		PageSize:      binary.BigEndian.Uint32(data[8:12]),
		CheckpointSeq: binary.BigEndian.Uint32(data[12:16]),
		Salt1:         binary.BigEndian.Uint32(data[16:20]),
		Salt2:         binary.BigEndian.Uint32(data[20:24]),
		Checksum1:     binary.BigEndian.Uint32(data[24:28]),
		Checksum2:     binary.BigEndian.Uint32(data[28:32]),
	}

	// Validate magic number (0x377f0682 or 0x377f0683)
	if h.Magic != 0x377f0682 && h.Magic != 0x377f0683 {
		return nil, ErrInvalidWAL
	}

	return h, nil
}

// WALFrameHeader represents a SQLite WAL frame header.
type WALFrameHeader struct {
	PageNumber   uint32
	CommitSize   uint32
	Salt1        uint32
	Salt2        uint32
	Checksum1    uint32
	Checksum2    uint32
}

// ParseWALFrameHeader parses a SQLite WAL frame header.
func ParseWALFrameHeader(data []byte) (*WALFrameHeader, error) {
	if len(data) < 24 {
		return nil, ErrInvalidWAL
	}

	return &WALFrameHeader{
		PageNumber: binary.BigEndian.Uint32(data[0:4]),
		CommitSize: binary.BigEndian.Uint32(data[4:8]),
		Salt1:      binary.BigEndian.Uint32(data[8:12]),
		Salt2:      binary.BigEndian.Uint32(data[12:16]),
		Checksum1:  binary.BigEndian.Uint32(data[16:20]),
		Checksum2:  binary.BigEndian.Uint32(data[20:24]),
	}, nil
}

// EnsureWALMode ensures the database is in WAL mode.
func EnsureWALMode(dbPath string) error {
	// Check if the database exists
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return nil // Will be created in WAL mode
	}

	// Read the first 100 bytes of the database header
	f, err := os.Open(dbPath)
	if err != nil {
		return err
	}
	defer f.Close()

	header := make([]byte, 100)
	if _, err := io.ReadFull(f, header); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil // Empty or new database
		}
		return err
	}

	// Check the journal mode byte at offset 18
	// 2 = WAL mode
	if header[18] != 2 {
		return fmt.Errorf("database is not in WAL mode (current mode: %d)", header[18])
	}

	return nil
}

// GetDBPath returns the configured database path.
func (r *Replicator) GetDBPath() string {
	return r.config.DBPath
}

// GetReplicaPath returns the configured replica path.
func (r *Replicator) GetReplicaPath() string {
	if r.config.ReplicaPath != "" {
		return r.config.ReplicaPath
	}
	// Default replica path
	ext := filepath.Ext(r.config.DBPath)
	base := r.config.DBPath[:len(r.config.DBPath)-len(ext)]
	return base + "-replica" + ext
}
