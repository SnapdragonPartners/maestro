package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
)

// LLMClient defines the interface for language model interactions
type LLMClient interface {
	// GenerateResponse generates a response given a prompt
	GenerateResponse(ctx context.Context, prompt string) (string, error)
}

// RequestWorker handles all agent requests (questions, code reviews, resource requests)
type RequestWorker struct {
	llmClient     LLMClient
	renderer      *templates.Renderer
	requestCh     chan *proto.AgentMsg
	requestDoneCh chan string
	dispatcher    MessageSender
	architectID   string
	queue         *Queue
	mergeCh       chan<- string
}

// NewRequestWorker creates a new unified request worker
func NewRequestWorker(llmClient LLMClient, renderer *templates.Renderer, requestCh chan *proto.AgentMsg, requestDoneCh chan string, dispatcher MessageSender, architectID string, queue *Queue, mergeCh chan<- string) *RequestWorker {
	return &RequestWorker{
		llmClient:     llmClient,
		renderer:      renderer,
		requestCh:     requestCh,
		requestDoneCh: requestDoneCh,
		dispatcher:    dispatcher,
		architectID:   architectID,
		queue:         queue,
		mergeCh:       mergeCh,
	}
}

// Run starts the unified request worker goroutine
func (w *RequestWorker) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			logx.Errorf("RequestWorker panic recovered: %v", r)
			// Consider restarting the worker or notifying the system
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-w.requestCh:
			// Process with error handling
			if err := w.processMessage(ctx, msg); err != nil {
				logx.Errorf("RequestWorker error processing %s: %v", msg.ID, err)
				// Send error response back to agent
				w.sendErrorResponse(msg, err)
			}

			// Signal completion
			select {
			case w.requestDoneCh <- msg.ID:
			case <-ctx.Done():
				return
			}
		}
	}
}

// processMessage handles all request types: question, plan, code, resource
func (w *RequestWorker) processMessage(ctx context.Context, msg *proto.AgentMsg) error {
	switch msg.Type {
	case proto.MsgTypeQUESTION:
		return w.handleQuestion(ctx, msg)
	case proto.MsgTypeREQUEST:
		return w.handleRequest(ctx, msg)
	default:
		return fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// handleQuestion processes QUESTION messages
func (w *RequestWorker) handleQuestion(ctx context.Context, msg *proto.AgentMsg) error {
	// Process question using LLM
	var response string
	if w.llmClient != nil {
		// Use LLM to generate answer
		question, exists := msg.GetPayload(proto.KeyQuestion)
		if !exists {
			return fmt.Errorf("no question payload in message")
		}
		llmResponse, err := w.llmClient.GenerateResponse(ctx, fmt.Sprintf("%v", question))
		if err != nil {
			return fmt.Errorf("failed to generate LLM response: %w", err)
		}
		response = llmResponse
		logx.Infof("RequestWorker: processed question %s", msg.ID)
	} else {
		// Mock mode - generate mock answer
		response = "Mock answer: This is a simulated response from the architect"
		logx.Infof("RequestWorker: mock processing question %s", msg.ID)
	}

	// Send answer back to agent
	if w.dispatcher != nil {
		answerMsg := proto.NewAgentMsg(proto.MsgTypeANSWER, w.architectID, msg.FromAgent)
		answerMsg.ParentMsgID = msg.ID
		answerMsg.SetPayload(proto.KeyAnswer, response)
		answerMsg.SetPayload(proto.KeyStatus, "answered")

		if err := w.dispatcher.SendMessage(answerMsg); err != nil {
			return fmt.Errorf("failed to send answer: %w", err)
		}
	}

	return nil
}

// handleRequest processes REQUEST messages with subtypes: plan, code, resource, question
func (w *RequestWorker) handleRequest(ctx context.Context, msg *proto.AgentMsg) error {
	// Get request subtype
	requestType, _ := msg.GetPayload(proto.KeyRequestType)
	approvalType, _ := msg.GetPayload(proto.KeyApprovalType)

	// Determine the specific request type
	var requestSubtype string
	if requestType != nil {
		requestSubtype = fmt.Sprintf("%v", requestType)
	} else if approvalType != nil {
		requestSubtype = fmt.Sprintf("%v", approvalType)
	} else {
		// Default to code review for backward compatibility
		requestSubtype = "code"
	}

	switch requestSubtype {
	case "plan":
		return w.handlePlanApproval(ctx, msg)
	case "code":
		return w.handleCodeApproval(ctx, msg)
	case "resource":
		return w.handleResourceRequest(ctx, msg)
	case "question":
		return w.handleQuestionRequest(ctx, msg)
	default:
		return fmt.Errorf("unsupported request subtype: %s", requestSubtype)
	}
}

// handlePlanApproval processes plan approval requests
func (w *RequestWorker) handlePlanApproval(ctx context.Context, msg *proto.AgentMsg) error {
	var approved bool = true
	var feedback string

	planRequest, exists := msg.GetPayload(proto.KeyRequest)
	if !exists {
		return fmt.Errorf("no plan request payload in message")
	}

	if w.llmClient != nil {
		// Use LLM for plan review
		llmResponse, err := w.llmClient.GenerateResponse(ctx, fmt.Sprintf("Review this implementation plan: %v", planRequest))
		if err != nil {
			return fmt.Errorf("failed to generate LLM plan review: %w", err)
		}
		feedback = llmResponse
		// For now, always approve in LLM mode (real logic would parse LLM response)
		approved = true
		logx.Infof("RequestWorker: processed plan review %s", msg.ID)
	} else {
		// Mock mode - auto-approve with mock feedback
		approved = true
		feedback = "Mock plan review: Plan looks good, auto-approved for demo"
		logx.Infof("RequestWorker: mock processing plan review %s", msg.ID)
	}

	return w.sendApprovalResult(ctx, msg, approved, feedback, string(proto.ApprovalTypePlan))
}

// handleCodeApproval processes code approval requests
func (w *RequestWorker) handleCodeApproval(ctx context.Context, msg *proto.AgentMsg) error {
	var approved bool = true
	var feedback string

	code, exists := msg.GetPayload("code")
	if !exists {
		return fmt.Errorf("no code payload in message")
	}

	if w.llmClient != nil {
		// Use LLM for code review
		llmResponse, err := w.llmClient.GenerateResponse(ctx, fmt.Sprintf("Review this code: %v", code))
		if err != nil {
			return fmt.Errorf("failed to generate LLM review: %w", err)
		}
		feedback = llmResponse
		// For now, always approve in LLM mode (real logic would parse LLM response)
		approved = true
		logx.Infof("RequestWorker: processed code review %s", msg.ID)
	} else {
		// Mock mode - auto-approve with mock feedback
		approved = true
		feedback = "Mock review: Code looks good, auto-approved for demo"
		logx.Infof("RequestWorker: mock processing code review %s", msg.ID)
	}

	// For code approvals, mark story as completed and signal merge if approved
	if approved {
		if storyID, exists := msg.GetPayload("story_id"); exists {
			if storyIDStr, ok := storyID.(string); ok && w.queue != nil {
				// Mark story as completed in queue
				if err := w.queue.MarkCompleted(storyIDStr); err != nil {
					logx.Warnf("failed to mark story %s as completed: %v", storyIDStr, err)
				} else {
					logx.Infof("marked story %s as completed after code approval", storyIDStr)

					// Signal merge channel
					if w.mergeCh != nil {
						select {
						case w.mergeCh <- storyIDStr:
							logx.Infof("signaled merge for story %s", storyIDStr)
						default:
							logx.Warnf("merge channel full for story %s", storyIDStr)
						}
					}
				}
			}
		}
	}

	return w.sendApprovalResult(ctx, msg, approved, feedback, string(proto.ApprovalTypeCode))
}

// handleResourceRequest processes resource approval requests
func (w *RequestWorker) handleResourceRequest(ctx context.Context, msg *proto.AgentMsg) error {
	// Extract resource request fields
	requestedTokens, _ := msg.GetPayload(proto.KeyRequestedTokens)
	requestedIterations, _ := msg.GetPayload(proto.KeyRequestedIterations)
	justification, _ := msg.GetPayload(proto.KeyJustification)
	storyID, _ := msg.GetPayload(proto.KeyStoryID)

	// Convert to int if needed
	tokens := 0
	iterations := 0
	if tokensFloat, ok := requestedTokens.(float64); ok {
		tokens = int(tokensFloat)
	} else if tokensInt, ok := requestedTokens.(int); ok {
		tokens = tokensInt
	}
	if iterFloat, ok := requestedIterations.(float64); ok {
		iterations = int(iterFloat)
	} else if iterInt, ok := requestedIterations.(int); ok {
		iterations = iterInt
	}

	justificationStr := ""
	if j, ok := justification.(string); ok {
		justificationStr = j
	}

	logx.Infof("RequestWorker: processing resource request %s for %d tokens, %d iterations", msg.ID, tokens, iterations)
	logx.Infof("  Justification: %s", justificationStr)

	// Resource approval logic (for now, approve reasonable requests)
	approved := true
	feedback := "Resource request approved"
	approvedTokens := tokens
	approvedIterations := iterations

	// Apply resource limits and business logic
	if tokens > 10000 {
		approved = false
		feedback = "Requested tokens exceed maximum limit (10,000)"
	} else if iterations > 50 {
		approved = false
		feedback = "Requested iterations exceed maximum limit (50)"
	} else if justificationStr == "" {
		approved = false
		feedback = "Resource requests must include justification"
	}

	if approved {
		logx.Infof("approved resource request: %d tokens, %d iterations", approvedTokens, approvedIterations)
	} else {
		logx.Warnf("rejected resource request: %s", feedback)
		approvedTokens = 0
		approvedIterations = 0
	}

	// Send structured ResourceApproval message
	return w.sendResourceApproval(ctx, msg, approved, approvedTokens, approvedIterations, feedback, storyID)
}

// handleQuestionRequest processes question-type requests
func (w *RequestWorker) handleQuestionRequest(ctx context.Context, msg *proto.AgentMsg) error {
	// Question requests are similar to QUESTION messages but come through REQUEST channel
	return w.handleQuestion(ctx, msg)
}

// sendApprovalResult sends approval result back to agent
func (w *RequestWorker) sendApprovalResult(ctx context.Context, msg *proto.AgentMsg, approved bool, feedback string, approvalType string) error {
	if w.dispatcher == nil {
		return nil
	}

	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, w.architectID, msg.FromAgent)
	resultMsg.ParentMsgID = msg.ID
	resultMsg.SetPayload("approved", approved)
	resultMsg.SetPayload(proto.KeyFeedback, feedback)
	resultMsg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())
	resultMsg.SetPayload(proto.KeyApprovalType, approvalType)

	// Set status based on approval result
	if approved {
		resultMsg.SetPayload(proto.KeyStatus, string(proto.ApprovalStatusApproved))
	} else {
		resultMsg.SetPayload(proto.KeyStatus, string(proto.ApprovalStatusRejected))
	}

	return w.dispatcher.SendMessage(resultMsg)
}

// sendResourceApproval sends a structured resource approval message back to the coder
func (w *RequestWorker) sendResourceApproval(ctx context.Context, msg *proto.AgentMsg, approved bool, approvedTokens, approvedIterations int, feedback string, storyID any) error {
	if w.dispatcher == nil {
		return nil
	}

	resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, w.architectID, msg.FromAgent)
	resultMsg.ParentMsgID = msg.ID

	// Set standard approval fields
	resultMsg.SetPayload("approved", approved)
	resultMsg.SetPayload(proto.KeyFeedback, feedback)
	resultMsg.SetPayload(proto.KeyRequestType, string(proto.RequestResource))

	// Set resource-specific fields using the new protocol keys
	resultMsg.SetPayload("approved_tokens", approvedTokens)
	resultMsg.SetPayload("approved_iterations", approvedIterations)

	// Include story ID if provided
	if storyID != nil {
		resultMsg.SetPayload(proto.KeyStoryID, storyID)
	}

	// Set status based on approval result
	if approved {
		resultMsg.SetPayload(proto.KeyStatus, string(proto.ApprovalStatusApproved))
	} else {
		resultMsg.SetPayload(proto.KeyStatus, string(proto.ApprovalStatusRejected))
	}

	// Add metadata to identify this as a resource approval
	resultMsg.SetMetadata("approval_type", "resource")
	resultMsg.SetMetadata("message_type", "resource_approval")

	logx.Infof("sending ResourceApproval to %s: %s (%d tokens, %d iterations)",
		msg.FromAgent,
		resultMsg.Payload[proto.KeyStatus],
		approvedTokens,
		approvedIterations)

	return w.dispatcher.SendMessage(resultMsg)
}

// sendErrorResponse sends an error response back to the agent
func (w *RequestWorker) sendErrorResponse(msg *proto.AgentMsg, err error) {
	if w.dispatcher == nil {
		return
	}

	var errorMsg *proto.AgentMsg
	if msg.Type == proto.MsgTypeQUESTION {
		errorMsg = proto.NewAgentMsg(proto.MsgTypeANSWER, w.architectID, msg.FromAgent)
		errorMsg.SetPayload(proto.KeyAnswer, fmt.Sprintf("Error processing question: %v", err))
	} else {
		errorMsg = proto.NewAgentMsg(proto.MsgTypeRESULT, w.architectID, msg.FromAgent)
		errorMsg.SetPayload("approved", false)
		errorMsg.SetPayload(proto.KeyFeedback, fmt.Sprintf("Error processing request: %v", err))
	}

	errorMsg.ParentMsgID = msg.ID
	errorMsg.SetPayload(proto.KeyStatus, "error")

	if sendErr := w.dispatcher.SendMessage(errorMsg); sendErr != nil {
		logx.Errorf("RequestWorker: failed to send error response: %v", sendErr)
	}
}

// DispatcherInterface defines the interface for pulling messages from the dispatcher
type DispatcherInterface interface {
	PullArchitectWork() *proto.AgentMsg
}

// MessageSender defines the interface for sending messages to agents
type MessageSender interface {
	SendMessage(msg *proto.AgentMsg) error
	SendMessageWithContext(ctx context.Context, msg *proto.AgentMsg) error
}

// MockDispatcher implements MessageSender for testing
type MockDispatcher struct {
	sentMessages []*proto.AgentMsg
}

// NewMockDispatcher creates a new mock dispatcher
func NewMockDispatcher() *MockDispatcher {
	return &MockDispatcher{
		sentMessages: make([]*proto.AgentMsg, 0),
	}
}

// SendMessage implements MessageSender interface
func (m *MockDispatcher) SendMessage(msg *proto.AgentMsg) error {
	return m.SendMessageWithContext(context.Background(), msg)
}

// SendMessageWithContext implements MessageSender interface with context support
func (m *MockDispatcher) SendMessageWithContext(ctx context.Context, msg *proto.AgentMsg) error {
	m.sentMessages = append(m.sentMessages, msg)
	answer, _ := msg.GetPayload(proto.KeyAnswer)
	logx.Infof("MockDispatcher: would send %s to %s (content: %v)",
		msg.Type, msg.ToAgent, answer)
	return nil
}

// GetSentMessages returns all sent messages for testing
func (m *MockDispatcher) GetSentMessages() []*proto.AgentMsg {
	return m.sentMessages
}

// DispatcherAdapter adapts the real dispatcher to implement MessageSender
type DispatcherAdapter struct {
	dispatcher *dispatch.Dispatcher
}

// NewDispatcherAdapter creates a new dispatcher adapter
func NewDispatcherAdapter(dispatcher *dispatch.Dispatcher) *DispatcherAdapter {
	return &DispatcherAdapter{
		dispatcher: dispatcher,
	}
}

// SendMessage implements MessageSender interface by using dispatcher's DispatchMessage
func (d *DispatcherAdapter) SendMessage(msg *proto.AgentMsg) error {
	return d.SendMessageWithContext(context.Background(), msg)
}

// SendMessageWithContext implements MessageSender interface with context support
func (d *DispatcherAdapter) SendMessageWithContext(ctx context.Context, msg *proto.AgentMsg) error {
	if d.dispatcher == nil {
		return fmt.Errorf("dispatcher not initialized")
	}

	// Add timeout protection for dispatcher send operations
	done := make(chan error, 1)

	go func() {
		// Use the dispatcher's existing DispatchMessage method
		err := d.dispatcher.DispatchMessage(msg)
		if err != nil {
			done <- fmt.Errorf("failed to dispatch message %s: %w", msg.ID, err)
		} else {
			done <- nil
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			return err
		}
		logx.Infof("DispatcherAdapter: sent %s to %s", msg.Type, msg.ToAgent)
		return nil
	case <-time.After(DispatcherSendTimeout):
		logx.Warnf("DispatcherAdapter: send timeout after %v for message %s", DispatcherSendTimeout, msg.ID)
		return fmt.Errorf("dispatch send timeout after %v", DispatcherSendTimeout)
	case <-ctx.Done():
		logx.Warnf("DispatcherAdapter: context cancelled for message %s", msg.ID)
		return ctx.Err()
	}
}

// Driver manages the state machine for an architect workflow
type Driver struct {
	architectID       string
	stateStore        *state.Store
	contextManager    *contextmgr.ContextManager
	currentState      ArchitectState
	stateData         map[string]any
	llmClient         LLMClient           // Optional LLM for live mode
	renderer          *templates.Renderer // Template renderer for prompts
	workDir           string              // Workspace directory
	specFile          string              // Path to spec file
	storiesDir        string              // Directory for story files
	queue             *Queue              // Story queue manager
	escalationHandler *EscalationHandler  // Escalation handler
	dispatcher        DispatcherInterface // Interface for pulling messages

	// v3 Unified worker architecture
	readyStoryCh  chan string
	idleAgentCh   <-chan string          // Read-only channel from dispatcher
	specCh        <-chan *proto.AgentMsg // Read-only channel for spec messages
	requestDoneCh chan string            // Unified completion channel
	requestCh     chan *proto.AgentMsg // Unified request channel
	mergeCh       chan string          // Signal when code is approved and ready to merge
	requestWorker *RequestWorker
	workerCtx     context.Context
	workerCancel  context.CancelFunc
}

// ChannelConfig holds configuration for architect channels
type ChannelConfig struct {
	ReadyStoryChSize  int
	IdleAgentChSize   int
	RequestDoneChSize int
	MergeChSize       int
	RequestChSize     int
}

// DefaultChannelConfig returns default channel sizes
func DefaultChannelConfig() *ChannelConfig {
	return &ChannelConfig{
		ReadyStoryChSize:  1,
		IdleAgentChSize:   1,
		RequestDoneChSize: 1,
		MergeChSize:       1,
		RequestChSize:     10,
	}
}

// NewDriver creates a new architect driver instance (mock mode)
func NewDriver(architectID string, stateStore *state.Store, workDir, storiesDir string) *Driver {
	return NewDriverWithChannelConfig(architectID, stateStore, workDir, storiesDir, DefaultChannelConfig())
}

// NewDriverWithChannelConfig creates a new architect driver with configurable channel sizes
func NewDriverWithChannelConfig(architectID string, stateStore *state.Store, workDir, storiesDir string, channelConfig *ChannelConfig) *Driver {
	renderer, _ := templates.NewRenderer()
	queue := NewQueue(storiesDir)
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create buffered channels with configurable sizes
	readyStoryCh := make(chan string, channelConfig.ReadyStoryChSize)
	idleAgentChRW := make(chan string, channelConfig.IdleAgentChSize) // Read-write for mock mode
	requestDoneCh := make(chan string, channelConfig.RequestDoneChSize)
	mergeCh := make(chan string, channelConfig.MergeChSize)

	// Create unified request channel
	requestCh := make(chan *proto.AgentMsg, channelConfig.RequestChSize)

	// Connect queue to ready channel
	queue.SetReadyChannel(readyStoryCh)

	// Create mock dispatcher for workers
	mockDispatcher := NewMockDispatcher()

	// Create unified request worker (no LLM = mock mode)
	requestWorker := NewRequestWorker(nil, renderer, requestCh, requestDoneCh, mockDispatcher, architectID, queue, mergeCh)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManager(),
		currentState:      StateScoping,
		stateData:         make(map[string]any),
		llmClient:         nil,
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		escalationHandler: escalationHandler,
		dispatcher:        nil,
		readyStoryCh:      readyStoryCh,
		idleAgentCh:       idleAgentChRW, // Cast to read-only interface
		requestDoneCh:     requestDoneCh,
		requestCh:         requestCh,
		mergeCh:           mergeCh,
		requestWorker:     requestWorker,
	}
}

// NewDriverWithDispatcher creates a new architect driver with LLM and real dispatcher for production mode
func NewDriverWithDispatcher(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient LLMClient, dispatcher *dispatch.Dispatcher, workDir, storiesDir string) *Driver {
	return NewDriverWithDispatcherAndChannelConfig(architectID, stateStore, modelConfig, llmClient, dispatcher, workDir, storiesDir, DefaultChannelConfig())
}

// NewDriverWithDispatcherAndChannelConfig creates a new architect driver with configurable channels for production mode
func NewDriverWithDispatcherAndChannelConfig(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient LLMClient, dispatcher *dispatch.Dispatcher, workDir, storiesDir string, channelConfig *ChannelConfig) *Driver {
	renderer, _ := templates.NewRenderer()
	queue := NewQueue(storiesDir)
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create buffered channels with configurable sizes
	readyStoryCh := make(chan string, channelConfig.ReadyStoryChSize)
	// Subscribe to all architect notifications
	architectChannels := dispatcher.SubscribeArchitect(architectID)
	idleAgentCh := architectChannels.IdleAgents
	specCh := architectChannels.Specs
	requestDoneCh := make(chan string, channelConfig.RequestDoneChSize)
	mergeCh := make(chan string, channelConfig.MergeChSize)

	// Create unified request channel
	requestCh := make(chan *proto.AgentMsg, channelConfig.RequestChSize)

	// Connect queue to ready channel
	queue.SetReadyChannel(readyStoryCh)

	// For production mode, use the real dispatcher through adapter
	messageSender := NewDispatcherAdapter(dispatcher)

	// Create unified request worker with live LLM
	requestWorker := NewRequestWorker(llmClient, renderer, requestCh, requestDoneCh, messageSender, architectID, queue, mergeCh)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:      StateWaiting,
		stateData:         make(map[string]any),
		llmClient:         llmClient,
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		escalationHandler: escalationHandler,
		dispatcher:        dispatcher,
		readyStoryCh:      readyStoryCh,
		idleAgentCh:       idleAgentCh,
		specCh:            specCh,
		requestDoneCh:     requestDoneCh,
		requestCh:         requestCh,
		mergeCh:           mergeCh,
		requestWorker:     requestWorker,
	}
}

// Initialize sets up the driver and loads any existing state
func (d *Driver) Initialize(ctx context.Context) error {
	// Load existing state if available
	savedState, savedData, err := d.stateStore.LoadState(d.architectID)
	if err != nil {
		return fmt.Errorf("failed to load state for architect %s: %w", d.architectID, err)
	}

	// If we have saved state, restore it
	if savedState != "" {
		// Convert string state to ArchitectState using the auto-generated constants
		loadedState := d.stringToArchitectState(savedState)
		if loadedState == StateError && savedState != "Error" {
			logx.Warnf("loaded unknown state '%s', setting to ERROR", savedState)
		}
		d.currentState = loadedState
		d.stateData = savedData
	}

	// Start unified worker goroutine
	d.workerCtx, d.workerCancel = context.WithCancel(ctx)
	go d.requestWorker.Run(d.workerCtx)

	logx.Infof("Architect unified request worker started")

	return nil
}

// stringToArchitectState converts a string state to ArchitectState using auto-generated String() values
// Returns StateError for unknown states
func (d *Driver) stringToArchitectState(stateStr string) ArchitectState {
	// Use the auto-generated String() values from stringer
	for _, state := range GetAllArchitectStates() {
		if state.String() == stateStr {
			return state
		}
	}
	return StateError
}

// Shutdown stops the worker goroutines and cleans up resources gracefully
func (d *Driver) Shutdown() {
	if d.workerCancel != nil {
		// Signal workers to stop
		d.workerCancel()

		// Wait for workers to finish current tasks (with timeout)
		done := make(chan struct{})
		go func() {
			// Wait for workers to drain their channels
			for len(d.requestCh) > 0 {
				time.Sleep(100 * time.Millisecond)
			}
			close(done)
		}()

		select {
		case <-done:
			logx.Infof("Architect workers shutdown gracefully")
		case <-time.After(30 * time.Second):
			logx.Warnf("Architect workers shutdown timeout - forcing closure")
		}

		// Persist current state to disk before shutting down
		if err := d.stateStore.SaveState(d.architectID, d.currentState.String(), d.stateData); err != nil {
			logx.Errorf("failed to persist state during shutdown: %v", err)
		} else {
			logx.Infof("state persisted successfully during shutdown")
		}

		// Close channels after workers are done
		close(d.requestCh)
		close(d.readyStoryCh)
		// Note: idleAgentCh is owned by dispatcher, don't close it here
		close(d.requestDoneCh)
	}
}

// ProcessWorkflow runs the main state machine loop for the architect workflow
func (d *Driver) ProcessWorkflow(ctx context.Context, specFile string) error {
	// Check if this is a different spec file than what was previously processed
	if previousSpecFile, exists := d.stateData["spec_file"]; exists {
		if previousSpecFile != specFile {
			// Different spec file - restart the workflow from the beginning
			logx.Infof("new spec file detected, restarting workflow...")
			d.currentState = StateScoping
			d.stateData = make(map[string]any)
		}
	}

	// Store spec file path
	d.specFile = specFile
	d.stateData["spec_file"] = specFile
	d.stateData["started_at"] = time.Now().UTC()

	// Run the state machine loop with heartbeat
	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we're already in a terminal state
		if d.currentState == StateDone || d.currentState == StateError {
			break
		}

		// Process current state
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			// Transition to error state
			d.transitionTo(ctx, StateError, map[string]any{
				"error":        err.Error(),
				"failed_state": d.currentState.String(),
			})
			return err
		}

		// Transition to next state (always call transitionTo - let it handle self-transitions)
		if err := d.transitionTo(ctx, nextState, nil); err != nil {
			return fmt.Errorf("failed to transition to state %s: %w", nextState, err)
		}

		// Compact context if needed
		if err := d.contextManager.CompactIfNeeded(); err != nil {
			// Log warning but don't fail
			logx.Warnf("context compaction failed: %v", err)
		}
	}

	return nil
}

// RunStateMachine starts the architect's state machine loop in WAITING state
func (d *Driver) RunStateMachine(ctx context.Context) error {
	// Start in WAITING state, ready to receive specs
	d.currentState = StateWaiting
	d.stateData = make(map[string]any)
	d.stateData["started_at"] = time.Now().UTC()
	
	logx.Infof("🏗️ Architect state machine starting in WAITING state")
	
	// Run the state machine loop with heartbeat
	for {
		// Check context cancellation
		select {
		case <-ctx.Done():
			logx.Infof("🏗️ Architect state machine context cancelled")
			return ctx.Err()
		default:
		}

		// Check if we're already in a terminal state
		if d.currentState == StateDone || d.currentState == StateError {
			logx.Infof("🏗️ Architect state machine reached terminal state: %s", d.currentState)
			break
		}

		// Log current state processing
		logx.Infof("🏗️ Architect processing state: %s", d.currentState)

		// Process current state
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			logx.Errorf("🏗️ Architect state processing error in %s: %v", d.currentState, err)
			// Transition to error state
			d.transitionTo(ctx, StateError, map[string]any{
				"error":        err.Error(),
				"failed_state": d.currentState.String(),
			})
			return err
		}

		// Transition to next state (always call transitionTo - let it handle self-transitions)
		if err := d.transitionTo(ctx, nextState, nil); err != nil {
			logx.Errorf("🏗️ Architect state transition failed: %s -> %s: %v", d.currentState, nextState, err)
			return fmt.Errorf("failed to transition to state %s: %w", nextState, err)
		}

		// Compact context if needed
		if err := d.contextManager.CompactIfNeeded(); err != nil {
			// Log warning but don't fail
			logx.Warnf("context compaction failed: %v", err)
		}
	}

	logx.Infof("🏗️ Architect state machine completed")
	return nil
}

// handleWaiting blocks until a spec message is received
func (d *Driver) handleWaiting(ctx context.Context) (ArchitectState, error) {
	logx.Infof("🏗️ Architect entering WAITING state, waiting for spec...")
	logx.Infof("🏗️ Architect spec channel info: %p (waiting to receive)", d.specCh)
	
	select {
	case <-ctx.Done():
		logx.Infof("🏗️ Architect WAITING state context cancelled")
		return StateError, ctx.Err()
	case specMsg := <-d.specCh:
		logx.Infof("🏗️ Architect received spec message %s, transitioning to SCOPING", specMsg.ID)
		
		// Store the spec message for processing in SCOPING state
		d.stateData["spec_message"] = specMsg
		
		return StateScoping, nil
	}
}

// processCurrentState handles the logic for the current state
func (d *Driver) processCurrentState(ctx context.Context) (ArchitectState, error) {
	switch d.currentState {
	case StateWaiting:
		// WAITING state - block until spec received
		return d.handleWaiting(ctx)
	case StateScoping:
		return d.handleScoping(ctx)
	case StateDispatching:
		return d.handleDispatching(ctx)
	case StateMonitoring:
		return d.handleMonitoring(ctx)
	case StateRequest:
		return d.handleRequest(ctx)
	case StateEscalated:
		return d.handleEscalated(ctx)
	case StateMerging:
		return d.handleMerging(ctx)
	case StateDone:
		// DONE is a terminal state - should not continue processing
		return StateDone, nil
	case StateError:
		// ERROR is a terminal state - should not continue processing
		return StateError, nil
	default:
		return StateError, fmt.Errorf("unknown state: %s", d.currentState)
	}
}

// handleScoping processes the scoping phase (spec analysis and story generation)
func (d *Driver) handleScoping(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Scoping phase: analyzing specification and generating stories")

	if d.llmClient != nil {
		// Use LLM for spec parsing
		return d.handleScopingWithLLM(ctx)
	} else {
		// Fallback to mock mode
		return d.handleScopingMock(ctx)
	}
}

// handleScopingWithLLM uses the LLM to analyze the specification
func (d *Driver) handleScopingWithLLM(ctx context.Context) (ArchitectState, error) {
	// Extract spec file path from the SPEC message
	specFile := d.getSpecFileFromMessage()
	if specFile == "" {
		return StateError, fmt.Errorf("no spec file path found in SPEC message")
	}
	
	logx.Infof("🏗️ Architect reading spec file: %s", specFile)
	
	// Read raw spec file content
	rawSpecContent, err := os.ReadFile(specFile)
	if err != nil {
		return StateError, fmt.Errorf("failed to read spec file %s: %w", specFile, err)
	}

	// LLM-first approach: send raw content directly to LLM
	templateData := &templates.TemplateData{
		TaskContent: string(rawSpecContent),
		Context:     d.formatContextAsString(),
		Extra: map[string]any{
			"spec_file_path": specFile,
			"mode":           "llm_analysis",
		},
	}

	prompt, err := d.renderer.Render(templates.SpecAnalysisTemplate, templateData)
	if err != nil {
		return StateError, fmt.Errorf("failed to render spec analysis template: %w", err)
	}

	// Get LLM response
	response, err := d.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return StateError, fmt.Errorf("failed to get LLM response for spec parsing: %w", err)
	}

	// Parse LLM response to extract requirements
	requirements, parseErr := d.parseSpecAnalysisJSON(response)
	if parseErr != nil {
		// Graceful fallback: try deterministic parsing if LLM response fails
		logx.Warnf("LLM response parsing failed (%v), falling back to deterministic parser", parseErr)

		specParser := NewSpecParser(d.storiesDir)
		fallbackRequirements, fallbackErr := specParser.ParseSpecFile(specFile)
		if fallbackErr != nil {
			return StateError, fmt.Errorf("both LLM parsing and deterministic fallback failed. LLM error: %v, Fallback error: %v", parseErr, fallbackErr)
		}

		requirements = fallbackRequirements
		d.stateData["parsing_method"] = "deterministic_fallback"
		d.stateData["llm_parse_error"] = parseErr.Error()
	} else {
		d.stateData["parsing_method"] = "llm_primary"
	}

	// Add LLM response to context
	d.contextManager.AddMessage("assistant", response)

	// Store parsed requirements and LLM analysis
	d.stateData["requirements"] = requirements
	d.stateData["llm_analysis"] = response
	d.stateData["raw_spec_content"] = string(rawSpecContent)
	d.stateData["spec_parsing_completed_at"] = time.Now().UTC()

	// Generate story files immediately in the scoping phase
	specParser := NewSpecParser(d.storiesDir)
	storyFiles, err := specParser.GenerateStoryFiles(requirements)
	if err != nil {
		return StateError, fmt.Errorf("failed to generate story files: %w", err)
	}

	d.stateData["story_files"] = storyFiles
	d.stateData["stories_generated"] = true
	d.stateData["stories_count"] = len(storyFiles)

	logx.Infof("scoping completed using %s method, extracted %d requirements and generated %d stories",
		d.stateData["parsing_method"], len(requirements), len(storyFiles))

	return StateDispatching, nil
}

// handleScopingMock provides mock scoping behavior
func (d *Driver) handleScopingMock(ctx context.Context) (ArchitectState, error) {
	// Use real spec parser even in mock mode
	specParser := NewSpecParser(d.storiesDir)

	// Check if spec file exists, if not create mock requirements
	var requirements []Requirement
	var err error

	if d.specFile != "" {
		requirements, err = specParser.ParseSpecFile(d.specFile)
		if err != nil {
			// Fall back to mock requirements if file doesn't exist or can't be parsed
			requirements = []Requirement{
				{
					Title:              "Sample requirement 1",
					Description:        "Mock requirement description",
					AcceptanceCriteria: []string{"Criterion 1", "Criterion 2"},
					EstimatedPoints:    2,
				},
				{
					Title:              "Sample requirement 2",
					Description:        "Another mock requirement",
					AcceptanceCriteria: []string{"Criterion A", "Criterion B"},
					EstimatedPoints:    1,
				},
			}
		}
	} else {
		// Create mock requirements when no spec file provided
		requirements = []Requirement{
			{
				Title:              "Mock Health Endpoint",
				Description:        "Create a simple health check endpoint",
				AcceptanceCriteria: []string{"GET /health returns 200", "Response includes timestamp"},
				EstimatedPoints:    1,
			},
		}
	}

	// Generate story files immediately in the scoping phase
	storyFiles, err := specParser.GenerateStoryFiles(requirements)
	if err != nil {
		return StateError, fmt.Errorf("failed to generate story files: %w", err)
	}

	d.stateData["requirements"] = requirements
	d.stateData["story_files"] = storyFiles
	d.stateData["stories_generated"] = true
	d.stateData["stories_count"] = len(storyFiles)
	d.stateData["spec_parsing_completed_at"] = time.Now().UTC()

	logx.Infof("mock scoping completed, generated %d stories", len(storyFiles))

	return StateDispatching, nil
}

// handleDispatching processes the dispatching phase (queue management and story assignment)
func (d *Driver) handleDispatching(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Dispatching phase: managing queue and assigning stories")

	// Initialize queue if not already done
	if _, exists := d.stateData["queue_initialized"]; !exists {
		// Load stories from the stories directory
		if err := d.queue.LoadFromDirectory(); err != nil {
			return StateError, fmt.Errorf("failed to load stories from directory: %w", err)
		}

		// Detect cycles in dependencies
		cycles := d.queue.DetectCycles()
		if len(cycles) > 0 {
			return StateError, fmt.Errorf("dependency cycles detected: %v", cycles)
		}

		// Persist queue state to JSON for monitoring
		if err := d.persistQueueState(); err != nil {
			return StateError, fmt.Errorf("critical: failed to persist queue state: %w", err)
		}

		d.stateData["queue_initialized"] = true
		d.stateData["queue_management_completed_at"] = time.Now().UTC()

		// Get queue summary for logging
		summary := d.queue.GetQueueSummary()
		logx.Infof("queue loaded: %d stories (%d ready)",
			summary["total_stories"], summary["ready_stories"])
		d.stateData["queue_summary"] = summary
	}

	// Check if there are ready stories to dispatch
	if story := d.queue.NextReadyStory(); story != nil {
		// Transition to MONITORING to wait for coder requests
		return StateMonitoring, nil
	}

	// If no stories are ready and all are completed, we're done
	if d.queue.AllStoriesCompleted() {
		logx.Infof("all stories completed - transitioning to DONE")
		return StateDone, nil
	}

	// Otherwise, stay in DISPATCHING and wait for stories to become ready
	return StateDispatching, nil
}

// handleMonitoring processes the monitoring phase (waiting for coder requests)
func (d *Driver) handleMonitoring(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Monitoring phase: waiting for coder requests and review completions")

	// First, check if we need to dispatch any ready stories
	if story := d.queue.NextReadyStory(); story != nil {
		logx.Infof("🏗️ Found ready story %s, dispatching to coder", story.ID)
		if err := d.dispatchReadyStory(ctx, story.ID); err != nil {
			logx.Errorf("🏗️ Failed to dispatch story %s: %v", story.ID, err)
		} else {
			logx.Infof("🏗️ Successfully dispatched story %s", story.ID)
		}
		// Stay in monitoring to handle more stories or wait for responses
		return StateMonitoring, nil
	}

	// Check if all stories are completed
	if d.queue.AllStoriesCompleted() {
		logx.Infof("🏗️ All stories completed, transitioning to DONE")
		return StateDone, nil
	}

	// In monitoring state, we wait for either:
	// 1. Coder requests (transition to REQUEST)
	// 2. Approved code reviews (transition to MERGING)
	// 3. Heartbeat to check for new ready stories
	select {
	case requestID := <-d.requestDoneCh:
		logx.Infof("🏗️ Request completed: %s, checking if merge needed", requestID)
		// Check if there are pending merges
		select {
		case <-d.mergeCh:
			// There's a merge signal, transition to merging
			return StateMerging, nil
		default:
			// No merge signal, stay in monitoring
			return StateMonitoring, nil
		}

	case <-d.mergeCh:
		// There's a merge signal, transition to merging
		return StateMerging, nil

	case <-time.After(HeartbeatInterval):
		// Heartbeat debug logging
		logx.Debugf("🏗️ Monitoring heartbeat: checking for ready stories")
		return StateMonitoring, nil

	case <-ctx.Done():
		return StateError, ctx.Err()
	}
}

// handleRequest processes the request phase (handling coder requests)
func (d *Driver) handleRequest(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Request phase: processing coder request")

	// Request handling is done by the RequestWorker (to be implemented in ARCH-005)
	// For now, approve all requests and return to monitoring
	return StateMonitoring, nil
}

// handleEscalated processes the escalated phase (waiting for human intervention)
func (d *Driver) handleEscalated(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Escalated phase: waiting for human intervention")

	// Check escalation timeout (2 hours)
	if escalatedAt, exists := d.stateData["escalated_at"].(time.Time); exists {
		timeSinceEscalation := time.Since(escalatedAt)
		if timeSinceEscalation > EscalationTimeout {
			logx.Warnf("escalation timeout exceeded (%v > %v), sending ABANDON review and re-queuing",
				timeSinceEscalation.Truncate(time.Minute), EscalationTimeout)

			// Log timeout event for monitoring
			if d.escalationHandler != nil {
				d.escalationHandler.LogTimeout(escalatedAt, timeSinceEscalation)
			}

			// Send ABANDON review and re-queue story
			if err := d.sendAbandonAndRequeue(ctx); err != nil {
				logx.Errorf("failed to send ABANDON review and re-queue: %v", err)
				return StateError, fmt.Errorf("failed to handle escalation timeout: %w", err)
			}

			return StateDispatching, nil
		}

		// Log remaining time periodically (every hour in actual usage, but for demo we'll be more verbose)
		timeRemaining := EscalationTimeout - timeSinceEscalation
		logx.Debugf("escalation timeout: %v remaining (escalated %v ago)",
			timeRemaining.Truncate(time.Minute), timeSinceEscalation.Truncate(time.Minute))
	} else {
		// If we don't have an escalation timestamp, this is an error - we should always record when we escalate
		logx.Warnf("in ESCALATED state but no escalation timestamp found")
		return StateError, fmt.Errorf("invalid escalated state: no escalation timestamp")
	}

	// Check for pending escalations
	if d.escalationHandler != nil {
		summary := d.escalationHandler.GetEscalationSummary()
		if summary.PendingEscalations > 0 {
			// Still have pending escalations, stay in escalated state
			return StateEscalated, nil
		}
		// No more pending escalations, return to request handling
		return StateRequest, nil
	}

	// No escalation handler, return to request
	return StateRequest, nil
}

// handleMerging processes the merging phase (merging approved code)
func (d *Driver) handleMerging(ctx context.Context) (ArchitectState, error) {
	d.contextManager.AddMessage("assistant", "Merging phase: processing completed stories")

	// Wait for merge signal with heartbeat
	select {
	case storyID := <-d.mergeCh:
		logx.Infof("processing merge completion for story %s", storyID)

		// Story has been marked completed in queue by review process
		// Now check if there are newly ready stories to dispatch
		return StateDispatching, nil

	case <-time.After(HeartbeatInterval):
		// Heartbeat debug logging
		logx.Debugf("heartbeat: %s", d.currentState)
		return StateMerging, nil

	case <-ctx.Done():
		return StateError, ctx.Err()
	}
}

// transitionTo moves the driver to a new state and persists it
func (d *Driver) transitionTo(ctx context.Context, newState ArchitectState, additionalData map[string]any) error {
	oldState := d.currentState
	d.currentState = newState

	// Add transition metadata
	d.stateData["previous_state"] = oldState.String()
	d.stateData["current_state"] = newState.String()
	d.stateData["transition_at"] = time.Now().UTC()

	// Special handling for ESCALATED state - record escalation timestamp for timeout guard
	if newState == StateEscalated {
		d.stateData["escalated_at"] = time.Now().UTC()
		logx.Infof("entered ESCALATED state - timeout guard set for %v", EscalationTimeout)
	}

	// Merge additional data if provided
	if additionalData != nil {
		for k, v := range additionalData {
			d.stateData[k] = v
		}
	}

	// Persist state
	if err := d.stateStore.SaveState(d.architectID, newState.String(), d.stateData); err != nil {
		return fmt.Errorf("failed to persist state transition from %s to %s: %w", oldState, newState, err)
	}

	// Enhanced logging for debugging
	if oldState != newState {
		logx.Infof("🏗️ Architect state transition: %s → %s", oldState, newState)
	} else {
		logx.Infof("🏗️ Architect staying in state: %s", oldState)
	}

	return nil
}

// GetCurrentState returns the current state of the driver
func (d *Driver) GetCurrentState() ArchitectState {
	return d.currentState
}

// GetStateData returns a copy of the current state data
func (d *Driver) GetStateData() map[string]any {
	result := make(map[string]any)
	for k, v := range d.stateData {
		result[k] = v
	}
	return result
}

// GetAgentType returns the type of the agent
func (d *Driver) GetAgentType() agent.AgentType {
	return agent.AgentTypeArchitect
}

// GetContextSummary returns a summary of the current context
func (d *Driver) GetContextSummary() string {
	return d.contextManager.GetContextSummary()
}

// formatContextAsString formats the context messages as a string for LLM prompts
func (d *Driver) formatContextAsString() string {
	messages := d.contextManager.GetMessages()
	if len(messages) == 0 {
		return "No previous context"
	}

	var contextParts []string
	for _, msg := range messages {
		contextParts = append(contextParts, fmt.Sprintf("%s: %s", msg.Role, msg.Content))
	}

	return strings.Join(contextParts, "\n")
}

// parseSpecAnalysisResponse extracts requirements from LLM response
func (d *Driver) parseSpecAnalysisResponse(response string) []map[string]any {
	// Simple mock parsing - in real implementation would parse JSON response
	return []map[string]any{
		{
			"title":            "Parsed requirement from LLM",
			"description":      "LLM-generated requirement description",
			"estimated_points": 2,
		},
	}
}

// formatRequirementsForLLM converts requirements to a string format for LLM analysis
func (d *Driver) formatRequirementsForLLM(requirements []Requirement) string {
	var result strings.Builder
	result.WriteString("Extracted Requirements:\n\n")

	for i, req := range requirements {
		result.WriteString(fmt.Sprintf("%d. **%s**\n", i+1, req.Title))
		if req.Description != "" {
			result.WriteString(fmt.Sprintf("   Description: %s\n", req.Description))
		}
		if len(req.AcceptanceCriteria) > 0 {
			result.WriteString("   Acceptance Criteria:\n")
			for _, criterion := range req.AcceptanceCriteria {
				result.WriteString(fmt.Sprintf("   - %s\n", criterion))
			}
		}
		result.WriteString(fmt.Sprintf("   Estimated Points: %d\n\n", req.EstimatedPoints))
	}

	return result.String()
}

// convertToRequirements converts state data back to Requirements slice
func (d *Driver) convertToRequirements(data any) ([]Requirement, error) {
	// Handle slice of Requirement structs (from spec parser)
	if reqs, ok := data.([]Requirement); ok {
		return reqs, nil
	}

	// Handle slice of maps (from mock or legacy data)
	if reqMaps, ok := data.([]map[string]any); ok {
		var requirements []Requirement
		for _, reqMap := range reqMaps {
			req := Requirement{}

			if title, ok := reqMap["title"].(string); ok {
				req.Title = title
			}
			if desc, ok := reqMap["description"].(string); ok {
				req.Description = desc
			}
			if points, ok := reqMap["estimated_points"].(int); ok {
				req.EstimatedPoints = points
			}

			// Handle acceptance criteria
			if criteria, ok := reqMap["acceptance_criteria"]; ok {
				if criteriaSlice, ok := criteria.([]string); ok {
					req.AcceptanceCriteria = criteriaSlice
				}
			}

			requirements = append(requirements, req)
		}
		return requirements, nil
	}

	return nil, fmt.Errorf("unsupported requirements data type: %T", data)
}

// parseSpecAnalysisJSON parses the LLM's JSON response to extract requirements
func (d *Driver) parseSpecAnalysisJSON(response string) ([]Requirement, error) {
	// Try to extract JSON from the response
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON found in LLM response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]

	// Define the expected LLM response structure
	var llmResponse struct {
		Analysis     string `json:"analysis"`
		Requirements []struct {
			Title              string   `json:"title"`
			Description        string   `json:"description"`
			AcceptanceCriteria []string `json:"acceptance_criteria"`
			EstimatedPoints    int      `json:"estimated_points"`
			Dependencies       []string `json:"dependencies,omitempty"`
		} `json:"requirements"`
		NextAction string `json:"next_action"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &llmResponse); err != nil {
		return nil, fmt.Errorf("failed to parse LLM JSON response: %w", err)
	}

	// Convert to internal Requirement format
	var requirements []Requirement
	for _, req := range llmResponse.Requirements {
		requirement := Requirement{
			Title:              req.Title,
			Description:        req.Description,
			AcceptanceCriteria: req.AcceptanceCriteria,
			EstimatedPoints:    req.EstimatedPoints,
			Dependencies:       req.Dependencies,
		}

		// Validate and set reasonable defaults
		if requirement.EstimatedPoints < 1 || requirement.EstimatedPoints > 5 {
			requirement.EstimatedPoints = 2 // Default to medium complexity
		}

		if requirement.Title == "" {
			continue // Skip empty requirements
		}

		if len(requirement.AcceptanceCriteria) == 0 {
			requirement.AcceptanceCriteria = []string{
				"Implementation completes successfully",
				"All tests pass",
				"Code follows project conventions",
			}
		}

		requirements = append(requirements, requirement)
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("no valid requirements extracted from LLM response")
	}

	return requirements, nil
}

// persistQueueState saves the current queue state to the state store
func (d *Driver) persistQueueState() error {
	queueData, err := d.queue.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize queue: %w", err)
	}

	// Store queue data in state data for persistence
	d.stateData["queue_json"] = string(queueData)

	return nil
}

// GetQueue returns the queue manager for external access
func (d *Driver) GetQueue() *Queue {
	return d.queue
}

// GetEscalationHandler returns the escalation handler for external access
func (d *Driver) GetEscalationHandler() *EscalationHandler {
	return d.escalationHandler
}

// HandleTask processes incoming TASK messages (spec uploads, etc.)
func (d *Driver) HandleTask(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	logx.Infof("architect received task: %s", msg.ID)
	
	// Check task type
	taskType, exists := msg.GetPayload("type")
	if !exists {
		return nil, fmt.Errorf("missing task type in payload")
	}
	
	switch taskType {
	case "spec_upload":
		return d.handleSpecUpload(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported task type: %v", taskType)
	}
}

// handleSpecUpload processes specification file uploads
func (d *Driver) handleSpecUpload(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract spec information from message
	filename, _ := msg.GetPayload("filename")
	filepath, _ := msg.GetPayload("filepath") 
	size, _ := msg.GetPayload("size")
	
	logx.Infof("processing spec upload: %v (%v bytes)", filename, size)
	
	// Start the workflow processing with the uploaded spec
	if filepath != nil {
		d.specFile = fmt.Sprintf("%v", filepath)
		// Trigger spec parsing workflow
		go func() {
			if err := d.ProcessWorkflow(ctx, d.specFile); err != nil {
				logx.Errorf("failed to process uploaded spec workflow: %v", err)
			}
		}()
	}
	
	// Send immediate response to web UI
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "processing")
	response.SetPayload("message", fmt.Sprintf("Spec upload %v accepted and processing started", filename))
	
	return response, nil
}

// HandleQuestion processes incoming QUESTION messages (legacy adapter support)
func (d *Driver) HandleQuestion(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// In the new architecture, questions are handled by workers
	// This is a placeholder for legacy compatibility
	logx.Infof("architect received question: %s (processed by workers)", msg.ID)

	response := proto.NewAgentMsg(proto.MsgTypeANSWER, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "processed")
	response.SetPayload("message", "Question processed by answer worker")

	return response, nil
}

// HandleResult processes incoming RESULT messages (legacy adapter support)
func (d *Driver) HandleResult(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// In the new architecture, results are handled by workers
	// This is a placeholder for legacy compatibility
	logx.Infof("architect received result: %s (processed by workers)", msg.ID)

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "processed")
	response.SetPayload("message", "Result processed by review worker")

	return response, nil
}

// RouteMessage routes incoming messages to appropriate worker channels with timeout
func (d *Driver) RouteMessage(msg *proto.AgentMsg) error {
	// Validate message first
	if msg == nil {
		return fmt.Errorf("cannot route nil message")
	}
	if msg.ID == "" {
		return fmt.Errorf("cannot route message with empty ID")
	}
	if msg.FromAgent == "" {
		return fmt.Errorf("cannot route message with no sender")
	}

	// Add timeout for channel operations
	timeout := time.After(5 * time.Second)

	switch msg.Type {
	case proto.MsgTypeQUESTION, proto.MsgTypeREQUEST:
		select {
		case d.requestCh <- msg:
			logx.Infof("routed %s %s to unified request worker", msg.Type, msg.ID)
			return nil
		case <-timeout:
			return fmt.Errorf("timeout routing %s %s - channel full after 5s", msg.Type, msg.ID)
		}
	default:
		return fmt.Errorf("unknown message type for routing: %s", msg.Type)
	}
}

// dispatchReadyStory assigns a ready story to an available agent
func (d *Driver) dispatchReadyStory(ctx context.Context, storyID string) error {
	logx.Infof("🏗️ Dispatching ready story %s", storyID)
	
	// Get the story from queue
	story, exists := d.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
	}

	// Send to shared work queue via dispatcher - don't assign to specific agent
	logx.Infof("🏗️ Sending story %s to shared work queue", storyID)

	return d.sendStoryToSharedQueue(ctx, storyID)
}

// sendStoryToSharedQueue sends a story to the dispatcher's shared work queue
func (d *Driver) sendStoryToSharedQueue(ctx context.Context, storyID string) error {
	logx.Infof("🏗️ Sending story %s to shared work queue", storyID)
	
	// Mark story as dispatched (no specific agent yet)
	if err := d.queue.MarkInProgress(storyID, "shared_queue"); err != nil {
		return fmt.Errorf("failed to mark story as dispatched: %w", err)
	}
	
	// Create task message for the shared work queue (no specific agent)
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, d.architectID, "coder") // Generic "coder" target
	taskMsg.SetPayload(proto.KeyStoryID, storyID)
	taskMsg.SetPayload(proto.KeyTaskType, "implement_story")
	
	logx.Infof("🏗️ Created TASK message %s for story %s -> shared queue", taskMsg.ID, storyID)

	// Get story details
	if story, exists := d.queue.stories[storyID]; exists {
		taskMsg.SetPayload(proto.KeyTitle, story.Title)
		taskMsg.SetPayload(proto.KeyFilePath, story.FilePath)
		taskMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		taskMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)

		// Read and parse story content for the coder
		if content, requirements, err := d.parseStoryContent(story.FilePath); err == nil {
			taskMsg.SetPayload(proto.KeyContent, content)
			taskMsg.SetPayload(proto.KeyRequirements, requirements)
		} else {
			// Fallback to title if content parsing fails
			taskMsg.SetPayload(proto.KeyContent, story.Title)
			taskMsg.SetPayload(proto.KeyRequirements, []string{})
		}
	}

	// Send task to dispatcher shared queue
	logx.Infof("🏗️ Sending TASK message %s to dispatcher shared queue", taskMsg.ID)

	// Send via dispatcher if available (production mode)
	if d.dispatcher != nil {
		// Cast to the full dispatcher interface to access DispatchMessage
		if dispatcher, ok := d.dispatcher.(*dispatch.Dispatcher); ok {
			if err := dispatcher.DispatchMessage(taskMsg); err != nil {
				logx.Errorf("🏗️ Failed to dispatch TASK message %s: %v", taskMsg.ID, err)
				return err
			}
			logx.Infof("🏗️ Successfully dispatched TASK message %s to shared queue", taskMsg.ID)
			return nil
		}
	}

	// Mock mode - just log the assignment
	logx.Infof("🏗️ Mock mode: story sent to shared queue (logged only)")
	return nil
}

// assignStoryToAgent assigns a specific story to a specific agent (for direct responses)
func (d *Driver) assignStoryToAgent(ctx context.Context, storyID, agentID string) error {
	logx.Infof("🏗️ Assigning story %s to specific agent %s", storyID, agentID)
	
	// Mark story as in progress
	if err := d.queue.MarkInProgress(storyID, agentID); err != nil {
		return fmt.Errorf("failed to mark story as in progress: %w", err)
	}

	// Create task message for the specific agent
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, d.architectID, agentID)
	taskMsg.SetPayload(proto.KeyStoryID, storyID)
	taskMsg.SetPayload(proto.KeyTaskType, "implement_story")
	
	logx.Infof("🏗️ Created TASK message %s for story %s -> agent %s", taskMsg.ID, storyID, agentID)

	// Get story details
	if story, exists := d.queue.stories[storyID]; exists {
		taskMsg.SetPayload(proto.KeyTitle, story.Title)
		taskMsg.SetPayload(proto.KeyFilePath, story.FilePath)
		taskMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		taskMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)

		// Read and parse story content for the coder
		if content, requirements, err := d.parseStoryContent(story.FilePath); err == nil {
			taskMsg.SetPayload(proto.KeyContent, content)
			taskMsg.SetPayload(proto.KeyRequirements, requirements)
		} else {
			// Fallback to title if content parsing fails
			taskMsg.SetPayload(proto.KeyContent, story.Title)
			taskMsg.SetPayload(proto.KeyRequirements, []string{})
		}
	}

	// Send task to agent via dispatcher
	logx.Infof("🏗️ Sending TASK message %s to dispatcher for agent %s", taskMsg.ID, agentID)

	// Send via dispatcher if available (production mode)
	if d.dispatcher != nil {
		// Cast to the full dispatcher interface to access DispatchMessage
		if dispatcher, ok := d.dispatcher.(*dispatch.Dispatcher); ok {
			if err := dispatcher.DispatchMessage(taskMsg); err != nil {
				logx.Errorf("🏗️ Failed to dispatch TASK message %s: %v", taskMsg.ID, err)
				return err
			}
			logx.Infof("🏗️ Successfully dispatched TASK message %s", taskMsg.ID)
			return nil
		}
	}

	// Mock mode - just log the assignment
	logx.Infof("🏗️ Mock mode: story assignment logged only")
	return nil
}

// sendAbandonAndRequeue sends an ABANDON review response and re-queues the story
func (d *Driver) sendAbandonAndRequeue(ctx context.Context) error {
	// Get the escalated story ID from escalation handler
	if d.escalationHandler == nil {
		return fmt.Errorf("no escalation handler available")
	}

	summary := d.escalationHandler.GetEscalationSummary()
	if len(summary.Escalations) == 0 {
		return fmt.Errorf("no escalations found to abandon")
	}

	// Find the most recent pending escalation
	var latestEscalation *EscalationEntry
	for _, escalation := range summary.Escalations {
		if escalation.Status == "pending" {
			if latestEscalation == nil || escalation.EscalatedAt.After(latestEscalation.EscalatedAt) {
				latestEscalation = escalation
			}
		}
	}

	if latestEscalation == nil {
		return fmt.Errorf("no pending escalations found to abandon")
	}

	storyID := latestEscalation.StoryID
	agentID := latestEscalation.AgentID

	// Create ABANDON review message
	if d.dispatcher != nil {
		abandonMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, agentID)
		abandonMsg.SetPayload("story_id", storyID)
		abandonMsg.SetPayload("review_result", "ABANDON")
		abandonMsg.SetPayload("review_notes", "Escalation timeout exceeded - abandoning current submission")
		abandonMsg.SetPayload("reviewed_at", time.Now().UTC().Format(time.RFC3339))
		abandonMsg.SetPayload("timeout_reason", "escalation_timeout")

		// Send via dispatcher adapter
		if adapter, ok := d.dispatcher.(*dispatch.Dispatcher); ok {
			dispatcherAdapter := NewDispatcherAdapter(adapter)
			if err := dispatcherAdapter.SendMessage(abandonMsg); err != nil {
				return fmt.Errorf("failed to send ABANDON message: %w", err)
			}
		}
	}

	// Re-queue the story by resetting it to pending status
	story, exists := d.queue.GetStory(storyID)
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	// Reset to pending status so it can be picked up again
	story.Status = StatusPending
	story.AssignedAgent = ""
	story.StartedAt = nil
	story.CompletedAt = nil
	story.LastUpdated = time.Now().UTC()

	// Trigger ready notification if dependencies are met
	d.queue.checkAndNotifyReady()

	logx.Infof("abandoned story %s due to escalation timeout and re-queued", storyID)
	return nil
}

// parseStoryContent reads a story file and extracts content and requirements for the coder
func (d *Driver) parseStoryContent(filePath string) (string, []string, error) {
	// Read the story file
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read story file %s: %w", filePath, err)
	}

	content := string(fileBytes)

	// Skip YAML frontmatter (everything before the second ---)
	lines := strings.Split(content, "\n")
	contentStart := 0
	dashCount := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				contentStart = i + 1
				break
			}
		}
	}

	if contentStart >= len(lines) {
		return "", nil, fmt.Errorf("no content found after YAML frontmatter in %s", filePath)
	}

	// Get content after frontmatter
	contentLines := lines[contentStart:]
	storyContent := strings.Join(contentLines, "\n")

	// Extract Task description (everything after **Task** until **Acceptance Criteria**)
	taskStart := strings.Index(storyContent, "**Task**")
	criteriaStart := strings.Index(storyContent, "**Acceptance Criteria**")

	var taskDescription string
	if taskStart != -1 && criteriaStart != -1 {
		taskDescription = strings.TrimSpace(storyContent[taskStart+8 : criteriaStart])
	} else if taskStart != -1 {
		taskDescription = strings.TrimSpace(storyContent[taskStart+8:])
	} else {
		// Fallback: use first paragraph
		paragraphs := strings.Split(strings.TrimSpace(storyContent), "\n\n")
		if len(paragraphs) > 0 {
			taskDescription = strings.TrimSpace(paragraphs[0])
		}
	}

	// Extract requirements from Acceptance Criteria bullets
	var requirements []string
	if criteriaStart != -1 {
		criteriaSection := storyContent[criteriaStart+23:] // Skip "**Acceptance Criteria**"
		lines := strings.Split(criteriaSection, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "*") || strings.HasPrefix(line, "-") {
				// Remove bullet point marker and clean up
				requirement := strings.TrimSpace(line[1:])
				if requirement != "" {
					requirements = append(requirements, requirement)
				}
			}
		}
	}

	return taskDescription, requirements, nil
}

// getSpecFileFromMessage extracts the spec file path from the stored SPEC message
func (d *Driver) getSpecFileFromMessage() string {
	// Get the stored spec message
	specMsgData, exists := d.stateData["spec_message"]
	if !exists {
		logx.Errorf("🏗️ No spec_message found in state data")
		return ""
	}
	
	// Cast to AgentMsg
	specMsg, ok := specMsgData.(*proto.AgentMsg)
	if !ok {
		logx.Errorf("🏗️ spec_message is not an AgentMsg: %T", specMsgData)
		return ""
	}
	
	// Debug: log all payload keys
	payloadKeys := make([]string, 0)
	for key := range specMsg.Payload {
		payloadKeys = append(payloadKeys, key)
	}
	logx.Infof("🏗️ SPEC message payload keys: %v", payloadKeys)
	
	// Extract spec file path from payload - try different keys
	specFile, exists := specMsg.GetPayload("spec_file")
	if !exists {
		// Try alternative keys
		specFile, exists = specMsg.GetPayload("file_path")
		if !exists {
			specFile, exists = specMsg.GetPayload("filepath")
			if !exists {
				logx.Errorf("🏗️ No spec file path found in payload with keys: %v", payloadKeys)
				return ""
			}
		}
	}
	
	// Convert to string
	if specFileStr, ok := specFile.(string); ok {
		logx.Infof("🏗️ Found spec file path: %s", specFileStr)
		return specFileStr
	}
	
	logx.Errorf("🏗️ Spec file path is not a string: %T = %v", specFile, specFile)
	return ""
}
