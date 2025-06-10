package agents

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// ArchitectAgent implements the Agent interface for processing development stories
type ArchitectAgent struct {
	id         string
	logger     *logx.Logger
	storiesDir string
	dispatcher TaskDispatcher
}

// TaskDispatcher interface for sending tasks to other agents
type TaskDispatcher interface {
	DispatchMessage(msg *proto.AgentMsg) error
}

// NewArchitectAgent creates a new architect agent
func NewArchitectAgent(id, storiesDir string) *ArchitectAgent {
	return &ArchitectAgent{
		id:         id,
		logger:     logx.NewLogger(id),
		storiesDir: storiesDir,
	}
}

// SetDispatcher sets the task dispatcher for routing tasks to other agents
func (a *ArchitectAgent) SetDispatcher(dispatcher TaskDispatcher) {
	a.dispatcher = dispatcher
}

// GetID returns the agent's identifier
func (a *ArchitectAgent) GetID() string {
	return a.id
}

// ProcessMessage processes incoming messages and converts stories to tasks
func (a *ArchitectAgent) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.logger.Info("Processing message %s from %s", msg.ID, msg.FromAgent)

	switch msg.Type {
	case proto.MsgTypeTASK:
		return a.handleTaskMessage(ctx, msg)
	case proto.MsgTypeQUESTION:
		return a.handleQuestionMessage(ctx, msg)
	case proto.MsgTypeSHUTDOWN:
		return a.handleShutdownMessage(ctx, msg)
	default:
		return nil, fmt.Errorf("unsupported message type: %s", msg.Type)
	}
}

// Shutdown performs cleanup when the agent is stopping
func (a *ArchitectAgent) Shutdown(ctx context.Context) error {
	a.logger.Info("Architect agent shutting down")
	return nil
}

func (a *ArchitectAgent) handleTaskMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Extract story ID from the message payload
	storyID, exists := msg.GetPayload("story_id")
	if !exists {
		return nil, fmt.Errorf("missing story_id in task message")
	}

	storyIDStr, ok := storyID.(string)
	if !ok {
		return nil, fmt.Errorf("story_id must be a string")
	}

	a.logger.Info("Processing story: %s", storyIDStr)

	// Read the story file
	story, err := a.readStoryFile(storyIDStr)
	if err != nil {
		return nil, fmt.Errorf("failed to read story %s: %w", storyIDStr, err)
	}

	// For MVP, simulate o3 call with mock response
	// In production, this would call the actual o3 API
	taskContent := a.generateTaskFromStory(story)

	// Create task message for coding agent
	taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, a.id, "claude")
	taskMsg.ParentMsgID = msg.ID
	taskMsg.SetPayload("story_id", storyIDStr)
	taskMsg.SetPayload("content", taskContent)
	taskMsg.SetPayload("requirements", a.extractRequirements(story))
	taskMsg.SetMetadata("story_source", filepath.Join(a.storiesDir, storyIDStr+".md"))
	taskMsg.SetMetadata("processing_agent", "o3-simulated")

	a.logger.Info("Generated task for story %s: %s", storyIDStr, taskContent)

	// Send task to coding agent if dispatcher is available
	if a.dispatcher != nil {
		err = a.dispatcher.DispatchMessage(taskMsg)
		if err != nil {
			a.logger.Error("Failed to dispatch task to coding agent: %v", err)
			return nil, fmt.Errorf("failed to dispatch task: %w", err)
		}
		a.logger.Info("Successfully dispatched task %s to %s", taskMsg.ID, taskMsg.ToAgent)
	}

	// Return result indicating task was created and dispatched
	result := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	result.ParentMsgID = msg.ID
	result.SetPayload("status", "task_created")
	result.SetPayload("task_message_id", taskMsg.ID)
	result.SetPayload("target_agent", "claude")
	result.SetMetadata("story_processed", storyIDStr)

	return result, nil
}

func (a *ArchitectAgent) handleQuestionMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	question, exists := msg.GetPayload("question")
	if !exists {
		return nil, fmt.Errorf("missing question in message")
	}

	questionStr, ok := question.(string)
	if !ok {
		return nil, fmt.Errorf("question must be a string")
	}

	a.logger.Info("Received question: %s", questionStr)

	// For MVP, provide simple responses to common questions
	answer := a.generateAnswer(questionStr)

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("answer", answer)
	response.SetPayload("question", questionStr)
	response.SetMetadata("answer_type", "architect_guidance")

	return response, nil
}

func (a *ArchitectAgent) handleShutdownMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	a.logger.Info("Received shutdown request")

	response := proto.NewAgentMsg(proto.MsgTypeRESULT, a.id, msg.FromAgent)
	response.ParentMsgID = msg.ID
	response.SetPayload("status", "shutdown_acknowledged")
	response.SetMetadata("agent_type", "architect")

	return response, nil
}

func (a *ArchitectAgent) readStoryFile(storyID string) (string, error) {
	filename := filepath.Join(a.storiesDir, storyID+".md")

	data, err := os.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read story file %s: %w", filename, err)
	}

	return string(data), nil
}

func (a *ArchitectAgent) generateTaskFromStory(story string) string {
	// Simple MVP implementation - extract title and key points
	lines := strings.Split(story, "\n")

	var title string
	var requirements []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Extract title from markdown headers
		if strings.HasPrefix(line, "# ") {
			title = strings.TrimPrefix(line, "# ")
		} else if strings.HasPrefix(line, "## ") && title == "" {
			title = strings.TrimPrefix(line, "## ")
		}

		// Look for bullet points as requirements
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			req := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			if req != "" {
				requirements = append(requirements, req)
			}
		}
	}

	if title == "" {
		title = "Development Task"
	}

	taskContent := fmt.Sprintf("Implement: %s", title)
	if len(requirements) > 0 {
		taskContent += "\n\nRequirements:\n"
		for _, req := range requirements {
			taskContent += fmt.Sprintf("- %s\n", req)
		}
	}

	return taskContent
}

func (a *ArchitectAgent) extractRequirements(story string) []string {
	lines := strings.Split(story, "\n")
	var requirements []string

	for _, line := range lines {
		line = strings.TrimSpace(line)

		// Look for bullet points and numbered lists
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			req := strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* ")
			if req != "" {
				requirements = append(requirements, req)
			}
		} else if len(line) > 3 && strings.Contains(line[:3], ".") {
			// Handle numbered lists like "1. ", "2. ", etc.
			parts := strings.SplitN(line, ".", 2)
			if len(parts) == 2 {
				req := strings.TrimSpace(parts[1])
				if req != "" {
					requirements = append(requirements, req)
				}
			}
		}
	}

	return requirements
}

func (a *ArchitectAgent) generateAnswer(question string) string {
	question = strings.ToLower(question)

	// Simple pattern matching for common architectural questions
	if strings.Contains(question, "pattern") || strings.Contains(question, "architecture") {
		return "Follow clean architecture principles with clear separation of concerns. Consider using dependency injection and interface-based design."
	}

	if strings.Contains(question, "database") || strings.Contains(question, "storage") {
		return "Choose appropriate storage based on data access patterns. Consider ACID properties for transactional data, eventual consistency for scalability."
	}

	if strings.Contains(question, "api") || strings.Contains(question, "rest") {
		return "Design RESTful APIs with clear resource modeling. Use proper HTTP status codes and implement consistent error handling."
	}

	if strings.Contains(question, "test") || strings.Contains(question, "testing") {
		return "Implement comprehensive testing strategy: unit tests for business logic, integration tests for components, and end-to-end tests for critical workflows."
	}

	if strings.Contains(question, "performance") || strings.Contains(question, "scale") {
		return "Focus on performance bottlenecks first. Implement caching strategically, optimize database queries, and consider async processing for heavy operations."
	}

	// Default response
	return "Based on the context, consider the trade-offs between complexity, maintainability, and performance. Follow established patterns and best practices for the technology stack."
}
