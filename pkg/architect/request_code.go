//nolint:dupl // Similar structure to other prompts but intentionally different content
package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCodePrompt creates a prompt for iterative code review.
func (d *Driver) generateCodePrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	storyID := requestMsg.Metadata["story_id"]

	// Get story info from queue for context
	var storyTitle, storyContent string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyContent = story.Content
		}
	}

	toolDocs := toolProvider.GenerateToolDocumentation()

	return fmt.Sprintf(`# Code Review Request (Iterative)

You are the architect reviewing code changes from %s for story: %s

**Story Title:** %s
**Story Content:**
%s

**Code Submission:**
%s

## Your Task

Review the code changes by:
1. Use **list_files** to see what files the coder modified
2. Use **read_file** to inspect specific files that need review
3. Use **get_diff** to see the actual changes made
4. Analyze the code quality, correctness, and adherence to requirements

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

## REQUIRED: Submit Your Decision

**You MUST call the submit_reply tool to provide your final decision.** Do not respond with text only.

Call **submit_reply** with your decision in this format:
- **response**: Your complete decision as a string
- Must start with one of: APPROVED, NEEDS_CHANGES, or REJECTED
- Follow with specific feedback explaining your decision

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to all their files
- Take your time to review thoroughly before submitting your decision
- **Remember: You MUST use submit_reply to send your final decision**

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, coderID, toolDocs, coderID)
}
