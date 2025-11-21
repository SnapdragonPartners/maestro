package architect

import (
	"orchestrator/pkg/proto"
)

// generateBudgetPrompt creates the prompt for budget review requests.
// The content is already fully rendered by the coder, so we just extract and return it.
func (d *Driver) generateBudgetPrompt(requestMsg *proto.AgentMsg) string {
	// Extract data from typed payload
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Warn("Budget review request missing typed payload")
		return "Budget review request missing data"
	}

	// Extract approval request payload which contains the pre-rendered content
	approvalPayload, err := typedPayload.ExtractApprovalRequest()
	if err != nil {
		d.logger.Warn("Failed to extract approval request payload: %v", err)
		return "Budget review request data extraction failed"
	}

	// The content field already contains the fully rendered budget review request
	// from the coder, including recent activity, issue patterns, and all context.
	// We just return it directly - no need to re-render or extract metadata.
	if approvalPayload.Content == "" {
		d.logger.Warn("Budget review request has empty content field")
		return "Budget review request is missing content"
	}

	return approvalPayload.Content
}
