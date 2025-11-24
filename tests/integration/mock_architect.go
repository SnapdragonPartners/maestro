//go:build integration

package integration

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/proto"
)

// ApprovalResponse defines how the mock architect should respond to approval requests.
type ApprovalResponse struct {
	Status       string        // "approved" or "changes_requested"
	ApprovalType string        // "plan" or "code"
	Feedback     string        // Optional feedback message
	Delay        time.Duration // Optional delay before responding
}

// AlwaysApprovalMockArchitect always approves requests.
//
//nolint:govet // fieldalignment: Test struct, optimization not critical
type AlwaysApprovalMockArchitect struct {
	id       string
	response ApprovalResponse
	mu       sync.RWMutex
	messages []ReceivedMessage // Track received messages for assertions
}

// ReceivedMessage tracks messages received by the mock architect.
type ReceivedMessage struct {
	Timestamp time.Time
	Message   *proto.AgentMsg
	FromCoder string
}

// NewAlwaysApprovalMockArchitect creates a mock architect that always approves.
func NewAlwaysApprovalMockArchitect(id string) *AlwaysApprovalMockArchitect {
	return &AlwaysApprovalMockArchitect{
		id: id,
		response: ApprovalResponse{
			Status:       "APPROVED",
			ApprovalType: "plan", // Will be overridden based on request
			Feedback:     "Plan looks good!",
		},
		messages: make([]ReceivedMessage, 0),
	}
}

// GetID returns the architect's ID.
func (m *AlwaysApprovalMockArchitect) GetID() string {
	return m.id
}

// ProcessMessage handles incoming messages and returns appropriate responses.
func (m *AlwaysApprovalMockArchitect) ProcessMessage(_ context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the message.
	m.messages = append(m.messages, ReceivedMessage{
		Timestamp: time.Now(),
		Message:   msg,
		FromCoder: msg.FromAgent,
	})

	// Add delay if specified.
	if m.response.Delay > 0 {
		time.Sleep(m.response.Delay)
	}

	switch msg.Type {
	case proto.MsgTypeREQUEST:
		// Handle unified REQUEST protocol based on payload kind
		if msg.Payload == nil {
			return nil, fmt.Errorf("REQUEST message missing payload")
		}

		switch msg.Payload.Kind {
		case proto.PayloadKindQuestionRequest:
			return m.handleQuestion(msg)
		case proto.PayloadKindApprovalRequest:
			return m.handleApprovalRequest(msg)
		default:
			return nil, fmt.Errorf("unsupported payload kind: %s", msg.Payload.Kind)
		}
	case proto.MsgTypeRESPONSE:
		// Just acknowledge response messages.
		return m.createAcknowledgment(msg), nil
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// handleApprovalRequest processes approval requests.
func (m *AlwaysApprovalMockArchitect) handleApprovalRequest(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract approval request from payload
	approvalReq, err := msg.Payload.ExtractApprovalRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract approval request: %w", err)
	}

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         fmt.Sprintf("approval-%d", time.Now().UnixNano()),
		RequestID:  msg.ID,
		Type:       approvalReq.ApprovalType,
		Status:     proto.ApprovalStatus(m.response.Status),
		Feedback:   m.response.Feedback,
		ReviewedBy: m.id,
		ReviewedAt: time.Now(),
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.Payload = proto.NewApprovalResponsePayload(approvalResult)

	return response, nil
}

// handleQuestion processes question messages.
func (m *AlwaysApprovalMockArchitect) handleQuestion(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract question request from payload
	_, err := msg.Payload.ExtractQuestionRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract question request: %w", err)
	}

	// Create question response payload
	questionResp := &proto.QuestionResponsePayload{
		AnswerText: "Please continue with your current approach. It looks good.",
		Confidence: proto.ConfidenceHigh,
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.Payload = proto.NewQuestionResponsePayload(questionResp)

	return response, nil
}

// createAcknowledgment creates a simple acknowledgment response.
func (m *AlwaysApprovalMockArchitect) createAcknowledgment(msg *proto.AgentMsg) *proto.AgentMsg {
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	// Simple acknowledgment doesn't need a payload
	return response
}

// GetReceivedMessages returns all messages received by this architect.
func (m *AlwaysApprovalMockArchitect) GetReceivedMessages() []ReceivedMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to prevent race conditions.
	messages := make([]ReceivedMessage, len(m.messages))
	copy(messages, m.messages)
	return messages
}

// CountMessagesByType returns the count of messages by type.
func (m *AlwaysApprovalMockArchitect) CountMessagesByType(msgType proto.MsgType) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, msg := range m.messages {
		if msg.Message.Type == msgType {
			count++
		}
	}
	return count
}

// SetResponseDelay sets a delay for all responses.
func (m *AlwaysApprovalMockArchitect) SetResponseDelay(delay time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.response.Delay = delay
}

// ChangesRequestedMockArchitect rejects the first N requests, then approves.
//
//nolint:govet // fieldalignment: Test struct, optimization not critical
type ChangesRequestedMockArchitect struct {
	id                string
	rejectCount       int
	currentRejections int
	feedback          string
	mu                sync.RWMutex
	messages          []ReceivedMessage
}

// NewChangesRequestedMockArchitect creates a mock that requests changes N times.
func NewChangesRequestedMockArchitect(id string, rejectCount int, feedback string) *ChangesRequestedMockArchitect {
	return &ChangesRequestedMockArchitect{
		id:          id,
		rejectCount: rejectCount,
		feedback:    feedback,
		messages:    make([]ReceivedMessage, 0),
	}
}

// GetID returns the architect's ID.
func (m *ChangesRequestedMockArchitect) GetID() string {
	return m.id
}

// ProcessMessage handles incoming messages with rejection logic.
func (m *ChangesRequestedMockArchitect) ProcessMessage(_ context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the message.
	m.messages = append(m.messages, ReceivedMessage{
		Timestamp: time.Now(),
		Message:   msg,
		FromCoder: msg.FromAgent,
	})

	switch msg.Type {
	case proto.MsgTypeREQUEST:
		// Handle unified REQUEST protocol based on payload kind
		if msg.Payload == nil {
			return nil, fmt.Errorf("REQUEST message missing payload")
		}

		switch msg.Payload.Kind {
		case proto.PayloadKindQuestionRequest:
			return m.handleQuestion(msg)
		case proto.PayloadKindApprovalRequest:
			return m.handleApprovalRequest(msg)
		default:
			return nil, fmt.Errorf("unsupported payload kind: %s", msg.Payload.Kind)
		}
	case proto.MsgTypeRESPONSE:
		return m.createAcknowledgment(msg), nil
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// handleApprovalRequest processes approval requests with rejection logic.
func (m *ChangesRequestedMockArchitect) handleApprovalRequest(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract approval request from payload
	approvalReq, err := msg.Payload.ExtractApprovalRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract approval request: %w", err)
	}

	// Determine status based on rejection count.
	status := proto.ApprovalStatusApproved
	feedback := "Plan looks good!"

	if m.currentRejections < m.rejectCount {
		status = proto.ApprovalStatusNeedsChanges
		feedback = m.feedback
		m.currentRejections++
	}

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         fmt.Sprintf("approval-%d", time.Now().UnixNano()),
		RequestID:  msg.ID,
		Type:       approvalReq.ApprovalType,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: m.id,
		ReviewedAt: time.Now(),
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.Payload = proto.NewApprovalResponsePayload(approvalResult)

	return response, nil
}

// handleQuestion processes questions.
func (m *ChangesRequestedMockArchitect) handleQuestion(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract question request from payload
	_, err := msg.Payload.ExtractQuestionRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract question request: %w", err)
	}

	// Create question response payload
	questionResp := &proto.QuestionResponsePayload{
		AnswerText: "Please revise based on the feedback provided.",
		Confidence: proto.ConfidenceMedium,
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.Payload = proto.NewQuestionResponsePayload(questionResp)

	return response, nil
}

// createAcknowledgment creates a simple acknowledgment.
func (m *ChangesRequestedMockArchitect) createAcknowledgment(msg *proto.AgentMsg) *proto.AgentMsg {
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	// Simple acknowledgment doesn't need a payload
	return response
}

// GetReceivedMessages returns all received messages.
func (m *ChangesRequestedMockArchitect) GetReceivedMessages() []ReceivedMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := make([]ReceivedMessage, len(m.messages))
	copy(messages, m.messages)
	return messages
}

// MalformedResponseMockArchitect sends malformed responses for testing error handling.
//
//nolint:govet // fieldalignment: Test struct, optimization not critical
type MalformedResponseMockArchitect struct {
	id       string
	response func(*proto.AgentMsg) *proto.AgentMsg
	mu       sync.RWMutex
	messages []ReceivedMessage
}

// NewMalformedResponseMockArchitect creates a mock that sends malformed responses.
func NewMalformedResponseMockArchitect(id string, responseFunc func(*proto.AgentMsg) *proto.AgentMsg) *MalformedResponseMockArchitect {
	return &MalformedResponseMockArchitect{
		id:       id,
		response: responseFunc,
		messages: make([]ReceivedMessage, 0),
	}
}

// GetID returns the architect's ID.
func (m *MalformedResponseMockArchitect) GetID() string {
	return m.id
}

// ProcessMessage handles messages with custom malformed responses.
func (m *MalformedResponseMockArchitect) ProcessMessage(_ context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Record the message.
	m.messages = append(m.messages, ReceivedMessage{
		Timestamp: time.Now(),
		Message:   msg,
		FromCoder: msg.FromAgent,
	})

	// Use custom response function.
	if m.response != nil {
		return m.response(msg), nil
	}

	// Default malformed response (no payload is malformed).
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	// Intentionally leave payload empty/nil for malformed response testing
	return response, nil
}

// GetReceivedMessages returns all received messages.
func (m *MalformedResponseMockArchitect) GetReceivedMessages() []ReceivedMessage {
	m.mu.RLock()
	defer m.mu.RUnlock()

	messages := make([]ReceivedMessage, len(m.messages))
	copy(messages, m.messages)
	return messages
}
