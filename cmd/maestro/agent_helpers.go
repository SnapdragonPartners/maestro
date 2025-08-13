package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"orchestrator/pkg/architect"
	"orchestrator/pkg/build"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// AgentConfig represents the configuration for creating agents.
//
//nolint:govet // struct alignment optimization not critical for this type
type AgentConfig struct {
	NumCoders       int
	ArchitectModel  *config.Model
	CoderModel      *config.Model
	WorkDir         string
	RepoURL         string
	TargetBranch    string
	MirrorDir       string
	BranchPattern   string
	WorktreePattern string
}

// AgentSet holds the created agent instances.
type AgentSet struct {
	Architect *architect.Driver
	Coders    []dispatch.Agent // Store as interface - we'll use reflection if needed
	AgentIDs  []string         // For cleanup tracking
}

// createAgentSet creates and initializes architect and coder agents.
//
//nolint:unused // will be used in bootstrap flow implementation
func createAgentSet(ctx context.Context, config *AgentConfig, dispatcher *dispatch.Dispatcher, orchestratorConfig *config.Config, persistenceChannel chan<- *persistence.Request, buildService *build.Service) (*AgentSet, error) {
	agentSet := &AgentSet{
		Coders:   make([]dispatch.Agent, 0, config.NumCoders),
		AgentIDs: make([]string, 0, config.NumCoders+1),
	}

	// Create architect agent
	architectID := "architect-001"
	agentSet.AgentIDs = append(agentSet.AgentIDs, architectID)

	architectWorkDir := filepath.Join(config.WorkDir, strings.ReplaceAll(architectID, ":", "-"))

	// Use the new DRY pattern - let architect create its own LLM client
	architectDriver, err := architect.NewArchitect(architectID, config.ArchitectModel, dispatcher, architectWorkDir, orchestratorConfig, persistenceChannel)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect: %w", err)
	}
	if err := architectDriver.Initialize(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize architect: %w", err)
	}

	agentSet.Architect = architectDriver

	// Create coder agents
	for i := 0; i < config.NumCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		agentSet.AgentIDs = append(agentSet.AgentIDs, coderID)

		coderWorkDir := filepath.Join(config.WorkDir, strings.ReplaceAll(coderID, ":", "-"))

		// Create clone manager for Git clone support
		gitRunner := coder.NewDefaultGitRunner()
		cloneManager := coder.NewCloneManager(
			gitRunner,
			config.WorkDir, // Use project workdir for shared mirror
			config.RepoURL,
			config.TargetBranch,
			config.MirrorDir,
			config.BranchPattern,
		)

		// Create coder with Claude LLM integration
		coderAgent, err := coder.NewCoder(coderID, coderID, coderWorkDir, config.CoderModel, os.Getenv("ANTHROPIC_API_KEY"), cloneManager, buildService)
		if err != nil {
			// Clean up any already created agents
			_ = cleanupAgents(agentSet)
			return nil, fmt.Errorf("failed to create coder agent %s: %w", coderID, err)
		}

		// Initialize the coder
		if err := coderAgent.Initialize(ctx); err != nil {
			_ = cleanupAgents(agentSet)
			return nil, fmt.Errorf("failed to initialize coder %s: %w", coderID, err)
		}

		agentSet.Coders = append(agentSet.Coders, coderAgent)
	}

	// Attach all agents to dispatcher
	dispatcher.Attach(agentSet.Architect)
	for _, coder := range agentSet.Coders {
		dispatcher.Attach(coder)
	}

	// Start agent state machines
	go func() {
		if err := agentSet.Architect.Run(ctx); err != nil {
			// Error handling - architect failure is critical
			fmt.Printf("ERROR: Architect state machine failed: %v\n", err)
		}
	}()

	for _, coderAgent := range agentSet.Coders {
		go func(c dispatch.Agent) {
			// Use MustAssert for type-safe access to Run() method
			runner := utils.MustAssert[*coder.Coder](c, "coder agent")
			if err := runner.Run(ctx); err != nil {
				// Error handling - coder failures are logged but not critical
				fmt.Printf("WARNING: Coder state machine failed: %v\n", err)
			}
		}(coderAgent)
	}

	return agentSet, nil
}

// waitForArchitectCompletion waits for the architect to reach DONE or ERROR state.
//
//nolint:unused // will be used in bootstrap flow implementation
func waitForArchitectCompletion(ctx context.Context, architect *architect.Driver) (proto.State, error) {
	// Set up a state notification channel to monitor architect state
	stateNotifCh := make(chan *proto.StateChangeNotification, 10)
	architect.SetStateNotificationChannel(stateNotifCh)

	for {
		select {
		case notification := <-stateNotifCh:
			if notification.AgentID == architect.GetID() {
				switch notification.ToState {
				case proto.StateDone:
					return proto.StateDone, nil
				case proto.StateError:
					return proto.StateError, fmt.Errorf("architect entered ERROR state")
				}
			}
		case <-ctx.Done():
			return proto.StateError, fmt.Errorf("context cancelled while waiting for architect completion: %w", ctx.Err())
		}
	}
}

// cleanupAgents gracefully shuts down and cleans up agent resources.
//
//nolint:unused,unparam // will be used in bootstrap flow implementation
func cleanupAgents(agentSet *AgentSet) error {
	if agentSet == nil {
		return nil
	}

	// Cleanup is handled by context cancellation - agents will stop when their Run() contexts are cancelled
	// No explicit cleanup needed

	return nil
}
