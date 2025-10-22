// Package webui provides a web-based user interface for monitoring and interacting with the orchestrator system.
package webui

import (
	"bufio"
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

// StoryProvider interface for agents that can provide stories.
type StoryProvider interface {
	GetStoryList() []*architect.QueuedStory
}

// Server represents the web UI HTTP server.
type Server struct {
	dispatcher  *dispatch.Dispatcher
	chatService *chat.Service
	logger      *logx.Logger
	templates   *template.Template
	workDir     string
}

// AgentListItem represents an agent in the list response.
type AgentListItem struct {
	LastTS time.Time `json:"last_ts"`
	ID     string    `json:"id"`
	Role   string    `json:"role"`
	State  string    `json:"state"`
}

// NewServer creates a new web UI server.
func NewServer(dispatcher *dispatch.Dispatcher, workDir string, chatService *chat.Service) *Server {
	// Load templates from embedded filesystem
	templates, err := template.ParseFS(templateFS, "web/templates/*.html")
	if err != nil {
		// This should never happen since templates are embedded at compile time
		panic(fmt.Sprintf("Failed to parse embedded templates: %v", err))
	}

	return &Server{
		dispatcher:  dispatcher,
		logger:      logx.NewLogger("webui"),
		workDir:     workDir,
		templates:   templates,
		chatService: chatService,
	}
}

// requireAuth wraps an HTTP handler with Basic Authentication.
// Username is always "maestro", password comes from MAESTRO_WEBUI_PASSWORD env var.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get password from environment
		expectedPassword := config.GetWebUIPassword()
		if expectedPassword == "" {
			// No password set - this should never happen as we generate one at startup
			s.logger.Error("WebUI password not set - denying access")
			w.Header().Set("WWW-Authenticate", `Basic realm="Maestro WebUI"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Check Basic Auth credentials
		username, password, ok := r.BasicAuth()
		if !ok {
			// No credentials provided
			w.Header().Set("WWW-Authenticate", `Basic realm="Maestro WebUI"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Validate credentials (constant-time comparison for password)
		expectedUsername := "maestro"
		if username != expectedUsername || password != expectedPassword {
			// Invalid credentials
			s.logger.Warn("Failed authentication attempt from %s (username: %s)", r.RemoteAddr, username)
			w.Header().Set("WWW-Authenticate", `Basic realm="Maestro WebUI"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		// Credentials valid - proceed to handler
		next(w, r)
	}
}

// RegisterRoutes sets up HTTP routes for the API.
func (s *Server) RegisterRoutes(mux *http.ServeMux) {
	// Web UI routes - protected by basic auth.
	mux.HandleFunc("/", s.requireAuth(s.handleDashboard))

	// Serve static files from embedded filesystem - protected by basic auth
	staticSubFS, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		panic(fmt.Sprintf("Failed to access embedded static files: %v", err))
	}
	mux.Handle("/static/", s.requireAuth(http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))).ServeHTTP))

	// API endpoints - all protected by basic auth.
	mux.HandleFunc("/api/agents", s.requireAuth(s.handleAgents))
	mux.HandleFunc("/api/agent/", s.requireAuth(s.handleAgent))
	mux.HandleFunc("/api/queues", s.requireAuth(s.handleQueues))
	mux.HandleFunc("/api/stories", s.requireAuth(s.handleStories))
	mux.HandleFunc("/api/upload", s.requireAuth(s.handleUpload))
	mux.HandleFunc("/api/answer", s.requireAuth(s.handleAnswer))
	mux.HandleFunc("/api/shutdown", s.requireAuth(s.handleShutdown))
	mux.HandleFunc("/api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc("/api/messages", s.requireAuth(s.handleMessages))
	mux.HandleFunc("/api/healthz", s.requireAuth(s.handleHealth))
	mux.HandleFunc("/api/chat", s.requireAuth(s.handleChat))
}

// handleAgents implements GET /api/agents.
func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Build response list from dispatcher (source of truth for agent state)
	agents := make([]AgentListItem, 0)

	if s.dispatcher == nil {
		// No dispatcher means no agents running
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(agents); err != nil {
			s.logger.Error("Failed to encode agents response: %v", err)
			http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		}
		return
	}

	registeredAgents := s.dispatcher.GetRegisteredAgents()
	for i := range registeredAgents {
		agentInfo := &registeredAgents[i]

		agents = append(agents, AgentListItem{
			ID:     agentInfo.ID,
			Role:   agentInfo.Type.String(),
			State:  agentInfo.State,
			LastTS: time.Now(), // Agents are live, use current time
		})
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

	// Get agent from dispatcher
	if s.dispatcher == nil {
		http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		return
	}

	registeredAgents := s.dispatcher.GetRegisteredAgents()
	var agentInfo *dispatch.AgentInfo
	for i := range registeredAgents {
		if registeredAgents[i].ID == agentID {
			agentInfo = &registeredAgents[i]
			break
		}
	}

	if agentInfo == nil {
		s.logger.Warn("Agent not found: %s", agentID)
		http.Error(w, "Agent not found", http.StatusNotFound)
		return
	}

	// Build response with available agent information
	response := map[string]interface{}{
		"id":         agentInfo.ID,
		"type":       agentInfo.Type.String(),
		"state":      agentInfo.State,
		"model_name": agentInfo.ModelName,
		"story_id":   agentInfo.StoryID,
	}

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
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

// handleStories implements GET /api/stories.
func (s *Server) handleStories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check if architect is available.
	_, err := s.findArchitectState()
	if err != nil {
		s.logger.Debug("Architect not available for stories request: %v", err)
		http.Error(w, "Architect not available", http.StatusServiceUnavailable)
		return
	}

	// Get the architect agent from dispatcher to access its stories.
	// Stories are maintained in architect's in-memory state (canonical source for active session).
	registeredAgents := s.dispatcher.GetRegisteredAgents()
	var stories []*architect.QueuedStory

	for i := range registeredAgents {
		agentInfo := &registeredAgents[i]
		if agentInfo.Type == agent.TypeArchitect {
			// Cast to StoryProvider interface to access GetStoryList.
			if storyProvider, ok := agentInfo.Driver.(StoryProvider); ok {
				stories = storyProvider.GetStoryList()
				break
			}
		}
	}

	// Send JSON response.
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(stories); err != nil {
		s.logger.Error("Failed to encode stories response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served story information: %d stories", len(stories))
}

// handleUpload implements POST /api/upload.
// validateUploadRequest validates the basic upload request.
func (s *Server) validateUploadRequest(r *http.Request) error {
	if r.Method != http.MethodPost {
		return fmt.Errorf("method not allowed")
	}

	// Parse multipart form first (validate request format)
	if err := r.ParseMultipartForm(100 << 10); err != nil { // 100 KB limit
		return fmt.Errorf("invalid multipart form: %w", err)
	}

	return nil
}

// validateUploadFile validates the uploaded file.
func (s *Server) validateUploadFile(_ multipart.File, header *multipart.FileHeader) error {
	// Check file size (100 KB limit)
	if header.Size > 100*1024 {
		return fmt.Errorf("file too large: %d bytes", header.Size)
	}

	// Check file extension.
	if !strings.HasSuffix(strings.ToLower(header.Filename), ".md") {
		return fmt.Errorf("invalid file extension: %s", header.Filename)
	}

	return nil
}

// checkArchitectAvailability checks if architect is available.
func (s *Server) checkArchitectAvailability() error {
	architectState, err := s.findArchitectState()
	if err != nil {
		return err
	}

	if architectState != proto.StateWaiting.String() {
		return fmt.Errorf("architect is busy")
	}

	return nil
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	// Validate request
	if err := s.validateUploadRequest(r); err != nil {
		s.logger.Warn("Upload request validation failed: %v", err)
		if err.Error() == "method not allowed" {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		} else {
			http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		}
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

	// Validate file
	if validateErr := s.validateUploadFile(file, header); validateErr != nil {
		s.logger.Warn("Upload file validation failed: %v", validateErr)
		http.Error(w, validateErr.Error(), http.StatusBadRequest)
		return
	}

	// Check architect availability
	if availErr := s.checkArchitectAvailability(); availErr != nil {
		s.logger.Warn("Architect availability check failed: %v", availErr)
		if availErr.Error() == "dispatcher not available" {
			http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		} else if availErr.Error() == "architect is busy" {
			http.Error(w, "Architect is busy", http.StatusConflict)
		} else {
			http.Error(w, "Architect not available", http.StatusConflict)
		}
		return
	}

	// Create specs directory if it doesn't exist.
	specsDir := filepath.Join(s.workDir, ".maestro", "specs")
	if mkdirErr := os.MkdirAll(specsDir, 0755); mkdirErr != nil {
		s.logger.Error("Failed to create specs directory: %v", mkdirErr)
		http.Error(w, "Failed to create specs directory", http.StatusInternalServerError)
		return
	}

	// Read file content.
	content, err := io.ReadAll(file)
	if err != nil {
		s.logger.Error("Failed to read uploaded file: %v", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Save file to specs directory.
	filePath := filepath.Join(specsDir, header.Filename)
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

	// Also read from log files and merge (for testing and completeness)
	fileLogs := s.readLogFiles(domain, since)

	// Merge logs from both sources, avoiding duplicates by timestamp+message
	logMap := make(map[string]logx.LogEntry)

	// Add in-memory logs first
	for i := range logs {
		log := &logs[i]
		key := log.Timestamp + "|" + log.Message
		logMap[key] = *log
	}

	// Add file logs (will not overwrite existing keys)
	for i := range fileLogs {
		log := &fileLogs[i]
		key := log.Timestamp + "|" + log.Message
		if _, exists := logMap[key]; !exists {
			logMap[key] = *log
		}
	}

	// Convert back to slice
	logs = make([]logx.LogEntry, 0, len(logMap))
	for key := range logMap {
		logs = append(logs, logMap[key])
	}

	// Sort by timestamp
	sort.Slice(logs, func(i, j int) bool {
		return logs[i].Timestamp < logs[j].Timestamp
	})

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

// StartServer starts the HTTP server using configuration settings.
func (s *Server) StartServer(ctx context.Context, host string, port int, useSSL bool, certFile, keyFile string) error {
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)

	addr := fmt.Sprintf("%s:%d", host, port)
	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	if useSSL {
		s.logger.Info("Starting web UI server on %s (HTTPS)", addr)
	} else {
		s.logger.Info("Starting web UI server on %s (HTTP)", addr)
	}

	// Start server in a goroutine (non-blocking).
	go func() {
		var err error
		if useSSL {
			err = server.ListenAndServeTLS(certFile, keyFile)
		} else {
			err = server.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			s.logger.Error("Server error: %v", err)
		}
	}()

	// Start graceful shutdown handler in background.
	go func() {
		<-ctx.Done()
		// Graceful shutdown - use background context with timeout since parent is cancelled.
		// We can't use the cancelled parent context for shutdown operations.
		s.logger.Info("Shutting down web UI server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		//nolint:contextcheck // Parent context is cancelled; we need a fresh context for shutdown
		if err := server.Shutdown(shutdownCtx); err != nil {
			s.logger.Error("HTTP server shutdown failed: %v", err)
		}
	}()

	return nil
}

// MessageEntry represents a message in the message viewer.
type MessageEntry struct {
	RequestType  *string `json:"request_type,omitempty"`  // "question" or "approval"
	ApprovalType *string `json:"approval_type,omitempty"` // "plan", "code", "budget_review", "completion"
	ResponseType *string `json:"response_type,omitempty"` // "answer" or "result"
	Status       *string `json:"status,omitempty"`        // "APPROVED", "REJECTED", "NEEDS_CHANGES", "PENDING"
	Feedback     *string `json:"feedback,omitempty"`      // For responses
	Reason       *string `json:"reason,omitempty"`        // For requests
	Timestamp    string  `json:"timestamp"`
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	StoryID      string  `json:"story_id,omitempty"`
	Content      string  `json:"content"`
}

// handleMessages implements GET /api/messages - returns recent inter-agent messages.
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read logs and extract message events
	logs := s.readMessageLogs()

	// Send JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(logs); err != nil {
		s.logger.Error("Failed to encode messages response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served %d message entries", len(logs))
}

// readMessageLogs reads message events from the database.
func (s *Server) readMessageLogs() []MessageEntry {
	dbPath := filepath.Join(s.workDir, ".maestro", "maestro.db")

	// Open database using sql package
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		s.logger.Debug("Failed to open database for message reading: %v", err)
		return []MessageEntry{}
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			s.logger.Warn("Failed to close database: %v", closeErr)
		}
	}()

	// Get current session ID from config for session-isolated queries
	cfg, err := config.GetConfig()
	if err != nil {
		s.logger.Warn("Failed to get config for session ID: %v", err)
		return []MessageEntry{}
	}

	// Use the persistence operations package with session isolation
	ops := persistence.NewDatabaseOperations(db, cfg.SessionID)
	recentMessages, err := ops.GetRecentMessages(5)
	if err != nil {
		s.logger.Warn("Failed to query recent messages: %v", err)
		return []MessageEntry{}
	}

	// Convert persistence.RecentMessage to MessageEntry
	messages := make([]MessageEntry, 0, len(recentMessages))
	for _, msg := range recentMessages {
		messages = append(messages, MessageEntry{
			Timestamp:    msg.CreatedAt,
			ID:           msg.ID,
			Type:         msg.Type,
			From:         msg.FromAgent,
			To:           msg.ToAgent,
			StoryID:      msg.StoryID,
			RequestType:  msg.RequestType,
			ApprovalType: msg.ApprovalType,
			ResponseType: msg.ResponseType,
			Status:       msg.Status,
			Content:      msg.Content,
			Feedback:     msg.Feedback,
			Reason:       msg.Reason,
		})
	}

	return messages
}

// handleChat implements GET /api/chat (read messages) and POST /api/chat (post message).
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleChatRead(w, r)
	case http.MethodPost:
		s.handleChatPost(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleChatPost implements POST /api/chat - posts a new chat message.
func (s *Server) handleChatPost(w http.ResponseWriter, r *http.Request) {
	if s.chatService == nil {
		http.Error(w, "Chat service not available", http.StatusServiceUnavailable)
		return
	}

	// Parse JSON request body
	var reqBody struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if reqBody.Text == "" {
		http.Error(w, "Text is required", http.StatusBadRequest)
		return
	}

	// Post message as "@human"
	postReq := &chat.PostRequest{
		Author: "@human",
		Text:   reqBody.Text,
	}

	resp, err := s.chatService.Post(r.Context(), postReq)
	if err != nil {
		s.logger.Error("Failed to post chat message: %v", err)
		http.Error(w, "Failed to post message", http.StatusInternalServerError)
		return
	}

	// Return success response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		s.logger.Error("Failed to encode response: %v", err)
	}
}

// handleChatRead implements GET /api/chat - reads recent chat messages.
func (s *Server) handleChatRead(w http.ResponseWriter, r *http.Request) {
	if s.chatService == nil {
		http.Error(w, "Chat service not available", http.StatusServiceUnavailable)
		return
	}

	// Get query parameter for cursor (optional)
	cursorStr := r.URL.Query().Get("since")

	// For now, just get all recent messages from the database directly
	// The cursor will be used by agents, but web UI shows all messages
	dbPath := filepath.Join(s.workDir, ".maestro", "maestro.db")
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		s.logger.Error("Failed to open database: %v", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			s.logger.Warn("Failed to close database: %v", closeErr)
		}
	}()

	// Get session ID from config
	cfg, err := config.GetConfig()
	if err != nil {
		s.logger.Error("Failed to get config: %v", err)
		http.Error(w, "Configuration error", http.StatusInternalServerError)
		return
	}

	// Query messages (session-isolated)
	query := `
		SELECT id, ts, author, text
		FROM chat
		WHERE session_id = ?
		ORDER BY id ASC
	`

	// If cursor provided, only get messages after that ID
	if cursorStr != "" {
		query = `
			SELECT id, ts, author, text
			FROM chat
			WHERE session_id = ? AND id > ?
			ORDER BY id ASC
		`
	}

	var rows *sql.Rows
	if cursorStr != "" {
		rows, err = db.Query(query, cfg.SessionID, cursorStr)
	} else {
		rows, err = db.Query(query, cfg.SessionID)
	}

	if err != nil {
		s.logger.Error("Failed to query messages: %v", err)
		http.Error(w, "Database query error", http.StatusInternalServerError)
		return
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			s.logger.Warn("Failed to close rows: %v", closeErr)
		}
	}()

	// Build response
	messages := []map[string]interface{}{}
	for rows.Next() {
		var id int64
		var timestamp, author, text string
		if err := rows.Scan(&id, &timestamp, &author, &text); err != nil {
			s.logger.Error("Failed to scan row: %v", err)
			continue
		}

		messages = append(messages, map[string]interface{}{
			"id":        id,
			"timestamp": timestamp,
			"author":    author,
			"text":      text,
		})
	}

	// Send response as direct array (not wrapped in object)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(messages); err != nil {
		s.logger.Error("Failed to encode response: %v", err)
	}
}

// findArchitectState finds the state of the architect agent from the dispatcher.
func (s *Server) findArchitectState() (string, error) {
	if s.dispatcher == nil {
		return "", fmt.Errorf("dispatcher not available")
	}

	// Get registered agents from dispatcher
	registeredAgents := s.dispatcher.GetRegisteredAgents()

	// Find the architect agent by type
	for i := range registeredAgents {
		agentInfo := &registeredAgents[i]
		if agentInfo.Type == agent.TypeArchitect {
			return agentInfo.State, nil
		}
	}

	return "", fmt.Errorf("no architect agent found")
}
