package webui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/chat"
)

const errDispatcherNotAvailable = "dispatcher not available"

// PM endpoint request/response types

// PMStartRequest represents a request to start a PM interview session.
type PMStartRequest struct {
	Expertise string `json:"expertise"` // NON_TECHNICAL, BASIC, or EXPERT
}

// PMStartResponse represents the response when starting a PM interview.
type PMStartResponse struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// PMChatRequest represents a message sent to the PM agent.
type PMChatRequest struct {
	SessionID string `json:"session_id"`
	Message   string `json:"message"`
}

// PMChatResponse represents the PM agent's reply.
type PMChatResponse struct {
	Reply     string `json:"reply"`
	Timestamp int64  `json:"timestamp"`
}

// PMPreviewResponse represents a generated spec preview.
type PMPreviewResponse struct {
	Markdown string `json:"markdown"`
	Message  string `json:"message"`
}

// PMStatusResponse represents the current PM agent status.
type PMStatusResponse struct {
	State        string `json:"state"`
	SessionID    string `json:"session_id,omitempty"`
	MessageCount int    `json:"message_count"`
	HasSession   bool   `json:"has_session"`
	CanPreview   bool   `json:"can_preview"`
	CanSubmit    bool   `json:"can_submit"`
}

// handlePMStart implements POST /api/pm/start - Start a new PM interview session.
func (s *Server) handlePMStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req PMStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn("Failed to parse PM start request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate expertise level
	validExpertise := map[string]bool{
		"NON_TECHNICAL": true,
		"BASIC":         true,
		"EXPERT":        true,
	}
	if req.Expertise == "" {
		req.Expertise = "BASIC" // Default
	}
	if !validExpertise[req.Expertise] {
		http.Error(w, "Invalid expertise level", http.StatusBadRequest)
		return
	}

	// Check PM availability
	if errCheck := s.checkPMAvailability(); errCheck != nil {
		s.logger.Warn("PM availability check failed: %v", errCheck)
		if errCheck.Error() == errDispatcherNotAvailable {
			http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "PM agent not available", http.StatusConflict)
		}
		return
	}

	// Get PM agent from dispatcher
	pmAgent := s.dispatcher.GetAgent("pm-001")
	if pmAgent == nil {
		http.Error(w, "PM agent not found", http.StatusNotFound)
		return
	}

	// Type assert to PM driver to access StartInterview method
	type InterviewStarter interface {
		StartInterview(expertise string) error
	}

	pmDriver, ok := pmAgent.(InterviewStarter)
	if !ok {
		s.logger.Error("PM agent does not implement InterviewStarter interface")
		http.Error(w, "PM agent does not support interview start", http.StatusInternalServerError)
		return
	}

	// Generate session ID (use timestamp-based ID like message IDs)
	sessionID := fmt.Sprintf("pm_session_%d", time.Now().UnixNano())

	// Call PM's StartInterview method directly
	s.logger.Info("Starting PM interview session %s (expertise: %s)", sessionID, req.Expertise)
	if err := pmDriver.StartInterview(req.Expertise); err != nil {
		s.logger.Error("Failed to start PM interview: %v", err)
		http.Error(w, "Failed to start interview", http.StatusInternalServerError)
		return
	}

	// Return session ID
	response := PMStartResponse{
		SessionID: sessionID,
		Message:   "Please describe what you want to work on and I'll help define it for implementation.",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM start response: %v", err)
	}
}

// handlePMChat implements POST /api/pm/chat - Send a message to the PM agent.
func (s *Server) handlePMChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req PMChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn("Failed to parse PM chat request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate inputs
	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	if req.Message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	// Post user message to product chat channel
	s.logger.Info("PM chat message received (session: %s): %s", req.SessionID, req.Message)

	postReq := &chat.PostRequest{
		Author:  "@human",
		Text:    req.Message,
		Channel: "product",
	}

	ctx := r.Context()
	if _, err := s.chatService.Post(ctx, postReq); err != nil {
		s.logger.Error("Failed to post message to product chat: %v", err)
		http.Error(w, "Failed to send message", http.StatusInternalServerError)
		return
	}

	// Return success response (PM will see message via chat injection)
	response := PMChatResponse{
		Reply:     "Message sent", // WebUI will poll for PM's response via chat endpoint
		Timestamp: time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM chat response: %v", err)
	}
}

// handlePMPreview implements GET /api/pm/preview - Legacy endpoint, redirects to /api/pm/preview/spec.
func (s *Server) handlePMPreview(w http.ResponseWriter, r *http.Request) {
	// Redirect to the new /api/pm/preview/spec endpoint
	s.handlePMPreviewGet(w, r)
}

// handlePMUpload implements POST /api/pm/upload - Upload a spec file directly to PM (bypass interview).
//
//nolint:cyclop // HTTP handler with validation steps, acceptable complexity
func (s *Server) handlePMUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form
	if err := r.ParseMultipartForm(100 << 10); err != nil { // 100KB max
		s.logger.Warn("Failed to parse multipart form: %v", err)
		http.Error(w, "Invalid multipart form", http.StatusBadRequest)
		return
	}

	// Get the uploaded file
	file, header, err := r.FormFile("file")
	if err != nil {
		s.logger.Warn("No file found in upload: %v", err)
		http.Error(w, "No file provided", http.StatusBadRequest)
		return
	}
	defer func() {
		if cerr := file.Close(); cerr != nil {
			s.logger.Warn("Failed to close uploaded file: %v", cerr)
		}
	}()

	// Validate file extension
	if header.Filename == "" || len(header.Filename) < 4 || header.Filename[len(header.Filename)-3:] != ".md" {
		http.Error(w, "Only .md files are allowed", http.StatusBadRequest)
		return
	}

	// Validate file size
	if header.Size > 100<<10 { // 100KB max
		http.Error(w, "File too large (max 100KB)", http.StatusBadRequest)
		return
	}

	// Check PM availability
	if errCheck := s.checkPMAvailability(); errCheck != nil {
		s.logger.Warn("PM availability check failed: %v", errCheck)
		if errCheck.Error() == errDispatcherNotAvailable {
			http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		} else {
			http.Error(w, "PM agent not available", http.StatusConflict)
		}
		return
	}

	// Read file content
	content, err := io.ReadAll(file)
	if err != nil {
		s.logger.Error("Failed to read uploaded file: %v", err)
		http.Error(w, "Failed to read file", http.StatusInternalServerError)
		return
	}

	// Get PM agent from dispatcher
	pmAgent := s.dispatcher.GetAgent("pm-001")
	if pmAgent == nil {
		http.Error(w, "PM agent not found", http.StatusNotFound)
		return
	}

	// Type assert to PM driver to access UploadSpec method
	type SpecUploader interface {
		UploadSpec(markdown string) error
	}

	pmDriver, ok := pmAgent.(SpecUploader)
	if !ok {
		s.logger.Error("PM agent does not implement SpecUploader interface")
		http.Error(w, "PM agent does not support spec upload", http.StatusInternalServerError)
		return
	}

	// Call PM's UploadSpec method directly
	s.logger.Info("Uploading spec to PM: %s (%d bytes)", header.Filename, header.Size)
	if err := pmDriver.UploadSpec(string(content)); err != nil {
		s.logger.Error("Failed to upload spec to PM: %v", err)
		http.Error(w, "Failed to send spec to PM", http.StatusInternalServerError)
		return
	}

	// Return success
	w.WriteHeader(http.StatusCreated)
	response := map[string]any{
		"message":  "Spec file uploaded successfully and sent to PM for validation",
		"filename": header.Filename,
		"size":     header.Size,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode upload response: %v", err)
	}
}

// PMPreviewActionRequest represents a preview action request (continue interview or submit).
type PMPreviewActionRequest struct {
	SessionID string `json:"session_id"`
	Action    string `json:"action"` // "continue_interview" or "submit_to_architect"
}

// PMPreviewActionResponse represents the response to a preview action.
type PMPreviewActionResponse struct {
	Message string `json:"message"`
	Success bool   `json:"success"`
}

// handlePMPreviewAction implements POST /api/pm/preview/action - Handle preview actions.
func (s *Server) handlePMPreviewAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req PMPreviewActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn("Failed to parse PM preview action request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Validate inputs
	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	if req.Action != "continue_interview" && req.Action != "submit_to_architect" {
		http.Error(w, "action must be 'continue_interview' or 'submit_to_architect'", http.StatusBadRequest)
		return
	}

	s.logger.Info("PM preview action received (session: %s, action: %s)", req.SessionID, req.Action)

	// Get PM agent from dispatcher
	pmAgent := s.dispatcher.GetAgent("pm-001")
	if pmAgent == nil {
		http.Error(w, "PM agent not found", http.StatusNotFound)
		return
	}

	// Type assert to PM driver to access PreviewAction method
	type PreviewActioner interface {
		PreviewAction(ctx context.Context, action string) error
	}

	pmDriver, ok := pmAgent.(PreviewActioner)
	if !ok {
		s.logger.Error("PM agent does not implement PreviewActioner interface")
		http.Error(w, "PM agent does not support preview actions", http.StatusInternalServerError)
		return
	}

	// Call PM's PreviewAction method directly
	ctx := r.Context()
	if err := pmDriver.PreviewAction(ctx, req.Action); err != nil {
		s.logger.Error("Failed to send preview action to PM: %v", err)
		http.Error(w, "Failed to send action to PM", http.StatusInternalServerError)
		return
	}

	// Return success
	response := PMPreviewActionResponse{
		Success: true,
		Message: fmt.Sprintf("Preview action '%s' sent to PM", req.Action),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode preview action response: %v", err)
	}
}

// handlePMPreviewGet implements GET /api/pm/preview/spec - Get the current preview spec markdown.
func (s *Server) handlePMPreviewGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get session ID from query params
	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "session_id parameter is required", http.StatusBadRequest)
		return
	}

	s.logger.Info("PM preview spec requested for session: %s", sessionID)

	// Get PM agent from dispatcher
	if s.dispatcher == nil {
		http.Error(w, "Dispatcher not available", http.StatusServiceUnavailable)
		return
	}

	pmAgent := s.dispatcher.GetAgent("pm-001")
	if pmAgent == nil {
		http.Error(w, "PM agent not found", http.StatusNotFound)
		return
	}

	// Type assert to PM driver to access GetDraftSpec method
	// We need to import the pm package for this
	type DraftSpecGetter interface {
		GetDraftSpec() string
	}

	pmDriver, ok := pmAgent.(DraftSpecGetter)
	if !ok {
		s.logger.Error("PM agent does not implement DraftSpecGetter interface")
		http.Error(w, "PM agent does not support draft spec retrieval", http.StatusInternalServerError)
		return
	}

	// Get the draft spec
	draftSpec := pmDriver.GetDraftSpec()
	if draftSpec == "" {
		response := PMPreviewResponse{
			Markdown: "# No Specification Available\n\nThe specification is not yet ready. Please wait for the PM to complete the interview.",
			Message:  "No draft spec available",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Return the spec
	response := PMPreviewResponse{
		Markdown: draftSpec,
		Message:  "Specification ready for review",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM preview spec response: %v", err)
	}
}

// handlePMStatus implements GET /api/pm/status - Get PM agent status.
func (s *Server) handlePMStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Get PM agent state
	pmState, err := s.getPMState()
	if err != nil {
		s.logger.Warn("Failed to get PM state: %v", err)
		response := PMStatusResponse{
			State:      "UNKNOWN",
			HasSession: false,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
		return
	}

	// Build response
	response := PMStatusResponse{
		State:        pmState,
		HasSession:   pmState != "WAITING", // Has session if not in WAITING state
		MessageCount: 0,                    // TODO: Query message count from database
		CanPreview:   false,                // Preview is automatic via spec_submit tool
		CanSubmit:    false,                // Submit is automatic via preview actions
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM status response: %v", err)
	}
}

// checkPMAvailability verifies that the PM agent is available to receive requests.
func (s *Server) checkPMAvailability() error {
	if s.dispatcher == nil {
		return fmt.Errorf("%s", errDispatcherNotAvailable)
	}

	// Check if PM agent exists and is in a ready state
	pmState, err := s.getPMState()
	if err != nil {
		return fmt.Errorf("PM agent not found: %w", err)
	}

	// PM is ready if it's in WAITING state (not currently processing)
	if pmState != "WAITING" {
		return fmt.Errorf("PM agent is busy (state: %s)", pmState)
	}

	return nil
}

// getPMState retrieves the current state of the PM agent.
func (s *Server) getPMState() (string, error) {
	if s.dispatcher == nil {
		return "", fmt.Errorf("dispatcher not available")
	}

	// Get registered agents from dispatcher
	registeredAgents := s.dispatcher.GetRegisteredAgents()

	// Find the PM agent by type
	for i := range registeredAgents {
		agentInfo := &registeredAgents[i]
		if agentInfo.Type == agent.TypePM {
			return agentInfo.State, nil
		}
	}

	return "", fmt.Errorf("no PM agent found")
}
