package store

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// KeyValueStore provides key-value storage for plugins.
// Core provides an in-memory implementation (MemoryKeyValueStore).
// Storage plugins provide persistent implementations.
type KeyValueStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Set(ctx context.Context, key string, value []byte) error
	Delete(ctx context.Context, key string) error
	List(ctx context.Context, prefix string) ([]string, error)
}

// MemoryKeyValueStore is a thread-safe in-memory KeyValueStore.
// Suitable for single-instance deployments or testing.
// Data is lost on process exit.
type MemoryKeyValueStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

// NewMemoryKeyValueStore creates a new in-memory KeyValueStore.
func NewMemoryKeyValueStore() *MemoryKeyValueStore {
	return &MemoryKeyValueStore{
		data: make(map[string][]byte),
	}
}

func (m *MemoryKeyValueStore) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	val, ok := m.data[key]
	if !ok {
		return nil, nil
	}
	cp := make([]byte, len(val))
	copy(cp, val)
	return cp, nil
}

func (m *MemoryKeyValueStore) Set(_ context.Context, key string, value []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(value))
	copy(cp, value)
	m.data[key] = cp
	return nil
}

func (m *MemoryKeyValueStore) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

func (m *MemoryKeyValueStore) List(_ context.Context, prefix string) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var keys []string
	for k := range m.data {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
