package proto

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type MsgType string

const (
	MsgTypeTASK     MsgType = "TASK"
	MsgTypeRESULT   MsgType = "RESULT"
	MsgTypeERROR    MsgType = "ERROR"
	MsgTypeQUESTION MsgType = "QUESTION"
	MsgTypeSHUTDOWN MsgType = "SHUTDOWN"
)

type AgentMsg struct {
	ID          string            `json:"id"`
	Type        MsgType           `json:"type"`
	FromAgent   string            `json:"from_agent"`
	ToAgent     string            `json:"to_agent"`
	Timestamp   time.Time         `json:"timestamp"`
	Payload     map[string]any    `json:"payload"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	RetryCount  int               `json:"retry_count,omitempty"`
	ParentMsgID string            `json:"parent_msg_id,omitempty"`
}

func NewAgentMsg(msgType MsgType, fromAgent, toAgent string) *AgentMsg {
	return &AgentMsg{
		ID:        generateID(),
		Type:      msgType,
		FromAgent: fromAgent,
		ToAgent:   toAgent,
		Timestamp: time.Now().UTC(),
		Payload:   make(map[string]any),
		Metadata:  make(map[string]string),
	}
}

func (msg *AgentMsg) ToJSON() ([]byte, error) {
	return json.Marshal(msg)
}

func (msg *AgentMsg) FromJSON(data []byte) error {
	return json.Unmarshal(data, msg)
}

func FromJSON(data []byte) (*AgentMsg, error) {
	var msg AgentMsg
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal AgentMsg: %w", err)
	}
	return &msg, nil
}

func (msg *AgentMsg) SetPayload(key string, value any) {
	if msg.Payload == nil {
		msg.Payload = make(map[string]any)
	}
	msg.Payload[key] = value
}

func (msg *AgentMsg) GetPayload(key string) (any, bool) {
	if msg.Payload == nil {
		return nil, false
	}
	val, exists := msg.Payload[key]
	return val, exists
}

func (msg *AgentMsg) SetMetadata(key, value string) {
	if msg.Metadata == nil {
		msg.Metadata = make(map[string]string)
	}
	msg.Metadata[key] = value
}

func (msg *AgentMsg) GetMetadata(key string) (string, bool) {
	if msg.Metadata == nil {
		return "", false
	}
	val, exists := msg.Metadata[key]
	return val, exists
}

func (msg *AgentMsg) Clone() *AgentMsg {
	clone := &AgentMsg{
		ID:          msg.ID,
		Type:        msg.Type,
		FromAgent:   msg.FromAgent,
		ToAgent:     msg.ToAgent,
		Timestamp:   msg.Timestamp,
		RetryCount:  msg.RetryCount,
		ParentMsgID: msg.ParentMsgID,
	}

	// Deep copy payload
	if msg.Payload != nil {
		clone.Payload = make(map[string]any)
		for k, v := range msg.Payload {
			clone.Payload[k] = v
		}
	}

	// Deep copy metadata
	if msg.Metadata != nil {
		clone.Metadata = make(map[string]string)
		for k, v := range msg.Metadata {
			clone.Metadata[k] = v
		}
	}

	return clone
}

func (msg *AgentMsg) Validate() error {
	if msg.ID == "" {
		return fmt.Errorf("message ID is required")
	}
	if msg.Type == "" {
		return fmt.Errorf("message type is required")
	}
	if msg.FromAgent == "" {
		return fmt.Errorf("from_agent is required")
	}
	if msg.ToAgent == "" {
		return fmt.Errorf("to_agent is required")
	}
	if msg.Timestamp.IsZero() {
		return fmt.Errorf("timestamp is required")
	}

	// Validate message type
	switch msg.Type {
	case MsgTypeTASK, MsgTypeRESULT, MsgTypeERROR, MsgTypeQUESTION, MsgTypeSHUTDOWN:
		// Valid types
	default:
		return fmt.Errorf("invalid message type: %s", msg.Type)
	}

	return nil
}

var (
	idCounter int64
	idMutex   sync.Mutex
)

// generateID creates a simple unique ID for messages
// In a real implementation, this might use UUIDs or other schemes
func generateID() string {
	idMutex.Lock()
	defer idMutex.Unlock()

	idCounter++
	return fmt.Sprintf("msg_%d_%d", time.Now().UnixNano(), idCounter)
}
