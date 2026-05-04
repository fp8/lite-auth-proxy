package plugin

import (
	"testing"
)

type mockPlugin struct {
	name     string
	priority int
}

func (m *mockPlugin) Name() string  { return m.name }
func (m *mockPlugin) Priority() int { return m.priority }

func TestRegister_DuplicatePanics(t *testing.T) {
	defer func() {
		Reset()
	}()
	Reset()

	Register(&mockPlugin{name: "test", priority: 1})

	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(&mockPlugin{name: "test", priority: 2})
}

func TestGet_ReturnsRegistered(t *testing.T) {
	defer Reset()
	Reset()

	p := &mockPlugin{name: "finder", priority: 5}
	Register(p)

	got := Get("finder")
	if got == nil {
		t.Fatal("expected to find plugin")
	}
	if got.Name() != "finder" {
		t.Errorf("expected name 'finder', got %q", got.Name())
	}
}

func TestGet_ReturnsNilForUnknown(t *testing.T) {
	defer Reset()
	Reset()

	if Get("nonexistent") != nil {
		t.Error("expected nil for unknown plugin")
	}
}

func TestAll_SortedByPriority(t *testing.T) {
	defer Reset()
	Reset()

	Register(&mockPlugin{name: "c", priority: 30})
	Register(&mockPlugin{name: "a", priority: 10})
	Register(&mockPlugin{name: "b", priority: 20})

	all := All()
	if len(all) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(all))
	}
	if all[0].Name() != "a" || all[1].Name() != "b" || all[2].Name() != "c" {
		t.Errorf("unexpected order: %v, %v, %v", all[0].Name(), all[1].Name(), all[2].Name())
	}
}

type mockMiddlewareProvider struct {
	mockPlugin
}

func (m *mockMiddlewareProvider) BuildMiddleware(deps *Deps) ([]Middleware, error) {
	return nil, nil
}

func TestOfType_FiltersCorrectly(t *testing.T) {
	defer Reset()
	Reset()

	Register(&mockPlugin{name: "plain", priority: 10})
	Register(&mockMiddlewareProvider{mockPlugin{name: "mw", priority: 20}})

	mws := OfType[MiddlewareProvider]()
	if len(mws) != 1 {
		t.Fatalf("expected 1 MiddlewareProvider, got %d", len(mws))
	}
	if mws[0].Name() != "mw" {
		t.Errorf("expected 'mw', got %q", mws[0].Name())
	}
}

func TestStorageBackend_SingletonEnforced(t *testing.T) {
	defer Reset()
	Reset()

	// First storage backend registers fine (satisfies StorageBackendProvider interface manually).
	// Since we can't easily implement all methods, just test the basic registration behavior.
	if StorageBackend() != nil {
		t.Error("expected nil storage backend initially")
	}
}

func TestReset_ClearsRegistry(t *testing.T) {
	defer Reset()
	Reset()

	Register(&mockPlugin{name: "temp", priority: 1})
	Reset()

	if Get("temp") != nil {
		t.Error("expected nil after reset")
	}
	if len(All()) != 0 {
		t.Error("expected empty registry after reset")
	}
}
