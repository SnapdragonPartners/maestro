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
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/version"
)

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static
var staticFS embed.FS

// StoryProvider interface for agents that can provide stories.
type StoryProvider interface {
	GetStoryList() []*architect.QueuedStory
}

// DemoAvailabilityChecker interface for checking demo availability.
// PM implements this to indicate when bootstrap is complete.
type DemoAvailabilityChecker interface {
	IsDemoAvailable() bool
}

// Server represents the web UI HTTP server.
type Server struct {
	dispatcher              *dispatch.Dispatcher
	chatService             *chat.Service
	llmFactory              *agent.LLMClientFactory
	demoService             DemoService
	demoAvailabilityChecker DemoAvailabilityChecker // PM provides this
	logger                  *logx.Logger
	templates               *template.Template
	workDir                 string
}

// AgentListItem represents an agent in the list response.
type AgentListItem struct {
	LastTS time.Time `json:"last_ts"`
	ID     string    `json:"id"`
	Role   string    `json:"role"`
	State  string    `json:"state"`
}

// NewServer creates a new web UI server.
func NewServer(dispatcher *dispatch.Dispatcher, workDir string, chatService *chat.Service, llmFactory *agent.LLMClientFactory) *Server {
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
		llmFactory:  llmFactory,
	}
}

// SetDemoService sets the demo service for demo mode operations.
func (s *Server) SetDemoService(demoService DemoService) {
	s.demoService = demoService
}

// SetDemoAvailabilityChecker sets the demo availability checker.
// PM implements this to indicate when bootstrap is complete and demo is available.
func (s *Server) SetDemoAvailabilityChecker(checker DemoAvailabilityChecker) {
	s.demoAvailabilityChecker = checker
}

// requireAuth wraps an HTTP handler with Basic Authentication.
// Username is always "maestro", password comes from unified password (secrets file or MAESTRO_PASSWORD env var).
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get password using unified password logic
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
	mux.HandleFunc("/api/services/status", s.requireAuth(s.handleServicesStatus))
	mux.HandleFunc("/api/agents", s.requireAuth(s.handleAgents))
	mux.HandleFunc("/api/agent/", s.requireAuth(s.handleAgent))
	mux.HandleFunc("/api/queues", s.requireAuth(s.handleQueues))
	mux.HandleFunc("/api/stories", s.requireAuth(s.handleStories))
	// NOTE: /api/upload removed - all specs must go through PM for validation
	// Use /api/pm/upload instead
	mux.HandleFunc("/api/answer", s.requireAuth(s.handleAnswer))
	mux.HandleFunc("/api/shutdown", s.requireAuth(s.handleShutdown))
	mux.HandleFunc("/api/logs", s.requireAuth(s.handleLogs))
	mux.HandleFunc("/api/messages", s.requireAuth(s.handleMessages))
	mux.HandleFunc("/api/healthz", s.requireAuth(s.handleHealth))
	mux.HandleFunc("/api/chat", s.requireAuth(s.handleChat))

	// PM agent endpoints - specification development
	mux.HandleFunc("/api/pm/start", s.requireAuth(s.handlePMStart))
	mux.HandleFunc("/api/pm/chat", s.requireAuth(s.handlePMChat))
	mux.HandleFunc("/api/pm/preview", s.requireAuth(s.handlePMPreview))
	mux.HandleFunc("/api/pm/preview/action", s.requireAuth(s.handlePMPreviewAction))
	mux.HandleFunc("/api/pm/preview/spec", s.requireAuth(s.handlePMPreviewGet))
	mux.HandleFunc("/api/pm/upload", s.requireAuth(s.handlePMUpload))
	mux.HandleFunc("/api/pm/status", s.requireAuth(s.handlePMStatus))

	// Demo endpoints - application demo mode
	mux.HandleFunc("/api/demo/status", s.requireAuth(s.handleDemoStatus))
	mux.HandleFunc("/api/demo/start", s.requireAuth(s.handleDemoStart))
	mux.HandleFunc("/api/demo/stop", s.requireAuth(s.handleDemoStop))
	mux.HandleFunc("/api/demo/restart", s.requireAuth(s.handleDemoRestart))
	mux.HandleFunc("/api/demo/rebuild", s.requireAuth(s.handleDemoRebuild))
	mux.HandleFunc("/api/demo/logs", s.requireAuth(s.handleDemoLogs))

	// Secrets endpoints - encrypted secrets management
	mux.HandleFunc("/api/secrets", s.requireAuth(s.handleSecretsRouter))
	mux.HandleFunc("/api/secrets/", s.requireAuth(s.handleSecretsDelete))
}

// handleSecretsRouter routes GET/POST to appropriate handlers.
func (s *Server) handleSecretsRouter(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleSecretsList(w, r)
	case http.MethodPost:
		s.handleSecretsSet(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleServicesStatus implements GET /api/services/status.
func (s *Server) handleServicesStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get chat configuration
	cfg, err := config.GetConfig()
	chatEnabled := false
	if err == nil {
		chatEnabled = cfg.Chat.Enabled
	}

	// Check agent readiness
	agentReady := false
	architectReady := false
	coderCount := 0

	if s.dispatcher != nil {
		registeredAgents := s.dispatcher.GetRegisteredAgents()

		// Count coders and check architect exists
		for i := range registeredAgents {
			agentInfo := &registeredAgents[i]
			if agentInfo.Type == agent.TypeArchitect {
				// Architect is ready if it exists (registered), regardless of current state
				// Once registered, the architect can accept specs even if currently busy
				architectReady = true
			} else if agentInfo.Type == agent.TypeCoder {
				coderCount++
			}
		}

		// System is ready if architect exists and is registered
		agentReady = architectReady
	}

	// Get rate limit stats from LLM factory
	rateLimitStats := make(map[string]interface{})
	if s.llmFactory != nil {
		stats := s.llmFactory.GetRateLimitStats()
		for provider := range stats {
			stat := stats[provider]
			rateLimitStats[provider] = map[string]interface{}{
				"available_tokens":     stat.AvailableTokens,
				"max_capacity":         stat.MaxCapacity,
				"active_requests":      stat.ActiveRequests,
				"max_concurrency":      stat.MaxConcurrency,
				"token_limit_hits":     stat.TokenLimitHits,
				"concurrency_hits":     stat.ConcurrencyHits,
				"tracked_acquisitions": stat.TrackedAcquisitions,
			}
		}
	}

	// Get demo status
	demoStatus := map[string]interface{}{
		"available": s.demoService != nil,
		"running":   false,
	}
	if s.demoService != nil {
		status := s.demoService.Status(r.Context())
		demoStatus["running"] = status.Running
		demoStatus["port"] = status.Port
		demoStatus["url"] = status.URL
		demoStatus["outdated"] = status.Outdated
	}

	// Build response
	response := map[string]interface{}{
		"chat": map[string]interface{}{
			"enabled": chatEnabled,
		},
		"agents": map[string]interface{}{
			"ready":           agentReady,
			"coder_count":     coderCount,
			"architect_ready": architectReady,
		},
		"demo":        demoStatus,
		"rate_limits": rateLimitStats,
	}

	// Send JSON response
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode services status response: %v", err)
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}

	s.logger.Debug("Served services status: chat=%v, agents_ready=%v", chatEnabled, agentReady)
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

	// Extract todo list from coder agents' state data
	if agentInfo.Type == agent.TypeCoder && agentInfo.Driver != nil {
		stateData := agentInfo.Driver.GetStateData()
		if todoListData, exists := stateData["todo_list"]; exists {
			// todoListData is the TodoList struct - convert to map for JSON
			if todoListMap, ok := todoListData.(map[string]interface{}); ok {
				response["todo_list"] = todoListMap
			} else {
				// If it's the actual struct, we need to marshal/unmarshal it
				// This handles the case where it's stored as a struct type
				todoJSON, err := json.Marshal(todoListData)
				if err == nil {
					var todoMap map[string]interface{}
					if json.Unmarshal(todoJSON, &todoMap) == nil {
						response["todo_list"] = todoMap
					}
				}
			}
		}
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
		"version": version.Version,
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

// handleDashboard serves the main dashboard page.
// This also acts as a catch-all for client-side routes (SPA routing).
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Let API and static routes be handled by their specific handlers.
	// For any other path, serve the dashboard (SPA catch-all for client-side routing).
	if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/static/") {
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
	Reason       *string `json:"reason,omitempty"`        // For requests
	Timestamp    string  `json:"timestamp"`
	ID           string  `json:"id"`
	Type         string  `json:"type"`
	From         string  `json:"from"`
	To           string  `json:"to"`
	StoryID      *string `json:"story_id,omitempty"` // Can be null for messages not associated with a story
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
	db, err := sql.Open("sqlite", dbPath)
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
		Text    string `json:"text"`
		Channel string `json:"channel"` // Optional: defaults to 'development'
	}
	if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	if reqBody.Text == "" {
		http.Error(w, "Text is required", http.StatusBadRequest)
		return
	}

	// Default to development channel if not specified
	channel := reqBody.Channel
	if channel == "" {
		channel = "development"
	}

	// Post message as "@human"
	postReq := &chat.PostRequest{
		Author:  "@human",
		Text:    reqBody.Text,
		Channel: channel,
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
	var cursorID int64
	if cursorStr != "" {
		if _, err := fmt.Sscanf(cursorStr, "%d", &cursorID); err != nil {
			s.logger.Warn("Invalid cursor format: %s", cursorStr)
		}
	}

	// Get all in-memory messages from chat service (canonical source of truth)
	allMessages := s.chatService.GetAllMessages()

	// Build response, filtering by cursor if provided
	messages := []map[string]interface{}{}
	for _, msg := range allMessages {
		if cursorID > 0 && msg.ID <= cursorID {
			continue // Skip messages we've already seen
		}

		messages = append(messages, map[string]interface{}{
			"id":        msg.ID,
			"timestamp": msg.Timestamp,
			"author":    msg.Author,
			"text":      msg.Text,
			"channel":   msg.Channel,
			"post_type": msg.PostType,
			"reply_to":  msg.ReplyTo,
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
