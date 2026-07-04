package registry

import (
	"sync"
	"testing"

	"go.agentcat.com/sdk/internal/core"
)

type mockServer struct {
	name string
}

func TestRegister(t *testing.T) {
	tests := []struct {
		name     string
		server   any
		instance *core.MCPcatInstance
	}{
		{
			name:   "register pointer server",
			server: &mockServer{name: "test1"},
			instance: &core.MCPcatInstance{
				ProjectID: "proj1",
				Options:   &core.Options{},
			},
		},
		{
			name:   "register another pointer server",
			server: &mockServer{name: "test2"},
			instance: &core.MCPcatInstance{
				ProjectID: "proj2",
				Options:   &core.Options{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRegistry()

			Register(tt.server, tt.instance)
			got := Get(tt.server)

			if got == nil {
				t.Error("Get() = nil, want non-nil")
			} else if got.ProjectID != tt.instance.ProjectID {
				t.Errorf("Get().ProjectID = %v, want %v", got.ProjectID, tt.instance.ProjectID)
			}
		})
	}
}

func TestRegister_PanicsOnNil(t *testing.T) {
	clearRegistry()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Register(nil, ...) should panic")
		}
	}()

	Register(nil, &core.MCPcatInstance{ProjectID: "proj"})
}

func TestRegister_PanicsOnNonPointer(t *testing.T) {
	clearRegistry()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Register(nonPointer, ...) should panic")
		}
	}()

	Register(mockServer{name: "value"}, &core.MCPcatInstance{ProjectID: "proj"})
}

func TestGet(t *testing.T) {
	tests := []struct {
		name       string
		setupFunc  func() any
		getServer  any
		wantNil    bool
		wantProjID string
	}{
		{
			name: "get registered server",
			setupFunc: func() any {
				server := &mockServer{name: "test1"}
				Register(server, &core.MCPcatInstance{
					ProjectID: "proj1",
					Options:   &core.Options{},
				})
				return server
			},
			wantNil:    false,
			wantProjID: "proj1",
		},
		{
			name: "get unregistered server",
			setupFunc: func() any {
				return &mockServer{name: "unregistered"}
			},
			wantNil: true,
		},
		{
			name: "get nil server",
			setupFunc: func() any {
				return nil
			},
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRegistry()

			server := tt.setupFunc()
			got := Get(server)

			if tt.wantNil {
				if got != nil {
					t.Errorf("Get() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Error("Get() = nil, want non-nil")
				} else if got.ProjectID != tt.wantProjID {
					t.Errorf("Get().ProjectID = %v, want %v", got.ProjectID, tt.wantProjID)
				}
			}
		})
	}
}

func TestUnregister(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func() any
	}{
		{
			name: "unregister existing server",
			setupFunc: func() any {
				server := &mockServer{name: "test1"}
				Register(server, &core.MCPcatInstance{
					ProjectID: "proj1",
					Options:   &core.Options{},
				})
				return server
			},
		},
		{
			name: "unregister non-existent server",
			setupFunc: func() any {
				return &mockServer{name: "nonexistent"}
			},
		},
		{
			name: "unregister nil server",
			setupFunc: func() any {
				return nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearRegistry()

			server := tt.setupFunc()
			Unregister(server)

			if server != nil {
				got := Get(server)
				if got != nil {
					t.Errorf("Get() after Unregister() = %v, want nil", got)
				}
			}
		})
	}
}

func TestMustBePointer(t *testing.T) {
	t.Run("pointer type does not panic", func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("mustBePointer panicked for pointer: %v", r)
			}
		}()
		mustBePointer(&mockServer{name: "test"})
	})

	t.Run("nil panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("mustBePointer should panic for nil")
			}
		}()
		mustBePointer(nil)
	})

	t.Run("non-pointer panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("mustBePointer should panic for non-pointer")
			}
		}()
		mustBePointer(mockServer{name: "test"})
	})
}

func TestConcurrentAccess(t *testing.T) {
	clearRegistry()

	const numGoroutines = 100
	const numOperations = 1000

	var wg sync.WaitGroup
	servers := make([]*mockServer, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		servers[i] = &mockServer{name: "test"}
	}

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				Register(servers[idx], &core.MCPcatInstance{
					ProjectID: "concurrent",
					Options:   &core.Options{},
				})
			}
		}(i)
	}
	wg.Wait()

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < numOperations; j++ {
				instance := Get(servers[idx])
				if instance == nil {
					t.Errorf("Get() returned nil for server %d", idx)
				}
			}
		}(i)
	}
	wg.Wait()

	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			Unregister(servers[idx])
		}(i)
	}
	wg.Wait()

	for i, server := range servers {
		if instance := Get(server); instance != nil {
			t.Errorf("Server %d still registered after Unregister()", i)
		}
	}
}

func TestConcurrentMixedOperations(t *testing.T) {
	clearRegistry()

	const numGoroutines = 50
	var wg sync.WaitGroup
	servers := make([]*mockServer, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		servers[i] = &mockServer{name: "test"}
	}

	wg.Add(numGoroutines * 3)

	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			Register(servers[idx], &core.MCPcatInstance{
				ProjectID: "mixed",
				Options:   &core.Options{},
			})
		}(i)

		go func(idx int) {
			defer wg.Done()
			Get(servers[idx])
		}(i)

		go func(idx int) {
			defer wg.Done()
			Unregister(servers[idx])
		}(i)
	}

	wg.Wait()
}

func TestRegistryLifecycle(t *testing.T) {
	clearRegistry()

	server := &mockServer{name: "lifecycle"}
	instance := &core.MCPcatInstance{
		ProjectID: "lifecycle-proj",
		Options:   &core.Options{},
	}

	got := Get(server)
	if got != nil {
		t.Errorf("Step 1: Get() = %v, want nil", got)
	}

	Register(server, instance)
	got = Get(server)
	if got == nil {
		t.Fatal("Step 2: Get() = nil, want non-nil")
	}
	if got.ProjectID != instance.ProjectID {
		t.Errorf("Step 2: ProjectID = %v, want %v", got.ProjectID, instance.ProjectID)
	}

	Unregister(server)
	got = Get(server)
	if got != nil {
		t.Errorf("Step 3: Get() = %v, want nil", got)
	}

	Register(server, instance)
	got = Get(server)
	if got == nil {
		t.Fatal("Step 4: Get() = nil, want non-nil")
	}
}

func TestMultipleServers(t *testing.T) {
	clearRegistry()

	servers := []*mockServer{
		{name: "server1"},
		{name: "server2"},
		{name: "server3"},
	}

	instances := []*core.MCPcatInstance{
		{ProjectID: "proj1", Options: &core.Options{}},
		{ProjectID: "proj2", Options: &core.Options{}},
		{ProjectID: "proj3", Options: &core.Options{}},
	}

	for i, server := range servers {
		Register(server, instances[i])
	}

	for i, server := range servers {
		got := Get(server)
		if got == nil {
			t.Errorf("Server %d: Get() = nil, want non-nil", i)
			continue
		}
		if got.ProjectID != instances[i].ProjectID {
			t.Errorf("Server %d: ProjectID = %v, want %v", i, got.ProjectID, instances[i].ProjectID)
		}
	}

	Unregister(servers[1])

	if got := Get(servers[1]); got != nil {
		t.Errorf("Server 1 after unregister: Get() = %v, want nil", got)
	}

	for _, idx := range []int{0, 2} {
		got := Get(servers[idx])
		if got == nil {
			t.Errorf("Server %d: Get() = nil, want non-nil", idx)
		}
	}
}

func TestRegisterOverwrite(t *testing.T) {
	clearRegistry()

	server := &mockServer{name: "test"}

	instance1 := &core.MCPcatInstance{
		ProjectID: "proj1",
		Options:   &core.Options{},
	}

	instance2 := &core.MCPcatInstance{
		ProjectID: "proj2",
		Options:   &core.Options{},
	}

	Register(server, instance1)
	got := Get(server)
	if got == nil || got.ProjectID != "proj1" {
		t.Error("First registration failed")
	}

	Register(server, instance2)
	got = Get(server)
	if got == nil {
		t.Fatal("Get() = nil after second registration")
	}
	if got.ProjectID != "proj2" {
		t.Errorf("ProjectID = %v, want proj2 (should be overwritten)", got.ProjectID)
	}
}

func clearRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	serverMCPcatMap = make(map[any]*core.MCPcatInstance)
}
