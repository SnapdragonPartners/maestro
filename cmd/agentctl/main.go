package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/architect"
	"orchestrator/pkg/coder"
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
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	// Parse the command structure first
	switch os.Args[1] {
	case "run":
		handleRunCommand()
	case "architect":
		handleArchitectCommand()
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown command '%s'\n\n", os.Args[1])
		printUsage()
		os.Exit(1)
	}
}

func handleRunCommand() {
	if len(os.Args) < 3 {
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

func handleArchitectCommand() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Error: Expected 'architect <subcommand>' command\n\n")
		printArchitectUsage()
		os.Exit(1)
	}

	subcommand := os.Args[2]
	switch subcommand {
	case "run":
		handleArchitectRun()
	case "list-escalations":
		handleListEscalations()
	default:
		fmt.Fprintf(os.Stderr, "Error: Unknown architect subcommand '%s'\n\n", subcommand)
		printArchitectUsage()
		os.Exit(1)
	}
}

func handleArchitectRun() {
	// Parse flags for architect run command
	var input string
	var workdir string
	var storiesDir string
	var mode string
	var showHelp bool

	flagSet := flag.NewFlagSet("architect-run", flag.ExitOnError)
	flagSet.StringVar(&input, "input", "", "Input specification file (markdown)")
	flagSet.StringVar(&workdir, "workdir", "", "Work directory (default: temp directory)")
	flagSet.StringVar(&storiesDir, "stories", "", "Stories directory (default: workdir/stories)")
	flagSet.StringVar(&mode, "mode", "mock", "Execution mode: mock or live")
	flagSet.BoolVar(&showHelp, "help", false, "Show help for architect run command")

	flagSet.Usage = func() {
		printArchitectRunUsage()
	}

	// Parse flags from position 3 onwards
	if err := flagSet.Parse(os.Args[3:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if showHelp {
		printArchitectRunUsage()
		os.Exit(0)
	}

	// Execute architect run command
	if err := runArchitectWorkflow(input, workdir, storiesDir, mode); err != nil {
		fmt.Fprintf(os.Stderr, "Error running architect workflow: %v\n", err)
		os.Exit(1)
	}
}

func handleListEscalations() {
	// Parse flags for list-escalations command
	var workdir string
	var status string
	var format string
	var showHelp bool

	flagSet := flag.NewFlagSet("list-escalations", flag.ExitOnError)
	flagSet.StringVar(&workdir, "workdir", ".", "Work directory containing logs (default: current directory)")
	flagSet.StringVar(&status, "status", "", "Filter by status: pending, acknowledged, resolved (default: all)")
	flagSet.StringVar(&format, "format", "table", "Output format: table, json (default: table)")
	flagSet.BoolVar(&showHelp, "help", false, "Show help for list-escalations command")

	flagSet.Usage = func() {
		printListEscalationsUsage()
	}

	// Parse flags from position 3 onwards
	if err := flagSet.Parse(os.Args[3:]); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing flags: %v\n", err)
		os.Exit(1)
	}

	if showHelp {
		printListEscalationsUsage()
		os.Exit(0)
	}

	// Execute list escalations command
	if err := listEscalations(workdir, status, format); err != nil {
		fmt.Fprintf(os.Stderr, "Error listing escalations: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "AgentCtl - Standalone Agent Runner\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s run <agent-type> --input <file> [--mode <live|mock>] [--workdir <dir>] [--output <file>]\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect <subcommand> [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Agent Types:\n")
	fmt.Fprintf(os.Stderr, "  architect  - Process development stories and generate tasks\n")
	fmt.Fprintf(os.Stderr, "  claude     - Process coding tasks and generate implementations\n\n")
	fmt.Fprintf(os.Stderr, "Architect Subcommands:\n")
	fmt.Fprintf(os.Stderr, "  list-escalations  - List escalated business questions requiring human intervention\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  %s run architect --input story.md --mode mock\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s run claude --input task.json --mode mock\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --workdir ./logs\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --status pending --format json\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Run Flags:\n")
	fmt.Fprintf(os.Stderr, "  --input string\n        Input file path (JSON for claude, markdown for architect)\n")
	fmt.Fprintf(os.Stderr, "  --output string\n        JSON result output file (default: stdout)\n")
	fmt.Fprintf(os.Stderr, "  --workdir string\n        Agent workspace for generated files (default: temp dir)\n")
	fmt.Fprintf(os.Stderr, "  --mode string\n        Execution mode: live (real API) or mock (fake responses) (default: mock)\n")
	fmt.Fprintf(os.Stderr, "  --help\n        Show help\n")
}

func printArchitectUsage() {
	fmt.Fprintf(os.Stderr, "AgentCtl - Architect Commands\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s architect <subcommand> [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Subcommands:\n")
	fmt.Fprintf(os.Stderr, "  run               - Run architect workflow from spec parsing to completion\n")
	fmt.Fprintf(os.Stderr, "  list-escalations  - List escalated business questions requiring human intervention\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  %s architect run --input spec.md --mode mock\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --status pending\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --format json\n", os.Args[0])
}

func printArchitectRunUsage() {
	fmt.Fprintf(os.Stderr, "AgentCtl - Architect Run\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s architect run [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Flags:\n")
	fmt.Fprintf(os.Stderr, "  --input string\n        Input specification file (markdown) (required)\n")
	fmt.Fprintf(os.Stderr, "  --workdir string\n        Work directory for state and output (default: temp directory)\n")
	fmt.Fprintf(os.Stderr, "  --stories string\n        Stories directory (default: workdir/stories)\n")
	fmt.Fprintf(os.Stderr, "  --mode string\n        Execution mode: mock or live (default: mock)\n")
	fmt.Fprintf(os.Stderr, "  --help\n        Show help for architect run command\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  %s architect run --input project_spec.md\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect run --input spec.md --mode mock --workdir ./project\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect run --input spec.md --mode live --workdir ./work\n", os.Args[0])
}

func printListEscalationsUsage() {
	fmt.Fprintf(os.Stderr, "AgentCtl - List Escalations\n\n")
	fmt.Fprintf(os.Stderr, "Usage:\n")
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations [flags]\n\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "Flags:\n")
	fmt.Fprintf(os.Stderr, "  --workdir string\n        Work directory containing logs (default: current directory)\n")
	fmt.Fprintf(os.Stderr, "  --status string\n        Filter by status: pending, acknowledged, resolved (default: all)\n")
	fmt.Fprintf(os.Stderr, "  --format string\n        Output format: table, json (default: table)\n")
	fmt.Fprintf(os.Stderr, "  --help\n        Show help for list-escalations command\n\n")
	fmt.Fprintf(os.Stderr, "Examples:\n")
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --status pending\n", os.Args[0])
	fmt.Fprintf(os.Stderr, "  %s architect list-escalations --workdir /path/to/logs --format json\n", os.Args[0])
}

// listEscalations implements the list-escalations command
func listEscalations(workdir, status, format string) error {
	// Create architect components to access escalation handler
	queue := architect.NewQueue(workdir + "/stories")
	escalationHandler := architect.NewEscalationHandler(workdir+"/logs", queue)

	// Get escalations filtered by status
	escalations := escalationHandler.GetEscalations(status)

	if len(escalations) == 0 {
		if status != "" {
			fmt.Printf("No escalations found with status: %s\n", status)
		} else {
			fmt.Printf("No escalations found\n")
		}
		return nil
	}

	// Output based on format
	switch format {
	case "json":
		return outputEscalationsJSON(escalations)
	case "table":
		return outputEscalationsTable(escalations)
	default:
		return fmt.Errorf("invalid format '%s', must be 'table' or 'json'", format)
	}
}

// outputEscalationsJSON outputs escalations in JSON format
func outputEscalationsJSON(escalations []*architect.EscalationEntry) error {
	jsonData, err := json.MarshalIndent(escalations, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal escalations to JSON: %w", err)
	}

	fmt.Println(string(jsonData))
	return nil
}

// outputEscalationsTable outputs escalations in table format
func outputEscalationsTable(escalations []*architect.EscalationEntry) error {
	fmt.Printf("ESCALATIONS (%d total)\n\n", len(escalations))
	fmt.Printf("%-15s %-12s %-10s %-10s %-80s\n", "ID", "TYPE", "PRIORITY", "STATUS", "QUESTION")
	fmt.Printf("%-15s %-12s %-10s %-10s %-80s\n",
		strings.Repeat("-", 15),
		strings.Repeat("-", 12),
		strings.Repeat("-", 10),
		strings.Repeat("-", 10),
		strings.Repeat("-", 80))

	for _, escalation := range escalations {
		question := escalation.Question
		if len(question) > 80 {
			question = question[:77] + "..."
		}

		fmt.Printf("%-15s %-12s %-10s %-10s %-80s\n",
			escalation.ID,
			escalation.Type,
			escalation.Priority,
			escalation.Status,
			question,
		)
	}

	fmt.Printf("\nUse --format json for detailed information\n")
	return nil
}

// runArchitectWorkflow implements the architect run command
func runArchitectWorkflow(input, workdir, storiesDir, mode string) error {
	ctx := context.Background()

	// Validate input
	if input == "" {
		return fmt.Errorf("input specification file is required")
	}

	if _, err := os.Stat(input); os.IsNotExist(err) {
		return fmt.Errorf("input file '%s' does not exist", input)
	}

	// Set up directories
	var cleanupWorkDir bool
	if workdir == "" {
		// Create temporary workspace
		tmpDir, err := os.MkdirTemp("", "agentctl-architect-*")
		if err != nil {
			return fmt.Errorf("failed to create temp directory: %w", err)
		}
		workdir = tmpDir
		cleanupWorkDir = true
	} else {
		// Use specified workdir and convert to absolute path
		absWorkDir, err := filepath.Abs(workdir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for workdir %s: %w", workdir, err)
		}
		workdir = absWorkDir
		// Create the directory if it doesn't exist
		if err := os.MkdirAll(workdir, 0755); err != nil {
			return fmt.Errorf("failed to create work directory %s: %w", workdir, err)
		}
		cleanupWorkDir = false // Don't cleanup user-specified directory
	}

	if cleanupWorkDir {
		defer os.RemoveAll(workdir)
	}

	// Set stories directory
	if storiesDir == "" {
		storiesDir = filepath.Join(workdir, "stories")
	}

	// Ensure stories directory exists
	if err := os.MkdirAll(storiesDir, 0755); err != nil {
		return fmt.Errorf("failed to create stories directory %s: %w", storiesDir, err)
	}

	// Create state store for persistence
	stateStore, err := state.NewStore(filepath.Join(workdir, "state"))
	if err != nil {
		return fmt.Errorf("failed to create state store: %w", err)
	}

	// Create architect driver based on mode
	architectID := "agentctl-architect"
	var driver *architect.Driver

	switch mode {
	case "mock":
		driver = architect.NewDriver(architectID, stateStore, workdir, storiesDir)
	case "live":
		// For live mode, we'd need API keys and model configuration
		apiKey := os.Getenv("OPENAI_API_KEY")
		if apiKey == "" {
			return fmt.Errorf("OPENAI_API_KEY environment variable required for live mode")
		}

		// Create model config for live mode
		modelConfig := &config.ModelCfg{
			MaxTokensPerMinute: 1000,
			MaxBudgetPerDayUSD: 10.0,
			APIKey:             apiKey,
			MaxContextTokens:   128000,
			MaxReplyTokens:     4096,
			CompactionBuffer:   2000,
		}

		driver = architect.NewDriverWithO3(architectID, stateStore, modelConfig, apiKey, workdir, storiesDir)
	default:
		return fmt.Errorf("invalid mode '%s', must be 'mock' or 'live'", mode)
	}

	// Initialize the driver
	fmt.Printf("🚀 Initializing architect workflow in %s mode...\n", mode)
	if err := driver.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize architect driver: %w", err)
	}

	// Print initial state
	fmt.Printf("📊 Initial state: %s\n", driver.GetCurrentState())

	// Process the workflow
	fmt.Printf("📝 Processing specification: %s\n", input)
	if err := driver.ProcessWorkflow(ctx, input); err != nil {
		return fmt.Errorf("architect workflow failed: %w", err)
	}

	// Print final results
	fmt.Printf("✅ Architect workflow completed successfully!\n")
	fmt.Printf("📊 Final state: %s\n", driver.GetCurrentState())

	// Show queue summary
	queue := driver.GetQueue()
	summary := queue.GetQueueSummary()
	fmt.Printf("📋 Queue summary: %d total stories, %d ready\n",
		summary["total_stories"], summary["ready_stories"])

	// Show escalation summary if any
	escalationHandler := driver.GetEscalationHandler()
	escalationSummary := escalationHandler.GetEscalationSummary()
	if escalationSummary.TotalEscalations > 0 {
		fmt.Printf("🚨 Escalations: %d total (%d pending, %d resolved)\n",
			escalationSummary.TotalEscalations,
			escalationSummary.PendingEscalations,
			escalationSummary.ResolvedEscalations)
	}

	// Show workspace location
	if !cleanupWorkDir {
		fmt.Printf("📁 Workspace: %s\n", workdir)
		fmt.Printf("📚 Stories: %s\n", storiesDir)
		fmt.Printf("💾 State: %s/state\n", workdir)
		fmt.Printf("📊 Logs: %s/logs\n", workdir)

		// Provide next steps
		fmt.Printf("\n🔧 Next steps:\n")
		fmt.Printf("   • Review generated stories: ls %s/\n", storiesDir)
		fmt.Printf("   • Check escalations: %s architect list-escalations --workdir %s\n", os.Args[0], workdir)
		fmt.Printf("   • Resume workflow: %s architect run --input %s --workdir %s\n", os.Args[0], input, workdir)
	}

	return nil
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
		// Use specified workdir and convert to absolute path
		absWorkDir, err := filepath.Abs(ctl.workdir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for workdir %s: %w", ctl.workdir, err)
		}
		workDir = absWorkDir
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

	// TODO: Update agentctl architect to use Phase 4 architect driver
	return fmt.Errorf("agentctl architect command temporarily disabled - use main orchestrator instead")

	/*
		// Old code commented out - needs update to Phase 4 architect
		if taskCapture.CapturedTask != nil {
			return ctl.outputMessage(taskCapture.CapturedTask)
		}

		return fmt.Errorf("no task was generated by architect")
	*/
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
		// Use specified workdir and convert to absolute path
		absWorkDir, err := filepath.Abs(ctl.workdir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for workdir %s: %w", ctl.workdir, err)
		}
		workDir = absWorkDir
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
			MaxContextTokens: 32000, // Claude 3 Sonnet context limit
			MaxReplyTokens:   4096,  // Conservative default
			CompactionBuffer: 1000,  // Default buffer
		}

		claudeAgent = coder.NewCoderWithClaude(agentID, "standalone-claude", workDir, stateStore, liveModelConfig, apiKey)
	case ModeMock:
		// Use Phase 3 state machine driver for mock mode (mocks LLM, not tools)
		stateStore, err := state.NewStore(filepath.Join(workDir, "state"))
		if err != nil {
			return fmt.Errorf("failed to create state store: %w", err)
		}

		// Create a mock model config for standalone testing
		mockModelConfig := &config.ModelCfg{
			MaxContextTokens: 32000, // Reasonable default
			MaxReplyTokens:   4096,  // Conservative default
			CompactionBuffer: 1000,  // Default buffer
		}

		claudeAgent = coder.NewCoder(agentID, "standalone-claude", workDir, stateStore, mockModelConfig)
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
