// Package build provides a service for executing build commands across different backend types.
// It supports Go, Node.js, Python, Make-based, and null backend implementations for MCP tools.
package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// Service provides orchestrator-level build execution endpoints.
type Service struct {
	buildRegistry *Registry
	logger        *logx.Logger
	projectCache  map[string]*ProjectInfo // Cache for backend detection
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
func NewBuildService() *Service {
	return &Service{
		buildRegistry: NewRegistry(),
		logger:        logx.NewLogger("build-service"),
		projectCache:  make(map[string]*ProjectInfo),
	}
}

// ExecuteBuild executes a build operation and returns the result.
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

	// Get or detect backend.
	backend, err := s.getBackend(req.ProjectRoot)
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

	// Execute operation.
	var operationErr error
	switch req.Operation {
	case "build":
		operationErr = backend.Build(execCtx, req.ProjectRoot, &outputBuffer)
	case "test":
		operationErr = backend.Test(execCtx, req.ProjectRoot, &outputBuffer)
	case "lint":
		operationErr = backend.Lint(execCtx, req.ProjectRoot, &outputBuffer)
	case "run":
		operationErr = backend.Run(execCtx, req.ProjectRoot, req.Args, &outputBuffer)
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

	s.logger.Info("Build request %s completed: success=%t, duration=%v", requestID, response.Success, duration)
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

	// Cache the result.
	s.projectCache[projectRoot] = &ProjectInfo{
		Backend:     backend,
		DetectedAt:  time.Now(),
		ProjectRoot: projectRoot,
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

// runMakeCommand executes a make command with the given target - shared utility for all backends.
func runMakeCommand(ctx context.Context, root string, stream io.Writer, target string) error {
	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = root
	cmd.Stdout = stream
	cmd.Stderr = stream

	_, _ = fmt.Fprintf(stream, "$ make %s\n", target)

	if err := cmd.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return fmt.Errorf("make %s failed with exit code %d", target, exitError.ExitCode())
		}
		return fmt.Errorf("make %s failed: %w", target, err)
	}

	return nil
}
