package cluster

import (
	"context"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"
)

// KV provides a distributed key-value store for an app.
type KV struct {
	app *App
	kv  jetstream.KeyValue
}

// KVEntry represents an entry in the key-value store.
type KVEntry struct {
	Key      string
	Value    []byte
	Revision uint64
}

// NewKV creates a new KV store for the app.
func NewKV(app *App) (*KV, error) {
	p := app.platform
	bucketName := fmt.Sprintf("%s_%s", p.name, app.name)

	kv, err := p.js.CreateOrUpdateKeyValue(context.Background(), jetstream.KeyValueConfig{
		Bucket:      bucketName,
		Description: fmt.Sprintf("KV store for %s/%s", p.name, app.name),
		History:     5,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create KV bucket: %w", err)
	}

	return &KV{
		app: app,
		kv:  kv,
	}, nil
}

// Get retrieves a value by key.
func (k *KV) Get(ctx context.Context, key string) ([]byte, error) {
	entry, err := k.kv.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return entry.Value(), nil
}

// GetEntry retrieves an entry with metadata by key.
func (k *KV) GetEntry(ctx context.Context, key string) (*KVEntry, error) {
	entry, err := k.kv.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	return &KVEntry{
		Key:      entry.Key(),
		Value:    entry.Value(),
		Revision: entry.Revision(),
	}, nil
}

// Put stores a value at the given key.
func (k *KV) Put(ctx context.Context, key string, value []byte) (uint64, error) {
	rev, err := k.kv.Put(ctx, key, value)
	return rev, err
}

// Create creates a new key only if it doesn't exist.
func (k *KV) Create(ctx context.Context, key string, value []byte) (uint64, error) {
	rev, err := k.kv.Create(ctx, key, value)
	return rev, err
}

// Update updates a key only if the revision matches.
func (k *KV) Update(ctx context.Context, key string, value []byte, revision uint64) (uint64, error) {
	rev, err := k.kv.Update(ctx, key, value, revision)
	return rev, err
}

// Delete deletes a key.
func (k *KV) Delete(ctx context.Context, key string) error {
	return k.kv.Delete(ctx, key)
}

// Keys returns all keys matching the given pattern.
func (k *KV) Keys(ctx context.Context) ([]string, error) {
	keys, err := k.kv.Keys(ctx)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// Watch watches for changes to keys matching the pattern.
func (k *KV) Watch(ctx context.Context, pattern string) (<-chan *KVEntry, error) {
	watcher, err := k.kv.Watch(ctx, pattern)
	if err != nil {
		return nil, err
	}

	ch := make(chan *KVEntry, 64)
	go func() {
		defer close(ch)
		defer watcher.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case entry := <-watcher.Updates():
				if entry == nil {
					continue
				}
				select {
				case ch <- &KVEntry{
					Key:      entry.Key(),
					Value:    entry.Value(),
					Revision: entry.Revision(),
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

// WatchAll watches for changes to all keys.
func (k *KV) WatchAll(ctx context.Context) (<-chan *KVEntry, error) {
	watcher, err := k.kv.WatchAll(ctx)
	if err != nil {
		return nil, err
	}

	ch := make(chan *KVEntry, 64)
	go func() {
		defer close(ch)
		defer watcher.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case entry := <-watcher.Updates():
				if entry == nil {
					continue
				}
				select {
				case ch <- &KVEntry{
					Key:      entry.Key(),
					Value:    entry.Value(),
					Revision: entry.Revision(),
				}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}
