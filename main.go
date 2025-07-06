package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
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

	// Pull-based polling control
	pollingCancel context.CancelFunc
	pollingWg     sync.WaitGroup
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

// ArchitectMessageAdapter allows the architect driver to receive messages while running workflows
type ArchitectMessageAdapter struct {
	driver *architect.Driver
	id     string
}

// GetID implements dispatch.Agent
func (a *ArchitectMessageAdapter) GetID() string {
	return a.id
}

// ProcessMessage implements dispatch.Agent - forwards messages to architect's question/review handlers
func (a *ArchitectMessageAdapter) ProcessMessage(ctx context.Context, msg *proto.AgentMsg) (*proto.AgentMsg, error) {
	switch msg.Type {
	case proto.MsgTypeQUESTION:
		// Forward to question handler
		return a.driver.HandleQuestion(ctx, msg)
	case proto.MsgTypeRESULT:
		// Forward to review evaluator
		return a.driver.HandleResult(ctx, msg)
	case proto.MsgTypeREQUEST:
		// Route to appropriate worker (review worker for approval requests)
		if err := a.driver.RouteMessage(msg); err != nil {
			return nil, fmt.Errorf("failed to route request message: %w", err)
		}
		// REQUEST messages are handled asynchronously by workers, no immediate response
		return nil, nil
	default:
		return nil, fmt.Errorf("architect cannot process message type: %s", msg.Type)
	}
}

// Shutdown implements dispatch.Agent
func (a *ArchitectMessageAdapter) Shutdown(ctx context.Context) error {
	return nil // Architect driver doesn't need special shutdown for messages
}

func main() {
	fmt.Println("orchestrator boot")

	var configPath string
	var specPath string
	var liveMode bool
	flag.StringVar(&configPath, "config", "", "Path to config file")
	flag.StringVar(&specPath, "spec", "", "Path to specification file (markdown)")
	flag.BoolVar(&liveMode, "live", false, "Use live API calls instead of mock mode")
	flag.Parse()

	// Use CONFIG_PATH env var if flag not provided
	if configPath == "" {
		configPath = os.Getenv("CONFIG_PATH")
	}

	// Default to config/config.json
	if configPath == "" {
		configPath = "config/config.json"
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Config loaded successfully (no need to print it)
	_ = cfg

	// Create orchestrator
	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Start orchestrator
	ctx := context.Background()
	if err := orchestrator.Start(ctx, liveMode); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}

	// If spec file provided, initiate architect workflow
	if specPath != "" {
		if err := orchestrator.ProcessSpecification(ctx, specPath); err != nil {
			log.Fatalf("Failed to process specification: %v", err)
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
func NewOrchestrator(cfg *config.Config) (*Orchestrator, error) {
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

	// Start polling workers for pull-based architecture
	if err := o.startPollingWorkers(ctx); err != nil {
		return fmt.Errorf("failed to start polling workers: %w", err)
	}

	o.logger.Info("Orchestrator started successfully")
	return nil
}

// Shutdown performs graceful shutdown of the orchestrator
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("Starting graceful shutdown")

	// Step 1: Stop polling workers
	if o.pollingCancel != nil {
		o.pollingCancel()
		o.logger.Info("Waiting for polling workers to stop...")
		o.pollingWg.Wait()
		o.logger.Info("Polling workers stopped")
	}

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

// ProcessSpecification runs the complete architect workflow for a specification file
func (o *Orchestrator) ProcessSpecification(ctx context.Context, specFile string) error {
	if o.architect == nil {
		return fmt.Errorf("no architect configured - check config file has architect agent")
	}

	o.logger.Info("Processing specification file: %s", specFile)
	fmt.Printf("🚀 Starting architect workflow...\n")

	// Initialize architect driver
	fmt.Printf("📋 Initializing architect driver...\n")
	if err := o.architect.Initialize(ctx); err != nil {
		return fmt.Errorf("failed to initialize architect: %w", err)
	}
	fmt.Printf("✅ Architect driver initialized\n")

	// Process the complete workflow from spec to dispatched tasks
	fmt.Printf("📝 Processing workflow from spec...\n")
	if err := o.architect.ProcessWorkflow(ctx, specFile); err != nil {
		return fmt.Errorf("workflow processing failed: %w", err)
	}

	fmt.Printf("🎉 Architect workflow completed successfully!\n")
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

			// Create architect's own state store in its workdir (not global state)
			architectStateStore, err := state.NewStore(filepath.Join(agentConfig.WorkDir, "state"))
			if err != nil {
				return fmt.Errorf("failed to create architect state store: %w", err)
			}

			baseLLMClient := agent.NewO3ClientWithModel(agentWithModel.Model.APIKey, "o3-mini")
			llmClient := &LLMClientAdapter{client: baseLLMClient}
			o.architect = architect.NewDriverWithDispatcher(logID, architectStateStore, &agentWithModel.Model, llmClient, o.dispatcher, agentConfig.WorkDir, filepath.Join(agentConfig.WorkDir, "stories"))

			// Create an adapter that allows the architect to receive messages
			architectAgent := &ArchitectMessageAdapter{
				driver: o.architect,
				id:     logID,
			}
			registeredAgent = architectAgent
			o.logger.Info("Created architect driver with live O3 LLM: %s", logID)

		case "coder":
			// Create individual state store for this agent in its work directory
			agentStateStore, err := state.NewStore(filepath.Join(agentConfig.WorkDir, "state"))
			if err != nil {
				return fmt.Errorf("failed to create state store for agent %s: %w", logID, err)
			}

			// Check if we should use live API calls (command line flag)
			if liveMode {
				// Use unified coder with Claude LLM integration
				liveAgent, err := coder.NewCoderWithClaude(logID, agentConfig.Name, agentConfig.WorkDir, agentStateStore, &agentWithModel.Model, agentWithModel.Model.APIKey)
				if err != nil {
					return fmt.Errorf("failed to create live coder agent %s: %w", logID, err)
				}
				registeredAgent = liveAgent
			} else {
				// Use unified coder with core state machine (mock mode)
				driverAgent, err := coder.NewCoder(logID, agentConfig.Name, agentConfig.WorkDir, agentStateStore, &agentWithModel.Model)
				if err != nil {
					return fmt.Errorf("failed to create coder agent %s: %w", logID, err)
				}
				registeredAgent = driverAgent
			}

		default:
			return fmt.Errorf("unknown agent type: %s", agentConfig.Type)
		}

		if err := o.dispatcher.RegisterAgent(registeredAgent); err != nil {
			return fmt.Errorf("failed to register agent %s: %w", logID, err)
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

// startPollingWorkers starts the pull-based polling workers for agents
func (o *Orchestrator) startPollingWorkers(ctx context.Context) error {
	// Create a cancellable context for polling workers
	pollingCtx, cancel := context.WithCancel(ctx)
	o.pollingCancel = cancel

	// Start architect polling worker for REQUEST messages from coders
	o.pollingWg.Add(1)
	go o.architectPollingWorker(pollingCtx)
	o.logger.Info("Started architect polling worker")

	// Start coder polling workers for each coder agent
	for agentID, agent := range o.agents {
		if o.isCoderAgent(agentID) {
			o.pollingWg.Add(1)
			go o.coderPollingWorker(pollingCtx, agentID, agent)
			o.logger.Info("Started coder polling worker for agent: %s", agentID)
		}
	}

	return nil
}

// isCoderAgent checks if an agent ID represents a coder agent
func (o *Orchestrator) isCoderAgent(agentID string) bool {
	// Coder agents have IDs like "claude_sonnet4:001"
	return !strings.Contains(agentID, "openai_o3") && agentID != "architect"
}

// architectPollingWorker polls for questions that need architect attention
func (o *Orchestrator) architectPollingWorker(ctx context.Context) {
	defer o.pollingWg.Done()

	ticker := time.NewTicker(500 * time.Millisecond) // Poll every 500ms
	defer ticker.Stop()

	o.logger.Debug("Architect polling worker started")

	// Get the architect adapter from registered agents
	var architectAgent dispatch.Agent
	for agentID, agent := range o.agents {
		if strings.Contains(agentID, "openai_o3") {
			if statusAgent, ok := agent.(*StatusAgentWrapper); ok {
				architectAgent = statusAgent.Agent
			} else {
				architectAgent = agent
			}
			break
		}
	}

	if architectAgent == nil {
		o.logger.Error("No architect agent found for polling worker")
		return
	}

	for {
		select {
		case <-ctx.Done():
			o.logger.Debug("Architect polling worker stopped")
			return
		case <-ticker.C:
			// Pull questions from the architect queue
			if question := o.dispatcher.PullArchitectWork(); question != nil {
				o.logger.Debug("Architect polling worker processing question: %s", question.ID)

				// Process the question through the architect agent
				if response, err := architectAgent.ProcessMessage(ctx, question); err != nil {
					o.logger.Error("Architect failed to process question %s: %v", question.ID, err)
					// Send error response
					errorMsg := proto.NewAgentMsg(proto.MsgTypeERROR, "architect", question.FromAgent)
					errorMsg.ParentMsgID = question.ID
					errorMsg.SetPayload("error", err.Error())
					if dispatchErr := o.dispatcher.DispatchMessage(errorMsg); dispatchErr != nil {
						o.logger.Error("Failed to dispatch error response: %v", dispatchErr)
					}
				} else if response != nil {
					// Send the architect's response back through the dispatcher
					if err := o.dispatcher.DispatchMessage(response); err != nil {
						o.logger.Error("Failed to dispatch architect response: %v", err)
					}
				}
			}
		}
	}
}

// coderPollingWorker polls for work and feedback for a specific coder agent
func (o *Orchestrator) coderPollingWorker(ctx context.Context, agentID string, agent StatusAgent) {
	defer o.pollingWg.Done()

	ticker := time.NewTicker(1 * time.Second) // Poll every 1 second for coders
	defer ticker.Stop()

	o.logger.Debug("Coder polling worker started for agent: %s", agentID)

	// Track agent state - assume READY initially
	agentState := "READY"

	for {
		select {
		case <-ctx.Done():
			o.logger.Debug("Coder polling worker stopped for agent: %s", agentID)
			return
		case <-ticker.C:
			// Debug: Log agent state and ID (append mode)
			debugMsg := fmt.Sprintf("DEBUG: Polling agent %s in state %s\n", agentID, agentState)
			if f, err := os.OpenFile("polling_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
				f.WriteString(debugMsg)
				f.Close()
			}
			
			switch agentState {
			case "READY":
				// When READY, poll for new tasks from shared work queue
				if task := o.dispatcher.PullSharedWork(); task != nil {
					o.logger.Debug("Coder %s polling worker processing task: %s", agentID, task.ID)
					agentState = "WORKING"

					// Process the task
					go func() {
						if response, err := agent.ProcessMessage(ctx, task); err != nil {
							o.logger.Error("Coder %s failed to process task %s: %v", agentID, task.ID, err)
							agentState = "READY" // Reset to ready on error
						} else if response != nil {
							// Task completed, send response and look for feedback
							if err := o.dispatcher.DispatchMessage(response); err != nil {
								o.logger.Error("Failed to dispatch coder response: %v", err)
							}
							// Agent is now waiting for feedback, state remains WORKING
						} else {
							// No response, task completed
							agentState = "READY"
						}
					}()
				}

			case "WORKING":
				// When WORKING, poll for feedback from architect
				if feedback := o.dispatcher.PullCoderFeedback(agentID); feedback != nil {
					o.logger.Debug("Coder %s polling worker processing feedback: %s", agentID, feedback.ID)
					
					// Debug: Write to file to confirm feedback is being pulled
					debugMsg := fmt.Sprintf("DEBUG: Pulled feedback for %s - type=%s, id=%s\n", agentID, feedback.Type, feedback.ID)
					os.WriteFile("feedback_debug.log", []byte(debugMsg), 0644)

					// Process the feedback
					go func() {
						if response, err := agent.ProcessMessage(ctx, feedback); err != nil {
							o.logger.Error("Coder %s failed to process feedback %s: %v", agentID, feedback.ID, err)
						} else if response != nil {
							// Send response back to architect
							if err := o.dispatcher.DispatchMessage(response); err != nil {
								o.logger.Error("Failed to dispatch coder feedback response: %v", err)
							}
						}
						// After processing feedback, agent is ready for new work
						agentState = "READY"
					}()
				}
			}
		}
	}
}
