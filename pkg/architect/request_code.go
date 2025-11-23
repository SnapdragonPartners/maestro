package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCodePrompt creates a concise user message for code reviews.
// Context (story, role, tools) is already in the system prompt.
func (d *Driver) generateCodePrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	_ = requestMsg   // context already in system prompt
	_ = coderID      // context already in system prompt
	_ = toolProvider // tools already documented in system prompt

	return fmt.Sprintf(`The coder submitted their code for review:

%s

Please review the code changes against the story acceptance criteria (shown in the system prompt above). Inspect their workspace to verify each acceptance criterion is met.

The story acceptance criteria are the authoritative requirements. Do not introduce new requirements or reference external specifications not mentioned in the story.

Provide your decision using submit_reply.

Your response must start with: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
