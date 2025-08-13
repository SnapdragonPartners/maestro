package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// handleWaiting processes the WAITING state.
func (c *Coder) handleWaiting(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", "WAITING")
	c.contextManager.AddAssistantMessage("Waiting for task assignment")

	// First check if we already have a task from previous processing.
	taskContent, exists := sm.GetStateValue(string(stateDataKeyTaskContent))
	if exists && taskContent != "" {
		logx.DebugState(ctx, "coder", "transition", "WAITING -> SETUP", "task content available")
		return StateSetup, false, nil
	}

	// If no story channel is set, stay in WAITING (shouldn't happen in normal operation).
	if c.storyCh == nil {
		logx.Warnf("🧑‍💻 Coder in WAITING state but no story channel set")
		return proto.StateWaiting, false, nil
	}

	// Block waiting for a story message.
	logx.Infof("🧑‍💻 Coder waiting for story message...")
	select {
	case <-ctx.Done():
		return proto.StateError, false, fmt.Errorf("coder waiting cancelled: %w", ctx.Err())
	case storyMsg, ok := <-c.storyCh:
		if !ok {
			// Channel closed by dispatcher - abnormal shutdown
			logx.Infof("🧑‍💻 Story channel closed, transitioning to ERROR")
			return proto.StateError, true, fmt.Errorf("story channel closed unexpectedly")
		}

		if storyMsg == nil {
			// This shouldn't happen with proper channel management, but handle gracefully
			logx.Warnf("🧑‍💻 Received nil story message on open channel")
			return proto.StateWaiting, false, nil
		}

		// Extract story content and store it in state data.
		content, exists := storyMsg.GetPayload(proto.KeyContent)
		if !exists {
			return proto.StateError, false, logx.Errorf("story message missing content")
		}

		contentStr, ok := content.(string)
		if !ok {
			return proto.StateError, false, logx.Errorf("story content must be a string")
		}

		// Extract the actual story ID from the payload.
		storyID, exists := storyMsg.GetPayload(proto.KeyStoryID)
		if !exists {
			return proto.StateError, false, logx.Errorf("story message missing story_id")
		}

		storyIDStr, ok := storyID.(string)
		if !ok {
			return proto.StateError, false, logx.Errorf("story_id must be a string")
		}

		logx.Infof("🧑‍💻 Received story message %s for story %s, transitioning to SETUP", storyMsg.ID, storyIDStr)

		// Set lease immediately to ensure story is never dropped.
		if c.dispatcher != nil {
			c.dispatcher.SetLease(c.BaseStateMachine.GetAgentID(), storyIDStr)
		}

		// Extract story type from the payload.
		storyType := string(proto.StoryTypeApp) // Default to app
		if storyTypePayload, exists := storyMsg.GetPayload(proto.KeyStoryType); exists {
			c.logger.Info("🧑‍💻 Received story_type payload: '%v' (type: %T)", storyTypePayload, storyTypePayload)
			if storyTypeStr, ok := storyTypePayload.(string); ok && proto.IsValidStoryType(storyTypeStr) {
				storyType = storyTypeStr
				c.logger.Info("🧑‍💻 Set story_type to: '%s'", storyType)
			} else {
				c.logger.Info("🧑‍💻 Invalid story_type payload, using default 'app'")
			}
		} else {
			c.logger.Info("🧑‍💻 No story_type payload found, using default 'app'")
		}

		// Store the task content, story ID, and story type for use in later states.
		sm.SetStateData(string(stateDataKeyTaskContent), contentStr)
		sm.SetStateData(KeyStoryMessageID, storyMsg.ID)
		sm.SetStateData(KeyStoryID, storyIDStr)        // For workspace manager - use actual story ID
		sm.SetStateData(proto.KeyStoryType, storyType) // Store story type for testing decisions
		sm.SetStateData(string(stateDataKeyStartedAt), time.Now().UTC())

		logx.DebugState(ctx, "coder", "transition", "WAITING -> SETUP", "received story message")
		return StateSetup, false, nil
	}
}
