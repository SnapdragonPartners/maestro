// Package contextmgr provides context management for LLM conversations including token counting and compaction.
package contextmgr

import (
	"encoding/json"
	"fmt"
	"time"
)

// SerializedMessage represents a Message in a format suitable for JSON serialization.
// All fields are explicitly typed for reliable round-trip serialization.
type SerializedMessage struct {
	Role        string             `json:"role"`
	Content     string             `json:"content"`
	Provenance  string             `json:"provenance,omitempty"`
	ToolCalls   []SerializedCall   `json:"tool_calls,omitempty"`
	ToolResults []SerializedResult `json:"tool_results,omitempty"`
}

// SerializedCall represents a ToolCall in serialized form.
// Fields are ordered to match ToolCall for simpler conversion.
//
//nolint:govet // struct alignment optimization not critical for serialization types.
type SerializedCall struct {
	ID         string         `json:"id"`
	Name       string         `json:"name"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

// SerializedResult represents a ToolResult in serialized form.
type SerializedResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// SerializedFragment represents a Fragment in serialized form.
//
//nolint:govet // struct alignment optimization not critical for serialization types.
type SerializedFragment struct {
	Timestamp  int64  `json:"timestamp"` // Unix timestamp
	Provenance string `json:"provenance"`
	Content    string `json:"content"`
}

// SerializedContext represents the full context manager state for serialization.
type SerializedContext struct {
	Messages           []SerializedMessage  `json:"messages"`
	UserBuffer         []SerializedFragment `json:"user_buffer,omitempty"`
	ModelName          string               `json:"model_name,omitempty"`
	CurrentTemplate    string               `json:"current_template,omitempty"`
	AgentID            string               `json:"agent_id,omitempty"`
	PendingToolCalls   []SerializedCall     `json:"pending_tool_calls,omitempty"`
	PendingToolResults []SerializedResult   `json:"pending_tool_results,omitempty"`
}

// Serialize converts the ContextManager state to JSON bytes.
// This includes all messages, user buffer, and pending tool state.
func (cm *ContextManager) Serialize() ([]byte, error) {
	sc := SerializedContext{
		ModelName:       cm.modelName,
		CurrentTemplate: cm.currentTemplate,
		AgentID:         cm.agentID,
	}

	// Serialize messages
	sc.Messages = make([]SerializedMessage, len(cm.messages))
	for i := range cm.messages {
		sc.Messages[i] = messageToSerialized(&cm.messages[i])
	}

	// Serialize user buffer
	if len(cm.userBuffer) > 0 {
		sc.UserBuffer = make([]SerializedFragment, len(cm.userBuffer))
		for i := range cm.userBuffer {
			frag := &cm.userBuffer[i]
			sc.UserBuffer[i] = SerializedFragment{
				Timestamp:  frag.Timestamp.Unix(),
				Provenance: frag.Provenance,
				Content:    frag.Content,
			}
		}
	}

	// Serialize pending tool calls
	if len(cm.pendingToolCalls) > 0 {
		sc.PendingToolCalls = make([]SerializedCall, len(cm.pendingToolCalls))
		for i := range cm.pendingToolCalls {
			tc := &cm.pendingToolCalls[i]
			sc.PendingToolCalls[i] = toolCallToSerialized(tc)
		}
	}

	// Serialize pending tool results
	if len(cm.pendingToolResults) > 0 {
		sc.PendingToolResults = make([]SerializedResult, len(cm.pendingToolResults))
		for i := range cm.pendingToolResults {
			tr := &cm.pendingToolResults[i]
			sc.PendingToolResults[i] = toolResultToSerialized(tr)
		}
	}

	data, err := json.Marshal(sc)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal context: %w", err)
	}
	return data, nil
}

// Deserialize restores the ContextManager state from JSON bytes.
// This replaces all existing state in the context manager.
func (cm *ContextManager) Deserialize(data []byte) error {
	var sc SerializedContext
	if err := json.Unmarshal(data, &sc); err != nil {
		return fmt.Errorf("failed to unmarshal context: %w", err)
	}

	// Restore scalar fields
	cm.modelName = sc.ModelName
	cm.currentTemplate = sc.CurrentTemplate
	cm.agentID = sc.AgentID

	// Restore messages
	cm.messages = make([]Message, len(sc.Messages))
	for i := range sc.Messages {
		cm.messages[i] = serializedToMessage(&sc.Messages[i])
	}

	// Restore user buffer
	if len(sc.UserBuffer) > 0 {
		cm.userBuffer = make([]Fragment, len(sc.UserBuffer))
		for i := range sc.UserBuffer {
			sf := &sc.UserBuffer[i]
			cm.userBuffer[i] = Fragment{
				Timestamp:  time.Unix(sf.Timestamp, 0),
				Provenance: sf.Provenance,
				Content:    sf.Content,
			}
		}
	} else {
		cm.userBuffer = make([]Fragment, 0)
	}

	// Restore pending tool calls
	if len(sc.PendingToolCalls) > 0 {
		cm.pendingToolCalls = make([]ToolCall, len(sc.PendingToolCalls))
		for i := range sc.PendingToolCalls {
			stc := &sc.PendingToolCalls[i]
			cm.pendingToolCalls[i] = serializedToToolCall(stc)
		}
	} else {
		cm.pendingToolCalls = nil
	}

	// Restore pending tool results
	if len(sc.PendingToolResults) > 0 {
		cm.pendingToolResults = make([]ToolResult, len(sc.PendingToolResults))
		for i := range sc.PendingToolResults {
			str := &sc.PendingToolResults[i]
			cm.pendingToolResults[i] = serializedToToolResult(str)
		}
	} else {
		cm.pendingToolResults = nil
	}

	// Note: chatService is NOT restored from serialization
	// It must be re-attached after deserialization via SetChatService()

	return nil
}

// messageToSerialized converts a Message to SerializedMessage.
//
//nolint:dupl // Serialize/deserialize pairs necessarily have similar structure.
func messageToSerialized(msg *Message) SerializedMessage {
	sm := SerializedMessage{
		Role:       msg.Role,
		Content:    msg.Content,
		Provenance: msg.Provenance,
	}

	if len(msg.ToolCalls) > 0 {
		sm.ToolCalls = make([]SerializedCall, len(msg.ToolCalls))
		for i := range msg.ToolCalls {
			sm.ToolCalls[i] = toolCallToSerialized(&msg.ToolCalls[i])
		}
	}

	if len(msg.ToolResults) > 0 {
		sm.ToolResults = make([]SerializedResult, len(msg.ToolResults))
		for i := range msg.ToolResults {
			sm.ToolResults[i] = toolResultToSerialized(&msg.ToolResults[i])
		}
	}

	return sm
}

// serializedToMessage converts a SerializedMessage to Message.
//
//nolint:dupl // Serialize/deserialize pairs necessarily have similar structure.
func serializedToMessage(sm *SerializedMessage) Message {
	msg := Message{
		Role:       sm.Role,
		Content:    sm.Content,
		Provenance: sm.Provenance,
	}

	if len(sm.ToolCalls) > 0 {
		msg.ToolCalls = make([]ToolCall, len(sm.ToolCalls))
		for i := range sm.ToolCalls {
			msg.ToolCalls[i] = serializedToToolCall(&sm.ToolCalls[i])
		}
	}

	if len(sm.ToolResults) > 0 {
		msg.ToolResults = make([]ToolResult, len(sm.ToolResults))
		for i := range sm.ToolResults {
			msg.ToolResults[i] = serializedToToolResult(&sm.ToolResults[i])
		}
	}

	return msg
}

// toolCallToSerialized converts a ToolCall to SerializedCall.
func toolCallToSerialized(tc *ToolCall) SerializedCall {
	return SerializedCall{
		ID:         tc.ID,
		Name:       tc.Name,
		Parameters: tc.Parameters,
	}
}

// serializedToToolCall converts a SerializedCall to ToolCall.
func serializedToToolCall(sc *SerializedCall) ToolCall {
	return ToolCall{
		ID:         sc.ID,
		Name:       sc.Name,
		Parameters: sc.Parameters,
	}
}

// toolResultToSerialized converts a ToolResult to SerializedResult.
func toolResultToSerialized(tr *ToolResult) SerializedResult {
	return SerializedResult{
		ToolCallID: tr.ToolCallID,
		Content:    tr.Content,
		IsError:    tr.IsError,
	}
}

// serializedToToolResult converts a SerializedResult to ToolResult.
func serializedToToolResult(sr *SerializedResult) ToolResult {
	return ToolResult{
		ToolCallID: sr.ToolCallID,
		Content:    sr.Content,
		IsError:    sr.IsError,
	}
}
