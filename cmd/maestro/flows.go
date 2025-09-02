package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"orchestrator/internal/kernel"
	"orchestrator/internal/orchestrator"
	"orchestrator/internal/supervisor"
	"orchestrator/pkg/agent"
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
	gitRepo  string
	specFile string
}

// NewBootstrapFlow creates a new bootstrap flow.
func NewBootstrapFlow(gitRepo, specFile string) *BootstrapFlow {
	return &BootstrapFlow{
		gitRepo:  gitRepo,
		specFile: specFile,
	}
}

// Run executes the bootstrap flow.
func (f *BootstrapFlow) Run(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("Starting bootstrap flow")

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

	// Create supervisor for agent lifecycle management (creates its own factory)
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create and register architect agent
	architect, err := supervisor.GetFactory().NewAgent(ctx, "architect-001", string(agent.TypeArchitect))
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), architect)

	// Create and register coder agents based on config
	numCoders := updatedConfig.Agents.MaxCoders
	for i := 0; i < numCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderAgent, coderErr := supervisor.GetFactory().NewAgent(ctx, coderID, string(agent.TypeCoder))
		if coderErr != nil {
			return fmt.Errorf("failed to create coder %s: %w", coderID, coderErr)
		}
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coderAgent)
	}

	k.Logger.Info("✅ Created and registered architect and %d coders", numCoders)

	// Inject spec into architect
	if specErr := InjectSpec(k.Dispatcher, "bootstrap", specContent); specErr != nil {
		return fmt.Errorf("failed to inject spec content: %w", specErr)
	}

	k.Logger.Info("📝 Injected bootstrap spec into architect")

	// Wait for architect completion - use interface
	finalState, err := f.waitForArchitectCompletion(ctx, architect)
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

// StateProvider interface for agents that provide state information.
type StateProvider interface {
	GetCurrentState() proto.State
}

// waitForArchitectCompletion waits for the architect to reach a terminal state.
// This preserves the existing waitForArchitectCompletion logic.
func (f *BootstrapFlow) waitForArchitectCompletion(ctx context.Context, architect dispatch.Agent) (proto.State, error) {
	// Cast to StateProvider to get state information
	stateProvider, ok := architect.(StateProvider)
	if !ok {
		return proto.StateError, fmt.Errorf("architect does not implement StateProvider interface")
	}
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return proto.StateError, fmt.Errorf("context cancelled while waiting for architect")

		case <-ticker.C:
			currentState := stateProvider.GetCurrentState()

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
	specFile string
	webUI    bool
}

// NewMainFlow creates a new main flow.
func NewMainFlow(specFile string, webUI bool) *OrchestratorFlow {
	return &OrchestratorFlow{
		specFile: specFile,
		webUI:    webUI,
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

	// Create supervisor for agent lifecycle management (creates its own factory)
	supervisor := supervisor.NewSupervisor(k)

	// Start supervisor's state change processor
	supervisor.Start(ctx)

	// Create and register architect agent
	architect, err := supervisor.GetFactory().NewAgent(ctx, "architect-001", string(agent.TypeArchitect))
	if err != nil {
		return fmt.Errorf("failed to create architect: %w", err)
	}
	supervisor.RegisterAgent(ctx, "architect-001", string(agent.TypeArchitect), architect)

	// Create and register coder agents based on config
	numCoders := k.Config.Agents.MaxCoders
	for i := 0; i < numCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)
		coderAgent, coderErr := supervisor.GetFactory().NewAgent(ctx, coderID, string(agent.TypeCoder))
		if coderErr != nil {
			return fmt.Errorf("failed to create coder %s: %w", coderID, coderErr)
		}
		supervisor.RegisterAgent(ctx, coderID, string(agent.TypeCoder), coderAgent)
	}

	k.Logger.Info("✅ Created and registered architect and %d coders", numCoders)

	// Run startup orchestration (Story 3: rebuild + reconcile/rollback)
	if err := f.runStartupOrchestration(ctx, k); err != nil {
		return fmt.Errorf("startup orchestration failed: %w", err)
	}

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

// TODO: Temporarily disabled startup orchestration to debug crash
// runStartupOrchestration executes the startup rebuild + reconcile/rollback from Story 3.
func (f *OrchestratorFlow) runStartupOrchestration(ctx context.Context, k *kernel.Kernel) error {
	k.Logger.Info("🔧 Starting startup orchestration")

	// Determine project directory from kernel
	// For now, use current working directory - in future this could be configurable
	projectDir := "."

	// Create startup orchestrator
	startupOrch, err := orchestrator.NewStartupOrchestrator(projectDir)
	if err != nil {
		return fmt.Errorf("failed to create startup orchestrator: %w", err)
	}

	// Execute startup sequence
	if err := startupOrch.OnStart(ctx); err != nil {
		return fmt.Errorf("startup orchestration failed: %w", err)
	}

	k.Logger.Info("✅ Startup orchestration completed successfully")
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
