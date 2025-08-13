package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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
	// Extract question details from message - handle different formats.
	var storyID, question string

	// Try the expected format first.
	if id, ok := msg.Payload["story_id"].(string); ok {
		storyID = id
	}
	if q, ok := msg.Payload[proto.KeyQuestion].(string); ok {
		question = q
	}

	// If that didn't work, try the format that Claude agents actually send.
	if storyID == "" || question == "" {
		// Claude agents send the full task content as "question".
		if taskContent, ok := msg.Payload[proto.KeyQuestion].(string); ok {
			// Extract story ID from the task content (front matter).
			if extractedID := qh.extractStoryIDFromContent(taskContent); extractedID != "" {
				storyID = extractedID
				// Extract question from unified protocol
				if questionPayload, ok := msg.Payload[proto.KeyQuestion]; ok {
					switch qp := questionPayload.(type) {
					case proto.QuestionRequestPayload:
						question = qp.Text
					case string:
						question = qp
					default:
						question = "Technical assistance requested during development"
					}
				} else {
					question = "Technical assistance requested during development"
				}
			}
		}
	}

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

	// Copy relevant context from message payload.
	for key, value := range msg.Payload {
		if key != proto.KeyStoryID && key != proto.KeyQuestion {
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
			"story_file_path":  story.FilePath,
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

	// Get LLM response using centralized helper
	answer, err := qh.driver.callLLMWithTemplate(ctx, prompt)
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

	// Set answer payload.
	resultMsg.Payload["question_id"] = pendingQ.ID
	resultMsg.Payload["story_id"] = pendingQ.StoryID
	resultMsg.Payload[proto.KeyAnswer] = pendingQ.Answer
	resultMsg.Payload["answered_at"] = pendingQ.AnsweredAt.Format(time.RFC3339)

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

// extractStoryIDFromContent extracts the story ID from task content front matter.
func (qh *QuestionHandler) extractStoryIDFromContent(content string) string {
	lines := strings.Split(content, "\n")
	inFrontMatter := false

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "---" {
			if inFrontMatter {
				// End of front matter, didn't find ID.
				break
			}
			inFrontMatter = true
			continue
		}

		if inFrontMatter && strings.HasPrefix(line, "id:") {
			// Extract ID value.
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}

	return ""
}

// truncateString truncates a string to the specified length.
func truncateString(s string, maxLength int) string {
	if len(s) <= maxLength {
		return s
	}
	return s[:maxLength] + "..."
}
