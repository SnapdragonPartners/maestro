// Package webui provides a web-based user interface for monitoring and interacting with the orchestrator system.
package webui

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// Server represents the web UI HTTP server.
type Server struct {
	dispatcher *dispatch.Dispatcher
	store      *state.Store
	logger     *logx.Logger
	workDir    string
	templates  *template.Template
}

// AgentListItem represents an agent in the list response.
type AgentListItem struct {
	ID     string    `json:"id"`
	Role   string    `json:"role"`
	State  string    `json:"state"`
	LastTS time.Time `json:"last_ts"`
}

// NewServer creates a new web UI server.
func NewServer(dispatcher *dispatch.Dispatcher, store *state.Store, workDir string) *Server {
	// Load templates.
	templates, err := template.ParseGlob("web/templates/*.html")
	if err != nil {
		// If templates not found, use embedded or fallback.
		templates = template.New("fallback")
		// Add a basic fallback template.
		if _, err := templates.New("base.html").Parse(`
		<!DOCTYPE html>
		<html><head><title>Maestro - Fallback</title></head>
		<body><h1>Maestro Web UI</h1><p>Template loading failed: {{.Error}}</p></body>
		</html>`); err != nil {
			// If even the fallback template fails, log the error.
			// This is a programming error, not a runtime error.
			panic(fmt.Sprintf("Failed to parse fallback template: %v", err))
		}
	}

	return &Server{
		dispatcher: dispatcher,
		store:      store,
		logger:     logx.NewLogger("webui"),
		workDir:    workDir,
		templates:  templates,
	}
}

// RegisterRoutes sets up HTTP routes for the API.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Web UI routes.
	mux.HandleFunc("/", s.handleDashboard)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("./web/static/"))))

	// API endpoints.
	mux.HandleFunc("/api/agents", s.handleAgents)
	mux.HandleFunc("/api/agent/", s.handleAgent)
	mux.HandleFunc("/api/queues", s.handleQueues)
	mux.HandleFunc("/api/upload", s.handleUpload)
	mux.HandleFunc("/api/answer", s.handleAnswer)
	mux.HandleFunc("/api/shutdown", s.handleShutdown)
	mux.HandleFunc("/api/logs", s.handleLogs)
	mux.HandleFunc("/api/healthz", s.handleHealth)
}

// handleAgents implements GET /api/agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Build response list.
	agents := make([]AgentListItem, 0)

	// Get agent info from dispatcher.
	if s.dispatcher != nil {
		registeredAgents := s.dispatcher.GetRegisteredAgents()

		for i := range registeredAgents {
			agentInfo := &registeredAgents[i]
			var currentState string
			var lastTS time.Time

			if agentInfo.Driver != nil {
				// Use driver's current state.
				currentState = agentInfo.State
				lastTS = time.Now() // Use current time for live agents
			} else {
				// Fallback to store if driver not available.
				agentState, err := s.store.GetStateInfo(agentInfo.ID)
				if err != nil {
					currentState = "WAITING"
					lastTS = time.Now()
				} else {
					currentState = agentState.State
					lastTS = agentState.LastTimestamp
				}
			}

			agents = append(agents, AgentListItem{
				ID:     agentInfo.ID,
				Role:   agentInfo.Type.String(),
				State:  currentState,
				LastTS: lastTS,
			})
		}
	}

	// Sort by ID for consistent ordering.
	sort.Slice(agents, func(i, j int) bool {
		return agents[i].ID < agents[j].ID
	})

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(agents); err != nil {
		s.logger.Error("Failed to encode agents response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served agents list: %d agents", len(agents))
}

// handleAgent implements GET /api/agent/:id.
func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Extract agent ID from URL path.
	path := strings.TrimPrefix(r.URL.Path, "/api/agent/")
	if path == "" {
		http.Error(w, "Agent ID required", http.StatusBadRequest)
		return
	}

	agentID := path

	// Get agent state.
	agentState, err := s.store.GetStateInfo(agentID)
	if err != nil {
		s.logger.Warn("Agent not found: %s", agentID)
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(agentState); err != nil {
		s.logger.Error("Failed to encode agent response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served agent details: %s", agentID)
}

// handleHealth implements GET /api/healthz.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := map[string]string{
		"status":  "ok",
		"version": "v1.0",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode health response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

// handleQueues implements GET /api/queues.
func (s *Server) handleQueues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dispatcher == nil {
		http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		return
	}

	// Get queue information with up to 25 heads per queue.
	queueInfo := s.dispatcher.DumpHeads(25)

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(queueInfo); err != nil {
		s.logger.Error("Failed to encode queues response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served queue information")
}

// handleUpload implements POST /api/upload.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form first (validate request format)
	if err := r.ParseMultipartForm(100 << 10); err != nil { // 100 KB limit
		s.logger.Warn("Failed to parse multipart form: %v", err)
		http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		return
	}

	// Get the uploaded file.
	file, header, err := r.FormFile("file")
	if err != nil {
		s.logger.Warn("No file found in upload: %v", err)
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			s.logger.Warn("Failed to close uploaded file: %v", closeErr)
		}
	}()

	// Check file size (100 KB limit)
	if header.Size > 100*1024 {
		s.logger.Warn("File too large: %d bytes", header.Size)
		http.Error(w, "File too large (max 100 KB)", http.StatusBadRequest)
		return
	}

	// Check file extension.
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".md") {
		s.logger.Warn("Invalid file extension: %s", header.Filename)
		http.Error(w, "Only .md files are allowed", http.StatusBadRequest)
		return
	}

	// Check if we have a dispatcher (only after validating file)
	if s.dispatcher == nil {
		http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		return
	}

	// No need to check architect state - let the dispatcher handle routing.

	// Create stories directory if it doesn't exist.
	storiesDir := filepath.Join(s.workDir, "stories")
	if mkdirErr := os.MkdirAll(storiesDir, 0755); mkdirErr != nil {
		s.logger.Error("Failed to create stories directory: %v", mkdirErr)
		http.Error(w, "Failed to create stories directory", http.StatusInternalServerError)
		return
	}

	// Read file content.
	content, err := io.ReadAll(file)
	if err != nil {
		s.logger.Error("Failed to read uploaded file: %v", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Save file to stories directory.
	filePath := filepath.Join(storiesDir, header.Filename)
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		s.logger.Error("Failed to save file: %v", err)
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}

	// Create and send SPEC message to architect (use logical name "architect")
	// The dispatcher will resolve this to the actual architect agent.
	msg := proto.NewAgentMsg(proto.MsgTypeSPEC, "web-ui", "architect")
	msg.SetPayload("type", "spec_upload")
	msg.SetPayload("filename", header.Filename)
	msg.SetPayload("filepath", filePath)
	msg.SetPayload("size", header.Size)
	msg.SetPayload("content", string(content)) // Add the actual file content

	s.logger.Info("Dispatching SPEC message %s to architect", msg.ID)
	if err := s.dispatcher.DispatchMessage(msg); err != nil {
		s.logger.Error("Failed to dispatch upload message: %v", err)
		// Don't delete the file, but return error.
		http.Error(w, "Failed to notify architect", http.StatusInternalServerError)
		return
	}

	// Return success.
	w.WriteHeader(http.StatusCreated)
	response := map[string]any{
		"message":  "File uploaded successfully",
		"filename": header.Filename,
		"size":     header.Size,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode upload response: %v", err)
	}

	s.logger.Info("Successfully uploaded file: %s (%d bytes)", header.Filename, header.Size)
}

// handleDashboard serves the main dashboard page.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Only serve dashboard for root path.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	data := map[string]interface{}{
		"Title": "Dashboard",
	}

	if err := s.templates.ExecuteTemplate(w, "base.html", data); err != nil {
		s.logger.Error("Failed to render dashboard template: %v", err)
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
		return
	}
}

func (s *Server) handleAnswer(w http.ResponseWriter, _ *http.Request) {
	http.Error(w, "Not implemented yet", http.StatusNotImplemented)
}

// handleShutdown implements POST /api/shutdown.
func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.dispatcher == nil {
		http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		return
	}

	// Call dispatcher.Stop() with a context timeout
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	go func() {
		if err := s.dispatcher.Stop(ctx); err != nil {
			s.logger.Error("Failed to stop dispatcher: %v", err)
		} else {
			s.logger.Info("Dispatcher stopped successfully via API")
		}
	}()

	// Return 202 Accepted immediately.
	w.WriteHeader(http.StatusAccepted)
	response := map[string]string{
		"message": "Shutdown initiated",
		"status":  "accepted",
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode shutdown response: %v", err)
	}

	s.logger.Info("Shutdown request accepted")
}

// handleLogs implements GET /api/logs.
func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters.
	query := r.URL.Query()
	domain := query.Get("domain")
	sinceStr := query.Get("since")

	var since time.Time
	var err error
	if sinceStr != "" {
		since, err = time.Parse(time.RFC3339, sinceStr)
		if err != nil {
			s.logger.Warn("Invalid since parameter: %s", sinceStr)
			http.Error(w, "Invalid since parameter (use RFC3339)", http.StatusBadRequest)
			return
		}
	}

	// Get logs from in-memory buffer first.
	logs := logx.GetRecentLogEntries(domain, since)

	// If no current logs, fall back to log files.
	if len(logs) == 0 {
		logs = s.readLogFiles(domain, since)
	}

	// If still no logs, create some sample logs to show the UI is working.
	if len(logs) == 0 {
		logs = []logx.LogEntry{
			{
				Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z"),
				AgentID:   "orchestrator",
				Level:     "INFO",
				Message:   "Web UI logs endpoint is working",
				Domain:    "webui",
			},
		}
	}

	// Limit to 1000 newest lines.
	if len(logs) > 1000 {
		logs = logs[len(logs)-1000:]
	}

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(logs); err != nil {
		s.logger.Error("Failed to encode logs response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served %d log entries (domain=%s, since=%s)", len(logs), domain, sinceStr)
}

// readLogFiles reads logs from the debug log files.
func (s *Server) readLogFiles(domain string, since time.Time) []logx.LogEntry {
	// Check if debug file logging is enabled.
	if !s.isDebugFileLoggingEnabled() {
		// If file logging not enabled, try to find any log files in workdir/logs.
		return s.readWorkdirLogs(domain, since)
	}

	// Read from debug log directory.
	logDir := s.getDebugLogDir()
	return s.readLogsFromDirectory(logDir, domain, since)
}

// readWorkdirLogs reads logs from <workdir>/logs/run.log or similar.
func (s *Server) readWorkdirLogs(domain string, since time.Time) []logx.LogEntry {
	logDir := filepath.Join(s.workDir, "logs")

	// Look for run.log first, then any .log files.
	patterns := []string{"run.log", "*.log"}

	for _, pattern := range patterns {
		files, err := filepath.Glob(filepath.Join(logDir, pattern))
		if err != nil {
			continue
		}

		if len(files) > 0 {
			return s.readLogsFromFiles(files, domain, since)
		}
	}

	return []logx.LogEntry{}
}

// readLogsFromDirectory reads all log files from a directory.
func (s *Server) readLogsFromDirectory(logDir, domain string, since time.Time) []logx.LogEntry {
	files, err := filepath.Glob(filepath.Join(logDir, "*.log"))
	if err != nil {
		s.logger.Warn("Failed to glob log files in %s: %v", logDir, err)
		return []logx.LogEntry{}
	}

	return s.readLogsFromFiles(files, domain, since)
}

// readLogsFromFiles reads and parses logs from multiple files.
func (s *Server) readLogsFromFiles(files []string, domain string, since time.Time) []logx.LogEntry {
	var allLogs []logx.LogEntry

	for _, file := range files {
		logs := s.readLogFile(file, domain, since)
		allLogs = append(allLogs, logs...)
	}

	// Sort by timestamp.
	sort.Slice(allLogs, func(i, j int) bool {
		return allLogs[i].Timestamp < allLogs[j].Timestamp
	})

	return allLogs
}

// readLogFile reads and parses a single log file.
func (s *Server) readLogFile(filename, domain string, since time.Time) []logx.LogEntry {
	file, err := os.Open(filename)
	if err != nil {
		s.logger.Warn("Failed to open log file %s: %v", filename, err)
		return []logx.LogEntry{}
	}
	defer func() {
		if err := file.Close(); err != nil {
			s.logger.Warn("Failed to close uploaded file: %v", err)
		}
	}()

	var logs []logx.LogEntry
	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		entry := s.parseLogLine(line)
		if entry == nil {
			continue
		}

		// Filter by domain if specified.
		if domain != "" && !s.matchesDomain(entry, domain) {
			continue
		}

		// Filter by timestamp if specified.
		if !since.IsZero() {
			entryTime, err := time.Parse("2006-01-02T15:04:05.000Z", entry.Timestamp)
			if err != nil || entryTime.Before(since) {
				continue
			}
		}

		logs = append(logs, *entry)
	}

	if err := scanner.Err(); err != nil {
		s.logger.Warn("Error scanning log file %s: %v", filename, err)
	}

	return logs
}

// parseLogLine parses a log line in the format: [timestamp] [agentID] LEVEL: message.
func (s *Server) parseLogLine(line string) *logx.LogEntry {
	// Expected format: [2006-01-02T15:04:05.000Z] [agentID] LEVEL: message.
	if !strings.HasPrefix(line, "[") {
		return nil
	}

	// Find first closing bracket (timestamp)
	end1 := strings.Index(line, "]")
	if end1 == -1 {
		return nil
	}

	timestamp := line[1:end1]
	remaining := line[end1+1:]

	// Find second opening bracket (agentID)
	start2 := strings.Index(remaining, "[")
	if start2 == -1 {
		return nil
	}

	remaining = remaining[start2:]
	end2 := strings.Index(remaining, "]")
	if end2 == -1 {
		return nil
	}

	agentID := remaining[1:end2]
	remaining = remaining[end2+1:]

	// Find level and message (format: " LEVEL: message")
	remaining = strings.TrimSpace(remaining)
	colonIndex := strings.Index(remaining, ":")
	if colonIndex == -1 {
		return nil
	}

	level := strings.TrimSpace(remaining[:colonIndex])
	message := strings.TrimSpace(remaining[colonIndex+1:])

	// Extract domain from message if it's in [domain] format.
	domain := ""
	if strings.HasPrefix(message, "[") {
		if endBracket := strings.Index(message, "]"); endBracket != -1 {
			domain = message[1:endBracket]
			message = strings.TrimSpace(message[endBracket+1:])
		}
	}

	return &logx.LogEntry{
		Timestamp: timestamp,
		AgentID:   agentID,
		Level:     level,
		Message:   message,
		Domain:    domain,
	}
}

// matchesDomain checks if a log entry matches the domain filter.
func (s *Server) matchesDomain(entry *logx.LogEntry, domain string) bool {
	// Check explicit domain field.
	if entry.Domain != "" {
		return strings.EqualFold(entry.Domain, domain)
	}

	// Check if agentID contains domain.
	return strings.Contains(strings.ToLower(entry.AgentID), strings.ToLower(domain))
}

// Helper functions for debug logging configuration.
func (s *Server) isDebugFileLoggingEnabled() bool {
	// This would need to access logx internal state.
	// For now, check environment variable.
	debugFile := os.Getenv("DEBUG_FILE")
	return debugFile == "1" || strings.EqualFold(debugFile, "true")
}

func (s *Server) getDebugLogDir() string {
	if debugLogDir := os.Getenv("DEBUG_LOG_DIR"); debugLogDir != "" {
		return debugLogDir
	}
	if debugDir := os.Getenv("DEBUG_DIR"); debugDir != "" {
		return debugDir
	}
	// Default to project root + logs.
	return filepath.Join(s.workDir, "..", "logs")
}

// StartServer starts the HTTP server on the specified port.
func (s *Server) StartServer(ctx context.Context, port int) error {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	server := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	s.logger.Info("Starting web UI server on port %d", port)

	// Start server in a goroutine.
	serverDone := make(chan error, 1)
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Error("Server error: %v", err)
			serverDone <- err
		} else {
			serverDone <- nil
		}
	}()

	// Wait for either context cancellation or server error.
	select {
	case err := <-serverDone:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		// Graceful shutdown.
		s.logger.Info("Shutting down web UI server")
		shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("HTTP server shutdown failed: %w", err)
		}
		return nil
	}

	return nil
}

// findArchitectState finds the state of the architect agent.
func (s *Server) findArchitectState() (*state.AgentState, error) {
	// List all agents.
	agents, err := s.store.ListAgents()
	if err != nil {
		return nil, fmt.Errorf("failed to list agents: %w", err)
	}

	// Find the architect agent.
	for _, agentID := range agents {
		if strings.HasPrefix(agentID, "architect:") {
			agentState, err := s.store.GetStateInfo(agentID)
			if err != nil {
				return nil, fmt.Errorf("failed to get architect state: %w", err)
			}
			return agentState, nil
		}
	}

	// No architect found.
	return nil, fmt.Errorf("no architect agent found")
}
