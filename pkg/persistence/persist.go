package persistence

import (
	"time"
)

// PersistStory persists a single story to the database with all available data.
// This is a fire-and-forget operation that sends the story to the persistence worker.
func PersistStory(story *Story, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || story == nil {
		return
	}

	// Send to persistence worker (fire-and-forget)
	persistenceChannel <- &Request{
		Operation: OpUpsertStory,
		Data:      story,
		Response:  nil, // Fire-and-forget
	}
}

// PersistStoryStatus persists a story status update with optional metrics data.
// This is used when completing stories and includes token/cost information.
func PersistStoryStatus(storyID string, status string, timestamp time.Time,
	promptTokens, completionTokens *int64, costUSD *float64,
	persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || storyID == "" {
		return
	}

	statusReq := &UpdateStoryStatusRequest{
		StoryID:          storyID,
		Status:           status,
		Timestamp:        timestamp,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		CostUSD:          costUSD,
	}

	persistenceChannel <- &Request{
		Operation: OpUpdateStoryStatus,
		Data:      statusReq,
		Response:  nil, // Fire-and-forget
	}
}

// PersistSpec persists a single spec to the database.
func PersistSpec(spec *Spec, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || spec == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}
}

// PersistAgentPlan persists a single agent plan to the database.
func PersistAgentPlan(plan *AgentPlan, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || plan == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertAgentPlan,
		Data:      plan,
		Response:  nil, // Fire-and-forget
	}
}

// PersistAgentResponse persists a single agent response to the database.
func PersistAgentResponse(response *AgentResponse, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || response == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpUpsertAgentResponse,
		Data:      response,
		Response:  nil, // Fire-and-forget
	}
}

// PersistDependency persists a single story dependency to the database.
func PersistDependency(storyID, dependsOnID string, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || storyID == "" || dependsOnID == "" {
		return
	}

	dependency := &StoryDependency{
		StoryID:   storyID,
		DependsOn: dependsOnID,
	}

	persistenceChannel <- &Request{
		Operation: OpAddStoryDependency,
		Data:      dependency,
		Response:  nil, // Fire-and-forget
	}
}

// PersistToolExecution persists a tool execution record for debugging and analysis.
func PersistToolExecution(toolExec *ToolExecution, persistenceChannel chan<- *Request) {
	if persistenceChannel == nil || toolExec == nil {
		return
	}

	persistenceChannel <- &Request{
		Operation: OpInsertToolExecution,
		Data:      toolExec,
		Response:  nil, // Fire-and-forget
	}
}
