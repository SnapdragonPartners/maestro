package logx

import (
	"fmt"
	"testing"
)

func ExampleLogger_orchestrator_usage() {
	// Example of how the orchestrator might use the logger.
	fmt.Println("=== Orchestrator Logging Demo ===")

	// Main orchestrator logger.
	orchestrator := NewLogger("orchestrator")
	orchestrator.Info("Starting orchestrator")
	orchestrator.Debug("Loading configuration from %s", "config/config.json")

	// Agent loggers.
	architect := NewLogger("architect")
	claude := NewLogger("claude")
	o3 := NewLogger("o3")

	// Simulate agent workflow.
	architect.Info("Processing story: %s", "Implement health endpoint")
	architect.Debug("Analyzing requirements")

	claude.Info("Received task from architect")
	claude.Warn("High complexity detected - estimated %d tokens", 800)

	o3.Info("Reviewing code implementation")
	o3.Error("Code review failed: missing error handling")

	// Agent can create sub-loggers for different operations.
	claudeValidator := claude.WithAgentID("claude-validator")
	claudeValidator.Info("Running validation tests")

	// Shutdown sequence.
	orchestrator.Info("Initiating graceful shutdown")
	architect.Info("Finishing current analysis")
	claude.Info("Completing active tasks")
	o3.Info("Finalizing reviews")
	orchestrator.Info("All agents stopped successfully")

	fmt.Println("=== End Demo ===")
}

func TestOrchestratorUsage(t *testing.T) {
	ExampleLogger_orchestrator_usage()
}
