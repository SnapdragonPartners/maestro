package build

import (
	"context"
	"io"
)

// BuildBackend defines the interface for different build system backends
type BuildBackend interface {
	// Name returns the backend name for logging and identification
	Name() string
	
	// Detect determines if this backend applies to the given project root
	Detect(root string) bool
	
	// Build executes the build process for the project
	Build(ctx context.Context, root string, stream io.Writer) error
	
	// Test executes the test suite for the project
	Test(ctx context.Context, root string, stream io.Writer) error
	
	// Lint executes linting checks for the project
	Lint(ctx context.Context, root string, stream io.Writer) error
	
	// Run executes the application with provided arguments
	Run(ctx context.Context, root string, args []string, stream io.Writer) error
}

// BackendPriority defines the priority order for backend detection
type BackendPriority int

const (
	// PriorityHigh is for specific project types (go.mod, package.json, etc.)
	PriorityHigh BackendPriority = 100
	
	// PriorityMedium is for generic build files (Makefile, build.sh, etc.)
	PriorityMedium BackendPriority = 50
	
	// PriorityLow is for fallback backends (NullBackend)
	PriorityLow BackendPriority = 10
)

// BackendRegistration combines a backend with its priority
type BackendRegistration struct {
	Backend  BuildBackend
	Priority BackendPriority
}