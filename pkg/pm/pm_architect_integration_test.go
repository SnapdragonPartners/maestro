package pm

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/architect"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// TestPMArchitectSpecReviewFlow tests the integration between PM and Architect
// for spec submission and review with feedback loop.
func TestPMArchitectSpecReviewFlow(t *testing.T) {
	t.Skip("Integration test - requires full setup including dispatcher and mock agents")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create dispatcher
	cfg := &config.Config{}
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Create mock PM that generates and submits a spec
	pmAgent := createMockPMAgent(t, dispatcher)

	// Create mock Architect that reviews specs
	architectAgent := createMockArchitectAgent(t, dispatcher)

	// Start both agents
	go pmAgent.Run(ctx)
	go architectAgent.Run(ctx)

	// Simulate PM generating a spec
	// This would trigger:
	// 1. PM INTERVIEWING -> DRAFTING -> SUBMITTING
	// 2. PM sends REQUEST(type=spec) to architect
	// 3. Architect receives REQUEST in handleSpecReview
	// 4. Architect sends RESPONSE with approval/feedback
	// 5. PM receives RESPONSE in WAITING state

	// Wait for completion or timeout
	<-ctx.Done()
}

// TestPMSubmittingToolCall tests that PM SUBMITTING state calls spec_submit tool.
func TestPMSubmittingToolCall(t *testing.T) {
	// This test verifies that:
	// 1. PM calls spec_submit tool with draft spec markdown
	// 2. Tool validates the spec
	// 3. Tool returns success/failure with validation errors
	// 4. PM handles both success and failure cases appropriately

	tests := []struct {
		name             string
		draftSpec        string
		expectSuccess    bool
		expectTransition proto.State
	}{
		{
			name: "Valid spec - submits to architect",
			draftSpec: `---
version: "1.0"
priority: must
---

# Feature: Test Feature

## Vision
Test feature vision.

## Scope
### In Scope
- Item 1

### Out of Scope
- Item 2

## Requirements

### R-001: Test Requirement
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Test requirement description.

**Acceptance Criteria:**
- [ ] Criterion 1
`,
			expectSuccess:    true,
			expectTransition: StateWaiting,
		},
		{
			name: "Invalid spec - missing requirements",
			draftSpec: `---
version: "1.0"
---

# Feature: Test Feature

## Vision
Test feature vision.
`,
			expectSuccess:    false,
			expectTransition: StateWorking,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO: Implement test with mock tool provider
			// 1. Create mock PM driver
			// 2. Set draft_spec in stateData
			// 3. Call handleSubmitting
			// 4. Verify tool was called with correct args
			// 5. Verify correct state transition
			t.Skip("TODO: Implement with mock tool provider")
		})
	}
}

// TestArchitectSpecReview tests the architect's spec review functionality.
func TestArchitectSpecReview(t *testing.T) {
	// This test verifies that:
	// 1. Architect receives REQUEST(type=spec) from PM
	// 2. Architect calls handleSpecReview
	// 3. Architect uses SCOPING tools (spec_feedback or submit_stories)
	// 4. Architect sends RESPONSE with approval or feedback
	// 5. Response is properly formatted with ApprovalResult

	tests := []struct {
		name             string
		specMarkdown     string
		architectAction  string // "approve" or "feedback"
		expectedStatus   proto.ApprovalStatus
		expectedFeedback string
	}{
		{
			name: "Architect approves spec",
			specMarkdown: `---
version: "1.0"
priority: must
---

# Feature: Valid Spec

## Vision
Clear vision statement.

## Scope
### In Scope
- Feature A

### Out of Scope
- Feature B

## Requirements

### R-001: Requirement One
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Clear requirement.

**Acceptance Criteria:**
- [ ] Criterion 1
- [ ] Criterion 2
`,
			architectAction:  "approve",
			expectedStatus:   proto.ApprovalStatusApproved,
			expectedFeedback: "Spec approved - stories generated successfully",
		},
		{
			name: "Architect requests changes",
			specMarkdown: `---
version: "1.0"
---

# Feature: Incomplete Spec

## Vision
Vague vision.

## Requirements

### R-001: Vague Requirement
**Type:** functional
**Priority:** must
**Dependencies:** []

**Description:** Too vague.

**Acceptance Criteria:**
- [ ] Vague criterion
`,
			architectAction:  "feedback",
			expectedStatus:   proto.ApprovalStatusNeedsChanges,
			expectedFeedback: "Requirements need more specificity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TODO: Implement test with mock architect
			// 1. Create mock architect driver
			// 2. Create REQUEST message with spec
			// 3. Call handleSpecReview
			// 4. Verify RESPONSE message created
			// 5. Verify ApprovalResult has correct status and feedback
			t.Skip("TODO: Implement with mock architect")
		})
	}
}

// TestPMArchitectFeedbackLoop tests the iterative feedback loop.
func TestPMArchitectFeedbackLoop(t *testing.T) {
	// This test verifies the complete feedback loop:
	// 1. PM submits spec (SUBMITTING -> WAITING)
	// 2. Architect requests changes (sends NEEDS_CHANGES)
	// 3. PM receives feedback (WAITING -> INTERVIEWING)
	// 4. PM refines spec (INTERVIEWING -> DRAFTING -> SUBMITTING)
	// 5. Architect approves (sends APPROVED)
	// 6. PM receives approval (WAITING stays in WAITING)

	t.Skip("Integration test - requires full message flow simulation")
}

// Helper functions for creating mock agents (will be implemented in integration tests).

func createMockPMAgent(t *testing.T, dispatcher *dispatch.Dispatcher) *Driver {
	t.Helper()

	// Create minimal PM agent for testing
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	contextManager := contextmgr.NewContextManagerWithModel("claude-sonnet-4")

	interviewRequestCh := make(chan *proto.AgentMsg, 10)

	driver := &Driver{
		pmID:               "pm-test-001",
		llmClient:          nil, // Mock LLM client
		renderer:           renderer,
		contextManager:     contextManager,
		dispatcher:         dispatcher,
		persistenceChannel: make(chan *persistence.Request),
		currentState:       StateWaiting,
		stateData:          make(map[string]any),
		interviewRequestCh: interviewRequestCh,
		workDir:            "/tmp/test-pm",
	}

	return driver
}

//nolint:unparam // Placeholder function for future integration tests
func createMockArchitectAgent(t *testing.T, _ *dispatch.Dispatcher) *architect.Driver {
	t.Helper()

	// Create minimal architect agent for testing
	// This is a placeholder - actual implementation would need full architect setup
	return nil
}
