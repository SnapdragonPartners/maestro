package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// generateQuestionPrompt creates a concise user message for technical questions.
// Context (story, role, tools) is already in the system prompt.
func (d *Driver) generateQuestionPrompt(requestMsg *proto.AgentMsg, questionPayload *proto.QuestionRequestPayload, coderID string, toolProvider *tools.ToolProvider) string {
	_ = requestMsg   // context already in system prompt
	_ = coderID      // context already in system prompt
	_ = toolProvider // tools already documented in system prompt

	return fmt.Sprintf(`The coder has a technical question:

%s

Please explore their workspace, analyze the code, and provide a clear answer using submit_reply.`, questionPayload.Text)
}
