package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
)

// generatePlanPrompt creates a concise user message for plan reviews.
// Context (story, role) is already in the system prompt.
func (d *Driver) generatePlanPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) string {
	// Story context is provided in the system prompt (via ensureContextForStory)
	// AND inline in the request payload content. Both are available to the LLM.
	_ = requestMsg

	return fmt.Sprintf(`The coder submitted their implementation plan for review:

%s

Please review the plan against the story acceptance criteria (shown in the system prompt above). The acceptance criteria are the authoritative requirements - verify the plan addresses each one.

If the plan contradicts the story requirements, request changes with specific reference to which acceptance criteria are not met.

Provide your decision using review_complete.

Your decision must be: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
