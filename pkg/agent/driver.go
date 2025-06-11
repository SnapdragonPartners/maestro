package agent

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/state"
)

// State represents the current state of the agent workflow
type State string

const (
	StatePlanning      State = "PLANNING"
	StateToolInvocation State = "TOOL_INVOCATION"
	StateCoding        State = "CODING"
	StateTesting       State = "TESTING"
	StateAwaitApproval State = "AWAIT_APPROVAL"
	StateDone          State = "DONE"
	StateError         State = "ERROR"
	StateQuestion      State = "QUESTION"
	StateFixing        State = "FIXING"
)

// Driver manages the state machine for an agent workflow
type Driver struct {
	agentID        string
	stateStore     *state.Store
	contextManager *contextmgr.ContextManager
	currentState   State
	stateData      map[string]interface{}
}

// NewDriver creates a new agent driver instance
func NewDriver(agentID string, stateStore *state.Store) *Driver {
	return &Driver{
		agentID:        agentID,
		stateStore:     stateStore,
		contextManager: contextmgr.NewContextManager(),
		currentState:   StatePlanning, // Default starting state
		stateData:      make(map[string]interface{}),
	}
}

// NewDriverWithModel creates a new agent driver with model configuration
func NewDriverWithModel(agentID string, stateStore *state.Store, modelConfig *config.ModelCfg) *Driver {
	return &Driver{
		agentID:        agentID,
		stateStore:     stateStore,
		contextManager: contextmgr.NewContextManagerWithModel(modelConfig),
		currentState:   StatePlanning, // Default starting state
		stateData:      make(map[string]interface{}),
	}
}

// Initialize sets up the driver and loads any existing state
func (d *Driver) Initialize(ctx context.Context) error {
	// Load existing state if available
	savedState, savedData, err := d.stateStore.LoadState(d.agentID)
	if err != nil {
		return fmt.Errorf("failed to load state for agent %s: %w", d.agentID, err)
	}
	
	// If we have saved state, restore it
	if savedState != "" {
		d.currentState = State(savedState)
		d.stateData = savedData
	}
	
	return nil
}

// ProcessTask runs the main state machine loop for processing a task
func (d *Driver) ProcessTask(ctx context.Context, taskContent string) error {
	// Add initial task to context
	d.contextManager.AddMessage("user", taskContent)
	
	// Store initial task data
	d.stateData["task_content"] = taskContent
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
				"error": err.Error(),
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
	case StatePlanning:
		return d.handlePlanningState(ctx)
	case StateToolInvocation:
		return d.handleToolInvocationState(ctx)
	case StateCoding:
		return d.handleCodingState(ctx)
	case StateTesting:
		return d.handleTestingState(ctx)
	case StateAwaitApproval:
		return d.handleAwaitApprovalState(ctx)
	case StateFixing:
		return d.handleFixingState(ctx)
	case StateQuestion:
		return d.handleQuestionState(ctx)
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

// handlePlanningState processes the planning phase
func (d *Driver) handlePlanningState(ctx context.Context) (State, error) {
	// For now, simulate planning phase
	// In story 031, this will use actual prompt templates
	
	d.contextManager.AddMessage("assistant", "Planning phase: analyzing requirements")
	
	// Simulate some planning work
	d.stateData["plan"] = "Analyzed requirements, ready to proceed with implementation"
	d.stateData["planning_completed_at"] = time.Now().UTC()
	
	// Check if we need tools
	taskContent, _ := d.stateData["task_content"].(string)
	if taskContent != "" && (containsKeyword(taskContent, "shell") || containsKeyword(taskContent, "command")) {
		return StateToolInvocation, nil
	}
	
	// Otherwise go straight to coding
	return StateCoding, nil
}

// handleToolInvocationState processes tool invocation
func (d *Driver) handleToolInvocationState(ctx context.Context) (State, error) {
	// For now, simulate tool usage
	// In story 032, this will actually execute tools
	
	d.contextManager.AddMessage("assistant", "Tool invocation phase: executing necessary tools")
	
	// Simulate tool execution
	toolResult := map[string]interface{}{
		"stdout":    "Tool execution completed successfully",
		"stderr":    "",
		"exit_code": 0,
	}
	
	d.stateData["tool_results"] = toolResult
	d.stateData["tool_invocation_completed_at"] = time.Now().UTC()
	
	return StateCoding, nil
}

// handleCodingState processes the coding phase
func (d *Driver) handleCodingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Coding phase: implementing solution")
	
	// Simulate code generation
	d.stateData["code_generated"] = true
	d.stateData["coding_completed_at"] = time.Now().UTC()
	
	return StateTesting, nil
}

// handleTestingState processes the testing phase
func (d *Driver) handleTestingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Testing phase: running tests")
	
	// For MVP, always assume tests pass to avoid infinite loops
	// In future stories, this will have proper test execution and failure handling
	testsPassed := true
	
	d.stateData["tests_passed"] = testsPassed
	d.stateData["testing_completed_at"] = time.Now().UTC()
	
	if !testsPassed {
		// Check if we've already tried fixing
		_, alreadyFixed := d.stateData["fixes_applied"]
		if alreadyFixed {
			// If we've already tried fixing and tests still fail, give up
			return StateError, fmt.Errorf("tests failed after fixing attempts")
		}
		return StateFixing, nil
	}
	
	return StateAwaitApproval, nil
}

// handleAwaitApprovalState processes the approval phase
func (d *Driver) handleAwaitApprovalState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Awaiting approval from architect")
	
	// For now, simulate automatic approval
	// In story 034, this will actually communicate with architect
	approved := true
	
	d.stateData["approval_status"] = approved
	d.stateData["approval_completed_at"] = time.Now().UTC()
	
	if approved {
		return StateDone, nil
	} else {
		return StateFixing, nil
	}
}

// handleFixingState processes the fixing phase
func (d *Driver) handleFixingState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Fixing phase: addressing issues")
	
	// Simulate fixing
	d.stateData["fixes_applied"] = true
	d.stateData["fixing_completed_at"] = time.Now().UTC()
	
	// After fixing, return to coding
	return StateCoding, nil
}

// handleQuestionState processes the question phase
func (d *Driver) handleQuestionState(ctx context.Context) (State, error) {
	d.contextManager.AddMessage("assistant", "Question phase: awaiting clarification")
	
	// For now, simulate getting an answer
	// In story 033, this will actually communicate with architect
	d.stateData["question_answered"] = true
	d.stateData["question_completed_at"] = time.Now().UTC()
	
	// Return to the appropriate state after getting answer
	return StateCoding, nil
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
	if err := d.stateStore.SaveState(d.agentID, string(newState), d.stateData); err != nil {
		return fmt.Errorf("failed to persist state transition from %s to %s: %w", oldState, newState, err)
	}
	
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

// containsKeyword checks if text contains a keyword (case-insensitive)
func containsKeyword(text, keyword string) bool {
	// Simple case-insensitive search
	// Could be enhanced with more sophisticated matching
	text = fmt.Sprintf(" %s ", text) // Add spaces for word boundary matching
	keyword = fmt.Sprintf(" %s ", keyword)
	
	for i := 0; i <= len(text)-len(keyword); i++ {
		match := true
		for j := 0; j < len(keyword); j++ {
			c1 := text[i+j]
			c2 := keyword[j]
			
			// Convert to lowercase for comparison
			if c1 >= 'A' && c1 <= 'Z' {
				c1 = c1 + 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 = c2 + 32
			}
			
			if c1 != c2 {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	
	return false
}