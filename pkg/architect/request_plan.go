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

Please review the plan and provide your decision using review_complete.

Your decision must be: APPROVED, NEEDS_CHANGES, or REJECTED`, approvalPayload.Content)
}
