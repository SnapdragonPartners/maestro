package tools

import (
	"context"
	"fmt"
	"time"
)

// MaintenanceItem represents an issue logged by the architect during reviews.
//
//nolint:govet // fieldalignment: field order matches spec (Description, Priority, Source, AddedAt)
type MaintenanceItem struct {
	Description string    // Free-text from the architect LLM
	Priority    string    // "p1" (urgent), "p2" (normal), "p3" (nice-to-have)
	Source      string    // Auto-set from context, e.g. "coder-001:bfe2c3c5"
	AddedAt     time.Time // Auto-set to time.Now()
}

// MaintenanceLog is the interface for accumulating maintenance items.
// Implemented by the architect Driver.
type MaintenanceLog interface {
	AddMaintenanceItem(item MaintenanceItem)
}

// AddMaintenanceItemTool logs an issue for the next maintenance cycle.
// Non-terminal tool — the architect calls it mid-review and keeps reviewing.
type AddMaintenanceItemTool struct {
	log     MaintenanceLog
	agentID string // Current agent being reviewed (for source)
	storyID string // Current story being reviewed (for source)
}

// NewAddMaintenanceItemTool creates a new add_maintenance_item tool.
func NewAddMaintenanceItemTool(log MaintenanceLog, agentID, storyID string) *AddMaintenanceItemTool {
	return &AddMaintenanceItemTool{
		log:     log,
		agentID: agentID,
		storyID: storyID,
	}
}

// Name returns the tool name.
func (t *AddMaintenanceItemTool) Name() string {
	return ToolAddMaintenanceItem
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *AddMaintenanceItemTool) PromptDocumentation() string {
	return `- **add_maintenance_item** - Log an issue for the next maintenance cycle
  - Parameters:
    - description (string, REQUIRED): What needs attention
    - priority (string, REQUIRED): p1=urgent, p2=normal, p3=nice-to-have
  - Non-terminal: call this during review, then continue with your review decision`
}

// Definition returns the tool definition for LLM.
func (t *AddMaintenanceItemTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolAddMaintenanceItem,
		Description: "Log an issue for the next maintenance cycle. Non-terminal — call this during review, then continue with your review decision.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"description": {
					Type:        "string",
					Description: "What needs attention",
				},
				"priority": {
					Type:        "string",
					Description: "p1=urgent, p2=normal, p3=nice-to-have",
					Enum:        []string{"p1", "p2", "p3"},
				},
			},
			Required: []string{"description", "priority"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *AddMaintenanceItemTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	description, ok := args["description"].(string)
	if !ok || description == "" {
		return nil, fmt.Errorf("description is required and must be a non-empty string")
	}

	priority, ok := args["priority"].(string)
	if !ok || priority == "" {
		return nil, fmt.Errorf("priority is required and must be a non-empty string")
	}

	validPriorities := map[string]bool{"p1": true, "p2": true, "p3": true}
	if !validPriorities[priority] {
		return nil, fmt.Errorf("priority must be one of: p1, p2, p3 (got: %s)", priority)
	}

	// Build source from context
	source := t.agentID
	if t.storyID != "" {
		source = fmt.Sprintf("%s:%s", t.agentID, t.storyID)
	}

	item := MaintenanceItem{
		Description: description,
		Priority:    priority,
		Source:      source,
		AddedAt:     time.Now(),
	}

	if t.log == nil {
		return &ExecResult{
			Content: fmt.Sprintf("Maintenance item not persisted (MaintenanceLog not configured, priority: %s). Continue with your review.", priority),
		}, nil
	}

	t.log.AddMaintenanceItem(item)

	return &ExecResult{
		Content: fmt.Sprintf("Maintenance item logged (priority: %s). Continue with your review.", priority),
	}, nil
}
