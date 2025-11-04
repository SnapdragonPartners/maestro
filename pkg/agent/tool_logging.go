package agent

import (
	"encoding/json"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
)

// LogToolExecution logs a tool execution to the database for debugging and analysis.
// This is a fire-and-forget operation - failures are logged but don't affect tool execution.
//
// Parameters:
//   - toolCall: The tool call being executed
//   - result: The result returned from tool execution (typically map[string]any)
//   - execErr: Error from tool execution (nil if successful)
//   - duration: How long the tool took to execute
//   - agentID: ID of the agent executing the tool
//   - storyID: Optional story ID context (empty string if not applicable)
//   - persistenceChannel: Channel for sending persistence requests
func LogToolExecution(
	toolCall *ToolCall,
	result any,
	execErr error,
	duration time.Duration,
	agentID string,
	storyID string,
	persistenceChannel chan<- *persistence.Request,
) {
	if persistenceChannel == nil || toolCall == nil {
		return // No persistence channel or tool call configured
	}

	// Get config for session ID
	cfg, err := config.GetConfig()
	if err != nil {
		// Can't use logger here since this is a shared utility, just return silently
		return
	}

	// Marshal parameters to JSON
	var paramsJSON string
	if toolCall.Parameters != nil {
		if jsonBytes, err := json.Marshal(toolCall.Parameters); err == nil {
			paramsJSON = string(jsonBytes)
		}
	}

	// Extract result data based on tool type
	var exitCode *int
	var success *bool
	var stdout, stderr, errorMsg string

	if execErr != nil {
		errorMsg = execErr.Error()
		successVal := false
		success = &successVal
	} else {
		successVal := true
		success = &successVal
	}

	// Extract shell-specific data from result map
	if resultMap, ok := result.(map[string]any); ok {
		if exitCodeVal, ok := resultMap["exit_code"].(int); ok {
			exitCode = &exitCodeVal
		}
		if stdoutVal, ok := resultMap["stdout"].(string); ok {
			stdout = stdoutVal
		}
		if stderrVal, ok := resultMap["stderr"].(string); ok {
			stderr = stderrVal
		}
		if errorVal, ok := resultMap["error"].(string); ok && errorVal != "" {
			errorMsg = errorVal
		}
		// Also check for generic success flag from result
		if successVal, ok := resultMap["success"].(bool); ok {
			success = &successVal
		}
	}

	// Create tool execution record
	durationMS := duration.Milliseconds()
	toolExec := &persistence.ToolExecution{
		SessionID:  cfg.SessionID,
		AgentID:    agentID,
		StoryID:    storyID,
		ToolName:   toolCall.Name,
		ToolID:     toolCall.ID,
		Params:     paramsJSON,
		ExitCode:   exitCode,
		Success:    success,
		Stdout:     stdout,
		Stderr:     stderr,
		Error:      errorMsg,
		DurationMS: &durationMS,
		CreatedAt:  time.Now(),
	}

	// Send to persistence worker (fire-and-forget)
	persistence.PersistToolExecution(toolExec, persistenceChannel)
}
