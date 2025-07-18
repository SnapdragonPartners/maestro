package exec

import (
	"fmt"
	"sync"
)

// Registry manages available executors
type Registry struct {
	mu        sync.RWMutex
	executors map[string]Executor
	default_  string
}

// NewRegistry creates a new executor registry
func NewRegistry() *Registry {
	return &Registry{
		executors: make(map[string]Executor),
		default_:  "local", // Default to local executor
	}
}

// Register adds an executor to the registry
func (r *Registry) Register(executor Executor) error {
	if executor == nil {
		return fmt.Errorf("executor cannot be nil")
	}

	name := executor.Name()
	if name == "" {
		return fmt.Errorf("executor name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.executors[string(name)] = executor
	return nil
}

// Get retrieves an executor by name
func (r *Registry) Get(name string) (Executor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	executor, exists := r.executors[name]
	if !exists {
		return nil, fmt.Errorf("executor '%s' not found", name)
	}

	return executor, nil
}

// GetDefault returns the default executor
func (r *Registry) GetDefault() (Executor, error) {
	return r.Get(r.default_)
}

// SetDefault sets the default executor
func (r *Registry) SetDefault(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.executors[name]; !exists {
		return fmt.Errorf("executor '%s' not found", name)
	}

	r.default_ = name
	return nil
}

// List returns all registered executor names
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.executors))
	for name := range r.executors {
		names = append(names, name)
	}

	return names
}

// GetAvailable returns all available executors
func (r *Registry) GetAvailable() []Executor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	available := make([]Executor, 0, len(r.executors))
	for _, executor := range r.executors {
		if executor.Available() {
			available = append(available, executor)
		}
	}

	return available
}

// GetBest returns the best available executor based on preference order
func (r *Registry) GetBest(preferences []string) (Executor, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Try preferences in order
	for _, name := range preferences {
		if executor, exists := r.executors[name]; exists && executor.Available() {
			return executor, nil
		}
	}

	// Fall back to default
	if executor, exists := r.executors[r.default_]; exists && executor.Available() {
		return executor, nil
	}

	// Fall back to any available executor
	for _, executor := range r.executors {
		if executor.Available() {
			return executor, nil
		}
	}

	return nil, fmt.Errorf("no available executors found")
}

// Global registry instance
var globalRegistry = NewRegistry()

// Register adds an executor to the global registry
func Register(executor Executor) error {
	return globalRegistry.Register(executor)
}

// Get retrieves an executor from the global registry
func Get(name string) (Executor, error) {
	return globalRegistry.Get(name)
}

// GetDefault returns the default executor from the global registry
func GetDefault() (Executor, error) {
	return globalRegistry.GetDefault()
}

// SetDefault sets the default executor in the global registry
func SetDefault(name string) error {
	return globalRegistry.SetDefault(name)
}

// List returns all registered executor names from the global registry
func List() []string {
	return globalRegistry.List()
}

// GetAvailable returns all available executors from the global registry
func GetAvailable() []Executor {
	return globalRegistry.GetAvailable()
}

// GetBest returns the best available executor from the global registry
func GetBest(preferences []string) (Executor, error) {
	return globalRegistry.GetBest(preferences)
}

// init registers the local executor by default
func init() {
	if err := Register(NewLocalExec()); err != nil {
		panic(fmt.Sprintf("Failed to register local executor: %v", err))
	}

	// Register Docker executor with a default image
	// This will be configurable later via configuration
	dockerExec := NewLongRunningDockerExec("alpine:latest")
	if err := Register(dockerExec); err != nil {
		panic(fmt.Sprintf("Failed to register docker executor: %v", err))
	}
}
