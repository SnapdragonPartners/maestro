package main

import (
	"context"
	"fmt"
	"path/filepath"

	"orchestrator/pkg/architect"
	"orchestrator/pkg/build"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/persistence"
)

// AgentConfig represents the configuration for creating agents.
// This preserves the existing structure from agent_helpers.go.
type AgentConfig struct {
	ArchitectModel  *config.Model
	CoderModel      *config.Model
	WorkDir         string
	RepoURL         string
	TargetBranch    string
	MirrorDir       string
	BranchPattern   string
	WorktreePattern string
	NumCoders       int
}

// AgentSet holds the created agent instances.
// This preserves the existing structure from agent_helpers.go.
type AgentSet struct {
	Architect *architect.Driver
	Coders    []dispatch.Agent // Store as interface - we'll use reflection if needed
	AgentIDs  []string         // For cleanup tracking
}

// AgentFactory wraps the existing createAgentSet functionality in a clean pattern.
type AgentFactory struct{}

// NewAgentFactory creates a new agent factory.
func NewAgentFactory() *AgentFactory {
	return &AgentFactory{}
}

// CreateAgentSet creates and initializes architect and coder agents.
// This wraps the existing createAgentSet logic from agent_helpers.go.
func (f *AgentFactory) CreateAgentSet(_ context.Context, cfg *AgentConfig, dispatcher *dispatch.Dispatcher, orchestratorConfig *config.Config, persistenceChannel chan<- *persistence.Request, buildService *build.Service) (*AgentSet, error) {
	agentSet := &AgentSet{
		Coders:   make([]dispatch.Agent, 0, cfg.NumCoders),
		AgentIDs: make([]string, 0, cfg.NumCoders+1),
	}

	// Create architect agent
	architectID := "architect-001"
	architect, err := architect.NewArchitect(
		architectID,
		cfg.ArchitectModel,
		dispatcher,
		cfg.WorkDir,
		orchestratorConfig,
		persistenceChannel,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect: %w", err)
	}

	agentSet.Architect = architect
	agentSet.AgentIDs = append(agentSet.AgentIDs, architectID)

	// Attach architect to dispatcher
	dispatcher.Attach(architect)

	// Create coder agents
	for i := 0; i < cfg.NumCoders; i++ {
		coderID := fmt.Sprintf("coder-%03d", i+1)

		// Each coder gets its own work directory within the project directory
		// This matches the original pattern: filepath.Join(projectDir, fsafeID)
		coderWorkDir := filepath.Join(cfg.WorkDir, coderID)

		// Create clone manager for this coder
		gitRunner := coder.NewDefaultGitRunner()
		cloneManager := coder.NewCloneManager(
			gitRunner,
			cfg.WorkDir, // Clone manager uses project work dir for mirrors
			cfg.RepoURL,
			cfg.TargetBranch,
			cfg.MirrorDir,
			fmt.Sprintf(cfg.WorktreePattern, coderID),
		)

		// Create coder with individual work directory
		coderAgent, err := coder.NewCoder(
			coderID,
			coderID,      // Use same ID for both parameters
			coderWorkDir, // Use individual work directory, not project directory
			cfg.CoderModel,
			"", // Empty spec path - will be provided via messages
			cloneManager,
			buildService,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create coder %s: %w", coderID, err)
		}

		// Attach coder to dispatcher
		dispatcher.Attach(coderAgent)

		// Add to agent set
		agentSet.Coders = append(agentSet.Coders, coderAgent)
		agentSet.AgentIDs = append(agentSet.AgentIDs, coderID)
	}

	return agentSet, nil
}

// createAgentConfig creates agent configuration from project settings.
// This consolidates the config creation logic from the old bootstrap and main flows.
func createAgentConfig(cfg *config.Config, workDir string) (*AgentConfig, error) {
	// Get architect model
	architectModel, err := getModelByName(cfg, cfg.Agents.ArchitectModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get architect model: %w", err)
	}

	// Get coder model
	coderModel, err := getModelByName(cfg, cfg.Agents.CoderModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get coder model: %w", err)
	}

	// Extract repository info
	repoURL := ""
	targetBranch := "main"
	if cfg.Git != nil {
		repoURL = cfg.Git.RepoURL
		if cfg.Git.TargetBranch != "" {
			targetBranch = cfg.Git.TargetBranch
		}
	}

	return &AgentConfig{
		NumCoders:       cfg.Agents.MaxCoders,
		ArchitectModel:  architectModel,
		CoderModel:      coderModel,
		WorkDir:         workDir,
		RepoURL:         repoURL,
		TargetBranch:    targetBranch,
		MirrorDir:       ".mirrors",
		BranchPattern:   "maestro/story-{STORY_ID}",
		WorktreePattern: "maestro-story-%s",
	}, nil
}

// getModelByName finds a model configuration by name.
// This preserves the existing model lookup logic.
func getModelByName(cfg *config.Config, modelName string) (*config.Model, error) {
	for i := range cfg.Orchestrator.Models {
		model := &cfg.Orchestrator.Models[i]
		if model.Name == modelName {
			return model, nil
		}
	}
	return nil, fmt.Errorf("model %s not found in configuration", modelName)
}
