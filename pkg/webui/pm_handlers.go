package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
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

// PMSubmitRequest represents a spec submission request.
type PMSubmitRequest struct {
	SessionID string `json:"session_id"`
}

// PMSubmitResponse represents the result of spec submission.
type PMSubmitResponse struct {
	Message string   `json:"message"`
	Errors  []string `json:"errors,omitempty"`
	Success bool     `json:"success"`
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

	// Generate session ID (use timestamp-based ID like message IDs)
	sessionID := fmt.Sprintf("pm_session_%d", time.Now().UnixNano())

	// Create interview request message
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "web-ui", "pm-001")
	payload := map[string]any{
		"type":       "interview_start",
		"session_id": sessionID,
		"expertise":  req.Expertise,
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindGeneric, payload))

	// Send to PM agent
	s.logger.Info("Starting PM interview session %s (expertise: %s)", sessionID, req.Expertise)
	if err := s.dispatcher.DispatchMessage(msg); err != nil {
		s.logger.Error("Failed to dispatch PM start message: %v", err)
		http.Error(w, "Failed to start interview", http.StatusInternalServerError)
		return
	}

	// Return session ID
	response := PMStartResponse{
		SessionID: sessionID,
		Message:   "Interview session started successfully",
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

	// TODO: For MVP, we'll use a simplified approach:
	// The PM agent doesn't have a synchronous chat API yet (it's designed for async state machine flow).
	// For now, return a placeholder response. Full implementation requires:
	// 1. PM agent to support synchronous chat calls via a new tool or message type
	// 2. Or: WebUI polls for PM responses via a messages endpoint
	// 3. Or: WebSockets for real-time bidirectional communication

	s.logger.Info("PM chat message received (session: %s): %s", req.SessionID, req.Message)

	// Placeholder response
	response := PMChatResponse{
		Reply:     "PM chat integration coming soon. For now, use the upload feature to submit specifications.",
		Timestamp: time.Now().Unix(),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM chat response: %v", err)
	}
}

// handlePMPreview implements GET /api/pm/preview - Generate a spec preview.
func (s *Server) handlePMPreview(w http.ResponseWriter, r *http.Request) {
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

	s.logger.Info("PM preview requested for session: %s", sessionID)

	// TODO: Full implementation requires:
	// 1. PM agent to support preview generation (DRAFTING state trigger)
	// 2. Database query to get conversation history
	// 3. LLM call to generate spec from conversation
	// 4. Return generated markdown

	// Placeholder response
	response := PMPreviewResponse{
		Markdown: "# Preview coming soon\n\nThe PM agent preview feature is under development.",
		Message:  "Preview generation not yet implemented",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM preview response: %v", err)
	}
}

// handlePMSubmit implements POST /api/pm/submit - Submit a completed spec to the architect.
func (s *Server) handlePMSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request body
	var req PMSubmitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.logger.Warn("Failed to parse PM submit request: %v", err)
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.SessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	s.logger.Info("PM spec submission requested for session: %s", req.SessionID)

	// TODO: Full implementation requires:
	// 1. PM agent to transition to SUBMITTING state
	// 2. spec_submit tool validation
	// 3. Send REQUEST to architect
	// 4. Return success/failure based on validation

	// Placeholder response
	response := PMSubmitResponse{
		Success: false,
		Message: "Spec submission not yet implemented. Use file upload instead.",
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		s.logger.Error("Failed to encode PM submit response: %v", err)
	}
}

// handlePMUpload implements POST /api/pm/upload - Upload a spec file directly to PM (bypass interview).
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

	// Create spec upload message to PM
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, "web-ui", "pm-001")
	payload := map[string]any{
		"type":          "spec_upload",
		"filename":      header.Filename,
		"spec_markdown": string(content),
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindGeneric, payload))

	s.logger.Info("Dispatching spec upload to PM: %s (%d bytes)", header.Filename, header.Size)
	if err := s.dispatcher.DispatchMessage(msg); err != nil {
		s.logger.Error("Failed to dispatch upload message to PM: %v", err)
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
		HasSession:   false, // TODO: Check for active session in database
		MessageCount: 0,     // TODO: Query message count from database
		CanPreview:   pmState == "INTERVIEWING" || pmState == "DRAFTING",
		CanSubmit:    pmState == "DRAFTING" || pmState == "SUBMITTING",
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
