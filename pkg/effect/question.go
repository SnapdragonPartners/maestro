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
	TargetAgent string
	Timeout     time.Duration
}

// Execute sends a question request and blocks waiting for the answer.
func (e *AwaitQuestionEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()

	// Create REQUEST message with question payload
	questionMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)
	questionMsg.SetPayload(proto.KeyKind, string(proto.RequestKindQuestion))
	questionMsg.SetPayload(proto.KeyQuestion, proto.QuestionRequestPayload{
		Text:    e.Question,
		Context: fmt.Sprintf("%s clarification (%s urgency)", e.OriginState, e.Urgency),
		Urgency: e.Urgency,
	})
	questionMsg.SetPayload(proto.KeyCorrelationID, proto.GenerateCorrelationID())

	if e.Context != "" {
		questionMsg.SetPayload("context", e.Context)
	}

	if e.OriginState != "" {
		questionMsg.SetPayload("origin", e.OriginState)
	}

	runtime.Info("📤 Sending question to %s from %s state: %s", e.TargetAgent, e.OriginState, e.Question)

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

	// Extract answer content
	answerContent := ""
	if content, exists := answerMsg.GetPayload("answer"); exists {
		if contentStr, ok := content.(string); ok {
			answerContent = contentStr
		}
	}

	if answerContent == "" {
		return nil, fmt.Errorf("received empty answer content")
	}

	result := &QuestionResult{
		Answer: answerContent,
		Data:   answerMsg.Payload,
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
