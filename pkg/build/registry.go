package build

import (
	"fmt"
	"sort"
)

// Registry manages available build backends and performs detection
type Registry struct {
	backends []BackendRegistration
}

// NewRegistry creates a new registry with default MVP backends
func NewRegistry() *Registry {
	r := &Registry{}

	// Register MVP backends in priority order
	// Higher priority backends are checked first
	r.Register(NewGoBackend(), PriorityHigh)     // Go projects (go.mod)
	r.Register(NewPythonBackend(), PriorityHigh) // Python projects (pyproject.toml, requirements.txt)
	r.Register(NewNodeBackend(), PriorityHigh)   // Node.js projects (package.json)
	r.Register(NewMakeBackend(), PriorityMedium) // Generic Makefile projects
	r.Register(NewNullBackend(), PriorityLow)    // Empty repositories (fallback)

	return r
}

// Register adds a backend to the registry with the specified priority
func (r *Registry) Register(backend BuildBackend, priority BackendPriority) {
	r.backends = append(r.backends, BackendRegistration{
		Backend:  backend,
		Priority: priority,
	})

	// Sort backends by priority (highest first)
	sort.Slice(r.backends, func(i, j int) bool {
		return r.backends[i].Priority > r.backends[j].Priority
	})
}

// Detect finds the most appropriate backend for the given project root
func (r *Registry) Detect(root string) (BuildBackend, error) {
	for _, registration := range r.backends {
		if registration.Backend.Detect(root) {
			return registration.Backend, nil
		}
	}

	return nil, fmt.Errorf("no suitable backend found for project at %s", root)
}

// List returns all registered backends in priority order
func (r *Registry) List() []BackendRegistration {
	return r.backends
}

// GetByName returns a backend by its name
func (r *Registry) GetByName(name string) (BuildBackend, error) {
	for _, registration := range r.backends {
		if registration.Backend.Name() == name {
			return registration.Backend, nil
		}
	}

	return nil, fmt.Errorf("backend not found: %s", name)
}
