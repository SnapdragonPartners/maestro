// Package integration provides integration tests for the orchestrator multi-agent system.
package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
)

const (
	approvalTypePlan = "plan"
)

// TestHarness manages multi-agent integration test scenarios.
type TestHarness struct {
	t *testing.T

	// Agent management.
	architect ArchitectAgent
	coders    map[string]*CoderAgent

	// Communication channels - per-coder to prevent head-of-line blocking.
	archToCoderChannels map[string]chan *proto.AgentMsg // architect -> specific coder
	coderToArchChannels map[string]chan *proto.AgentMsg // specific coder -> architect

	// Test configuration.
	timeouts TestTimeouts

	// Synchronization.
	mu sync.RWMutex
}

// TestTimeouts configures various test timeout durations.
type TestTimeouts struct {
	Plan      time.Duration // How long to wait for plan approval
	Global    time.Duration // Overall test timeout
	Pump      time.Duration // Message pump interval
	CoderStep time.Duration // Individual coder step timeout
}

// DefaultTimeouts returns sensible defaults for testing.
func DefaultTimeouts() TestTimeouts {
	return TestTimeouts{
		Plan:      100 * time.Millisecond,
		Global:    2 * time.Second,
		Pump:      10 * time.Millisecond,
		CoderStep: 50 * time.Millisecond,
	}
}

// CoderAgent wraps a coder driver with channel communication.
type CoderAgent struct {
	ID     string
	Driver *coder.Coder

	// Message channels for this specific coder.
	FromArch chan *proto.AgentMsg
	ToArch   chan *proto.AgentMsg
}

// ArchitectAgent interface for different mock architect implementations.
type ArchitectAgent interface {
	// ProcessMessage handles incoming messages and returns responses.
	ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error)

	// GetID returns the architect's identifier.
	GetID() string
}

// StopCondition defines when the test harness should stop pumping messages.
type StopCondition func(harness *TestHarness) bool

// NewTestHarness creates a new test harness with default configuration.
func NewTestHarness(t *testing.T) *TestHarness {
	return &TestHarness{
		t:                   t,
		coders:              make(map[string]*CoderAgent),
		archToCoderChannels: make(map[string]chan *proto.AgentMsg),
		coderToArchChannels: make(map[string]chan *proto.AgentMsg),
		timeouts:            DefaultTimeouts(),
	}
}

// SetTimeouts configures test timeouts.
func (h *TestHarness) SetTimeouts(timeouts TestTimeouts) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.timeouts = timeouts
}

// SetArchitect sets the architect agent for this test.
func (h *TestHarness) SetArchitect(architect ArchitectAgent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.architect = architect
}

// AddCoder adds a coder agent to the test harness.
func (h *TestHarness) AddCoder(coderID string, driver *coder.Coder) {
	h.mu.Lock()
	defer h.mu.Unlock()

	// Create dedicated channels for this coder.
	fromArch := make(chan *proto.AgentMsg, 100)
	toArch := make(chan *proto.AgentMsg, 100)

	coderAgent := &CoderAgent{
		ID:       coderID,
		Driver:   driver,
		FromArch: fromArch,
		ToArch:   toArch,
	}

	h.coders[coderID] = coderAgent
	h.archToCoderChannels[coderID] = fromArch
	h.coderToArchChannels[coderID] = toArch
}

// GetCoderState returns the current state of a specific coder.
func (h *TestHarness) GetCoderState(coderID string) proto.State {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if coderAgent, exists := h.coders[coderID]; exists {
		return coderAgent.Driver.GetCurrentState()
	}
	return proto.StateError
}

// GetAllCoderStates returns the current states of all coders.
func (h *TestHarness) GetAllCoderStates() map[string]proto.State {
	h.mu.RLock()
	defer h.mu.RUnlock()

	states := make(map[string]proto.State)
	for id, coderAgent := range h.coders {
		states[id] = coderAgent.Driver.GetCurrentState()
	}
	return states
}

// DefaultStopCondition stops when all coders reach DONE state.
func DefaultStopCondition(harness *TestHarness) bool {
	states := harness.GetAllCoderStates()
	for _, state := range states {
		if state != proto.StateDone {
			return false
		}
	}
	return len(states) > 0 // Ensure we have at least one coder
}

// Run pumps messages between agents until the stop condition is met.
func (h *TestHarness) Run(ctx context.Context, stopWhen StopCondition) error {
	if stopWhen == nil {
		stopWhen = DefaultStopCondition
	}

	// Create context with global timeout.
	h.mu.RLock()
	globalTimeout := h.timeouts.Global
	pumpInterval := h.timeouts.Pump
	h.mu.RUnlock()

	runCtx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	// Start message pumping.
	ticker := time.NewTicker(pumpInterval)
	defer ticker.Stop()

	for {
		select {
		case <-runCtx.Done():
			return fmt.Errorf("test harness timed out after %v", globalTimeout)
		case <-ticker.C:
			// Check stop condition.
			if stopWhen(h) {
				return nil
			}

			// Pump messages.
			if err := h.pumpMessages(runCtx); err != nil {
				return fmt.Errorf("message pump error: %w", err)
			}
		}
	}
}

// Wait blocks until a specific coder reaches the target state or timeout.
func (h *TestHarness) Wait(ctx context.Context, coderID string, wantState proto.State) error {
	h.mu.RLock()
	globalTimeout := h.timeouts.Global
	h.mu.RUnlock()

	waitCtx, cancel := context.WithTimeout(ctx, globalTimeout)
	defer cancel()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-waitCtx.Done():
			currentState := h.GetCoderState(coderID)
			return fmt.Errorf("timeout waiting for coder %s to reach %s (current: %s)",
				coderID, wantState, currentState)
		case <-ticker.C:
			if h.GetCoderState(coderID) == wantState {
				return nil
			}
		}
	}
}

// pumpMessages processes one round of message passing between agents.
func (h *TestHarness) pumpMessages(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// Process messages from coders to architect.
	for coderID, toArchCh := range h.coderToArchChannels {
		select {
		case msg := <-toArchCh:
			// Send message to architect.
			response, err := h.architect.ProcessMessage(ctx, msg)
			if err != nil {
				h.t.Logf("Architect error processing message from %s: %v", coderID, err)
				continue
			}

			// Send response back to originating coder if we got one.
			if response != nil && response.ToAgent == coderID {
				fromArchCh := h.archToCoderChannels[coderID]
				select {
				case fromArchCh <- response:
					// Message sent successfully.
				default:
					h.t.Logf("Warning: Could not send architect response to coder %s (channel full)", coderID)
				}
			}
		default:
			// No message waiting.
		}
	}

	// Process coder steps (let each coder process incoming messages and advance)
	for coderID, coderAgent := range h.coders {
		if err := h.stepCoder(ctx, coderID, coderAgent); err != nil {
			h.t.Logf("Coder %s step error: %v", coderID, err)
		}
	}

	return nil
}

// stepCoder advances a single coder by one step.
func (h *TestHarness) stepCoder(ctx context.Context, coderID string, coderAgent *CoderAgent) error {
	// Check for incoming messages from architect.
	select {
	case msg := <-coderAgent.FromArch:
		// Process message using the coder's ProcessMessage method.
		if response, err := h.processCoderMessage(ctx, coderAgent, msg); err != nil {
			return fmt.Errorf("coder %s failed to process message: %w", coderID, err)
		} else if response != nil {
			// Send response to architect.
			select {
			case coderAgent.ToArch <- response:
				// Message sent successfully.
			default:
				return fmt.Errorf("coder %s response channel full", coderID)
			}
		}
	default:
		// No incoming message, just step the coder's state machine.
		stepCtx, cancel := context.WithTimeout(ctx, h.timeouts.CoderStep)
		defer cancel()

		// Let the coder advance its state machine.
		if _, err := coderAgent.Driver.Step(stepCtx); err != nil {
			// Note: Some errors (like invalid transitions) are expected during testing
			h.t.Logf("Coder %s step resulted in: %v", coderID, err)
		}

		// Check for pending approval requests and send them to architect.
		if hasPending, _, content, reason, _ := coderAgent.Driver.GetPendingApprovalRequest(); hasPending {
			requestMsg := h.createApprovalRequestMessage(coderID, content, reason)
			select {
			case coderAgent.ToArch <- requestMsg:
				// Clear the pending request since we've sent it.
				coderAgent.Driver.ClearPendingApprovalRequest()
				h.t.Logf("Sent approval request from coder %s to architect", coderID)
			default:
				h.t.Logf("Warning: Could not send approval request from coder %s (channel full)", coderID)
			}
		}

		// Check for pending questions and send them to architect.
		if hasPending, _, content, reason := coderAgent.Driver.GetPendingQuestion(); hasPending {
			questionMsg := h.createQuestionMessage(coderID, content, reason)
			select {
			case coderAgent.ToArch <- questionMsg:
				// Clear the pending question since we've sent it.
				coderAgent.Driver.ClearPendingQuestion()
				h.t.Logf("Sent question from coder %s to architect", coderID)
			default:
				h.t.Logf("Warning: Could not send question from coder %s (channel full)", coderID)
			}
		}
	}

	return nil
}

// processCoderMessage simulates the coder processing an incoming message.
//nolint:unparam // first return always nil by design in test harness
func (h *TestHarness) processCoderMessage(ctx context.Context, coderAgent *CoderAgent, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// This is where we'd normally call the coder's ProcessMessage method.
	// For now, we'll implement a simplified version based on message type.

	switch msg.Type {
	case proto.MsgTypeRESULT:
		// This is an approval result from the architect.
		if err := h.processApprovalResult(ctx, coderAgent, msg); err != nil {
			return nil, err
		}

		// Step the coder to let it process the approval.
		if _, err := coderAgent.Driver.Step(ctx); err != nil {
			h.t.Logf("Coder step after approval: %v", err)
		}

		// No response needed for this message type.
		return nil, fmt.Errorf("test harness: no response for this message type")

	case proto.MsgTypeANSWER:
		// This is an answer to a question from the architect.
		if answer, exists := msg.GetPayload(proto.KeyAnswer); exists {
			if answerStr, ok := answer.(string); ok {
				if err := coderAgent.Driver.ProcessAnswer(answerStr); err != nil {
					return nil, fmt.Errorf("failed to process answer: %w", err)
				}
			}
		}
		// No response needed for this message type.
		return nil, fmt.Errorf("test harness: no response for this message type")

	default:
		h.t.Logf("Unexpected message type for coder: %s", msg.Type)
		// No response needed for this message type.
		return nil, fmt.Errorf("test harness: no response for this message type")
	}
}

// processApprovalResult handles approval results from the architect.
func (h *TestHarness) processApprovalResult(_ context.Context, coderAgent *CoderAgent, msg *proto.AgentMsg) error {
	// Extract status.
	status, exists := msg.GetPayload(proto.KeyStatus)
	if !exists {
		return fmt.Errorf("missing status in approval result")
	}

	statusStr, ok := status.(string)
	if !ok {
		return fmt.Errorf("status must be a string")
	}

	// Extract approval type.
	approvalTypeRaw, exists := msg.GetPayload(proto.KeyApprovalType)
	if !exists {
		return fmt.Errorf("missing approval_type in approval result")
	}

	approvalTypeStr, ok := approvalTypeRaw.(string)
	if !ok {
		return fmt.Errorf("approval_type must be a string")
	}

	// Process the approval result.
	return coderAgent.Driver.ProcessApprovalResult(context.Background(), statusStr, approvalTypeStr)
}

// createApprovalRequestMessage creates a REQUEST message for architect approval.
func (h *TestHarness) createApprovalRequestMessage(coderID, content, reason string) *proto.AgentMsg {
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, coderID, h.architect.GetID())
	msg.SetPayload(proto.KeyRequestType, proto.RequestApproval.String())

	// Determine approval type based on coder state or reason.
	approvalType := approvalTypePlan // Default
	if h.coders[coderID] != nil {
		currentState := h.coders[coderID].Driver.GetCurrentState()
		if currentState == coder.StateCodeReview {
			approvalType = "code"
		}
	}

	msg.SetPayload(proto.KeyApprovalType, approvalType)
	msg.SetPayload(proto.KeyContent, content)
	msg.SetPayload(proto.KeyReason, reason)
	return msg
}

// createQuestionMessage creates a QUESTION message for the architect.
func (h *TestHarness) createQuestionMessage(coderID, content, reason string) *proto.AgentMsg {
	msg := proto.NewAgentMsg(proto.MsgTypeQUESTION, coderID, h.architect.GetID())
	msg.SetPayload(proto.KeyQuestion, content)
	msg.SetPayload(proto.KeyReason, reason)
	return msg
}
