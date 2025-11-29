package webui

import (
	"encoding/json"
	"net/http"

	"orchestrator/pkg/demo"
)

// DemoService is the interface for demo operations needed by the web UI.
type DemoService interface {
	Start(ctx interface{ Done() <-chan struct{} }) error
	Stop(ctx interface{ Done() <-chan struct{} }) error
	Restart(ctx interface{ Done() <-chan struct{} }) error
	Rebuild(ctx interface{ Done() <-chan struct{} }) error
	Status(ctx interface{ Done() <-chan struct{} }) *demo.Status
	GetLogs(ctx interface{ Done() <-chan struct{} }) (string, error)
	IsRunning() bool
	SetWorkspacePath(path string)
}

// handleDemoStatus implements GET /api/demo/status.
func (s *Server) handleDemoStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
		return
	}

	status := s.demoService.Status(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Error("Failed to encode demo status response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served demo status: running=%v", status.Running)
}

// handleDemoStart implements POST /api/demo/start.
func (s *Server) handleDemoStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
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

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
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

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
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
func (s *Server) handleDemoRebuild(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.demoService == nil {
		http.Error(w, "Demo service not available", http.StatusServiceUnavailable)
		return
	}

	if err := s.demoService.Rebuild(r.Context()); err != nil {
		s.logger.Error("Failed to rebuild demo: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	status := s.demoService.Status(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(status); err != nil {
		s.logger.Error("Failed to encode demo rebuild response: %v", err)
	}

	s.logger.Info("Demo rebuilt via API")
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
