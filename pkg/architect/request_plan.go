package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
)

// generatePlanPrompt creates a concise user message for plan reviews.
// Context (story, role) is already in the system prompt.
func (d *Driver) generatePlanPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) string {
	_ = requestMsg // context already in system prompt

	return fmt.Sprintf(`The coder submitted their implementation plan for review:

%s

Please review the plan against the story acceptance criteria (shown in the system prompt above). The acceptance criteria are the authoritative requirements - verify the plan addresses each one.

If the plan contradicts the story requirements, request changes with specific reference to which acceptance criteria are not met.

Provide your decision using review_complete.

Your decision must be: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
