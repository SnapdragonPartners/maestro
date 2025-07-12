package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/webui"
)

// Orchestrator manages the multi-agent system
type Orchestrator struct {
	config       *config.Config
	dispatcher   *dispatch.Dispatcher
	rateLimiter  *limiter.Limiter
	eventLog     *eventlog.Writer
	logger       *logx.Logger
	agents       map[string]StatusAgent
	shutdownTime time.Duration
	architect    *architect.Driver // Phase 4: Architect driver for spec processing
	webServer    *webui.Server     // Web UI server
	workDir      string            // Working directory for this run

	// Agents handle their own state machines
}

// StatusAgent interface for agents that can generate status reports
type StatusAgent interface {
	dispatch.Agent
	GenerateStatus() (string, error)
}

// LLMClientAdapter adapts agent.LLMClient to architect.LLMClient interface
type LLMClientAdapter struct {
	client agent.LLMClient
}

func (a *LLMClientAdapter) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	req := agent.CompletionRequest{
		Messages: []agent.CompletionMessage{
			{Role: agent.RoleUser, Content: prompt},
		},
		MaxTokens:   2000,
		Temperature: 0.7,
	}

	resp, err := a.client.Complete(ctx, req)
	if err != nil {
		return "", err
	}

	return resp.Content, nil
}

// ArchitectMessageAdapter removed - architect driver is now registered directly with dispatcher

func main() {
	fmt.Println("orchestrator boot")

	var configPath string
	var specPath string
	var liveMode bool
	var uiMode bool
	var workDir string
	flag.StringVar(&configPath, "config", "", "Path to config file")
	flag.StringVar(&specPath, "spec", "", "Path to specification file (markdown)")
	flag.StringVar(&workDir, "workdir", "", "Working directory (default: current directory)")
	flag.BoolVar(&liveMode, "live", false, "Use live API calls instead of mock mode")
	flag.BoolVar(&uiMode, "ui", false, "Start web UI server on port 8080")
	flag.Parse()

	// Use CONFIG_PATH env var if flag not provided
	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}

	// Default to config/config.json
	if configPath == "" {
		configPath = "config/config.json"
	}

	// Set working directory
	if workDir == "" {
		workDir, _ = os.Getwd() // Use current directory as default
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Config loaded successfully (no need to print it)
	_ = cfg

	// Create orchestrator
	orchestrator, err := NewOrchestrator(cfg, workDir)
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Start orchestrator
	ctx := context.Background()
	if err := orchestrator.Start(ctx, liveMode); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Start web UI if requested
	if uiMode {
		if err := orchestrator.StartWebUI(ctx, 8080); err != nil {
			log.Fatalf("Failed to start web UI: %v", err)
		}

		// Open browser
		if err := openBrowser("http://localhost:8080"); err != nil {
			log.Printf("Failed to open browser: %v", err)
			fmt.Println("🌐 Web UI available at: http://localhost:8080")
		}
	}

	// If spec file provided, send SPEC message to dispatcher
	if specPath != "" {
		if err := orchestrator.SendSpecification(ctx, specPath); err != nil {
			log.Fatalf("Failed to send specification: %v", err)
		}
	}

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for signal
	sig := <-sigChan
	orchestrator.logger.Info("Received signal %v, initiating graceful shutdown", sig)

	// Perform graceful shutdown
	shutdownCtx, cancel := context.WithTimeout(ctx, orchestrator.shutdownTime)
	defer cancel()

	if err := orchestrator.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during shutdown: %v", err)
		os.Exit(1)
	}

	orchestrator.logger.Info("Orchestrator shutdown completed successfully")
}

// NewOrchestrator creates a new orchestrator instance
func NewOrchestrator(cfg *config.Config, workDir string) (*Orchestrator, error) {
	logger := logx.NewLogger("orchestrator")

	// Create rate limiter
	rateLimiter := limiter.NewLimiter(cfg)

	// Create event log
	eventLog, err := eventlog.NewWriter("logs", 24)
	if err != nil {
		return nil, fmt.Errorf("failed to create event log: %w", err)
	}

	// No global state store - each agent manages its own state

	// Create dispatcher
	dispatcher, err := dispatch.NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		return nil, fmt.Errorf("failed to create dispatcher: %w", err)
	}

	// Set default shutdown timeout
	shutdownTime := 30 * time.Second
	if cfg.GracefulShutdownTimeoutSec > 0 {
		shutdownTime = time.Duration(cfg.GracefulShutdownTimeoutSec) * time.Second
	}

	return &Orchestrator{
		config:       cfg,
		dispatcher:   dispatcher,
		rateLimiter:  rateLimiter,
		eventLog:     eventLog,
		logger:       logger,
		agents:       make(map[string]StatusAgent),
		shutdownTime: shutdownTime,
		architect:    nil, // Will be set during agent creation
		workDir:      workDir,
	}, nil
}

// Start initializes and starts the orchestrator
func (o *Orchestrator) Start(ctx context.Context, liveMode bool) error {
	o.logger.Info("Starting orchestrator")

	// Start dispatcher
	if err := o.dispatcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// Create and register agents
	if err := o.createAgents(liveMode); err != nil {
		return fmt.Errorf("failed to create agents: %w", err)
	}

	// Agents will handle their own polling and state machines

	o.logger.Info("Orchestrator started successfully")
	return nil
}

// Shutdown performs graceful shutdown of the orchestrator
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("Starting graceful shutdown")

	// Step 1: Agents handle their own shutdown via SHUTDOWN messages

	// Step 2: Broadcast SHUTDOWN message to all agents
	if err := o.broadcastShutdown(); err != nil {
		o.logger.Error("Failed to broadcast shutdown: %v", err)
	}

	// Step 3: Wait for agents to complete current work
	o.logger.Info("Waiting %v for agents to complete work", o.shutdownTime)
	time.Sleep(o.shutdownTime / 4) // Give agents time to process shutdown

	// Step 4: Collect status from all agents and generate STATUS.md files
	if err := o.collectAgentStatus(); err != nil {
		o.logger.Error("Failed to collect agent status: %v", err)
	}

	// Step 5: Stop dispatcher
	if err := o.dispatcher.Stop(ctx); err != nil {
		o.logger.Error("Failed to stop dispatcher: %v", err)
	}

	// Step 6: Close other resources
	o.rateLimiter.Close()

	if err := o.eventLog.Close(); err != nil {
		o.logger.Error("Failed to close event log: %v", err)
	}

	o.logger.Info("Graceful shutdown completed")
	return nil
}

// SendSpecification sends a SPEC message to the dispatcher for the architect to process
func (o *Orchestrator) SendSpecification(ctx context.Context, specFile string) error {
	o.logger.Info("Sending specification file to architect: %s", specFile)
	fmt.Printf("🚀 Sending specification to architect...\n")

	// Create SPEC message for the architect
	specMsg := proto.NewAgentMsg(proto.MsgTypeSPEC, "orchestrator", "architect")
	specMsg.SetPayload("spec_file", specFile)
	specMsg.SetPayload("type", "spec_file")
	specMsg.SetMetadata("source", "command_line")

	// Send via dispatcher
	if err := o.dispatcher.DispatchMessage(specMsg); err != nil {
		return fmt.Errorf("failed to dispatch specification message: %w", err)
	}

	fmt.Printf("✅ Specification sent to architect via dispatcher\n")
	return nil
}

func (o *Orchestrator) createAgents(liveMode bool) error {
	// Create agents from configuration
	allAgents := o.config.GetAllAgents()

	// First pass: find the coder agent ID for architects to target
	var coderAgentID string
	for _, agentWithModel := range allAgents {
		if agentWithModel.Agent.Type == "coder" {
			coderAgentID = agentWithModel.Agent.GetLogID(agentWithModel.ModelName)
			break
		}
	}

	if coderAgentID == "" {
		return fmt.Errorf("no coder agent found in configuration")
	}

	for _, agentWithModel := range allAgents {
		agentConfig := agentWithModel.Agent
		modelName := agentWithModel.ModelName

		// Generate log ID for this agent
		logID := agentConfig.GetLogID(modelName)

		var registeredAgent dispatch.Agent

		switch agentConfig.Type {
		case "architect":
			// Store architect driver for spec processing with live O3 LLM
			// The architect runs workflows AND receives messages from coding agents

			// Use command-line workDir instead of config workdir for agent isolation
			agentWorkDir := filepath.Join(o.workDir, logID)
			architectStatePath := filepath.Join(agentWorkDir, "state")
			o.logger.Info("Creating architect state store at: %s", architectStatePath)
			architectStateStore, err := state.NewStore(architectStatePath)
			if err != nil {
				return fmt.Errorf("failed to create architect state store: %w", err)
			}

			baseLLMClient := agent.NewO3ClientWithModel(agentWithModel.Model.APIKey, "o3-mini")
			llmClient := &LLMClientAdapter{client: baseLLMClient}
			o.architect = architect.NewDriver(logID, architectStateStore, &agentWithModel.Model, llmClient, o.dispatcher, agentWorkDir, filepath.Join(agentWorkDir, "stories"))

			// Initialize the architect driver
			if err := o.architect.Initialize(context.Background()); err != nil {
				return fmt.Errorf("failed to initialize architect: %w", err)
			}

			// Register the architect driver directly with dispatcher
			// Note: Run() will be started after dispatcher.Attach() sets up channels
			registeredAgent = o.architect
			o.logger.Info("Created architect driver with live O3 LLM: %s", logID)

		case "coder":
			// Use command-line workDir instead of config workdir for agent isolation
			agentWorkDir := filepath.Join(o.workDir, logID)
			agentStatePath := filepath.Join(agentWorkDir, "state")
			o.logger.Info("Creating coder state store at: %s", agentStatePath)
			agentStateStore, err := state.NewStore(agentStatePath)
			if err != nil {
				return fmt.Errorf("failed to create state store for agent %s: %w", logID, err)
			}

			// Check if we should use live API calls (command line flag)
			if liveMode {
				// Use unified coder with Claude LLM integration
				liveAgent, err := coder.NewCoderWithClaude(logID, agentConfig.Name, agentWorkDir, agentStateStore, &agentWithModel.Model, agentWithModel.Model.APIKey)
				if err != nil {
					return fmt.Errorf("failed to create live coder agent %s: %w", logID, err)
				}

				// Initialize and start the coder's state machine
				if err := liveAgent.Initialize(context.Background()); err != nil {
					return fmt.Errorf("failed to initialize coder %s: %w", logID, err)
				}

				go func() {
					if err := liveAgent.Run(context.Background()); err != nil {
						o.logger.Error("Coder %s state machine error: %v", logID, err)
					}
				}()

				registeredAgent = liveAgent
			} else {
				// Use unified coder with core state machine (mock mode)
				driverAgent, err := coder.NewCoder(logID, agentStateStore, &agentWithModel.Model, nil, agentWorkDir, &agentConfig)
				if err != nil {
					return fmt.Errorf("failed to create coder agent %s: %w", logID, err)
				}

				// Initialize and start the coder's state machine
				if err := driverAgent.Initialize(context.Background()); err != nil {
					return fmt.Errorf("failed to initialize coder %s: %w", logID, err)
				}

				go func() {
					if err := driverAgent.Run(context.Background()); err != nil {
						o.logger.Error("Coder %s state machine error: %v", logID, err)
					}
				}()

				registeredAgent = driverAgent
			}

		default:
			return fmt.Errorf("unknown agent type: %s", agentConfig.Type)
		}

		// Attach agent to dispatcher using new channel-based API
		o.dispatcher.Attach(registeredAgent)

		// Start architect's state machine after channels are set up
		if agentConfig.Type == "architect" {
			go func() {
				if err := registeredAgent.(*architect.Driver).Run(context.Background()); err != nil {
					o.logger.Error("Architect state machine error: %v", err)
				}
			}()
		}

		o.agents[logID] = &StatusAgentWrapper{Agent: registeredAgent}
		o.logger.Info("Created and registered agent: %s (%s) using model %s", logID, agentConfig.Type, modelName)
	}

	o.logger.Info("Created and registered %d agents total", len(o.agents))
	return nil
}

func (o *Orchestrator) broadcastShutdown() error {
	o.logger.Info("Broadcasting SHUTDOWN message to all agents")

	for agentID := range o.agents {
		shutdownMsg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", agentID)
		shutdownMsg.SetPayload("reason", "Graceful shutdown requested")
		shutdownMsg.SetPayload("timeout", o.shutdownTime.String())
		shutdownMsg.SetMetadata("shutdown_type", "graceful")

		if err := o.dispatcher.DispatchMessage(shutdownMsg); err != nil {
			o.logger.Error("Failed to send shutdown to agent %s: %v", agentID, err)
			continue
		}

		o.logger.Debug("Sent shutdown message to agent %s", agentID)
	}

	return nil
}

func (o *Orchestrator) collectAgentStatus() error {
	o.logger.Info("Collecting status from all agents")

	// Create status directory if it doesn't exist
	if err := os.MkdirAll("status", 0755); err != nil {
		return fmt.Errorf("failed to create status directory: %w", err)
	}

	for agentID, agent := range o.agents {
		o.logger.Debug("Collecting status from agent %s", agentID)

		status, err := agent.GenerateStatus()
		if err != nil {
			o.logger.Error("Failed to get status from agent %s: %v", agentID, err)
			// Continue with other agents
			continue
		}

		// Write STATUS.md file for this agent
		statusFile := fmt.Sprintf("status/%s-STATUS.md", agentID)
		if err := os.WriteFile(statusFile, []byte(status), 0644); err != nil {
			o.logger.Error("Failed to write status file for agent %s: %v", agentID, err)
			continue
		}

		o.logger.Info("Generated status file: %s", statusFile)
	}

	// Generate overall orchestrator status
	orchestratorStatus := o.generateOrchestratorStatus()
	if err := os.WriteFile("status/orchestrator-STATUS.md", []byte(orchestratorStatus), 0644); err != nil {
		o.logger.Error("Failed to write orchestrator status file: %v", err)
	}

	return nil
}

func (o *Orchestrator) generateOrchestratorStatus() string {
	stats := o.dispatcher.GetStats()

	status := fmt.Sprintf(`# Orchestrator Status Report

Generated: %s

## Configuration
- Config loaded: %t
- Graceful shutdown timeout: %v

## Dispatcher Status
- Running: %v
- Queue Length: %v
- Queue Capacity: %v
- Registered Agents: %v

## Rate Limiter Status
`, time.Now().Format(time.RFC3339),
		o.config != nil,
		o.shutdownTime,
		stats["running"],
		stats["queue_length"],
		stats["queue_capacity"],
		stats["agents"])

	// Add rate limiter status for each model
	for model := range o.config.Models {
		tokens, budget, agents, err := o.rateLimiter.GetStatus(model)
		if err != nil {
			status += fmt.Sprintf("- %s: Error getting status: %v\n", model, err)
		} else {
			status += fmt.Sprintf("- %s: %d tokens, $%.2f budget, %d agents\n", model, tokens, budget, agents)
		}
	}

	status += fmt.Sprintf(`
## Event Log
- Log directory: logs/
- Current log file: %s

## Shutdown Information
- Shutdown initiated: %s
- Shutdown type: Graceful
- Status collection completed: %s
`, o.eventLog.GetCurrentLogFile(), time.Now().Format(time.RFC3339), time.Now().Format(time.RFC3339))

	return status
}

// StatusAgentWrapper wraps regular agents to provide status functionality
type StatusAgentWrapper struct {
	dispatch.Agent
}

func (w *StatusAgentWrapper) GenerateStatus() (string, error) {
	agentID := w.Agent.GetID()

	status := fmt.Sprintf(`# %s Agent Status Report

Generated: %s

## Agent Information
- Agent ID: %s
- Agent Type: %s
- Status: Active

## Statistics
- Messages processed: Available in event log
- Last activity: %s

## Current State
- Ready for shutdown: Yes
- Active tasks: None
- Resources: All released

## Notes
Agent has been notified of shutdown and is ready to terminate.
`, agentID, time.Now().Format(time.RFC3339), agentID, getAgentType(agentID), time.Now().Format(time.RFC3339))

	return status, nil
}

// StartWebUI starts the web UI server
func (o *Orchestrator) StartWebUI(ctx context.Context, port int) error {
	o.logger.Info("Starting web UI server on port %d", port)

	// Create state store for web UI to use
	stateStore, err := state.NewStore(filepath.Join(o.workDir, "state"))
	if err != nil {
		return fmt.Errorf("failed to create state store for web UI: %w", err)
	}

	// Create web UI server
	o.webServer = webui.NewServer(o.dispatcher, stateStore, o.workDir)

	// Start server in background
	go func() {
		if err := o.webServer.StartServer(ctx, port); err != nil {
			o.logger.Error("Web UI server error: %v", err)
		}
	}()

	o.logger.Info("Web UI server started successfully")
	return nil
}

// openBrowser opens the default browser to the given URL
func openBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "windows":
		cmd = "cmd"
		args = []string{"/c", "start", url}
	case "darwin":
		cmd = "open"
		args = []string{url}
	default: // "linux", "freebsd", "openbsd", "netbsd"
		cmd = "xdg-open"
		args = []string{url}
	}

	return exec.Command(cmd, args...).Start()
}

func getAgentType(agentID string) string {
	switch agentID {
	case "architect":
		return "Story Processing & Task Generation"
	case "claude":
		return "Code Generation & Implementation"
	default:
		return "Unknown"
	}
}
