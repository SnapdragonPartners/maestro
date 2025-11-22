package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCompletionPrompt creates a concise user message for completion reviews.
// Context (story, role, tools) is already in the system prompt.
func (d *Driver) generateCompletionPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	_ = requestMsg   // context already in system prompt
	_ = coderID      // context already in system prompt
	_ = toolProvider // tools already documented in system prompt

	return fmt.Sprintf(`The coder claims the story is complete:

%s

Please verify completion by reviewing their workspace, checking all acceptance criteria are met, and provide your decision using submit_reply.

Your response must start with: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
