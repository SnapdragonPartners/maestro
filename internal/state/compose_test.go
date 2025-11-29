package state

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

func TestComposeRegistry_RegisterAndGet(t *testing.T) {
	r := NewComposeRegistry()

	stack := &ComposeStack{
		ProjectName: "coder-001",
		ComposeFile: "/path/to/compose.yml",
		Network:     "coder-001-network",
		StartedAt:   time.Now(),
	}

	r.Register(stack)

	got := r.Get("coder-001")
	if got == nil {
		t.Fatal("expected stack to be registered")
	}

	if got.ProjectName != stack.ProjectName {
		t.Errorf("ProjectName = %q, want %q", got.ProjectName, stack.ProjectName)
	}
	if got.ComposeFile != stack.ComposeFile {
		t.Errorf("ComposeFile = %q, want %q", got.ComposeFile, stack.ComposeFile)
	}
	if got.Network != stack.Network {
		t.Errorf("Network = %q, want %q", got.Network, stack.Network)
	}
}

func TestComposeRegistry_GetReturnsNilForUnknown(t *testing.T) {
	r := NewComposeRegistry()

	got := r.Get("unknown")
	if got != nil {
		t.Error("expected nil for unknown stack")
	}
}

func TestComposeRegistry_Unregister(t *testing.T) {
	r := NewComposeRegistry()

	stack := &ComposeStack{
		ProjectName: "demo",
		ComposeFile: "/path/to/compose.yml",
		Network:     "demo-network",
		StartedAt:   time.Now(),
	}

	r.Register(stack)
	if !r.Exists("demo") {
		t.Fatal("expected stack to exist after register")
	}

	r.Unregister("demo")
	if r.Exists("demo") {
		t.Error("expected stack to not exist after unregister")
	}
}

func TestComposeRegistry_All(t *testing.T) {
	r := NewComposeRegistry()

	stacks := []*ComposeStack{
		{ProjectName: "coder-001", Network: "coder-001-network", StartedAt: time.Now()},
		{ProjectName: "coder-002", Network: "coder-002-network", StartedAt: time.Now()},
		{ProjectName: "demo", Network: "demo-network", StartedAt: time.Now()},
	}

	for _, s := range stacks {
		r.Register(s)
	}

	all := r.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d stacks, want 3", len(all))
	}

	// Verify all project names are present
	names := make(map[string]bool)
	for _, s := range all {
		names[s.ProjectName] = true
	}

	for _, s := range stacks {
		if !names[s.ProjectName] {
			t.Errorf("missing stack %q in All() result", s.ProjectName)
		}
	}
}

func TestComposeRegistry_Count(t *testing.T) {
	r := NewComposeRegistry()

	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0 for empty registry", r.Count())
	}

	r.Register(&ComposeStack{ProjectName: "coder-001", StartedAt: time.Now()})
	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1", r.Count())
	}

	r.Register(&ComposeStack{ProjectName: "coder-002", StartedAt: time.Now()})
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}

	r.Unregister("coder-001")
	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1 after unregister", r.Count())
	}
}

func TestComposeRegistry_Exists(t *testing.T) {
	r := NewComposeRegistry()

	if r.Exists("demo") {
		t.Error("expected Exists() = false for empty registry")
	}

	r.Register(&ComposeStack{ProjectName: "demo", StartedAt: time.Now()})

	if !r.Exists("demo") {
		t.Error("expected Exists() = true after register")
	}

	if r.Exists("unknown") {
		t.Error("expected Exists() = false for unknown stack")
	}
}

func TestComposeRegistry_RegisterNilIsNoOp(t *testing.T) {
	r := NewComposeRegistry()

	r.Register(nil)

	if r.Count() != 0 {
		t.Error("expected nil registration to be no-op")
	}
}

func TestComposeRegistry_GetReturnsCopy(t *testing.T) {
	r := NewComposeRegistry()

	original := &ComposeStack{
		ProjectName: "demo",
		ComposeFile: "/original/path",
		Network:     "demo-network",
		StartedAt:   time.Now(),
	}

	r.Register(original)

	// Get a copy and modify it
	copy1 := r.Get("demo")
	copy1.ComposeFile = "/modified/path"

	// Get another copy - should still have original value
	copy2 := r.Get("demo")
	if copy2.ComposeFile != "/original/path" {
		t.Error("expected Get() to return isolated copy")
	}
}

func TestComposeRegistry_AllReturnsCopies(t *testing.T) {
	r := NewComposeRegistry()

	r.Register(&ComposeStack{
		ProjectName: "demo",
		ComposeFile: "/original/path",
		StartedAt:   time.Now(),
	})

	// Get all and modify
	all := r.All()
	all[0].ComposeFile = "/modified/path"

	// Get again - should still have original value
	got := r.Get("demo")
	if got.ComposeFile != "/original/path" {
		t.Error("expected All() to return isolated copies")
	}
}

func TestComposeRegistry_ConcurrentAccess(t *testing.T) {
	r := NewComposeRegistry()

	var wg sync.WaitGroup
	numGoroutines := 100

	// Concurrent registrations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			r.Register(&ComposeStack{
				ProjectName: fmt.Sprintf("coder-%03d", n),
				StartedAt:   time.Now(),
			})
		}(i)
	}

	// Concurrent reads while writing
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = r.All()
			_ = r.Count()
		}()
	}

	wg.Wait()

	// Should have all registrations
	if r.Count() != numGoroutines {
		t.Errorf("Count() = %d, want %d after concurrent registrations", r.Count(), numGoroutines)
	}
}
