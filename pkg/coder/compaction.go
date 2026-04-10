package coder

import (
	"fmt"
	"strings"

	"orchestrator/pkg/chat"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// configureContextManager applies compaction callback, token counting, and chat service
// to the coder's context manager. Called from both NewCoder and resume paths to prevent drift.
func (c *Coder) configureContextManager(chatService *chat.Service, agentID string) {
	if c.contextManager == nil {
		return
	}

	// Wire compaction callback for state re-injection
	c.contextManager.SetCompactionCallback(c.buildCompactionStateSummary)

	// Wire tiktoken for accurate token counting
	tc, err := utils.NewTokenCounter("gpt-4")
	if err != nil {
		logx.NewLogger("coder").Warn("Failed to create token counter: %v (using char/4 fallback)", err)
	} else {
		c.contextManager.SetTokenCounter(tc)
	}

	// Wire chat service for automatic message injection
	if chatService != nil {
		chatAdapter := contextmgr.NewChatServiceAdapter(chatService)
		c.contextManager.SetChatService(chatAdapter, agentID)
		logx.NewLogger("coder").Info("💬 Chat injection configured for coder %s", agentID)
	}
}

// maxCompactionSummaryChars is the hard cap on the total state summary injected after compaction.
const maxCompactionSummaryChars = 2000

// buildCompactionStateSummary builds a structured state summary for re-injection after
// context compaction. Returns a compact representation of the coder's key working state
// so the LLM can maintain continuity after older messages are removed.
func (c *Coder) buildCompactionStateSummary(removedCount int) string {
	sm := c.BaseStateMachine
	var parts []string

	parts = append(parts,
		fmt.Sprintf("[ Context compacted: %d messages removed. Key working state preserved below. ]", removedCount),
		fmt.Sprintf("Current phase: %s", sm.GetCurrentState()),
	)

	// Story ID
	if storyID := utils.GetStateValueOr[string](sm, KeyStoryID, ""); storyID != "" {
		parts = append(parts, fmt.Sprintf("Story: %s", storyID))
	}

	// Approved plan (truncated to keep summary compact)
	if plan := utils.GetStateValueOr[string](sm, KeyPlan, ""); plan != "" {
		if len(plan) > 600 {
			plan = plan[:600] + "..."
		}
		parts = append(parts, fmt.Sprintf("Approved plan:\n%s", plan))
	}

	// Todo progress
	if c.todoList != nil {
		parts = append(parts, c.getTodoListStatus())
	}

	// Plan confidence
	if conf := utils.GetStateValueOr[string](sm, string(stateDataKeyPlanConfidence), ""); conf != "" {
		parts = append(parts, fmt.Sprintf("Plan confidence: %s", conf))
	}

	result := strings.Join(parts, "\n\n")
	if len(result) > maxCompactionSummaryChars {
		result = result[:maxCompactionSummaryChars] + "\n[... summary truncated ...]"
	}
	return result
}
