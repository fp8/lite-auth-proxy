package store

import (
	"context"
	"sync"
	"testing"
)

func TestMemoryKeyValueStore_GetSetDelete(t *testing.T) {
	kv := NewMemoryKeyValueStore()
	ctx := context.Background()

	// Get non-existent key
	val, err := kv.Get(ctx, "missing")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil for missing key, got %v", val)
	}

	// Set and get
	if err := kv.Set(ctx, "key1", []byte("value1")); err != nil {
		t.Fatalf("Set failed: %v", err)
	}
	val, err = kv.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if string(val) != "value1" {
		t.Errorf("expected 'value1', got '%s'", string(val))
	}

	// Delete and verify
	if err := kv.Delete(ctx, "key1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	val, err = kv.Get(ctx, "key1")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if val != nil {
		t.Errorf("expected nil after delete, got %v", val)
	}
}

func TestMemoryKeyValueStore_List(t *testing.T) {
	kv := NewMemoryKeyValueStore()
	ctx := context.Background()

	_ = kv.Set(ctx, "apikeys/key1", []byte("v1"))
	_ = kv.Set(ctx, "apikeys/key2", []byte("v2"))
	_ = kv.Set(ctx, "other/key3", []byte("v3"))

	keys, err := kv.List(ctx, "apikeys/")
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
	if keys[0] != "apikeys/key1" || keys[1] != "apikeys/key2" {
		t.Errorf("unexpected keys: %v", keys)
	}
}

func TestMemoryKeyValueStore_ValueIsolation(t *testing.T) {
	kv := NewMemoryKeyValueStore()
	ctx := context.Background()

	original := []byte("original")
	_ = kv.Set(ctx, "key", original)

	// Mutate original slice — should not affect stored value.
	original[0] = 'X'

	val, _ := kv.Get(ctx, "key")
	if string(val) != "original" {
		t.Errorf("stored value was mutated: got '%s'", string(val))
	}

	// Mutate retrieved value — should not affect stored value.
	val[0] = 'Y'

	val2, _ := kv.Get(ctx, "key")
	if string(val2) != "original" {
		t.Errorf("stored value was mutated via Get: got '%s'", string(val2))
	}
}

func TestMemoryKeyValueStore_ConcurrentAccess(t *testing.T) {
	kv := NewMemoryKeyValueStore()
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := "key"
			_ = kv.Set(ctx, key, []byte("v"))
			_, _ = kv.Get(ctx, key)
			_, _ = kv.List(ctx, "")
			_ = kv.Delete(ctx, key)
		}(i)
	}
	wg.Wait()
}
