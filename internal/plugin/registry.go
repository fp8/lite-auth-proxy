package plugin

import (
	"fmt"
	"sort"
)

var registry = &pluginRegistry{
	plugins: make(map[string]Plugin),
}

type pluginRegistry struct {
	plugins        map[string]Plugin
	storageBackend StorageBackendProvider // at most one; enforced by Register()
}

// Register adds a plugin to the global registry.
// Panics if a plugin with the same name is already registered.
// For StorageBackendProvider plugins: panics if any StorageBackendProvider is already
// registered, even under a different name. Only one storage backend may exist in a binary.
func Register(p Plugin) {
	if _, exists := registry.plugins[p.Name()]; exists {
		panic("plugin already registered: " + p.Name())
	}
	if sb, ok := p.(StorageBackendProvider); ok {
		if existing := registry.storageBackend; existing != nil {
			panic(fmt.Sprintf(
				"only one storage backend allowed: %q is already registered, cannot register %q",
				existing.Name(), p.Name(),
			))
		}
		registry.storageBackend = sb
	}
	registry.plugins[p.Name()] = p
}

// Get returns a registered plugin by name, or nil if not found.
func Get(name string) Plugin {
	return registry.plugins[name]
}

// StorageBackend returns the single registered StorageBackendProvider, or nil.
func StorageBackend() StorageBackendProvider {
	return registry.storageBackend
}

// All returns all registered plugins sorted by priority.
func All() []Plugin {
	result := make([]Plugin, 0, len(registry.plugins))
	for _, p := range registry.plugins {
		result = append(result, p)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority() < result[j].Priority()
	})
	return result
}

// OfType returns all registered plugins that implement the given interface,
// sorted by Priority() ascending (lower = earlier in pipeline).
func OfType[T Plugin]() []T {
	var result []T
	for _, p := range registry.plugins {
		if t, ok := p.(T); ok {
			result = append(result, t)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Priority() < result[j].Priority()
	})
	return result
}

// Reset clears the registry. Only for use in tests.
func Reset() {
	registry.plugins = make(map[string]Plugin)
	registry.storageBackend = nil
}
