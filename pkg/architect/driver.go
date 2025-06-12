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
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/templates"
)

// LLMClient defines the interface for language model interactions
type LLMClient interface {
	// GenerateResponse generates a response given a prompt
	GenerateResponse(ctx context.Context, prompt string) (string, error)
}

// DispatcherInterface defines the interface for pulling messages from the dispatcher
type DispatcherInterface interface {
	PullArchitectWork() *proto.AgentMsg
}

// State represents the current state of the architect workflow
type State string

const (
	StateSpecParsing        State = "SPEC_PARSING"
	StateStoryGeneration    State = "STORY_GENERATION"
	StateQueueManagement    State = "QUEUE_MANAGEMENT"
	StateDispatching        State = "DISPATCHING"
	StateAnswering          State = "ANSWERING"
	StateReviewing          State = "REVIEWING"
	StateAwaitHumanFeedback State = "AWAIT_HUMAN_FEEDBACK"
	StateCompleted          State = "COMPLETED"
	StateError              State = "ERROR"
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
	storyDispatcher   *StoryDispatcher    // Story dispatcher for agent assignment
	questionHandler   *QuestionHandler    // Question handler for ANSWERING state
	reviewEvaluator   *ReviewEvaluator    // Review evaluator for REVIEWING state
	escalationHandler *EscalationHandler  // Escalation handler for AWAIT_HUMAN_FEEDBACK state
	dispatcher        DispatcherInterface // Interface for pulling messages
}

// NewDriver creates a new architect driver instance
func NewDriver(architectID string, stateStore *state.Store, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	queue := NewQueue(storiesDir)

	// Create escalation handler
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create a mock story dispatcher for testing/demo
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Create question handler (no LLM = mock mode)
	questionHandler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Create review evaluator (no LLM = mock mode)
	reviewEvaluator := NewReviewEvaluator(nil, renderer, queue, workDir, escalationHandler)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManager(),
		currentState:      StateSpecParsing, // Default starting state
		stateData:         make(map[string]interface{}),
		llmClient:         nil, // No LLM - mock mode
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		storyDispatcher:   storyDispatcher,
		questionHandler:   questionHandler,
		reviewEvaluator:   reviewEvaluator,
		escalationHandler: escalationHandler,
		dispatcher:        nil, // No dispatcher for mock mode
	}
}

// NewDriverWithModel creates a new architect driver with model configuration
func NewDriverWithModel(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	queue := NewQueue(storiesDir)

	// Create escalation handler
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create a mock story dispatcher for testing/demo
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Create question handler (no LLM = mock mode)
	questionHandler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Create review evaluator (no LLM = mock mode)
	reviewEvaluator := NewReviewEvaluator(nil, renderer, queue, workDir, escalationHandler)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:      StateSpecParsing, // Default starting state
		stateData:         make(map[string]interface{}),
		llmClient:         nil, // No LLM - mock mode
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		storyDispatcher:   storyDispatcher,
		questionHandler:   questionHandler,
		reviewEvaluator:   reviewEvaluator,
		escalationHandler: escalationHandler,
		dispatcher:        nil, // No dispatcher for mock mode
	}
}

// NewDriverWithLLM creates a new architect driver with LLM integration for live mode
func NewDriverWithLLM(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient LLMClient, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	queue := NewQueue(storiesDir)

	// Create escalation handler
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create a mock story dispatcher for testing/demo
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Create question handler with live LLM
	questionHandler := NewQuestionHandler(llmClient, renderer, queue, escalationHandler)

	// Create review evaluator with live LLM
	reviewEvaluator := NewReviewEvaluator(llmClient, renderer, queue, workDir, escalationHandler)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:      StateSpecParsing, // Default starting state
		stateData:         make(map[string]interface{}),
		llmClient:         llmClient, // Live LLM mode
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		storyDispatcher:   storyDispatcher,
		questionHandler:   questionHandler,
		reviewEvaluator:   reviewEvaluator,
		escalationHandler: escalationHandler,
		dispatcher:        nil, // No dispatcher for mock mode
	}
}

// NewDriverWithO3 creates a new architect driver with OpenAI o3 integration
func NewDriverWithO3(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, apiKey string, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	queue := NewQueue(storiesDir)

	// Create escalation handler
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create a mock story dispatcher for testing/demo
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Use default o3 model - in the future this could be configurable via model name
	// For now, assume o3-mini for architecture tasks
	llmClient := agent.NewO3Client(apiKey)

	// Create question handler with O3 LLM
	questionHandler := NewQuestionHandler(llmClient, renderer, queue, escalationHandler)

	// Create review evaluator with O3 LLM
	reviewEvaluator := NewReviewEvaluator(llmClient, renderer, queue, workDir, escalationHandler)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:      StateSpecParsing, // Default starting state
		stateData:         make(map[string]interface{}),
		llmClient:         llmClient, // O3 LLM mode
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		storyDispatcher:   storyDispatcher,
		questionHandler:   questionHandler,
		reviewEvaluator:   reviewEvaluator,
		escalationHandler: escalationHandler,
		dispatcher:        nil, // No dispatcher for mock mode
	}
}

// NewDriverWithDispatcher creates a new architect driver with LLM and real dispatcher for production mode
func NewDriverWithDispatcher(architectID string, stateStore *state.Store, modelConfig *config.ModelCfg, llmClient LLMClient, dispatcher *dispatch.Dispatcher, workDir, storiesDir string) *Driver {
	renderer, _ := templates.NewRenderer() // Ignore error for now, fallback to mock mode
	queue := NewQueue(storiesDir)

	// Create escalation handler
	escalationHandler := NewEscalationHandler(workDir+"/logs", queue)

	// Create a REAL story dispatcher with live dispatcher
	storyDispatcher := NewStoryDispatcher(queue, dispatcher)

	// Create question handler with live LLM
	questionHandler := NewQuestionHandler(llmClient, renderer, queue, escalationHandler)

	// Create review evaluator with live LLM
	reviewEvaluator := NewReviewEvaluator(llmClient, renderer, queue, workDir, escalationHandler)

	return &Driver{
		architectID:       architectID,
		stateStore:        stateStore,
		contextManager:    contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:      StateSpecParsing, // Default starting state
		stateData:         make(map[string]interface{}),
		llmClient:         llmClient, // Live LLM
		renderer:          renderer,
		workDir:           workDir,
		storiesDir:        storiesDir,
		queue:             queue,
		storyDispatcher:   storyDispatcher,
		questionHandler:   questionHandler,
		reviewEvaluator:   reviewEvaluator,
		escalationHandler: escalationHandler,
		dispatcher:        dispatcher, // Store dispatcher for pull-based messaging
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

	return nil
}

// HandleQuestion processes incoming QUESTION messages from agents
func (d *Driver) HandleQuestion(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Forward to the question handler
	err := d.questionHandler.HandleQuestion(ctx, msg)
	if err != nil {
		return nil, err
	}

	// Create acknowledgment response - use ANSWER type for questions
	response := proto.NewAgentMsg(proto.MsgTypeANSWER, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "question_received")
	response.SetPayload("message", "Question is being processed")

	return response, nil
}

// HandleRequest processes incoming REQUEST messages from agents (approval requests)
func (d *Driver) HandleRequest(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Forward to the review evaluator for approval processing
	err := d.reviewEvaluator.HandleResult(ctx, msg)
	if err != nil {
		return nil, err
	}

	// Create approval response - use RESULT type for approval decisions
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "request_received")
	response.SetPayload("message", "Approval request is being processed")

	return response, nil
}

// HandleResult processes incoming RESULT messages from agents (code submissions)
func (d *Driver) HandleResult(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Forward to the review evaluator
	err := d.reviewEvaluator.HandleResult(ctx, msg)
	if err != nil {
		return nil, err
	}

	// Create acknowledgment response
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, d.architectID, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "submission_received")
	response.SetPayload("message", "Code submission is being reviewed")

	return response, nil
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
		if d.currentState == StateCompleted || d.currentState == StateError {
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
	case StateQueueManagement:
		return d.handleQueueManagement(ctx)
	case StateDispatching:
		return d.handleDispatching(ctx)
	case StateAnswering:
		return d.handleAnswering(ctx)
	case StateReviewing:
		return d.handleReviewing(ctx)
	case StateAwaitHumanFeedback:
		return d.handleAwaitHumanFeedback(ctx)
	case StateCompleted:
		// COMPLETED is a terminal state - should not continue processing
		return StateCompleted, nil
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

	return StateQueueManagement, nil
}

// handleQueueManagement processes the queue management phase
func (d *Driver) handleQueueManagement(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Queue management phase: managing story dependencies")

	// Load stories from the stories directory
	if err := d.queue.LoadFromDirectory(); err != nil {
		return StateError, fmt.Errorf("failed to load stories from directory: %w", err)
	}

	// Detect cycles in dependencies
	cycles := d.queue.DetectCycles()
	if len(cycles) > 0 {
		return StateError, fmt.Errorf("dependency cycles detected: %v", cycles)
	}

	// Get queue summary for logging
	summary := d.queue.GetQueueSummary()
	fmt.Printf("Queue loaded: %d stories (%d ready)\n",
		summary["total_stories"], summary["ready_stories"])

	// Store queue state data
	d.stateData["queue_initialized"] = true
	d.stateData["queue_summary"] = summary
	d.stateData["queue_management_completed_at"] = time.Now().UTC()

	// Persist queue state to JSON
	if err := d.persistQueueState(); err != nil {
		// Log warning but don't fail - queue is still in memory
		fmt.Printf("Warning: failed to persist queue state: %v\n", err)
	}

	return StateDispatching, nil
}

// handleDispatching processes the dispatching phase
func (d *Driver) handleDispatching(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Dispatching phase: assigning stories to agents")

	// The dispatcher should already be running from the orchestrator
	// Don't start/stop it here as it needs to stay running for the entire workflow
	if d.storyDispatcher.dispatcher != nil {
		// Dispatcher is managed by the orchestrator, not by the architect
		// Just verify it's available for dispatching
	}

	// Use the story dispatcher to assign ready stories to agents
	result, err := d.storyDispatcher.DispatchReadyStories(ctx)
	if err != nil {
		return StateError, fmt.Errorf("failed to dispatch stories: %w", err)
	}

	// Log dispatch results
	fmt.Printf("Dispatch completed: %d stories assigned\n", result.StoriesDispatched)
	for _, assignment := range result.Assignments {
		fmt.Printf("  - Assigned story %s to agent %s\n", assignment.StoryID, assignment.AgentID)
	}

	if len(result.Errors) > 0 {
		fmt.Printf("Dispatch warnings:\n")
		for _, errMsg := range result.Errors {
			fmt.Printf("  - %s\n", errMsg)
		}
	}

	// Store dispatch results
	d.stateData["stories_dispatched"] = result.StoriesDispatched
	d.stateData["dispatch_assignments"] = result.Assignments
	d.stateData["dispatch_errors"] = result.Errors
	d.stateData["dispatching_completed_at"] = time.Now().UTC()

	// Get assignment status for monitoring
	assignmentStatus := d.storyDispatcher.GetAssignmentStatus()
	d.stateData["active_assignments"] = assignmentStatus.ActiveAssignments

	// Persist updated queue state
	if err := d.persistQueueState(); err != nil {
		fmt.Printf("Warning: failed to persist queue state: %v\n", err)
	}

	// Determine next state based on dispatch results
	if result.StoriesDispatched > 0 {
		// Stories were dispatched, we should monitor their progress and handle interactions
		return StateAnswering, nil
	} else {
		// No stories were dispatched, check if all work is actually done
		allCompleted := d.queue.AllStoriesCompleted()
		if allCompleted {
			return StateCompleted, nil
		} else {
			// Still have pending work but nothing ready to dispatch
			// Wait a bit and try again (or handle questions)
			return StateAnswering, nil
		}
	}
}

// handleAnswering processes the answering phase (technical Q&A)
func (d *Driver) handleAnswering(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Answering phase: handling technical questions")

	questionsHandled := 0

	if d.dispatcher != nil {
		// Use pull-based approach with real dispatcher
		fmt.Printf("Using dispatcher to pull questions from queue...\n")

		// Poll for questions with a timeout
		timeout := time.After(5 * time.Second)           // Wait up to 5 seconds for questions
		ticker := time.NewTicker(500 * time.Millisecond) // Check every 500ms
		defer ticker.Stop()

		for {
			select {
			case <-timeout:
				// Timeout reached, stop polling
				goto checkCompletion
			case <-ticker.C:
				// Pull a message from the dispatcher (could be QUESTION or REQUEST)
				if message := d.dispatcher.PullArchitectWork(); message != nil {
					fmt.Printf("Architect pulled message: %s (type: %s)\n", message.ID, message.Type)

					var response *proto.AgentMsg
					var err error

					// Process based on message type
					switch message.Type {
					case proto.MsgTypeQUESTION:
						// Information request - use HandleQuestion (sends ANSWER)
						response, err = d.HandleQuestion(ctx, message)
					case proto.MsgTypeREQUEST:
						// Approval request - use HandleRequest (sends RESULT)
						response, err = d.HandleRequest(ctx, message)
					default:
						fmt.Printf("Unexpected message type %s for architect\n", message.Type)
						continue
					}

					if err != nil {
						fmt.Printf("Failed to process message %s: %v\n", message.ID, err)
					} else if response != nil {
						// The response is handled by the handlers through the dispatcher
						questionsHandled++
						fmt.Printf("Successfully processed message %s\n", message.ID)
					}
				}
			}
		}

	checkCompletion:
		fmt.Printf("Question Handler Status: %d questions handled this cycle\n", questionsHandled)

	} else {
		// Fallback to legacy behavior for mock mode
		questionStatus := d.questionHandler.GetQuestionStatus()

		fmt.Printf("Question Handler Status: %d total, %d pending, %d answered, %d escalated\n",
			questionStatus.TotalQuestions,
			questionStatus.PendingQuestions,
			questionStatus.AnsweredQuestions,
			questionStatus.EscalatedQuestions,
		)

		// Store question handling statistics
		d.stateData["questions_total"] = questionStatus.TotalQuestions
		d.stateData["questions_pending"] = questionStatus.PendingQuestions
		d.stateData["questions_answered"] = questionStatus.AnsweredQuestions
		d.stateData["questions_escalated"] = questionStatus.EscalatedQuestions

		// Clean up answered questions to free memory
		cleared := d.questionHandler.ClearAnsweredQuestions()
		if cleared > 0 {
			fmt.Printf("Cleared %d answered questions from memory\n", cleared)
		}
	}

	d.stateData["answering_completed_at"] = time.Now().UTC()

	// Check if there are ready stories waiting to be dispatched
	readyStories := d.queue.GetReadyStories()
	if len(readyStories) > 0 {
		// New stories became ready (dependencies completed), go back to dispatching
		return StateDispatching, nil
	}

	// Check if there are still active assignments before completing
	assignmentStatus := d.storyDispatcher.GetAssignmentStatus()
	fmt.Printf("Assignment status check: %d active assignments, %d total assignments\n",
		assignmentStatus.ActiveAssignments, len(assignmentStatus.Assignments))
	if assignmentStatus.ActiveAssignments > 0 {
		// Still have active work, continue answering questions
		fmt.Printf("Active assignments: %d, continuing to answer questions...\n", assignmentStatus.ActiveAssignments)
		return StateAnswering, nil
	}

	// Check if there are still pending questions before completing
	questionStatus := d.questionHandler.GetQuestionStatus()
	fmt.Printf("Question status check: %d pending, %d answered, %d total questions\n",
		questionStatus.PendingQuestions, questionStatus.AnsweredQuestions, questionStatus.TotalQuestions)
	if questionStatus.PendingQuestions > 0 {
		// Still have pending questions, continue answering
		fmt.Printf("Pending questions: %d, continuing to answer questions...\n", questionStatus.PendingQuestions)
		return StateAnswering, nil
	}

	// TEMPORARY FIX: Since current implementation is mock/demo mode where agents complete immediately,
	// we need to mark stories as completed when they finish their mock processing.
	// This handles the gap between mock agent completion and proper state tracking.

	// Check if we have stories that finished processing but aren't marked complete
	if questionsHandled > 0 && questionStatus.PendingQuestions == 0 && assignmentStatus.ActiveAssignments == 0 {
		// In mock mode, agents complete their workflow immediately and clear assignments
		// But stories remain in StatusInProgress, causing the infinite loop
		allStories := d.queue.GetAllStories()
		storiesNeedingCompletion := 0

		for _, story := range allStories {
			if story.Status == StatusInProgress {
				// Check if this story has any active assignments
				hasActiveAssignment := false
				for _, assignment := range assignmentStatus.Assignments {
					if assignment.StoryID == story.ID {
						hasActiveAssignment = true
						break
					}
				}

				if !hasActiveAssignment {
					storiesNeedingCompletion++
					fmt.Printf("Marking story %s as completed (mock agent finished processing)\n", story.ID)
					if err := d.queue.MarkCompleted(story.ID); err != nil {
						fmt.Printf("Warning: failed to mark story %s as completed: %v\n", story.ID, err)
					}
				}
			}
		}

		if storiesNeedingCompletion > 0 {
			fmt.Printf("Marked %d stories as completed after mock agent processing\n", storiesNeedingCompletion)
		}
	}

	// Check if all stories are completed AND no pending questions
	if d.queue.AllStoriesCompleted() && questionStatus.PendingQuestions == 0 {
		fmt.Printf("All stories completed and no pending questions - transitioning to COMPLETED\n")
		return StateCompleted, nil
	}

	// If we handled questions this cycle but still have work, continue
	if questionsHandled > 0 && d.dispatcher != nil {
		return StateAnswering, nil
	}

	// No questions handled this cycle and using dispatcher - we're done
	if d.dispatcher != nil {
		return StateCompleted, nil
	}

	// Legacy behavior: continue monitoring
	time.Sleep(1 * time.Second)
	return StateAnswering, nil
}

// handleReviewing processes the code review phase
func (d *Driver) handleReviewing(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Reviewing phase: evaluating code submissions")

	// Check for incoming RESULT messages (code submissions)
	// In a real implementation, this would listen to a message queue or dispatcher
	// For now, we simulate the reviewing capability

	// Get current review status
	reviewStatus := d.reviewEvaluator.GetReviewStatus()

	// Log current review processing status
	fmt.Printf("Review Evaluator Status: %d total, %d pending, %d approved, %d needs fixes\n",
		reviewStatus.TotalReviews,
		reviewStatus.PendingReviews,
		reviewStatus.ApprovedReviews,
		reviewStatus.NeedsFixesReviews,
	)

	// Store review processing statistics
	d.stateData["reviews_total"] = reviewStatus.TotalReviews
	d.stateData["reviews_pending"] = reviewStatus.PendingReviews
	d.stateData["reviews_approved"] = reviewStatus.ApprovedReviews
	d.stateData["reviews_needs_fixes"] = reviewStatus.NeedsFixesReviews
	d.stateData["reviewing_completed_at"] = time.Now().UTC()

	// Clean up completed reviews to free memory
	cleared := d.reviewEvaluator.ClearCompletedReviews()
	if cleared > 0 {
		fmt.Printf("Cleared %d completed reviews from memory\n", cleared)
	}

	// The REVIEWING state is typically an ongoing state that processes code submissions as they arrive
	// For the MVP, we transition to COMPLETED to indicate the reviewing capability is ready
	// In a full implementation, this would stay in REVIEWING and process submissions continuously
	return StateCompleted, nil
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

	return StateCompleted, nil
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

// GetStoryDispatcher returns the story dispatcher for external access
func (d *Driver) GetStoryDispatcher() *StoryDispatcher {
	return d.storyDispatcher
}

// GetQuestionHandler returns the question handler for external access
func (d *Driver) GetQuestionHandler() *QuestionHandler {
	return d.questionHandler
}

// GetReviewEvaluator returns the review evaluator for external access
func (d *Driver) GetReviewEvaluator() *ReviewEvaluator {
	return d.reviewEvaluator
}

// GetEscalationHandler returns the escalation handler for external access
func (d *Driver) GetEscalationHandler() *EscalationHandler {
	return d.escalationHandler
}
