// Package build provides a service for executing build commands across different backend types.
// It supports Go, Node.js, Python, Make-based, and null backend implementations for MCP tools.
package build

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// DefaultExecDir is the default working directory inside containers.
const DefaultExecDir = "/workspace"

// Service provides orchestrator-level build execution endpoints.
type Service struct {
	buildRegistry *Registry
	logger        *logx.Logger
	projectCache  map[string]*ProjectInfo // Cache for backend detection
	executor      Executor                // Command executor (container or host)
}

// ProjectInfo caches backend information for a project.
type ProjectInfo struct {
	Backend     Backend
	DetectedAt  time.Time
	ProjectRoot string
}

// Request represents a build operation request.
//
//nolint:govet // JSON serialization struct, logical order preferred
type Request struct {
	ProjectRoot string            `json:"project_root"`
	Operation   string            `json:"operation"` // "build", "test", "lint", "run"
	Args        []string          `json:"args"`      // Arguments for run operation
	Timeout     int               `json:"timeout"`   // Timeout in seconds
	Context     map[string]string `json:"context"`   // Additional context
	ExecDir     string            `json:"exec_dir"`  // Execution directory (container path), defaults to /workspace
}

// Response represents a build operation response.
//
//nolint:govet // JSON serialization struct, logical order preferred
type Response struct {
	Success   bool              `json:"success"`
	Backend   string            `json:"backend"`
	Operation string            `json:"operation"`
	Output    string            `json:"output"`
	Duration  time.Duration     `json:"duration"`
	Error     string            `json:"error,omitempty"`
	Metadata  map[string]string `json:"metadata"`
	RequestID string            `json:"request_id"`
}

// NewBuildService creates a new build service.
// IMPORTANT: You must call SetExecutor() before using the service for build operations.
// Build operations should use ContainerExecutor to run in containers.
// For tests, use a MockExecutor.
func NewBuildService() *Service {
	return &Service{
		buildRegistry: NewRegistry(),
		logger:        logx.NewLogger("build-service"),
		projectCache:  make(map[string]*ProjectInfo),
		executor:      nil, // Must be set via SetExecutor before use
	}
}

// SetExecutor sets the command executor for the build service.
// Use NewContainerExecutor for container execution (recommended for production).
// Use NewHostExecutor for host execution (testing/migration only).
func (s *Service) SetExecutor(exec Executor) {
	// Only log if executor is actually changing
	if s.executor == nil || s.executor.Name() != exec.Name() {
		s.logger.Info("Build service executor set to: %s", exec.Name())
	}
	s.executor = exec
}

// GetExecutor returns the current executor.
func (s *Service) GetExecutor() Executor {
	return s.executor
}

// ExecuteBuild executes a build operation and returns the result.
//
//nolint:cyclop // Complexity slightly over limit due to comprehensive validation and executor selection.
func (s *Service) ExecuteBuild(ctx context.Context, req *Request) (*Response, error) {
	startTime := time.Now()
	requestID := fmt.Sprintf("build-%d", startTime.UnixNano())

	s.logger.Info("Build request %s: %s operation for %s", requestID, req.Operation, req.ProjectRoot)

	// Validate request.
	if req.ProjectRoot == "" {
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     "project_root is required",
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "validation_error"},
		}, fmt.Errorf("project_root is required")
	}

	if req.Operation == "" {
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     "operation is required",
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "validation_error"},
		}, fmt.Errorf("operation is required")
	}

	// Verify executor is configured.
	if s.executor == nil {
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     "executor not configured - call SetExecutor first",
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "configuration_error"},
		}, fmt.Errorf("executor not configured - call SetExecutor with ContainerExecutor or MockExecutor")
	}

	// Normalize project root for cache consistency.
	normalizedRoot, err := filepath.Abs(req.ProjectRoot)
	if err != nil {
		normalizedRoot = req.ProjectRoot
	}
	if resolved, resolveErr := filepath.EvalSymlinks(normalizedRoot); resolveErr == nil {
		normalizedRoot = resolved
	}

	// Get or detect backend using normalized path.
	backend, err := s.getBackend(normalizedRoot)
	if err != nil {
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     fmt.Sprintf("backend detection failed: %v", err),
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "backend_detection"},
		}, err
	}

	// Set up context with timeout.
	timeout := 5 * time.Minute // Default timeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Capture output.
	var outputBuffer strings.Builder

	// Determine execution directory based on executor type.
	// For container execution: use ExecDir (defaults to /workspace)
	// For host execution: use the normalized project root (host path)
	execDir := req.ExecDir
	isHostExec := isHostExecutor(s.executor)

	if execDir == "" {
		// Default based on executor type.
		if isHostExec {
			execDir = normalizedRoot
		} else {
			execDir = DefaultExecDir
		}
	}

	// Validate ExecDir based on executor type to prevent misconfigurations.
	if validationErr := validateExecDir(execDir, isHostExec); validationErr != nil {
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     validationErr.Error(),
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "validation_error"},
		}, validationErr
	}

	// Execute operation.
	var operationErr error
	switch req.Operation {
	case "build":
		operationErr = backend.Build(execCtx, s.executor, execDir, &outputBuffer)
	case "test":
		operationErr = backend.Test(execCtx, s.executor, execDir, &outputBuffer)
	case "lint":
		operationErr = backend.Lint(execCtx, s.executor, execDir, &outputBuffer)
	case "run":
		operationErr = backend.Run(execCtx, s.executor, execDir, req.Args, &outputBuffer)
	default:
		operationErr = fmt.Errorf("unknown operation: %s", req.Operation)
		// Return error immediately for invalid operations.
		return &Response{
			Success:   false,
			Operation: req.Operation,
			Error:     operationErr.Error(),
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "invalid_operation"},
		}, operationErr
	}

	duration := time.Since(startTime)
	output := outputBuffer.String()

	// Build response.
	response := &Response{
		Success:   operationErr == nil,
		Backend:   backend.Name(),
		Operation: req.Operation,
		Output:    output,
		Duration:  duration,
		RequestID: requestID,
		Metadata: map[string]string{
			"backend":     backend.Name(),
			"duration_ms": fmt.Sprintf("%d", duration.Milliseconds()),
			"project":     req.ProjectRoot,
			"executor":    s.executor.Name(),
		},
	}

	if operationErr != nil {
		response.Error = operationErr.Error()
		response.Metadata["error_type"] = "operation_failed"

		// Check for timeout.
		if execCtx.Err() == context.DeadlineExceeded {
			response.Metadata["error_type"] = "timeout"
		}
	}

	s.logger.Info("Build request %s completed: success=%t, duration=%v, executor=%s",
		requestID, response.Success, duration, s.executor.Name())
	return response, nil
}

// getBackend gets or detects the backend for a project.
func (s *Service) getBackend(projectRoot string) (Backend, error) {
	// Check cache first.
	if info, exists := s.projectCache[projectRoot]; exists {
		// Cache is valid for 5 minutes.
		if time.Since(info.DetectedAt) < 5*time.Minute {
			return info.Backend, nil
		}
		// Cache expired, remove it.
		delete(s.projectCache, projectRoot)
	}

	// Detect backend.
	backend, err := s.buildRegistry.Detect(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to detect backend for %s: %w", projectRoot, err)
	}

	// Cache the result, but NOT NullBackend.
	// NullBackend is for empty repos - if files are added we want to re-detect
	// on the next call rather than continuing to report success for 5 minutes.
	if backend.Name() != "null" {
		s.projectCache[projectRoot] = &ProjectInfo{
			Backend:     backend,
			DetectedAt:  time.Now(),
			ProjectRoot: projectRoot,
		}
	}

	return backend, nil
}

// GetBackendInfo returns information about the detected backend for a project.
func (s *Service) GetBackendInfo(projectRoot string) (*BackendInfo, error) {
	backend, err := s.getBackend(projectRoot)
	if err != nil {
		return nil, err
	}

	return &BackendInfo{
		Name:        backend.Name(),
		ProjectRoot: projectRoot,
		DetectedAt:  time.Now(),
		Operations:  []string{"build", "test", "lint", "run"},
	}, nil
}

// BackendInfo provides information about a detected backend.
type BackendInfo struct {
	Name        string    `json:"name"`
	ProjectRoot string    `json:"project_root"`
	DetectedAt  time.Time `json:"detected_at"`
	Operations  []string  `json:"operations"`
}

// ClearCache clears the backend detection cache.
func (s *Service) ClearCache() {
	s.projectCache = make(map[string]*ProjectInfo)
	s.logger.Info("Backend cache cleared")
}

// GetCacheStatus returns the current cache status.
func (s *Service) GetCacheStatus() map[string]interface{} {
	status := map[string]interface{}{
		"cache_size":    len(s.projectCache),
		"cache_entries": make([]map[string]interface{}, 0),
	}

	for projectRoot, info := range s.projectCache {
		entry := map[string]interface{}{
			"project_root": projectRoot,
			"backend":      info.Backend.Name(),
			"detected_at":  info.DetectedAt.Format(time.RFC3339),
			"age_seconds":  time.Since(info.DetectedAt).Seconds(),
		}
		if cacheEntries, err := utils.GetMapField[[]map[string]interface{}](status, "cache_entries"); err == nil {
			status["cache_entries"] = append(cacheEntries, entry)
		}
	}

	return status
}

// runMakeTarget is a helper that executes a make target using the provided executor.
// This is used by backends that delegate to Makefiles.
func runMakeTarget(ctx context.Context, exec Executor, execDir string, stream io.Writer, target string) error {
	opts := ExecOpts{
		Dir:    execDir,
		Stdout: stream,
		Stderr: stream,
	}

	exitCode, err := exec.Run(ctx, []string{"make", target}, opts)
	if err != nil {
		return fmt.Errorf("make %s execution failed: %w", target, err)
	}

	if exitCode != 0 {
		return fmt.Errorf("make %s failed with exit code %d", target, exitCode)
	}

	return nil
}

// isHostExecutor returns true if the executor runs commands on the host.
// Uses type switch for reliable detection instead of string comparison.
func isHostExecutor(exec Executor) bool {
	switch exec.(type) {
	case *HostExecutor:
		return true
	case *MockExecutor:
		// MockExecutor simulates host execution for testing
		return true
	default:
		return false
	}
}

// validateExecDir validates that ExecDir is appropriate for the executor type.
// This prevents misconfigurations like passing container paths to host executors.
func validateExecDir(execDir string, isHost bool) error {
	if execDir == "" {
		return nil // Will be set to default
	}

	// Must be absolute path
	if !filepath.IsAbs(execDir) {
		return fmt.Errorf("execDir must be absolute path, got: %s", execDir)
	}

	if isHost {
		// For host executor: reject obvious container paths like /workspace.
		if execDir == DefaultExecDir {
			return fmt.Errorf("execDir '%s' looks like a container path but executor is host-based; "+
				"use project root path instead", execDir)
		}
	} else {
		// For container executor: execDir should be a container path (e.g., /workspace).
		// Reject paths that look like host paths (contains /Users/, /home/, C:\, etc.)
		if strings.Contains(execDir, "/Users/") ||
			strings.Contains(execDir, "/home/") ||
			strings.Contains(execDir, ":\\") {
			return fmt.Errorf("execDir '%s' looks like a host path but executor is container-based; "+
				"use container path like /workspace instead", execDir)
		}
	}

	return nil
}
