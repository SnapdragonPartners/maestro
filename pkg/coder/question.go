package coder

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleQuestion processes architect question requests from PLANNING or CODING states.
// It sends a QUESTION message to the architect, waits for an ANSWER, and returns to the origin state.
//
//nolint:unparam // done is always false for non-terminal states, consistent with other state handlers
func (c *Coder) handleQuestion(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	c.logger.Info("🧑‍💻 Handling question for architect")

	// Get pending question from state
	questionDataRaw, exists := sm.GetStateValue(KeyPendingQuestion)
	if !exists {
		return proto.StateError, false, logx.Errorf("no pending question data found in state")
	}

	questionData, ok := questionDataRaw.(map[string]any)
	if !ok || questionData == nil {
		return proto.StateError, false, logx.Errorf("pending question data has invalid type: %T", questionDataRaw)
	}

	// Extract question details
	question := utils.GetMapFieldOr[string](questionData, "question", "")
	contextStr := utils.GetMapFieldOr[string](questionData, "context", "")
	urgency := utils.GetMapFieldOr[string](questionData, "urgency", "medium")
	origin := utils.GetMapFieldOr[string](questionData, "origin", "")

	if question == "" {
		return proto.StateError, false, logx.Errorf("pending question has empty question text")
	}

	if origin == "" {
		return proto.StateError, false, logx.Errorf("pending question has no origin state")
	}

	// Create question effect
	eff := effect.NewQuestionEffect(question, contextStr, urgency, origin)

	// Set story_id for dispatcher validation
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	eff.StoryID = storyID

	c.logger.Info("🧑‍💻 Sending question to architect: %s", question)

	// Execute the question effect (blocks until answer received)
	result, err := c.ExecuteEffect(ctx, eff)
	if err != nil {
		c.logger.Error("🧑‍💻 Failed to get answer: %v", err)
		return proto.StateError, false, logx.Wrap(err, "failed to execute question effect")
	}

	// Process the answer
	if questionResult, ok := result.(*effect.QuestionResult); ok {
		// Answer received from architect
		c.logger.Info("🧑‍💻 Received answer from architect")

		// Add the Q&A to context so the LLM can see it
		qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, questionResult.Answer)
		c.contextManager.AddMessage("architect-answer", qaContent)

		// Mark question as answered
		sm.SetStateData(KeyQuestionAnswered, true)
	} else {
		c.logger.Error("🧑‍💻 Invalid question result type: %T", result)
		return proto.StateError, false, logx.Errorf("invalid question result type: %T", result)
	}

	// Clear the pending question
	sm.SetStateData(KeyPendingQuestion, nil)

	// Transition back to origin state
	c.logger.Info("🧑‍💻 Returning to origin state: %s", origin)
	switch origin {
	case string(StatePlanning):
		return StatePlanning, false, nil
	case string(StateCoding):
		return StateCoding, false, nil
	default:
		return proto.StateError, false, logx.Errorf("unknown origin state: %s", origin)
	}
}
