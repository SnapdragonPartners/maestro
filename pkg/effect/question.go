package effect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// AwaitQuestionEffect represents an async question request effect.
type AwaitQuestionEffect struct {
	Question    string
	Context     string
	Urgency     string
	OriginState string
	StoryID     string // Story ID for message payload (required by dispatcher)
	TargetAgent string
	Timeout     time.Duration
}

// Execute sends a question request and blocks waiting for the answer.
func (e *AwaitQuestionEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()

	// Create REQUEST message with question payload
	questionMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)

	// Build question request payload
	payload := &proto.QuestionRequestPayload{
		Text:     e.Question,
		Context:  fmt.Sprintf("%s clarification (%s urgency)", e.OriginState, e.Urgency),
		Urgency:  e.Urgency,
		Metadata: make(map[string]string),
	}

	// Add story_id to metadata (required by dispatcher)
	if e.StoryID != "" {
		payload.Metadata["story_id"] = e.StoryID
	}

	// Add origin state to metadata if provided
	if e.OriginState != "" {
		payload.Metadata["origin"] = e.OriginState
	}

	// Set typed payload
	questionMsg.SetTypedPayload(proto.NewQuestionRequestPayload(payload))

	// Store correlation_id and story_id in message metadata for tracking
	questionMsg.SetMetadata("correlation_id", proto.GenerateCorrelationID())
	if e.StoryID != "" {
		questionMsg.SetMetadata("story_id", e.StoryID)
	}

	runtime.Info("📤 Sending question to %s from %s state", e.TargetAgent, e.OriginState)

	// Send the question
	if err := runtime.SendMessage(questionMsg); err != nil {
		return nil, fmt.Errorf("failed to send question: %w", err)
	}

	// Create timeout context
	timeoutCtx := ctx
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	runtime.Debug("⏳ Blocking waiting for RESULT message from %s", e.TargetAgent)

	// Block waiting for RESPONSE message (unified protocol)
	answerMsg, err := runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
	if err != nil {
		return nil, fmt.Errorf("failed to receive answer: %w", err)
	}

	// Extract answer content from typed payload
	typedPayload := answerMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("answer message missing typed payload")
	}

	answerPayload, err := typedPayload.ExtractQuestionResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to extract question response: %w", err)
	}

	if answerPayload.AnswerText == "" {
		return nil, fmt.Errorf("received empty answer content")
	}

	result := &QuestionResult{
		Answer: answerPayload.AnswerText,
		Data:   nil, // No longer using map[string]any - typed payload only
	}

	runtime.Info("✅ Received answer from %s", e.TargetAgent)
	return result, nil
}

// Type returns the effect type identifier.
func (e *AwaitQuestionEffect) Type() string {
	return "await_question"
}

// QuestionResult represents the result of a question request.
type QuestionResult struct {
	Data   map[string]any `json:"data,omitempty"`
	Answer string         `json:"answer"`
}

// NewQuestionEffect creates an effect for question requests.
func NewQuestionEffect(question, context, urgency, originState string) *AwaitQuestionEffect {
	return &AwaitQuestionEffect{
		Question:    question,
		Context:     context,
		Urgency:     urgency,
		OriginState: originState,
		TargetAgent: "architect",
		Timeout:     3 * time.Minute, // Timeout for question responses
	}
}
