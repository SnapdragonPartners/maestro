package webui

import (
	"context"
	"encoding/json"
	"fmt"
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
// Supports optional file attachment (inline spec upload via paperclip button).
type PMChatRequest struct {
	SessionID   string `json:"session_id"`
	Message     string `json:"message"`
	FileContent string `json:"file_content,omitempty"` // Full file text (bypasses chat 4096 limit via context injection)
	FileName    string `json:"file_name,omitempty"`    // Original filename (must be .md)
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

	// Choose initialization message based on PM state after StartInterview
	// Bootstrap paths → WORKING or AWAIT_ARCHITECT; normal path → AWAIT_USER
	initMessage := "Please describe what you want to work on and I'll help define it for implementation."
	pmState, _ := s.getPMState()
	if pmState == "WORKING" || pmState == "AWAIT_ARCHITECT" {
		initMessage = "Give me a minute to check the environment and bootstrap Maestro's basic requirements before we get started."
	}

	// Return session ID
	response := PMStartResponse{
		SessionID: sessionID,
		Message:   initMessage,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM start response: %v", err)
	}
}

// handlePMChat implements POST /api/pm/chat - Send a message to the PM agent.
// Supports optional file attachment (file_content + file_name fields).
//
//nolint:cyclop // Complexity from file validation + text handling in single endpoint is inherent
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
	// Require either a text message or a file attachment
	if req.Message == "" && req.FileContent == "" {
		http.Error(w, "message or file_content is required", http.StatusBadRequest)
		return
	}

	ctx := r.Context()

	// Handle file attachment: inject full content into PM context, post short notification to chat
	if req.FileContent != "" {
		// Validate file extension
		if req.FileName == "" || len(req.FileName) < 4 || req.FileName[len(req.FileName)-3:] != ".md" {
			http.Error(w, "Only .md files are allowed", http.StatusBadRequest)
			return
		}
		// Validate file size (100KB max)
		if len(req.FileContent) > 100<<10 {
			http.Error(w, "File too large (max 100KB)", http.StatusBadRequest)
			return
		}

		// Get PM agent to inject file content directly into its context
		pmAgent := s.dispatcher.GetAgent("pm-001")
		if pmAgent == nil {
			http.Error(w, "PM agent not found", http.StatusNotFound)
			return
		}

		type SpecFileInjector interface {
			InjectSpecFile(fileName, content string) error
		}

		pmDriver, ok := pmAgent.(SpecFileInjector)
		if !ok {
			s.logger.Error("PM agent does not implement SpecFileInjector interface")
			http.Error(w, "PM agent does not support file injection", http.StatusInternalServerError)
			return
		}

		if err := pmDriver.InjectSpecFile(req.FileName, req.FileContent); err != nil {
			s.logger.Error("Failed to inject spec file to PM: %v", err)
			http.Error(w, fmt.Sprintf("Failed to process file: %s", err.Error()), http.StatusInternalServerError)
			return
		}

		// Post short notification to chat so user sees confirmation
		notification := fmt.Sprintf("Uploaded %s (%d bytes)", req.FileName, len(req.FileContent))
		notifReq := &chat.PostRequest{
			Author:  "@human",
			Text:    notification,
			Channel: "product",
		}
		if _, err := s.chatService.Post(ctx, notifReq); err != nil {
			s.logger.Warn("Failed to post file notification to chat: %v", err)
			// Non-fatal: file was already injected into PM context
		}

		s.logger.Info("PM file injected: %s (%d bytes, session: %s)", req.FileName, len(req.FileContent), req.SessionID)
	}

	// Post user text message to product chat channel (if present)
	if req.Message != "" {
		s.logger.Info("PM chat message received (session: %s): %s", req.SessionID, req.Message)

		postReq := &chat.PostRequest{
			Author:  "@human",
			Text:    req.Message,
			Channel: "product",
		}

		if _, err := s.chatService.Post(ctx, postReq); err != nil {
			s.logger.Error("Failed to post message to product chat: %v", err)
			http.Error(w, "Failed to send message", http.StatusInternalServerError)
			return
		}
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

	// PM can accept uploads in WAITING (before interview) and AWAIT_USER (during interview)
	// Block uploads when WORKING (actively processing)
	if pmState != "WAITING" && pmState != "AWAIT_USER" {
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
