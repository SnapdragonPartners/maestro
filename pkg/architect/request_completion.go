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

Please verify completion by reviewing their workspace and checking that ALL acceptance criteria in the story (shown in the system prompt above) are met.

The story acceptance criteria are the authoritative definition of "done". Each criterion must be satisfied for approval.

Provide your decision using submit_reply.

Your response must start with: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
