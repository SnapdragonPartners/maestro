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
		return m.handleApprovalRequest(msg)
	case proto.MsgTypeQUESTION:
		return m.handleQuestion(msg)
	case proto.MsgTypeRESULT:
		// Just acknowledge result messages.
		return m.createAcknowledgment(msg), nil
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// handleApprovalRequest processes approval requests.
func (m *AlwaysApprovalMockArchitect) handleApprovalRequest(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract approval type from the request.
	approvalType := "plan" // default
	if reqApprovalType, exists := msg.GetPayload(proto.KeyApprovalType); exists {
		if approvalTypeStr, ok := reqApprovalType.(string); ok {
			approvalType = approvalTypeStr
		}
	}

	// Create approval response.
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         response.ID + "_approval",
		RequestID:  msg.ID,
		Type:       proto.ApprovalType(approvalType),
		Status:     proto.ApprovalStatus(m.response.Status),
		Feedback:   m.response.Feedback,
		ReviewedBy: m.id,
		ReviewedAt: time.Now(),
	}

	response.SetPayload("approval_result", approvalResult)

	return response, nil
}

// handleQuestion processes question messages.
func (m *AlwaysApprovalMockArchitect) handleQuestion(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// For simplicity, always provide a helpful answer.
	response := proto.NewAgentMsg(proto.MsgTypeANSWER, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyAnswer, "Please continue with your current approach. It looks good.")

	return response, nil
}

// createAcknowledgment creates a simple acknowledgment response.
func (m *AlwaysApprovalMockArchitect) createAcknowledgment(msg *proto.AgentMsg) *proto.AgentMsg {
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "acknowledged")
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
		return m.handleApprovalRequest(msg)
	case proto.MsgTypeQUESTION:
		return m.handleQuestion(msg)
	case proto.MsgTypeRESULT:
		return m.createAcknowledgment(msg), nil
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// handleApprovalRequest processes approval requests with rejection logic.
func (m *ChangesRequestedMockArchitect) handleApprovalRequest(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract approval type.
	approvalType := "plan"
	if reqApprovalType, exists := msg.GetPayload(proto.KeyApprovalType); exists {
		if approvalTypeStr, ok := reqApprovalType.(string); ok {
			approvalType = approvalTypeStr
		}
	}

	// Determine status based on rejection count.
	status := "APPROVED"
	feedback := "Plan looks good!"

	if m.currentRejections < m.rejectCount {
		status = "NEEDS_CHANGES"
		feedback = m.feedback
		m.currentRejections++
	}

	// Create response.
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID

	// Create approval result
	approvalResult := &proto.ApprovalResult{
		ID:         response.ID + "_approval",
		RequestID:  msg.ID,
		Type:       proto.ApprovalType(approvalType),
		Status:     proto.ApprovalStatus(status),
		Feedback:   feedback,
		ReviewedBy: m.id,
		ReviewedAt: time.Now(),
	}

	response.SetPayload("approval_result", approvalResult)

	return response, nil
}

// handleQuestion processes questions.
func (m *ChangesRequestedMockArchitect) handleQuestion(msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	response := proto.NewAgentMsg(proto.MsgTypeANSWER, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyAnswer, "Please revise based on the feedback provided.")

	return response, nil
}

// createAcknowledgment creates a simple acknowledgment.
func (m *ChangesRequestedMockArchitect) createAcknowledgment(msg *proto.AgentMsg) *proto.AgentMsg {
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload(proto.KeyStatus, "acknowledged")
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

	// Default malformed response.
	response := proto.NewAgentMsg(proto.MsgTypeRESULT, m.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("invalid_field", "malformed_value")
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
