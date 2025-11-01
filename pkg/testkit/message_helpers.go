package testkit

import (
	"time"

	"orchestrator/pkg/proto"
)

// MessageBuilder helps create synthetic AgentMsg instances for testing.
type MessageBuilder struct {
	msg *proto.AgentMsg
}

// NewStoryMessage creates a new STORY message builder.
func NewStoryMessage(fromAgent, toAgent string) *MessageBuilder {
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, fromAgent, toAgent)
	return &MessageBuilder{msg: msg}
}

// NewResponseMessage creates a new RESPONSE message builder.
func NewResponseMessage(fromAgent, toAgent string) *MessageBuilder {
	msg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, fromAgent, toAgent)
	return &MessageBuilder{msg: msg}
}

// NewErrorMessage creates a new ERROR message builder.
func NewErrorMessage(fromAgent, toAgent string) *MessageBuilder {
	msg := proto.NewAgentMsg(proto.MsgTypeERROR, fromAgent, toAgent)
	return &MessageBuilder{msg: msg}
}

// NewRequestMessage creates a new REQUEST message builder.
func NewRequestMessage(fromAgent, toAgent string) *MessageBuilder {
	msg := proto.NewAgentMsg(proto.MsgTypeREQUEST, fromAgent, toAgent)
	return &MessageBuilder{msg: msg}
}

// NewShutdownMessage creates a new SHUTDOWN message builder.
func NewShutdownMessage(fromAgent, toAgent string) *MessageBuilder {
	msg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, fromAgent, toAgent)
	return &MessageBuilder{msg: msg}
}

// WithContent sets the content payload (common for STORY messages).
func (mb *MessageBuilder) WithContent(content string) *MessageBuilder {
	// For STORY messages, use generic payload
	payload := map[string]any{"content": content}
	mb.msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, payload))
	return mb
}

// WithStoryID sets the story_id in metadata (not payload).
func (mb *MessageBuilder) WithStoryID(storyID string) *MessageBuilder {
	mb.msg.SetMetadata("story_id", storyID)
	return mb
}

// WithRequirements adds requirements to the payload.
func (mb *MessageBuilder) WithRequirements(requirements []string) *MessageBuilder {
	mb.mergeToPayload("requirements", requirements)
	return mb
}

// WithStatus sets the status payload (common for RESULT messages).
func (mb *MessageBuilder) WithStatus(status string) *MessageBuilder {
	mb.mergeToPayload("status", status)
	return mb
}

// WithImplementation sets the implementation payload (for coding results).
func (mb *MessageBuilder) WithImplementation(implementation string) *MessageBuilder {
	mb.mergeToPayload("implementation", implementation)
	return mb
}

// WithTestResults sets the test_results payload.
func (mb *MessageBuilder) WithTestResults(success bool, output string) *MessageBuilder {
	testResults := map[string]any{
		"success": success,
		"output":  output,
		"elapsed": "100ms",
	}
	mb.mergeToPayload("test_results", testResults)
	return mb
}

// WithError sets the error payload (for ERROR messages).
func (mb *MessageBuilder) WithError(errorMsg string) *MessageBuilder {
	payload := map[string]any{"error": errorMsg}
	mb.msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindError, payload))
	return mb
}

// WithQuestion sets the question payload (for QUESTION messages).
func (mb *MessageBuilder) WithQuestion(question string) *MessageBuilder {
	questionPayload := &proto.QuestionRequestPayload{
		Text: question,
	}
	mb.msg.SetTypedPayload(proto.NewQuestionRequestPayload(questionPayload))
	return mb
}

// WithAnswer sets the answer payload (for QUESTION responses).
func (mb *MessageBuilder) WithAnswer(answer string) *MessageBuilder {
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: answer,
	}
	mb.msg.SetTypedPayload(proto.NewQuestionResponsePayload(answerPayload))
	return mb
}

// mergeToPayload merges a key-value pair into the existing generic payload or creates one.
func (mb *MessageBuilder) mergeToPayload(key string, value any) {
	existing := mb.msg.GetTypedPayload()
	var data map[string]any

	if existing != nil {
		// Try to extract existing data
		data, _ = existing.ExtractGeneric()
	}

	if data == nil {
		data = make(map[string]any)
	}

	data[key] = value

	// Determine payload kind based on message type
	kind := proto.PayloadKindGeneric
	switch mb.msg.Type {
	case proto.MsgTypeSTORY:
		kind = proto.PayloadKindStory
	case proto.MsgTypeERROR:
		kind = proto.PayloadKindError
	}

	mb.msg.SetTypedPayload(proto.NewGenericPayload(kind, data))
}

// WithMetadata sets a metadata field.
func (mb *MessageBuilder) WithMetadata(key, value string) *MessageBuilder {
	mb.msg.SetMetadata(key, value)
	return mb
}

// WithParentMessage sets the parent message ID.
func (mb *MessageBuilder) WithParentMessage(parentMsg *proto.AgentMsg) *MessageBuilder {
	mb.msg.ParentMsgID = parentMsg.ID
	return mb
}

// WithTimestamp sets a custom timestamp.
func (mb *MessageBuilder) WithTimestamp(timestamp time.Time) *MessageBuilder {
	mb.msg.Timestamp = timestamp
	return mb
}

// Build returns the constructed AgentMsg.
func (mb *MessageBuilder) Build() *proto.AgentMsg {
	return mb.msg
}

// Predefined message factories for common test scenarios.

// HealthEndpointTask creates a standard health endpoint story.
func HealthEndpointTask(fromAgent, toAgent string) *proto.AgentMsg {
	return NewStoryMessage(fromAgent, toAgent).
		WithContent("Create a health endpoint that returns JSON with status and timestamp").
		WithRequirements([]string{
			"GET /health endpoint",
			"Return JSON response",
			"Include status field",
			"Include timestamp field",
			"Return 200 status code",
		}).
		WithMetadata("story_type", "health_endpoint").
		Build()
}

// SuccessfulCodeResult creates a standard successful coding result.
func SuccessfulCodeResult(fromAgent, toAgent, implementation string) *proto.AgentMsg {
	return NewResponseMessage(fromAgent, toAgent).
		WithStatus("completed").
		WithImplementation(implementation).
		WithTestResults(true, "All checks passed: go fmt, go build completed successfully").
		WithMetadata("agent_type", "coding_agent").
		Build()
}

// FailedCodeResult creates a standard failed coding result.
func FailedCodeResult(fromAgent, toAgent, errorMsg string) *proto.AgentMsg {
	return NewErrorMessage(fromAgent, toAgent).
		WithError(errorMsg).
		WithMetadata("error_type", "processing_error").
		Build()
}

// ArchitectTaskResult creates a standard architect task creation result.
func ArchitectTaskResult(fromAgent, toAgent, taskMsgID string) *proto.AgentMsg {
	return NewResponseMessage(fromAgent, toAgent).
		WithStatus("task_created").
		WithMetadata("task_message_id", taskMsgID).
		WithMetadata("target_agent", "claude").
		Build()
}

// ShutdownAcknowledgment creates a standard shutdown acknowledgment.
func ShutdownAcknowledgment(fromAgent, toAgent string) *proto.AgentMsg {
	return NewResponseMessage(fromAgent, toAgent).
		WithStatus("shutdown_acknowledged").
		WithMetadata("agent_type", "test_agent").
		Build()
}

// QuestionAboutArchitecture creates a standard architecture question.
func QuestionAboutArchitecture(fromAgent, toAgent string) *proto.AgentMsg {
	return NewRequestMessage(fromAgent, toAgent).
		WithQuestion("What architecture pattern should I use for this API?").
		WithMetadata("question_type", "architecture").
		Build()
}

// ArchitectureAnswer creates a standard architecture answer.
func ArchitectureAnswer(fromAgent, toAgent string, originalMsg *proto.AgentMsg) *proto.AgentMsg {
	return NewResponseMessage(fromAgent, toAgent).
		WithAnswer("Follow clean architecture principles with clear separation of concerns.").
		WithMetadata("answer_type", "architect_guidance").
		WithParentMessage(originalMsg).
		Build()
}
