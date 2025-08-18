package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"orchestrator/internal/kernel"
	"orchestrator/internal/supervisor"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/architect"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
)

// FlowRunner interface defines the common behavior for orchestrator flows.
type FlowRunner interface {
	Run(ctx context.Context, k *kernel.Kernel) error
}

// BootstrapFlow handles single-spec execution with termination.
// This consolidates the bootstrap logic from bootstrap.go.
type BootstrapFlow struct {
	factory  *AgentFactory
	gitRepo  string
	specFile string
}

// NewBootstrapFlow creates a new bootstrap flow.
func NewBootstrapFlow(gitRepo, specFile string) *BootstrapFlow {
	return &BootstrapFlow{
		gitRepo:  gitRepo,
		specFile: specFile,
		factory:  NewAgentFactory(),
	}
}

// Run executes the bootstrap flow.
func (f *BootstrapFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting bootstrap flow")

	// Create supervisor for agent lifecycle management
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Load and inject spec content FIRST to complete interactive setup
	k.Logger.Info("📝 Starting interactive bootstrap setup...")
	specContent, err := f.loadSpecContent(ctx)
	if err != nil {
		return fmt.Errorf("failed to load spec content: %w", err)
	}

	// Get updated configuration after interactive setup
	updatedConfig, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get updated config: %w", err)
	}

	// Create agent configuration with updated git settings
	agentConfig, err := createAgentConfig(&updatedConfig, ".")
	if err != nil {
		return fmt.Errorf("failed to create agent config: %w", err)
	}

	// Create agent set with updated configuration
	agentSet, err := f.factory.CreateAgentSet(ctx, agentConfig, k.Dispatcher, &updatedConfig, k.PersistenceChannel, k.BuildService)
	if err != nil {
		return fmt.Errorf("failed to create agent set: %w", err)
	}

	k.Logger.Info("✅ Created agent set with architect and %d coders", len(agentSet.Coders))

	// Register and start agents with updated configuration
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), agentSet.Architect)
	for i, coder := range agentSet.Coders {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coder)
	}

	// Inject spec into architect
	if specErr := InjectSpec(k.Dispatcher, "bootstrap", specContent); specErr != nil {
		return fmt.Errorf("failed to inject spec content: %w", specErr)
	}

	k.Logger.Info("📝 Injected bootstrap spec into architect")

	// Wait for architect completion
	finalState, err := f.waitForArchitectCompletion(ctx, agentSet.Architect)
	if err != nil {
		return fmt.Errorf("bootstrap failed: %w", err)
	}

	k.Logger.Info("🎉 Bootstrap completed with architect in %s state", finalState)
	return nil
}

// loadSpecContent loads the specification content for bootstrap.
func (f *BootstrapFlow) loadSpecContent(ctx context.Context) ([]byte, error) {
	if f.specFile != "" {
		// Load from file
		content, err := os.ReadFile(f.specFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read spec file: %w", err)
		}
		return content, nil
	}

	// Run interactive bootstrap setup (implemented in interactive_bootstrap.go)
	return f.runInteractiveBootstrapSetup(ctx)
}

// waitForArchitectCompletion waits for the architect to reach a terminal state.
// This preserves the existing waitForArchitectCompletion logic.
func (f *BootstrapFlow) waitForArchitectCompletion(ctx context.Context, architect *architect.Driver) (proto.State, error) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return proto.StateError, fmt.Errorf("context cancelled while waiting for architect")

		case <-ticker.C:
			currentState := architect.GetCurrentState()

			// Check for terminal states
			if currentState == proto.StateDone {
				return proto.StateDone, nil
			}

			if currentState == proto.StateError {
				return proto.StateError, fmt.Errorf("architect reached ERROR state")
			}

			// Continue waiting for non-terminal states
		}
	}
}

// OrchestratorFlow handles long-running multi-spec processing.
// This consolidates the main orchestrator logic from main.go.
type OrchestratorFlow struct {
	factory  *AgentFactory
	specFile string
	webUI    bool
}

// NewMainFlow creates a new main flow.
func NewMainFlow(specFile string, webUI bool) *OrchestratorFlow {
	return &OrchestratorFlow{
		specFile: specFile,
		webUI:    webUI,
		factory:  NewAgentFactory(),
	}
}

// Run executes the main flow.
func (f *OrchestratorFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting main flow")

	// Start web UI if requested
	if f.webUI {
		if err := k.StartWebUI(8080); err != nil {
			return fmt.Errorf("failed to start web UI: %w", err)
		}
		k.Logger.Info("🌐 Web UI started on port 8080")
	}

	// Create supervisor for agent lifecycle management
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create agent configuration
	agentConfig, err := createAgentConfig(k.Config, ".")
	if err != nil {
		return fmt.Errorf("failed to create agent config: %w", err)
	}

	// Create agent set
	agentSet, err := f.factory.CreateAgentSet(ctx, agentConfig, k.Dispatcher, k.Config, k.PersistenceChannel, k.BuildService)
	if err != nil {
		return fmt.Errorf("failed to create agent set: %w", err)
	}

	// Register agents with supervisor
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), agentSet.Architect)
	for i, coder := range agentSet.Coders {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coder)
	}

	k.Logger.Info("✅ Created agent set with architect and %d coders", len(agentSet.Coders))

	// Handle initial spec if provided
	if f.specFile != "" {
		specContent, err := os.ReadFile(f.specFile)
		if err != nil {
			return fmt.Errorf("failed to read spec file: %w", err)
		}

		if err := InjectSpec(k.Dispatcher, "cli", specContent); err != nil {
			return fmt.Errorf("failed to inject initial spec: %w", err)
		}

		k.Logger.Info("📝 Injected initial spec from file: %s", f.specFile)
	}

	// Enter main event loop
	k.Logger.Info("🚀 Main orchestrator running - ready for specs")

	// Wait for context cancellation (Ctrl+C, etc.)
	<-ctx.Done()
	k.Logger.Info("📴 Main flow shutting down due to context cancellation")

	return nil
}

// InjectSpec provides centralized spec injection into the dispatcher.
// This consolidates the spec injection logic that was duplicated across flows.
func InjectSpec(dispatcher *dispatch.Dispatcher, source string, content []byte) error {
	// Create spec message using the existing protocol (matches bootstrap.go pattern)
	msg := proto.NewAgentMsg(proto.MsgTypeSPEC, source, string(agent.TypeArchitect))
	msg.SetPayload("spec_content", string(content))
	msg.SetPayload("type", "spec_content") // Preserve existing protocol
	msg.SetMetadata("source", source)

	// Send via dispatcher (matches bootstrap.go pattern)
	if err := dispatcher.DispatchMessage(msg); err != nil {
		return fmt.Errorf("failed to dispatch spec message: %w", err)
	}
	return nil
}
