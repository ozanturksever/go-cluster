package wal

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// NATSStore implements Store using NATS Object Store.
type NATSStore struct {
	js         jetstream.JetStream
	bucketName string
	objStore   jetstream.ObjectStore
}

// NATSStoreConfig configures the NATS WAL store.
type NATSStoreConfig struct {
	// MaxBytes is the maximum size of the WAL object store.
	// Defaults to 100MB if not set.
	MaxBytes int64
}

// NewNATSStore creates a new NATS-backed WAL store.
func NewNATSStore(js jetstream.JetStream, platform, app string, opts ...func(*NATSStoreConfig)) (*NATSStore, error) {
	return NewNATSStoreWithContext(context.Background(), js, platform, app, opts...)
}

// NewNATSStoreWithContext creates a new NATS-backed WAL store with context.
func NewNATSStoreWithContext(ctx context.Context, js jetstream.JetStream, platform, app string, opts ...func(*NATSStoreConfig)) (*NATSStore, error) {
	cfg := &NATSStoreConfig{
		MaxBytes: 100 * 1024 * 1024, // 100MB default (reasonable for tests and small deployments)
	}
	for _, opt := range opts {
		opt(cfg)
	}

	bucketName := fmt.Sprintf("%s_%s_wal", platform, app)

	// Create or get the object store bucket with timeout
	createCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	objStore, err := js.CreateOrUpdateObjectStore(createCtx, jetstream.ObjectStoreConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("WAL segments for %s/%s", platform, app),
		MaxBytes:    cfg.MaxBytes,
		Storage:     jetstream.FileStorage,
	})
	if err != nil {
		return nil, fmt.Errorf("create object store: %w", err)
	}

	return &NATSStore{
		js:         js,
		bucketName: bucketName,
		objStore:   objStore,
	}, nil
}

// WithWALMaxBytes sets the maximum size of the WAL object store.
func WithWALMaxBytes(maxBytes int64) func(*NATSStoreConfig) {
	return func(cfg *NATSStoreConfig) {
		cfg.MaxBytes = maxBytes
	}
}

// segmentKey returns the object key for a segment.
func segmentKey(pos Position) string {
	return fmt.Sprintf("seg/%d/%016d", pos.Generation, pos.Index)
}

// parseSegmentKey parses a segment key into a position.
func parseSegmentKey(key string) (Position, error) {
	parts := strings.Split(key, "/")
	if len(parts) != 3 || parts[0] != "seg" {
		return Position{}, fmt.Errorf("invalid segment key: %s", key)
	}

	gen, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return Position{}, fmt.Errorf("parse generation: %w", err)
	}

	idx, err := strconv.ParseUint(parts[2], 10, 64)
	if err != nil {
		return Position{}, fmt.Errorf("parse index: %w", err)
	}

	return Position{Generation: gen, Index: idx}, nil
}

// WriteSegment writes a WAL segment to NATS.
func (s *NATSStore) WriteSegment(ctx context.Context, seg Segment, data []byte) error {
	key := segmentKey(seg.Position)

	// Store metadata as description
	meta, err := json.Marshal(seg)
	if err != nil {
		return fmt.Errorf("marshal segment metadata: %w", err)
	}

	// Put the object - store metadata in description by using Put with ObjectMeta
	_, err = s.objStore.Put(ctx, jetstream.ObjectMeta{
		Name:        key,
		Description: string(meta),
	}, strings.NewReader(string(data)))
	if err != nil {
		return fmt.Errorf("put object: %w", err)
	}

	return nil
}

// ReadSegment reads a WAL segment from NATS.
func (s *NATSStore) ReadSegment(ctx context.Context, pos Position) ([]byte, error) {
	key := segmentKey(pos)

	data, err := s.objStore.GetBytes(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("get object: %w", err)
	}

	return data, nil
}

// ListSegments lists segments after the given position.
func (s *NATSStore) ListSegments(ctx context.Context, after Position) ([]Segment, error) {
	// List all objects
	objects, err := s.objStore.List(ctx)
	if err != nil {
		// NATS returns an error when there are no objects - treat as empty list
		if strings.Contains(err.Error(), "no objects found") {
			return []Segment{}, nil
		}
		return nil, fmt.Errorf("list objects: %w", err)
	}

	var segments []Segment
	for _, obj := range objects {
		// Skip non-segment objects
		if !strings.HasPrefix(obj.Name, "seg/") {
			continue
		}

		// Parse metadata from description
		var seg Segment
		if obj.Description != "" {
			if err := json.Unmarshal([]byte(obj.Description), &seg); err != nil {
				// Try to parse position from key
				pos, err := parseSegmentKey(obj.Name)
				if err != nil {
					continue
				}
				seg.Position = pos
				seg.Size = int64(obj.Size)
			}
		} else {
			// Parse position from key
			pos, err := parseSegmentKey(obj.Name)
			if err != nil {
				continue
			}
			seg.Position = pos
			seg.Size = int64(obj.Size)
		}

		// Filter by position
		if seg.Position.After(after) {
			segments = append(segments, seg)
		}
	}

	// Sort by position
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].Position.Generation != segments[j].Position.Generation {
			return segments[i].Position.Generation < segments[j].Position.Generation
		}
		return segments[i].Position.Index < segments[j].Position.Index
	})

	return segments, nil
}

// DeleteSegmentsBefore deletes segments before the given position.
func (s *NATSStore) DeleteSegmentsBefore(ctx context.Context, pos Position) error {
	segments, err := s.ListSegments(ctx, Position{})
	if err != nil {
		return err
	}

	for _, seg := range segments {
		if pos.After(seg.Position) {
			key := segmentKey(seg.Position)
			if err := s.objStore.Delete(ctx, key); err != nil {
				// Log but continue - best effort cleanup
				continue
			}
		}
	}

	return nil
}

// WatchSegments watches for new segments.
func (s *NATSStore) WatchSegments(ctx context.Context, after Position) (<-chan Segment, error) {
	watcher, err := s.objStore.Watch(ctx)
	if err != nil {
		return nil, fmt.Errorf("create watcher: %w", err)
	}

	ch := make(chan Segment, 64)

	go func() {
		defer close(ch)
		defer watcher.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case info := <-watcher.Updates():
				if info == nil {
					continue
				}

				// Skip deletions
				if info.Deleted {
					continue
				}

				// Skip non-segment objects
				if !strings.HasPrefix(info.Name, "seg/") {
					continue
				}

				// Parse segment
				var seg Segment
				if info.Description != "" {
					if err := json.Unmarshal([]byte(info.Description), &seg); err != nil {
						pos, err := parseSegmentKey(info.Name)
						if err != nil {
							continue
						}
						seg.Position = pos
					}
				} else {
					pos, err := parseSegmentKey(info.Name)
					if err != nil {
						continue
					}
					seg.Position = pos
				}

				// Filter by position
				if !seg.Position.After(after) {
					continue
				}

				select {
				case ch <- seg:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// GetLatestPosition returns the latest position in the store.
func (s *NATSStore) GetLatestPosition(ctx context.Context) (Position, error) {
	segments, err := s.ListSegments(ctx, Position{})
	if err != nil {
		return Position{}, err
	}

	if len(segments) == 0 {
		return Position{}, nil
	}

	// Return the last segment's position
	return segments[len(segments)-1].Position, nil
}

// Cleanup removes old segments based on retention policy.
func (s *NATSStore) Cleanup(ctx context.Context, retention time.Duration) error {
	cutoff := time.Now().Add(-retention)

	segments, err := s.ListSegments(ctx, Position{})
	if err != nil {
		return err
	}

	for _, seg := range segments {
		if seg.CreatedAt.Before(cutoff) {
			key := segmentKey(seg.Position)
			if err := s.objStore.Delete(ctx, key); err != nil {
				// Log but continue
				continue
			}
		}
	}

	return nil
}

// GetBucketName returns the bucket name.
func (s *NATSStore) GetBucketName() string {
	return s.bucketName
}
