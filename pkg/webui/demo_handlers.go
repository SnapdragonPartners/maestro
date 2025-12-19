package webui

import (
	"context"
	"encoding/json"
	"net/http"

	"orchestrator/pkg/demo"
)

// DemoService is the interface for demo operations needed by the web UI.
type DemoService interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
	Restart(ctx context.Context) error
	Rebuild(ctx context.Context) error
	RebuildWithOptions(ctx context.Context, opts demo.RebuildOptions) error
	Status(ctx context.Context) *demo.Status
	GetLogs(ctx context.Context) (string, error)
	IsRunning() bool
	SetWorkspacePath(path string)
}

// isDemoAvailable checks if demo mode is available.
// Returns true only if: demo service is wired AND PM says bootstrap is complete.
// If no availability checker is set, falls back to just checking if service exists.
func (s *Server) isDemoAvailable() bool {
	if s.demoService == nil {
		return false
	}
	// If PM availability checker is wired, use it
	if s.demoAvailabilityChecker != nil {
		return s.demoAvailabilityChecker.IsDemoAvailable()
	}
	// Fallback: if no checker is set, demo is available if service exists
	return true
}

// handleDemoStatus implements GET /api/demo/status.
func (s *Server) handleDemoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if demo service exists (but allow status check even if unavailable)
	if s.demoService == nil {
		// Return unavailable status instead of error
		response := map[string]interface{}{
			"available": false,
			"running":   false,
			"reason":    "Demo service not configured",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Trigger bootstrap check if PM is wired and hasn't checked yet
	if s.demoAvailabilityChecker != nil {
		_ = s.demoAvailabilityChecker.EnsureBootstrapChecked(r.Context())
	}

	// Get status from demo service
	status := s.demoService.Status(r.Context())

	// Determine overall health from services (healthy if any service is running)
	healthy := false
	for i := range status.Services {
		if status.Services[i].Healthy {
			healthy = true
			break
		}
	}

	// Create response that includes availability from PM
	response := map[string]interface{}{
		"available": s.isDemoAvailable(),
		"running":   status.Running,
		"healthy":   healthy,
		"port":      status.Port,
		"url":       status.URL,
		"error":     status.Error,
	}

	// Include port detection info if available
	if status.ContainerPort > 0 {
		response["container_port"] = status.ContainerPort
	}
	if len(status.DetectedPorts) > 0 {
		response["detected_ports"] = status.DetectedPorts
	}
	if len(status.UnreachablePorts) > 0 {
		response["unreachable_ports"] = status.UnreachablePorts
	}
	if status.DiagnosticError != "" {
		response["diagnostic_error"] = status.DiagnosticError
		response["diagnostic_type"] = status.DiagnosticType
	}

	// If not available, add reason
	if !s.isDemoAvailable() {
		response["reason"] = "Bootstrap incomplete - Dockerfile and/or Makefile are missing. Start an interview or upload a spec to bootstrap your project."
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode demo status response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served demo status: available=%v, running=%v", s.isDemoAvailable(), status.Running)
}

// handleDemoStart implements POST /api/demo/start.
func (s *Server) handleDemoStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if !s.isDemoAvailable() {
		http.Error(w, "Demo not available - bootstrap incomplete", http.StatusServiceUnavailable)
		return
	}

	if s.demoService.IsRunning() {
		http.Error(w, "Demo is already running", http.StatusConflict)
		return
	}

	if err := s.demoService.Start(r.Context()); err != nil {
		s.logger.Error("Failed to start demo: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := s.demoService.Status(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Error("Failed to encode demo start response: %v", err)
	}

	s.logger.Info("Demo started via API")
}

// handleDemoStop implements POST /api/demo/stop.
func (s *Server) handleDemoStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Stop should work even if bootstrap becomes incomplete (allow stopping running demo)
	if s.demoService == nil {
		http.Error(w, "Demo service not configured", http.StatusServiceUnavailable)
		return
	}

	if !s.demoService.IsRunning() {
		http.Error(w, "Demo is not running", http.StatusConflict)
		return
	}

	if err := s.demoService.Stop(r.Context()); err != nil {
		s.logger.Error("Failed to stop demo: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"running": false,
		"message": "Demo stopped",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode demo stop response: %v", err)
	}

	s.logger.Info("Demo stopped via API")
}

// handleDemoRestart implements POST /api/demo/restart.
func (s *Server) handleDemoRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Restart should work even if bootstrap becomes incomplete (allow restarting running demo)
	if s.demoService == nil {
		http.Error(w, "Demo service not configured", http.StatusServiceUnavailable)
		return
	}

	if !s.demoService.IsRunning() {
		http.Error(w, "Demo is not running", http.StatusConflict)
		return
	}

	if err := s.demoService.Restart(r.Context()); err != nil {
		s.logger.Error("Failed to restart demo: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := s.demoService.Status(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Error("Failed to encode demo restart response: %v", err)
	}

	s.logger.Info("Demo restarted via API")
}

// handleDemoRebuild implements POST /api/demo/rebuild.
// Accepts optional JSON body: {"skip_detection": true} to use cached port.
func (s *Server) handleDemoRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Rebuild requires availability (need Dockerfile etc to rebuild)
	if !s.isDemoAvailable() {
		http.Error(w, "Demo not available - bootstrap incomplete", http.StatusServiceUnavailable)
		return
	}

	// Parse optional request body for options
	var opts demo.RebuildOptions
	if r.Body != nil && r.ContentLength > 0 {
		var req struct {
			SkipDetection bool `json:"skip_detection"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err == nil {
			opts.SkipDetection = req.SkipDetection
		}
	}

	if err := s.demoService.RebuildWithOptions(r.Context(), opts); err != nil {
		s.logger.Error("Failed to rebuild demo: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := s.demoService.Status(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Error("Failed to encode demo rebuild response: %v", err)
	}

	s.logger.Info("Demo rebuilt via API (skip_detection=%v)", opts.SkipDetection)
}

// handleDemoLogs implements GET /api/demo/logs.
func (s *Server) handleDemoLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
		return
	}

	logs, err := s.demoService.GetLogs(r.Context())
	if err != nil {
		s.logger.Error("Failed to get demo logs: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	response := map[string]interface{}{
		"logs": logs,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode demo logs response: %v", err)
	}

	s.logger.Debug("Served demo logs: %d bytes", len(logs))
}
