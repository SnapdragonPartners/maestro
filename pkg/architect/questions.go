package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

const (
	questionStatusAnswered  = "answered"
	questionStatusEscalated = "escalated"
)

// QuestionHandler manages technical question processing for the ANSWERING state.
type QuestionHandler struct {
	llmClient         agent.LLMClient
	renderer          *templates.Renderer
	queue             *Queue
	escalationHandler *EscalationHandler
	driver            *Driver // Reference to driver for Effects execution
	// Track pending questions.
	pendingQuestions map[string]*PendingQuestion // questionID -> PendingQuestion
	workDir          string                      // Working directory for user instructions
}

// PendingQuestion represents a question awaiting response.
//
//nolint:govet // Large complex struct, logical grouping preferred
type PendingQuestion struct {
	ID         string         `json:"id"`
	StoryID    string         `json:"story_id"`
	AgentID    string         `json:"agent_id"`
	Question   string         `json:"question"`
	Context    map[string]any `json:"context"`
	AskedAt    time.Time      `json:"asked_at"`
	Status     string         `json:"status"` // "pending", "answered", "escalated"
	Answer     string         `json:"answer,omitempty"`
	AnsweredAt *time.Time     `json:"answered_at,omitempty"`
}

// NewQuestionHandler creates a new question handler.
func NewQuestionHandler(llmClient agent.LLMClient, renderer *templates.Renderer, queue *Queue, escalationHandler *EscalationHandler, workDir string, driver *Driver) *QuestionHandler {
	return &QuestionHandler{
		llmClient:         llmClient,
		renderer:          renderer,
		queue:             queue,
		escalationHandler: escalationHandler,
		workDir:           workDir,
		driver:            driver,
		pendingQuestions:  make(map[string]*PendingQuestion),
	}
}

// HandleQuestion processes an incoming QUESTION message from a coding agent.
func (qh *QuestionHandler) HandleQuestion(ctx context.Context, msg *proto.AgentMsg) error {
	// Extract question details from typed payload
	typedPayload := msg.GetTypedPayload()
	if typedPayload == nil {
		return fmt.Errorf("question message missing typed payload")
	}

	questionPayload, err := typedPayload.ExtractQuestionRequest()
	if err != nil {
		return fmt.Errorf("failed to extract question request: %w", err)
	}

	// Extract story_id from metadata
	storyID := ""
	if sid, exists := questionPayload.Metadata["story_id"]; exists {
		storyID = sid
	}

	question := questionPayload.Text

	if storyID == "" || question == "" {
		return fmt.Errorf("invalid question message: missing story_id or question (storyID='%s', question='%s')", storyID, question)
	}

	// Create pending question record.
	pendingQ := &PendingQuestion{
		ID:       msg.ID, // Use message ID as question ID
		StoryID:  storyID,
		AgentID:  msg.FromAgent,
		Question: question,
		Context:  make(map[string]any),
		AskedAt:  time.Now().UTC(),
		Status:   "pending",
	}

	// Copy metadata to context
	for key, value := range questionPayload.Metadata {
		if key != "story_id" {
			pendingQ.Context[key] = value
		}
	}

	// Store pending question.
	qh.pendingQuestions[pendingQ.ID] = pendingQ

	// Check if this is a business question that should be escalated.
	if qh.isBusinessQuestion(question, pendingQ.Context) {
		return qh.escalateQuestion(ctx, pendingQ)
	}

	// Process technical question with LLM.
	return qh.answerTechnicalQuestion(ctx, pendingQ)
}

// answerTechnicalQuestion uses the LLM to answer a technical question.
func (qh *QuestionHandler) answerTechnicalQuestion(ctx context.Context, pendingQ *PendingQuestion) error {
	if qh.llmClient == nil {
		// Mock mode - provide a simple answer.
		return qh.sendMockAnswer(ctx, pendingQ)
	}

	// Get story context for better answers.
	story, exists := qh.queue.GetStory(pendingQ.StoryID)
	if !exists {
		return fmt.Errorf("story %s not found in queue", pendingQ.StoryID)
	}

	// Prepare template data for Q&A prompt.
	templateData := &templates.TemplateData{
		TaskContent: pendingQ.Question,
		Extra: map[string]any{
			"story_id":         pendingQ.StoryID,
			"story_title":      story.Title,
			"story_type":       story.StoryType,
			"story_content":    story.Content,
			"agent_id":         pendingQ.AgentID,
			"question_id":      pendingQ.ID,
			"question_context": pendingQ.Context,
		},
	}

	// Render Q&A prompt template.
	prompt, err := qh.renderer.RenderWithUserInstructions(templates.TechnicalQATemplate, templateData, qh.workDir, "ARCHITECT")
	if err != nil {
		return fmt.Errorf("failed to render Q&A template: %w", err)
	}

	// Reset context for this Q&A
	templateName := fmt.Sprintf("qa-%s", pendingQ.ID)
	qh.driver.contextManager.ResetForNewTemplate(templateName, prompt)

	// Use toolloop with submit_reply tool to get structured answer
	answer, err := qh.driver.toolLoop.Run(ctx, &toolloop.Config{
		ContextManager: qh.driver.contextManager,
		ToolProvider:   newListToolProvider([]tools.Tool{tools.NewSubmitReplyTool()}),
		CheckTerminal:  qh.driver.checkTerminalTools,
		OnIterationLimit: func(_ context.Context) (string, error) {
			return "", fmt.Errorf("maximum tool iterations exceeded for question answering")
		},
		MaxIterations: 10,
		MaxTokens:     agent.ArchitectMaxTokens,
		AgentID:       qh.driver.architectID,
	})

	if err != nil {
		return fmt.Errorf("failed to get LLM response for question: %w", err)
	}

	// Update question record.
	now := time.Now().UTC()
	pendingQ.Answer = answer
	pendingQ.Status = questionStatusAnswered
	pendingQ.AnsweredAt = &now

	// Send RESULT message back to the requesting agent.
	return qh.sendAnswerToAgent(ctx, pendingQ)
}

// sendMockAnswer provides a mock answer for testing.
func (qh *QuestionHandler) sendMockAnswer(ctx context.Context, pendingQ *PendingQuestion) error {
	mockAnswer := fmt.Sprintf("Mock answer for question: %s\n\nThis is a simulated technical response that would normally be generated by the LLM based on the question context and story details.", pendingQ.Question)

	// Update question record.
	now := time.Now().UTC()
	pendingQ.Answer = mockAnswer
	pendingQ.Status = questionStatusAnswered
	pendingQ.AnsweredAt = &now

	// Send answer back to agent.
	return qh.sendAnswerToAgent(ctx, pendingQ)
}

// sendAnswerToAgent sends an ANSWER message with the answer back to the requesting agent using Effects.
func (qh *QuestionHandler) sendAnswerToAgent(ctx context.Context, pendingQ *PendingQuestion) error {
	// Create RESPONSE message.
	resultMsg := proto.NewAgentMsg(
		proto.MsgTypeRESPONSE,
		"architect",      // from
		pendingQ.AgentID, // to
	)

	// Set parent message ID to link back to the question.
	resultMsg.ParentMsgID = pendingQ.ID

	// Set typed question response payload
	answerPayload := &proto.QuestionResponsePayload{
		AnswerText: pendingQ.Answer,
		Metadata: map[string]string{
			"question_id": pendingQ.ID,
			"story_id":    pendingQ.StoryID,
			"answered_at": pendingQ.AnsweredAt.Format(time.RFC3339),
		},
	}
	resultMsg.SetTypedPayload(proto.NewQuestionResponsePayload(answerPayload))

	// Add metadata.
	resultMsg.SetMetadata("question_type", "technical")
	resultMsg.SetMetadata("answer_method", qh.getAnswerMethod())

	// Log the answer for debugging.
	fmt.Printf("📝 Answered question %s for story %s: %s\n",
		pendingQ.ID, pendingQ.StoryID, truncateString(pendingQ.Answer, 100))

	// Send using Effects pattern.
	return qh.sendResponseEffect(ctx, resultMsg)
}

// sendResponseEffect sends a response message using the Effects pattern.
func (qh *QuestionHandler) sendResponseEffect(ctx context.Context, msg *proto.AgentMsg) error {
	if qh.driver == nil {
		return fmt.Errorf("no driver available for Effects execution")
	}
	effect := &SendResponseEffect{Response: msg}
	return qh.driver.ExecuteEffect(ctx, effect)
}

// isBusinessQuestion determines if a question requires business-level escalation.
func (qh *QuestionHandler) isBusinessQuestion(question string, context map[string]any) bool {
	// Check for business-related keywords or flags.
	businessKeywords := []string{
		"business", "requirement", "stakeholder", "customer",
		"revenue", "pricing", "policy", "compliance",
		"legal", "regulation", "strategy", "roadmap",
	}

	questionLower := strings.ToLower(question)
	for _, keyword := range businessKeywords {
		if strings.Contains(questionLower, keyword) {
			return true
		}
	}

	// Check for explicit business flag in context.
	if businessFlag, exists := context["is_business_question"]; exists {
		if business, ok := businessFlag.(bool); ok && business {
			return true
		}
	}

	return false
}

// escalateQuestion handles business questions that need human intervention.
func (qh *QuestionHandler) escalateQuestion(ctx context.Context, pendingQ *PendingQuestion) error {
	// Update question status.
	pendingQ.Status = questionStatusEscalated

	// Use the escalation handler to properly escalate the business question.
	if qh.escalationHandler != nil {
		if err := qh.escalationHandler.EscalateBusinessQuestion(ctx, pendingQ); err != nil {
			return fmt.Errorf("failed to escalate business question: %w", err)
		}
	} else {
		// Fallback for when no escalation handler is available.
		fmt.Printf("🚨 Escalated business question %s for story %s: %s\n",
			pendingQ.ID, pendingQ.StoryID, truncateString(pendingQ.Question, 100))
	}

	return nil
}

// formatQuestionContext creates a context string for the LLM prompt.

// getAnswerMethod returns the method used to answer questions.
func (qh *QuestionHandler) getAnswerMethod() string {
	if qh.llmClient == nil {
		return "mock"
	}
	return "llm"
}

// GetPendingQuestions returns all pending questions.
func (qh *QuestionHandler) GetPendingQuestions() []*PendingQuestion {
	questions := make([]*PendingQuestion, 0, len(qh.pendingQuestions))
	for _, q := range qh.pendingQuestions {
		questions = append(questions, q)
	}
	return questions
}

// GetQuestionStatus returns statistics about question handling.
func (qh *QuestionHandler) GetQuestionStatus() *QuestionStatus {
	status := &QuestionStatus{
		TotalQuestions:     len(qh.pendingQuestions),
		PendingQuestions:   0,
		AnsweredQuestions:  0,
		EscalatedQuestions: 0,
		Questions:          make([]*PendingQuestion, 0, len(qh.pendingQuestions)),
	}

	for _, q := range qh.pendingQuestions {
		status.Questions = append(status.Questions, q)

		switch q.Status {
		case "pending":
			status.PendingQuestions++
		case questionStatusAnswered:
			status.AnsweredQuestions++
		case questionStatusEscalated:
			status.EscalatedQuestions++
		}
	}

	return status
}

// ClearAnsweredQuestions removes answered questions from memory (cleanup).
func (qh *QuestionHandler) ClearAnsweredQuestions() int {
	cleared := 0
	for id, q := range qh.pendingQuestions {
		if q.Status == questionStatusAnswered {
			delete(qh.pendingQuestions, id)
			cleared++
		}
	}
	return cleared
}

// QuestionStatus represents the current state of question handling.
//
//nolint:govet // JSON serialization struct, logical order preferred
type QuestionStatus struct {
	TotalQuestions     int                `json:"total_questions"`
	PendingQuestions   int                `json:"pending_questions"`
	AnsweredQuestions  int                `json:"answered_questions"`
	EscalatedQuestions int                `json:"escalated_questions"`
	Questions          []*PendingQuestion `json:"questions"`
}

// truncateString truncates a string to the specified length.
func truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "..."
}
