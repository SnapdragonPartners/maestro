package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/tools"
)

// HarnessResult wraps the tool result with additional metadata for testing.
//
//nolint:govet // fieldalignment: prefer logical grouping over memory optimization
type HarnessResult struct {
	Success   bool        `json:"success"`
	ToolName  string      `json:"tool_name"`
	Duration  string      `json:"duration"`
	Result    interface{} `json:"result,omitempty"`
	Error     string      `json:"error,omitempty"`
	Arguments interface{} `json:"arguments"`
}

// createToolByName creates a tool instance by name using constants from pkg/tools.
func createToolByName(toolName string) (tools.Tool, error) {
	switch toolName {
	case tools.ToolContainerBuild:
		return tools.NewContainerBuildTool(nil), nil
	case tools.ToolContainerUpdate:
		return tools.NewContainerUpdateTool(nil), nil
	case tools.ToolContainerTest:
		return tools.NewContainerTestTool(nil), nil
	case tools.ToolContainerList:
		return tools.NewContainerListTool(nil), nil
	case tools.ToolAskQuestion:
		return tools.NewAskQuestionTool(), nil
	case tools.ToolSubmitPlan:
		return tools.NewSubmitPlanTool(), nil
	case tools.ToolMarkStoryComplete:
		return tools.NewMarkStoryCompleteTool(), nil
	case tools.ToolDone:
		return tools.NewDoneTool(), nil
	// Add more tools as needed - CreateMakefile doesn't have a constant yet
	default:
		return nil, fmt.Errorf("unknown tool: %s", toolName)
	}
}

func main() {
	if len(os.Args) < 3 {
		log.Fatalf("Usage: %s <tool_name> <args_json>", os.Args[0])
	}

	toolName := os.Args[1]
	argsJSON := os.Args[2]

	// Initialize config system with a temporary directory
	configDir := "/tmp/test-config"
	if err := os.MkdirAll(configDir+"/.maestro", 0755); err != nil {
		log.Printf("Warning: failed to create config directory: %v", err)
	}

	if err := config.LoadConfig(configDir); err != nil {
		log.Printf("Warning: LoadConfig failed: %v - continuing with defaults", err)
	}

	// Parse arguments
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		result := HarnessResult{
			Success:   false,
			Error:     fmt.Sprintf("failed to parse arguments JSON: %v", err),
			ToolName:  toolName,
			Arguments: argsJSON,
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		os.Exit(1)
	}

	// Create tool instance
	tool, err := createToolByName(toolName)
	if err != nil {
		result := HarnessResult{
			Success:   false,
			Error:     fmt.Sprintf("failed to create tool: %v", err),
			ToolName:  toolName,
			Arguments: args,
		}
		_ = json.NewEncoder(os.Stdout).Encode(result)
		os.Exit(1)
	}

	// Execute tool with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	startTime := time.Now()
	toolResult, err := tool.Exec(ctx, args)
	duration := time.Since(startTime)

	// Prepare harness result
	result := HarnessResult{
		ToolName:  toolName,
		Duration:  duration.String(),
		Arguments: args,
	}

	if err != nil {
		result.Success = false
		result.Error = err.Error()
	} else {
		result.Success = true
		result.Result = toolResult
	}

	// Output result as JSON
	if encodeErr := json.NewEncoder(os.Stdout).Encode(result); encodeErr != nil {
		cancel()
		log.Printf("failed to encode result: %v", encodeErr)
		//nolint:gocritic // exitAfterDefer: intentional exit after cleanup
		os.Exit(1)
	}

	if !result.Success {
		os.Exit(1)
	}
}
