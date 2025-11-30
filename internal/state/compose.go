// Package state provides runtime state management for container orchestration.
package state

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
	"time"
)

// ComposeStack represents an active Docker Compose stack.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type ComposeStack struct {
	ProjectName string    `json:"project_name"` // e.g., "coder-001", "demo"
	ComposeFile string    `json:"compose_file"` // Path to compose file
	Network     string    `json:"network"`      // Network name
	StartedAt   time.Time `json:"started_at"`
}

// ComposeRegistry manages active Docker Compose stacks.
// Thread-safe for concurrent access.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type ComposeRegistry struct {
	mu     sync.RWMutex
	stacks map[string]*ComposeStack // keyed by ProjectName
}

// NewComposeRegistry creates a new compose stack registry.
func NewComposeRegistry() *ComposeRegistry {
	return &ComposeRegistry{
		stacks: make(map[string]*ComposeStack),
	}
}

// Register adds a stack to the registry.
func (r *ComposeRegistry) Register(stack *ComposeStack) {
	if stack == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Store a copy to prevent external mutation
	s := *stack
	r.stacks[stack.ProjectName] = &s
}

// Unregister removes a stack from the registry.
func (r *ComposeRegistry) Unregister(projectName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.stacks, projectName)
}

// Get returns a stack by project name, or nil if not found.
func (r *ComposeRegistry) Get(projectName string) *ComposeStack {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stack, ok := r.stacks[projectName]
	if !ok {
		return nil
	}

	// Return a copy to prevent external mutation
	s := *stack
	return &s
}

// All returns all registered stacks.
func (r *ComposeRegistry) All() []*ComposeStack {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*ComposeStack, 0, len(r.stacks))
	for _, stack := range r.stacks {
		// Return copies to prevent external mutation
		s := *stack
		result = append(result, &s)
	}
	return result
}

// Count returns the number of registered stacks.
func (r *ComposeRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.stacks)
}

// Exists checks if a stack is registered.
func (r *ComposeRegistry) Exists(projectName string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.stacks[projectName]
	return ok
}

// Cleanup tears down all registered stacks.
// Called during orchestrator shutdown.
func (r *ComposeRegistry) Cleanup(ctx context.Context) error {
	r.mu.Lock()
	stacks := make([]*ComposeStack, 0, len(r.stacks))
	for _, stack := range r.stacks {
		s := *stack
		stacks = append(stacks, &s)
	}
	r.mu.Unlock()

	var errs []error
	for _, stack := range stacks {
		if err := r.teardownStack(ctx, stack); err != nil {
			errs = append(errs, fmt.Errorf("failed to teardown %s: %w", stack.ProjectName, err))
		}
		r.Unregister(stack.ProjectName)
	}

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %v", errs)
	}
	return nil
}

// teardownStack runs docker compose down for a stack.
func (r *ComposeRegistry) teardownStack(ctx context.Context, stack *ComposeStack) error {
	// Build command: docker compose -p <project> -f <file> down -v
	args := []string{"compose", "-p", stack.ProjectName}
	if stack.ComposeFile != "" {
		args = append(args, "-f", stack.ComposeFile)
	}
	args = append(args, "down", "-v")

	cmd := exec.CommandContext(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose down failed: %w, output: %s", err, string(output))
	}
	return nil
}
