package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCompletionPrompt creates a structured review prompt for completion reviews.
// Context (story, role, tools) is already in the system prompt.
// The prompt enforces a step-by-step protocol that requires fresh tool calls,
// preventing the architect from relying on stale tool results from prior iterations.
func (d *Driver) generateCompletionPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	_ = requestMsg   // context already in system prompt
	_ = coderID      // context already in system prompt
	_ = toolProvider // tools already documented in system prompt

	return fmt.Sprintf(`The coder claims the story is complete:

%s

## Verification Protocol

Follow these steps IN ORDER:

1. Call get_diff to check if any code changes were made on this branch.
2. Use read_file and list_files to verify that ALL acceptance criteria in the story (shown in the system prompt above) are satisfied.
3. Call review_complete with your decision (status: APPROVED/NEEDS_CHANGES/REJECTED) and detailed feedback explaining your reasoning.

**Important**: If you have previously reviewed this coder's work in this conversation, the coder has made changes since then. Your earlier tool results are OUTDATED. You MUST use fresh tool calls to inspect the current workspace state. Base your review only on what the tools show you NOW, not on previous results.

The story acceptance criteria are the authoritative definition of "done". Each criterion must be satisfied for approval.`, approvalPayload.Content)
}
