package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/agents"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// AgentMode represents the execution mode
type AgentMode string

const (
	ModeLive AgentMode = "live"
	ModeMock AgentMode = "mock"
)

// AgentCtl represents the agent control CLI
type AgentCtl struct {
	agentType string
	input     string
	output    string
	workdir   string
	mode      AgentMode
}

func main() {
	// Check for minimum required arguments
	if len(os.Args) < 3 {
		printUsage()
		os.Exit(1)
	}

	// Parse the command structure first
	if os.Args[1] != "run" {
		fmt.Fprintf(os.Stderr, "Error: Expected 'run <agent-type>' command\n\n")
		printUsage()
		os.Exit(1)
	}

	agentType := os.Args[2]
	if agentType != "architect" && agentType != "claude" {
		fmt.Fprintf(os.Stderr, "Error: Invalid agent type '%s', must be 'architect' or 'claude'\n\n", agentType)
		printUsage()
		os.Exit(1)
	}

	// Now parse the flags from position 3 onwards
	var ctl AgentCtl
	var showHelp bool

	// Create a new flag set to avoid conflicts
	var modeStr string
	flagSet := flag.NewFlagSet("agentctl", flag.ExitOnError)
	flagSet.StringVar(&ctl.input, "input", "", "Input file path (JSON for claude, markdown for architect)")
	flagSet.StringVar(&ctl.output, "output", "", "Output file path (default: stdout)")
	flagSet.StringVar(&ctl.workdir, "workdir", "", "Work directory (default: temp dir)")
	flagSet.StringVar(&modeStr, "mode", "mock", "Execution mode: live or mock")
	flagSet.BoolVar(&showHelp, "help", false, "Show help")

	flagSet.Usage = func() {
		printUsage()
	}

	// Parse flags from position 3 onwards
	if err := flagSet.Parse(os.Args[3:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if showHelp {
		printUsage()
		os.Exit(0)
	}

	ctl.agentType = agentType
	
	// Parse and validate mode
	switch modeStr {
	case "live":
		ctl.mode = ModeLive
	case "mock":
		ctl.mode = ModeMock
	default:
		fmt.Fprintf(os.Stderr, "Error: Invalid mode '%s', must be 'live' or 'mock'\n\n", modeStr)
		printUsage()
		os.Exit(1)
	}

	// Validate inputs
	if err := ctl.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printUsage()
		os.Exit(1)
	}

	// Run the agent
	if err := ctl.run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error running agent: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "AgentCtl - Standalone Agent Runner\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s run <agent-type> --input <file> [--mode <live|mock>] [--workdir <dir>] [--output <file>]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Agent Types:\n")
	fmt.Fprintf(os.Stderr, "  architect  - Process development stories and generate tasks\n")
	fmt.Fprintf(os.Stderr, "  claude     - Process coding tasks and generate implementations\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  %s run architect --input story.md --mode mock\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s run claude --input task.json --mode mock\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s run claude --input task.json --mode live --workdir ./claude-work\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s run claude --input task.json --mode live --workdir ./claude-work --output result.json\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Flags:\n")
	fmt.Fprintf(os.Stderr, "  --input string\n        Input file path (JSON for claude, markdown for architect)\n")
	fmt.Fprintf(os.Stderr, "  --output string\n        JSON result output file (default: stdout)\n")
	fmt.Fprintf(os.Stderr, "  --workdir string\n        Agent workspace for generated files (default: temp dir)\n")
	fmt.Fprintf(os.Stderr, "  --mode string\n        Execution mode: live (real API) or mock (fake responses) (default: mock)\n")
	fmt.Fprintf(os.Stderr, "  --help\n        Show help\n")
}

func (ctl *AgentCtl) validate() error {
	// Validate agent type
	if ctl.agentType != "architect" && ctl.agentType != "claude" {
		return fmt.Errorf("invalid agent type '%s', must be 'architect' or 'claude'", ctl.agentType)
	}

	// Validate input file
	if ctl.input == "" {
		return fmt.Errorf("input file is required")
	}

	if _, err := os.Stat(ctl.input); os.IsNotExist(err) {
		return fmt.Errorf("input file '%s' does not exist", ctl.input)
	}

	// Mode is already validated and set above

	return nil
}

func (ctl *AgentCtl) run() error {
	ctx := context.Background()

	switch ctl.agentType {
	case "architect":
		return ctl.runArchitect(ctx)
	case "claude":
		return ctl.runClaude(ctx)
	default:
		return fmt.Errorf("unsupported agent type: %s", ctl.agentType)
	}
}

func (ctl *AgentCtl) runArchitect(ctx context.Context) error {
	// Determine workspace directory
	var workDir string
	var cleanupWorkDir bool
	
	if ctl.workdir != "" {
		// Use specified workdir
		workDir = ctl.workdir
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return fmt.Errorf("failed to create work directory %s: %w", workDir, err)
		}
		cleanupWorkDir = false // Don't cleanup user-specified directory
	} else {
		// Create temporary workspace
		tmpDir, err := os.MkdirTemp("", "agentctl-architect-*")
		if err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		workDir = tmpDir
		cleanupWorkDir = true
	}
	
	if cleanupWorkDir {
		defer os.RemoveAll(workDir)
	}

	// Create architect agent with a custom task capturer
	agentID := "agentctl-architect"
	targetCoder := "agentctl-claude" // Default target for generated tasks
	
	architect := agents.NewArchitectAgent(agentID, "standalone-architect", filepath.Dir(ctl.input), workDir, targetCoder)
	
	// Create a task capture dispatcher for standalone mode
	taskCapture := &TaskCaptureDispatcher{}
	architect.SetDispatcher(taskCapture)

	// Read the story file 
	storyContent, err := os.ReadFile(ctl.input)
	if err != nil {
		return fmt.Errorf("failed to read story file: %w", err)
	}

	// Extract story ID from filename (e.g., "001.md" -> "001")
	storyID := filepath.Base(ctl.input)
	if ext := filepath.Ext(storyID); ext != "" {
		storyID = storyID[:len(storyID)-len(ext)]
	}

	// Create input message
	inputMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "agentctl", agentID)
	inputMsg.SetPayload("story_id", storyID)
	inputMsg.SetMetadata("story_content", string(storyContent))
	inputMsg.SetMetadata("cli_mode", "standalone")

	// Process the message
	_, err = architect.ProcessMessage(ctx, inputMsg)
	if err != nil {
		return fmt.Errorf("architect failed to process message: %w", err)
	}

	// Output the captured TASK message
	if taskCapture.CapturedTask != nil {
		return ctl.outputMessage(taskCapture.CapturedTask)
	}

	return fmt.Errorf("no task was generated by architect")
}

// TaskCaptureDispatcher captures the TASK message for standalone architect runs
type TaskCaptureDispatcher struct {
	CapturedTask *proto.AgentMsg
}

func (t *TaskCaptureDispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	if msg.Type == proto.MsgTypeTASK {
		t.CapturedTask = msg
	}
	return nil
}

func (ctl *AgentCtl) runClaude(ctx context.Context) error {
	// Determine workspace directory
	var workDir string
	var cleanupWorkDir bool
	
	if ctl.workdir != "" {
		// Use specified workdir
		workDir = ctl.workdir
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(workDir, 0755); err != nil {
			return fmt.Errorf("failed to create work directory %s: %w", workDir, err)
		}
		cleanupWorkDir = false // Don't cleanup user-specified directory
	} else {
		// Create temporary workspace
		tmpDir, err := os.MkdirTemp("", "agentctl-claude-*")
		if err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		workDir = tmpDir
		cleanupWorkDir = true
	}
	
	if cleanupWorkDir {
		defer os.RemoveAll(workDir)
	}

	// Read input message
	inputData, err := os.ReadFile(ctl.input)
	if err != nil {
		return fmt.Errorf("failed to read input file: %w", err)
	}

	// Parse the AgentMsg
	var inputMsg proto.AgentMsg
	if err := json.Unmarshal(inputData, &inputMsg); err != nil {
		return fmt.Errorf("failed to parse input JSON: %w", err)
	}

	// Create Claude agent based on mode
	agentID := "agentctl-claude"
	var claudeAgent dispatch.Agent

	switch ctl.mode {
	case ModeLive:
		// Use Phase 3 state machine with live LLM integration
		apiKey := os.Getenv("ANTHROPIC_API_KEY")
		if apiKey == "" {
			return fmt.Errorf("ANTHROPIC_API_KEY environment variable required for live mode")
		}
		
		stateStore, err := state.NewStore(filepath.Join(workDir, "state"))
		if err != nil {
			return fmt.Errorf("failed to create state store: %w", err)
		}
		
		// Create model config for live mode
		liveModelConfig := &config.ModelCfg{
			MaxContextTokens: 32000,  // Claude 3 Sonnet context limit
			MaxReplyTokens:   4096,   // Conservative default
			CompactionBuffer: 1000,   // Default buffer
		}
		
		claudeAgent = agents.NewLiveDriverBasedAgent(agentID, "standalone-claude", workDir, stateStore, liveModelConfig, apiKey)
	case ModeMock:
		// Use Phase 3 state machine driver for mock mode (mocks LLM, not tools)
		stateStore, err := state.NewStore(filepath.Join(workDir, "state"))
		if err != nil {
			return fmt.Errorf("failed to create state store: %w", err)
		}
		
		// Create a mock model config for standalone testing
		mockModelConfig := &config.ModelCfg{
			MaxContextTokens: 32000,  // Reasonable default
			MaxReplyTokens:   4096,   // Conservative default
			CompactionBuffer: 1000,   // Default buffer
		}
		
		claudeAgent = agents.NewDriverBasedAgent(agentID, "standalone-claude", workDir, stateStore, mockModelConfig)
	}

	// Update the message routing for standalone mode
	inputMsg.ToAgent = agentID
	inputMsg.FromAgent = "agentctl"

	// Process the message
	resultMsg, err := claudeAgent.ProcessMessage(ctx, &inputMsg)
	if err != nil {
		return fmt.Errorf("claude failed to process message: %w", err)
	}

	// Output the result
	return ctl.outputMessage(resultMsg)
}

func (ctl *AgentCtl) outputMessage(msg *proto.AgentMsg) error {
	// Convert to JSON
	jsonData, err := json.MarshalIndent(msg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal result to JSON: %w", err)
	}

	// Output to file or stdout
	if ctl.output != "" {
		err = os.WriteFile(ctl.output, jsonData, 0644)
		if err != nil {
			return fmt.Errorf("failed to write output file: %w", err)
		}
		fmt.Printf("Result written to %s\n", ctl.output)
	} else {
		fmt.Println(string(jsonData))
	}

	return nil
}