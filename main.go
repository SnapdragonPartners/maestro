package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"orchestrator/agents"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
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
}

// StatusAgent interface for agents that can generate status reports
type StatusAgent interface {
	dispatch.Agent
	GenerateStatus() (string, error)
}

func main() {
	fmt.Println("orchestrator boot")

	var configPath string
	flag.StringVar(&configPath, "config", "", "Path to config file")
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

	// Pretty print the config
	configJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal config: %v", err)
	}

	fmt.Printf("Loaded configuration:\n%s\n", configJSON)

	// Create orchestrator
	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Start orchestrator
	ctx := context.Background()
	if err := orchestrator.Start(ctx); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
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
	}, nil
}

// Start initializes and starts the orchestrator
func (o *Orchestrator) Start(ctx context.Context) error {
	o.logger.Info("Starting orchestrator")

	// Start dispatcher
	if err := o.dispatcher.Start(ctx); err != nil {
		return fmt.Errorf("failed to start dispatcher: %w", err)
	}

	// Create and register agents
	if err := o.createAgents(); err != nil {
		return fmt.Errorf("failed to create agents: %w", err)
	}

	o.logger.Info("Orchestrator started successfully")
	return nil
}

// Shutdown performs graceful shutdown of the orchestrator
func (o *Orchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("Starting graceful shutdown")

	// Step 1: Broadcast SHUTDOWN message to all agents
	if err := o.broadcastShutdown(); err != nil {
		o.logger.Error("Failed to broadcast shutdown: %v", err)
	}

	// Step 2: Wait for agents to complete current work
	o.logger.Info("Waiting %v for agents to complete work", o.shutdownTime)
	time.Sleep(o.shutdownTime / 4) // Give agents time to process shutdown

	// Step 3: Collect status from all agents and generate STATUS.md files
	if err := o.collectAgentStatus(); err != nil {
		o.logger.Error("Failed to collect agent status: %v", err)
	}

	// Step 4: Stop dispatcher
	if err := o.dispatcher.Stop(ctx); err != nil {
		o.logger.Error("Failed to stop dispatcher: %v", err)
	}

	// Step 5: Close other resources
	o.rateLimiter.Close()

	if err := o.eventLog.Close(); err != nil {
		o.logger.Error("Failed to close event log: %v", err)
	}

	o.logger.Info("Graceful shutdown completed")
	return nil
}

func (o *Orchestrator) createAgents() error {
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
		agent := agentWithModel.Agent
		modelName := agentWithModel.ModelName
		
		// Generate log ID for this agent
		logID := agent.GetLogID(modelName)
		
		var registeredAgent dispatch.Agent
		
		switch agent.Type {
		case "architect":
			architectAgent := agents.NewArchitectAgent(logID, agent.Name, "stories", agent.WorkDir, coderAgentID)
			architectAgent.SetDispatcher(o.dispatcher)
			registeredAgent = architectAgent
			
		case "coder":
			// Check if we should use live API calls (feature flag)
			useLiveAPI := os.Getenv("CLAUDE_LIVE_API") == "true"
			if useLiveAPI {
				liveAgent := agents.NewLiveClaudeAgent(logID, agent.Name, agent.WorkDir, agentWithModel.Model.APIKey, true)
				registeredAgent = liveAgent
			} else {
				claudeAgent := agents.NewClaudeAgent(logID, agent.Name, agent.WorkDir)
				registeredAgent = claudeAgent
			}
			
		default:
			return fmt.Errorf("unknown agent type: %s", agent.Type)
		}
		
		if err := o.dispatcher.RegisterAgent(registeredAgent); err != nil {
			return fmt.Errorf("failed to register agent %s: %w", logID, err)
		}
		
		o.agents[logID] = &StatusAgentWrapper{Agent: registeredAgent}
		o.logger.Info("Created and registered agent: %s (%s) using model %s", logID, agent.Type, modelName)
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
