//nolint:dupl // Similar structure to other prompts but intentionally different content
package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateQuestionPrompt creates a prompt for iterative technical question answering.
func (d *Driver) generateQuestionPrompt(requestMsg *proto.AgentMsg, questionPayload *proto.QuestionRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
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

	return fmt.Sprintf(`# Technical Question from Coder (Iterative)

You are the architect answering a technical question from %s working on story: %s

**Story Title:** %s
**Story Content:**
%s

**Question:**
%s

## Your Task

Answer the technical question by:
1. Use **list_files** to see what files exist in the coder's workspace
2. Use **read_file** to inspect relevant code files that relate to the question
3. Use **get_diff** to see what changes the coder has made so far
4. Analyze the codebase context to provide an informed answer

**Note:** Your read tools are automatically rooted at %s's workspace (/mnt/coders/%s), so paths are relative to their working directory

## REQUIRED: Submit Your Answer

**You MUST call the submit_reply tool to provide your final answer.** Do not respond with text only.

Call **submit_reply** with your response in this format:
- **response**: Your complete answer as a string

Your answer should:
- Provide a clear, actionable answer to the question
- Reference specific files, functions, or patterns when helpful
- Suggest concrete next steps if applicable

## Available Tools

%s

## Important Notes

- You can explore the coder's workspace at /mnt/coders/%s
- You have read-only access to their files
- Use the tools to understand context before answering
- **Remember: You MUST use submit_reply to send your final answer**

Begin answering the question now.`, coderID, storyID, storyTitle, storyContent, questionPayload.Text, coderID, coderID, toolDocs, coderID)
}
