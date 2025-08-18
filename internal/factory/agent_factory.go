// Package factory provides agent creation and management functionality for the orchestrator.
package factory

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
type AgentSet struct {
	Architect *architect.Driver
	Coders    []dispatch.Agent // Store as interface - we'll use reflection if needed
	AgentIDs  []string         // For cleanup tracking
}

// AgentFactory creates and manages agent instances.
type AgentFactory struct {
	dispatcher         *dispatch.Dispatcher
	persistenceChannel chan<- *persistence.Request
	buildService       *build.Service
}

// NewAgentFactory creates a new agent factory.
func NewAgentFactory(dispatcher *dispatch.Dispatcher, persistenceChannel chan<- *persistence.Request, buildService *build.Service) *AgentFactory {
	return &AgentFactory{
		dispatcher:         dispatcher,
		persistenceChannel: persistenceChannel,
		buildService:       buildService,
	}
}

// CreateAgentSet creates and initializes architect and coder agents.
func (f *AgentFactory) CreateAgentSet(ctx context.Context, cfg *AgentConfig) (*AgentSet, error) {
	// Get current config from singleton
	orchestratorConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	agentSet := &AgentSet{
		Coders:   make([]dispatch.Agent, 0, cfg.NumCoders),
		AgentIDs: make([]string, 0, cfg.NumCoders+1),
	}

	// Create architect agent
	architectID := "architect-001"
	architect, err := architect.NewArchitect(
		ctx,
		architectID,
		cfg.ArchitectModel,
		f.dispatcher,
		cfg.WorkDir,
		&orchestratorConfig,
		f.persistenceChannel,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create architect: %w", err)
	}

	agentSet.Architect = architect
	agentSet.AgentIDs = append(agentSet.AgentIDs, architectID)

	// Attach architect to dispatcher
	f.dispatcher.Attach(architect)

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
			ctx,
			coderID,
			coderWorkDir, // Use individual work directory, not project directory
			cfg.CoderModel,
			cloneManager,
			f.buildService,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to create coder %s: %w", coderID, err)
		}

		// Attach coder to dispatcher
		f.dispatcher.Attach(coderAgent)

		// Add to agent set
		agentSet.Coders = append(agentSet.Coders, coderAgent)
		agentSet.AgentIDs = append(agentSet.AgentIDs, coderID)
	}

	return agentSet, nil
}

// RecreateAgent recreates a single agent by type and ID.
func (f *AgentFactory) RecreateAgent(ctx context.Context, agentID, agentType string) (dispatch.Agent, error) {
	// Get current config from singleton
	orchestratorConfig, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Get current config to create agent config
	agentConfig, err := f.createAgentConfigFromCurrent()
	if err != nil {
		return nil, fmt.Errorf("failed to create agent config: %w", err)
	}

	switch agentType {
	case "architect":
		architect, err := architect.NewArchitect(
			ctx,
			agentID,
			agentConfig.ArchitectModel,
			f.dispatcher,
			agentConfig.WorkDir,
			&orchestratorConfig,
			f.persistenceChannel,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to recreate architect: %w", err)
		}

		// Attach to dispatcher
		f.dispatcher.Attach(architect)
		return architect, nil

	case "coder":
		// Extract coder number from ID (e.g., "coder-001" -> 1)
		coderWorkDir := filepath.Join(agentConfig.WorkDir, agentID)

		// Create clone manager for this coder
		gitRunner := coder.NewDefaultGitRunner()
		cloneManager := coder.NewCloneManager(
			gitRunner,
			agentConfig.WorkDir, // Clone manager uses project work dir for mirrors
			agentConfig.RepoURL,
			agentConfig.TargetBranch,
			agentConfig.MirrorDir,
			fmt.Sprintf(agentConfig.WorktreePattern, agentID),
		)

		// Create coder with individual work directory
		coderAgent, err := coder.NewCoder(
			ctx,
			agentID,
			coderWorkDir, // Use individual work directory, not project directory
			agentConfig.CoderModel,
			cloneManager,
			f.buildService,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to recreate coder %s: %w", agentID, err)
		}

		// Attach to dispatcher
		f.dispatcher.Attach(coderAgent)
		return coderAgent, nil

	default:
		return nil, fmt.Errorf("unknown agent type: %s", agentType)
	}
}

// createAgentConfigFromCurrent creates agent configuration from current orchestrator config.
func (f *AgentFactory) createAgentConfigFromCurrent() (*AgentConfig, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Get architect model
	architectModel, err := getModelByName(&cfg, cfg.Agents.ArchitectModel)
	if err != nil {
		return nil, fmt.Errorf("failed to get architect model: %w", err)
	}

	// Get coder model
	coderModel, err := getModelByName(&cfg, cfg.Agents.CoderModel)
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

	// For agent recreation, we use the existing work directory structure
	workDir := "work" // Default work directory used by orchestrator

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

// CreateAgentConfig creates agent configuration from project settings.
// This is used by external packages that need to create agent configurations.
func CreateAgentConfig(cfg *config.Config, workDir string) (*AgentConfig, error) {
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
func getModelByName(cfg *config.Config, modelName string) (*config.Model, error) {
	for i := range cfg.Orchestrator.Models {
		model := &cfg.Orchestrator.Models[i]
		if model.Name == modelName {
			return model, nil
		}
	}
	return nil, fmt.Errorf("model %s not found in configuration", modelName)
}
