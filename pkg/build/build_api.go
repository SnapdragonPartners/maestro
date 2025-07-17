package build

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/logx"
)

// BuildService provides orchestrator-level build execution endpoints
type BuildService struct {
	buildRegistry *Registry
	logger        *logx.Logger
	projectCache  map[string]*ProjectInfo // Cache for backend detection
}

// ProjectInfo caches backend information for a project
type ProjectInfo struct {
	Backend     BuildBackend
	DetectedAt  time.Time
	ProjectRoot string
}

// BuildRequest represents a build operation request
type BuildRequest struct {
	ProjectRoot string            `json:"project_root"`
	Operation   string            `json:"operation"` // "build", "test", "lint", "run"
	Args        []string          `json:"args"`      // Arguments for run operation
	Timeout     int               `json:"timeout"`   // Timeout in seconds
	Context     map[string]string `json:"context"`   // Additional context
}

// BuildResponse represents a build operation response
type BuildResponse struct {
	Success   bool              `json:"success"`
	Backend   string            `json:"backend"`
	Operation string            `json:"operation"`
	Output    string            `json:"output"`
	Duration  time.Duration     `json:"duration"`
	Error     string            `json:"error,omitempty"`
	Metadata  map[string]string `json:"metadata"`
	RequestID string            `json:"request_id"`
}

// NewBuildService creates a new build service
func NewBuildService() *BuildService {
	return &BuildService{
		buildRegistry: NewRegistry(),
		logger:        logx.NewLogger("build-service"),
		projectCache:  make(map[string]*ProjectInfo),
	}
}

// ExecuteBuild executes a build operation and returns the result
func (s *BuildService) ExecuteBuild(ctx context.Context, req *BuildRequest) (*BuildResponse, error) {
	startTime := time.Now()
	requestID := fmt.Sprintf("build-%d", startTime.UnixNano())

	s.logger.Info("Build request %s: %s operation for %s", requestID, req.Operation, req.ProjectRoot)

	// Validate request
	if req.ProjectRoot == "" {
		return &BuildResponse{
			Success:   false,
			Operation: req.Operation,
			Error:     "project_root is required",
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "validation_error"},
		}, fmt.Errorf("project_root is required")
	}

	if req.Operation == "" {
		return &BuildResponse{
			Success:   false,
			Operation: req.Operation,
			Error:     "operation is required",
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "validation_error"},
		}, fmt.Errorf("operation is required")
	}

	// Get or detect backend
	backend, err := s.getBackend(req.ProjectRoot)
	if err != nil {
		return &BuildResponse{
			Success:   false,
			Operation: req.Operation,
			Error:     fmt.Sprintf("backend detection failed: %v", err),
			Duration:  time.Since(startTime),
			RequestID: requestID,
			Metadata:  map[string]string{"error_type": "backend_detection"},
		}, err
	}

	// Set up context with timeout
	timeout := 5 * time.Minute // Default timeout
	if req.Timeout > 0 {
		timeout = time.Duration(req.Timeout) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Capture output
	var outputBuffer strings.Builder

	// Execute operation
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
		// Return error immediately for invalid operations
		return &BuildResponse{
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

	// Build response
	response := &BuildResponse{
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

		// Check for timeout
		if execCtx.Err() == context.DeadlineExceeded {
			response.Metadata["error_type"] = "timeout"
		}
	}

	s.logger.Info("Build request %s completed: success=%t, duration=%v", requestID, response.Success, duration)
	return response, nil
}

// getBackend gets or detects the backend for a project
func (s *BuildService) getBackend(projectRoot string) (BuildBackend, error) {
	// Check cache first
	if info, exists := s.projectCache[projectRoot]; exists {
		// Cache is valid for 5 minutes
		if time.Since(info.DetectedAt) < 5*time.Minute {
			return info.Backend, nil
		}
		// Cache expired, remove it
		delete(s.projectCache, projectRoot)
	}

	// Detect backend
	backend, err := s.buildRegistry.Detect(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to detect backend for %s: %w", projectRoot, err)
	}

	// Cache the result
	s.projectCache[projectRoot] = &ProjectInfo{
		Backend:     backend,
		DetectedAt:  time.Now(),
		ProjectRoot: projectRoot,
	}

	return backend, nil
}

// GetBackendInfo returns information about the detected backend for a project
func (s *BuildService) GetBackendInfo(projectRoot string) (*BackendInfo, error) {
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

// BackendInfo provides information about a detected backend
type BackendInfo struct {
	Name        string    `json:"name"`
	ProjectRoot string    `json:"project_root"`
	DetectedAt  time.Time `json:"detected_at"`
	Operations  []string  `json:"operations"`
}

// ClearCache clears the backend detection cache
func (s *BuildService) ClearCache() {
	s.projectCache = make(map[string]*ProjectInfo)
	s.logger.Info("Backend cache cleared")
}

// GetCacheStatus returns the current cache status
func (s *BuildService) GetCacheStatus() map[string]interface{} {
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
		status["cache_entries"] = append(status["cache_entries"].([]map[string]interface{}), entry)
	}

	return status
}
