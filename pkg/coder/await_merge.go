package coder

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/git"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// handleAwaitMerge processes the AWAIT_MERGE state using Effects pattern
//
//nolint:unparam // bool return is part of state machine interface
func (c *Coder) handleAwaitMerge(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get merge result that was stored by PREPARE_MERGE state
	mergeResultRaw, exists := sm.GetStateValue(KeyMergeResult)
	if !exists {
		return proto.StateError, false, logx.Errorf("no merge result found in AWAIT_MERGE state - should have been set by PREPARE_MERGE")
	}

	// Process the merge result
	if mergeResult, ok := mergeResultRaw.(*git.MergeResult); ok {
		return c.processMergeResult(ctx, sm, mergeResult)
	}

	return proto.StateError, false, logx.Errorf("invalid merge result type: %T", mergeResultRaw)
}

// processMergeResult processes the architect's merge response and determines next state.
func (c *Coder) processMergeResult(_ context.Context, sm *agent.BaseStateMachine, result *git.MergeResult) (proto.State, bool, error) {
	// Store completion timestamp
	sm.SetStateData(KeyMergeCompletedAt, time.Now().UTC())

	// Handle merge based on standardized approval status
	switch result.Status {
	case string(proto.ApprovalStatusApproved):
		c.logger.Info("🧑‍💻 PR merged successfully, story complete")

		// Apply any pending container config updates from this story
		// This is deferred from container_update tool until after merge succeeds
		c.applyPendingContainerConfig()

		// Check if knowledge.dot was modified and trigger reindexing
		c.checkAndReindexKnowledge()

		return proto.StateDone, false, nil

	case string(proto.ApprovalStatusNeedsChanges):
		// Get merge feedback
		feedback := result.ConflictInfo
		if feedback == "" {
			feedback = "Unknown merge issue"
		}

		c.logger.Info("🧑‍💻 Merge needs changes, transitioning to CODING: %s", feedback)

		// If all todos were complete (which allowed done to be called), add feedback as new todo
		if c.todoList != nil {
			allComplete := c.todoList.GetCurrentTodo() == nil && c.todoList.GetCompletedCount() == c.todoList.GetTotalCount()
			if allComplete && feedback != "" {
				feedbackTodo := fmt.Sprintf("Address merge issue: %s", feedback)
				c.todoList.AddTodo(feedbackTodo, -1) // -1 means append to end
				c.logger.Info("📋 Added merge feedback as new todo")
				sm.SetStateData("todo_list", c.todoList)
			}
		}

		// Use mini-template to format the merge failure message
		if c.renderer != nil {
			renderedMessage, err := c.renderer.RenderSimple(templates.MergeFailureFeedbackTemplate, feedback)
			if err != nil {
				c.logger.Error("Failed to render merge failure feedback: %v", err)
				// Fallback to simple message
				renderedMessage = fmt.Sprintf("Merge requires changes. Fix and resubmit: %s", feedback)
			}
			c.contextManager.AddMessage("architect", renderedMessage)
		}

		return StateCoding, false, nil

	case string(proto.ApprovalStatusRejected):
		c.logger.Error("🧑‍💻 Merge rejected - unrecoverable error: %s", result.ConflictInfo)
		return proto.StateError, false, logx.Errorf("merge rejected: %s", result.ConflictInfo)

	default:
		return proto.StateError, false, logx.Errorf("unknown merge status: %s", result.Status)
	}
}

// checkAndReindexKnowledge checks if knowledge.dot was modified and triggers reindexing.
func (c *Coder) checkAndReindexKnowledge() {
	// Get workspace path
	knowledgePath := filepath.Join(c.workDir, ".maestro", "knowledge.dot")

	// Get session ID from config
	cfg, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Failed to get config for knowledge reindexing: %v", err)
		return
	}

	// Create response channel for modification check
	responseChan := make(chan interface{}, 1)

	// Check if graph was modified via persistence queue
	c.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpCheckKnowledgeModified,
		Data: &persistence.CheckKnowledgeModifiedRequest{
			DotPath: knowledgePath,
		},
		Response: responseChan,
	}

	// Wait for response with timeout
	select {
	case resp := <-responseChan:
		if err, ok := resp.(error); ok {
			c.logger.Warn("Failed to check knowledge modification: %v", err)
			return
		}
		if modified, ok := resp.(bool); ok && modified {
			c.logger.Info("📚 Knowledge graph modified, triggering reindex")

			// Trigger rebuild via persistence queue (fire-and-forget)
			c.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpRebuildKnowledgeIndex,
				Data: &persistence.RebuildKnowledgeIndexRequest{
					DotPath:   knowledgePath,
					SessionID: cfg.SessionID,
				},
				Response: nil, // Fire-and-forget
			}
		}
	case <-time.After(2 * time.Second):
		c.logger.Warn("Knowledge modification check timed out")
	}
}

// applyPendingContainerConfig applies any pending container configuration after successful merge.
func (c *Coder) applyPendingContainerConfig() {
	name, dockerfile, imageID, exists := c.GetPendingContainerConfig()
	if !exists {
		return
	}

	c.logger.Info("🐳 Applying pending container config: name=%s, dockerfile=%s, imageID=%s",
		name, dockerfile, imageID)

	// Get current config to preserve existing settings
	cfg, err := config.GetConfig()
	if err != nil {
		c.logger.Warn("Failed to get config for container update: %v", err)
		return
	}

	// Update container fields from pending config
	containerCfg := cfg.Container
	if containerCfg == nil {
		containerCfg = &config.ContainerConfig{}
	}
	containerCfg.Name = name
	containerCfg.Dockerfile = dockerfile
	containerCfg.PinnedImageID = imageID

	// Apply to global config
	if updateErr := config.UpdateContainer(containerCfg); updateErr != nil {
		c.logger.Warn("Failed to update container config: %v", updateErr)
		return
	}

	c.logger.Info("🐳 Successfully applied pending container config")

	// Clear pending config
	c.hasPendingContainerConfig = false
}
