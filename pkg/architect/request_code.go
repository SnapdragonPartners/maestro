package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCodePrompt creates a structured review prompt for code reviews.
// Context (story, role, tools) is already in the system prompt.
// The prompt enforces a step-by-step protocol that requires fresh tool calls,
// preventing the architect from relying on stale tool results from prior iterations.
func (d *Driver) generateCodePrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	// Story context is provided in the system prompt (via ensureContextForStory)
	// AND inline in the request payload content. Both are available to the LLM.
	_ = requestMsg
	_ = coderID
	_ = toolProvider

	return fmt.Sprintf(`The coder submitted their code for review:

%s

## Review Protocol

Follow these steps IN ORDER:

1. Call get_diff to see the current state of all changes on this branch.
2. Review the diff against the story acceptance criteria (shown in the system prompt above).
3. If you need more detail on specific files, use read_file to inspect them.
4. Call review_complete with your decision (status: APPROVED/NEEDS_CHANGES/REJECTED) and detailed feedback explaining your reasoning.

**Important**: If you have previously reviewed this coder's work in this conversation, the coder has made changes since then. Your earlier tool results are OUTDATED. You MUST call get_diff again to see the current workspace state. Base your review only on what the tools show you NOW, not on previous results.

The story acceptance criteria are the authoritative requirements. Do not introduce new requirements or reference external specifications not mentioned in the story.`, approvalPayload.Content)
}
