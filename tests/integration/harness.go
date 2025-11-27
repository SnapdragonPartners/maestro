//go:build integration

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
//
//nolint:govet // fieldalignment: Test struct, optimization not critical
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
	storyCh := make(chan *proto.AgentMsg, 100) // Not used in tests but needed for SetChannels

	// Set up the coder driver channels properly
	driver.SetChannels(storyCh, toArch, fromArch)

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
	stepCtx, cancel := context.WithTimeout(ctx, h.timeouts.CoderStep)
	defer cancel()

	// Let the coder advance its state machine.
	// The coder will handle incoming messages from its channels internally.
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

	// Questions are now handled inline via Effects pattern - no forwarding needed.

	return nil
}

// createApprovalRequestMessage creates a REQUEST message for architect approval.
func (h *TestHarness) createApprovalRequestMessage(coderID, content, reason string) *proto.AgentMsg {
	// Determine approval type based on coder state or reason.
	approvalType := proto.ApprovalTypePlan // Default
	if h.coders[coderID] != nil {
		currentState := h.coders[coderID].Driver.GetCurrentState()
		if currentState == coder.StateCodeReview {
			approvalType = proto.ApprovalTypeCode
		}
	}

	// Create structured approval request payload
	payload := &proto.ApprovalRequestPayload{
		ApprovalType: approvalType,
		Content:      content,
		Reason:       reason,
		Confidence:   proto.ConfidenceMedium,
	}

	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, coderID, h.architect.GetID())
	msg.Payload = proto.NewApprovalRequestPayload(payload)
	return msg
}
