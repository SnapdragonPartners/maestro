//nolint:dupl // Similar structure to other prompts but intentionally different content
package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateCompletionPrompt creates a prompt for iterative completion review.
func (d *Driver) generateCompletionPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
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

	return fmt.Sprintf(`# Story Completion Review Request (Iterative)

You are the architect reviewing a completion request from %s for story: %s

**Story Title:** %s
**Story Content:**
%s

**Completion Claim:**
%s

## Your Task

Verify the story is complete by:
1. Use **list_files** to see what files were created/modified
2. Use **read_file** to inspect the implementation
3. Use **get_diff** to see all changes made vs main branch
4. Verify all acceptance criteria are met

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

When you have completed your review, call **submit_reply** with your decision:
- Your response must start with one of: APPROVED, NEEDS_CHANGES, or REJECTED
- Provide specific feedback on what's complete or what still needs work

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- Verify the implementation matches the story requirements
- Check for code quality, tests, documentation as needed
- Be thorough but fair in your assessment

Begin your review now.`, coderID, storyID, storyTitle, storyContent, approvalPayload.Content, coderID, coderID, toolDocs, coderID)
}
