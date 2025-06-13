package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
)

// LLMClient defines the interface for language model interactions
type LLMClient interface {
	// GenerateResponse generates a response given a prompt
	GenerateResponse(ctx context.Context, prompt string) (string, error)
}

// AnswerWorker handles QUESTION messages using LLM
type AnswerWorker struct {
	llmClient          LLMClient
	renderer           *templates.Renderer
	questionCh         chan *proto.AgentMsg
	questionAnsweredCh chan string
	dispatcher         MessageSender
	architectID        string
}

// ReviewWorker handles REQUEST messages for code review
type ReviewWorker struct {
	llmClient    LLMClient
	renderer     *templates.Renderer
	reviewReqCh  chan *proto.AgentMsg
	reviewDoneCh chan string
	dispatcher   MessageSender
	architectID  string
}

// NewAnswerWorker creates a new answer worker
func NewAnswerWorker(llmClient LLMClient, renderer *templates.Renderer, questionCh chan *proto.AgentMsg, questionAnsweredCh chan string, dispatcher MessageSender, architectID string) *AnswerWorker {
	return &AnswerWorker{
		llmClient:          llmClient,
		renderer:           renderer,
		questionCh:         questionCh,
		questionAnsweredCh: questionAnsweredCh,
		dispatcher:         dispatcher,
		architectID:        architectID,
	}
}

// NewReviewWorker creates a new review worker
func NewReviewWorker(llmClient LLMClient, renderer *templates.Renderer, reviewReqCh chan *proto.AgentMsg, reviewDoneCh chan string, dispatcher MessageSender, architectID string) *ReviewWorker {
	return &ReviewWorker{
		llmClient:    llmClient,
		renderer:     renderer,
		reviewReqCh:  reviewReqCh,
		reviewDoneCh: reviewDoneCh,
		dispatcher:   dispatcher,
		architectID:  architectID,
	}
}

// Run starts the answer worker goroutine
func (w *AnswerWorker) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("AnswerWorker panic recovered: %v\n", r)
			// Consider restarting the worker or notifying the system
		}
	}()
	
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-w.questionCh:
			// Process with error handling
			if err := w.processMessage(ctx, msg); err != nil {
				fmt.Printf("AnswerWorker error processing %s: %v\n", msg.ID, err)
				// Send error response back to agent
				w.sendErrorResponse(msg, err)
			}
			
			// Signal completion
			select {
			case w.questionAnsweredCh <- msg.ID:
			case <-ctx.Done():
				return
			}
		}
	}
}

// processMessage handles the core message processing logic for AnswerWorker
func (w *AnswerWorker) processMessage(ctx context.Context, msg *proto.AgentMsg) error {
	// Process question using LLM
	var response string
	if w.llmClient != nil {
		// Use LLM to generate answer
		question, exists := msg.GetPayload("question")
		if !exists {
			return fmt.Errorf("no question payload in message")
		}
		llmResponse, err := w.llmClient.GenerateResponse(ctx, fmt.Sprintf("%v", question))
		if err != nil {
			return fmt.Errorf("failed to generate LLM response: %w", err)
		}
		response = llmResponse
		fmt.Printf("AnswerWorker: processed question %s\n", msg.ID)
	} else {
		// Mock mode - generate mock answer
		response = "Mock answer: This is a simulated response from the architect"
		fmt.Printf("AnswerWorker: mock processing question %s\n", msg.ID)
	}
	
	// Send answer back to agent
	if w.dispatcher != nil {
		answerMsg := proto.NewAgentMsg(proto.MsgTypeANSWER, w.architectID, msg.FromAgent)
		answerMsg.ParentMsgID = msg.ID
		answerMsg.SetPayload("answer", response)
		answerMsg.SetPayload("status", "answered")
		
		if err := w.dispatcher.SendMessage(answerMsg); err != nil {
			return fmt.Errorf("failed to send answer: %w", err)
		}
	}
	
	return nil
}

// sendErrorResponse sends an error response back to the agent
func (w *AnswerWorker) sendErrorResponse(msg *proto.AgentMsg, err error) {
	if w.dispatcher == nil {
		return
	}
	
	errorMsg := proto.NewAgentMsg(proto.MsgTypeANSWER, w.architectID, msg.FromAgent)
	errorMsg.ParentMsgID = msg.ID
	errorMsg.SetPayload("answer", fmt.Sprintf("Error processing question: %v", err))
	errorMsg.SetPayload("status", "error")
	
	if sendErr := w.dispatcher.SendMessage(errorMsg); sendErr != nil {
		fmt.Printf("AnswerWorker: failed to send error response: %v\n", sendErr)
	}
}

// Run starts the review worker goroutine
func (w *ReviewWorker) Run(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("ReviewWorker panic recovered: %v\n", r)
			// Consider restarting the worker or notifying the system
		}
	}()
	
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-w.reviewReqCh:
			// Process with error handling
			if err := w.processMessage(ctx, msg); err != nil {
				fmt.Printf("ReviewWorker error processing %s: %v\n", msg.ID, err)
				// Send error response back to agent
				w.sendErrorResponse(msg, err)
			}
			
			// Signal completion
			select {
			case w.reviewDoneCh <- msg.ID:
			case <-ctx.Done():
				return
			}
		}
	}
}

// processMessage handles the core message processing logic for ReviewWorker
func (w *ReviewWorker) processMessage(ctx context.Context, msg *proto.AgentMsg) error {
	var approved bool = true
	var feedback string
	
	if w.llmClient != nil {
		// Use LLM for code review
		code, exists := msg.GetPayload("code")
		if !exists {
			return fmt.Errorf("no code payload in message")
		}
		llmResponse, err := w.llmClient.GenerateResponse(ctx, fmt.Sprintf("Review this code: %v", code))
		if err != nil {
			return fmt.Errorf("failed to generate LLM review: %w", err)
		}
		feedback = llmResponse
		// For now, always approve in LLM mode (real logic would parse LLM response)
		approved = true
		fmt.Printf("ReviewWorker: processed review %s\n", msg.ID)
	} else {
		// Mock mode - auto-approve with mock feedback
		approved = true
		feedback = "Mock review: Code looks good, auto-approved for demo"
		fmt.Printf("ReviewWorker: mock processing review %s\n", msg.ID)
	}
	
	// Send review result back to agent
	if w.dispatcher != nil {
		resultMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, w.architectID, msg.FromAgent)
		resultMsg.ParentMsgID = msg.ID
		resultMsg.SetPayload("approved", approved)
		resultMsg.SetPayload("feedback", feedback)
		resultMsg.SetPayload("status", "reviewed")
		
		if err := w.dispatcher.SendMessage(resultMsg); err != nil {
			return fmt.Errorf("failed to send review result: %w", err)
		}
	}
	
	return nil
}

// sendErrorResponse sends an error response back to the agent
func (w *ReviewWorker) sendErrorResponse(msg *proto.AgentMsg, err error) {
	if w.dispatcher == nil {
		return
	}
	
	errorMsg := proto.NewAgentMsg(proto.MsgTypeRESULT, w.architectID, msg.FromAgent)
	errorMsg.ParentMsgID = msg.ID
	errorMsg.SetPayload("approved", false)
	errorMsg.SetPayload("feedback", fmt.Sprintf("Error processing review: %v", err))
	errorMsg.SetPayload("status", "error")
	
	if sendErr := w.dispatcher.SendMessage(errorMsg); sendErr != nil {
		fmt.Printf("ReviewWorker: failed to send error response: %v\n", sendErr)
	}
}

// DispatcherInterface defines the interface for pulling messages from the dispatcher
type DispatcherInterface interface {
	PullArchitectWork() *proto.AgentMsg
}

// MessageSender defines the interface for sending messages to agents
type MessageSender interface {
	SendMessage(msg *proto.AgentMsg) error
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
	m.sentMessages = append(m.sentMessages, msg)
	answer, _ := msg.GetPayload("answer")
	fmt.Printf("📤 MockDispatcher: would send %s to %s (content: %v)\n", 
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
	if d.dispatcher == nil {
		return fmt.Errorf("dispatcher not initialized")
	}
	
	// Use the dispatcher's existing DispatchMessage method
	err := d.dispatcher.DispatchMessage(msg)
	if err != nil {
		return fmt.Errorf("failed to dispatch message %s: %w", msg.ID, err)
	}
	
	fmt.Printf("📤 DispatcherAdapter: sent %s to %s\n", msg.Type, msg.ToAgent)
	return nil
}

// State represents the current state of the architect workflow
type State string

const (
	StateSpecParsing        State = "SPEC_PARSING"
	StateStoryGeneration    State = "STORY_GENERATION"
	StateQueueAndDispatch   State = "QUEUE_AND_DISPATCH"
	StateAwaitHumanFeedback State = "AWAIT_HUMAN_FEEDBACK"
	StateDone              State = "DONE"
	StateError             State = "ERROR"
)

// Driver manages the state machine for an architect workflow
type Driver struct {
	architectID       string
	stateStore        *state.Store
	contextManager    *contextmgr.ContextManager
	currentState      State
	stateData         map[string]interface{}
	llmClient         LLMClient           // Optional LLM for live mode
	renderer          *templates.Renderer // Template renderer for prompts
	workDir           string              // Workspace directory
	specFile          string              // Path to spec file
	storiesDir        string              // Directory for story files
	queue             *Queue              // Story queue manager
	escalationHandler *EscalationHandler  // Escalation handler
	dispatcher        DispatcherInterface // Interface for pulling messages

	// v2 Channel-based workers
	readyStoryCh        chan string
	idleAgentCh         <-chan string  // Read-only channel from dispatcher
	reviewDoneCh        chan string
	questionAnsweredCh  chan string
	questionCh          chan *proto.AgentMsg
	reviewReqCh         chan *proto.AgentMsg
	answerWorker        *AnswerWorker
	reviewWorker        *ReviewWorker
	workerCtx           context.Context
	workerCancel        context.CancelFunc
}

// NewDriver creates a new architect driver instance (mock mode)
func NewDriver(architectID string, stateStore *state.Store, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer()
	queue := NewQueue(storiesDir)
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create buffered channels (size 1 as per spec)
	readyStoryCh := make(chan string, 1)
	idleAgentChRW := make(chan string, 1)  // Read-write for mock mode
	reviewDoneCh := make(chan string, 1)
	questionAnsweredCh := make(chan string, 1)

	// Create worker channels
	questionCh := make(chan *proto.AgentMsg, 10)
	reviewReqCh := make(chan *proto.AgentMsg, 10)

	// Connect queue to ready channel
	queue.SetReadyChannel(readyStoryCh)

	// Create mock dispatcher for workers
	mockDispatcher := NewMockDispatcher()

	// Create workers (no LLM = mock mode)
	answerWorker := NewAnswerWorker(nil, renderer, questionCh, questionAnsweredCh, mockDispatcher, architectID)
	reviewWorker := NewReviewWorker(nil, renderer, reviewReqCh, reviewDoneCh, mockDispatcher, architectID)

	return &Driver{
		architectID:        architectID,
		stateStore:         stateStore,
		contextManager:     contextmgr.NewContextManager(),
		currentState:       StateSpecParsing,
		stateData:          make(map[string]interface{}),
		llmClient:          nil,
		renderer:           renderer,
		workDir:            workDir,
		storiesDir:         storiesDir,
		queue:              queue,
		escalationHandler:  escalationHandler,
		dispatcher:         nil,
		readyStoryCh:       readyStoryCh,
		idleAgentCh:        idleAgentChRW,  // Cast to read-only interface
		reviewDoneCh:       reviewDoneCh,
		questionAnsweredCh: questionAnsweredCh,
		questionCh:         questionCh,
		reviewReqCh:        reviewReqCh,
		answerWorker:       answerWorker,
		reviewWorker:       reviewWorker,
	}
}

// NewDriverWithDispatcher creates a new architect driver with LLM and real dispatcher for production mode
func NewDriverWithDispatcher(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient LLMClient, dispatcher *dispatch.Dispatcher, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer()
	queue := NewQueue(storiesDir)
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create buffered channels (size 1 as per spec)
	readyStoryCh := make(chan string, 1)
	// Subscribe to dispatcher's idle agent notifications
	idleAgentCh := dispatcher.SubscribeIdleAgents(architectID)
	reviewDoneCh := make(chan string, 1)
	questionAnsweredCh := make(chan string, 1)

	// Create worker channels
	questionCh := make(chan *proto.AgentMsg, 10)
	reviewReqCh := make(chan *proto.AgentMsg, 10)

	// Connect queue to ready channel
	queue.SetReadyChannel(readyStoryCh)

	// For production mode, use the real dispatcher through adapter
	messageSender := NewDispatcherAdapter(dispatcher)

	// Create workers with live LLM
	answerWorker := NewAnswerWorker(llmClient, renderer, questionCh, questionAnsweredCh, messageSender, architectID)
	reviewWorker := NewReviewWorker(llmClient, renderer, reviewReqCh, reviewDoneCh, messageSender, architectID)

	return &Driver{
		architectID:        architectID,
		stateStore:         stateStore,
		contextManager:     contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:       StateSpecParsing,
		stateData:          make(map[string]interface{}),
		llmClient:          llmClient,
		renderer:           renderer,
		workDir:            workDir,
		storiesDir:         storiesDir,
		queue:              queue,
		escalationHandler:  escalationHandler,
		dispatcher:         dispatcher,
		readyStoryCh:       readyStoryCh,
		idleAgentCh:        idleAgentCh,
		reviewDoneCh:       reviewDoneCh,
		questionAnsweredCh: questionAnsweredCh,
		questionCh:         questionCh,
		reviewReqCh:        reviewReqCh,
		answerWorker:       answerWorker,
		reviewWorker:       reviewWorker,
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
		d.currentState = State(savedState)
		d.stateData = savedData
	}

	// Start worker goroutines
	d.workerCtx, d.workerCancel = context.WithCancel(ctx)
	go d.answerWorker.Run(d.workerCtx)
	go d.reviewWorker.Run(d.workerCtx)

	fmt.Printf("Architect workers started (answer and review)\n")

	return nil
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
			for len(d.questionCh) > 0 || len(d.reviewReqCh) > 0 {
				time.Sleep(100 * time.Millisecond)
			}
			close(done)
		}()
		
		select {
		case <-done:
			fmt.Printf("Architect workers shutdown gracefully\n")
		case <-time.After(30 * time.Second):
			fmt.Printf("Architect workers shutdown timeout - forcing closure\n")
		}
		
		// Close channels after workers are done
		close(d.questionCh)
		close(d.reviewReqCh)
		close(d.readyStoryCh)
		// Note: idleAgentCh is owned by dispatcher, don't close it here
		close(d.reviewDoneCh)
		close(d.questionAnsweredCh)
	}
}


// ProcessWorkflow runs the main state machine loop for the architect workflow
func (d *Driver) ProcessWorkflow(ctx context.Context, specFile string) error {
	// Check if this is a different spec file than what was previously processed
	if previousSpecFile, exists := d.stateData["spec_file"]; exists {
		if previousSpecFile != specFile {
			// Different spec file - restart the workflow from the beginning
			fmt.Printf("🔄 New spec file detected, restarting workflow...\n")
			d.currentState = StateSpecParsing
			d.stateData = make(map[string]interface{})
		}
	}

	// Store spec file path
	d.specFile = specFile
	d.stateData["spec_file"] = specFile
	d.stateData["started_at"] = time.Now().UTC()

	// Run the state machine loop
	maxIterations := 100 // Prevent infinite loops
	iteration := 0

mainLoop:
	for iteration < maxIterations {
		iteration++

		// Check context cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Check if we're already in a terminal state
		if d.currentState == StateDone || d.currentState == StateError {
			break mainLoop
		}

		// Process current state
		nextState, err := d.processCurrentState(ctx)
		if err != nil {
			// Transition to error state
			d.transitionTo(ctx, StateError, map[string]interface{}{
				"error":        err.Error(),
				"failed_state": string(d.currentState),
			})
			return err
		}

		// Transition to next state
		if nextState != d.currentState {
			d.transitionTo(ctx, nextState, nil)
		} else {
			// If state didn't change and it's not a terminal state, something is wrong
			return fmt.Errorf("state machine stuck in state %s", d.currentState)
		}

		// Compact context if needed
		if err := d.contextManager.CompactIfNeeded(); err != nil {
			// Log warning but don't fail
			fmt.Printf("Warning: context compaction failed: %v\n", err)
		}
	}

	if iteration >= maxIterations {
		return fmt.Errorf("state machine exceeded maximum iterations (%d)", maxIterations)
	}

	return nil
}

// processCurrentState handles the logic for the current state
func (d *Driver) processCurrentState(ctx context.Context) (State, error) {
	switch d.currentState {
	case StateSpecParsing:
		return d.handleSpecParsing(ctx)
	case StateStoryGeneration:
		return d.handleStoryGeneration(ctx)
	case StateQueueAndDispatch:
		return d.handleQueueAndDispatch(ctx)
	case StateAwaitHumanFeedback:
		return d.handleAwaitHumanFeedback(ctx)
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

// handleSpecParsing processes the spec parsing phase
func (d *Driver) handleSpecParsing(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Spec parsing phase: analyzing specification")

	if d.llmClient != nil {
		// Use LLM for spec parsing
		return d.handleSpecParsingWithLLM(ctx)
	} else {
		// Fallback to mock mode
		return d.handleSpecParsingMock(ctx)
	}
}

// handleSpecParsingWithLLM uses the LLM to analyze the specification
func (d *Driver) handleSpecParsingWithLLM(ctx context.Context) (State, error) {
	// Read raw spec file content
	rawSpecContent, err := os.ReadFile(d.specFile)
	if err != nil {
		return StateError, fmt.Errorf("failed to read spec file %s: %w", d.specFile, err)
	}

	// LLM-first approach: send raw content directly to LLM
	templateData := &templates.TemplateData{
		TaskContent: string(rawSpecContent),
		Context:     d.formatContextAsString(),
		Extra: map[string]interface{}{
			"spec_file_path": d.specFile,
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
		fmt.Printf("Warning: LLM response parsing failed (%v), falling back to deterministic parser\n", parseErr)

		specParser := NewSpecParser(d.storiesDir)
		fallbackRequirements, fallbackErr := specParser.ParseSpecFile(d.specFile)
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

	fmt.Printf("Spec parsing completed using %s method, extracted %d requirements\n",
		d.stateData["parsing_method"], len(requirements))

	return StateStoryGeneration, nil
}

// handleSpecParsingMock provides mock spec parsing behavior
func (d *Driver) handleSpecParsingMock(ctx context.Context) (State, error) {
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

	d.stateData["requirements"] = requirements
	d.stateData["spec_parsing_completed_at"] = time.Now().UTC()

	return StateStoryGeneration, nil
}

// handleStoryGeneration processes the story generation phase
func (d *Driver) handleStoryGeneration(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Story generation phase: creating story files")

	// Get requirements from previous state
	requirementsData, exists := d.stateData["requirements"]
	if !exists {
		return StateError, fmt.Errorf("no requirements found from spec parsing state")
	}

	// Convert to requirements slice
	requirements, err := d.convertToRequirements(requirementsData)
	if err != nil {
		return StateError, fmt.Errorf("failed to convert requirements data: %w", err)
	}

	// Generate story files
	specParser := NewSpecParser(d.storiesDir)
	storyFiles, err := specParser.GenerateStoryFiles(requirements)
	if err != nil {
		return StateError, fmt.Errorf("failed to generate story files: %w", err)
	}

	d.stateData["story_files"] = storyFiles
	d.stateData["stories_generated"] = true
	d.stateData["stories_count"] = len(storyFiles)
	d.stateData["story_generation_completed_at"] = time.Now().UTC()

	// Log generated stories
	fmt.Printf("Generated %d story files:\n", len(storyFiles))
	for _, story := range storyFiles {
		fmt.Printf("  - %s: %s\n", story.ID, story.Title)
	}

	return StateQueueAndDispatch, nil
}

// handleQueueAndDispatch processes the merged queue management and dispatching phase with channel-based workers
func (d *Driver) handleQueueAndDispatch(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Queue and dispatch phase: managing stories and agents with channel workers")

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
		fmt.Printf("Queue loaded: %d stories (%d ready)\n",
			summary["total_stories"], summary["ready_stories"])
		d.stateData["queue_summary"] = summary
	}

	// Main dispatch loop with channel select
	for {
		select {
		case <-ctx.Done():
			return StateError, ctx.Err()

		case readyStoryID := <-d.readyStoryCh:
			// New story became ready for dispatch
			fmt.Printf("📥 Story ready for dispatch: %s\n", readyStoryID)
			// Dispatch story to available agent
			if err := d.dispatchReadyStory(ctx, readyStoryID); err != nil {
				fmt.Printf("Failed to dispatch story %s: %v\n", readyStoryID, err)
				// Re-queue the story for later attempt by putting it back in ready channel
				select {
				case d.readyStoryCh <- readyStoryID:
				default:
					// Channel full, story will be picked up on next cycle
				}
			}

		case idleAgentID := <-d.idleAgentCh:
			// Agent became idle and available for work
			fmt.Printf("👤 Agent available: %s\n", idleAgentID)
			// Check for ready stories to assign
			if story := d.queue.NextReadyStory(); story != nil {
				if err := d.assignStoryToAgent(ctx, story.ID, idleAgentID); err != nil {
					fmt.Printf("Failed to assign story to agent: %v\n", err)
				}
			}

		case reviewedMsgID := <-d.reviewDoneCh:
			// Review worker completed a review
			fmt.Printf("✅ Review completed: %s\n", reviewedMsgID)
			// TODO: Process review completion

		case answeredMsgID := <-d.questionAnsweredCh:
			// Answer worker completed answering a question
			fmt.Printf("💬 Question answered: %s\n", answeredMsgID)
			// TODO: Process answer completion

		default:
			// No channel activity - check completion conditions
			
			// Check if all stories are completed
			if d.queue.AllStoriesCompleted() {
				fmt.Printf("✨ All stories completed - transitioning to DONE\n")
				return StateDone, nil
			}

			// Check for business escalations
			if d.escalationHandler != nil {
				summary := d.escalationHandler.GetEscalationSummary()
				if summary.PendingEscalations > 0 {
					fmt.Printf("🚨 %d escalations require human feedback\n", summary.PendingEscalations)
					return StateAwaitHumanFeedback, nil
				}
			}

			// Check if spec was updated (would trigger restart)
			if previousSpecFile, exists := d.stateData["spec_file"]; exists {
				if previousSpecFile != d.specFile {
					fmt.Printf("🔄 Spec file updated, restarting from parsing\n")
					return StateSpecParsing, nil
				}
			}

			// Brief pause to avoid busy waiting
			time.Sleep(100 * time.Millisecond)
		}
	}
}



// handleAwaitHumanFeedback processes the human feedback phase
func (d *Driver) handleAwaitHumanFeedback(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Await human feedback phase: managing escalated issues")

	// Get pending escalations from the escalation handler
	pendingEscalations := d.escalationHandler.GetEscalations("pending")
	acknowledgedEscalations := d.escalationHandler.GetEscalations("acknowledged")

	// Log current escalation status
	summary := d.escalationHandler.GetEscalationSummary()
	fmt.Printf("🚨 Human feedback required: %d pending, %d acknowledged escalations\n",
		summary.PendingEscalations, len(acknowledgedEscalations))

	// Store escalation data for monitoring
	d.stateData["pending_escalations"] = len(pendingEscalations)
	d.stateData["acknowledged_escalations"] = len(acknowledgedEscalations)
	d.stateData["total_escalations"] = summary.TotalEscalations
	d.stateData["escalation_summary"] = summary
	d.stateData["await_human_feedback_completed_at"] = time.Now().UTC()

	// Check if there are any stories awaiting human feedback
	awaitingFeedbackStories := d.queue.GetStoriesByStatus(StatusAwaitHumanFeedback)
	d.stateData["stories_awaiting_feedback"] = len(awaitingFeedbackStories)

	if len(pendingEscalations) > 0 {
		fmt.Printf("📋 Pending escalations requiring human intervention:\n")
		for _, escalation := range pendingEscalations {
			fmt.Printf("   - %s: %s (priority: %s, story: %s)\n",
				escalation.ID,
				truncateString(escalation.Question, 80),
				escalation.Priority,
				escalation.StoryID)
		}
	}

	if len(awaitingFeedbackStories) > 0 {
		fmt.Printf("📋 Stories awaiting human feedback:\n")
		for _, story := range awaitingFeedbackStories {
			fmt.Printf("   - %s: %s\n", story.ID, story.Title)
		}
	}

	// In the architect workflow, this is typically a terminal state
	// until human intervention resolves the escalations
	fmt.Printf("🔄 Architect workflow paused - awaiting human intervention\n")
	fmt.Printf("   Use 'agentctl architect list-escalations' to view and manage escalations\n")

	return StateDone, nil
}

// transitionTo moves the driver to a new state and persists it
func (d *Driver) transitionTo(ctx context.Context, newState State, additionalData map[string]interface{}) error {
	oldState := d.currentState
	d.currentState = newState

	// Add transition metadata
	d.stateData["previous_state"] = string(oldState)
	d.stateData["current_state"] = string(newState)
	d.stateData["transition_at"] = time.Now().UTC()

	// Merge additional data if provided
	if additionalData != nil {
		for k, v := range additionalData {
			d.stateData[k] = v
		}
	}

	// Persist state
	if err := d.stateStore.SaveState(d.architectID, string(newState), d.stateData); err != nil {
		return fmt.Errorf("failed to persist state transition from %s to %s: %w", oldState, newState, err)
	}

	// Log transition for debugging
	fmt.Printf("Architect state transition: %s → %s\n", oldState, newState)

	return nil
}

// GetCurrentState returns the current state of the driver
func (d *Driver) GetCurrentState() State {
	return d.currentState
}

// GetStateData returns a copy of the current state data
func (d *Driver) GetStateData() map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range d.stateData {
		result[k] = v
	}
	return result
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
func (d *Driver) parseSpecAnalysisResponse(response string) []map[string]interface{} {
	// Simple mock parsing - in real implementation would parse JSON response
	return []map[string]interface{}{
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
func (d *Driver) convertToRequirements(data interface{}) ([]Requirement, error) {
	// Handle slice of Requirement structs (from spec parser)
	if reqs, ok := data.([]Requirement); ok {
		return reqs, nil
	}

	// Handle slice of maps (from mock or legacy data)
	if reqMaps, ok := data.([]map[string]interface{}); ok {
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

// HandleQuestion processes incoming QUESTION messages (legacy adapter support)
func (d *Driver) HandleQuestion(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// In the new architecture, questions are handled by workers
	// This is a placeholder for legacy compatibility
	fmt.Printf("Architect received question: %s (processed by workers)\n", msg.ID)
	
	response := proto.NewAgentMsg(proto.MsgTypeANSWER, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "processed")
	response.SetPayload("message", "Question processed by answer worker")
	
	return response, nil
}

// HandleResult processes incoming RESULT messages (legacy adapter support)
func (d *Driver) HandleResult(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// In the new architecture, results are handled by workers
	// This is a placeholder for legacy compatibility
	fmt.Printf("Architect received result: %s (processed by workers)\n", msg.ID)
	
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "processed")
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
	case proto.MsgTypeQUESTION:
		select {
		case d.questionCh <- msg:
			fmt.Printf("🔀 Routed QUESTION %s to answer worker\n", msg.ID)
			return nil
		case <-timeout:
			return fmt.Errorf("timeout routing question %s - channel full after 5s", msg.ID)
		}
	case proto.MsgTypeREQUEST:
		select {
		case d.reviewReqCh <- msg:
			fmt.Printf("🔀 Routed REQUEST %s to review worker\n", msg.ID)
			return nil
		case <-timeout:
			return fmt.Errorf("timeout routing request %s - channel full after 5s", msg.ID)
		}
	default:
		return fmt.Errorf("unknown message type for routing: %s", msg.Type)
	}
}

// dispatchReadyStory assigns a ready story to an available agent
func (d *Driver) dispatchReadyStory(ctx context.Context, storyID string) error {
	// Get the story from queue
	story, exists := d.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}
	
	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
	}
	
	// Use logical agent name "coder" instead of hardcoded ID
	// The dispatcher will resolve this to an actual available coder agent
	agentID := "coder"
	
	return d.assignStoryToAgent(ctx, storyID, agentID)
}

// assignStoryToAgent assigns a specific story to a specific agent
func (d *Driver) assignStoryToAgent(ctx context.Context, storyID, agentID string) error {
	// Mark story as in progress
	if err := d.queue.MarkInProgress(storyID, agentID); err != nil {
		return fmt.Errorf("failed to mark story as in progress: %w", err)
	}
	
	// Create task message for the agent
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, d.architectID, agentID)
	taskMsg.SetPayload("story_id", storyID)
	taskMsg.SetPayload("task_type", "implement_story")
	
	// Get story details
	if story, exists := d.queue.stories[storyID]; exists {
		taskMsg.SetPayload("title", story.Title)
		taskMsg.SetPayload("file_path", story.FilePath)
		taskMsg.SetPayload("estimated_points", story.EstimatedPoints)
		taskMsg.SetPayload("depends_on", story.DependsOn)
	}
	
	// Send task to agent via dispatcher
	fmt.Printf("📋 Assigned story %s to agent %s\n", storyID, agentID)
	
	// Send via dispatcher if available (production mode)
	if d.dispatcher != nil {
		// Cast to the full dispatcher interface to access DispatchMessage
		if dispatcher, ok := d.dispatcher.(*dispatch.Dispatcher); ok {
			return dispatcher.DispatchMessage(taskMsg)
		}
	}
	
	// Mock mode - just log the assignment
	fmt.Printf("🔄 Mock mode: story assignment logged only\n")
	return nil
}
